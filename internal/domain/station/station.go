// Package station implements the Station aggregate: a work position with a
// capability set, occupied by at most one worker/robot at a time.
package station

import (
	"errors"

	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
)

var (
	// ErrOccupied is returned when a station already has an occupant and a
	// second occupant attempts to check in.
	ErrOccupied = errors.New("station: already occupied")
	// ErrNotOccupied is returned when check-out is attempted on an empty station.
	ErrNotOccupied = errors.New("station: not occupied")
	// ErrCapabilityMismatch is returned when a task's required capabilities
	// are not a subset of the station's capabilities.
	ErrCapabilityMismatch = errors.New("station: capabilities do not match")
)

// OccupantId identifies the worker or robot occupying a station.
type OccupantId string

// Station is the aggregate root for a work position.
type Station struct {
	id           shared.StationId
	capabilities shared.CapabilitySet
	occupant     *OccupantId
}

// New creates an unoccupied station with the given capabilities.
func New(id shared.StationId, capabilities shared.CapabilitySet) *Station {
	return &Station{id: id, capabilities: capabilities}
}

// Rehydrate reconstructs a Station from persisted state.
func Rehydrate(id shared.StationId, capabilities shared.CapabilitySet, occupant *OccupantId) *Station {
	return &Station{id: id, capabilities: capabilities, occupant: occupant}
}

func (s *Station) Id() shared.StationId               { return s.id }
func (s *Station) Capabilities() shared.CapabilitySet { return s.capabilities }
func (s *Station) Occupant() *OccupantId              { return s.occupant }
func (s *Station) IsOccupied() bool                   { return s.occupant != nil }

// CheckIn assigns an occupant to the station. Rejected if the station
// already has an occupant (one occupant at a time).
func (s *Station) CheckIn(occupant OccupantId) error {
	if s.occupant != nil {
		return ErrOccupied
	}
	s.occupant = &occupant
	return nil
}

// CheckOut clears the station's occupant.
func (s *Station) CheckOut() error {
	if s.occupant == nil {
		return ErrNotOccupied
	}
	s.occupant = nil
	return nil
}

// CanAccept reports whether the station's capabilities satisfy what a task requires.
func (s *Station) CanAccept(required shared.CapabilitySet) bool {
	return s.capabilities.HasAll(required)
}

// ValidateAccept returns ErrCapabilityMismatch if the station cannot accept a
// task requiring the given capabilities.
func (s *Station) ValidateAccept(required shared.CapabilitySet) error {
	if !s.CanAccept(required) {
		return ErrCapabilityMismatch
	}
	return nil
}
