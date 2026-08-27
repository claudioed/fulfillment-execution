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

// PostgresProjection is the WRITER implementation of report.ProjectionStore,
// backed by a pgxpool over the analytical database. Every Apply* runs in a
// transaction that first claims the event id in analytics_processed_events
// (ON CONFLICT DO NOTHING); it only mutates the rollup when the claim is new,
// making each apply idempotent per eventId under Kafka's at-least-once
// delivery. It is the only writer of the analytical database.
type PostgresProjection struct {
	pool *pgxpool.Pool
}

// NewPostgresProjection constructs a PostgresProjection over pool.
func NewPostgresProjection(pool *pgxpool.Pool) *PostgresProjection {
	return &PostgresProjection{pool: pool}
}

// claim inserts eventId into analytics_processed_events, returning true iff
// this call newly recorded it (so the caller should apply the effect). It
// runs inside tx so the claim and the effect commit atomically.
func claim(ctx context.Context, tx pgx.Tx, eventId string, occurredAt time.Time) (bool, error) {
	tag, err := tx.Exec(ctx,
		`INSERT INTO analytics_processed_events (event_id, occurred_at)
		 VALUES ($1, $2) ON CONFLICT (event_id) DO NOTHING`,
		eventId, occurredAt)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// inTx runs fn in a transaction, committing on success and rolling back on
// error.
func (p *PostgresProjection) inTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}

// ApplyTaskClaimed records the claim time so a later completion can compute
// claim-to-complete. Idempotent on eventId.
func (p *PostgresProjection) ApplyTaskClaimed(ctx context.Context, eventId, taskId, taskType, stationId string, at time.Time) error {
	return p.inTx(ctx, func(tx pgx.Tx) error {
		isNew, err := claim(ctx, tx, eventId, at)
		if err != nil {
			return fmt.Errorf("analyticsstore: claim event: %w", err)
		}
		if !isNew {
			return nil
		}
		_, err = tx.Exec(ctx,
			`INSERT INTO analytics_pending_claims (task_type, station_id, task_id, claimed_at)
			 VALUES ($1, $2, $3, $4)
			 ON CONFLICT (task_type, station_id, task_id) DO UPDATE SET claimed_at = EXCLUDED.claimed_at`,
			taskType, stationId, taskId, at)
		if err != nil {
			return fmt.Errorf("analyticsstore: record claim: %w", err)
		}
		return nil
	})
}

// ApplyTaskCompleted increments completions and folds in claim-to-complete
// seconds when the matching claim is known. Idempotent on eventId.
func (p *PostgresProjection) ApplyTaskCompleted(ctx context.Context, eventId, taskId, taskType, stationId string, at time.Time) error {
	return p.inTx(ctx, func(tx pgx.Tx) error {
		isNew, err := claim(ctx, tx, eventId, at)
		if err != nil {
			return fmt.Errorf("analyticsstore: claim event: %w", err)
		}
		if !isNew {
			return nil
		}

		var claimedAt time.Time
		var haveClaim bool
		row := tx.QueryRow(ctx,
			`SELECT claimed_at FROM analytics_pending_claims
			 WHERE task_type = $1 AND station_id = $2 AND task_id = $3`,
			taskType, stationId, taskId)
		switch err := row.Scan(&claimedAt); {
		case err == nil:
			haveClaim = true
		case errors.Is(err, pgx.ErrNoRows):
			haveClaim = false
		default:
			return fmt.Errorf("analyticsstore: lookup claim: %w", err)
		}

		var claimSeconds float64
		var withClaim int
		if haveClaim {
			claimSeconds = at.Sub(claimedAt).Seconds()
			withClaim = 1
		}

		if err := upsertRollup(ctx, tx, taskType, stationId, at, rollupDelta{
			completions:          1,
			claimSeconds:         claimSeconds,
			completionsWithClaim: withClaim,
		}); err != nil {
			return err
		}

		if haveClaim {
			if _, err := tx.Exec(ctx,
				`DELETE FROM analytics_pending_claims
				 WHERE task_type = $1 AND station_id = $2 AND task_id = $3`,
				taskType, stationId, taskId); err != nil {
				return fmt.Errorf("analyticsstore: clear claim: %w", err)
			}
		}
		return nil
	})
}

// ApplyLeaseExpired increments the lease-expiry counter. Idempotent on eventId.
func (p *PostgresProjection) ApplyLeaseExpired(ctx context.Context, eventId, taskId, taskType, stationId string, at time.Time) error {
	return p.inTx(ctx, func(tx pgx.Tx) error {
		isNew, err := claim(ctx, tx, eventId, at)
		if err != nil {
			return fmt.Errorf("analyticsstore: claim event: %w", err)
		}
		if !isNew {
			return nil
		}
		return upsertRollup(ctx, tx, taskType, stationId, at, rollupDelta{leaseExpiries: 1})
	})
}

// ApplyWeightDiscrepancy increments the weigh-check divert counter.
// Idempotent on eventId.
func (p *PostgresProjection) ApplyWeightDiscrepancy(ctx context.Context, eventId, taskType, stationId string, at time.Time) error {
	return p.inTx(ctx, func(tx pgx.Tx) error {
		isNew, err := claim(ctx, tx, eventId, at)
		if err != nil {
			return fmt.Errorf("analyticsstore: claim event: %w", err)
		}
		if !isNew {
			return nil
		}
		return upsertRollup(ctx, tx, taskType, stationId, at, rollupDelta{weighCheckDiverts: 1})
	})
}

// rollupDelta is the set of counter increments a single event contributes to
// a throughput row.
type rollupDelta struct {
	completions          int
	leaseExpiries        int
	weighCheckDiverts    int
	claimSeconds         float64
	completionsWithClaim int
}

// upsertRollup adds delta into the (task_type, station_id, hour_bucket) row,
// inserting it if absent. hour_bucket is derived by truncating at to the
// UTC hour.
func upsertRollup(ctx context.Context, tx pgx.Tx, taskType, stationId string, at time.Time, delta rollupDelta) error {
	bucket := at.UTC().Truncate(time.Hour)
	_, err := tx.Exec(ctx,
		`INSERT INTO throughput_rollup (
			task_type, station_id, hour_bucket,
			completions, lease_expiries, weigh_check_diverts,
			claim_to_complete_seconds, completions_with_claim)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 ON CONFLICT (task_type, station_id, hour_bucket) DO UPDATE SET
			completions               = throughput_rollup.completions + EXCLUDED.completions,
			lease_expiries            = throughput_rollup.lease_expiries + EXCLUDED.lease_expiries,
			weigh_check_diverts       = throughput_rollup.weigh_check_diverts + EXCLUDED.weigh_check_diverts,
			claim_to_complete_seconds = throughput_rollup.claim_to_complete_seconds + EXCLUDED.claim_to_complete_seconds,
			completions_with_claim    = throughput_rollup.completions_with_claim + EXCLUDED.completions_with_claim`,
		taskType, stationId, bucket,
		delta.completions, delta.leaseExpiries, delta.weighCheckDiverts,
		delta.claimSeconds, delta.completionsWithClaim)
	if err != nil {
		return fmt.Errorf("analyticsstore: upsert rollup: %w", err)
	}
	return nil
}

// Compile-time assertion that PostgresProjection satisfies the write port.
var _ report.ProjectionStore = (*PostgresProjection)(nil)
