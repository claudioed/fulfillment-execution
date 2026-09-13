package usecases

import (
	"context"

	"github.com/claudioed/fulfillment-execution/internal/application/ports"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
	"github.com/claudioed/fulfillment-execution/internal/domain/station"
)

// RegisterStation adds a Station to the pool, or updates its capability set
// if the stationId is already registered (e.g. recertifying a station).
//
// LocationLookup is the live, synchronous, registration-time outbound read
// from facility-layout's GET /locations/{locationCode} endpoint (see
// ADR-0024). It is nil-safe: a RegisterStation built without it (as every
// pre-existing test in this package does) behaves exactly as before this
// feature — a supplied locationCode is recorded unchecked. This is the
// same "additive, does not alter existing behaviour" discipline ADR-0009's
// Fragile flag and inventory-storage's own StowStock/LocationLookup
// wiring already established.
type RegisterStation struct {
	Stations       ports.StationRepo
	Publisher      ports.EventPublisher
	LocationLookup ports.LocationRoleLookup
}

// Execute creates or updates the station and persists it. Re-registering an
// existing stationId is idempotent: it overwrites the capability set rather
// than erroring.
//
// locationCode is optional (empty skips it entirely). When supplied and
// LocationLookup is wired, RegisterStation validates that the LocationCode
// actually resolves to a facility-layout WorkCenter-role slot:
// ErrStationLocationNotWorkCenter rejects registration outright when the
// role is KNOWN and is something other than WorkCenter (e.g. Storage,
// Dock) — a station physically standing in a storage aisle is a real
// modeling mistake this catches. An UNKNOWN LocationCode (unrecognized by
// facility-layout, or the lookup itself unavailable/erroring) fails open:
// the locationCode is still recorded, unchecked. Registering a station is
// not a safety-critical write the way inventory-storage's StowStock is, so
// facility-layout's uptime is never a hard dependency for it — only an
// explicitly WRONG, already-known role blocks registration.
func (uc *RegisterStation) Execute(ctx context.Context, stationId string, capabilities []string, locationCode string) (*station.Station, error) {
	caps := make([]shared.Capability, len(capabilities))
	for i, c := range capabilities {
		caps[i] = shared.Capability(c)
	}

	if locationCode != "" && uc.LocationLookup != nil {
		info, err := uc.LocationLookup.GetRole(ctx, locationCode)
		if err == nil && info.Known && info.Role != "WorkCenter" {
			return nil, ErrStationLocationNotWorkCenter
		}
	}

	s := station.New(shared.StationId(stationId), shared.NewCapabilitySet(caps...))
	s.SetLocationCode(locationCode)
	if err := uc.Stations.Save(ctx, s); err != nil {
		return nil, err
	}
	return s, nil
}
