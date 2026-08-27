package productclassification

import (
	"context"

	"github.com/claudioed/fulfillment-execution/internal/application/ports"
)

// PermissiveLookup is the default ProductClassificationLookup: it never
// contacts inventory-storage and always reports Known=false, which
// SealPackage treats as "no classification info available, no segregation
// constraint from this item" (fail-open). Selected via
// PRODUCT_CLASSIFICATION_MODE (default "permissive"), mirroring
// inventory-storage's own LOCATION_LOOKUP_MODE=http|permissive pattern for
// its facilitylayout adapter — so existing tests, CI and deployments that
// do not set the env var see identical behaviour to before this feature
// existed.
type PermissiveLookup struct{}

// NewPermissiveLookup constructs a PermissiveLookup.
func NewPermissiveLookup() *PermissiveLookup {
	return &PermissiveLookup{}
}

func (PermissiveLookup) GetClassification(_ context.Context, _ string) (ports.ClassificationInfo, error) {
	return ports.ClassificationInfo{Known: false}, nil
}
