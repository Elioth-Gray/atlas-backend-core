package middleware

import (
	"net/http"
	"slices"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"atlas-backend-core/internal/config"
	"atlas-backend-core/internal/requestid"
)

// CORS returns a CORS middleware configured from cfg. When no origins are configured,
// CORS is disabled and the middleware is a no-op. An origin of "*" allows every origin.
func CORS(cfg config.CORSConfig) gin.HandlerFunc {
	if len(cfg.AllowedOrigins) == 0 {
		return func(c *gin.Context) { c.Next() }
	}

	corsCfg := cors.Config{
		AllowMethods: []string{
			http.MethodGet, http.MethodPost, http.MethodPut,
			http.MethodPatch, http.MethodDelete, http.MethodOptions,
		},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", requestid.Header},
		ExposeHeaders:    []string{requestid.Header},
		AllowCredentials: cfg.AllowCredentials,
		MaxAge:           cfg.MaxAge,
	}
	if slices.Contains(cfg.AllowedOrigins, "*") {
		corsCfg.AllowAllOrigins = true
	} else {
		corsCfg.AllowOrigins = cfg.AllowedOrigins
	}
	return cors.New(corsCfg)
}
