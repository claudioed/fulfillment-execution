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
type SealPackage struct {
	Tasks     ports.TaskRepo
	Packages  ports.PackageRepo
	Publisher ports.EventPublisher
	Clock     ports.Clock
	NewId     func() shared.PackageId
}

// Execute validates that stationId holds the active claim on taskId (which
// must be a Pack task), then scans contents and seals a new Package for the
// task's order.
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

	p := pack.New(uc.NewId(), t.OrderRef())
	for _, sku := range contents {
		if err := p.ScanItem(sku); err != nil {
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
