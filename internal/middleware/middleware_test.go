package middleware

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"atlas-backend-core/internal/httpx"
	"atlas-backend-core/internal/requestid"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	slog.SetDefault(slog.New(slog.DiscardHandler)) // keep expected panic logs out of test output
	os.Exit(m.Run())
}

func TestRequestID(t *testing.T) {
	tests := []struct {
		name     string
		incoming string
		wantSame bool
	}{
		{name: "generates when missing", incoming: "", wantSame: false},
		{name: "reuses valid incoming id", incoming: "abc-123_DEF.456:7", wantSame: true},
		{name: "replaces unsafe incoming id", incoming: "bad id\r\ninjected", wantSame: false},
		{name: "replaces overly long id", incoming: strings.Repeat("a", 129), wantSame: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ginID, ctxID string
			r := gin.New()
			r.Use(RequestID())
			r.GET("/", func(c *gin.Context) {
				ginID = c.GetString(requestid.ContextKey)
				ctxID = requestid.FromContext(c.Request.Context())
				c.Status(http.StatusOK)
			})

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.incoming != "" {
				req.Header.Set(requestid.Header, tt.incoming)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			got := w.Header().Get(requestid.Header)
			if got == "" || got != ginID || got != ctxID {
				t.Fatalf("ids differ: header=%q gin=%q ctx=%q", got, ginID, ctxID)
			}
			if (got == tt.incoming) != tt.wantSame {
				t.Errorf("header = %q, incoming = %q, wantSame = %t", got, tt.incoming, tt.wantSame)
			}
		})
	}
}

func TestRecovery(t *testing.T) {
	r := gin.New()
	r.Use(Recovery())
	r.GET("/panic", func(*gin.Context) { panic("boom") })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/panic", nil))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	var resp httpx.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body %q: %v", w.Body.String(), err)
	}
	if resp.Error.Code != httpx.CodeInternal || strings.Contains(w.Body.String(), "boom") {
		t.Errorf("unexpected body %s", w.Body)
	}
}

func TestSecurityHeaders(t *testing.T) {
	tests := []struct {
		name     string
		hsts     bool
		wantHSTS bool
	}{
		{name: "without hsts", hsts: false, wantHSTS: false},
		{name: "with hsts", hsts: true, wantHSTS: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gin.New()
			r.Use(SecurityHeaders(tt.hsts))
			r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

			if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q", got)
			}
			if got := w.Header().Get("X-Frame-Options"); got != "DENY" {
				t.Errorf("X-Frame-Options = %q", got)
			}
			if got := w.Header().Get("Strict-Transport-Security") != ""; got != tt.wantHSTS {
				t.Errorf("HSTS present = %t, want %t", got, tt.wantHSTS)
			}
		})
	}
}

func TestMaxBodySize(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{name: "within limit", body: `{"a":1}`, wantStatus: http.StatusOK},
		{name: "declared length over limit", body: strings.Repeat("x", 64), wantStatus: http.StatusRequestEntityTooLarge},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gin.New()
			r.Use(MaxBodySize(16))
			r.POST("/", func(c *gin.Context) { c.Status(http.StatusOK) })

			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.body)))

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}
