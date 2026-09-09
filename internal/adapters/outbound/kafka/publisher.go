// Package kafka provides the outbound adapter that publishes
// fulfillment-execution domain events onto Kafka, satisfying
// ports.EventPublisher. It currently forwards TaskCompleted only, enriching
// it with the completed Task's OrderRef (looked up via TaskRepo, since the
// domain event itself carries only TaskId/StationId) — the same
// repo-lookup-enrichment pattern inventory-storage's Kafka publisher uses
// for ReservationRevoked. It also enriches TaskCompleted with the
// completing associate's identity and the task's duration, resolved via a
// StationRepo lookup and the Task's ClaimedAt timestamp respectively (see
// ADR-0014) — inputs the labor-performance bounded context needs and that
// this service is the sole owner of.
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
// Planning can call RecordCompletion(WorkUnitId), plus two
// labor-performance-relevant facts resolved at publish time (ADR-0014):
// AssociateId (the occupant of the claiming station, if any — empty for a
// station with no checked-in occupant, e.g. a robot) and DurationSeconds
// (elapsed time between the task's claim and its completion).
type TaskCompletedData struct {
	TaskId          string `json:"task_id"`
	StationId       string `json:"station_id"`
	WorkUnitId      string `json:"work_unit_id"`
	AssociateId     string `json:"associate_id,omitempty"`
	DurationSeconds int64  `json:"duration_seconds,omitempty"`
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
	Writer   Writer
	Tasks    ports.TaskRepo
	Stations ports.StationRepo
	NewId    func() string
}

// NewPublisher constructs a Publisher writing to Topic on brokers.
func NewPublisher(brokers []string, tasks ports.TaskRepo, stations ports.StationRepo, newId func() string) *Publisher {
	return NewPublisherWithWriter(&kafkago.Writer{
		Addr:                   kafkago.TCP(brokers...),
		Topic:                  Topic,
		Balancer:               &kafkago.LeastBytes{},
		AllowAutoTopicCreation: true,
	}, tasks, stations, newId)
}

// NewPublisherWithWriter constructs a Publisher over an explicit Writer.
// When used only as an Encoder (the outbox configuration, ADR 0020) the
// writer may be nil — Encode never touches it.
func NewPublisherWithWriter(w Writer, tasks ports.TaskRepo, stations ports.StationRepo, newId func() string) *Publisher {
	return &Publisher{Writer: w, Tasks: tasks, Stations: stations, NewId: newId}
}

// Encode builds the wire form of every TaskCompleted event in evts — the
// envelope enriched with the completed Task's OrderRef, the completing
// associate's identity and the task's duration — WITHOUT sending it.
// Other event types are not part of the integration contract and yield
// nothing. The repo lookups happen here, so when Encode runs inside a use
// case's transaction (the outbox path) they see the just-saved rows.
//
// The W3C trace context of whatever span is active on ctx is injected
// into each message's headers, so the consumer's span becomes a child of
// the caller's — the publish span when called through Publish, the use
// case's span when called by the outbox publisher.
func (p *Publisher) Encode(ctx context.Context, evts ...shared.DomainEvent) ([]Encoded, error) {
	var out []Encoded
	for _, e := range evts {
		tc, ok := e.(shared.TaskCompleted)
		if !ok {
			continue
		}

		t, err := p.Tasks.FindById(ctx, tc.TaskId)
		if err != nil {
			return nil, fmt.Errorf("kafka: lookup task %s for enrichment: %w", tc.TaskId, err)
		}
		var workUnitId string
		var durationSeconds int64
		if t != nil {
			workUnitId = string(t.OrderRef())
			if claimedAt := t.ClaimedAt(); claimedAt != nil {
				durationSeconds = int64(tc.OccurredAt().Sub(*claimedAt).Seconds())
			}
		}

		associateId, err := p.associateId(ctx, tc.StationId)
		if err != nil {
			return nil, fmt.Errorf("kafka: lookup station %s for enrichment: %w", tc.StationId, err)
		}

		env := Envelope{
			EventId:    p.NewId(),
			EventType:  "TaskCompleted",
			OccurredAt: tc.OccurredAt(),
			Source:     "fulfillment-execution",
			Data: TaskCompletedData{
				TaskId:          string(tc.TaskId),
				StationId:       string(tc.StationId),
				WorkUnitId:      workUnitId,
				AssociateId:     associateId,
				DurationSeconds: durationSeconds,
			},
		}
		payload, err := json.Marshal(env)
		if err != nil {
			return nil, fmt.Errorf("kafka: marshal envelope: %w", err)
		}
		enc := Encoded{Topic: Topic, EventType: env.EventType, Key: []byte(tc.TaskId), Value: payload}
		observability.InjectKafkaTrace(ctx, &enc.Headers)
		out = append(out, enc)
	}
	return out, nil
}

// Publish forwards every TaskCompleted event in evts onto Kafka, enriched
// with the completed Task's OrderRef, the completing associate's identity,
// and the task's duration. Each message is encoded and written inside its
// own "kafka.publish <topic>" producer span.
func (p *Publisher) Publish(ctx context.Context, evts ...shared.DomainEvent) error {
	for _, e := range evts {
		if _, ok := e.(shared.TaskCompleted); !ok {
			continue
		}
		if err := p.publishOne(ctx, e); err != nil {
			return err
		}
	}
	return nil
}

// publishOne encodes and writes one event inside a producer span, so the
// injected traceparent names the publish span itself (the consumer hangs
// off the publish, not its caller) and a broker error is recorded on it.
func (p *Publisher) publishOne(ctx context.Context, e shared.DomainEvent) error {
	ctx, span := otel.Tracer(observability.InstrumentationName).Start(ctx,
		"kafka.publish "+Topic,
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			semconv.MessagingSystemKafka,
			semconv.MessagingDestinationName(Topic),
			semconv.MessagingOperationName("publish"),
		),
	)
	defer span.End()

	encoded, err := p.Encode(ctx, e)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	for _, enc := range encoded {
		if err := p.Writer.WriteMessages(ctx, enc.message()); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return fmt.Errorf("kafka: publish TaskCompleted: %w", err)
		}
	}
	return nil
}

// associateId resolves the occupant checked into stationId at publish
// time, returning "" (not an error) when Stations is unset, the station is
// unknown, or the station has no occupant — associate identity is an
// intentionally soft, best-effort fact (see ADR-0014), not every station
// has a checked-in occupant (e.g. a robot station never checks anyone in).
func (p *Publisher) associateId(ctx context.Context, stationId shared.StationId) (string, error) {
	if p.Stations == nil {
		return "", nil
	}
	s, err := p.Stations.FindById(ctx, stationId)
	if err != nil {
		return "", err
	}
	if s == nil || s.Occupant() == nil {
		return "", nil
	}
	return string(*s.Occupant()), nil
}

// Close releases the underlying Kafka writer.
func (p *Publisher) Close() error {
	if w, ok := p.Writer.(*kafkago.Writer); ok {
		return w.Close()
	}
	return nil
}
