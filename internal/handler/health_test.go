package handler

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"atlas-backend-core/internal/httpx"
)

type pingerFunc func(ctx context.Context) error

func (f pingerFunc) PingContext(ctx context.Context) error { return f(ctx) }

func TestHealthHandler(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		pingErr    error
		wantStatus int
		wantCode   string
	}{
		{name: "live ignores database", path: "/healthz", pingErr: errors.New("down"), wantStatus: http.StatusOK},
		{name: "ready when database reachable", path: "/readyz", wantStatus: http.StatusOK},
		{
			name:       "not ready when database unreachable",
			path:       "/readyz",
			pingErr:    errors.New("dial tcp: connection refused"),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   httpx.CodeUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewHealthHandler(pingerFunc(func(context.Context) error { return tt.pingErr }))
			r := gin.New()
			r.GET("/healthz", h.Live)
			r.GET("/readyz", h.Ready)

			w := doRequest(t, r, http.MethodGet, tt.path, "")

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tt.wantStatus, w.Body)
			}
			if tt.wantCode != "" {
				if apiErr := decodeError(t, w); apiErr.Code != tt.wantCode {
					t.Errorf("code = %q, want %q", apiErr.Code, tt.wantCode)
				}
			}
		})
	}
}
