package middleware

import (
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"syscall"

	"github.com/gin-gonic/gin"

	"atlas-backend-core/internal/httpx"
	"atlas-backend-core/internal/logger"
)

// Recovery converts panics into a consistent JSON 500 response and logs the panic value
// with its stack trace. Panics caused by the client dropping the connection are logged at
// warn level without writing a response, and http.ErrAbortHandler is re-raised so net/http
// can abort the response as intended.
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(rec)
			}

			ctx := c.Request.Context()
			log := logger.FromContext(ctx)
			if isConnectionLost(rec) {
				log.WarnContext(ctx, "client connection lost", slog.Any("error", rec))
				c.Abort()
				return
			}

			log.ErrorContext(ctx, "panic recovered",
				slog.Any("panic", rec),
				slog.String("stack", string(debug.Stack())),
			)
			if c.Writer.Written() {
				c.Abort()
				return
			}
			httpx.AbortWithError(c, http.StatusInternalServerError, httpx.CodeInternal, "internal server error")
		}()
		c.Next()
	}
}

func isConnectionLost(rec any) bool {
	err, ok := rec.(error)
	if !ok {
		return false
	}
	return errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET)
}
