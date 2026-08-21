//go:build integration

package postgres_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/postgres"
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
