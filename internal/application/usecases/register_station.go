package usecases

import (
	"context"

	"github.com/claudioed/fulfillment-execution/internal/application/ports"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
	"github.com/claudioed/fulfillment-execution/internal/domain/station"
)

// RegisterStation adds a Station to the pool, or updates its capability set
// if the stationId is already registered (e.g. recertifying a station).
type RegisterStation struct {
	Stations  ports.StationRepo
	Publisher ports.EventPublisher
}

// Execute creates or updates the station and persists it. Re-registering an
// existing stationId is idempotent: it overwrites the capability set rather
// than erroring.
func (uc *RegisterStation) Execute(ctx context.Context, stationId string, capabilities []string) (*station.Station, error) {
	caps := make([]shared.Capability, len(capabilities))
	for i, c := range capabilities {
		caps[i] = shared.Capability(c)
	}

	s := station.New(shared.StationId(stationId), shared.NewCapabilitySet(caps...))
	if err := uc.Stations.Save(ctx, s); err != nil {
		return nil, err
	}
	return s, nil
}
