package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	pack "github.com/claudioed/fulfillment-execution/internal/domain/package"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
)

// PackageRepo is a pgxpool-backed implementation of ports.PackageRepo.
type PackageRepo struct {
	pool *pgxpool.Pool
}

// NewPackageRepo constructs a PackageRepo backed by pool.
func NewPackageRepo(pool *pgxpool.Pool) *PackageRepo {
	return &PackageRepo{pool: pool}
}

func (r *PackageRepo) Save(ctx context.Context, p *pack.Package) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO packages (id, order_ref, status, scanned_contents, fragile_handling)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (id) DO UPDATE SET order_ref = EXCLUDED.order_ref, status = EXCLUDED.status, scanned_contents = EXCLUDED.scanned_contents, fragile_handling = EXCLUDED.fragile_handling
	`, string(p.Id()), string(p.OrderRef()), string(p.Status()), p.ScannedContents(), p.FragileHandling())
	return err
}

func (r *PackageRepo) FindById(ctx context.Context, id shared.PackageId) (*pack.Package, error) {
	var (
		packageId, orderRef, status string
		scannedContents             []string
		fragileHandling             bool
	)
	err := r.pool.QueryRow(ctx, `
		SELECT id, order_ref, status, scanned_contents, fragile_handling FROM packages WHERE id = $1
	`, string(id)).Scan(&packageId, &orderRef, &status, &scannedContents, &fragileHandling)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return pack.Rehydrate(shared.PackageId(packageId), shared.OrderRef(orderRef), pack.Status(status), scannedContents, fragileHandling), nil
}
