// Package router builds the gin engine: global middleware, route groups, and handlers.
package router

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"atlas-backend-core/api"
	"atlas-backend-core/internal/config"
	"atlas-backend-core/internal/handler"
	"atlas-backend-core/internal/httpx"
	"atlas-backend-core/internal/middleware"
)

// Deps holds everything the router needs. It is built once at startup in cmd/api.
type Deps struct {
	Config              *config.Config
	Logger              *slog.Logger
	DB                  handler.Pinger
	Tokens              middleware.AccessTokenParser
	AuthService         handler.AuthService
	ShopService         handler.ShopService
	FeedbackService     handler.FeedbackService
	InsightService      handler.InsightService
	ConsultationService handler.ConsultationService
	MemoryService       handler.MemoryService
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

	// The API contract and Swagger UI are for development; production does not expose them.
	if !cfg.App.IsProduction() {
		docs := handler.NewDocsHandler(api.OpenAPI)
		r.GET("/docs", docs.UI)
		r.GET("/docs/init.js", docs.InitJS)
		r.GET("/docs/openapi.yaml", docs.Spec)
	}

	v1 := r.Group("/api/v1")
	shops := handler.NewShopHandler(deps.ShopService, deps.FeedbackService)
	registerPublicRoutes(v1.Group("/public"), shops)
	registerAuthRoutes(v1.Group("/auth"), handler.NewAuthHandler(deps.AuthService))
	registerOwnerRoutes(
		v1.Group("/owner", middleware.RequireAuth(deps.Tokens)),
		shops,
		handler.NewInsightHandler(deps.InsightService),
		handler.NewConsultationHandler(deps.ConsultationService, deps.MemoryService),
	)

	return r, nil
}

// registerPublicRoutes mounts the unauthenticated customer endpoints (QR page).
func registerPublicRoutes(g *gin.RouterGroup, shops *handler.ShopHandler) {
	g.GET("/shop-categories", shops.Categories)
	g.GET("/shop-scales", shops.Scales)
	g.GET("/shops/:shopId", shops.GetPublic)
	g.POST("/shops/:shopId/feedbacks", shops.SubmitFeedback)
}

func registerAuthRoutes(g *gin.RouterGroup, h *handler.AuthHandler) {
	g.POST("/signup", h.Register)
	g.POST("/signin", h.Login)
	g.POST("/refresh", h.Refresh)
}

// registerOwnerRoutes mounts the endpoints for shop members. Every handler checks that the
// authenticated user belongs to the shop that owns the requested resource.
func registerOwnerRoutes(g *gin.RouterGroup, shops *handler.ShopHandler, insights *handler.InsightHandler, consult *handler.ConsultationHandler) {
	g.GET("/shops", shops.ListMine)
	g.GET("/shops/:shopId", shops.GetMine)
	g.PUT("/shops/:shopId", shops.UpdateMine)
	g.GET("/shops/:shopId/dashboard", insights.Dashboard)
	g.GET("/shops/:shopId/issues", insights.ListIssues)
	g.GET("/shops/:shopId/memories", consult.ListMemories)

	g.GET("/issues/:issueId", insights.GetIssue)
	g.POST("/issues/:issueId/consultations", consult.Start)

	g.GET("/consultations/:sessionId", consult.Get)
	g.POST("/consultations/:sessionId/turns", consult.Turn)
	g.POST("/consultations/:sessionId/save", consult.Save)

	g.PATCH("/memories/:memoryId", consult.UpdateMemory)
}
