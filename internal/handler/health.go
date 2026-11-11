package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"atlas-backend-core/internal/apperror"
)

const readinessTimeout = 2 * time.Second

// Pinger reports whether a dependency is reachable. *sql.DB satisfies it.
type Pinger interface {
	PingContext(ctx context.Context) error
}

// HealthHandler serves liveness and readiness probes.
type HealthHandler struct {
	db Pinger
}

// NewHealthHandler returns a HealthHandler that checks db for readiness.
func NewHealthHandler(db Pinger) *HealthHandler {
	return &HealthHandler{db: db}
}

type healthResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks,omitempty"`
}

// Live reports that the process is up. It never touches dependencies.
func (h *HealthHandler) Live(c *gin.Context) {
	c.JSON(http.StatusOK, healthResponse{Status: "ok"})
}

// Ready reports whether the service can handle traffic, i.e. the database is reachable.
func (h *HealthHandler) Ready(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), readinessTimeout)
	defer cancel()

	if err := h.db.PingContext(ctx); err != nil {
		handleError(c, apperror.Wrap(apperror.ErrUnavailable, "database is not reachable", err))
		return
	}
	c.JSON(http.StatusOK, healthResponse{Status: "ok", Checks: map[string]string{"database": "ok"}})
}
