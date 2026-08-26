package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/claudioed/fulfillment-execution/internal/analytics/report"
	"github.com/claudioed/fulfillment-execution/internal/application/ports"
	"github.com/claudioed/fulfillment-execution/internal/observability"
)

// AnalyticsConsumerGroup is the Kafka consumer group the analytics projector
// reads under. It is distinct from the OLTP consumer group so the two
// pipelines track their offsets independently.
const AnalyticsConsumerGroup = "fulfillment-analytics"

// analyticsEnvelope is the inbound decode shape of the Envelope v1 wrapper on
// the analytics topic. The data payload is left as a RawMessage and decoded
// per event_type. It is declared here (rather than imported from the outbound
// publisher) so this inbound adapter does not depend on an outbound adapter.
type analyticsEnvelope struct {
	EventId       string          `json:"event_id"`
	EventType     string          `json:"event_type"`
	OccurredAt    time.Time       `json:"occurred_at"`
	Source        string          `json:"source"`
	SchemaVersion int             `json:"schema_version"`
	Data          json.RawMessage `json:"data"`
}

// analyticsData is the union of fields the projecting event payloads carry.
// Each event_type populates the subset it needs. TaskType is enriched onto
// task-scoped events by the publisher (via a TaskRepo lookup) since the domain
// events do not carry it.
type analyticsData struct {
	TaskId    string `json:"task_id"`
	TaskType  string `json:"task_type"`
	StationId string `json:"station_id"`
	PackageId string `json:"package_id"`
}

// AnalyticsConsumer reads analytics events off the analytics topic and
// applies each to the throughput ProjectionStore, exactly once per event_id
// despite Kafka's at-least-once delivery.
type AnalyticsConsumer struct {
	Reader     *kafkago.Reader
	Projection report.ProjectionStore
	Processed  ports.ProcessedEvents
	Logger     *slog.Logger
}

// NewAnalyticsConsumer constructs an AnalyticsConsumer reading topic from
// brokers under AnalyticsConsumerGroup.
func NewAnalyticsConsumer(brokers []string, topic string, projection report.ProjectionStore, processed ports.ProcessedEvents, logger *slog.Logger) *AnalyticsConsumer {
	if logger == nil {
		logger = slog.Default()
	}
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers: brokers,
		Topic:   topic,
		GroupID: AnalyticsConsumerGroup,
	})
	return &AnalyticsConsumer{Reader: reader, Projection: projection, Processed: processed, Logger: logger}
}

// Run reads and handles messages until ctx is cancelled or the reader
// returns a fatal error. A handling error is logged and the loop continues
// so one bad message cannot wedge the projector.
func (c *AnalyticsConsumer) Run(ctx context.Context) error {
	for {
		msg, err := c.Reader.ReadMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
		if err := c.Handle(ctx, msg); err != nil {
			c.Logger.ErrorContext(ctx, "analytics message handling failed", "error", err)
		}
	}
}

// Close releases the underlying Kafka reader.
func (c *AnalyticsConsumer) Close() error {
	return c.Reader.Close()
}

// Handle processes one consumed message inside a "kafka.consume <topic>"
// span whose parent is the producer's span, read from the message headers.
// It is exported separately from Run so the propagation can be tested without
// a live broker.
func (c *AnalyticsConsumer) Handle(ctx context.Context, msg kafkago.Message) error {
	ctx = observability.ExtractKafkaTrace(ctx, msg.Headers)

	ctx, span := otel.Tracer(observability.InstrumentationName).Start(ctx,
		"kafka.consume "+msg.Topic,
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			semconv.MessagingSystemKafka,
			semconv.MessagingDestinationName(msg.Topic),
			semconv.MessagingOperationName("process"),
		),
	)
	defer span.End()

	if err := c.HandleMessage(ctx, msg.Value); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	return nil
}

// HandleMessage decodes raw as an analyticsEnvelope and applies the matching
// projection method for its event_type. Event types outside the projection
// contract are ignored (and not marked processed). For a projecting event it
// dedupes on event_id via ProcessedEvents before applying, so a redelivery is
// a no-op. It is exported separately from Run so tests can feed raw envelopes
// without a live broker.
func (c *AnalyticsConsumer) HandleMessage(ctx context.Context, raw []byte) error {
	var env analyticsEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("analytics: decode envelope: %w", err)
	}

	// Only the four throughput-moving events project; everything else
	// (TaskCreated, ItemPicked, PackageSealed, LabelApplied, PackageDiverted)
	// is acknowledged without touching the read model or the processed set.
	switch env.EventType {
	case "TaskClaimed", "TaskCompleted", "LeaseExpired", "WeightDiscrepancyDetected":
	default:
		return nil
	}

	isNew, err := c.Processed.MarkProcessed(ctx, env.EventId)
	if err != nil {
		return fmt.Errorf("analytics: mark processed: %w", err)
	}
	if !isNew {
		return nil
	}

	var data analyticsData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return fmt.Errorf("analytics: decode data: %w", err)
	}

	switch env.EventType {
	case "TaskClaimed":
		return c.Projection.ApplyTaskClaimed(ctx, env.EventId, data.TaskId, data.TaskType, data.StationId, env.OccurredAt)
	case "TaskCompleted":
		return c.Projection.ApplyTaskCompleted(ctx, env.EventId, data.TaskId, data.TaskType, data.StationId, env.OccurredAt)
	case "LeaseExpired":
		return c.Projection.ApplyLeaseExpired(ctx, env.EventId, data.TaskId, data.TaskType, data.StationId, env.OccurredAt)
	case "WeightDiscrepancyDetected":
		return c.Projection.ApplyWeightDiscrepancy(ctx, env.EventId, taskTypeSlam, data.StationId, env.OccurredAt)
	default:
		return nil
	}
}

// taskTypeSlam is the process path a weigh-check divert belongs to: SLAM.
const taskTypeSlam = "SLAM"
