// Package handler contains the HTTP handlers. Handlers stay thin: bind input, call a
// service, map the result to a response DTO. Every error goes through handleError.
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"atlas-backend-core/internal/apperror"
	"atlas-backend-core/internal/httpx"
	"atlas-backend-core/internal/logger"
)

// statusClientClosedRequest is the non-standard status (popularized by nginx) recorded
// when the client goes away before the response is written.
const statusClientClosedRequest = 499

// handleError is the single place where errors become HTTP responses. Handlers must never
// write error bodies themselves. 5xx errors are logged with the request-scoped logger,
// which already carries the request ID; their details are never sent to the client.
func handleError(c *gin.Context, err error) {
	ctx := c.Request.Context()
	if errors.Is(err, context.Canceled) {
		c.AbortWithStatus(statusClientClosedRequest)
		return
	}

	status, body := mapError(err)
	if status >= http.StatusInternalServerError {
		logger.FromContext(ctx).ErrorContext(ctx, "request failed",
			slog.Int("status", status),
			slog.String("error", err.Error()),
		)
	}
	c.AbortWithStatusJSON(status, httpx.ErrorResponse{Error: body})
}

func mapError(err error) (int, httpx.APIError) {
	var (
		tooLarge *http.MaxBytesError
		bindErr  *bindError
		paramErr *paramError
	)
	if errors.As(err, &paramErr) {
		return http.StatusBadRequest, httpx.APIError{
			Code:    httpx.CodeValidation,
			Message: "request validation failed",
			Details: []httpx.FieldError{{Field: paramErr.field, Message: paramErr.message}},
		}
	}
	if details, ok := httpx.AsValidationErrors(err); ok {
		return http.StatusBadRequest, httpx.APIError{
			Code:    httpx.CodeValidation,
			Message: "request validation failed",
			Details: httpx.ValidationDetails(details),
		}
	}

	switch {
	case errors.As(err, &tooLarge):
		return http.StatusRequestEntityTooLarge, httpx.APIError{
			Code:    httpx.CodePayloadTooLarge,
			Message: fmt.Sprintf("request body must not exceed %d bytes", tooLarge.Limit),
		}
	case errors.As(err, &bindErr):
		return http.StatusBadRequest, httpx.APIError{Code: httpx.CodeBadRequest, Message: bindErrorMessage(bindErr.err)}
	case errors.Is(err, apperror.ErrInvalidInput):
		return http.StatusBadRequest, appError(err, httpx.CodeInvalidInput, "invalid input")
	case errors.Is(err, apperror.ErrUnauthorized):
		return http.StatusUnauthorized, appError(err, httpx.CodeUnauthorized, "authentication required")
	case errors.Is(err, apperror.ErrForbidden):
		return http.StatusForbidden, appError(err, httpx.CodeForbidden, "access denied")
	case errors.Is(err, apperror.ErrNotFound):
		return http.StatusNotFound, appError(err, httpx.CodeNotFound, "resource not found")
	case errors.Is(err, apperror.ErrConflict):
		return http.StatusConflict, appError(err, httpx.CodeConflict, "resource already exists")
	case errors.Is(err, apperror.ErrUnavailable):
		return http.StatusServiceUnavailable, appError(err, httpx.CodeUnavailable, "service unavailable")
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, httpx.APIError{Code: httpx.CodeTimeout, Message: "request timed out"}
	default:
		return http.StatusInternalServerError, httpx.APIError{Code: httpx.CodeInternal, Message: "internal server error"}
	}
}

// appError uses the client-safe message of an *apperror.Error when present.
func appError(err error, code, fallback string) httpx.APIError {
	msg := fallback
	var appErr *apperror.Error
	if errors.As(err, &appErr) && appErr.Message != "" {
		msg = appErr.Message
	}
	return httpx.APIError{Code: code, Message: msg}
}

func bindErrorMessage(err error) string {
	var (
		syntaxErr *json.SyntaxError
		typeErr   *json.UnmarshalTypeError
		numErr    *strconv.NumError
	)
	switch {
	case errors.Is(err, io.EOF):
		return "request body must not be empty"
	case errors.As(err, &syntaxErr), errors.Is(err, io.ErrUnexpectedEOF):
		return "request body contains malformed JSON"
	case errors.As(err, &typeErr):
		if typeErr.Field != "" {
			return fmt.Sprintf("field %q must be of type %s", typeErr.Field, typeErr.Type)
		}
		return "request body contains a value of the wrong type"
	case errors.As(err, &numErr):
		return fmt.Sprintf("value %q is not a valid number", numErr.Num)
	default:
		return "invalid request"
	}
}
