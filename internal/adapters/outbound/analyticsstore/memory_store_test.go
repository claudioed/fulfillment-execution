package analyticsstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/analyticsstore"
	"github.com/claudioed/fulfillment-execution/internal/analytics/report"
)

func TestMemoryStore_ClaimCompleteIdempotent(t *testing.T) {
	base := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)
	ctx := context.Background()
	s := analyticsstore.NewMemoryStore()

	apply := func() {
		// TaskCreated has no projection method (it does not move a
		// throughput counter); the consumer ignores it. The lifecycle that
		// matters here is claim -> complete.
		if err := s.ApplyTaskClaimed(ctx, "claim-1", "T1", "PICK", "st1", base); err != nil {
			t.Fatalf("claim: %v", err)
		}
		if err := s.ApplyTaskCompleted(ctx, "complete-1", "T1", "PICK", "st1", base.Add(45*time.Second)); err != nil {
			t.Fatalf("complete: %v", err)
		}
	}

	// Apply the full sequence twice with the SAME event ids (duplicate
	// delivery): the counters must reflect one logical occurrence.
	apply()
	apply()

	rep, err := s.Query(ctx, report.ReportQuery{
		From:        base.Add(-time.Hour),
		To:          base.Add(time.Hour),
		Granularity: report.GranularityHour,
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rep.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rep.Rows))
	}
	row := rep.Rows[0]
	if row.Completions != 1 {
		t.Errorf("Completions = %d, want 1 (idempotent)", row.Completions)
	}
	if row.AvgClaimToCompleteSeconds != 45 {
		t.Errorf("AvgClaimToCompleteSeconds = %v, want 45", row.AvgClaimToCompleteSeconds)
	}
}

func TestMemoryStore_LeaseExpiryAndDivert(t *testing.T) {
	base := time.Date(2026, 4, 1, 9, 30, 0, 0, time.UTC)
	ctx := context.Background()
	s := analyticsstore.NewMemoryStore()

	if err := s.ApplyLeaseExpired(ctx, "le-1", "T2", "PICK", "st1", base); err != nil {
		t.Fatalf("lease expired: %v", err)
	}
	// Duplicate lease expiry ignored.
	if err := s.ApplyLeaseExpired(ctx, "le-1", "T2", "PICK", "st1", base); err != nil {
		t.Fatalf("lease expired dup: %v", err)
	}
	if err := s.ApplyWeightDiscrepancy(ctx, "wd-1", "PACK", "st2", base); err != nil {
		t.Fatalf("weight discrepancy: %v", err)
	}

	rep, err := s.Query(ctx, report.ReportQuery{
		From:        base.Add(-time.Hour),
		To:          base.Add(time.Hour),
		Granularity: report.GranularityHour,
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	var leaseRow, divertRow *report.Row
	for i := range rep.Rows {
		switch rep.Rows[i].Key.TaskType {
		case "PICK":
			leaseRow = &rep.Rows[i]
		case "PACK":
			divertRow = &rep.Rows[i]
		}
	}
	if leaseRow == nil || leaseRow.LeaseExpiries != 1 {
		t.Errorf("PICK lease expiries = %v, want 1", leaseRow)
	}
	if divertRow == nil || divertRow.WeighCheckDiverts != 1 {
		t.Errorf("PACK weigh-check diverts = %v, want 1", divertRow)
	}
}

func TestMemoryStore_FreshnessLag(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	s := analyticsstore.NewMemoryStore()
	s.Now = func() time.Time { return now }

	// No events yet: lag is zero.
	lag, err := s.FreshnessLag(ctx)
	if err != nil {
		t.Fatalf("FreshnessLag: %v", err)
	}
	if lag != 0 {
		t.Errorf("empty lag = %v, want 0", lag)
	}

	// An event 10 minutes old makes the lag 10 minutes.
	if err := s.ApplyTaskCompleted(ctx, "c", "T", "PICK", "st1", now.Add(-10*time.Minute)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	lag, err = s.FreshnessLag(ctx)
	if err != nil {
		t.Fatalf("FreshnessLag: %v", err)
	}
	if lag != 10*time.Minute {
		t.Errorf("lag = %v, want 10m", lag)
	}
}

// Compile-time assertions that MemoryStore satisfies both ports.
var (
	_ report.ProjectionStore = (*analyticsstore.MemoryStore)(nil)
	_ report.ReportStore     = (*analyticsstore.MemoryStore)(nil)
)
