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
	"log"
	"strings"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/fulfillment-execution/internal/application/ports"
	"github.com/claudioed/fulfillment-execution/internal/application/usecases"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
	"github.com/claudioed/fulfillment-execution/internal/domain/task"
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
}

// Consumer reads WorkReleased events off warehouse.work-planning.events and
// creates a Task for each one, exactly once per event_id despite Kafka's
// at-least-once delivery.
type Consumer struct {
	Reader     *kafkago.Reader
	CreateTask *usecases.CreateTask
	Processed  ports.ProcessedEvents
	Logger     *log.Logger
}

// NewConsumer constructs a Consumer reading topic from brokers as part of
// consumer group "fulfillment-execution".
func NewConsumer(brokers []string, topic string, createTask *usecases.CreateTask, processed ports.ProcessedEvents, logger *log.Logger) *Consumer {
	if logger == nil {
		logger = log.Default()
	}
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers: brokers,
		Topic:   topic,
		GroupID: "fulfillment-execution",
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
		if err := c.HandleMessage(ctx, msg.Value); err != nil {
			c.Logger.Printf("kafka: failed to handle message: %v", err)
		}
	}
}

// Close releases the underlying Kafka reader.
func (c *Consumer) Close() error {
	return c.Reader.Close()
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

	if _, err := c.CreateTask.Execute(ctx, taskType, shared.NewCPT(env.Data.CPT), orderRef, required); err != nil {
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
