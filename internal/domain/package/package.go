// Package pack implements the Package aggregate: pack output, from scanned
// contents through sealing to the SLAM weigh-check (label applied or
// diverted on discrepancy). Named `pack` because `package` is a Go keyword.
package pack

import (
	"errors"

	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
)

// Status is the lifecycle state of a package.
type Status string

const (
	Open     Status = "OPEN"
	Sealed   Status = "SEALED"
	Labeled  Status = "LABELED"
	Diverted Status = "DIVERTED"
)

var (
	// ErrNoScannedContents is returned when sealing is attempted with no
	// scanned contents (cannot seal without scanned contents).
	ErrNoScannedContents = errors.New("package: cannot seal without scanned contents")
	// ErrAlreadySealed is returned when sealing an already-sealed package.
	ErrAlreadySealed = errors.New("package: already sealed")
	// ErrNotSealed is returned when SLAM is attempted before the package is sealed.
	ErrNotSealed = errors.New("package: must be sealed before SLAM")
	// ErrAlreadyProcessed is returned when SLAM is attempted on a package
	// that already has a label applied or was diverted.
	ErrAlreadyProcessed = errors.New("package: SLAM already processed")
)

// WeightTolerance is the maximum allowed |actual-expected| deviation, in the
// same unit as the weights passed to Weigh (e.g. kilograms).
const WeightTolerance = 0.05

// Package is the aggregate root for pack output: an order becoming a sealed carton.
type Package struct {
	id              shared.PackageId
	orderRef        shared.OrderRef
	status          Status
	scannedContents []string
	fragileHandling bool
}

// New creates an empty, open package for the given order. fragileHandling is
// derived from the owning Pack task's Fragile flag (itself stamped by
// wes-work-planning from inventory-storage's ProductClassification) — true
// if any of the package's scanned/sealed contents came from an order line
// classified Fragile, so downstream packing/sortation can route it
// accordingly.
func New(id shared.PackageId, orderRef shared.OrderRef, fragileHandling bool) *Package {
	return &Package{id: id, orderRef: orderRef, status: Open, fragileHandling: fragileHandling}
}

// Rehydrate reconstructs a Package from persisted state.
func Rehydrate(id shared.PackageId, orderRef shared.OrderRef, status Status, scannedContents []string, fragileHandling bool) *Package {
	return &Package{id: id, orderRef: orderRef, status: status, scannedContents: scannedContents, fragileHandling: fragileHandling}
}

func (p *Package) Id() shared.PackageId      { return p.id }
func (p *Package) OrderRef() shared.OrderRef { return p.orderRef }
func (p *Package) Status() Status            { return p.status }
func (p *Package) ScannedContents() []string { return p.scannedContents }

// FragileHandling reports whether this package requires fragile packing
// care, derived at construction time from the owning task's Fragile flag.
func (p *Package) FragileHandling() bool { return p.fragileHandling }

// ScanItem records a scanned item as part of the package's contents.
func (p *Package) ScanItem(sku string) error {
	if p.status != Open {
		return ErrAlreadySealed
	}
	p.scannedContents = append(p.scannedContents, sku)
	return nil
}

// Seal closes the package. Cannot seal without scanned contents.
func (p *Package) Seal() error {
	if p.status != Open {
		return ErrAlreadySealed
	}
	if len(p.scannedContents) == 0 {
		return ErrNoScannedContents
	}
	p.status = Sealed
	return nil
}

// Weigh runs the SLAM weigh-check: if the actual weight is within
// WeightTolerance of the expected weight the label is applied, otherwise the
// package is diverted. Returns true if the label was applied.
func (p *Package) Weigh(expectedWeight, actualWeight float64) (labelApplied bool, err error) {
	if p.status == Open {
		return false, ErrNotSealed
	}
	if p.status != Sealed {
		return false, ErrAlreadyProcessed
	}
	deviation := expectedWeight - actualWeight
	if deviation < 0 {
		deviation = -deviation
	}
	if deviation > WeightTolerance {
		p.status = Diverted
		return false, nil
	}
	p.status = Labeled
	return true, nil
}
