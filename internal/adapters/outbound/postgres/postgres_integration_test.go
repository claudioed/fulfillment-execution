//go:build integration

package postgres_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/postgres"
	pack "github.com/claudioed/fulfillment-execution/internal/domain/package"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
	"github.com/claudioed/fulfillment-execution/internal/domain/station"
	"github.com/claudioed/fulfillment-execution/internal/domain/task"
)

func requireDatabaseURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set, skipping postgres integration test")
	}
	return url
}

func newPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := requireDatabaseURL(t)
	if err := postgres.Migrate(url, "../../../../migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestTaskRepo_SaveAndFindById(t *testing.T) {
	pool := newPool(t)
	repo := postgres.NewTaskRepo(pool)

	id := shared.TaskId("integration-task-1")
	cpt := shared.NewCPT(time.Now().Add(time.Hour).Truncate(time.Microsecond))
	tk := task.New(id, task.Pick, cpt, "order-1", shared.NewCapabilitySet("pick"))

	if err := repo.Save(context.Background(), tk); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := repo.FindById(context.Background(), id)
	if err != nil {
		t.Fatalf("FindById: %v", err)
	}
	if got == nil {
		t.Fatalf("expected task to be found")
	}
	if got.Status() != task.Pending {
		t.Fatalf("expected Pending, got %s", got.Status())
	}
}

// TaskRepo's query specific to this repo: FindClaimableByType, which backs
// PULL dispatch (ClaimNext) and must return only Pending/lease-expired tasks
// of the requested type, earliest CPT first.
func TestTaskRepo_FindClaimableByType_OrdersByEarliestCPT(t *testing.T) {
	pool := newPool(t)
	repo := postgres.NewTaskRepo(pool)
	ctx := context.Background()
	now := time.Now().Truncate(time.Microsecond)

	later := task.New("integration-task-claimable-later", task.Pick, shared.NewCPT(now.Add(2*time.Hour)), "order-later", shared.NewCapabilitySet("pick"))
	earlier := task.New("integration-task-claimable-earlier", task.Pick, shared.NewCPT(now.Add(time.Hour)), "order-earlier", shared.NewCapabilitySet("pick"))
	wrongType := task.New("integration-task-claimable-pack", task.Pack, shared.NewCPT(now.Add(30*time.Minute)), "order-pack", shared.NewCapabilitySet("pack"))
	if err := repo.Save(ctx, later); err != nil {
		t.Fatalf("Save(later): %v", err)
	}
	if err := repo.Save(ctx, earlier); err != nil {
		t.Fatalf("Save(earlier): %v", err)
	}
	if err := repo.Save(ctx, wrongType); err != nil {
		t.Fatalf("Save(wrongType): %v", err)
	}

	candidates, err := repo.FindClaimableByType(ctx, task.Pick, now)
	if err != nil {
		t.Fatalf("FindClaimableByType: %v", err)
	}
	var earlierIdx, laterIdx = -1, -1
	for i, c := range candidates {
		if c.Type() != task.Pick {
			t.Fatalf("expected only PICK tasks, got %s", c.Type())
		}
		switch c.Id() {
		case earlier.Id():
			earlierIdx = i
		case later.Id():
			laterIdx = i
		}
	}
	if earlierIdx == -1 || laterIdx == -1 {
		t.Fatalf("expected both seeded tasks to be claimable, got %d candidates", len(candidates))
	}
	if earlierIdx >= laterIdx {
		t.Fatalf("expected earlier-CPT task (idx %d) to sort before later-CPT task (idx %d)", earlierIdx, laterIdx)
	}
}

func TestStationRepo_SaveAndFindById(t *testing.T) {
	pool := newPool(t)
	repo := postgres.NewStationRepo(pool)

	id := shared.StationId("integration-station-1")
	s := station.New(id, shared.NewCapabilitySet("pick"))

	if err := repo.Save(context.Background(), s); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := repo.FindById(context.Background(), id)
	if err != nil {
		t.Fatalf("FindById: %v", err)
	}
	if got == nil {
		t.Fatalf("expected station to be found")
	}
	if !got.CanAccept(shared.NewCapabilitySet("pick")) {
		t.Fatalf("expected round-tripped station to keep its capabilities")
	}
}

func TestPackageRepo_SaveAndFindById(t *testing.T) {
	pool := newPool(t)
	repo := postgres.NewPackageRepo(pool)
	ctx := context.Background()

	id := shared.PackageId("integration-package-1")
	p := pack.New(id, "order-1")
	if err := p.ScanItem("sku-1"); err != nil {
		t.Fatalf("ScanItem: %v", err)
	}
	if err := p.Seal(); err != nil {
		t.Fatalf("Seal: %v", err)
	}

	if err := repo.Save(ctx, p); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := repo.FindById(ctx, id)
	if err != nil {
		t.Fatalf("FindById: %v", err)
	}
	if got == nil {
		t.Fatalf("expected package to be found")
	}
	if got.Status() != pack.Sealed {
		t.Fatalf("expected Sealed, got %s", got.Status())
	}
	if len(got.ScannedContents()) != 1 || got.ScannedContents()[0] != "sku-1" {
		t.Fatalf("expected scanned contents [sku-1], got %v", got.ScannedContents())
	}

	// Package-specific: the SLAM weigh-check outcome (label vs. divert) must
	// round-trip through an update to the same row.
	if _, err := got.Weigh(2.0, 2.5); err != nil {
		t.Fatalf("Weigh: %v", err)
	}
	if err := repo.Save(ctx, got); err != nil {
		t.Fatalf("Save (post-weigh): %v", err)
	}
	reloaded, err := repo.FindById(ctx, id)
	if err != nil {
		t.Fatalf("FindById (post-weigh): %v", err)
	}
	if reloaded.Status() != pack.Diverted {
		t.Fatalf("expected Diverted after weigh-check discrepancy, got %s", reloaded.Status())
	}
}

// ProcessedEventsRepo's query specific to this repo: MarkProcessed must be
// idempotent so an at-least-once source (Kafka) is consumed exactly once.
func TestProcessedEventsRepo_MarkProcessed_IsIdempotent(t *testing.T) {
	pool := newPool(t)
	repo := postgres.NewProcessedEventsRepo(pool)
	ctx := context.Background()
	eventId := "integration-event-1"
	// Clean up any leftover row from a prior run of this test so the
	// idempotency check below starts from a known "not yet processed" state.
	if _, err := pool.Exec(ctx, `DELETE FROM processed_events WHERE event_id = $1`, eventId); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	first, err := repo.MarkProcessed(ctx, eventId)
	if err != nil {
		t.Fatalf("MarkProcessed (first): %v", err)
	}
	if !first {
		t.Fatalf("expected first MarkProcessed call to return true")
	}

	second, err := repo.MarkProcessed(ctx, eventId)
	if err != nil {
		t.Fatalf("MarkProcessed (second): %v", err)
	}
	if second {
		t.Fatalf("expected second MarkProcessed call for the same event id to return false")
	}
}
