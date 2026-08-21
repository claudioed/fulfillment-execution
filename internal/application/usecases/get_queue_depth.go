package usecases

import (
	"context"

	"github.com/claudioed/fulfillment-execution/internal/application/ports"
	"github.com/claudioed/fulfillment-execution/internal/domain/task"
)

// GetQueueDepth is a read model projecting how much pending work sits in a
// given task-type queue.
type GetQueueDepth struct {
	Tasks ports.TaskRepo
}

// Execute returns the count of Pending tasks of taskType.
func (uc *GetQueueDepth) Execute(ctx context.Context, taskType task.Type) (int, error) {
	return uc.Tasks.CountByTypeAndStatus(ctx, taskType, task.Pending)
}
