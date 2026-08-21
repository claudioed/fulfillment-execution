package station_test

import (
	"errors"
	"testing"

	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
	"github.com/claudioed/fulfillment-execution/internal/domain/station"
)

func newPickStation() *station.Station {
	return station.New(shared.StationId("st1"), shared.NewCapabilitySet("pick"))
}

func TestCheckIn_Success(t *testing.T) {
	s := newPickStation()
	if err := s.CheckIn("worker-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !s.IsOccupied() {
		t.Fatalf("expected station to be occupied")
	}
}

// Invariant: one occupant at a time.
func TestCheckIn_RejectsSecondOccupant(t *testing.T) {
	s := newPickStation()
	_ = s.CheckIn("worker-1")
	err := s.CheckIn("worker-2")
	if !errors.Is(err, station.ErrOccupied) {
		t.Fatalf("expected ErrOccupied, got %v", err)
	}
	if *s.Occupant() != station.OccupantId("worker-1") {
		t.Fatalf("original occupant must remain, got %s", *s.Occupant())
	}
}

func TestCheckOut_Success(t *testing.T) {
	s := newPickStation()
	_ = s.CheckIn("worker-1")
	if err := s.CheckOut(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.IsOccupied() {
		t.Fatalf("expected station to be vacant")
	}
}

func TestCheckOut_RejectsWhenNotOccupied(t *testing.T) {
	s := newPickStation()
	err := s.CheckOut()
	if !errors.Is(err, station.ErrNotOccupied) {
		t.Fatalf("expected ErrNotOccupied, got %v", err)
	}
}

// Invariant: claim rejected if capability mismatch (checked at the station level).
func TestValidateAccept_RejectsCapabilityMismatch(t *testing.T) {
	s := newPickStation()
	err := s.ValidateAccept(shared.NewCapabilitySet("pack"))
	if !errors.Is(err, station.ErrCapabilityMismatch) {
		t.Fatalf("expected ErrCapabilityMismatch, got %v", err)
	}
}

func TestValidateAccept_AcceptsMatchingCapability(t *testing.T) {
	s := newPickStation()
	if err := s.ValidateAccept(shared.NewCapabilitySet("pick")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
