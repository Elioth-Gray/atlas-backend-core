// Package router builds the gin engine: global middleware, route groups, and handlers.
package router

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"atlas-backend-core/internal/config"
	"atlas-backend-core/internal/handler"
	"atlas-backend-core/internal/httpx"
	"atlas-backend-core/internal/middleware"
)

// Deps holds everything the router needs. It is built once at startup in cmd/api.
type Deps struct {
	Config      *config.Config
	Logger      *slog.Logger
	DB          handler.Pinger
	UserService handler.UserService
}

// SetMode selects gin's mode for the given APP_ENV. Debug mode prints route information,
// so it is only used in development.
func SetMode(env string) {
	switch env {
	case config.EnvProduction, config.EnvStaging:
		gin.SetMode(gin.ReleaseMode)
	case config.EnvTest:
		gin.SetMode(gin.TestMode)
	default:
		gin.SetMode(gin.DebugMode)
	}
}

// InitRouter returns a gin engine with every route and middleware registered.
func InitRouter(deps Deps) (*gin.Engine, error) {
	cfg := deps.Config
	httpx.RegisterValidator()

	r := gin.New()
	r.HandleMethodNotAllowed = true
	if err := r.SetTrustedProxies(cfg.HTTP.TrustedProxies); err != nil {
		return nil, fmt.Errorf("set trusted proxies: %w", err)
	}

	// Order matters: the request ID must exist before the logger tags it, and recovery
	// sits inside the logger so recovered panics are still logged as 500s.
	r.Use(
		middleware.RequestID(),
		middleware.Logger(deps.Logger, "/healthz", "/readyz"),
		middleware.Recovery(),
		middleware.SecurityHeaders(cfg.App.IsProduction()),
		middleware.CORS(cfg.CORS),
		middleware.MaxBodySize(cfg.HTTP.MaxBodyBytes),
	)
	r.NoRoute(func(c *gin.Context) {
		httpx.AbortWithError(c, http.StatusNotFound, httpx.CodeRouteNotFound, "route not found")
	})
	r.NoMethod(func(c *gin.Context) {
		httpx.AbortWithError(c, http.StatusMethodNotAllowed, httpx.CodeMethodNotAllowed, "method not allowed")
	})

	health := handler.NewHealthHandler(deps.DB)
	r.GET("/healthz", health.Live)
	r.GET("/readyz", health.Ready)

	v1 := r.Group("/api/v1")
	registerUserRoutes(v1, handler.NewUserHandler(deps.UserService))

	return r, nil
}

func registerUserRoutes(v1 *gin.RouterGroup, h *handler.UserHandler) {
	users := v1.Group("/users")
	users.POST("", h.Create)
	users.GET("", h.List)
	users.GET("/:id", h.Get)
	users.PATCH("/:id", h.Update)
	users.DELETE("/:id", h.Delete)
}
