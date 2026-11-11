package grpcclient

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"atlas-backend-core/internal/requestid"
)

func TestInterceptors(t *testing.T) {
	tests := []struct {
		name         string
		token        string
		requestID    string
		timeout      time.Duration
		wantAuth     string
		wantID       string
		wantDeadline bool
	}{
		{name: "token, request id and default timeout", token: "secret", requestID: "req-1", timeout: time.Second,
			wantAuth: "Bearer secret", wantID: "req-1", wantDeadline: true},
		{name: "nothing configured", wantDeadline: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.requestID != "" {
				ctx = requestid.WithContext(ctx, tt.requestID)
			}

			var md metadata.MD
			var hasDeadline bool
			invoker := func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
				md, _ = metadata.FromOutgoingContext(ctx)
				_, hasDeadline = ctx.Deadline()
				return nil
			}
			// Chain exactly as New does: timeout first, then metadata.
			withMetadata := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
				return metadataUnary(tt.token)(ctx, method, req, reply, cc, invoker, opts...)
			}
			if err := timeoutUnary(tt.timeout)(ctx, "/x/Y", nil, nil, nil, withMetadata); err != nil {
				t.Fatal(err)
			}

			if got := first(md, "authorization"); got != tt.wantAuth {
				t.Errorf("authorization = %q, want %q", got, tt.wantAuth)
			}
			if got := first(md, requestid.MetadataKey); got != tt.wantID {
				t.Errorf("request id = %q, want %q", got, tt.wantID)
			}
			if hasDeadline != tt.wantDeadline {
				t.Errorf("deadline set = %t, want %t", hasDeadline, tt.wantDeadline)
			}
		})
	}
}

func TestNewRequiresTarget(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("expected an error for an empty target")
	}
}

func first(md metadata.MD, key string) string {
	if v := md.Get(key); len(v) > 0 {
		return v[0]
	}
	return ""
}
