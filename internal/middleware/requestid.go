// Package middleware contains gin middleware. Every middleware is a factory function that
// closes over its configuration and returns a gin.HandlerFunc.
package middleware

import (
	"github.com/gin-gonic/gin"

	"atlas-backend-core/internal/requestid"
)

// RequestID ensures every request carries a correlation ID. A valid incoming X-Request-ID
// header is reused, otherwise a new one is generated. The ID is echoed in the response
// header, stored on the gin.Context under requestid.ContextKey, and attached to the
// request's context.Context so downstream services can read it.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(requestid.Header)
		if !requestid.IsValid(id) {
			id = requestid.New()
		}
		c.Set(requestid.ContextKey, id)
		c.Request = c.Request.WithContext(requestid.WithContext(c.Request.Context(), id))
		c.Header(requestid.Header, id)
		c.Next()
	}
}
