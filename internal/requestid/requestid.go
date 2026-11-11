// Package requestid generates, validates, and propagates per-request correlation IDs.
package requestid

import (
	"context"

	"github.com/google/uuid"
)

const (
	// Header is the HTTP header used to receive and return the request ID.
	Header = "X-Request-ID"
	// MetadataKey is the gRPC metadata key carrying the request ID between services.
	MetadataKey = "x-request-id"
	// ContextKey is the gin.Context key under which the request ID is stored.
	ContextKey = "requestID"

	maxLength = 128
)

type ctxKey struct{}

// New returns a fresh random request ID.
func New() string { return uuid.NewString() }

// IsValid reports whether a client-supplied ID is safe to reuse: 1 to 128 characters drawn
// from letters, digits, and "-", "_", ".", ":". Anything else is replaced with a new ID so
// untrusted input never reaches logs or response headers verbatim.
func IsValid(id string) bool {
	if len(id) == 0 || len(id) > maxLength {
		return false
	}
	for i := 0; i < len(id); i++ {
		switch ch := id[i]; {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9',
			ch == '-', ch == '_', ch == '.', ch == ':':
		default:
			return false
		}
	}
	return true
}

// WithContext returns a copy of ctx that carries id.
func WithContext(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext returns the request ID stored in ctx, or "" when none is set.
func FromContext(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}
