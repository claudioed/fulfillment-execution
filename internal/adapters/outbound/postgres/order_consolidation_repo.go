package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/fulfillment-execution/internal/domain/consolidation"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
)

// OrderConsolidationRepo is a pgxpool-backed implementation of
// ports.OrderConsolidationRepo.
type OrderConsolidationRepo struct {
	pool *pgxpool.Pool
}

// NewOrderConsolidationRepo constructs an OrderConsolidationRepo backed by pool.
func NewOrderConsolidationRepo(pool *pgxpool.Pool) *OrderConsolidationRepo {
	return &OrderConsolidationRepo{pool: pool}
}

func (r *OrderConsolidationRepo) Save(ctx context.Context, oc *consolidation.OrderConsolidation) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO order_consolidations (order_ref, required_lines, arrived_lines)
		VALUES ($1, $2, $3)
		ON CONFLICT (order_ref) DO UPDATE SET
			required_lines = EXCLUDED.required_lines,
			arrived_lines = EXCLUDED.arrived_lines
	`, oc.OrderRef(), oc.RequiredLineIds(), oc.ArrivedLineIds())
	return err
}

func (r *OrderConsolidationRepo) FindByOrderRef(ctx context.Context, orderRef shared.OrderRef) (*consolidation.OrderConsolidation, error) {
	var (
		storedOrderRef string
		requiredLines  []string
		arrivedLines   []string
	)
	row := r.pool.QueryRow(ctx, `
		SELECT order_ref, required_lines, arrived_lines
		FROM order_consolidations WHERE order_ref = $1
	`, string(orderRef))
	if err := row.Scan(&storedOrderRef, &requiredLines, &arrivedLines); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return consolidation.Rehydrate(storedOrderRef, requiredLines, arrivedLines), nil
}
