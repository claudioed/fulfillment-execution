package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/claudioed/fulfillment-execution/internal/application/ports"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
	"github.com/claudioed/fulfillment-execution/internal/observability"
)

// AnalyticsTopic is the dedicated topic the analytics data product consumes.
// It is separate from the integration topic (Topic) so the OLTP integration
// contract and the analytical read-model stream evolve independently
// (ADR-0012).
const AnalyticsTopic = "warehouse.fulfillment.analytics"

// analyticsSchemaVersion is the schema version stamped onto every analytics
// envelope this publisher emits.
const analyticsSchemaVersion = 1

// AnalyticsEnvelope is the shared Envelope v1 wrapper for the analytics
// stream. Unlike the integration Envelope it carries the payload as a
// json.RawMessage so a single publisher can emit the event_type-specific
// data object for all nine domain events without a bespoke struct per type.
type AnalyticsEnvelope struct {
	EventId       string          `json:"event_id"`
	EventType     string          `json:"event_type"`
	OccurredAt    time.Time       `json:"occurred_at"`
	Source        string          `json:"source"`
	SchemaVersion int             `json:"schema_version"`
	Data          json.RawMessage `json:"data"`
}

// AnalyticsPublisher publishes every fulfillment-execution domain event onto
// AnalyticsTopic as an AnalyticsEnvelope. It satisfies ports.EventPublisher
// and is a SEPARATE adapter from Publisher: the integration publisher
// (publisher.go) forwards only TaskCompleted and is left untouched.
type AnalyticsPublisher struct {
	Writer Writer
	NewId  func() string
}

// NewAnalyticsPublisher constructs an AnalyticsPublisher writing to
// AnalyticsTopic on brokers. newId mints the envelope event_id.
func NewAnalyticsPublisher(brokers []string, newId func() string) *AnalyticsPublisher {
	return &AnalyticsPublisher{
		Writer: &kafkago.Writer{
			Addr:                   kafkago.TCP(brokers...),
			Topic:                  AnalyticsTopic,
			Balancer:               &kafkago.LeastBytes{},
			AllowAutoTopicCreation: true,
		},
		NewId: newId,
	}
}

// Publish emits every event in evts onto AnalyticsTopic. Events with no
// analytics payload (an unrecognised type) are skipped rather than erroring,
// so the caller can hand it the full event stream indiscriminately.
func (p *AnalyticsPublisher) Publish(ctx context.Context, evts ...shared.DomainEvent) error {
	for _, e := range evts {
		eventType, key, data, ok := marshalData(e)
		if !ok {
			continue
		}
		env := AnalyticsEnvelope{
			EventId:       p.NewId(),
			EventType:     eventType,
			OccurredAt:    e.OccurredAt(),
			Source:        "fulfillment-execution",
			SchemaVersion: analyticsSchemaVersion,
			Data:          data,
		}
		payload, err := json.Marshal(env)
		if err != nil {
			return fmt.Errorf("kafka: marshal analytics envelope: %w", err)
		}
		if err := p.write(ctx, eventType, key, payload); err != nil {
			return err
		}
	}
	return nil
}

// marshalData maps a domain event to its analytics event_type, aggregate-id
// message key, and snake_case JSON payload. The bool return is false for an
// event type outside the analytics contract, so Publish can skip it.
func marshalData(e shared.DomainEvent) (eventType, key string, data json.RawMessage, ok bool) {
	switch ev := e.(type) {
	case shared.TaskCreated:
		return "TaskCreated", string(ev.TaskId), mustMarshal(map[string]any{
			"task_id": string(ev.TaskId),
		}), true
	case shared.TaskClaimed:
		return "TaskClaimed", string(ev.TaskId), mustMarshal(map[string]any{
			"task_id":    string(ev.TaskId),
			"station_id": string(ev.StationId),
		}), true
	case shared.LeaseExpired:
		return "LeaseExpired", string(ev.TaskId), mustMarshal(map[string]any{
			"task_id": string(ev.TaskId),
		}), true
	case shared.TaskCompleted:
		return "TaskCompleted", string(ev.TaskId), mustMarshal(map[string]any{
			"task_id":    string(ev.TaskId),
			"station_id": string(ev.StationId),
		}), true
	case shared.ItemPicked:
		return "ItemPicked", string(ev.TaskId), mustMarshal(map[string]any{
			"task_id": string(ev.TaskId),
		}), true
	case shared.PackageSealed:
		return "PackageSealed", string(ev.PackageId), mustMarshal(map[string]any{
			"package_id": string(ev.PackageId),
		}), true
	case shared.WeightDiscrepancyDetected:
		return "WeightDiscrepancyDetected", string(ev.PackageId), mustMarshal(map[string]any{
			"package_id": string(ev.PackageId),
			"expected_g": ev.ExpectedWeight,
			"actual_g":   ev.ActualWeight,
		}), true
	case shared.LabelApplied:
		return "LabelApplied", string(ev.PackageId), mustMarshal(map[string]any{
			"package_id": string(ev.PackageId),
		}), true
	case shared.PackageDiverted:
		return "PackageDiverted", string(ev.PackageId), mustMarshal(map[string]any{
			"package_id": string(ev.PackageId),
		}), true
	default:
		return "", "", nil, false
	}
}

// mustMarshal marshals a map whose shape is fully controlled by marshalData,
// so an error here is a programming mistake rather than a runtime condition.
func mustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("kafka: marshal analytics data: %v", err))
	}
	return b
}

// write publishes one already-marshalled envelope inside a
// "kafka.publish <topic>" producer span, injecting that span's context into
// the message headers so the projector's consume span becomes its child.
func (p *AnalyticsPublisher) write(ctx context.Context, eventType, key string, payload []byte) error {
	ctx, span := otel.Tracer(observability.InstrumentationName).Start(ctx,
		"kafka.publish "+AnalyticsTopic,
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			semconv.MessagingSystemKafka,
			semconv.MessagingDestinationName(AnalyticsTopic),
			semconv.MessagingOperationName("publish"),
		),
	)
	defer span.End()

	msg := kafkago.Message{Key: []byte(key), Value: payload}
	observability.InjectKafkaTrace(ctx, &msg.Headers)

	if err := p.Writer.WriteMessages(ctx, msg); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("kafka: publish %s analytics event: %w", eventType, err)
	}
	return nil
}

// Close releases the underlying Kafka writer.
func (p *AnalyticsPublisher) Close() error {
	if w, ok := p.Writer.(*kafkago.Writer); ok {
		return w.Close()
	}
	return nil
}

// Compile-time assertion that AnalyticsPublisher satisfies the outbound
// event-publishing port.
var _ ports.EventPublisher = (*AnalyticsPublisher)(nil)
