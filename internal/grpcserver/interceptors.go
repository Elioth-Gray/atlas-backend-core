package grpcserver

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"log/slog"
	"runtime/debug"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"atlas-backend-core/internal/logger"
	"atlas-backend-core/internal/requestid"
)

const (
	authorizationKey   = "authorization"
	bearerPrefix       = "Bearer "
	healthMethodPrefix = "/grpc.health.v1.Health/"
)

// publicMethodPrefixes can be called without credentials: load balancer health probes and
// (outside production) reflection for grpcurl / grpcui.
var publicMethodPrefixes = []string{
	healthMethodPrefix,
	"/grpc.reflection.v1.ServerReflection/",
	"/grpc.reflection.v1alpha.ServerReflection/",
}

// wrappedStream lets stream interceptors replace the stream's context.
type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedStream) Context() context.Context { return w.ctx }

// --- request ID --------------------------------------------------------------------

// requestIDUnary attaches a request ID and the logger to the context. A valid incoming
// "x-request-id" is reused so logs can be correlated across services; otherwise a new ID
// is generated. The ID is echoed back in the response header metadata.
func requestIDUnary(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx, id := withRequestID(ctx, log)
		_ = grpc.SetHeader(ctx, metadata.Pairs(requestid.MetadataKey, id))
		return handler(ctx, req)
	}
}

func requestIDStream(log *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx, id := withRequestID(ss.Context(), log)
		_ = ss.SetHeader(metadata.Pairs(requestid.MetadataKey, id))
		return handler(srv, &wrappedStream{ServerStream: ss, ctx: ctx})
	}
}

func withRequestID(ctx context.Context, log *slog.Logger) (context.Context, string) {
	id := firstMetadata(ctx, requestid.MetadataKey)
	if !requestid.IsValid(id) {
		id = requestid.New()
	}
	ctx = requestid.WithContext(ctx, id)
	return logger.WithContext(ctx, log), id
}

func firstMetadata(ctx context.Context, key string) string {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if v := md.Get(key); len(v) > 0 {
			return v[0]
		}
	}
	return ""
}

// --- recovery ----------------------------------------------------------------------

// recoveryUnary converts panics into codes.Internal and logs them with a stack trace.
func recoveryUnary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer recoverPanic(ctx, info.FullMethod, &err)
		return handler(ctx, req)
	}
}

func recoveryStream() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		defer recoverPanic(ss.Context(), info.FullMethod, &err)
		return handler(srv, ss)
	}
}

// recoverPanic must be called directly with defer so recover() can stop the panic.
func recoverPanic(ctx context.Context, method string, err *error) {
	r := recover()
	if r == nil {
		return
	}
	logger.FromContext(ctx).ErrorContext(ctx, "panic recovered",
		slog.String("method", method),
		slog.Any("panic", r),
		slog.String("stack", string(debug.Stack())),
	)
	*err = status.Error(codes.Internal, "internal error")
}

// --- logging -----------------------------------------------------------------------

// loggingUnary writes one log line per RPC with method, status code, latency, and peer.
// Health checks are not logged.
func loggingUnary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		logRPC(ctx, info.FullMethod, start, err)
		return resp, err
	}
}

func loggingStream() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		start := time.Now()
		err := handler(srv, ss)
		logRPC(ss.Context(), info.FullMethod, start, err)
		return err
	}
}

func logRPC(ctx context.Context, method string, start time.Time, err error) {
	if strings.HasPrefix(method, healthMethodPrefix) {
		return
	}
	code := status.Code(err)
	level := slog.LevelInfo
	if isServerFault(code) {
		level = slog.LevelError
	}
	attrs := []slog.Attr{
		slog.String("method", method),
		slog.String("code", code.String()),
		slog.Duration("latency", time.Since(start)),
	}
	if p, ok := peer.FromContext(ctx); ok {
		attrs = append(attrs, slog.String("peer", p.Addr.String()))
	}
	logger.FromContext(ctx).LogAttrs(ctx, level, "grpc request", attrs...)
}

func isServerFault(c codes.Code) bool {
	switch c {
	case codes.Unknown, codes.Internal, codes.DataLoss, codes.Unimplemented, codes.Unavailable:
		return true
	default:
		return false
	}
}

// --- auth --------------------------------------------------------------------------

// authUnary requires "authorization: Bearer <token>" on every non-public method.
func authUnary(token string) grpc.UnaryServerInterceptor {
	check := tokenChecker(token)
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := check(ctx, info.FullMethod); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

func authStream(token string) grpc.StreamServerInterceptor {
	check := tokenChecker(token)
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := check(ss.Context(), info.FullMethod); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}

// tokenChecker returns a function validating the bearer token of a call. Tokens are
// compared through their SHA-256 digests in constant time. An empty token allows every
// call; config rejects that in production.
func tokenChecker(token string) func(ctx context.Context, method string) error {
	if token == "" {
		return func(context.Context, string) error { return nil }
	}
	want := sha256.Sum256([]byte(token))

	return func(ctx context.Context, method string) error {
		if isPublicMethod(method) {
			return nil
		}
		got, ok := strings.CutPrefix(firstMetadata(ctx, authorizationKey), bearerPrefix)
		gotSum := sha256.Sum256([]byte(got))
		if !ok || subtle.ConstantTimeCompare(gotSum[:], want[:]) != 1 {
			return status.Error(codes.Unauthenticated, "missing or invalid credentials")
		}
		return nil
	}
}

func isPublicMethod(method string) bool {
	for _, prefix := range publicMethodPrefixes {
		if strings.HasPrefix(method, prefix) {
			return true
		}
	}
	return false
}
