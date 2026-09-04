package memory

import (
	"context"
	"sync"

	"github.com/claudioed/fulfillment-execution/internal/domain/consolidation"
	"github.com/claudioed/fulfillment-execution/internal/domain/shared"
)

// OrderConsolidationRepo is a thread-safe in-memory implementation of
// ports.OrderConsolidationRepo.
type OrderConsolidationRepo struct {
	mu         sync.RWMutex
	byOrderRef map[shared.OrderRef]*consolidation.OrderConsolidation
}

// NewOrderConsolidationRepo constructs an empty OrderConsolidationRepo.
func NewOrderConsolidationRepo() *OrderConsolidationRepo {
	return &OrderConsolidationRepo{byOrderRef: make(map[shared.OrderRef]*consolidation.OrderConsolidation)}
}

func (r *OrderConsolidationRepo) Save(_ context.Context, oc *consolidation.OrderConsolidation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byOrderRef[shared.OrderRef(oc.OrderRef())] = oc
	return nil
}

func (r *OrderConsolidationRepo) FindByOrderRef(_ context.Context, orderRef shared.OrderRef) (*consolidation.OrderConsolidation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	oc, ok := r.byOrderRef[orderRef]
	if !ok {
		return nil, nil
	}
	return oc, nil
}
