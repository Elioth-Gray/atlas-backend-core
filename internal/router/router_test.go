package router

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"atlas-backend-core/api"
	"atlas-backend-core/internal/auth"
	"atlas-backend-core/internal/config"
	"atlas-backend-core/internal/httpx"
	"atlas-backend-core/internal/requestid"
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
		Tokens: auth.NewTokenManager(config.AuthConfig{JWTSecret: "test-secret", JWTIssuer: "test", AccessTTL: time.Minute, RefreshTTL: time.Hour}),
		// Services are never reached by these tests.
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
		bearer     string
		wantStatus int
		wantCode   string
	}{
		{name: "liveness", method: http.MethodGet, path: "/healthz", wantStatus: http.StatusOK},
		{name: "readiness", method: http.MethodGet, path: "/readyz", wantStatus: http.StatusOK},
		{name: "unknown route", method: http.MethodGet, path: "/nope", wantStatus: http.StatusNotFound, wantCode: httpx.CodeRouteNotFound},
		{name: "wrong method", method: http.MethodPut, path: "/api/v1/auth/signin", wantStatus: http.StatusMethodNotAllowed, wantCode: httpx.CodeMethodNotAllowed},
		{name: "invalid id reaches validation", method: http.MethodGet, path: "/api/v1/public/shops/x", wantStatus: http.StatusBadRequest, wantCode: httpx.CodeValidation},
		{name: "owner routes require a token", method: http.MethodGet, path: "/api/v1/owner/shops", wantStatus: http.StatusUnauthorized, wantCode: httpx.CodeUnauthorized},
		{name: "owner routes reject a bad token", method: http.MethodGet, path: "/api/v1/owner/shops", bearer: "nope", wantStatus: http.StatusUnauthorized, wantCode: httpx.CodeUnauthorized},
		{name: "cors preflight from allowed origin", method: http.MethodOptions, path: "/api/v1/auth/signin", origin: "http://localhost:3000", wantStatus: http.StatusNoContent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.bearer != "" {
				req.Header.Set("Authorization", "Bearer "+tt.bearer)
			}
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

func TestDocsRoutes(t *testing.T) {
	engine := newTestEngine(t)
	for _, path := range []string{"/docs", "/docs/init.js", "/docs/openapi.yaml"} {
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK {
			t.Errorf("GET %s: status = %d, want 200", path, w.Code)
		}
	}
}

func TestDocsDisabledInProduction(t *testing.T) {
	cfg := &config.Config{
		App:  config.AppConfig{Name: "test", Env: config.EnvProduction},
		HTTP: config.HTTPConfig{MaxBodyBytes: 1 << 20},
	}
	engine, err := InitRouter(Deps{Config: cfg, Logger: slog.New(slog.DiscardHandler), DB: okPinger{}})
	if err != nil {
		t.Fatalf("InitRouter: %v", err)
	}
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/docs/openapi.yaml", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

// TestOpenAPICoversRoutes keeps api/openapi.yaml in sync with the router: every registered
// route (except the docs themselves) must be documented with the same method.
func TestOpenAPICoversRoutes(t *testing.T) {
	documented := openAPIOperations(string(api.OpenAPI))
	for _, rt := range newTestEngine(t).Routes() {
		if strings.HasPrefix(rt.Path, "/docs") {
			continue
		}
		key := strings.ToLower(rt.Method) + " " + ginPathToOpenAPI(rt.Path)
		if !documented[key] {
			t.Errorf("route %s %s is missing from api/openapi.yaml", rt.Method, rt.Path)
		}
		delete(documented, key)
	}
	for op := range documented {
		t.Errorf("api/openapi.yaml documents %q, which the router does not serve", op)
	}
}

var (
	specPathLine   = regexp.MustCompile(`^  (/\S*):\s*$`)
	specMethodLine = regexp.MustCompile(`^    (get|post|put|patch|delete):\s*$`)
	ginParam       = regexp.MustCompile(`:(\w+)`)
)

// openAPIOperations lists "method path" pairs under the top-level paths object. It relies
// on the file's two-space indentation instead of a YAML parser.
func openAPIOperations(spec string) map[string]bool {
	ops := map[string]bool{}
	inPaths, path := false, ""
	for _, line := range strings.Split(strings.ReplaceAll(spec, "\r\n", "\n"), "\n") {
		if line != "" && line[0] != ' ' && line[0] != '#' {
			inPaths = strings.HasPrefix(line, "paths:")
			continue
		}
		if !inPaths {
			continue
		}
		if m := specPathLine.FindStringSubmatch(line); m != nil {
			path = m[1]
		} else if m := specMethodLine.FindStringSubmatch(line); m != nil && path != "" {
			ops[m[1]+" "+path] = true
		}
	}
	return ops
}

func ginPathToOpenAPI(p string) string { return ginParam.ReplaceAllString(p, "{$1}") }
