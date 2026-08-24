package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/events"
	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/memory"
	"github.com/claudioed/fulfillment-execution/internal/application/usecases"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
	"github.com/claudioed/fulfillment-execution/internal/domain/task"
)

var base = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

// harness builds Deps over an in-memory repo, seeding through the real
// CreateTask use case, with a FixedClock so lease timing is deterministic.
type harness struct {
	deps    Deps
	tasks   *memory.TaskRepo
	create  *usecases.CreateTask
	claim   *usecases.ClaimNext
	station *usecases.RegisterStation
	clock   *memory.FixedClock
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	tasks := memory.NewTaskRepo()
	stations := memory.NewStationRepo()
	publisher := events.NewBufferedPublisher()
	clock := memory.NewFixedClock(base)

	n := 0
	newId := func() shared.TaskId {
		n++
		return shared.TaskId([]byte{'t', byte('0' + n)})
	}

	return &harness{
		deps: Deps{
			GetQueueDepth: &usecases.GetQueueDepth{Tasks: tasks},
			Tasks:         tasks,
			Now:           clock.Now,
		},
		tasks:   tasks,
		create:  &usecases.CreateTask{Tasks: tasks, Publisher: publisher, Clock: clock, NewId: newId},
		claim:   &usecases.ClaimNext{Tasks: tasks, Stations: stations, Publisher: publisher, Clock: clock},
		station: &usecases.RegisterStation{Stations: stations, Publisher: publisher},
		clock:   clock,
	}
}

func (h *harness) seedPending(t *testing.T, tt task.Type, orderRef string, cptOffset time.Duration, cap string) {
	t.Helper()
	_, err := h.create.Execute(context.Background(), tt, shared.NewCPT(base.Add(cptOffset)), shared.OrderRef(orderRef), shared.NewCapabilitySet(shared.Capability(cap)))
	if err != nil {
		t.Fatalf("seedPending: %v", err)
	}
}

func TestGetQueueStatus(t *testing.T) {
	h := newHarness(t)
	h.seedPending(t, task.Pick, "o1", time.Hour, "pick")
	h.seedPending(t, task.Pick, "o2", 2*time.Hour, "pick")
	h.seedPending(t, task.Pack, "o3", time.Hour, "pack")

	tests := []struct {
		name      string
		in        string
		wantDepth int
		wantErr   bool
	}{
		{"pick depth 2", "PICK", 2, false},
		{"pack depth 1", "PACK", 1, false},
		{"slam depth 0", "SLAM", 0, false},
		{"unknown path rejected", "FLY", 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := h.deps.getQueueStatus(context.Background(), queueStatusInput{ProcessPath: tc.in})
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out.Depth != tc.wantDepth {
				t.Fatalf("depth = %d, want %d", out.Depth, tc.wantDepth)
			}
		})
	}
}

func TestFindClaimableWork(t *testing.T) {
	h := newHarness(t)
	// Two PICK tasks; o-early has the earlier CPT so it must be "best".
	h.seedPending(t, task.Pick, "o-late", 2*time.Hour, "pick")
	h.seedPending(t, task.Pick, "o-early", time.Hour, "pick")

	out, err := h.deps.findClaimableWork(context.Background(), findClaimableInput{ProcessPath: "PICK"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.CandidateCount != 2 {
		t.Fatalf("candidateCount = %d, want 2", out.CandidateCount)
	}
	if out.Best == nil || out.Best.OrderRef != "o-early" {
		t.Fatalf("best = %+v, want earliest-CPT order o-early", out.Best)
	}

	// Empty queue -> nil best, zero candidates.
	empty, err := h.deps.findClaimableWork(context.Background(), findClaimableInput{ProcessPath: "SLAM"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if empty.CandidateCount != 0 || empty.Best != nil {
		t.Fatalf("empty queue = %+v, want 0 candidates and nil best", empty)
	}

	if _, err := h.deps.findClaimableWork(context.Background(), findClaimableInput{ProcessPath: "NOPE"}); err == nil {
		t.Fatal("unknown path must error")
	}
}

func TestDiagnoseStuckTasks(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Register a station and claim a PICK task, creating an active lease that
	// expires at base + DefaultLeaseDuration (5m).
	if _, err := h.station.Execute(ctx, "s1", []string{"pick"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	h.seedPending(t, task.Pick, "o1", time.Hour, "pick")
	if _, err := h.claim.Execute(ctx, "s1", task.Pick); err != nil {
		t.Fatalf("claim: %v", err)
	}

	// At base, lease is healthy: withinSeconds=0 flags nothing.
	out, err := h.deps.diagnoseStuckTasks(ctx, diagnoseInput{WithinSeconds: 0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Count != 0 {
		t.Fatalf("healthy lease flagged: %+v", out)
	}

	// A 10-minute window catches the 5-minute lease as "expiring soon".
	soon, err := h.deps.diagnoseStuckTasks(ctx, diagnoseInput{WithinSeconds: 600})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if soon.Count != 1 || soon.Tasks[0].LeaseStationId != "s1" {
		t.Fatalf("expected 1 expiring task for s1, got %+v", soon)
	}

	// Advance past expiry: withinSeconds=0 now flags it as already-expired.
	h.clock.Advance(6 * time.Minute)
	expired, err := h.deps.diagnoseStuckTasks(ctx, diagnoseInput{WithinSeconds: 0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if expired.Count != 1 {
		t.Fatalf("expected 1 expired task, got %+v", expired)
	}
	if expired.Tasks[0].Reason == "" {
		t.Fatal("expired task missing reason")
	}
}
