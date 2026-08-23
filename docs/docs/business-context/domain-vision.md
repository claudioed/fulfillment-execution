---
id: domain-vision
title: Domain vision
sidebar_label: Domain vision
sidebar_position: 1
description: Why a distinct Fulfillment Execution context exists, what business problem it solves, and where its boundaries were drawn on purpose.
---

# Domain vision

## The platform-wide vision

The whole `warehouse-systems` platform exists to:

> Fulfil customer orders from many disparate SKUs at massive scale by
> receiving goods, storing them under chaotic storage, and reliably picking,
> packing and shipping them along the fastest/cheapest path — continuously
> re-optimising physical work in real time.

Fulfillment Execution owns the last four words of that sentence made
concrete: **the physical work itself**, and specifically its *lifecycle*.

## The vision for this context

> Turn released work into completed physical operations, and make it
> impossible to lose a unit of work in the process.

Everything the service does reduces to those two clauses.

**"Turn released work into completed physical operations"** — upstream,
`wes-work-planning` decides that a unit of work should happen now. That
decision arrives as a `WorkReleased` event and becomes a `Task` in this
service's pool. From there the task must find a station that can do it, be
performed, and be confirmed. When the confirmation lands, the loop closes:
`TaskCompleted` goes back to Work Planning so its own plan can advance.

**"Make it impossible to lose a unit of work"** — this is the harder half.
A warehouse floor is not a reliable network. Scanners die mid-pick, associates
go on break with a task open, a pick-to-light station reboots. A design where
work can be assigned but never confirmed produces *stranded work*: a task the
system believes is in progress and that no one is actually doing. Nobody
notices until the order misses its CPT. The lease exists precisely to make
that failure mode structurally impossible — see
[ADR-0003](../adr/0003-lease-based-at-most-once-claiming.md).

## Why this is a separate bounded context

The industry reference model splits warehouse software into three tiers by
time horizon:

| Tier | Time horizon | Answers |
| --- | --- | --- |
| **WMS** | minutes → days | *What* needs to happen, and *why* |
| **WES** | seconds → minutes | *Who* (human or machine) does it, right now, in what order |
| **WCS** | milliseconds → seconds | *How* the machine performs the next physical step |

Fulfillment Execution is squarely in the **WES tier**, and specifically the
execution slice of it. The reference material is emphatic that these tiers are
*deployment/product categories*, not clean bounded contexts — vendors disagree
about where WES stops and WCS starts, and "some vendors call their WCS a WES
or vice versa." The stable seams are the **business capabilities** underneath,
which is why this platform carves contexts by capability rather than by tier
label.

The capability this context owns is **task lifecycle management**. Splitting
it out from Work Planning was deliberate:

- **Work Planning** answers *how much* work should be on the floor and *when*
  to release it, using rate, headcount, and live buffer telemetry. It changes
  when flow-balancing policy changes.
- **Fulfillment Execution** answers *how a released unit of work gets safely
  from the pool into a completed state*. It changes when dispatch or claim
  semantics change.

Those two things change for entirely different reasons and at entirely
different cadences. Fusing them would mean a change to lease duration risked
regressing the release algorithm.

## The "same word, different model" trap, made explicit

The DDD reference calls this out directly, and it applies here more than
anywhere else in the platform:

> WMS's `Pick Task` is a business-level demand signal tied to an Order's
> fulfilment lifecycle — it persists, has an SLA, has priority. WES's `Task`
> is an execution-level unit bound to a specific worker at a specific moment,
> and is largely disposable once completed. Same English word, two different
> models. Do not share the class across contexts.

That is exactly why the `WorkReleased` consumer **translates** rather than
deserialising into a shared type. `WorkReleased.data.work_unit_id` becomes this
context's `orderRef`; `data.cpt` becomes a `shared.CPT`; `data.path_id` is
mapped, by prefix convention, to a `task.Type`. Nothing crosses the boundary
as a shared struct. That translation step is the Anti-Corruption Layer.

## What this context deliberately refuses to do

- **It does not name a worker or a station in advance.** No push assignment
  exists in the model. See [Why pull, not push](./why-pull-not-push.md).
- **It does not know about associates.** `Station.occupant` is an opaque
  `OccupantId`; there is no roster, no certification record, no shift window.
  Those live in `workforce-management`, which stops at the process-path
  boundary and never links an associate to an individual task.
- **It does not own stock truth.** A `Task` carries an `orderRef`, not a SKU
  ledger position. Reservations and bin-accurate location are
  `inventory-storage`'s job.
- **It does not decide how much work to release.** Only how a released unit
  completes.
- **It does not drive PLCs.** WCS is a Generic Subdomain in this platform —
  buy, don't build — and would sit behind an Anti-Corruption Layer if and when
  that edge is wired.
