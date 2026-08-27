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

// ClassificationInfo is the placement/segregation-relevant subset of a
// SKU's classification, as seen from this bounded context — the result of
// the live, synchronous cross-context lookup SealPackage uses per scanned
// SKU. Known=false means "no classification info available for this SKU",
// which SealPackage treats as no constraint (fail-open) — this service
// does not own product classification and cannot assume every SKU is known
// to inventory-storage (see ADR-0010, mirroring inventory-storage's own
// SlotAttributes.Known convention for facility-layout lookups).
type ClassificationInfo struct {
	// Hazmat reports whether inventory-storage's ProductClassification
	// carries the Hazmat handling tag for this SKU.
	Hazmat bool
	// DOTHazardClass is the DOT hazard class (1-9) when Hazmat is true.
	// Zero when Hazmat is false or the class is not (yet) recorded
	// upstream — a Hazmat SKU with no recorded class is treated as having
	// no hazard class for segregation purposes (fail-open per item, not
	// per SKU-wide Hazmat flag), since the segregation check is keyed on
	// hazard class, not the Hazmat boolean alone.
	DOTHazardClass int
	// Fragile reports whether the classification carries the Fragile
	// handling tag. Not used by SealPackage today (fragile handling
	// already rides in via Task.Fragile per ADR-0009) — carried here for
	// completeness of the classification shape and potential future use.
	Fragile bool
	// Known is false when this SKU has no classification available
	// (lookup miss, unmodeled SKU, or the permissive no-op adapter).
	// SealPackage treats Known=false exactly like Hazmat=false: no
	// segregation constraint from this item.
	Known bool
}

// ProductClassificationLookup is the outbound port for the live,
// synchronous, per-scanned-item cross-context read from inventory-storage's
// GET /products/{sku}/classification endpoint, used by SealPackage to
// enforce same-package DOT hazard segregation (see ADR-0010). Unlike the
// Fragile flag (ADR-0009, stamped onto Task at release time by
// wes-work-planning), a Pack task's scanned contents are discovered live at
// the scan station — the classification of each scanned SKU must be looked
// up at seal time, not release time.
type ProductClassificationLookup interface {
	GetClassification(ctx context.Context, sku string) (ClassificationInfo, error)
}
