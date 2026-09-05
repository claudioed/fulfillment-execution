package usecases

import (
	"context"

	"github.com/claudioed/fulfillment-execution/internal/application/ports"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
)

// GetInstalledCapacity is a read model projecting how many stations are
// PHYSICALLY registered with a given capability — the raw installed
// ceiling a process path can ever be staffed up to, regardless of
// whether every station is currently occupied. It answers "how many
// stations CAN work this path", never "how many are working it right
// now" (that is a staffing question, out of this bounded context's
// scope — workforce-management owns AssociateShift/LaborAssignment).
//
// This exists for workforce-management's CommitShiftPlan to enforce
// plannedHeads against fulfillment-execution's real Station registry —
// see ADR-0018 (this repo) and workforce-management's own ADR for the
// installed-capacity-ceiling decision.
type GetInstalledCapacity struct {
	Stations ports.StationRepo
}

// Execute returns the count of registered stations holding capability.
func (uc *GetInstalledCapacity) Execute(ctx context.Context, capability shared.Capability) (int, error) {
	return uc.Stations.CountByCapability(ctx, capability)
}
