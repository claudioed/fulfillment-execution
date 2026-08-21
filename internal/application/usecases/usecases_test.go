package usecases_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/events"
	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/memory"
	"github.com/claudioed/fulfillment-execution/internal/application/usecases"
	pack "github.com/claudioed/fulfillment-execution/internal/domain/package"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
	"github.com/claudioed/fulfillment-execution/internal/domain/station"
	"github.com/claudioed/fulfillment-execution/internal/domain/task"
)

var epoch = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func idSeq(prefix string) func() shared.TaskId {
	n := 0
	return func() shared.TaskId {
		n++
		return shared.TaskId(prefix + string(rune('0'+n)))
	}
}

type harness struct {
	tasks     *memory.TaskRepo
	stations  *memory.StationRepo
	packages  *memory.PackageRepo
	publisher *events.BufferedPublisher
	clock     *memory.FixedClock
}

func newHarness() *harness {
	return &harness{
		tasks:     memory.NewTaskRepo(),
		stations:  memory.NewStationRepo(),
		packages:  memory.NewPackageRepo(),
		publisher: events.NewBufferedPublisher(),
		clock:     memory.NewFixedClock(epoch),
	}
}

func TestCreateTask_PutsTaskInPool(t *testing.T) {
	h := newHarness()
	uc := &usecases.CreateTask{Tasks: h.tasks, Publisher: h.publisher, Clock: h.clock, NewId: idSeq("t")}

	tk, err := uc.Execute(context.Background(), task.Pick, shared.NewCPT(epoch.Add(time.Hour)), "order-1", shared.NewCapabilitySet("pick"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tk.Status() != task.Pending {
		t.Fatalf("expected Pending, got %s", tk.Status())
	}

	got, _ := h.tasks.FindById(context.Background(), tk.Id())
	if got == nil {
		t.Fatalf("expected task to be persisted")
	}
	if len(h.publisher.Events()) != 1 || h.publisher.Events()[0].EventName() != "TaskCreated" {
		t.Fatalf("expected a single TaskCreated event, got %+v", h.publisher.Events())
	}
}

func TestClaimNext_SelectsEarliestCPTMatchingCapabilities(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	create := &usecases.CreateTask{Tasks: h.tasks, Publisher: h.publisher, Clock: h.clock, NewId: idSeq("t")}

	// A later-CPT task that matches, and an earlier-CPT task requiring a
	// capability the station lacks — ClaimNext must skip it and take the
	// best-fit match, not merely the earliest CPT overall.
	_, _ = create.Execute(ctx, task.Pick, shared.NewCPT(epoch.Add(30*time.Minute)), "order-hazmat", shared.NewCapabilitySet("pick", "hazmat"))
	wantTask, _ := create.Execute(ctx, task.Pick, shared.NewCPT(epoch.Add(time.Hour)), "order-1", shared.NewCapabilitySet("pick"))

	_ = h.stations.Save(ctx, station.New("s1", shared.NewCapabilitySet("pick")))

	claim := &usecases.ClaimNext{Tasks: h.tasks, Stations: h.stations, Publisher: h.publisher, Clock: h.clock}
	got, err := claim.Execute(ctx, "s1", task.Pick)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Id() != wantTask.Id() {
		t.Fatalf("expected task %s (capability-matching), got %s", wantTask.Id(), got.Id())
	}
	if got.Status() != task.Claimed {
		t.Fatalf("expected Claimed, got %s", got.Status())
	}
}

func TestClaimNext_ReturnsErrNoClaimableTaskWhenPoolEmpty(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	_ = h.stations.Save(ctx, station.New("s1", shared.NewCapabilitySet("pick")))

	claim := &usecases.ClaimNext{Tasks: h.tasks, Stations: h.stations, Publisher: h.publisher, Clock: h.clock}
	_, err := claim.Execute(ctx, "s1", task.Pick)
	if !errors.Is(err, usecases.ErrNoClaimableTask) {
		t.Fatalf("expected ErrNoClaimableTask, got %v", err)
	}
}

// Invariant (through the use case): at-most-once claim — a second station
// cannot claim a task another station holds an active lease on.
func TestClaimNext_AtMostOnce_SecondStationCannotClaimSameTask(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	create := &usecases.CreateTask{Tasks: h.tasks, Publisher: h.publisher, Clock: h.clock, NewId: idSeq("t")}
	_, _ = create.Execute(ctx, task.Pick, shared.NewCPT(epoch.Add(time.Hour)), "order-1", shared.NewCapabilitySet("pick"))

	_ = h.stations.Save(ctx, station.New("s1", shared.NewCapabilitySet("pick")))
	_ = h.stations.Save(ctx, station.New("s2", shared.NewCapabilitySet("pick")))

	claim := &usecases.ClaimNext{Tasks: h.tasks, Stations: h.stations, Publisher: h.publisher, Clock: h.clock}
	first, err := claim.Execute(ctx, "s1", task.Pick)
	if err != nil {
		t.Fatalf("first claim should succeed: %v", err)
	}

	_, err = claim.Execute(ctx, "s2", task.Pick)
	if !errors.Is(err, usecases.ErrNoClaimableTask) {
		t.Fatalf("expected ErrNoClaimableTask (task already leased), got %v", err)
	}
	if first.Lease().StationId != shared.StationId("s1") {
		t.Fatalf("lease should remain owned by s1")
	}
}

// Lease-expiry via a FixedClock: after the lease window passes, the pool
// makes the task claimable again.
func TestClaimNext_ExpiredLeaseReturnsTaskToPool(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	create := &usecases.CreateTask{Tasks: h.tasks, Publisher: h.publisher, Clock: h.clock, NewId: idSeq("t")}
	_, _ = create.Execute(ctx, task.Pick, shared.NewCPT(epoch.Add(time.Hour)), "order-1", shared.NewCapabilitySet("pick"))

	_ = h.stations.Save(ctx, station.New("s1", shared.NewCapabilitySet("pick")))
	_ = h.stations.Save(ctx, station.New("s2", shared.NewCapabilitySet("pick")))

	claim := &usecases.ClaimNext{Tasks: h.tasks, Stations: h.stations, Publisher: h.publisher, Clock: h.clock, LeaseDuration: time.Minute}
	_, err := claim.Execute(ctx, "s1", task.Pick)
	if err != nil {
		t.Fatalf("first claim should succeed: %v", err)
	}

	h.clock.Advance(2 * time.Minute)

	got, err := claim.Execute(ctx, "s2", task.Pick)
	if err != nil {
		t.Fatalf("claim after lease expiry should succeed: %v", err)
	}
	if got.Lease().StationId != shared.StationId("s2") {
		t.Fatalf("expected s2 to hold the new claim, got %s", got.Lease().StationId)
	}
}

func TestExpireLeases_SweepsExpiredClaimsBackToPending(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	create := &usecases.CreateTask{Tasks: h.tasks, Publisher: h.publisher, Clock: h.clock, NewId: idSeq("t")}
	tk, _ := create.Execute(ctx, task.Pick, shared.NewCPT(epoch.Add(time.Hour)), "order-1", shared.NewCapabilitySet("pick"))
	_ = h.stations.Save(ctx, station.New("s1", shared.NewCapabilitySet("pick")))

	claim := &usecases.ClaimNext{Tasks: h.tasks, Stations: h.stations, Publisher: h.publisher, Clock: h.clock, LeaseDuration: time.Minute}
	_, _ = claim.Execute(ctx, "s1", task.Pick)

	h.clock.Advance(2 * time.Minute)

	sweep := &usecases.ExpireLeases{Tasks: h.tasks, Publisher: h.publisher, Clock: h.clock}
	freed, err := sweep.Execute(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if freed != 1 {
		t.Fatalf("expected 1 freed task, got %d", freed)
	}

	got, _ := h.tasks.FindById(ctx, tk.Id())
	if got.Status() != task.Pending {
		t.Fatalf("expected Pending after sweep, got %s", got.Status())
	}
}

func TestRenewLease_ExtendsClaim(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	create := &usecases.CreateTask{Tasks: h.tasks, Publisher: h.publisher, Clock: h.clock, NewId: idSeq("t")}
	_, _ = create.Execute(ctx, task.Pick, shared.NewCPT(epoch.Add(time.Hour)), "order-1", shared.NewCapabilitySet("pick"))
	_ = h.stations.Save(ctx, station.New("s1", shared.NewCapabilitySet("pick")))

	claim := &usecases.ClaimNext{Tasks: h.tasks, Stations: h.stations, Publisher: h.publisher, Clock: h.clock, LeaseDuration: time.Minute}
	claimed, _ := claim.Execute(ctx, "s1", task.Pick)

	h.clock.Advance(30 * time.Second)
	renew := &usecases.RenewLease{Tasks: h.tasks, Clock: h.clock, LeaseDuration: time.Minute}
	if err := renew.Execute(ctx, claimed.Id(), "s1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, _ := h.tasks.FindById(ctx, claimed.Id())
	if !got.Lease().Expiry.After(epoch.Add(time.Minute)) {
		t.Fatalf("expected extended lease expiry, got %v", got.Lease().Expiry)
	}
}

func TestCompleteTask_ValidatesOwnershipAndCompletes(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	create := &usecases.CreateTask{Tasks: h.tasks, Publisher: h.publisher, Clock: h.clock, NewId: idSeq("t")}
	_, _ = create.Execute(ctx, task.Pick, shared.NewCPT(epoch.Add(time.Hour)), "order-1", shared.NewCapabilitySet("pick"))
	_ = h.stations.Save(ctx, station.New("s1", shared.NewCapabilitySet("pick")))

	claim := &usecases.ClaimNext{Tasks: h.tasks, Stations: h.stations, Publisher: h.publisher, Clock: h.clock}
	claimed, _ := claim.Execute(ctx, "s1", task.Pick)

	complete := &usecases.CompleteTask{Tasks: h.tasks, Publisher: h.publisher, Clock: h.clock}

	if err := complete.Execute(ctx, claimed.Id(), "wrong-station"); err == nil {
		t.Fatalf("expected ownership validation to reject a non-owning station")
	}

	if err := complete.Execute(ctx, claimed.Id(), "s1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, _ := h.tasks.FindById(ctx, claimed.Id())
	if got.Status() != task.Completed {
		t.Fatalf("expected Completed, got %s", got.Status())
	}
}

func TestSealPackageAndRunSlam_LabelsWithinTolerance(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	create := &usecases.CreateTask{Tasks: h.tasks, Publisher: h.publisher, Clock: h.clock, NewId: idSeq("t")}
	_, _ = create.Execute(ctx, task.Pack, shared.NewCPT(epoch.Add(time.Hour)), "order-1", shared.NewCapabilitySet("pack"))
	_ = h.stations.Save(ctx, station.New("s1", shared.NewCapabilitySet("pack")))

	claim := &usecases.ClaimNext{Tasks: h.tasks, Stations: h.stations, Publisher: h.publisher, Clock: h.clock}
	claimed, _ := claim.Execute(ctx, "s1", task.Pack)

	seal := &usecases.SealPackage{Tasks: h.tasks, Packages: h.packages, Publisher: h.publisher, Clock: h.clock, NewId: func() shared.PackageId { return "p1" }}
	p, err := seal.Execute(ctx, claimed.Id(), "s1", []string{"sku-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	slam := &usecases.RunSlam{Packages: h.packages, Publisher: h.publisher, Clock: h.clock}
	if err := slam.Execute(ctx, p.Id(), 2.0, 2.0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, _ := h.packages.FindById(ctx, p.Id())
	if got.Status() != pack.Labeled {
		t.Fatalf("expected LABELED, got %s", got.Status())
	}
}

func TestGetQueueDepth_CountsPendingTasksOfType(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	create := &usecases.CreateTask{Tasks: h.tasks, Publisher: h.publisher, Clock: h.clock, NewId: idSeq("t")}
	_, _ = create.Execute(ctx, task.Pick, shared.NewCPT(epoch.Add(time.Hour)), "order-1", shared.NewCapabilitySet("pick"))
	_, _ = create.Execute(ctx, task.Pick, shared.NewCPT(epoch.Add(2*time.Hour)), "order-2", shared.NewCapabilitySet("pick"))
	_, _ = create.Execute(ctx, task.Pack, shared.NewCPT(epoch.Add(time.Hour)), "order-3", shared.NewCapabilitySet("pack"))

	depth := &usecases.GetQueueDepth{Tasks: h.tasks}
	got, err := depth.Execute(ctx, task.Pick)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 2 {
		t.Fatalf("expected 2, got %d", got)
	}
}
