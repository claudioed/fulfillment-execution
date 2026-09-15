// Package report holds the fulfillment throughput read model: the shapes of
// the analytical report the data product serves, the query that selects it,
// and the outbound ports the writer and reader adapters implement. It is a
// read-model region that depends on nothing else in this module — the OLTP
// domain and application layers must not import it, and it must not import
// them (ADR-0012).
package report

import "time"

// Granularity is the time-bucket resolution a report is rolled up to. Only
// hourly buckets are modelled for this round.
type Granularity string

const (
	// GranularityHour rolls rows up into UTC hour buckets.
	GranularityHour Granularity = "hour"
)

// RowKey identifies a single throughput row: the process path (task type),
// the station, and the UTC hour bucket the row aggregates. HourBucket is the
// bucket start, truncated to the hour in UTC.
type RowKey struct {
	TaskType   string
	StationId  string
	HourBucket time.Time
}

// Row is one aggregated throughput row for a (taskType, stationId,
// hourBucket) key.
type Row struct {
	Key RowKey
	// Completions is the number of TaskCompleted events in this bucket.
	Completions int
	// AvgClaimToCompleteSeconds is the mean elapsed seconds from a task's
	// claim to its completion, over the completions in this bucket that had
	// a recorded claim. Zero when no completion in the bucket had a claim.
	AvgClaimToCompleteSeconds float64
	// LeaseExpiries is the number of LeaseExpired events in this bucket.
	LeaseExpiries int
	// WeighCheckDiverts is the number of WeightDiscrepancyDetected events
	// (SLAM weigh-check diversions) in this bucket.
	WeighCheckDiverts int
	// PackagesManifested is the number of PackageManifested events (a SLAM
	// pass — see ADR-0025) in this bucket, attributed to the (task_type,
	// station_id) of the package's originating SLAM task. This is the
	// on-time-to-CPT KPI's denominator (companion to order-management
	// ADR 0014 §6 — see ADR-0026).
	PackagesManifested int
	// PackagesOnTimeToCPT is the subset of PackagesManifested whose
	// manifested_at (the PackageManifested event's occurred_at) was at or
	// before the originating SLAM task's CPT. The boundary instant counts
	// as on-time (manifestedAt <= cpt) — the deliberate mirror of
	// Task.IsCPTMissed's own boundary (now >= cpt counts as missed), so the
	// two predicates agree at the instant: manifesting exactly at CPT is a
	// promise kept, not broken.
	PackagesOnTimeToCPT int
	// PackagesLateToCPT is the complement of PackagesOnTimeToCPT within
	// PackagesManifested (manifestedAt > cpt). PackagesOnTimeToCPT +
	// PackagesLateToCPT always equals PackagesManifested.
	//
	// The on-time-to-CPT rate itself is NOT stored — like every other rate
	// in this report (e.g. lease-expiry rate, weigh-check divert rate), it
	// is derived by the caller from the raw counts
	// (PackagesOnTimeToCPT / PackagesManifested) rather than precomputed
	// here, so the read model never has to reconcile a stored percentage
	// against its own inputs.
	PackagesLateToCPT int
}

// ThroughputReport is the full result of a report query: the matching rows.
type ThroughputReport struct {
	Rows []Row
}

// ReportQuery selects and filters the rows a report covers. From is
// inclusive and To is exclusive, both compared against a row's HourBucket.
// TaskType and StationId are optional exact-match filters (empty means "no
// filter on this dimension").
type ReportQuery struct {
	From        time.Time
	To          time.Time
	TaskType    string
	StationId   string
	Granularity Granularity
}
