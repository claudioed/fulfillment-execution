---
id: 0009-fragile-and-hazmat-handling-flags
title: 9. Fragile and hazmat handling flags carried on Task and Package
sidebar_label: 9. Fragile & hazmat handling flags
sidebar_position: 9
description: A Task.Fragile flag stamped by wes-work-planning at release time, and a derived Package.FragileHandling flag set at seal time — plus hazmat, documented as a known Capability value that already worked with no code change.
---

# 9. Fragile and hazmat handling flags carried on `Task` and `Package`

## Status

**Accepted.**

## Context

`inventory-storage` introduced a `ProductClassification` concept upstream of
this service — it owns whether a SKU is, among other things, hazmat- or
fragile-relevant. This service does not call `inventory-storage` directly
(per the existing anti-corruption-layer rule at every context boundary — see
[ADR-0004](./0004-kafka-integration-events-and-envelope.md)), so any of that
classification this service needs has to arrive already resolved, carried on
something this service already consumes.

The two classifications have different consequences here, discovered during
a boundary check before implementation started:

- **Hazmat** is a *station-eligibility* concern: a hazmat task must be
  claimed only by a station certified to handle it. This service already has
  a mechanism for exactly that shape of rule — `shared.Capability` /
  `shared.CapabilitySet`, an open string type (not a closed enum, matching
  the `LocationType`-style convention already used elsewhere in this
  codebase) compared by set containment in `Task.Claim` via
  `CapabilitySet.HasAll`, and `RegisterStation` already accepts arbitrary
  capability strings for a station's certification set. A task requiring
  hazmat handling already works today by putting `"hazmat"` in
  `requiredCapabilities` — no structural code exists to change.
- **Fragile** is a *packing* concern, not a placement or capability-matching
  concern: it affects how a station packs an order into a carton, not which
  station or task can be claimed. Nothing in the existing model carried this
  — `Task` had no packing-hint field, and `Package` had no notion of care
  level at all.

The forces:

- **Contexts must not share aggregates or call each other synchronously for
  domain facts.** `inventory-storage`'s classification must arrive already
  resolved into this context's vocabulary, riding on something this service
  already consumes — not via a new synchronous call.
- **`wes-work-planning` already does exactly this kind of translation.** CPT
  and `requiredCapabilities` already ride in on `WorkReleased` and get
  stamped onto the `Task` at creation time via the existing `CreateTask` use
  case (see the repository's `INTEGRATION.md` and
  [ADR-0004](./0004-kafka-integration-events-and-envelope.md)). A fragile
  hint is the same shape of fact — this service should not invent a second
  ingestion path for it.
- **`SealPackage` already loads the owning `Task`** to validate claim
  ownership before it builds the `Package` (see
  [Aggregates & invariants](../ddd/aggregates-and-invariants.md)). Any
  fragile-derived state on the `Package` is available at that exact moment
  for free, without a second lookup.
- **Adding a caller-supplied `fragile` argument to `SealPackage.Execute`
  would let it drift from the Task's own answer.** The station driving
  `seal-package` should not be trusted (or asked) to restate a fact the
  system already knows from the Task it is fulfilling.
- **Kafka delivery already has one documented simplification** (the
  `path_id`-prefix convention) for a field the producer doesn't
  unambiguously carry yet. Backward compatibility with any
  already-documented `wes-work-planning` producer that predates a new
  `data.fragile` field is a real, immediate constraint, not hypothetical.

## Decision

**`Task` gains a `fragile bool` field with a `Fragile()` accessor, threaded
through `New`/`Rehydrate` and the `CreateTask` use case's `Execute`
parameters, exactly like CPT and `requiredCapabilities` already are.
`Package` gains a `fragileHandling bool` field with a `FragileHandling()`
accessor, set at construction (`pack.New`/`Rehydrate`) and derived — not
independently supplied — by `SealPackage` from `task.Fragile()` on the Task
it already loaded. Hazmat gets no code change: it is documented as a known,
named `Capability` value in the ubiquitous language. No new aggregate is
introduced for either.**

### Task.Fragile

```go
func New(id shared.TaskId, taskType Type, cpt shared.CPT, orderRef shared.OrderRef,
    required shared.CapabilitySet, fragile bool) *Task
```

`fragile` is opaque to this service: it does not gate `Claim`, does not
appear in `requiredCapabilities`, and has no effect on dispatch priority or
capability matching. It is read by exactly one caller, `SealPackage`.

The Kafka consumer (`internal/adapters/inbound/kafka`) reads an optional
`data.fragile bool` off the `WorkReleased` envelope, defaulting to `false`
when absent, and passes it straight to `CreateTask.Execute` — the same
mapping discipline `path_id` and `cpt` already get, and the same
"default-and-document" backward-compatibility approach the `path_id`-prefix
rule already established.

