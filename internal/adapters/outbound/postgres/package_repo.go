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
		INSERT INTO packages (id, order_ref, status, scanned_contents, fragile_handling, scanned_hazard_classes, gift_wrap_requested)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (id) DO UPDATE SET order_ref = EXCLUDED.order_ref, status = EXCLUDED.status, scanned_contents = EXCLUDED.scanned_contents, fragile_handling = EXCLUDED.fragile_handling, scanned_hazard_classes = EXCLUDED.scanned_hazard_classes, gift_wrap_requested = EXCLUDED.gift_wrap_requested
	`, string(p.Id()), string(p.OrderRef()), string(p.Status()), p.ScannedContents(), p.FragileHandling(), p.ScannedHazardClasses(), p.GiftWrapRequested())
	return err
}

func (r *PackageRepo) FindById(ctx context.Context, id shared.PackageId) (*pack.Package, error) {
	var (
		packageId, orderRef, status string
		scannedContents             []string
		fragileHandling             bool
		scannedHazardClasses        []int
		giftWrapRequested           bool
	)
	err := r.pool.QueryRow(ctx, `
		SELECT id, order_ref, status, scanned_contents, fragile_handling, scanned_hazard_classes, gift_wrap_requested FROM packages WHERE id = $1
	`, string(id)).Scan(&packageId, &orderRef, &status, &scannedContents, &fragileHandling, &scannedHazardClasses, &giftWrapRequested)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return pack.Rehydrate(shared.PackageId(packageId), shared.OrderRef(orderRef), pack.Status(status), scannedContents, fragileHandling, scannedHazardClasses, giftWrapRequested), nil
}
