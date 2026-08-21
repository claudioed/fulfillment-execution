package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
	"github.com/claudioed/fulfillment-execution/internal/domain/station"
)

// StationRepo is a pgxpool-backed implementation of ports.StationRepo.
type StationRepo struct {
	pool *pgxpool.Pool
}

// NewStationRepo constructs a StationRepo backed by pool.
func NewStationRepo(pool *pgxpool.Pool) *StationRepo {
	return &StationRepo{pool: pool}
}

func (r *StationRepo) Save(ctx context.Context, s *station.Station) error {
	var occupant *string
	if s.Occupant() != nil {
		v := string(*s.Occupant())
		occupant = &v
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO stations (id, capabilities, occupant)
		VALUES ($1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET capabilities = EXCLUDED.capabilities, occupant = EXCLUDED.occupant
	`, string(s.Id()), capabilitiesToSlice(s.Capabilities()), occupant)
	return err
}

func (r *StationRepo) FindById(ctx context.Context, id shared.StationId) (*station.Station, error) {
	var (
		stationId    string
		capabilities []string
		occupant     *string
	)
	err := r.pool.QueryRow(ctx, `
		SELECT id, capabilities, occupant FROM stations WHERE id = $1
	`, string(id)).Scan(&stationId, &capabilities, &occupant)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var occ *station.OccupantId
	if occupant != nil {
		v := station.OccupantId(*occupant)
		occ = &v
	}
	return station.Rehydrate(shared.StationId(stationId), sliceToCapabilities(capabilities), occ), nil
}
