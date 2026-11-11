// Package apperror defines transport-agnostic domain errors. Services and repositories
// return these; the HTTP layer maps them to status codes in one place.
package apperror

import (
	"errors"
	"fmt"
)

// Sentinel error kinds. Match them with errors.Is.
var (
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("conflict")
	ErrInvalidInput = errors.New("invalid input")
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
	ErrUnavailable  = errors.New("unavailable")
)

// Error is a domain error of a given Kind carrying a Message that is safe to show to API
// clients. Err optionally holds the underlying cause, which is logged but never exposed.
type Error struct {
	Kind    error
	Message string
	Err     error
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

// Unwrap exposes both the kind and the cause so errors.Is and errors.As see through it.
func (e *Error) Unwrap() []error {
	if e.Err != nil {
		return []error{e.Kind, e.Err}
	}
	return []error{e.Kind}
}

// New returns an Error of the given kind with a client-safe message.
func New(kind error, message string) *Error {
	return &Error{Kind: kind, Message: message}
}

// Wrap returns an Error of the given kind with a client-safe message and an internal cause.
func Wrap(kind error, message string, err error) *Error {
	return &Error{Kind: kind, Message: message, Err: err}
}

// NotFound returns an ErrNotFound error with the given message.
func NotFound(message string) *Error { return New(ErrNotFound, message) }

// Conflict returns an ErrConflict error with the given message.
func Conflict(message string) *Error { return New(ErrConflict, message) }

// InvalidInput returns an ErrInvalidInput error with the given message.
func InvalidInput(message string) *Error { return New(ErrInvalidInput, message) }

// Unauthorized returns an ErrUnauthorized error with the given message.
func Unauthorized(message string) *Error { return New(ErrUnauthorized, message) }

// Forbidden returns an ErrForbidden error with the given message.
func Forbidden(message string) *Error { return New(ErrForbidden, message) }
