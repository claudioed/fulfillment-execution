---
id: context-relationships
title: Bounded-context relationships
sidebar_label: Context relationships
sidebar_position: 5
description: How this bounded context relates to the other four services and to WCS, using Evans/Vernon context-mapping vocabulary — Customer/Supplier, Open Host Service, Published Language, Conformist, Anti-Corruption Layer.
---

# Bounded-context relationships

This page is the *strategic* view — which context-mapping pattern governs each
edge, and why. The *technical* view — actual topics, envelopes and wiring
status — is on the [Context map](../ecosystem/context-map.md) and
[Integration contracts](../ecosystem/integration-contracts.md).

## Vocabulary

Using the standard Evans/Vernon patterns as the platform reference uses them:

| Pattern | Meaning |
| --- | --- |
| **Customer/Supplier (C/S)** | Upstream and downstream teams negotiate; downstream's needs are a real input to upstream's planning. |
| **Open Host Service (OHS)** | Upstream publishes a stable, general-purpose protocol for many downstreams. |
| **Published Language (PL)** | A shared, documented interchange format — here, the CloudEvents-typed event catalogue plus the OpenAPI contract. |
| **Conformist (CF)** | Downstream adopts upstream's model wholesale, with no translation layer. |
| **Anti-Corruption Layer (ACL)** | Downstream translates at the boundary so the upstream model never leaks inward. |

## Edge by edge

### `wes-work-planning` → `fulfillment-execution` — **Customer/Supplier + ACL**

`wes-work-planning` is the conductor: it turns a shift's charge into a plan and
releases work continuously (waveless). When it releases a unit, it publishes
`WorkReleased` on `warehouse.work-planning.events`. This service consumes it
and creates a `Task`.

**Why Customer/Supplier and not Conformist:** the relationship is genuinely
bidirectional in planning terms. This service's queue depth is exactly the
buffer telemetry Work Planning flow-balances on, and the completion feedback
this service publishes is a required input to Work Planning's own progress
model. Neither side can do its job without the other's signal — that is the
Customer/Supplier shape, not a one-way conformity.

**The ACL is real, not nominal.** `internal/adapters/inbound/kafka/consumer.go`
decodes the envelope and extracts exactly three scalars, mapping each into this
context's own vocabulary:

| From `WorkReleased.data` | Becomes | Via |
| --- | --- | --- |
| `path_id` | `task.Type` | prefix convention `pick-*` / `pack-*` / `slam-*`, defaulting to `PICK` |
| `work_unit_id` | `shared.OrderRef` | direct |
| `cpt` | `shared.CPT` | `shared.NewCPT` |
| *(derived from type)* | `shared.CapabilitySet` | `PICK`→`pick`, `PACK`→`pack`, `SLAM`→`slam` |

Note that `data.ref` is decoded but **not** used — the deliberate choice was
`work_unit_id` as the correlation key, because that is what Work Planning's
`RecordCompletion` expects back. No upstream struct crosses the boundary. This
is precisely the discipline the reference model demands: *"WES's `Task` is
built FROM WMS's released work, not shared with it."*

### `fulfillment-execution` → `wes-work-planning` — **Customer/Supplier (feedback edge)**

The direction reverses for completions. This service publishes `TaskCompleted`
to `warehouse.fulfillment.events`; `wes-work-planning` consumes it and calls
its own `RecordCompletion(workUnitId)`.

This closes what the repo's own integration notes call the
**drum-buffer-rope feedback edge: Execution → Orchestration**. Without it, the
conductor would be releasing work into a void with no confirmation that any of
it landed, and its plan-versus-actual would be pure guesswork.

The enrichment step matters strategically: the domain event carries only
`TaskId`/`StationId`, and the adapter backfills `work_unit_id` via a
`TaskRepo` lookup. The *correlation key the consumer needs* is an integration
concern and is added at the boundary — the domain model is not reshaped to
serve a downstream's join.

