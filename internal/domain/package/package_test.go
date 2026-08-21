package pack_test

import (
	"errors"
	"testing"

	pack "github.com/claudioed/fulfillment-execution/internal/domain/package"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
)

func newPackage() *pack.Package {
	return pack.New(shared.PackageId("p1"), shared.OrderRef("order-1"))
}

// Invariant: cannot seal without scanned contents.
func TestSeal_RejectsWithoutScannedContents(t *testing.T) {
	p := newPackage()
	err := p.Seal()
	if !errors.Is(err, pack.ErrNoScannedContents) {
		t.Fatalf("expected ErrNoScannedContents, got %v", err)
	}
	if p.Status() != pack.Open {
		t.Fatalf("expected Open, got %s", p.Status())
	}
}

func TestSeal_SucceedsWithScannedContents(t *testing.T) {
	p := newPackage()
	_ = p.ScanItem("sku-1")
	if err := p.Seal(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Status() != pack.Sealed {
		t.Fatalf("expected Sealed, got %s", p.Status())
	}
}

func TestSeal_RejectsDoubleSeal(t *testing.T) {
	p := newPackage()
	_ = p.ScanItem("sku-1")
	_ = p.Seal()
	err := p.Seal()
	if !errors.Is(err, pack.ErrAlreadySealed) {
		t.Fatalf("expected ErrAlreadySealed, got %v", err)
	}
}

func TestWeigh_RejectsBeforeSeal(t *testing.T) {
	p := newPackage()
	_, err := p.Weigh(2.0, 2.0)
	if !errors.Is(err, pack.ErrNotSealed) {
		t.Fatalf("expected ErrNotSealed, got %v", err)
	}
}

func TestWeigh_AppliesLabelWithinTolerance(t *testing.T) {
	p := newPackage()
	_ = p.ScanItem("sku-1")
	_ = p.Seal()
	labeled, err := p.Weigh(2.0, 2.02)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !labeled {
		t.Fatalf("expected label applied within tolerance")
	}
	if p.Status() != pack.Labeled {
		t.Fatalf("expected Labeled, got %s", p.Status())
	}
}

// Invariant: SLAM diverts when |actual-expected| weight > tolerance.
func TestWeigh_DivertsOutsideTolerance(t *testing.T) {
	p := newPackage()
	_ = p.ScanItem("sku-1")
	_ = p.Seal()
	labeled, err := p.Weigh(2.0, 2.5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if labeled {
		t.Fatalf("expected label NOT applied outside tolerance")
	}
	if p.Status() != pack.Diverted {
		t.Fatalf("expected Diverted, got %s", p.Status())
	}
}

func TestWeigh_RejectsReprocessingAfterDiverted(t *testing.T) {
	p := newPackage()
	_ = p.ScanItem("sku-1")
	_ = p.Seal()
	_, _ = p.Weigh(2.0, 2.5)
	_, err := p.Weigh(2.0, 2.0)
	if !errors.Is(err, pack.ErrAlreadyProcessed) {
		t.Fatalf("expected ErrAlreadyProcessed, got %v", err)
	}
}
