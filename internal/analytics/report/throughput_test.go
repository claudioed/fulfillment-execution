package report_test

import (
	"context"
	"testing"
	"time"

	"github.com/claudioed/fulfillment-execution/internal/analytics/report"
)

// fakeStore is an in-memory implementation of both report ports used to
// exercise report derivation from a synthetic event sequence. It is a test
// double local to this package: the production stores live in the
// analyticsstore outbound adapter (Task 1.3/1.4).
type fakeStore struct {
	seen map[string]bool
	rows map[report.RowKey]*acc
	// claim records the claim time per (taskType, stationId, taskId) so a
	// later completion can compute claim-to-complete seconds.
	claims map[claimKey]time.Time
	now    time.Time
}

// acc is the fake store's per-row accumulator, kept separate from the public
// report.Row so the running-total intermediate state never leaks into the
// read-model type.
type acc struct {
	completions                 int
	leaseExpiries               int
	weighCheckDiverts           int
	totalClaimToCompleteSeconds float64
	completionsWithClaim        int
}

type claimKey struct {
	taskType  string
	stationId string
	taskId    string
}

func newFakeStore(now time.Time) *fakeStore {
	return &fakeStore{
		seen:   map[string]bool{},
		rows:   map[report.RowKey]*acc{},
		claims: map[claimKey]time.Time{},
		now:    now,
	}
}

func (s *fakeStore) row(k report.RowKey) *acc {
	r, ok := s.rows[k]
	if !ok {
		r = &acc{}
		s.rows[k] = r
	}
	return r
}

func (s *fakeStore) dup(eventId string) bool {
	if s.seen[eventId] {
		return true
	}
	s.seen[eventId] = true
	return false
}

func hourBucket(t time.Time) time.Time {
	return t.UTC().Truncate(time.Hour)
}

func (s *fakeStore) ApplyTaskClaimed(_ context.Context, eventId, taskId, taskType, stationId string, at time.Time) error {
	if s.dup(eventId) {
		return nil
	}
	s.claims[claimKey{taskType, stationId, taskId}] = at
	return nil
}

func (s *fakeStore) ApplyTaskCompleted(_ context.Context, eventId, taskId, taskType, stationId string, at time.Time) error {
	if s.dup(eventId) {
		return nil
	}
	r := s.row(report.RowKey{TaskType: taskType, StationId: stationId, HourBucket: hourBucket(at)})
	r.completions++
	if claimed, ok := s.claims[claimKey{taskType, stationId, taskId}]; ok {
		r.totalClaimToCompleteSeconds += at.Sub(claimed).Seconds()
		r.completionsWithClaim++
	}
	return nil
}

func (s *fakeStore) ApplyLeaseExpired(_ context.Context, eventId, taskId, taskType, stationId string, at time.Time) error {
	if s.dup(eventId) {
		return nil
	}
	r := s.row(report.RowKey{TaskType: taskType, StationId: stationId, HourBucket: hourBucket(at)})
	r.leaseExpiries++
	return nil
}

func (s *fakeStore) ApplyWeightDiscrepancy(_ context.Context, eventId, taskType, stationId string, at time.Time) error {
	if s.dup(eventId) {
		return nil
	}
	r := s.row(report.RowKey{TaskType: taskType, StationId: stationId, HourBucket: hourBucket(at)})
	r.weighCheckDiverts++
	return nil
}

func (s *fakeStore) Query(_ context.Context, q report.ReportQuery) (report.ThroughputReport, error) {
	out := report.ThroughputReport{}
	for k, r := range s.rows {
		if k.HourBucket.Before(q.From) || !k.HourBucket.Before(q.To) {
			continue
		}
		if q.TaskType != "" && k.TaskType != q.TaskType {
			continue
		}
		if q.StationId != "" && k.StationId != q.StationId {
			continue
		}
		row := report.Row{
			Key:               k,
			Completions:       r.completions,
			LeaseExpiries:     r.leaseExpiries,
			WeighCheckDiverts: r.weighCheckDiverts,
		}
		if r.completionsWithClaim > 0 {
			row.AvgClaimToCompleteSeconds = r.totalClaimToCompleteSeconds / float64(r.completionsWithClaim)
		}
		out.Rows = append(out.Rows, row)
	}
	return out, nil
}

func (s *fakeStore) FreshnessLag(_ context.Context) (time.Duration, error) {
	return 0, nil
}

