package router

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"

	"atlas-backend-core/internal/config"
	"atlas-backend-core/internal/httpx"
	"atlas-backend-core/internal/requestid"
	"atlas-backend-core/internal/service"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

type okPinger struct{}

func (okPinger) PingContext(context.Context) error { return nil }

func newTestEngine(t *testing.T) *gin.Engine {
	t.Helper()
	cfg := &config.Config{
		App:  config.AppConfig{Name: "test", Env: config.EnvTest},
		HTTP: config.HTTPConfig{MaxBodyBytes: 1 << 20},
		CORS: config.CORSConfig{AllowedOrigins: []string{"http://localhost:3000"}},
	}
	engine, err := InitRouter(Deps{
		Config: cfg,
		Logger: slog.New(slog.DiscardHandler),
		DB:     okPinger{},
		// The user service is never reached by these tests.
		UserService: service.NewUserService(nil),
	})
	if err != nil {
		t.Fatalf("InitRouter: %v", err)
	}
	return engine
}

func TestInitRouter(t *testing.T) {
	engine := newTestEngine(t)

	tests := []struct {
		name       string
		method     string
		path       string
		origin     string
		wantStatus int
		wantCode   string
	}{
		{name: "liveness", method: http.MethodGet, path: "/healthz", wantStatus: http.StatusOK},
		{name: "readiness", method: http.MethodGet, path: "/readyz", wantStatus: http.StatusOK},
		{name: "unknown route", method: http.MethodGet, path: "/nope", wantStatus: http.StatusNotFound, wantCode: httpx.CodeRouteNotFound},
		{name: "wrong method", method: http.MethodPut, path: "/api/v1/users", wantStatus: http.StatusMethodNotAllowed, wantCode: httpx.CodeMethodNotAllowed},
		{name: "invalid id reaches validation", method: http.MethodGet, path: "/api/v1/users/x", wantStatus: http.StatusBadRequest, wantCode: httpx.CodeValidation},
		{name: "cors preflight from allowed origin", method: http.MethodOptions, path: "/api/v1/users", origin: "http://localhost:3000", wantStatus: http.StatusNoContent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
				req.Header.Set("Access-Control-Request-Method", http.MethodPost)
			}
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tt.wantStatus, w.Body)
			}
			if w.Header().Get(requestid.Header) == "" {
				t.Error("missing request id header")
			}
			if w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Error("missing security headers")
			}
			if tt.wantCode != "" {
				var resp httpx.ErrorResponse
				if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
					t.Fatalf("decode body %q: %v", w.Body.String(), err)
				}
				if resp.Error.Code != tt.wantCode {
					t.Errorf("code = %q, want %q", resp.Error.Code, tt.wantCode)
				}
			}
		})
	}
}
