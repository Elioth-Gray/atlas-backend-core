package handler

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"atlas-backend-core/internal/domain"
	"atlas-backend-core/internal/httpx"
	"atlas-backend-core/internal/service"
)

// UserService is the business logic UserHandler depends on.
type UserService interface {
	Create(ctx context.Context, in service.CreateUserInput) (domain.User, error)
	Get(ctx context.Context, id uuid.UUID) (domain.User, error)
	List(ctx context.Context, params service.PageParams) (service.Page[domain.User], error)
	Update(ctx context.Context, id uuid.UUID, in service.UpdateUserInput) (domain.User, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

// UserHandler serves the /api/v1/users endpoints.
type UserHandler struct {
	svc UserService
}

// NewUserHandler returns a UserHandler backed by svc.
func NewUserHandler(svc UserService) *UserHandler {
	return &UserHandler{svc: svc}
}

// Create handles POST /api/v1/users.
func (h *UserHandler) Create(c *gin.Context) {
	var req createUserRequest
	if err := bindJSON(c, &req); err != nil {
		handleError(c, err)
		return
	}

	user, err := h.svc.Create(c.Request.Context(), service.CreateUserInput{Name: req.Name, Email: req.Email})
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(http.StatusCreated, httpx.DataResponse[userResponse]{Data: newUserResponse(user)})
}

// Get handles GET /api/v1/users/:id.
func (h *UserHandler) Get(c *gin.Context) {
	id, err := bindID(c)
	if err != nil {
		handleError(c, err)
		return
	}

	user, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, httpx.DataResponse[userResponse]{Data: newUserResponse(user)})
}

// List handles GET /api/v1/users.
func (h *UserHandler) List(c *gin.Context) {
	var q listUsersQuery
	if err := bindQuery(c, &q); err != nil {
		handleError(c, err)
		return
	}

	page, err := h.svc.List(c.Request.Context(), service.PageParams{Page: q.Page, PageSize: q.PageSize})
	if err != nil {
		handleError(c, err)
		return
	}

	data := make([]userResponse, 0, len(page.Items))
	for _, u := range page.Items {
		data = append(data, newUserResponse(u))
	}
	c.JSON(http.StatusOK, httpx.ListResponse[userResponse]{
		Data: data,
		Meta: httpx.NewPageMeta(page.Page, page.PageSize, page.Total),
	})
}

// Update handles PATCH /api/v1/users/:id.
func (h *UserHandler) Update(c *gin.Context) {
	id, err := bindID(c)
	if err != nil {
		handleError(c, err)
		return
	}
	var req updateUserRequest
	if err := bindJSON(c, &req); err != nil {
		handleError(c, err)
		return
	}

	user, err := h.svc.Update(c.Request.Context(), id, service.UpdateUserInput{Name: req.Name, Email: req.Email})
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, httpx.DataResponse[userResponse]{Data: newUserResponse(user)})
}

// Delete handles DELETE /api/v1/users/:id.
func (h *UserHandler) Delete(c *gin.Context) {
	id, err := bindID(c)
	if err != nil {
		handleError(c, err)
		return
	}

	if err := h.svc.Delete(c.Request.Context(), id); err != nil {
		handleError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