### Package.FragileHandling — derived, not supplied

```go
func New(id shared.PackageId, orderRef shared.OrderRef, fragileHandling bool) *Package
```

`SealPackage.Execute` is the only caller. It already does:

```go
t, err := uc.Tasks.FindById(ctx, taskId)
...
p := pack.New(uc.NewId(), t.OrderRef(), t.Fragile())
```

There is deliberately no `fragile` field on `sealPackageRequest` — the HTTP
DTO the station sends. The station reports *what it scanned*; whether that
warrants fragile handling is a fact this service already knows from the
Task, not something to re-litigate per request.

### Hazmat — documentation only

`shared.Capability` stays an **open string type**, not a closed enum — this
is a deliberate continuation of the existing convention, not a new choice
made for hazmat. `"hazmat"` is added to the ubiquitous language as a known,
named value alongside `pick`/`pack`/`slam`, in `CLAUDE.md` and
`docs/docs/business-context/ubiquitous-language.md`. No code in
`Task.Claim`, `CapabilitySet.HasAll`, or `RegisterStation` changes, because
none needs to: a task with `requiredCapabilities: ["hazmat"]` today is
already only claimable by a station whose registered capability set
contains `"hazmat"`.

### Domain events stay thin

Per the existing discipline (see
[Domain events](../ddd/domain-events.md) and
[ADR-0004](./0004-kafka-integration-events-and-envelope.md)), neither
`TaskCreated` nor `PackageSealed` gained a `fragile`/`fragileHandling`
field. Both flags are already visible on their aggregate's HTTP response
(`fragile` on the task response, `fragileHandling` on the package response)
for anything that needs to read the current state; nothing consuming these
events in-process needs the value pushed onto the event payload today. If a
future external consumer needs it on the wire, the existing
repo-lookup-enrichment pattern (used for `TaskCompleted`'s `work_unit_id`) is
the template — enrich in the adapter, not the domain event.

## Consequences

### Easier

- **No new aggregate, no new consistency boundary.** Both flags live on
  aggregates that already exist, set once at construction and never
  mutated — no new invariant to enforce, no new failing-path test class
  beyond simple round-tripping.
- **Hazmat cost nothing to add.** The entire mechanism — capability-set
  matching in `Task.Claim`, open acceptance in `RegisterStation` — already
  existed and needed zero lines of production code changed. This is the
  cheapest possible outcome for a cross-context requirement: the existing
  design already generalized to it.
- **`FragileHandling` cannot drift from its Task.** Because `SealPackage`
  derives it rather than accepting it as an independent argument, there is
  no code path where a Package's fragile-handling flag disagrees with the
  Task that produced it.
- **Backward-compatible by construction.** `data.fragile`'s absence on an
  older `WorkReleased` producer defaults cleanly to `false`, matching the
  existing `path_id`-prefix simplification's shape exactly — one more
  documented, bounded gap instead of a new kind of failure.
- **This positions a future WCS sortation ACL cheaply.** A fragile package
  can be routed to a non-tilt lane by a future outbound adapter that reads
  `Package.FragileHandling()` — that adapter does not exist yet and is
  explicitly out of scope this round, but the flag it would need is already
  in place and already tested.

### Harder

- **Two more fields to keep in sync across every layer that touches Task or
  Package.** `New`, `Rehydrate`, the Postgres schema (new migration
  `0003_fragile_handling`), the HTTP DTOs, and the Kafka mapping all needed
  a coordinated, mechanical change — the kind of ripple that is easy to
  half-do. Every call site was audited in this round; a future field addition
  should expect the same footprint.
- **`fragile` on `CreateTask.Execute` is one more positional-ish parameter.**
  The use case's signature grows again (type, cpt, orderRef, capabilities,
  fragile) — still explicit and named at call sites, but each addition make
  the signature longer. If this pattern repeats, a request-struct parameter
  object would be a reasonable follow-up refactor.
- **A caller with no better information must guess `fragile: false`.** The
  HTTP `POST /tasks` endpoint accepts `fragile` from any caller, not only
  `wes-work-planning` via Kafka — a human or script hitting the REST API
  directly can set it incorrectly (or leave it `false` when it should be
  `true`) with no validation against `inventory-storage`. This mirrors how
  `requiredCapabilities` already works today (accepted as freeform, not
  validated against a source of truth) and is an accepted, existing shape of
  risk rather than a new one.
- **No sortation ACL exists yet.** `Package.FragileHandling()` is currently
  read by nothing except the HTTP/GET-style responses — there is no
  consumer (WCS or otherwise) acting on it yet. It is data with a documented
  intended use, not a wired end-to-end capability, which is the same honest
  gap this repo already accepts for `ItemPicked` (see
  [Domain events](../ddd/domain-events.md)).
