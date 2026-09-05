---
id: ubiquitous-language
title: Ubiquitous language
sidebar_label: Ubiquitous language
sidebar_position: 4
description: The exact vocabulary of the Fulfillment Execution bounded context, with definitions, where each term appears in code, and the cross-context terms that look shared but are not.
---

# Ubiquitous language

These are the exact names this bounded context uses. They appear unchanged in
the domain code, the REST API, the event catalogue and this documentation — if
a term here does not match an identifier in `internal/domain/`, one of the two
is wrong.

## Core terms

| Term | Definition | In code |
| --- | --- | --- |
| **Task** | A unit of physical work. Has a type (`Pick`, `Pack`, `Rebin`, `SLAM`), a CPT, an order reference, a set of required capabilities, and a **Fragile** flag. States: `Pending → Claimed(leased) → Completed`, or lease-expires back to `Pending`. At most one active claim, ever. | `internal/domain/task.Task` |
| **claimNext(stationId, capabilities)** | **PULL dispatch.** Returns the highest-priority (earliest CPT) pending task the station is certified and equipped for. The system never names a station in advance — there is deliberately no `assign(task, station)`. | `usecases.ClaimNext`, `POST /stations/{stationId}/claim-next` |
| **Lease** | A time-boxed claim. If the claim is not confirmed (renewed) or completed before expiry, the task returns to the pool — this is what prevents work from vanishing. Renewal is allowed and expected for legitimately long work. | `task.Lease`, default 5 minutes |
| **Station** | A work position with a capability set. One occupant at a time. Capabilities must match what a task requires before the station can claim it. | `internal/domain/station.Station` |
| **Fragile** | A `Task`-level packing hint (bool), stamped by `wes-work-planning` at release time and sourced from `inventory-storage`'s `ProductClassification` concept — true if the upstream order line was classified Fragile. Threaded through `CreateTask` and carried on the aggregate; it does not gate claiming or capability matching. `SealPackage` reads it off the owning task to derive `Package.FragileHandling`. | `task.Task.Fragile()`, `usecases.CreateTask` |
| **Gift wrap** | A `Task`-level packing hint (bool), stamped by `wes-work-planning` at release time from a *caller-stated* `WorkReleased.data.gift_wrap` request — not a product classification, and with no `inventory-storage` involvement at all (see [ADR-0011](../adr/0011-gift-wrap-handling-flag.md)). Same category as **Fragile**: a packing-care hint threaded through `CreateTask`, read only by `SealPackage` to derive `Package.GiftWrapRequested`, never gating claiming or capability matching. Explicitly **not** like **Hazmat**: any station may fulfill a gift-wrap request, so no `Capability`/`CapabilitySet` value or `Task.Claim` change exists or is needed for it. Has no HTTP ingestion path — `giftWrap` on the task response is read-only. | `task.Task.GiftWrap()`, `usecases.CreateTask` |
| **Package** | Pack output: an order becoming a sealed carton. Cannot be sealed without scanned contents. Also carries **FragileHandling** (bool) and **GiftWrapRequested** (bool), each derived independently at construction time from the owning task's `Fragile`/`GiftWrap` flags rather than accepted as separate inputs — true if any of the package's scanned/sealed contents came from a Task classified Fragile / flagged for gift wrap upstream. | `internal/domain/package` (imported as `pack`) |
| **DOT hazard class** | An integer 1-9, looked up live per scanned SKU via `ProductClassificationLookup` at seal time (not stamped on `Task` at release time — see [ADR-0010](../adr/0010-package-segregation-and-sort-lane.md)). Recorded on `Package.ScannedHazardClasses()` when the SKU is Hazmat-classified. Zero/absent means no hazard class — fail-open. | `ports.ClassificationInfo.DOTHazardClass`, `pack.Package.ScanItemWithClass` |
| **Segregation** | The same-package invariant that two scanned items' DOT hazard classes must be mutually compatible per a class-level 49 CFR §177.848-derived matrix — violated pairs reject with `pack.ErrPackageSegregationViolation` before the second item is ever recorded. | `pack.IsSegregationIncompatible`, `pack.Package.ScanItemWithClass` |
| **SortLane** | `Package`'s derived WES-tier sortation routing decision: `HAZMAT_LANE` (any scanned item carried a DOT hazard class) beats `FRAGILE_NO_TILT` (`FragileHandling()` true) beats `STANDARD`. A decision only — no WCS device/conveyor integration exists in this repository. | `pack.Package.SortLane()` |
| **SLAM weigh-check** | Scan, Label, Apply, Manifest. The actual package weight must be within tolerance of the expected weight, otherwise the package is **diverted** instead of labelled. | `pack.Package.Weigh`, `pack.WeightTolerance = 0.05` |
| **Process path** | Pick / Pack / Rebin / SLAM as **named task types (queues)**, not as steps in a workflow. See [Process paths](./process-paths.md). | `task.Type` |
| **CPT** | **Critical Pull Time** — the deadline by which a task must be completed for its order to ship on schedule. Priority derives entirely from CPT; earliest CPT is dispatched first. There is no separate priority field. | `shared.CPT` |
| **Capability** | An open string type (not a closed enum — same convention as, e.g., `LocationType` elsewhere in this codebase), naming a certification or piece of equipment a station must have to accept a task. Known values in active use: `pick`, `pack`, `rebin`, `slam`, and **`hazmat`**. Compared as set containment: a station may claim a task only if its capability set contains every required capability. Hazmat needs no dedicated code path — the existing generic `CapabilitySet.HasAll` check inside `Task.Claim`, and `RegisterStation`'s already-open acceptance of arbitrary capability strings, enforce it exactly like any other capability. | `shared.Capability`, `shared.CapabilitySet.HasAll` |
| **Queue depth** | How many `Pending` tasks of a given type sit in the pool. A **projection**, computed on demand from task state, never a stored counter. | `usecases.GetQueueDepth`, `GET /queues/{taskType}/depth` |
| **OrderRef** | The reference back to the released work unit this task or package fulfils. Populated from `WorkReleased.data.work_unit_id`, and carried back out on `TaskCompleted` as `work_unit_id` so Work Planning can correlate. | `shared.OrderRef` |
| **OrderConsolidation** | Tracks which of an order's required lines have arrived at the Rebin path, exposing `IsComplete()` as the trigger for creating that order's `PACK` task. A small aggregate scoped entirely inside this bounded context — see [ADR-0016](../adr/0016-rebin-and-order-consolidation.md). Idempotent on redelivery; rejects an arrival for a line outside the order's established required set (`ErrUnknownLine`). | `internal/domain/consolidation.OrderConsolidation`, `usecases.ArriveAtRebin`, `POST /rebin/arrivals` |

