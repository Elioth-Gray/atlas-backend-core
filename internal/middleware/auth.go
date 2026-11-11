package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"atlas-backend-core/internal/auth"
	"atlas-backend-core/internal/httpx"
)

// UserIDKey is the gin.Context key holding the authenticated user's ID.
const UserIDKey = "userID"

// AccessTokenParser verifies access tokens. *auth.TokenManager satisfies it.
type AccessTokenParser interface {
	ParseAccess(token string) (uuid.UUID, error)
}

// RequireAuth rejects requests without a valid "Authorization: Bearer <access token>"
// header. On success the user ID is stored on the gin.Context (UserIDKey) and on the
// request's context.Context (auth.UserIDFromContext).
func RequireAuth(tokens AccessTokenParser) gin.HandlerFunc {
	return func(c *gin.Context) {
		scheme, token, ok := strings.Cut(c.GetHeader("Authorization"), " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
			c.Header("WWW-Authenticate", `Bearer realm="atlas"`)
			httpx.AbortWithError(c, http.StatusUnauthorized, httpx.CodeUnauthorized, "missing bearer token")
			return
		}
		userID, err := tokens.ParseAccess(strings.TrimSpace(token))
		if err != nil {
			c.Header("WWW-Authenticate", `Bearer realm="atlas", error="invalid_token"`)
			httpx.AbortWithError(c, http.StatusUnauthorized, httpx.CodeUnauthorized, "invalid or expired access token")
			return
		}
		c.Set(UserIDKey, userID)
		c.Request = c.Request.WithContext(auth.WithUserID(c.Request.Context(), userID))
		c.Next()
	}
}
