package middleware

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"atlas-backend-core/internal/httpx"
)

// SecurityHeaders sets defensive response headers suited to a JSON API. HSTS is only sent
// when enableHSTS is true, which should be the case in production behind TLS.
//
// Header values are built once and shared across responses to avoid per-request
// allocations; each slice has len == cap, so a later Header().Add copies instead of
// mutating the shared backing array.
func SecurityHeaders(enableHSTS bool) gin.HandlerFunc {
	headers := map[string][]string{
		"X-Content-Type-Options":  {"nosniff"},
		"X-Frame-Options":         {"DENY"},
		"Referrer-Policy":         {"no-referrer"},
		"Content-Security-Policy": {"default-src 'none'; frame-ancestors 'none'"},
	}
	if enableHSTS {
		headers["Strict-Transport-Security"] = []string{"max-age=63072000; includeSubDomains"}
	}

	return func(c *gin.Context) {
		h := c.Writer.Header()
		for k, v := range headers {
			h[k] = v
		}
		c.Next()
	}
}

// MaxBodySize limits request bodies to limit bytes. Requests declaring a larger
// Content-Length are rejected with 413 immediately; other bodies are wrapped with
// http.MaxBytesReader so oversized payloads fail while being decoded.
func MaxBodySize(limit int64) gin.HandlerFunc {
	msg := fmt.Sprintf("request body must not exceed %d bytes", limit)

	return func(c *gin.Context) {
		if c.Request.ContentLength > limit {
			httpx.AbortWithError(c, http.StatusRequestEntityTooLarge, httpx.CodePayloadTooLarge, msg)
			return
		}
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		}
		c.Next()
	}
}
