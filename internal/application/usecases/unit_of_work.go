package usecases

import (
	"context"

	"github.com/claudioed/fulfillment-execution/internal/application/ports"
)

// atomically runs fn inside uow when one is wired, or directly otherwise.
// Keeping this in one place means every use case treats a nil UnitOfWork
// identically (the in-memory / log-publisher configuration) instead of
// each re-deciding the fallback. See ports.UnitOfWork and ADR 0020.
func atomically(ctx context.Context, uow ports.UnitOfWork, fn func(ctx context.Context) error) error {
	if uow == nil {
		return fn(ctx)
	}
	return uow.Execute(ctx, fn)
}
