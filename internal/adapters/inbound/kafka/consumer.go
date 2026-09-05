// Package kafka provides the inbound adapter that consumes WorkReleased
// events from Work Planning and turns each one into a Task via the existing
// CreateTask use case — the intended use of that use case, so it is called
// directly rather than through a new one.
package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/claudioed/fulfillment-execution/internal/application/ports"
	"github.com/claudioed/fulfillment-execution/internal/application/usecases"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
	"github.com/claudioed/fulfillment-execution/internal/domain/task"
	"github.com/claudioed/fulfillment-execution/internal/observability"
)

// Envelope is the CloudEvents-like wrapper shared across all four
// warehouse-systems services.
type Envelope struct {
	EventId    string           `json:"event_id"`
	EventType  string           `json:"event_type"`
	OccurredAt time.Time        `json:"occurred_at"`
	Source     string           `json:"source"`
	Data       WorkReleasedData `json:"data"`
}

// WorkReleasedData is the payload of a WorkReleased event.
type WorkReleasedData struct {
	PathId     string    `json:"path_id"`
	WorkUnitId string    `json:"work_unit_id"`
	CPT        time.Time `json:"cpt"`
	Ref        string    `json:"ref"`
	// Fragile is an optional packing hint set by wes-work-planning at
	// release time, sourced from inventory-storage's ProductClassification
	// (true if the upstream order line was classified Fragile). It is
	// omitted, not required: any already-documented producer that predates
	// this field simply does not send it, and it defaults to false — a
	// known simplification for this round, matching the existing path_id
	// prefix convention (see README's Integration section).
	Fragile bool `json:"fragile"`
	// GiftWrap is an optional packing hint set by wes-work-planning at
	// work-enqueue time — a caller-stated request that this order's
	// package be gift-wrapped, not a product classification. It is
	// omitted, not required, and never published as explicit false: any
	// producer that does not carry a gift-wrap request for the order
	// simply omits the field, and it defaults to false (see ADR-0011).
	GiftWrap bool `json:"gift_wrap"`
}

// Consumer reads WorkReleased events off warehouse.work-planning.events and
// creates a Task for each one, exactly once per event_id despite Kafka's
// at-least-once delivery.
type Consumer struct {
	Reader     *kafkago.Reader
	CreateTask *usecases.CreateTask
	Processed  ports.ProcessedEvents
	Logger     *slog.Logger
}

// NewConsumer constructs a Consumer reading topic from brokers as part of
// consumer group "fulfillment-execution".
func NewConsumer(brokers []string, topic string, createTask *usecases.CreateTask, processed ports.ProcessedEvents, logger *slog.Logger) *Consumer {
	return newConsumer(brokers, topic, "fulfillment-execution", kafkago.FirstOffset, createTask, processed, logger)
}

// NewConsumerWithGroup constructs an isolated Consumer. Its first assignment
// begins at the latest offset, so a system-test database is populated only by
// events released after the test's service process is ready.
func NewConsumerWithGroup(brokers []string, topic, groupID string, createTask *usecases.CreateTask, processed ports.ProcessedEvents, logger *slog.Logger) *Consumer {
	return newConsumer(brokers, topic, groupID, kafkago.LastOffset, createTask, processed, logger)
}

func newConsumer(brokers []string, topic, groupID string, startOffset int64, createTask *usecases.CreateTask, processed ports.ProcessedEvents, logger *slog.Logger) *Consumer {
	if logger == nil {
		logger = slog.Default()
	}
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:     brokers,
		Topic:       topic,
		GroupID:     groupID,
		StartOffset: startOffset,
	})
	return &Consumer{Reader: reader, CreateTask: createTask, Processed: processed, Logger: logger}
}

// Run reads and handles messages until ctx is cancelled or the reader
// returns a fatal error. A handling error is logged and the loop continues
// so one bad message cannot wedge the consumer.
func (c *Consumer) Run(ctx context.Context) error {
	for {
		msg, err := c.Reader.ReadMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
		if err := c.Handle(ctx, msg); err != nil {
			c.Logger.ErrorContext(ctx, "kafka message handling failed", "error", err)
		}
	}
}

// Close releases the underlying Kafka reader.
func (c *Consumer) Close() error {
	return c.Reader.Close()
}

// Handle processes one consumed message inside a
// "kafka.consume <topic>" span whose parent is the producer's span, read
// from the message headers. That link is what makes a WorkReleased published
// by wes-work-planning and the Task created here parts of a single
// distributed trace.
//
// It is exported separately from Run so the propagation can be tested
// without a live broker.
func (c *Consumer) Handle(ctx context.Context, msg kafkago.Message) error {
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

// HandleMessage decodes raw as an Envelope and, if it is a not-yet-processed
// WorkReleased event, creates a Task via CreateTask. It is exported
// separately from Run so tests can feed it a fake envelope without a live
// broker.
func (c *Consumer) HandleMessage(ctx context.Context, raw []byte) error {
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("kafka: decode envelope: %w", err)
	}
	if env.EventType != "WorkReleased" {
		return nil
	}

	isNew, err := c.Processed.MarkProcessed(ctx, env.EventId)
	if err != nil {
		return fmt.Errorf("kafka: mark processed: %w", err)
	}
	if !isNew {
		// Already applied by a prior delivery of this event_id; ack without
		// creating a duplicate Task.
		return nil
	}

	taskType := deriveTaskType(env.Data.PathId)
	required := requiredCapabilities(taskType)
	orderRef := shared.OrderRef(env.Data.WorkUnitId)

	if _, err := c.CreateTask.Execute(ctx, taskType, shared.NewCPT(env.Data.CPT), orderRef, required, env.Data.Fragile, env.Data.GiftWrap); err != nil {
		return fmt.Errorf("kafka: create task: %w", err)
	}
	return nil
}

// deriveTaskType maps a path_id to a task type by prefix convention
// ("pick-*", "pack-*", "slam-*"), defaulting to Pick when no prefix matches.
// path_id does not carry the task type in general — this prefix convention
// is a known simplification for this integration (see README).
func deriveTaskType(pathId string) task.Type {
	switch {
	case strings.HasPrefix(pathId, "pick-"):
		return task.Pick
	case strings.HasPrefix(pathId, "pack-"):
		return task.Pack
	case strings.HasPrefix(pathId, "slam-"):
		return task.Slam
	default:
		return task.Pick
	}
}

// requiredCapabilities maps a task type to the capability names Workforce
// Management uses.
func requiredCapabilities(t task.Type) shared.CapabilitySet {
	switch t {
	case task.Pack:
		return shared.NewCapabilitySet("pack")
	case task.Slam:
		return shared.NewCapabilitySet("slam")
	default:
		return shared.NewCapabilitySet("pick")
	}
}
