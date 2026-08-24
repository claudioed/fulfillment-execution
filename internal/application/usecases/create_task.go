package usecases

import (
	"context"

	"github.com/claudioed/fulfillment-execution/internal/application/ports"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
	"github.com/claudioed/fulfillment-execution/internal/domain/task"
)

// CreateTask puts a new unit of Pick, Pack, or SLAM work into the pool.
type CreateTask struct {
	Tasks     ports.TaskRepo
	Publisher ports.EventPublisher
	Clock     ports.Clock
	NewId     func() shared.TaskId
}

// Execute creates and persists the task, then publishes TaskCreated. fragile
// is the packing hint sourced from wes-work-planning at release time (itself
// derived from inventory-storage's ProductClassification) — see
// task.New for what it means and does not mean.
func (uc *CreateTask) Execute(ctx context.Context, taskType task.Type, cpt shared.CPT, orderRef shared.OrderRef, required shared.CapabilitySet, fragile bool) (*task.Task, error) {
	id := uc.NewId()
	t := task.New(id, taskType, cpt, orderRef, required, fragile)
	if err := uc.Tasks.Save(ctx, t); err != nil {
		return nil, err
	}
	if err := uc.Publisher.Publish(ctx, shared.NewTaskCreated(id, uc.Clock.Now())); err != nil {
		return nil, err
	}
	return t, nil
}
