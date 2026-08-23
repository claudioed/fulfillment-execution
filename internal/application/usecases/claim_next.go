package usecases

import (
	"context"
	"time"

	"github.com/claudioed/fulfillment-execution/internal/application/ports"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
	"github.com/claudioed/fulfillment-execution/internal/domain/task"
)

// DefaultLeaseDuration is how long a claim is held before it expires and the
// task returns to the pool.
const DefaultLeaseDuration = 5 * time.Minute

// ClaimNext implements PULL dispatch: a station calls claimNext and the
// system selects the highest-priority (earliest CPT) pending task the
// station is certified/equipped for. The station is never named in advance.
type ClaimNext struct {
	Tasks         ports.TaskRepo
	Stations      ports.StationRepo
	Publisher     ports.EventPublisher
	Clock         ports.Clock
	LeaseDuration time.Duration
	// Metrics is optional: nil means this use case runs uninstrumented.
	Metrics ports.Metrics
}

// Execute finds the best-fit pending task for stationId and leases it.
func (uc *ClaimNext) Execute(ctx context.Context, stationId shared.StationId, taskType task.Type) (*task.Task, error) {
	st, err := uc.Stations.FindById(ctx, stationId)
	if err != nil {
		return nil, err
	}
	if st == nil {
		return nil, ErrStationNotFound
	}

	now := uc.Clock.Now()
	candidates, err := uc.Tasks.FindClaimableByType(ctx, taskType, now)
	if err != nil {
		return nil, err
	}

	leaseDuration := uc.LeaseDuration
	if leaseDuration <= 0 {
		leaseDuration = DefaultLeaseDuration
	}

	// candidates are ordered earliest-CPT-first by the repository; take the
	// first one this station's capabilities satisfy.
	for _, t := range candidates {
		err := t.Claim(stationId, st.Capabilities(), now, leaseDuration)
		if err == nil {
			if err := uc.Tasks.Save(ctx, t); err != nil {
				return nil, err
			}
			if err := uc.Publisher.Publish(ctx, shared.NewTaskClaimed(t.Id(), stationId, now)); err != nil {
				return nil, err
			}
			if uc.Metrics != nil {
				uc.Metrics.TaskClaimed(ctx, t.Type())
			}
			return t, nil
		}
	}
	return nil, ErrNoClaimableTask
}
