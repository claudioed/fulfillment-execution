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

// Metrics records the business events of the task lifecycle for the
// observability pipeline. It is a port, not a direct OTel dependency, so the
// application layer stays free of vendor telemetry types; the adapter behind
// it turns each call into an OTel counter increment.
//
// Use cases treat a nil Metrics as "not instrumented" rather than an error:
// telemetry must never change what a use case decides.
type Metrics interface {
	// TaskClaimed records that one task of taskType was leased to a station.
	TaskClaimed(ctx context.Context, taskType task.Type)
	// TaskCompleted records that one task of taskType was completed.
	TaskCompleted(ctx context.Context, taskType task.Type)
}

// Clock supplies the current time, injected so use cases and lease logic are
// deterministic under test.
type Clock interface {
	Now() time.Time
}

// ProcessedEvents tracks inbound event ids already applied, so an
// at-least-once source (e.g. Kafka) can be consumed idempotently.
type ProcessedEvents interface {
	// MarkProcessed records eventId as processed if it is not already
	// present. It returns true if this call newly recorded it, false if
	// eventId was already marked processed.
	MarkProcessed(ctx context.Context, eventId string) (bool, error)
}
