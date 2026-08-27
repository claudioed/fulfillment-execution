package observability_test

import (
	"context"
	"testing"

	kafkago "github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/claudioed/fulfillment-execution/internal/observability"
)

func TestKafkaHeaderCarrier_GetSetKeys(t *testing.T) {
	headers := []kafkago.Header{{Key: "existing", Value: []byte("value")}}
	carrier := observability.KafkaHeaderCarrier{Headers: &headers}

	if got := carrier.Get("existing"); got != "value" {
		t.Errorf("Get(existing) = %q, want %q", got, "value")
	}
	if got := carrier.Get("EXISTING"); got != "value" {
		t.Errorf("Get is case-sensitive; got %q for a differently-cased key", got)
	}
	if got := carrier.Get("absent"); got != "" {
		t.Errorf("Get(absent) = %q, want empty", got)
	}

	carrier.Set("traceparent", "abc")
	if got := carrier.Get("traceparent"); got != "abc" {
		t.Errorf("Set then Get = %q, want %q", got, "abc")
	}
	if len(headers) != 2 {
		t.Fatalf("Set appended %d headers, want 1 new one: %v", len(headers)-1, headers)
	}

	// Setting the same key again replaces rather than duplicating -- a
	// duplicated traceparent is ambiguous to a consumer.
	carrier.Set("traceparent", "def")
	if len(headers) != 2 {
		t.Errorf("re-Set duplicated the header: %v", headers)
	}
	if got := carrier.Get("traceparent"); got != "def" {
		t.Errorf("re-Set left %q, want %q", got, "def")
	}

	keys := carrier.Keys()
	if len(keys) != 2 || keys[0] != "existing" || keys[1] != "traceparent" {
		t.Errorf("Keys() = %v, want [existing traceparent]", keys)
	}
}

func TestKafkaHeaderCarrier_NilHeadersIsSafe(t *testing.T) {
	carrier := observability.KafkaHeaderCarrier{}

	if got := carrier.Get("traceparent"); got != "" {
		t.Errorf("Get on a nil carrier = %q, want empty", got)
	}
	if keys := carrier.Keys(); keys != nil {
		t.Errorf("Keys on a nil carrier = %v, want nil", keys)
	}
	carrier.Set("traceparent", "abc") // must not panic
}

// TestKafkaTracePropagation_RoundTrip is the cross-service propagation proof
// that does not need a broker: the trace context a producer injects into
// message headers is what a consumer extracts, so the consumer's span is a
// child of the producer's.
func TestKafkaTracePropagation_RoundTrip(t *testing.T) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	producerCtx, producerSpan := tp.Tracer("producer").Start(context.Background(), "kafka.publish test-topic")
	defer producerSpan.End()

	var headers []kafkago.Header
	observability.InjectKafkaTrace(producerCtx, &headers)

	if len(headers) == 0 {
		t.Fatal("InjectKafkaTrace wrote no headers")
	}
	var sawTraceparent bool
	for _, h := range headers {
		if h.Key == "traceparent" {
			sawTraceparent = true
		}
	}
	if !sawTraceparent {
		t.Fatalf("no W3C traceparent header injected: %v", headers)
	}

	// A fresh context on the consumer side, exactly as a consumer that
	// never shared memory with the producer would have.
	consumerCtx := observability.ExtractKafkaTrace(context.Background(), headers)
	extracted := trace.SpanContextFromContext(consumerCtx)

	if !extracted.IsValid() {
		t.Fatal("ExtractKafkaTrace produced no valid span context")
	}
	if extracted.TraceID() != producerSpan.SpanContext().TraceID() {
		t.Errorf("trace id = %s, want the producer's %s", extracted.TraceID(), producerSpan.SpanContext().TraceID())
	}
	if extracted.SpanID() != producerSpan.SpanContext().SpanID() {
		t.Errorf("parent span id = %s, want the producer's %s", extracted.SpanID(), producerSpan.SpanContext().SpanID())
	}

	// And a span started on that context really is a child of the producer.
	_, consumerSpan := tp.Tracer("consumer").Start(consumerCtx, "kafka.consume test-topic")
	defer consumerSpan.End()

	if consumerSpan.SpanContext().TraceID() != producerSpan.SpanContext().TraceID() {
		t.Errorf("consumer span joined trace %s, want the producer's %s",
			consumerSpan.SpanContext().TraceID(), producerSpan.SpanContext().TraceID())
	}
}

func TestExtractKafkaTrace_NoHeadersLeavesContextUntouched(t *testing.T) {
	ctx := observability.ExtractKafkaTrace(context.Background(), nil)

	if trace.SpanContextFromContext(ctx).IsValid() {
		t.Error("extracting from no headers produced a span context out of nowhere")
	}
}
