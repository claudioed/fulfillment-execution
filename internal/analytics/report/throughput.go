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
