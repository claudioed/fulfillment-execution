package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	kafkago "github.com/segmentio/kafka-go"

	outboundkafka "github.com/claudioed/fulfillment-execution/internal/adapters/outbound/kafka"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
)

// OutboxPublisher implements ports.EventPublisher by writing each event's
// Kafka wire form — one row per (event x topic), produced by the same
// Encoders the direct publishers use — into outbox_events instead of the
// broker (ADR 0020). When called inside UnitOfWork.Execute the inserts
// join the use case's transaction, so the aggregate change and its events
// commit together or not at all. Encoding happens here, inside that
// transaction, because the integration and analytics encoders read the
// task/station repos to enrich the payload and must see the just-saved
// rows. OutboxRelay later drains the table onto Kafka.
type OutboxPublisher struct {
	pool     *pgxpool.Pool
	encoders []outboundkafka.Encoder
}

// NewOutboxPublisher constructs an OutboxPublisher over pool that fans
// every published event through each of encoders (in production: the
// integration Publisher and the AnalyticsPublisher).
func NewOutboxPublisher(pool *pgxpool.Pool, encoders ...outboundkafka.Encoder) *OutboxPublisher {
	return &OutboxPublisher{pool: pool, encoders: encoders}
}

// outboxHeader is the JSON shape one Kafka header is stored as in
// outbox_events.headers.
type outboxHeader struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func marshalHeaders(hs []kafkago.Header) ([]byte, error) {
	out := make([]outboxHeader, 0, len(hs))
	for _, h := range hs {
		out = append(out, outboxHeader{Key: h.Key, Value: string(h.Value)})
	}
	return json.Marshal(out)
}

func unmarshalHeaders(raw []byte) ([]kafkago.Header, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var stored []outboxHeader
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, err
	}
	if len(stored) == 0 {
		return nil, nil
	}
	out := make([]kafkago.Header, 0, len(stored))
	for _, h := range stored {
		out = append(out, kafkago.Header{Key: h.Key, Value: []byte(h.Value)})
	}
	return out, nil
}

// Publish stores the wire form of every event in evts, for every
// configured encoder, in the outbox. It never touches Kafka.
func (p *OutboxPublisher) Publish(ctx context.Context, evts ...shared.DomainEvent) error {
	if len(evts) == 0 {
		return nil
	}
	q := querierFrom(ctx, p.pool)
	for _, enc := range p.encoders {
		msgs, err := enc.Encode(ctx, evts...)
		if err != nil {
			return err
		}
		for _, m := range msgs {
			headers, err := marshalHeaders(m.Headers)
			if err != nil {
				return fmt.Errorf("postgres: marshal outbox headers for %s: %w", m.EventType, err)
			}
			if _, err := q.Exec(ctx, `
				INSERT INTO outbox_events (topic, event_type, key, value, headers)
				VALUES ($1, $2, $3, $4, $5)
			`, m.Topic, m.EventType, m.Key, m.Value, headers); err != nil {
				return fmt.Errorf("postgres: enqueue outbox event %s on %s: %w", m.EventType, m.Topic, err)
			}
		}
	}
	return nil
}
