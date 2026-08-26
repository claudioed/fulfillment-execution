package analyticsstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/fulfillment-execution/internal/analytics/report"
)

// PostgresReport is the READER implementation of report.ReportStore, backed
// by a pgxpool over the analytical database. The pool it is given is expected
// to be pinned to a read-only role / default_transaction_read_only=on, so a
// bug in the reader cannot mutate the read model (ADR-0012). The reader never
// issues writes.
type PostgresReport struct {
	pool *pgxpool.Pool
}

// NewPostgresReport constructs a PostgresReport over pool.
func NewPostgresReport(pool *pgxpool.Pool) *PostgresReport {
	return &PostgresReport{pool: pool}
}

// Query returns the throughput rows matching q. The average claim-to-complete
// is derived in SQL from the running sum and the count of completions that
// had a claim. From is inclusive, To is exclusive; empty TaskType/StationId
// disables that filter.
func (r *PostgresReport) Query(ctx context.Context, q report.ReportQuery) (report.ThroughputReport, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT task_type, station_id, hour_bucket,
			completions, lease_expiries, weigh_check_diverts,
			CASE WHEN completions_with_claim > 0
			     THEN claim_to_complete_seconds / completions_with_claim
			     ELSE 0 END AS avg_claim_to_complete_seconds
		 FROM throughput_rollup
		 WHERE hour_bucket >= $1 AND hour_bucket < $2
		   AND ($3 = '' OR task_type = $3)
		   AND ($4 = '' OR station_id = $4)
		 ORDER BY hour_bucket, task_type, station_id`,
		q.From, q.To, q.TaskType, q.StationId)
	if err != nil {
		return report.ThroughputReport{}, fmt.Errorf("analyticsstore: query rollup: %w", err)
	}
	defer rows.Close()

	var out report.ThroughputReport
	for rows.Next() {
		var (
			row    report.Row
			bucket time.Time
		)
		if err := rows.Scan(
			&row.Key.TaskType, &row.Key.StationId, &bucket,
			&row.Completions, &row.LeaseExpiries, &row.WeighCheckDiverts,
			&row.AvgClaimToCompleteSeconds,
		); err != nil {
			return report.ThroughputReport{}, fmt.Errorf("analyticsstore: scan row: %w", err)
		}
		row.Key.HourBucket = bucket.UTC()
		out.Rows = append(out.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return report.ThroughputReport{}, fmt.Errorf("analyticsstore: iterate rows: %w", err)
	}
	return out, nil
}

// FreshnessLag returns now minus the most recent event's occurred_at, i.e.
// how far the read model trails real time. Zero when the read model is empty
// or (defensively) when the latest event is future-dated.
func (r *PostgresReport) FreshnessLag(ctx context.Context) (time.Duration, error) {
	var latest time.Time
	err := r.pool.QueryRow(ctx,
		`SELECT max(occurred_at) FROM analytics_processed_events`).Scan(&latest)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return 0, nil
	case err != nil:
		return 0, fmt.Errorf("analyticsstore: freshness query: %w", err)
	}
	if latest.IsZero() {
		return 0, nil
	}
	lag := time.Since(latest)
	if lag < 0 {
		return 0, nil
	}
	return lag, nil
}

// Compile-time assertion that PostgresReport satisfies the read port.
var _ report.ReportStore = (*PostgresReport)(nil)
