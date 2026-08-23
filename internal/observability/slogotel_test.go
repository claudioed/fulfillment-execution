package observability_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/claudioed/fulfillment-execution/internal/observability"
)

// newTestLogger returns a logger writing JSON through the trace-correlating
// handler, plus the buffer it writes to.
func newTestLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	handler := observability.NewSlogHandler(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return slog.New(handler), &buf
}

func decodeRecord(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()

	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &rec); err != nil {
		t.Fatalf("log line is not valid JSON (%v): %s", err, buf.String())
	}
	return rec
}

func TestSlogHandler_AttachesTraceIdsWhenSpanIsActive(t *testing.T) {
	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	ctx, span := tp.Tracer("test").Start(context.Background(), "unit-under-test")
	defer span.End()

	logger, buf := newTestLogger()
	logger.InfoContext(ctx, "http request", "status", 200)

	rec := decodeRecord(t, buf)

	wantTrace := span.SpanContext().TraceID().String()
	wantSpan := span.SpanContext().SpanID().String()

	if got := rec["trace_id"]; got != wantTrace {
		t.Errorf("trace_id = %v, want %v", got, wantTrace)
	}
	if got := rec["span_id"]; got != wantSpan {
		t.Errorf("span_id = %v, want %v", got, wantSpan)
	}
	if got := rec["status"]; got != float64(200) {
		t.Errorf("the wrapper dropped the caller's own attributes: status = %v", got)
	}
}

func TestSlogHandler_OmitsTraceIdsWithoutASpan(t *testing.T) {
	logger, buf := newTestLogger()
	logger.InfoContext(context.Background(), "http request")

	rec := decodeRecord(t, buf)

	if _, ok := rec["trace_id"]; ok {
		t.Errorf("trace_id present without an active span: %v", rec)
	}
	if _, ok := rec["span_id"]; ok {
		t.Errorf("span_id present without an active span: %v", rec)
	}
}

// TestSlogHandler_SurvivesWithAttrsAndWithGroup guards the easy mistake of
// embedding the wrapped handler: With/WithGroup must re-wrap, or a
// logger.With(...) silently loses trace correlation.
func TestSlogHandler_SurvivesWithAttrsAndWithGroup(t *testing.T) {
	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	ctx, span := tp.Tracer("test").Start(context.Background(), "unit-under-test")
	defer span.End()

	t.Run("WithAttrs", func(t *testing.T) {
		logger, buf := newTestLogger()
		logger.With("component", "kafka").InfoContext(ctx, "consumed")

		rec := decodeRecord(t, buf)
		if got := rec["trace_id"]; got != span.SpanContext().TraceID().String() {
			t.Errorf("trace_id after With() = %v, want %v", got, span.SpanContext().TraceID())
		}
		if got := rec["component"]; got != "kafka" {
			t.Errorf("component = %v, want kafka", got)
		}
	})

	t.Run("WithGroup", func(t *testing.T) {
		logger, buf := newTestLogger()
		logger.WithGroup("request").InfoContext(ctx, "served")

		rec := decodeRecord(t, buf)
		// The trace ids are added to the record after grouping, so they stay
		// inside the group -- what matters is that they are still emitted.
		group, ok := rec["request"].(map[string]any)
		if !ok {
			t.Fatalf("expected a %q group in the record: %v", "request", rec)
		}
		if got := group["trace_id"]; got != span.SpanContext().TraceID().String() {
			t.Errorf("trace_id after WithGroup() = %v, want %v", got, span.SpanContext().TraceID())
		}
	})

	t.Run("empty group name is a no-op", func(t *testing.T) {
		logger, buf := newTestLogger()
		logger.WithGroup("").InfoContext(ctx, "served")

		rec := decodeRecord(t, buf)
		if got := rec["trace_id"]; got != span.SpanContext().TraceID().String() {
			t.Errorf("trace_id after WithGroup(\"\") = %v, want %v", got, span.SpanContext().TraceID())
		}
	})
}

func TestNewSlogHandler_NilNextFallsBackToDefault(t *testing.T) {
	if h := observability.NewSlogHandler(nil); h == nil {
		t.Fatal("NewSlogHandler(nil) returned nil")
	}
}

func TestSlogHandler_EnabledDelegates(t *testing.T) {
	handler := observability.NewSlogHandler(slog.NewJSONHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelWarn}))

	if handler.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("Enabled(Info) = true for a Warn-level handler; the wrapper must delegate")
	}
	if !handler.Enabled(context.Background(), slog.LevelError) {
		t.Error("Enabled(Error) = false for a Warn-level handler; the wrapper must delegate")
	}
}
