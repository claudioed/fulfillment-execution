// Package kafka provides the outbound adapter that publishes
// fulfillment-execution domain events onto Kafka, satisfying
// ports.EventPublisher. It currently forwards TaskCompleted only, enriching
// it with the completed Task's OrderRef (looked up via TaskRepo, since the
// domain event itself carries only TaskId/StationId) — the same
// repo-lookup-enrichment pattern inventory-storage's Kafka publisher uses
// for ReservationRevoked.
package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/fulfillment-execution/internal/application/ports"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
)

// Topic is the topic fulfillment-execution publishes its outbound
// integration events to.
const Topic = "warehouse.fulfillment.events"

// Envelope is the CloudEvents-like wrapper shared across all four
// warehouse-systems services.
type Envelope struct {
	EventId    string            `json:"event_id"`
	EventType  string            `json:"event_type"`
	OccurredAt time.Time         `json:"occurred_at"`
	Source     string            `json:"source"`
	Data       TaskCompletedData `json:"data"`
}

// TaskCompletedData is the payload of a published TaskCompleted event,
// enriched with the completed Task's OrderRef as work_unit_id so Work
// Planning can call RecordCompletion(WorkUnitId).
type TaskCompletedData struct {
	TaskId     string `json:"task_id"`
	StationId  string `json:"station_id"`
	WorkUnitId string `json:"work_unit_id"`
}

// Writer is the subset of *kafkago.Writer the Publisher needs, so tests can
// substitute a fake without a live broker.
type Writer interface {
	WriteMessages(ctx context.Context, msgs ...kafkago.Message) error
}

// Publisher publishes fulfillment-execution domain events onto Kafka. It
// satisfies ports.EventPublisher. Event types other than TaskCompleted are
// not yet part of the published integration contract and are skipped.
type Publisher struct {
	Writer Writer
	Tasks  ports.TaskRepo
	NewId  func() string
}

// NewPublisher constructs a Publisher writing to Topic on brokers.
func NewPublisher(brokers []string, tasks ports.TaskRepo, newId func() string) *Publisher {
	return &Publisher{
		Writer: &kafkago.Writer{
			Addr:                   kafkago.TCP(brokers...),
			Topic:                  Topic,
			Balancer:               &kafkago.LeastBytes{},
			AllowAutoTopicCreation: true,
		},
		Tasks: tasks,
		NewId: newId,
	}
}

// Publish forwards every TaskCompleted event in evts onto Kafka, enriched
// with the completed Task's OrderRef via a TaskRepo lookup.
func (p *Publisher) Publish(ctx context.Context, evts ...shared.DomainEvent) error {
	for _, e := range evts {
		tc, ok := e.(shared.TaskCompleted)
		if !ok {
			continue
		}

		t, err := p.Tasks.FindById(ctx, tc.TaskId)
		if err != nil {
			return fmt.Errorf("kafka: lookup task %s for enrichment: %w", tc.TaskId, err)
		}
		var workUnitId string
		if t != nil {
			workUnitId = string(t.OrderRef())
		}

		env := Envelope{
			EventId:    p.NewId(),
			EventType:  "TaskCompleted",
			OccurredAt: tc.OccurredAt(),
			Source:     "fulfillment-execution",
			Data: TaskCompletedData{
				TaskId:     string(tc.TaskId),
				StationId:  string(tc.StationId),
				WorkUnitId: workUnitId,
			},
		}
		payload, err := json.Marshal(env)
		if err != nil {
			return fmt.Errorf("kafka: marshal envelope: %w", err)
		}
		if err := p.Writer.WriteMessages(ctx, kafkago.Message{Key: []byte(tc.TaskId), Value: payload}); err != nil {
			return fmt.Errorf("kafka: publish TaskCompleted: %w", err)
		}
	}
	return nil
}

// Close releases the underlying Kafka writer.
func (p *Publisher) Close() error {
	if w, ok := p.Writer.(*kafkago.Writer); ok {
		return w.Close()
	}
	return nil
}
