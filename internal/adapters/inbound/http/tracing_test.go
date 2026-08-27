package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	stdhttp "net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/claudioed/fulfillment-execution/internal/adapters/inbound/http"
	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/memory"
	"github.com/claudioed/fulfillment-execution/internal/application/usecases"
	"github.com/claudioed/fulfillment-execution/internal/observability"
)

// TestRouter_RequestLogCarriesTraceCorrelation is the end-to-end form of the
// log/trace correlation requirement: a request through the real router must
// produce a span, and the request log line must name that span, so an
// operator can jump from a log line straight to its trace.
func TestRouter_RequestLogCarriesTraceCorrelation(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	var logs bytes.Buffer
	logger := slog.New(observability.NewSlogHandler(
		slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}),
	))

	router := http.NewRouter(&http.Handlers{
		GetQueueDepth: &usecases.GetQueueDepth{Tasks: memory.NewTaskRepo()},
	}, logger)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(stdhttp.MethodGet, "/queues/Pick/depth", nil))

	if rec.Code != stdhttp.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	ended := recorder.Ended()
	if len(ended) == 0 {
		t.Fatal("the request produced no span; otelchi is not wired into the router")
	}
	span := ended[0]

	// The span name uses the route pattern, not the raw path, so a task id
	// in the URL can never explode span cardinality.
	if span.Name() != "GET /queues/{taskType}/depth" {
		t.Errorf("span name = %q, want the route pattern %q", span.Name(), "GET /queues/{taskType}/depth")
	}
	if span.SpanKind() != trace.SpanKindServer {
		t.Errorf("span kind = %v, want server", span.SpanKind())
	}

	var rec2 map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &rec2); err != nil {
		t.Fatalf("request log is not valid JSON (%v): %s", err, logs.String())
	}
	if got := rec2["trace_id"]; got != span.SpanContext().TraceID().String() {
		t.Errorf("log trace_id = %v, want the request span's %v", got, span.SpanContext().TraceID())
	}
	if got := rec2["span_id"]; got != span.SpanContext().SpanID().String() {
		t.Errorf("log span_id = %v, want the request span's %v", got, span.SpanContext().SpanID())
	}
	if got := rec2["msg"]; got != "http request" {
		t.Errorf("log msg = %v, want %q", got, "http request")
	}
}
