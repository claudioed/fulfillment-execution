package postgres

import (
	"context"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool builds the pgxpool every repository in this package is
// constructed from, with the OTel pgx tracer installed so each query,
// batch, copy and connection acquisition becomes a child span of whatever
// span is active on the calling context.
//
// otelpgx puts the SQL statement on the span in its parameterised form
// (values stay as placeholders); query parameters are NOT recorded, which is
// the default and is left that way deliberately -- task ids and station ids
// are not worth leaking into a trace backend.
func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	cfg.ConnConfig.Tracer = otelpgx.NewTracer(
		// Span names become "query <first line of SQL>" rather than the
		// whole multi-line statement, keeping them readable and bounded.
		otelpgx.WithTrimSQLInSpanName(),
	)

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return pool, nil
}

// RecordPoolStats registers observable gauges for pgxpool's connection
// counts (idle, in use, max) on the global MeterProvider. It is separate
// from NewPool so a caller that does not want pool metrics simply does not
// call it.
func RecordPoolStats(pool *pgxpool.Pool) error {
	return otelpgx.RecordStats(pool)
}
