// Package logger builds the application's slog logger and carries it through
// context.Context values.
package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"atlas-backend-core/internal/requestid"
)

type ctxKey struct{}

// New returns a logger writing to w. level is one of debug, info, warn, error and format is
// either json or text. Every record logged with a context (InfoContext, ErrorContext,
// LogAttrs, ...) automatically gets a request_id attribute when the context carries one,
// including logs emitted by GORM and other libraries that receive the request context.
func New(w io.Writer, level, format string) (*slog.Logger, error) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("parse log level %q: %w", level, err)
	}
	opts := &slog.HandlerOptions{Level: lvl}

	var h slog.Handler
	switch format {
	case "json":
		h = slog.NewJSONHandler(w, opts)
	case "text":
		h = slog.NewTextHandler(w, opts)
	default:
		return nil, fmt.Errorf("unsupported log format %q", format)
	}
	return slog.New(contextHandler{h}), nil
}

// contextHandler enriches records with request-scoped values found in the context.
type contextHandler struct{ slog.Handler }

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := requestid.FromContext(ctx); id != "" {
		r.AddAttrs(slog.String("request_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

func (h contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{h.Handler.WithAttrs(attrs)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{h.Handler.WithGroup(name)}
}

// WithContext returns a copy of ctx that carries l.
func WithContext(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// FromContext returns the logger stored in ctx, falling back to slog.Default when none is
// set. Always log with the *Context variants (e.g. ErrorContext(ctx, ...)) so the request
// ID is attached.
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}
