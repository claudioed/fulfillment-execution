// Package analyticsstore provides the outbound adapters that persist and
// serve the fulfillment throughput read model: an in-memory implementation
// (MemoryStore) for tests and local runs, and Postgres implementations (a
// writer projection and a read-only reader) for deployment. All satisfy the
// report.ProjectionStore and/or report.ReportStore ports.
package analyticsstore

import (
	"context"
	"sync"
	"time"

	"github.com/claudioed/fulfillment-execution/internal/analytics/report"
)

// MemoryStore is an in-memory implementation of both report.ProjectionStore
// (write) and report.ReportStore (read), backed by maps. It is idempotent
// per eventId via a seen-set, so a duplicate delivery is a no-op. It is safe
// for concurrent use.
type MemoryStore struct {
	// Now supplies the current time for FreshnessLag; defaults to time.Now
	// when nil so lag is deterministic under test.
	Now func() time.Time

	mu     sync.Mutex
	seen   map[string]struct{}
	rows   map[report.RowKey]*rowAcc
	claims map[string]time.Time // key: taskType|stationId|taskId
	// latest is the OccurredAt of the most recently applied event, used to
	// compute FreshnessLag.
	latest time.Time
}

// rowAcc accumulates the running totals for one report row. AvgClaim is
// derived from the two claim-to-complete fields at query time.
type rowAcc struct {
	completions          int
	leaseExpiries        int
	weighCheckDiverts    int
	totalClaimSeconds    float64
	completionsWithClaim int
}

// NewMemoryStore constructs an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		seen:   map[string]struct{}{},
		rows:   map[report.RowKey]*rowAcc{},
		claims: map[string]time.Time{},
	}
}

func claimKey(taskType, stationId, taskId string) string {
	return taskType + "|" + stationId + "|" + taskId
}

func hourBucket(t time.Time) time.Time {
	return t.UTC().Truncate(time.Hour)
}

// firstApply marks eventId as seen and reports whether this is the first
// time (so the caller should apply the effect) or a duplicate (skip). It
// also advances the freshness watermark. The caller must hold s.mu.
func (s *MemoryStore) firstApply(eventId string, at time.Time) bool {
	if _, dup := s.seen[eventId]; dup {
		return false
	}
	s.seen[eventId] = struct{}{}
	if at.After(s.latest) {
		s.latest = at
	}
	return true
}

func (s *MemoryStore) row(k report.RowKey) *rowAcc {
	r, ok := s.rows[k]
	if !ok {
		r = &rowAcc{}
		s.rows[k] = r
	}
	return r
}

// ApplyTaskClaimed records a claim so a later completion can compute
// claim-to-complete. Idempotent on eventId.
func (s *MemoryStore) ApplyTaskClaimed(_ context.Context, eventId, taskId, taskType, stationId string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.firstApply(eventId, at) {
		return nil
	}
	s.claims[claimKey(taskType, stationId, taskId)] = at
	return nil
}

// ApplyTaskCompleted records a completion, incrementing the row's completion
// count and folding claim-to-complete seconds in when the claim is known.
// Idempotent on eventId.
func (s *MemoryStore) ApplyTaskCompleted(_ context.Context, eventId, taskId, taskType, stationId string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.firstApply(eventId, at) {
		return nil
	}
	r := s.row(report.RowKey{TaskType: taskType, StationId: stationId, HourBucket: hourBucket(at)})
	r.completions++
	if claimed, ok := s.claims[claimKey(taskType, stationId, taskId)]; ok {
		r.totalClaimSeconds += at.Sub(claimed).Seconds()
		r.completionsWithClaim++
	}
	return nil
}

// ApplyLeaseExpired records a lease expiry. Idempotent on eventId.
func (s *MemoryStore) ApplyLeaseExpired(_ context.Context, eventId, taskId, taskType, stationId string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.firstApply(eventId, at) {
		return nil
	}
	r := s.row(report.RowKey{TaskType: taskType, StationId: stationId, HourBucket: hourBucket(at)})
	r.leaseExpiries++
	return nil
}

// ApplyWeightDiscrepancy records a SLAM weigh-check diversion. Idempotent on
// eventId.
func (s *MemoryStore) ApplyWeightDiscrepancy(_ context.Context, eventId, taskType, stationId string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.firstApply(eventId, at) {
		return nil
	}
	r := s.row(report.RowKey{TaskType: taskType, StationId: stationId, HourBucket: hourBucket(at)})
	r.weighCheckDiverts++
	return nil
}

// Query returns the rows matching q. From is inclusive, To is exclusive,
// both compared against a row's HourBucket; empty TaskType/StationId means
// no filter on that dimension.
func (s *MemoryStore) Query(_ context.Context, q report.ReportQuery) (report.ThroughputReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

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
			row.AvgClaimToCompleteSeconds = r.totalClaimSeconds / float64(r.completionsWithClaim)
		}
		out.Rows = append(out.Rows, row)
	}
	return out, nil
}

// FreshnessLag returns how far the read model lags real time: now minus the
// OccurredAt of the most recently applied event. Zero when nothing has been
// applied yet, and never negative (a future-dated event clamps to zero).
func (s *MemoryStore) FreshnessLag(_ context.Context) (time.Duration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.latest.IsZero() {
		return 0, nil
	}
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	lag := now.Sub(s.latest)
	if lag < 0 {
		return 0, nil
	}
	return lag, nil
}
