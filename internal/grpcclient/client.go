// Package grpcclient creates connections to other backends' gRPC servers. Every call made
// through such a connection carries the current request ID ("x-request-id") and, when
// configured, the shared bearer token, so logs can be correlated across services.
//
// Typical use: create one connection per downstream service at startup, wrap it in the
// generated client (e.g. paymentv1.NewPaymentServiceClient(conn)), inject that client
// into the service that needs it, and Close the connection on shutdown.
package grpcclient

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"atlas-backend-core/internal/requestid"
)

// Config describes how to reach another backend's gRPC server.
type Config struct {
	// Target is the server address, e.g. "payments:9090" or "dns:///payments.internal:9090".
	Target string
	// AuthToken is sent as "authorization: Bearer <token>" on every call when non-empty.
	// Only send it over TLS or a private network.
	AuthToken string
	// TLS enables transport security using the system certificate pool.
	TLS bool
	// Timeout is applied to calls whose context has no deadline. Zero disables it.
	Timeout time.Duration
}

// New returns a client connection for cfg. The connection is established lazily on the
// first call. Extra dial options are appended after the defaults (useful in tests).
func New(cfg Config, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
	if cfg.Target == "" {
		return nil, errors.New("grpcclient: target is required")
	}

	creds := insecure.NewCredentials()
	if cfg.TLS {
		creds = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	}
	dialOpts := append([]grpc.DialOption{
		grpc.WithTransportCredentials(creds),
		grpc.WithChainUnaryInterceptor(timeoutUnary(cfg.Timeout), metadataUnary(cfg.AuthToken)),
		grpc.WithChainStreamInterceptor(metadataStream(cfg.AuthToken)),
	}, opts...)

	conn, err := grpc.NewClient(cfg.Target, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("grpcclient: connect to %s: %w", cfg.Target, err)
	}
	return conn, nil
}

// timeoutUnary applies a default deadline to calls that do not already have one.
func timeoutUnary(d time.Duration) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker, opts ...grpc.CallOption,
	) error {
		if _, ok := ctx.Deadline(); !ok && d > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, d)
			defer cancel()
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

func metadataUnary(token string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker, opts ...grpc.CallOption,
	) error {
		return invoker(outgoingContext(ctx, token), method, req, reply, cc, opts...)
	}
}

func metadataStream(token string) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string,
		streamer grpc.Streamer, opts ...grpc.CallOption,
	) (grpc.ClientStream, error) {
		return streamer(outgoingContext(ctx, token), desc, cc, method, opts...)
	}
}

// outgoingContext adds the request ID from ctx and the bearer token to outgoing metadata.
func outgoingContext(ctx context.Context, token string) context.Context {
	if id := requestid.FromContext(ctx); id != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, requestid.MetadataKey, id)
	}
	if token != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
	}
	return ctx
}
