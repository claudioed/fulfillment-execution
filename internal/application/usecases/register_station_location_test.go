package usecases_test

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/fulfillment-execution/internal/application/ports"
	"github.com/claudioed/fulfillment-execution/internal/application/usecases"
)

// fakeLocationRoleLookup is a stub ports.LocationRoleLookup for
// RegisterStation tests, mirroring wes-work-planning's own
// fakeTravelDistanceLookup pattern.
type fakeLocationRoleLookup struct {
	info ports.LocationRoleInfo
	err  error
	// calls records every locationCode GetRole was invoked with, so a
	// test can assert the lookup was (or was not) attempted.
	calls []string
}

func (f *fakeLocationRoleLookup) GetRole(_ context.Context, locationCode string) (ports.LocationRoleInfo, error) {
	f.calls = append(f.calls, locationCode)
	if f.err != nil {
		return ports.LocationRoleInfo{}, f.err
	}
	return f.info, nil
}

func TestRegisterStation_LocationCode_WorkCenter_Accepted(t *testing.T) {
	h := newHarness()
	lookup := &fakeLocationRoleLookup{info: ports.LocationRoleInfo{Role: "WorkCenter", Known: true}}
	register := &usecases.RegisterStation{Stations: h.stations, Publisher: h.publisher, LocationLookup: lookup}

	got, err := register.Execute(context.Background(), "s1", []string{"pack"}, "WH1-STOR-AMB-A07-01-01-A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.LocationCode() != "WH1-STOR-AMB-A07-01-01-A" {
		t.Fatalf("expected LocationCode to be recorded, got %q", got.LocationCode())
	}
	if len(lookup.calls) != 1 || lookup.calls[0] != "WH1-STOR-AMB-A07-01-01-A" {
		t.Fatalf("expected exactly one GetRole call with the given code, got %v", lookup.calls)
	}
}

func TestRegisterStation_LocationCode_NonWorkCenter_Rejected(t *testing.T) {
	h := newHarness()
	lookup := &fakeLocationRoleLookup{info: ports.LocationRoleInfo{Role: "Storage", Known: true}}
	register := &usecases.RegisterStation{Stations: h.stations, Publisher: h.publisher, LocationLookup: lookup}

	_, err := register.Execute(context.Background(), "s1", []string{"pack"}, "WH1-STOR-AMB-A07-01-01-A")
	if !errors.Is(err, usecases.ErrStationLocationNotWorkCenter) {
		t.Fatalf("got err %v, want ErrStationLocationNotWorkCenter", err)
	}
}

func TestRegisterStation_LocationCode_UnknownLocation_FailsOpen(t *testing.T) {
	h := newHarness()
	lookup := &fakeLocationRoleLookup{info: ports.LocationRoleInfo{Known: false}}
	register := &usecases.RegisterStation{Stations: h.stations, Publisher: h.publisher, LocationLookup: lookup}

	got, err := register.Execute(context.Background(), "s1", []string{"pack"}, "UNRECOGNIZED-CODE")
	if err != nil {
		t.Fatalf("expected fail-open for an unknown location, got: %v", err)
	}
	if got.LocationCode() != "UNRECOGNIZED-CODE" {
		t.Fatalf("expected the locationCode to still be recorded unchecked, got %q", got.LocationCode())
	}
}

func TestRegisterStation_LocationCode_LookupErrorFailsOpen(t *testing.T) {
	h := newHarness()
	lookup := &fakeLocationRoleLookup{err: errors.New("facility-layout unavailable")}
	register := &usecases.RegisterStation{Stations: h.stations, Publisher: h.publisher, LocationLookup: lookup}

	got, err := register.Execute(context.Background(), "s1", []string{"pack"}, "WH1-STOR-AMB-A07-01-01-A")
	if err != nil {
		t.Fatalf("expected Execute to still succeed on a lookup error (fail-open), got: %v", err)
	}
	if got.LocationCode() != "WH1-STOR-AMB-A07-01-01-A" {
		t.Fatalf("expected the locationCode to still be recorded, got %q", got.LocationCode())
	}
}

func TestRegisterStation_EmptyLocationCode_SkipsLookup(t *testing.T) {
	h := newHarness()
	lookup := &fakeLocationRoleLookup{info: ports.LocationRoleInfo{Role: "Storage", Known: true}}
	register := &usecases.RegisterStation{Stations: h.stations, Publisher: h.publisher, LocationLookup: lookup}

	got, err := register.Execute(context.Background(), "s1", []string{"pack"}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lookup.calls) != 0 {
		t.Fatalf("expected GetRole not to be called when locationCode is empty, got %v", lookup.calls)
	}
	if got.LocationCode() != "" {
		t.Fatalf("expected empty LocationCode, got %q", got.LocationCode())
	}
}

func TestRegisterStation_NilLocationLookup_RecordsLocationCodeUnchecked(t *testing.T) {
	h := newHarness()
	// No LocationLookup wired at all — mirrors every pre-existing caller
	// and test that predates this feature.
	register := &usecases.RegisterStation{Stations: h.stations, Publisher: h.publisher}

	got, err := register.Execute(context.Background(), "s1", []string{"pack"}, "WH1-STOR-AMB-A07-01-01-A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.LocationCode() != "WH1-STOR-AMB-A07-01-01-A" {
		t.Fatalf("expected the locationCode to still be recorded unchecked, got %q", got.LocationCode())
	}
}