func TestThroughputReport_DerivesFromEventSequence(t *testing.T) {
	base := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	s := newFakeStore(base)
	ctx := context.Background()

	// Synthetic sequence for one station in one hour bucket:
	//  - task A: claimed then completed 30s later
	//  - task B: claimed then completed 90s later
	//  - task C: lease expired
	//  - one weigh-check divert
	must(t, s.ApplyTaskClaimed(ctx, "e1", "A", "PICK", "st1", base))
	must(t, s.ApplyTaskCompleted(ctx, "e2", "A", "PICK", "st1", base.Add(30*time.Second)))
	must(t, s.ApplyTaskClaimed(ctx, "e3", "B", "PICK", "st1", base.Add(time.Minute)))
	must(t, s.ApplyTaskCompleted(ctx, "e4", "B", "PICK", "st1", base.Add(time.Minute+90*time.Second)))
	must(t, s.ApplyLeaseExpired(ctx, "e5", "C", "PICK", "st1", base.Add(2*time.Minute)))
	must(t, s.ApplyWeightDiscrepancy(ctx, "e6", "PACK", "st2", base.Add(3*time.Minute)))

	rep, err := s.Query(ctx, report.ReportQuery{
		From:        base.Add(-time.Hour),
		To:          base.Add(2 * time.Hour),
		Granularity: report.GranularityHour,
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	bucket := base.Truncate(time.Hour)
	pickRow := findRow(rep, report.RowKey{TaskType: "PICK", StationId: "st1", HourBucket: bucket})
	if pickRow == nil {
		t.Fatal("no PICK/st1 row")
	}
	if pickRow.Completions != 2 {
		t.Errorf("Completions = %d, want 2", pickRow.Completions)
	}
	if pickRow.LeaseExpiries != 1 {
		t.Errorf("LeaseExpiries = %d, want 1", pickRow.LeaseExpiries)
	}
	if pickRow.AvgClaimToCompleteSeconds != 60 {
		t.Errorf("AvgClaimToCompleteSeconds = %v, want 60", pickRow.AvgClaimToCompleteSeconds)
	}

	packRow := findRow(rep, report.RowKey{TaskType: "PACK", StationId: "st2", HourBucket: bucket})
	if packRow == nil {
		t.Fatal("no PACK/st2 row")
	}
	if packRow.WeighCheckDiverts != 1 {
		t.Errorf("WeighCheckDiverts = %d, want 1", packRow.WeighCheckDiverts)
	}
}

func TestThroughputReport_FiltersAndIdempotency(t *testing.T) {
	base := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	ctx := context.Background()

	tests := []struct {
		name  string
		query report.ReportQuery
		want  int // number of rows expected
	}{
		{"no filter", report.ReportQuery{From: base.Add(-time.Hour), To: base.Add(time.Hour), Granularity: report.GranularityHour}, 2},
		{"task type filter", report.ReportQuery{From: base.Add(-time.Hour), To: base.Add(time.Hour), TaskType: "PICK", Granularity: report.GranularityHour}, 1},
		{"station filter", report.ReportQuery{From: base.Add(-time.Hour), To: base.Add(time.Hour), StationId: "st2", Granularity: report.GranularityHour}, 1},
		{"window excludes all", report.ReportQuery{From: base.Add(24 * time.Hour), To: base.Add(48 * time.Hour), Granularity: report.GranularityHour}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newFakeStore(base)
			// Apply the same completion twice with the same eventId → counts once.
			must(t, s.ApplyTaskCompleted(ctx, "dup", "A", "PICK", "st1", base))
			must(t, s.ApplyTaskCompleted(ctx, "dup", "A", "PICK", "st1", base))
			must(t, s.ApplyTaskCompleted(ctx, "other", "B", "PACK", "st2", base))

			rep, err := s.Query(ctx, tt.query)
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			if len(rep.Rows) != tt.want {
				t.Errorf("rows = %d, want %d", len(rep.Rows), tt.want)
			}
			if tt.name == "no filter" {
				pick := findRow(rep, report.RowKey{TaskType: "PICK", StationId: "st1", HourBucket: base.Truncate(time.Hour)})
				if pick == nil || pick.Completions != 1 {
					t.Errorf("dedupe failed: PICK completions = %v", pick)
				}
			}
		})
	}
}

func findRow(rep report.ThroughputReport, k report.RowKey) *report.Row {
	for i := range rep.Rows {
		if rep.Rows[i].Key == k {
			return &rep.Rows[i]
		}
	}
	return nil
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
}
