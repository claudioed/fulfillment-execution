package usecases

import (
	"context"

	"github.com/claudioed/fulfillment-execution/internal/application/ports"
	pack "github.com/claudioed/fulfillment-execution/internal/domain/package"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
	"github.com/claudioed/fulfillment-execution/internal/domain/task"
)

// SealPackage runs the Pack path: a station scans an order's contents and
// seals them into a Package, referenced by the Pack task it is working.
//
// ClassificationLookup is the live, synchronous, per-scanned-SKU outbound
// read from inventory-storage's product-classification endpoint (see
// ADR-0010). It is nil-safe: a SealPackage built without it (as every
// pre-existing test in this package does) behaves exactly as before this
// feature — every scanned item is treated as unclassified, no segregation
// check runs, ScanItem's plain path is used. This is the same
// "additive, does not alter existing behaviour" discipline ADR-0009's
// Fragile flag and inventory-storage's own StowStock/LocationLookup
// wiring already established.
type SealPackage struct {
	Tasks                ports.TaskRepo
	Packages             ports.PackageRepo
	Publisher            ports.EventPublisher
	Clock                ports.Clock
	NewId                func() shared.PackageId
	ClassificationLookup ports.ProductClassificationLookup
}

// Execute validates that stationId holds the active claim on taskId (which
// must be a Pack task), then scans contents and seals a new Package for the
// task's order.
//
// When ClassificationLookup is wired, each scanned SKU is looked up live
// before being recorded, and its DOT hazard class (when Hazmat) is checked
// against every already-scanned item's hazard class for same-package
// segregation (pack.ErrPackageSegregationViolation on the first
// incompatible pair). A single SKU's lookup transport error fails open for
// that SKU only — it is recorded as unclassified rather than aborting the
// whole seal. This is a deliberate asymmetry with inventory-storage's
// StowStock, which fails closed when a classified SKU's placement lookup
// errors: that is a harder, at-rest safety gate, whereas this is a
// pack-time hint from a soft dependency that should not halt an active
// pack station over a lookup blip. See ADR-0010.
func (uc *SealPackage) Execute(ctx context.Context, taskId shared.TaskId, stationId shared.StationId, contents []string) (*pack.Package, error) {
	t, err := uc.Tasks.FindById(ctx, taskId)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, ErrTaskNotFound
	}
	if t.Type() != task.Pack {
		return nil, ErrWrongTaskType
	}
	lease := t.Lease()
	if lease == nil || lease.StationId != stationId {
		return nil, task.ErrNotOwner
	}

	p := pack.New(uc.NewId(), t.OrderRef(), t.Fragile())
	for _, sku := range contents {
		hazardClass := uc.lookupHazardClass(ctx, sku)
		if err := p.ScanItemWithClass(sku, hazardClass); err != nil {
			return nil, err
		}
	}
	if err := p.Seal(); err != nil {
		return nil, err
	}
	if err := uc.Packages.Save(ctx, p); err != nil {
		return nil, err
	}
	if err := uc.Publisher.Publish(ctx, shared.NewPackageSealed(p.Id(), uc.Clock.Now())); err != nil {
		return nil, err
	}
	return p, nil
}

// lookupHazardClass returns sku's DOT hazard class (1-9), or 0 meaning "no
// hazard class" — either because ClassificationLookup is nil (permissive
// by construction), the lookup returned Known=false, the SKU is not
// Hazmat, or the lookup itself errored (fail-open per item; see Execute's
// doc comment for why this differs from inventory-storage's StowStock).
func (uc *SealPackage) lookupHazardClass(ctx context.Context, sku string) int {
	if uc.ClassificationLookup == nil {
		return 0
	}
	info, err := uc.ClassificationLookup.GetClassification(ctx, sku)
	if err != nil {
		// Fail-open for this single SKU only: a soft-dependency lookup
		// blip at an active pack station must not halt the whole seal.
		return 0
	}
	if !info.Known || !info.Hazmat {
		return 0
	}
	return info.DOTHazardClass
}
