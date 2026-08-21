// Package ports declares the outbound interfaces the application layer
// depends on: repositories, event publishing, and time. Adapters implement
// these; the application layer never depends on adapters directly.
package ports

import (
	"context"
	"time"

	pack "github.com/claudioed/fulfillment-execution/internal/domain/package"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
	"github.com/claudioed/fulfillment-execution/internal/domain/station"
	"github.com/claudioed/fulfillment-execution/internal/domain/task"
)

// TaskRepo persists and retrieves Task aggregates.
type TaskRepo interface {
	Save(ctx context.Context, t *task.Task) error
	FindById(ctx context.Context, id shared.TaskId) (*task.Task, error)
	// FindClaimableByType returns Pending or lease-expired tasks of the
	// given type, ordered by earliest CPT first, for ClaimNext to scan.
	FindClaimableByType(ctx context.Context, taskType task.Type, now time.Time) ([]*task.Task, error)
	// FindAllClaimed returns every task currently in the Claimed state, for
	// the lease-expiry sweep.
	FindAllClaimed(ctx context.Context) ([]*task.Task, error)
	CountByTypeAndStatus(ctx context.Context, taskType task.Type, status task.Status) (int, error)
}

// StationRepo persists and retrieves Station aggregates.
type StationRepo interface {
	Save(ctx context.Context, s *station.Station) error
	FindById(ctx context.Context, id shared.StationId) (*station.Station, error)
}

// PackageRepo persists and retrieves Package aggregates.
type PackageRepo interface {
	Save(ctx context.Context, p *pack.Package) error
	FindById(ctx context.Context, id shared.PackageId) (*pack.Package, error)
}

// EventPublisher publishes domain events raised by use cases.
type EventPublisher interface {
	Publish(ctx context.Context, events ...shared.DomainEvent) error
}

// Clock supplies the current time, injected so use cases and lease logic are
// deterministic under test.
type Clock interface {
	Now() time.Time
}
