package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ProcessedEventsRepo is a pgxpool-backed implementation of
// ports.ProcessedEvents.
type ProcessedEventsRepo struct {
	pool *pgxpool.Pool
}

// NewProcessedEventsRepo constructs a ProcessedEventsRepo backed by pool.
func NewProcessedEventsRepo(pool *pgxpool.Pool) *ProcessedEventsRepo {
	return &ProcessedEventsRepo{pool: pool}
}

func (r *ProcessedEventsRepo) MarkProcessed(ctx context.Context, eventId string) (bool, error) {
	tag, err := r.pool.Exec(ctx,
		`INSERT INTO processed_events (event_id, processed_at) VALUES ($1, now()) ON CONFLICT (event_id) DO NOTHING`,
		eventId)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
