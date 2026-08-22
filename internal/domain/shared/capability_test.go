package shared_test

import (
	"testing"

	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
)

func TestNewCapabilitySet_EmptyWhenNoArgs(t *testing.T) {
	set := shared.NewCapabilitySet()
	if set.Contains("pick") {
		t.Fatalf("expected empty set to contain nothing")
	}
}

func TestCapabilitySet_Contains(t *testing.T) {
	set := shared.NewCapabilitySet("pick", "hazmat")
	if !set.Contains("pick") {
		t.Fatalf("expected set to contain pick")
	}
	if set.Contains("pack") {
		t.Fatalf("expected set not to contain pack")
	}
}

func TestCapabilitySet_HasAll(t *testing.T) {
	tests := []struct {
		name     string
		set      shared.CapabilitySet
		required shared.CapabilitySet
		want     bool
	}{
		{"empty required is always satisfied", shared.NewCapabilitySet("pick"), shared.NewCapabilitySet(), true},
		{"exact match", shared.NewCapabilitySet("pick"), shared.NewCapabilitySet("pick"), true},
		{"superset satisfies subset", shared.NewCapabilitySet("pick", "hazmat"), shared.NewCapabilitySet("pick"), true},
		{"missing one required capability fails", shared.NewCapabilitySet("pick"), shared.NewCapabilitySet("pick", "hazmat"), false},
		{"disjoint sets fail", shared.NewCapabilitySet("pick"), shared.NewCapabilitySet("pack"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.set.HasAll(tt.required); got != tt.want {
				t.Fatalf("HasAll() = %v, want %v", got, tt.want)
			}
		})
	}
}
