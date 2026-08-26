package report

import (
	"context"
	"time"
)

// ReportStore is the read side of the throughput data product: the reader
// process queries it to serve reports. It is read-only by contract — the
// Postgres implementation runs over a pool pinned to a read-only role.
type ReportStore interface {
	// Query returns the throughput rows matching q.
	Query(ctx context.Context, q ReportQuery) (ThroughputReport, error)
	// FreshnessLag reports how far the read model lags real time: the age
	// of the most recently applied event. A larger lag means the projection
	// is further behind the event stream.
	FreshnessLag(ctx context.Context) (time.Duration, error)
}

// ProjectionStore is the write side of the throughput data product: the
// projector process applies each consumed event to it. Every Apply* method
// is idempotent on eventId — applying the same eventId twice records the
// effect once, so the at-least-once Kafka stream can be projected exactly
// once.
//
// The methods take the derivation-relevant fields already extracted from the
// analytics envelope (rather than a domain event) so this port stays free of
// any OLTP domain dependency.
type ProjectionStore interface {
	// ApplyTaskClaimed records that taskId (of taskType) was claimed by
	// stationId at `at`, so a later completion can compute claim-to-complete.
	ApplyTaskClaimed(ctx context.Context, eventId, taskId, taskType, stationId string, at time.Time) error
	// ApplyTaskCompleted records a completion of taskId at `at`, updating the
	// (taskType, stationId, hour) row's Completions and claim-to-complete.
	ApplyTaskCompleted(ctx context.Context, eventId, taskId, taskType, stationId string, at time.Time) error
	// ApplyLeaseExpired records a lease expiry for taskId at `at`.
	ApplyLeaseExpired(ctx context.Context, eventId, taskId, taskType, stationId string, at time.Time) error
	// ApplyWeightDiscrepancy records a SLAM weigh-check diversion at `at`.
	ApplyWeightDiscrepancy(ctx context.Context, eventId, taskType, stationId string, at time.Time) error
}
