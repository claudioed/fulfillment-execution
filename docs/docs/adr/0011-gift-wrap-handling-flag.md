---
id: 0011-gift-wrap-handling-flag
title: 11. Gift wrap handling flag carried on Task and Package
sidebar_label: 11. Gift wrap handling flag
sidebar_position: 11
description: A Task.GiftWrap flag stamped by wes-work-planning at release time from a caller-stated WorkReleased.data.gift_wrap request, and a derived Package.GiftWrapRequested flag set at seal time — deliberately not a station-eligibility/capability concern, unlike hazmat.
---

# 11. Gift wrap handling flag carried on `Task` and `Package`

## Status

**Accepted.**

## Context

A request came in to "implement gift wrap." The initial framing treated
gift wrap as a per-SKU product attribute, the same shape of fact as
`fragile` or hazmat classification — something `inventory-storage` would
know about a SKU and hand downstream. On inspection that framing did not
hold: gift wrap is not a property of a product at all. It is a
characteristic of a specific order's fulfilment, stated by whoever
requests the work — "wrap this one as a gift" — at the moment work is
enqueued in `wes-work-planning`, not a fact `inventory-storage`'s
`ProductClassification` concept has any way to carry, because it is not
about the SKU.

That reframing puts gift wrap in a different position from fragile and
hazmat despite looking superficially similar to both:

- **Fragile and hazmat both originate in `inventory-storage`'s
  `ProductClassification`** (see
  [ADR-0009](./0009-fragile-and-hazmat-handling-flags.md)) — a fact about
  the SKU, read once upstream and stamped onto `WorkReleased` by
  `wes-work-planning`. Gift wrap has no such upstream source; it is a
  caller-stated fact about the released work itself, owned entirely by
  `wes-work-planning` (see that repository's ADR-0010 for the producer-side
  half of this decision).
- **Hazmat is a station-eligibility/capability concern** — a hazmat task
  must be claimed only by a station certified to handle it, enforced via
  the existing `shared.Capability`/`shared.CapabilitySet` mechanism. Gift
  wrap has no such constraint: any station can fold a box lid over
  wrapping paper. It is a packing concern only, the same category as
  fragile handling, not the same category as hazmat.
- **Fragile is a *packing* concern, not a placement or
  capability-matching concern** — it affects how a station packs an
  order, not which station or task can be claimed. Gift wrap is
  structurally identical in this respect: same shape of hint, same
  point of consumption (`SealPackage`), same non-effect on `Claim`.

The forces, once the framing above was settled:

- **This service does not call `wes-work-planning` synchronously for
  domain facts**, per the same anti-corruption-layer rule ADR-0009 already
  established at every context boundary (see
  [ADR-0004](./0004-kafka-integration-events-and-envelope.md)). Whatever
  this service needs about gift wrap has to arrive already resolved,
  riding on something already consumed — `WorkReleased`.
- **`CreateTask` is the existing, single ingestion point for
  `WorkReleased`-carried facts.** CPT, `requiredCapabilities`, and
  (per ADR-0009) `fragile` already ride in this way and get stamped onto
  `Task` at creation time. Gift wrap is the same shape of fact and should
  not get a second ingestion path.
- **`SealPackage` already loads the owning `Task`** before it builds the
  `Package`, exactly as ADR-0009 observed for `fragileHandling`. Any
  gift-wrap-derived state on the `Package` is available at that exact
  moment for free.
- **A caller-supplied `giftWrap` argument to `SealPackage.Execute` (or a
  `giftWrap` field on `sealPackageRequest`) would let it drift from the
  Task's own answer.** The station driving `seal-package` should not be
  trusted, or asked, to restate a fact the system already knows from the
  Task it is fulfilling — the same reasoning ADR-0009 already applied to
  fragile.
- **`WorkReleased.data.gift_wrap` is optional and must be omitted, not
  published as explicit `false`, when gift wrap was not requested** — a
  stricter convention than `fragile`'s "absent defaults to false" gap
  tolerance, but it collapses to the same wire behavior on this side of
  the boundary: whether the field is omitted or explicitly `false`, this
  service's mapping reads it as `false` either way.

## Decision

**`Task` gains a `giftWrap bool` field with a `GiftWrap()` accessor,
threaded through `New`/`Rehydrate` and the `CreateTask` use case's
`Execute` parameters, exactly like `fragile` is today. `Package` gains a
`giftWrapRequested bool` field with a `GiftWrapRequested()` accessor, set
at construction (`pack.New`/`Rehydrate`) and derived — not independently
supplied — by `SealPackage` from `task.GiftWrap()` on the Task it already
loaded. Gift wrap gets no station-eligibility/capability code path at
all — deliberately unlike hazmat, any station may fulfill a gift-wrap
request. `fragile` and `giftWrap` are kept as separate, independently
derived fields on both `Task` and `Package`; neither is merged into the
other.**

### Task.GiftWrap

```go
func New(id shared.TaskId, taskType Type, cpt shared.CPT, orderRef shared.OrderRef,
    required shared.CapabilitySet, fragile bool, giftWrap bool) *Task
```

`giftWrap` is opaque to this service: it does not gate `Claim`, does not
appear in `requiredCapabilities`, and has no effect on dispatch priority or
capability matching. It is read by exactly one caller, `SealPackage`.

