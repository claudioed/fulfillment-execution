package observability

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// TraceHandler is a slog.Handler that stamps every record logged with an
// active span in its context with that span's trace_id and span_id, so a log
// line can be correlated with the trace it belongs to. Records logged
// without an active span pass through untouched.
//
// It only sees the context for the *Context flavours of the slog API
// (logger.InfoContext, logger.ErrorContext, ...); plain logger.Info carries
// no context and so is never stamped.
type TraceHandler struct {
	next slog.Handler
}

// NewSlogHandler wraps next so its records carry trace correlation
// attributes. A nil next yields a nil handler-safe no-op wrapper around
// slog.Default()'s handler.
func NewSlogHandler(next slog.Handler) slog.Handler {
	if next == nil {
		next = slog.Default().Handler()
	}
	return TraceHandler{next: next}
}

func (h TraceHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h TraceHandler) Handle(ctx context.Context, rec slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		rec = rec.Clone()
		rec.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.next.Handle(ctx, rec)
}

// WithAttrs and WithGroup re-wrap the derived handler; without them a
// logger.With(...) call would silently drop the trace correlation.
func (h TraceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return TraceHandler{next: h.next.WithAttrs(attrs)}
}

func (h TraceHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return TraceHandler{next: h.next.WithGroup(name)}
}
