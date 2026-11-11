package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"atlas-backend-core/internal/logger"
)

// Logger stores base in the request context (see logger.FromContext) and writes one
// access log line per request. Requests to skipPaths produce no access log line, which
// keeps noisy health probes out of the logs. It must run after RequestID so the request
// ID is already in the context; the logger's handler attaches it to every record.
func Logger(base *slog.Logger, skipPaths ...string) gin.HandlerFunc {
	skip := make(map[string]struct{}, len(skipPaths))
	for _, p := range skipPaths {
		skip[p] = struct{}{}
	}

	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		c.Request = c.Request.WithContext(logger.WithContext(c.Request.Context(), base))

		c.Next()

		if _, ok := skip[path]; ok {
			return
		}
		status := c.Writer.Status()
		level := slog.LevelInfo
		if status >= http.StatusInternalServerError {
			level = slog.LevelError
		}
		base.LogAttrs(c.Request.Context(), level, "http request",
			slog.String("method", c.Request.Method),
			slog.String("path", path),
			slog.String("route", c.FullPath()),
			slog.Int("status", status),
			slog.Duration("latency", time.Since(start)),
			slog.Int("bytes", c.Writer.Size()),
			slog.String("client_ip", c.ClientIP()),
			slog.String("user_agent", c.Request.UserAgent()),
		)
	}
}
