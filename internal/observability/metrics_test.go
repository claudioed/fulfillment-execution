package observability_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/claudioed/fulfillment-execution/internal/domain/task"
	"github.com/claudioed/fulfillment-execution/internal/observability"
)

// installTestMeterProvider makes the global MeterProvider a manual-reader
// one, so a test can collect exactly what was recorded without a Collector.
func installTestMeterProvider(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(provider)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	return reader
}

// findMetric returns the collected metric with the given name.
func findMetric(t *testing.T, reader *sdkmetric.ManualReader, name string) metricdata.Metrics {
	t.Helper()

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name == name {
				return m
			}
		}
	}
	t.Fatalf("metric %q was never recorded", name)
	return metricdata.Metrics{}
}

func TestMetrics_CountsClaimedAndCompletedByTaskType(t *testing.T) {
	reader := installTestMeterProvider(t)

	metrics, err := observability.NewMetrics()
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}

	ctx := context.Background()
	metrics.TaskClaimed(ctx, task.Pick)
	metrics.TaskClaimed(ctx, task.Pick)
	metrics.TaskClaimed(ctx, task.Pack)
	metrics.TaskCompleted(ctx, task.Slam)

	claimed := findMetric(t, reader, "fulfillment.tasks.claimed")
	sum, ok := claimed.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("fulfillment.tasks.claimed is %T, want an int64 sum", claimed.Data)
	}

	byType := map[string]int64{}
	for _, dp := range sum.DataPoints {
		v, present := dp.Attributes.Value(attribute.Key("task.type"))
		if !present {
			t.Fatalf("data point has no task.type attribute: %v", dp.Attributes)
		}
		byType[v.AsString()] = dp.Value
	}

	if byType[string(task.Pick)] != 2 {
		t.Errorf("claimed[Pick] = %d, want 2", byType[string(task.Pick)])
	}
	if byType[string(task.Pack)] != 1 {
		t.Errorf("claimed[Pack] = %d, want 1", byType[string(task.Pack)])
	}

	completed := findMetric(t, reader, "fulfillment.tasks.completed")
	completedSum, ok := completed.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("fulfillment.tasks.completed is %T, want an int64 sum", completed.Data)
	}
	if len(completedSum.DataPoints) != 1 || completedSum.DataPoints[0].Value != 1 {
		t.Errorf("completed data points = %v, want a single point of 1", completedSum.DataPoints)
	}
}

// TestHTTPServerMetrics_RecordsRequestDurationByRoute checks the metric half
// of the HTTP instrumentation (otelchi only emits spans), and specifically
// that http.route carries the chi route pattern rather than the raw path --
// the difference between a bounded time series and a cardinality explosion.
func TestHTTPServerMetrics_RecordsRequestDurationByRoute(t *testing.T) {
	reader := installTestMeterProvider(t)

	r := chi.NewRouter()
	r.Use(observability.HTTPServerMetrics())
	r.Post("/tasks/{id}/complete", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/tasks/task-42/complete", "application/json", http.NoBody)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()

	duration := findMetric(t, reader, "http.server.request.duration")
	hist, ok := duration.Data.(metricdata.Histogram[float64])
	if !ok {
		t.Fatalf("http.server.request.duration is %T, want a float64 histogram", duration.Data)
	}
	if duration.Unit != "s" {
		t.Errorf("unit = %q, want %q", duration.Unit, "s")
	}
	if len(hist.DataPoints) != 1 {
		t.Fatalf("data points = %d, want 1", len(hist.DataPoints))
	}

	attrs := hist.DataPoints[0].Attributes
	route, ok := attrs.Value(attribute.Key("http.route"))
	if !ok {
		t.Fatalf("no http.route attribute: %v", attrs)
	}
	if route.AsString() != "/tasks/{id}/complete" {
		t.Errorf("http.route = %q, want the route pattern %q", route.AsString(), "/tasks/{id}/complete")
	}

	method, ok := attrs.Value(attribute.Key("http.request.method"))
	if !ok || method.AsString() != http.MethodPost {
		t.Errorf("http.request.method = %v, want POST", method)
	}
	status, ok := attrs.Value(attribute.Key("http.response.status_code"))
	if !ok || status.AsInt64() != http.StatusNoContent {
		t.Errorf("http.response.status_code = %v, want 204", status)
	}
}

func TestHTTPServerMetrics_UnmatchedRouteOmitsRouteAttribute(t *testing.T) {
	reader := installTestMeterProvider(t)

	r := chi.NewRouter()
	r.Use(observability.HTTPServerMetrics())
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/no/such/path")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()

	duration := findMetric(t, reader, "http.server.request.duration")
	hist := duration.Data.(metricdata.Histogram[float64])
	if len(hist.DataPoints) != 1 {
		t.Fatalf("data points = %d, want 1", len(hist.DataPoints))
	}
	if _, ok := hist.DataPoints[0].Attributes.Value(attribute.Key("http.route")); ok {
		t.Error("http.route recorded for an unmatched request; the raw path must never become an attribute")
	}
}
