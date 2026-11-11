// Package grpcserver runs the gRPC server used for service-to-service communication with
// other ATLAS backends. RPC handlers are thin adapters over the same services the REST
// handlers use, so business logic is never duplicated per transport.
package grpcserver

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthgrpc "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"

	userv1 "atlas-backend-core/gen/atlas/user/v1"
	"atlas-backend-core/internal/config"
)

// minClientPingInterval is the most frequent keepalive ping the server tolerates from a
// client before closing the connection with GOAWAY.
const minClientPingInterval = 10 * time.Second

// Deps holds everything the gRPC server needs. It is built once at startup in cmd/api.
type Deps struct {
	Config      config.GRPCConfig
	Production  bool
	Logger      *slog.Logger
	UserService UserService
}

// Server is a gRPC server with standard health checking and graceful shutdown.
type Server struct {
	srv             *grpc.Server
	health          *health.Server
	services        []string
	addr            string
	log             *slog.Logger
	shutdownTimeout time.Duration
}

// New builds the gRPC server: interceptors (outermost first: request ID, recovery,
// logging, auth), message size limits, keepalive, optional TLS, the application services,
// the grpc.health.v1 service, and reflection outside production.
func New(deps Deps) (*Server, error) {
	cfg := deps.Config
	opts := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(
			requestIDUnary(deps.Logger),
			recoveryUnary(),
			loggingUnary(),
			authUnary(cfg.AuthToken),
		),
		grpc.ChainStreamInterceptor(
			requestIDStream(deps.Logger),
			recoveryStream(),
			loggingStream(),
			authStream(cfg.AuthToken),
		),
		grpc.MaxRecvMsgSize(cfg.MaxRecvMsgBytes),
		grpc.MaxSendMsgSize(cfg.MaxSendMsgBytes),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    cfg.KeepaliveTime,
			Timeout: cfg.KeepaliveTimeout,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             minClientPingInterval,
			PermitWithoutStream: true,
		}),
	}
	if cfg.TLSCertFile != "" {
		creds, err := credentials.NewServerTLSFromFile(cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load gRPC TLS key pair: %w", err)
		}
		opts = append(opts, grpc.Creds(creds))
	}
	if cfg.AuthToken == "" {
		deps.Logger.Warn("gRPC authentication is disabled because GRPC_AUTH_TOKEN is empty")
	}

	srv := grpc.NewServer(opts...)
	userv1.RegisterUserServiceServer(srv, NewUserServer(deps.UserService))

	hs := health.NewServer()
	healthgrpc.RegisterHealthServer(srv, hs)
	if !deps.Production {
		reflection.Register(srv)
	}

	s := &Server{
		srv:             srv,
		health:          hs,
		services:        []string{"", userv1.UserService_ServiceDesc.ServiceName},
		addr:            cfg.Addr(),
		log:             deps.Logger,
		shutdownTimeout: cfg.ShutdownTimeout,
	}
	s.setStatus(healthgrpc.HealthCheckResponse_NOT_SERVING)
	return s, nil
}

// Run listens on the configured address and serves until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", s.addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.addr, err)
	}
	return s.Serve(ctx, ln)
}

// Serve serves on ln until ctx is cancelled. It then reports NOT_SERVING, waits for
// in-flight RPCs to finish (GracefulStop), and forces a Stop once the shutdown timeout
// expires.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	errCh := make(chan error, 1)
	go func() { errCh <- s.srv.Serve(ln) }()
	s.setStatus(healthgrpc.HealthCheckResponse_SERVING)
	s.log.Info("grpc server started", slog.String("addr", ln.Addr().String()))

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("grpc serve: %w", err)
		}
		return nil
	case <-ctx.Done():
	}

	s.log.Info("shutting down grpc server", slog.Duration("timeout", s.shutdownTimeout))
	s.health.Shutdown()

	stopped := make(chan struct{})
	go func() {
		s.srv.GracefulStop()
		close(stopped)
	}()
	timer := time.NewTimer(s.shutdownTimeout)
	defer timer.Stop()
	select {
	case <-stopped:
	case <-timer.C:
		s.log.Warn("grpc graceful shutdown timed out, forcing stop")
		s.srv.Stop()
		<-stopped
	}
	s.log.Info("grpc server stopped")
	return nil
}

func (s *Server) setStatus(st healthgrpc.HealthCheckResponse_ServingStatus) {
	for _, name := range s.services {
		s.health.SetServingStatus(name, st)
	}
}
