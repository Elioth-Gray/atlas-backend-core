// Package httpx holds HTTP transport primitives shared by handlers and middleware: the
// success envelope, the error body, and validation error formatting.
package httpx

import "github.com/gin-gonic/gin"

// Machine-readable error codes returned in APIError.Code.
const (
	CodeBadRequest       = "BAD_REQUEST"
	CodeValidation       = "VALIDATION_ERROR"
	CodeInvalidInput     = "INVALID_INPUT"
	CodeUnauthorized     = "UNAUTHORIZED"
	CodeForbidden        = "FORBIDDEN"
	CodeNotFound         = "NOT_FOUND"
	CodeRouteNotFound    = "ROUTE_NOT_FOUND"
	CodeMethodNotAllowed = "METHOD_NOT_ALLOWED"
	CodeConflict         = "CONFLICT"
	CodePayloadTooLarge  = "PAYLOAD_TOO_LARGE"
	CodeInternal         = "INTERNAL_ERROR"
	CodeUnavailable      = "SERVICE_UNAVAILABLE"
	CodeTimeout          = "TIMEOUT"
)

// DataResponse is the envelope for a single resource: {"data": ...}.
type DataResponse[T any] struct {
	Data T `json:"data"`
}

// ListResponse is the envelope for a page of resources: {"data": [...], "meta": {...}}.
type ListResponse[T any] struct {
	Data []T      `json:"data"`
	Meta PageMeta `json:"meta"`
}

// PageMeta describes where a page sits within a collection.
type PageMeta struct {
	Page       int   `json:"page"`
	PageSize   int   `json:"page_size"`
	Total      int64 `json:"total"`
	TotalPages int64 `json:"total_pages"`
}

// NewPageMeta computes pagination metadata for the given page.
func NewPageMeta(page, pageSize int, total int64) PageMeta {
	var totalPages int64
	if pageSize > 0 {
		totalPages = (total + int64(pageSize) - 1) / int64(pageSize)
	}
	return PageMeta{Page: page, PageSize: pageSize, Total: total, TotalPages: totalPages}
}

// FieldError describes a validation failure on a single request field.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// APIError is the error body returned by every endpoint.
type APIError struct {
	Code    string       `json:"code"`
	Message string       `json:"message"`
	Details []FieldError `json:"details,omitempty"`
}

// ErrorResponse is the envelope for errors: {"error": {...}}.
type ErrorResponse struct {
	Error APIError `json:"error"`
}

// AbortWithError stops the handler chain and writes a JSON error body. Middleware uses it
// directly; handlers go through their package's handleError instead.
func AbortWithError(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, ErrorResponse{Error: APIError{Code: code, Message: message}})
}
