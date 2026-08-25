package pack_test

import (
	"errors"
	"testing"

	pack "github.com/claudioed/fulfillment-execution/internal/domain/package"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
)

func newPackage() *pack.Package {
	return pack.New(shared.PackageId("p1"), shared.OrderRef("order-1"), false)
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

func TestScanItem_RejectsAfterSeal(t *testing.T) {
	p := newPackage()
	_ = p.ScanItem("sku-1")
	_ = p.Seal()
	err := p.ScanItem("sku-2")
	if !errors.Is(err, pack.ErrAlreadySealed) {
		t.Fatalf("expected ErrAlreadySealed, got %v", err)
	}
	if len(p.ScannedContents()) != 1 {
		t.Fatalf("expected scanned contents to remain unchanged, got %v", p.ScannedContents())
	}
}

func TestNew_Getters(t *testing.T) {
	p := newPackage()
	if p.Id() != shared.PackageId("p1") {
		t.Fatalf("expected Id p1, got %s", p.Id())
	}
	if p.OrderRef() != shared.OrderRef("order-1") {
		t.Fatalf("expected OrderRef order-1, got %s", p.OrderRef())
	}
	if len(p.ScannedContents()) != 0 {
		t.Fatalf("expected no scanned contents on a new package")
	}
}

func TestRehydrate_ReconstructsPersistedState(t *testing.T) {
	p := pack.Rehydrate(shared.PackageId("p2"), shared.OrderRef("order-2"), pack.Sealed, []string{"sku-1", "sku-2"}, false, nil)
	if p.Id() != shared.PackageId("p2") {
		t.Fatalf("expected Id p2, got %s", p.Id())
	}
	if p.OrderRef() != shared.OrderRef("order-2") {
		t.Fatalf("expected OrderRef order-2, got %s", p.OrderRef())
	}
	if p.Status() != pack.Sealed {
		t.Fatalf("expected Sealed, got %s", p.Status())
	}
	if len(p.ScannedContents()) != 2 {
		t.Fatalf("expected 2 scanned contents, got %v", p.ScannedContents())
	}
}

// FragileHandling is derived at construction time from the owning task's
// Fragile flag; it must round-trip through both New and Rehydrate, and must
// not depend on scanned contents or sealing.
func TestNew_FragileHandling_RoundTrips(t *testing.T) {
	p := pack.New(shared.PackageId("p1"), shared.OrderRef("order-1"), true)
	if !p.FragileHandling() {
		t.Fatalf("expected FragileHandling() to be true")
	}
}

func TestNew_NotFragileHandlingByDefault(t *testing.T) {
	p := newPackage()
	if p.FragileHandling() {
		t.Fatalf("expected FragileHandling() to be false when not requested")
	}
}

func TestRehydrate_FragileHandling_RoundTrips(t *testing.T) {
	p := pack.Rehydrate(shared.PackageId("p2"), shared.OrderRef("order-2"), pack.Sealed, []string{"sku-1"}, true, nil)
	if !p.FragileHandling() {
		t.Fatalf("expected rehydrated FragileHandling() to be true")
	}
}

// FragileHandling must not gate or otherwise affect the Seal invariant.
func TestSeal_SucceedsRegardlessOfFragileHandling(t *testing.T) {
	p := pack.New(shared.PackageId("p1"), shared.OrderRef("order-1"), true)
	_ = p.ScanItem("sku-1")
	if err := p.Seal(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !p.FragileHandling() {
		t.Fatalf("expected FragileHandling() to remain true after seal")
	}
}

// --- Segregation matrix (49 CFR §177.848, class-level simplification) ---

func TestIsSegregationIncompatible_Class1IsMaximallyRestrictive(t *testing.T) {
	for class := 1; class <= 9; class++ {
		if !pack.IsSegregationIncompatible(1, class) {
			t.Fatalf("expected class 1 incompatible with class %d", class)
		}
		if !pack.IsSegregationIncompatible(class, 1) {
			t.Fatalf("expected class %d incompatible with class 1 (symmetry)", class)
		}
	}
}

func TestIsSegregationIncompatible_Class1IncompatibleWithItself(t *testing.T) {
	if !pack.IsSegregationIncompatible(1, 1) {
		t.Fatalf("expected class 1 incompatible with itself")
	}
}

func TestIsSegregationIncompatible_Class9BroadlyCompatibleExceptClass1(t *testing.T) {
	for class := 2; class <= 9; class++ {
		if pack.IsSegregationIncompatible(9, class) {
			t.Fatalf("expected class 9 compatible with class %d", class)
		}
	}
	if !pack.IsSegregationIncompatible(9, 1) {
		t.Fatalf("expected class 9 incompatible with class 1")
	}
}

func TestIsSegregationIncompatible_IsSymmetric(t *testing.T) {
	for a := 1; a <= 9; a++ {
		for b := 1; b <= 9; b++ {
			if pack.IsSegregationIncompatible(a, b) != pack.IsSegregationIncompatible(b, a) {
				t.Fatalf("expected symmetry for (%d, %d)", a, b)
			}
		}
	}
}

func TestIsSegregationIncompatible_OutOfRangeReturnsFalse(t *testing.T) {
	cases := [][2]int{{0, 3}, {3, 0}, {10, 3}, {3, 10}, {-1, 5}}
	for _, c := range cases {
		if pack.IsSegregationIncompatible(c[0], c[1]) {
			t.Fatalf("expected out-of-range (%d, %d) to be compatible (false)", c[0], c[1])
		}
	}
}

// TestIsSegregationIncompatible_KnownCompatiblePair and its incompatible
// counterpart pin two concrete, hand-checked entries against 49 CFR
// §177.848's table (collapsed to class level) so a future edit to the
// matrix has at least two independently-verifiable anchors, not just the
// symmetry/class-1/class-9 structural properties above.
func TestIsSegregationIncompatible_KnownCompatiblePair(t *testing.T) {
	// Class 3 (flammable liquids) and class 8 (corrosive) are NOT marked
	// "X" in 49 CFR §177.848's table (class 3 row, class 8 column is
	// blank) — compatible.
	if pack.IsSegregationIncompatible(3, 8) {
		t.Fatalf("expected class 3 and class 8 to be compatible")
	}
}

func TestIsSegregationIncompatible_KnownIncompatiblePair(t *testing.T) {
	// Class 3 (flammable liquids) and class 5 (oxidizers, division 5.1
	// row) are marked "O" in 49 CFR §177.848's table, which this
	// same-package matrix collapses to incompatible (rule 2).
	if !pack.IsSegregationIncompatible(3, 5) {
		t.Fatalf("expected class 3 and class 5 to be incompatible")
	}
}

// --- ScanItemWithClass: round-trip and rejection ---

func TestScanItemWithClass_UnclassifiedItemNeverBlocksOrRecords(t *testing.T) {
	p := newPackage()
	if err := p.ScanItemWithClass("sku-1", 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := p.ScanItemWithClass("sku-2", 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(p.ScannedHazardClasses()) != 0 {
		t.Fatalf("expected no recorded hazard classes for unclassified items, got %v", p.ScannedHazardClasses())
	}
	if len(p.ScannedContents()) != 2 {
		t.Fatalf("expected both items scanned, got %v", p.ScannedContents())
	}
}

func TestScanItemWithClass_CompatibleClassesBothRecorded(t *testing.T) {
	p := newPackage()
	if err := p.ScanItemWithClass("sku-hazmat-3", 3); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := p.ScanItemWithClass("sku-hazmat-9", 9); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := p.ScannedHazardClasses()
	if len(got) != 2 || got[0] != 3 || got[1] != 9 {
		t.Fatalf("expected [3 9], got %v", got)
	}
}

func TestScanItemWithClass_IncompatibleClassRejectedAndNotAppended(t *testing.T) {
	p := newPackage()
	if err := p.ScanItemWithClass("sku-hazmat-1", 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	err := p.ScanItemWithClass("sku-hazmat-3", 3)
	if !errors.Is(err, pack.ErrPackageSegregationViolation) {
		t.Fatalf("expected ErrPackageSegregationViolation, got %v", err)
	}
	if len(p.ScannedContents()) != 1 {
		t.Fatalf("expected the incompatible item to NOT be appended, got %v", p.ScannedContents())
	}
	if len(p.ScannedHazardClasses()) != 1 {
		t.Fatalf("expected the incompatible item's class to NOT be recorded, got %v", p.ScannedHazardClasses())
	}
}

func TestScanItemWithClass_RejectsAfterSeal(t *testing.T) {
	p := newPackage()
	_ = p.ScanItem("sku-1")
	_ = p.Seal()
	err := p.ScanItemWithClass("sku-2", 3)
	if !errors.Is(err, pack.ErrAlreadySealed) {
		t.Fatalf("expected ErrAlreadySealed, got %v", err)
	}
}

func TestScanItemWithClass_HazardClassesRoundTripThroughRehydrate(t *testing.T) {
	p := pack.Rehydrate(shared.PackageId("p2"), shared.OrderRef("order-2"), pack.Sealed, []string{"sku-1"}, false, []int{3})
	got := p.ScannedHazardClasses()
	if len(got) != 1 || got[0] != 3 {
		t.Fatalf("expected rehydrated hazard classes [3], got %v", got)
	}
}

// --- SortLane: priority-order truth table ---

func TestSortLane_HazmatBeatsFragile(t *testing.T) {
	p := pack.New(shared.PackageId("p1"), shared.OrderRef("order-1"), true) // fragile
	_ = p.ScanItemWithClass("sku-1", 3)                                     // hazmat too
	if got := p.SortLane(); got != pack.SortLaneHazmat {
		t.Fatalf("expected HAZMAT_LANE when both hazmat and fragile, got %s", got)
	}
}

func TestSortLane_FragileOnly(t *testing.T) {
	p := pack.New(shared.PackageId("p1"), shared.OrderRef("order-1"), true)
	_ = p.ScanItem("sku-1")
	if got := p.SortLane(); got != pack.SortLaneFragileNoTilt {
		t.Fatalf("expected FRAGILE_NO_TILT, got %s", got)
	}
}

func TestSortLane_HazmatOnly(t *testing.T) {
	p := pack.New(shared.PackageId("p1"), shared.OrderRef("order-1"), false)
	_ = p.ScanItemWithClass("sku-1", 7)
	if got := p.SortLane(); got != pack.SortLaneHazmat {
		t.Fatalf("expected HAZMAT_LANE, got %s", got)
	}
}

func TestSortLane_NeitherIsStandard(t *testing.T) {
	p := newPackage()
	_ = p.ScanItem("sku-1")
	if got := p.SortLane(); got != pack.SortLaneStandard {
		t.Fatalf("expected STANDARD, got %s", got)
	}
}

func TestSortLane_EmptyOpenPackageIsStandard(t *testing.T) {
	p := newPackage()
	if got := p.SortLane(); got != pack.SortLaneStandard {
		t.Fatalf("expected STANDARD for a fresh package, got %s", got)
	}
}
