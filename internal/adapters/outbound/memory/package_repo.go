package memory

import (
	"context"
	"sync"

	pack "github.com/claudioed/fulfillment-execution/internal/domain/package"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
)

// PackageRepo is a thread-safe in-memory implementation of ports.PackageRepo.
type PackageRepo struct {
	mu       sync.RWMutex
	packages map[shared.PackageId]*pack.Package
}

// NewPackageRepo constructs an empty PackageRepo.
func NewPackageRepo() *PackageRepo {
	return &PackageRepo{packages: make(map[shared.PackageId]*pack.Package)}
}

func (r *PackageRepo) Save(_ context.Context, p *pack.Package) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.packages[p.Id()] = p
	return nil
}

func (r *PackageRepo) FindById(_ context.Context, id shared.PackageId) (*pack.Package, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.packages[id]
	if !ok {
		return nil, nil
	}
	return p, nil
}
