package grpcserver

import (
	"context"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	healthgrpc "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	userv1 "atlas-backend-core/gen/atlas/user/v1"
	"atlas-backend-core/internal/config"
	"atlas-backend-core/internal/domain"
	"atlas-backend-core/internal/grpcclient"
	"atlas-backend-core/internal/service"
)

const testToken = "test-token-for-grpc-server-tests"

// fakeUserService implements UserService with overridable functions.
type fakeUserService struct {
	createFn func(ctx context.Context, in service.CreateUserInput) (domain.User, error)
	getFn    func(ctx context.Context, id uuid.UUID) (domain.User, error)
	listFn   func(ctx context.Context, p service.PageParams) (service.Page[domain.User], error)
	updateFn func(ctx context.Context, id uuid.UUID, in service.UpdateUserInput) (domain.User, error)
	deleteFn func(ctx context.Context, id uuid.UUID) error
}

func (f *fakeUserService) Create(ctx context.Context, in service.CreateUserInput) (domain.User, error) {
	return f.createFn(ctx, in)
}

func (f *fakeUserService) Get(ctx context.Context, id uuid.UUID) (domain.User, error) {
	return f.getFn(ctx, id)
}

func (f *fakeUserService) List(ctx context.Context, p service.PageParams) (service.Page[domain.User], error) {
	return f.listFn(ctx, p)
}

func (f *fakeUserService) Update(ctx context.Context, id uuid.UUID, in service.UpdateUserInput) (domain.User, error) {
	return f.updateFn(ctx, id, in)
}

func (f *fakeUserService) Delete(ctx context.Context, id uuid.UUID) error {
	return f.deleteFn(ctx, id)
}

// testServer starts the real gRPC server (all interceptors included) on an in-memory
// bufconn listener and returns a dial function for clients with any token.
func testServer(t *testing.T, svc UserService) func(token string) *grpc.ClientConn {
	t.Helper()
	srv, err := New(Deps{
		Config: config.GRPCConfig{
			AuthToken:        testToken,
			MaxRecvMsgBytes:  1 << 20,
			MaxSendMsgBytes:  1 << 20,
			KeepaliveTime:    time.Minute,
			KeepaliveTimeout: 10 * time.Second,
			ShutdownTimeout:  time.Second,
		},
		Logger:      slog.New(slog.DiscardHandler),
		UserService: svc,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	lis := bufconn.Listen(1 << 20)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, lis) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})

	return func(token string) *grpc.ClientConn {
		t.Helper()
		conn, err := grpcclient.New(
			grpcclient.Config{Target: "passthrough:///bufnet", AuthToken: token, Timeout: 5 * time.Second},
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}
}

// fieldViolations extracts the fields named in a BadRequest detail of err.
func fieldViolations(err error) []string {
	var fields []string
	for _, d := range status.Convert(err).Details() {
		if br, ok := d.(*errdetails.BadRequest); ok {
			for _, v := range br.GetFieldViolations() {
				fields = append(fields, v.GetField())
			}
		}
	}
	return fields
}

func TestHealthIsPublicAndServing(t *testing.T) {
	dial := testServer(t, &fakeUserService{})
	client := healthgrpc.NewHealthClient(dial("")) // no token

	for _, svcName := range []string{"", userv1.UserService_ServiceDesc.ServiceName} {
		resp, err := client.Check(context.Background(), &healthgrpc.HealthCheckRequest{Service: svcName})
		if err != nil {
			t.Fatalf("Check(%q): %v", svcName, err)
		}
		if resp.GetStatus() != healthgrpc.HealthCheckResponse_SERVING {
			t.Errorf("Check(%q) = %v, want SERVING", svcName, resp.GetStatus())
		}
	}
}

func TestTokenChecker(t *testing.T) {
	check := tokenChecker("secret")
	tests := []struct {
		name     string
		method   string
		header   string
		wantCode codes.Code
	}{
		{name: "valid token", method: "/atlas.user.v1.UserService/GetUser", header: "Bearer secret", wantCode: codes.OK},
		{name: "missing token", method: "/atlas.user.v1.UserService/GetUser", wantCode: codes.Unauthenticated},
		{name: "wrong token", method: "/atlas.user.v1.UserService/GetUser", header: "Bearer nope", wantCode: codes.Unauthenticated},
		{name: "wrong scheme", method: "/atlas.user.v1.UserService/GetUser", header: "Basic secret", wantCode: codes.Unauthenticated},
		{name: "health is public", method: "/grpc.health.v1.Health/Check", wantCode: codes.OK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.header != "" {
				ctx = incoming(ctx, authorizationKey, tt.header)
			}
			if got := status.Code(check(ctx, tt.method)); got != tt.wantCode {
				t.Errorf("code = %v, want %v", got, tt.wantCode)
			}
		})
	}
}

func TestRecoveryUnary(t *testing.T) {
	interceptor := recoveryUnary()
	_, err := interceptor(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/x/Y"},
		func(context.Context, any) (any, error) { panic("boom") })
	if status.Code(err) != codes.Internal {
		t.Fatalf("code = %v, want Internal", status.Code(err))
	}
	if status.Convert(err).Message() != "internal error" {
		t.Errorf("panic value leaked: %q", status.Convert(err).Message())
	}
}
