// Package events provides outbound EventPublisher implementations: a log
// publisher for local/dev use and a buffered publisher for tests, both
// satisfying an interface shaped to drop in a Kafka producer later.
package events

import (
	"context"
	"log"
	"sync"

	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
)

// LogPublisher publishes domain events by logging them. It satisfies
// ports.EventPublisher and is a placeholder for a Kafka producer: swap it
// for an adapter that serializes DomainEvent onto a topic per EventName().
type LogPublisher struct {
	logger *log.Logger
}

// NewLogPublisher constructs a LogPublisher writing through logger. A nil
// logger uses the standard library's default logger.
func NewLogPublisher(logger *log.Logger) *LogPublisher {
	if logger == nil {
		logger = log.Default()
	}
	return &LogPublisher{logger: logger}
}

func (p *LogPublisher) Publish(_ context.Context, evts ...shared.DomainEvent) error {
	for _, e := range evts {
		p.logger.Printf("event=%s occurred_at=%s", e.EventName(), e.OccurredAt().Format("2006-01-02T15:04:05Z07:00"))
	}
	return nil
}

// BufferedPublisher accumulates published events in memory, for assertions
// in tests.
type BufferedPublisher struct {
	mu     sync.Mutex
	events []shared.DomainEvent
}

// NewBufferedPublisher constructs an empty BufferedPublisher.
func NewBufferedPublisher() *BufferedPublisher {
	return &BufferedPublisher{}
}

func (p *BufferedPublisher) Publish(_ context.Context, evts ...shared.DomainEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, evts...)
	return nil
}

// Events returns a copy of every event published so far, in order.
func (p *BufferedPublisher) Events() []shared.DomainEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]shared.DomainEvent, len(p.events))
	copy(out, p.events)
	return out
}
