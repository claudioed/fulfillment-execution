package shared_test

import (
	"testing"
	"time"

	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
)

func TestCPT_Time(t *testing.T) {
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cpt := shared.NewCPT(at)
	if !cpt.Time().Equal(at) {
		t.Fatalf("expected Time() to return %v, got %v", at, cpt.Time())
	}
}

func TestCPT_Before(t *testing.T) {
	earlier := shared.NewCPT(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	later := shared.NewCPT(time.Date(2026, 1, 1, 13, 0, 0, 0, time.UTC))
	same := shared.NewCPT(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))

	if !earlier.Before(later) {
		t.Fatalf("expected earlier CPT to be Before later CPT")
	}
	if later.Before(earlier) {
		t.Fatalf("expected later CPT not to be Before earlier CPT")
	}
	if earlier.Before(same) {
		t.Fatalf("expected equal CPTs not to be Before each other")
	}
}
