package grpcserver

import (
	"context"
	"errors"
	"log/slog"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"atlas-backend-core/internal/apperror"
	"atlas-backend-core/internal/logger"
	"atlas-backend-core/internal/validation"
)

// toStatus converts a service error into a gRPC status error at the handler boundary.
// Expected domain errors keep their client-safe message; anything else is logged and
// reported as INTERNAL without details, so SQL errors or internals never leak.
func toStatus(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, apperror.ErrInvalidInput):
		return status.Error(codes.InvalidArgument, clientMessage(err, "invalid input"))
	case errors.Is(err, apperror.ErrNotFound):
		return status.Error(codes.NotFound, clientMessage(err, "resource not found"))
	case errors.Is(err, apperror.ErrConflict):
		return status.Error(codes.AlreadyExists, clientMessage(err, "resource already exists"))
	case errors.Is(err, apperror.ErrUnauthorized):
		return status.Error(codes.Unauthenticated, clientMessage(err, "authentication required"))
	case errors.Is(err, apperror.ErrForbidden):
		return status.Error(codes.PermissionDenied, clientMessage(err, "access denied"))
	case errors.Is(err, apperror.ErrUnavailable):
		return status.Error(codes.Unavailable, clientMessage(err, "service unavailable"))
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "request canceled")
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, "deadline exceeded")
	default:
		logger.FromContext(ctx).ErrorContext(ctx, "rpc failed", slog.String("error", err.Error()))
		return status.Error(codes.Internal, "internal error")
	}
}

func clientMessage(err error, fallback string) string {
	var appErr *apperror.Error
	if errors.As(err, &appErr) && appErr.Message != "" {
		return appErr.Message
	}
	return fallback
}

// badRequest collects field violations and turns them into a single INVALID_ARGUMENT
// status carrying google.rpc.BadRequest details.
type badRequest struct {
	validate   *validator.Validate
	violations []*errdetails.BadRequest_FieldViolation
}

func (b *badRequest) add(field, description string) {
	b.violations = append(b.violations, &errdetails.BadRequest_FieldViolation{Field: field, Description: description})
}

// check validates value against validator tags (e.g. "required,email") and records the
// first failing rule for field.
func (b *badRequest) check(field string, value any, tags string) {
	err := b.validate.Var(value, tags)
	if err == nil {
		return
	}
	var ve validator.ValidationErrors
	if errors.As(err, &ve) && len(ve) > 0 {
		b.add(field, field+" "+validation.Message(ve[0]))
		return
	}
	b.add(field, field+" is invalid")
}

// uuid validates and parses a UUID field.
func (b *badRequest) uuid(field, value string) uuid.UUID {
	b.check(field, value, "required,uuid")
	id, _ := uuid.Parse(value) // a parse failure was already recorded by check
	return id
}

// err returns the INVALID_ARGUMENT status, or nil when nothing was recorded.
func (b *badRequest) err() error {
	if len(b.violations) == 0 {
		return nil
	}
	st := status.New(codes.InvalidArgument, "request validation failed")
	if detailed, err := st.WithDetails(&errdetails.BadRequest{FieldViolations: b.violations}); err == nil {
		st = detailed
	}
	return st.Err()
}
