package usecases

import (
	"context"

	"github.com/claudioed/fulfillment-execution/internal/application/ports"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
)

// CompleteTask finishes a task, validating that the calling station owns the
// active claim.
type CompleteTask struct {
	Tasks     ports.TaskRepo
	Publisher ports.EventPublisher
	Clock     ports.Clock
}

// Execute completes taskId on behalf of stationId.
func (uc *CompleteTask) Execute(ctx context.Context, taskId shared.TaskId, stationId shared.StationId) error {
	t, err := uc.Tasks.FindById(ctx, taskId)
	if err != nil {
		return err
	}
	if t == nil {
		return ErrTaskNotFound
	}

	now := uc.Clock.Now()
	if err := t.Complete(stationId, now); err != nil {
		return err
	}
	if err := uc.Tasks.Save(ctx, t); err != nil {
		return err
	}
	return uc.Publisher.Publish(ctx, shared.NewTaskCompleted(taskId, stationId, now))
}
