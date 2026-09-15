package usecases

import (
	"context"

	"github.com/claudioed/fulfillment-execution/internal/application/ports"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
)

// SweepCPTMisses is the Clock-driven sweep that detects tasks still open
// (Pending or Claimed) past their CPT and raises TaskCPTMissed for each —
// fulfillment-execution's half of order-management ADR 0014 §5's promise
// feedback loop (see ADR-0025 for the full design, including the deliberate
// re-fire-every-sweep-tick semantics: a task remaining open past its CPT
// raises TaskCPTMissed again on every pass, matching ExpireLeases's own
// unconditional-on-match style rather than tracking "already reported"
// state).
type SweepCPTMisses struct {
	Tasks     ports.TaskRepo
	Publisher ports.EventPublisher
	Clock     ports.Clock
	// UnitOfWork brackets each detected task's Publish atomically (ADR
	// 0020); nil runs it directly. One scope per overdue task, so a
	// failure part-way through the sweep leaves the already-published
	// tasks committed (the sweep re-fires on the next tick anyway — see
	// the re-fire design note above).
	UnitOfWork ports.UnitOfWork
}

// Execute scans every still-open task whose CPT is at or before now and
// publishes TaskCPTMissed for each. Unlike ExpireLeases, this never mutates
// the Task aggregate itself — a CPT miss does not change a task's
// lifecycle state, it only reports a fact upstream. Returns the count of
// TaskCPTMissed events raised.
func (uc *SweepCPTMisses) Execute(ctx context.Context) (int, error) {
	now := uc.Clock.Now()
	overdue, err := uc.Tasks.FindOpenPastCPT(ctx, now)
	if err != nil {
		return 0, err
	}

	reported := 0
	for _, t := range overdue {
		err := atomically(ctx, uc.UnitOfWork, func(ctx context.Context) error {
			return uc.Publisher.Publish(ctx, shared.NewTaskCPTMissed(t.Id(), t.OrderRef(), string(t.Type()), t.CPT().Time(), now))
		})
		if err != nil {
			return reported, err
		}
		reported++
	}
	return reported, nil
}
