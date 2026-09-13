// Package observ provides structured JSON logging with request-id propagation.
package observ

import (
	"context"
	"io"
	"log/slog"
)

// Log levels mirror slog levels.
const (
	LevelDebug = slog.LevelDebug
	LevelInfo  = slog.LevelInfo
	LevelWarn  = slog.LevelWarn
	LevelError = slog.LevelError
)

// RequestIDKey is the attribute key used for request-id in log output.
const RequestIDKey = "request_id"

// New creates a new JSON slog.Logger that automatically extracts request_id
// from context for all *Context logging methods.
func New(w io.Writer, level slog.Level) *slog.Logger {
	h := &requestIDHandler{
		inner: slog.NewJSONHandler(w, &slog.HandlerOptions{
			Level: level,
		}),
	}
	return slog.New(h)
}

type requestIDCtxKey struct{}

// WithRequestID stores a request ID in the context.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDCtxKey{}, id)
}

// RequestIDFromContext extracts the request ID from context.
func RequestIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDCtxKey{}).(string); ok {
		return id
	}
	return ""
}

// requestIDHandler injects request_id from context into every log record.
type requestIDHandler struct {
	inner slog.Handler
}

func (h *requestIDHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *requestIDHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := RequestIDFromContext(ctx); id != "" {
		r.AddAttrs(slog.String(RequestIDKey, id))
	}
	return h.inner.Handle(ctx, r)
}

func (h *requestIDHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &requestIDHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h *requestIDHandler) WithGroup(name string) slog.Handler {
	return &requestIDHandler{inner: h.inner.WithGroup(name)}
}
