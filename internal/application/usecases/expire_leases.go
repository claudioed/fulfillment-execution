package usecases

import (
	"context"

	"github.com/claudioed/fulfillment-execution/internal/application/ports"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
)

// ExpireLeases is the Clock-driven sweep that frees tasks whose lease has
// timed out without confirmation or completion, returning them to Pending
// so they are never silently lost.
type ExpireLeases struct {
	Tasks     ports.TaskRepo
	Publisher ports.EventPublisher
	Clock     ports.Clock
}

// Execute scans every Claimed task and frees the ones whose lease has expired.
// Returns the count of tasks freed.
func (uc *ExpireLeases) Execute(ctx context.Context) (int, error) {
	now := uc.Clock.Now()
	claimed, err := uc.Tasks.FindAllClaimed(ctx)
	if err != nil {
		return 0, err
	}

	freed := 0
	for _, t := range claimed {
		if !t.ExpireLeaseIfDue(now) {
			continue
		}
		if err := uc.Tasks.Save(ctx, t); err != nil {
			return freed, err
		}
		if err := uc.Publisher.Publish(ctx, shared.NewLeaseExpired(t.Id(), now)); err != nil {
			return freed, err
		}
		freed++
	}
	return freed, nil
}
