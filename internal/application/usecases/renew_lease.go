package usecases

import (
	"context"
	"time"

	"github.com/claudioed/fulfillment-execution/internal/application/ports"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
)

// RenewLease extends a station's active claim on a task, preventing it from
// expiring back to the pool.
type RenewLease struct {
	Tasks         ports.TaskRepo
	Clock         ports.Clock
	LeaseDuration time.Duration
}

// Execute renews the lease held by stationId on taskId.
func (uc *RenewLease) Execute(ctx context.Context, taskId shared.TaskId, stationId shared.StationId) error {
	t, err := uc.Tasks.FindById(ctx, taskId)
	if err != nil {
		return err
	}
	if t == nil {
		return ErrTaskNotFound
	}

	leaseDuration := uc.LeaseDuration
	if leaseDuration <= 0 {
		leaseDuration = DefaultLeaseDuration
	}

	if err := t.RenewLease(stationId, uc.Clock.Now(), leaseDuration); err != nil {
		return err
	}
	return uc.Tasks.Save(ctx, t)
}
