package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"atlas-backend-core/internal/apperror"
	"atlas-backend-core/internal/auth"
	"atlas-backend-core/internal/service"
)

// paramError reports an invalid path parameter as a field-level validation error.
type paramError struct {
	field   string
	message string
}

func (e *paramError) Error() string { return "invalid path parameter " + e.field + ": " + e.message }

// uuidParam parses the named path parameter (e.g. "shopId") as a UUID.
func uuidParam(c *gin.Context, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param(name))
	if err != nil {
		return uuid.Nil, &paramError{field: name, message: "must be a valid UUID"}
	}
	return id, nil
}

// currentUser returns the user authenticated by middleware.RequireAuth.
func currentUser(c *gin.Context) (uuid.UUID, error) {
	id, ok := auth.UserIDFromContext(c.Request.Context())
	if !ok {
		return uuid.Nil, apperror.Unauthorized("authentication required")
	}
	return id, nil
}

// pageQuery is the pagination part of list query strings.
type pageQuery struct {
	Page     int `form:"page" binding:"omitempty,min=1,max=100000"`
	PageSize int `form:"page_size" binding:"omitempty,min=1,max=100"`
}

func (q pageQuery) params() service.PageParams {
	return service.PageParams{Page: q.Page, PageSize: q.PageSize}
}

// parseUUIDs converts already validated UUID strings.
func parseUUIDs(in []string) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(in))
	for _, s := range in {
		if id, err := uuid.Parse(s); err == nil {
			out = append(out, id)
		}
	}
	return out
}