The Kafka consumer (`internal/adapters/inbound/kafka`) reads an optional
`data.gift_wrap bool` off the `WorkReleased` envelope, defaulting to
`false` when absent, and passes it straight to `CreateTask.Execute` — the
same mapping discipline `fragile` already gets. `data.gift_wrap` (owned by
`wes-work-planning`'s own ADR-0010 on that repository) is omitted entirely
by the producer when gift wrap was not requested, never published as
explicit `false`; this side's mapping treats "absent" and "false" as the
same input, so the distinction is invisible here by design.

### Package.GiftWrapRequested — derived, not supplied

```go
func New(id shared.PackageId, orderRef shared.OrderRef, fragileHandling bool, giftWrapRequested bool) *Package
```

`SealPackage.Execute` is the only caller. It already does, per ADR-0009:

```go
t, err := uc.Tasks.FindById(ctx, taskId)
...
p := pack.New(uc.NewId(), t.OrderRef(), t.Fragile(), t.GiftWrap())
```

There is deliberately no `giftWrap` field on `sealPackageRequest` — the
HTTP DTO the station sends — for the same reason ADR-0009 gives for
`fragile`: the station reports what it scanned, not facts the system
already knows from the Task.

### No capability gating — the deliberate contrast with hazmat

Unlike hazmat (see ADR-0009), gift wrap introduces no new
`Capability`/`CapabilitySet` value and no change to `Task.Claim`,
`CapabilitySet.HasAll`, or `RegisterStation`. Any station may claim and
fulfill a gift-wrap-flagged Pack task. This is a deliberate design choice,
not an oversight: gift wrap is a packing instruction ("use gift wrap
paper, not a standard carton"), not a certification or equipment
requirement. Conflating it with hazmat's station-eligibility mechanism
would incorrectly restrict which stations can pack a gift order for no
operational reason.

### Domain events stay thin

Per the existing discipline (see
[Domain events](../ddd/domain-events.md) and
[ADR-0004](./0004-kafka-integration-events-and-envelope.md)), neither
`TaskCreated` nor `PackageSealed` gained a `giftWrap`/`giftWrapRequested`
field, matching ADR-0009's treatment of `fragile`/`fragileHandling`. Both
flags are visible on their aggregate's HTTP response (`giftWrap` on the
task response — read-only, since HTTP has no ingestion path for it —
`giftWrapRequested` on the package response) for anything that needs to
read current state.

## Consequences

### Easier

- **No new aggregate, no new consistency boundary, no new capability
  mechanism.** Both flags live on aggregates that already exist, set once
  at construction and never mutated. Gift wrap needed zero changes to
  `Task.Claim`, `CapabilitySet`, or `RegisterStation` — the cheapest
  possible outcome, mirroring how cheap hazmat was in ADR-0009 for the
  opposite reason (hazmat reused an existing mechanism; gift wrap needs no
  mechanism at all).
- **`GiftWrapRequested` cannot drift from its Task.** Because
  `SealPackage` derives it rather than accepting it as an independent
  argument, there is no code path where a Package's gift-wrap flag
  disagrees with the Task that produced it.
- **Fragile and gift wrap stay independently testable and independently
  true/false**, because they are separate fields rather than a merged
  "special handling" flag — a future third packing hint can be added the
  same way without touching either.
- **Backward-compatible by construction.** `data.gift_wrap`'s absence on
  any `WorkReleased` producer (including every one that predates this
  field) defaults cleanly to `false`, matching the existing `fragile` and
  `path_id`-prefix precedent.

### Harder

- **Two more fields to keep in sync across every layer that touches Task
  or Package.** `New`, `Rehydrate`, the Postgres schema (new migration
  `0005_gift_wrap`), the HTTP DTOs, and the Kafka mapping all needed a
  coordinated, mechanical change — the same ripple ADR-0009 already
  documented for fragile, now doubled by having two independent flags
  instead of one.
- **`giftWrap` on `CreateTask.Execute` is one more positional-ish
  parameter**, continuing the growth ADR-0009 already flagged as a
  candidate for a future request-struct refactor if this pattern repeats
  again.
- **`giftWrap` is not settable through the HTTP `POST /tasks` endpoint at
  all** (unlike `fragile`, which is HTTP-settable and only *validated*
  nowhere). This is a narrower, more deliberate gap than fragile's: since
  gift wrap has exactly one legitimate source (`wes-work-planning` via
  Kafka), the HTTP path simply does not expose a way to set it, so a
  direct API caller cannot get gift wrap onto a task at all outside the
  Kafka-consumer path. If a future need arises for an HTTP-originated gift
  wrap request, that is a new, separate decision — not something this
  round's HTTP contract should silently allow via an unvalidated field.
- **No sortation ACL or downstream consumer exists for
  `GiftWrapRequested()`.** Like `FragileHandling()` before any consumer
  existed for it (ADR-0009), it is currently read by nothing except the
  HTTP/GET-style responses. `Package.SortLane()` deliberately does **not**
  read `GiftWrapRequested()` — gift wrap has no sortation-routing
  implication, only a packing-station one — so this flag has an even
  narrower footprint than fragile's did at the same stage.
