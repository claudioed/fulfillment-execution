package ports

import "context"

// EquipmentCommandPort is the anti-corruption-layer seam toward a future
// WCS / equipment-control tier (conveyors, print-and-apply heads,
// checkweighers). No adapter implements this port today, and none is
// planned until a WCS integration is actually commissioned — this
// service's own context map documents that tier as "strategic, not
// built" and its OpenAPI contract states plainly that this service "does
// not drive physical WCS/equipment directly over this API."
//
// The port exists so that boundary is enforced by the compiler rather
// than only by prose: if a WCS integration is ever added, its vocabulary
// (chutes, diverts, encoder ticks, drive-unit ids) is translated into
// this interface at a single outbound adapter, and none of that
// vocabulary is permitted to leak into Task, Package, or Station — the
// aggregates this service's actual invariants depend on. Equipment
// lifecycles change on a different cycle than domain lifecycles (a
// conveyor or sorter cell can be swapped without any of this service's
// business rules changing), and this seam is what keeps that swap from
// ever touching domain code.
//
// Deliberately unimplemented: EquipmentCommandPort declares no methods
// yet. Adding methods here is a decision for the day a WCS integration
// is actually scoped, not a speculative API designed in advance of any
// real equipment to drive. See ADR-0012.
type EquipmentCommandPort interface {
	// no methods — placeholder seam, see package doc comment and ADR-0012.
	_reservedForFutureWCSIntegration(ctx context.Context)
}