### `fulfillment-execution` → WCS / equipment — **Customer/Supplier + Conformist, behind an ACL**

Strategically, this service is upstream of WCS: it decides that a carton
should be sealed and labelled, and WCS drives the conveyor, the print-and-apply
head and the check-weigher that do it.

The platform reference classifies WCS as a **Generic Subdomain** — *"Buy,
don't build — device orchestration is rarely a competitive advantage"* — which
determines the pattern on this edge. At its lower boundary a WCS is inevitably
**Conformist** to each vendor's PLC protocol; the correct defence is an
**Anti-Corruption Layer** on this side, so that vendor protocol shapes never
climb up into the `Task` or `Package` model.

:::info Not wired today
There is **no WCS integration in this repository**: no adapter, no topic, no
command channel. `apis/openapi.yaml` says so explicitly — driving physical
equipment "is a separate command channel," out of scope for this API. The edge
is drawn on the context map because it is the real strategic shape of the
system, and it is labelled as not-yet-built rather than implied to exist.
:::

### `workforce-management` ↔ `fulfillment-execution` — **no technical edge; a deliberate boundary**

There is no Kafka topic, no HTTP call, and no shared type between these two
services. That absence is the design.

`workforce-management` owns "who is on shift, on which process path, at what
rate." It **stops at the path boundary** — its own charter says it "never links
an associate to a specific task — dispatch of individual tasks to a claiming
station belongs to Fulfillment Execution." From this side, the mirror-image
rule holds: `Station.occupant` is an opaque `OccupantId` with no roster, no
certifications, no shift window behind it.

The stated reason on both sides is the same: the two contexts change at
**completely different cadences** — shifts versus seconds. Coupling them would
mean a dispatch-policy change had to be reasoned about in terms of shift
planning, and vice versa.

The one thing they share is a **published language** for capability names —
`pick`, `pack`, `slam`. The `WorkReleased` consumer derives required
capabilities "matching the capability names Workforce Management uses." That is
a shared *vocabulary*, not a shared type, which is exactly what Published
Language means.

### `inventory-storage` ↔ `fulfillment-execution` — **indirect only**

`inventory-storage` is the WMS-tier authority on stock reality — chaotic stow,
bin-accurate location, revocable reservations. It publishes `StockReserved`
and `ReservationRevoked` to `warehouse.inventory.events`.

This service does **not** consume that topic. Stock reality reaches it
transitively: `wes-work-planning` projects those events into its own
`UsableInventoryObserved` read model and factors them into *what it releases* —
by which point, from this context's perspective, the decision is already made.
A `Task` carries an `orderRef`, never a SKU or a bin.

That is the correct shape. If this service consumed inventory events it would
have to form an opinion about stock truth, which is another context's Core.

### `facility-layout` ↔ `fulfillment-execution` — **no relationship today**

`facility-layout` is the newest service, a **Generic Subdomain** owning the
physical warehouse map: the Site-Area-Zone-Aisle-Bay-Level-Position hierarchy
and placement rules. It is intended as an **Open Host Service** other contexts
conform to for physical-location truth.

It currently has **no live integration with any of the other four services** —
it has only an in-process log publisher and no AsyncAPI spec at all. And this
service has no notion of location: a `Task` says *what* work and *by when*,
never *where*. There is no edge to describe, in either direction, and none is
drawn as if there were.

## Summary

| Edge | Pattern | Wired today? |
| --- | --- | --- |
| `wes-work-planning` → this | Customer/Supplier, ACL on this side | **Yes** — Kafka `warehouse.work-planning.events` |
| this → `wes-work-planning` | Customer/Supplier (feedback) | **Yes** — Kafka `warehouse.fulfillment.events` |
| this → WCS / equipment | Customer/Supplier + Conformist behind ACL | No — strategic only |
| this ↔ `workforce-management` | Published Language (capability names) only | No — deliberate boundary |
| `inventory-storage` → this | Indirect, via Work Planning | No direct edge |
| `facility-layout` ↔ this | None | No |
