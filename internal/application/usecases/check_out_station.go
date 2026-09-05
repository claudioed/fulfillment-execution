package usecases

import (
	"context"

	"github.com/claudioed/fulfillment-execution/internal/application/ports"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
	"github.com/claudioed/fulfillment-execution/internal/domain/station"
)

// CheckOutStation clears a station's occupant, wiring the Station
// aggregate's CheckOut invariant (station.ErrNotOccupied when the station
// is already empty) to the application layer. See CheckInStation for why
// this publishes no domain event.
type CheckOutStation struct {
	Stations ports.StationRepo
}

// Execute checks the current occupant out of stationId. Returns
// ErrStationNotFound if no station is registered with stationId, or
// station.ErrNotOccupied if the station has no occupant.
func (uc *CheckOutStation) Execute(ctx context.Context, stationId shared.StationId) (*station.Station, error) {
	s, err := uc.Stations.FindById(ctx, stationId)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, ErrStationNotFound
	}

	if err := s.CheckOut(); err != nil {
		return nil, err
	}
	if err := uc.Stations.Save(ctx, s); err != nil {
		return nil, err
	}
	return s, nil
}
