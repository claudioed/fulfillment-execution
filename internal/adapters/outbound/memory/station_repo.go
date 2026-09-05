package memory

import (
	"context"
	"sync"

	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
	"github.com/claudioed/fulfillment-execution/internal/domain/station"
)

// StationRepo is a thread-safe in-memory implementation of ports.StationRepo.
type StationRepo struct {
	mu       sync.RWMutex
	stations map[shared.StationId]*station.Station
}

// NewStationRepo constructs an empty StationRepo.
func NewStationRepo() *StationRepo {
	return &StationRepo{stations: make(map[shared.StationId]*station.Station)}
}

func (r *StationRepo) Save(_ context.Context, s *station.Station) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stations[s.Id()] = s
	return nil
}

func (r *StationRepo) FindById(_ context.Context, id shared.StationId) (*station.Station, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.stations[id]
	if !ok {
		return nil, nil
	}
	return s, nil
}

func (r *StationRepo) CountByCapability(_ context.Context, capability shared.Capability) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	count := 0
	for _, s := range r.stations {
		if s.Capabilities().Contains(capability) {
			count++
		}
	}
	return count, nil
}
