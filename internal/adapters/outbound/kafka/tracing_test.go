package kafka_test

import (
	"context"
	"errors"
	"testing"

	kafkago "github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	outboundkafka "github.com/claudioed/fulfillment-execution/internal/adapters/outbound/kafka"
	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/memory"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
	"github.com/claudioed/fulfillment-execution/internal/observability"
)

// failingWriter stands in for a broker that rejects the write, so the
// publish span's error path can be asserted without a live Kafka.
type failingWriter struct{}

func (failingWriter) WriteMessages(context.Context, ...kafkago.Message) error {
	return errors.New("broker unavailable")
}

func installRecorder(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	return recorder
}

// TestPublish_InjectsTraceContextIntoMessageHeaders is the producer half of
// cross-service tracing: the published message must carry the W3C
// traceparent of the publish span, so whichever service consumes it can
// continue this trace instead of starting a new one.
func TestPublish_InjectsTraceContextIntoMessageHeaders(t *testing.T) {
	recorder := installRecorder(t)

	tasks := memory.NewTaskRepo()
	newTestTask(t, tasks, shared.OrderRef("wu-original"))

	w := &fakeWriter{}
	p := &outboundkafka.Publisher{Writer: w, Tasks: tasks, NewId: func() string { return "evt-1" }}

	callerCtx, callerSpan := otel.Tracer("caller").Start(context.Background(), "CompleteTask")

	if err := p.Publish(callerCtx, shared.NewTaskCompleted("task-1", "station-1", epoch)); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	callerSpan.End()

	if len(w.msgs) != 1 {
		t.Fatalf("published %d messages, want 1", len(w.msgs))
	}

	headers := w.msgs[0].Headers
	carrier := observability.KafkaHeaderCarrier{Headers: &headers}
	if carrier.Get("traceparent") == "" {
		t.Fatalf("no traceparent header on the published message: %v", headers)
	}

	// A consumer extracting from those headers lands in the same trace.
	extracted := trace.SpanContextFromContext(observability.ExtractKafkaTrace(context.Background(), headers))
	if !extracted.IsValid() {
		t.Fatal("the injected headers do not extract back to a valid span context")
	}
	if extracted.TraceID() != callerSpan.SpanContext().TraceID() {
		t.Errorf("published trace id = %s, want the caller's %s", extracted.TraceID(), callerSpan.SpanContext().TraceID())
	}

	var publishSpan bool
	for _, s := range recorder.Ended() {
		if s.Name() == "kafka.publish "+outboundkafka.Topic {
			publishSpan = true
			if s.SpanKind() != trace.SpanKindProducer {
				t.Errorf("span kind = %v, want producer", s.SpanKind())
			}
			if s.Parent().SpanID() != callerSpan.SpanContext().SpanID() {
				t.Errorf("parent = %s, want the caller span %s", s.Parent().SpanID(), callerSpan.SpanContext().SpanID())
			}
			// The traceparent must name the publish span itself, not its
			// caller, so the consumer hangs off the publish.
			if extracted.SpanID() != s.SpanContext().SpanID() {
				t.Errorf("traceparent names span %s, want the publish span %s", extracted.SpanID(), s.SpanContext().SpanID())
			}
		}
	}
	if !publishSpan {
		t.Error("no kafka.publish span recorded")
	}
}

func TestPublish_MarksTheSpanFailedWhenTheWriteFails(t *testing.T) {
	recorder := installRecorder(t)

	tasks := memory.NewTaskRepo()
	newTestTask(t, tasks, shared.OrderRef("wu-original"))

	p := &outboundkafka.Publisher{
		Writer: &failingWriter{},
		Tasks:  tasks,
		NewId:  func() string { return "evt-1" },
	}

	if err := p.Publish(context.Background(), shared.NewTaskCompleted("task-1", "station-1", epoch)); err == nil {
		t.Fatal("expected the write error to propagate")
	}

	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("recorded %d spans, want the publish span", len(ended))
	}
	if len(ended[0].Events()) == 0 {
		t.Error("the write error was not recorded on the publish span")
	}
}
