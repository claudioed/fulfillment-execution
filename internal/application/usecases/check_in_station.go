package usecases

import (
	"context"

	"github.com/claudioed/fulfillment-execution/internal/application/ports"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
	"github.com/claudioed/fulfillment-execution/internal/domain/station"
)

// CheckInStation assigns an occupant (worker or robot) to a station,
// wiring the Station aggregate's CheckIn invariant (one occupant at a
// time — station.ErrOccupied on a second check-in) to the application
// layer. This is operational state, not itself a labor-performance fact:
// no domain event is published here (see ADR-0014) — the
// labor-performance-relevant identity/duration facts ride on the existing
// TaskCompleted event instead, resolved at completion time via a
// StationRepo lookup of whichever occupant happens to be checked in then.
type CheckInStation struct {
	Stations ports.StationRepo
}

// Execute checks occupant into stationId. Returns ErrStationNotFound if no
// station is registered with stationId, or station.ErrOccupied if the
// station already has an occupant.
func (uc *CheckInStation) Execute(ctx context.Context, stationId shared.StationId, occupant station.OccupantId) (*station.Station, error) {
	s, err := uc.Stations.FindById(ctx, stationId)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, ErrStationNotFound
	}

	if err := s.CheckIn(occupant); err != nil {
		return nil, err
	}
	if err := uc.Stations.Save(ctx, s); err != nil {
		return nil, err
	}
	return s, nil
}
