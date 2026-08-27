package events_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/events"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
)

// countingPublisher records how many events it received and can be made to
// fail, so MultiPublisher's fan-out and error-abort behaviour is observable.
type countingPublisher struct {
	got int
	err error
}

func (c *countingPublisher) Publish(_ context.Context, evts ...shared.DomainEvent) error {
	if c.err != nil {
		return c.err
	}
	c.got += len(evts)
	return nil
}

func TestMultiPublisher_FansOutToAll(t *testing.T) {
	a, b := &countingPublisher{}, &countingPublisher{}
	m := events.NewMultiPublisher(a, b)

	ev := shared.NewTaskCreated("t1", time.Now())
	if err := m.Publish(context.Background(), ev); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if a.got != 1 || b.got != 1 {
		t.Fatalf("fan-out counts = (%d,%d), want (1,1)", a.got, b.got)
	}
}

func TestMultiPublisher_SkipsNilTargets(t *testing.T) {
	a := &countingPublisher{}
	m := events.NewMultiPublisher(a, nil)

	if err := m.Publish(context.Background(), shared.NewTaskCreated("t1", time.Now())); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if a.got != 1 {
		t.Fatalf("got = %d, want 1", a.got)
	}
}

func TestMultiPublisher_StopsAtFirstError(t *testing.T) {
	boom := errors.New("boom")
	first := &countingPublisher{err: boom}
	second := &countingPublisher{}
	m := events.NewMultiPublisher(first, second)

	if err := m.Publish(context.Background(), shared.NewTaskCreated("t1", time.Now())); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if second.got != 0 {
		t.Fatalf("second publisher should not run after first errors, got %d", second.got)
	}
}
