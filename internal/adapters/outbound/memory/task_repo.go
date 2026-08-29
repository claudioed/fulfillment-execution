// Package memory provides thread-safe in-memory implementations of every
// outbound port, for tests and local development.
package memory

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
	"github.com/claudioed/fulfillment-execution/internal/domain/task"
)

// TaskRepo is a thread-safe in-memory implementation of ports.TaskRepo.
type TaskRepo struct {
	mu    sync.RWMutex
	tasks map[shared.TaskId]*task.Task
}

// NewTaskRepo constructs an empty TaskRepo.
func NewTaskRepo() *TaskRepo {
	return &TaskRepo{tasks: make(map[shared.TaskId]*task.Task)}
}

func (r *TaskRepo) Save(_ context.Context, t *task.Task) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tasks[t.Id()] = t
	return nil
}

func (r *TaskRepo) FindById(_ context.Context, id shared.TaskId) (*task.Task, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tasks[id]
	if !ok {
		return nil, nil
	}
	return t, nil
}

func (r *TaskRepo) FindClaimableByType(_ context.Context, taskType task.Type, now time.Time) ([]*task.Task, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*task.Task
	for _, t := range r.tasks {
		if t.Type() != taskType {
			continue
		}
		if t.IsAvailable(now) {
			result = append(result, t)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CPT().Before(result[j].CPT())
	})
	return result, nil
}

func (r *TaskRepo) FindAllClaimed(_ context.Context) ([]*task.Task, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*task.Task
	for _, t := range r.tasks {
		if t.Status() == task.Claimed {
			result = append(result, t)
		}
	}
	return result, nil
}

func (r *TaskRepo) CountByTypeAndStatus(_ context.Context, taskType task.Type, status task.Status) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	count := 0
	for _, t := range r.tasks {
		if t.Type() == taskType && t.Status() == status {
			count++
		}
	}
	return count, nil
}

func (r *TaskRepo) FindByOrderRef(_ context.Context, orderRef shared.OrderRef) ([]*task.Task, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*task.Task
	for _, t := range r.tasks {
		if t.OrderRef() == orderRef {
			result = append(result, t)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Id() < result[j].Id()
	})
	return result, nil
}
