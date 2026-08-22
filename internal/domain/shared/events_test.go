package shared_test

import (
	"testing"
	"time"

	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
)

var eventNow = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

// Every domain event must report its own EventName and the OccurredAt time
// it was constructed with — that's what read-model projections key off of.
func TestDomainEvents_NameAndOccurredAt(t *testing.T) {
	tests := []struct {
		name  string
		event shared.DomainEvent
		want  string
	}{
		{"TaskCreated", shared.NewTaskCreated("t1", eventNow), "TaskCreated"},
		{"TaskClaimed", shared.NewTaskClaimed("t1", "s1", eventNow), "TaskClaimed"},
		{"LeaseExpired", shared.NewLeaseExpired("t1", eventNow), "LeaseExpired"},
		{"TaskCompleted", shared.NewTaskCompleted("t1", "s1", eventNow), "TaskCompleted"},
		{"ItemPicked", shared.NewItemPicked("t1", eventNow), "ItemPicked"},
		{"PackageSealed", shared.NewPackageSealed("p1", eventNow), "PackageSealed"},
		{"WeightDiscrepancyDetected", shared.NewWeightDiscrepancyDetected("p1", 2.0, 2.5, eventNow), "WeightDiscrepancyDetected"},
		{"LabelApplied", shared.NewLabelApplied("p1", eventNow), "LabelApplied"},
		{"PackageDiverted", shared.NewPackageDiverted("p1", eventNow), "PackageDiverted"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.event.EventName(); got != tt.want {
				t.Fatalf("EventName() = %q, want %q", got, tt.want)
			}
			if !tt.event.OccurredAt().Equal(eventNow) {
				t.Fatalf("OccurredAt() = %v, want %v", tt.event.OccurredAt(), eventNow)
			}
		})
	}
}

func TestNewTaskClaimed_CarriesTaskAndStationId(t *testing.T) {
	e := shared.NewTaskClaimed("t1", "s1", eventNow)
	if e.TaskId != "t1" {
		t.Fatalf("expected TaskId t1, got %s", e.TaskId)
	}
	if e.StationId != "s1" {
		t.Fatalf("expected StationId s1, got %s", e.StationId)
	}
}

func TestNewWeightDiscrepancyDetected_CarriesWeights(t *testing.T) {
	e := shared.NewWeightDiscrepancyDetected("p1", 2.0, 2.5, eventNow)
	if e.PackageId != "p1" {
		t.Fatalf("expected PackageId p1, got %s", e.PackageId)
	}
	if e.ExpectedWeight != 2.0 {
		t.Fatalf("expected ExpectedWeight 2.0, got %v", e.ExpectedWeight)
	}
	if e.ActualWeight != 2.5 {
		t.Fatalf("expected ActualWeight 2.5, got %v", e.ActualWeight)
	}
}