:::note On the CPT acronym
The domain code defines CPT as **Critical Pull Time** (`internal/domain/shared/cpt.go`),
which is the warehouse-industry reading and the one used throughout this
documentation. The `apis/openapi.yaml` description expands it once as
"Committed Processing Time". Both refer to the same value — the ship-by
deadline that drives priority — and the discrepancy is in prose only, not in
any schema or field name.
:::

## Lifecycle vocabulary

| Term | Meaning |
| --- | --- |
| **Pending** | In the pool, claimable by any capability-matching station. Counted by queue depth. |
| **Claimed** | Leased to exactly one station. Only the owner may renew or complete. |
| **Completed** | Terminal. A second completion is rejected — no double-complete. |
| **At-most-once** | The claim guarantee: two stations can never hold an active claim on the same task simultaneously. |
| **Lease expiry** | The `Claimed → Pending` edge. Evaluated lazily at every claim/renew/complete, and eagerly by the `ExpireLeases` sweep. |
| **Diverted** | A package that failed the SLAM weigh-check and was routed off the standard path instead of being labelled. |

## Terms borrowed from the wider reference model

These come from the fulfilment reference model and appear in this context's
prose, though not all are modelled here as types:

| Term | Meaning | Modelled here? |
| --- | --- | --- |
| **Tote** | The container (~2 ft) that carries items from pick to pack, with a physical *and* virtual map of contents. | No — implicit in the Pick path narrative. |
| **Pod** | A mobile storage tower of coded bins; robots carry pods to stations. | No — belongs to WCS / `inventory-storage`. |
| **Bin** | A coded slot within a pod or shelf; the unit of location. | No — `facility-layout` and `inventory-storage` own location. |
| **Waveless release** | Continuous release of work rather than batching into waves. | No — that is `wes-work-planning`'s decision; this context only receives what was released. |

## Terms that look shared across contexts but are not

This is the most important table on the page. The reference model warns
explicitly that "same word, different model is allowed — and expected," and
that forcing a shared type across the boundary is the classic DDD trap.

| Word | Here it means | Elsewhere it means |
| --- | --- | --- |
| **Task** | An execution-level unit bound to a claiming station at a specific moment; disposable once completed. | In a WMS, a *business-level demand signal* tied to an order's fulfilment lifecycle — it persists, has an SLA, has priority. Different lifecycle, different model, deliberately not a shared class. |
| **ShiftPlan** | Not modelled. | `workforce-management` owns a committed headcount split across paths; `wes-work-planning` has its *own* different `ShiftPlan`. Neither is this context's concern. |
| **WorkUnit** | Arrives as `orderRef` — an opaque correlation key, nothing more. | In `wes-work-planning` it is a first-class aggregate with release state. This context deliberately learns nothing about it beyond the id. |
| **Station** | A work position with capabilities, claimable-from. | In `workforce-management` the equivalent concern stops at the **path** level — it plans heads per path and never links an associate to a station or a task. |
| **Location** | Not modelled at all. | `facility-layout` owns the Site-Area-Zone-Aisle-Bay-Level-Position hierarchy; `inventory-storage` owns what is *in* a bin. |

Every one of these is translated at the boundary rather than shared. The
`WorkReleased` consumer is the concrete example: it decodes an envelope,
extracts three scalars, and constructs *this* context's types. No upstream
struct crosses the line.
