package usecases

import (
	"context"

	"github.com/claudioed/fulfillment-execution/internal/application/ports"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
)

// RunSlam runs the SLAM (Scan, Label, Apply, Manifest) weigh-check on a
// sealed package: a matching weight applies the shipping label, a
// discrepancy diverts the package.
type RunSlam struct {
	Packages  ports.PackageRepo
	Publisher ports.EventPublisher
	Clock     ports.Clock
}

// Execute weighs packageId against expectedWeight and records the outcome.
func (uc *RunSlam) Execute(ctx context.Context, packageId shared.PackageId, actualWeight, expectedWeight float64) error {
	p, err := uc.Packages.FindById(ctx, packageId)
	if err != nil {
		return err
	}
	if p == nil {
		return ErrPackageNotFound
	}

	now := uc.Clock.Now()
	labelApplied, err := p.Weigh(expectedWeight, actualWeight)
	if err != nil {
		return err
	}
	if err := uc.Packages.Save(ctx, p); err != nil {
		return err
	}

	if !labelApplied {
		if err := uc.Publisher.Publish(ctx,
			shared.NewWeightDiscrepancyDetected(packageId, expectedWeight, actualWeight, now),
			shared.NewPackageDiverted(packageId, now),
		); err != nil {
			return err
		}
		return nil
	}
	return uc.Publisher.Publish(ctx, shared.NewLabelApplied(packageId, now))
}
