---
id: aggregates-and-invariants
title: Aggregates & invariants
sidebar_label: Aggregates & invariants
sidebar_position: 2
description: The three aggregate roots of this bounded context, every invariant each one enforces, the typed error it returns, and the HTTP status it maps to.
---

# Aggregates & invariants

Three aggregate roots, each enforcing its own invariants in pure Go with no
framework or SQL types anywhere near them. Every invariant listed here has a
**failing-path** unit test — asserting the rule *rejects* the bad case, not
merely that the happy path works.

```mermaid
classDiagram
    class Task {
        <<Aggregate Root>>
        -TaskId id
        -Type taskType : PICK|PACK|SLAM
        -Status status : PENDING|CLAIMED|COMPLETED
        -CPT cpt
        -OrderRef orderRef
        -CapabilitySet requiredCapabilities
        -Lease* lease
        +Claim(stationId, capabilities, now, duration) error
        +RenewLease(stationId, now, duration) error
        +Complete(stationId, now) error
        +ExpireLeaseIfDue(now) bool
        +IsAvailable(now) bool
    }
    class Lease {
        <<Value Object>>
        +StationId StationId
        +Expiry time.Time
    }
    class Station {
        <<Aggregate Root>>
        -StationId id
        -CapabilitySet capabilities
        -OccupantId* occupant
        +CheckIn(occupant) error
        +CheckOut() error
        +CanAccept(required) bool
        +ValidateAccept(required) error
    }
    class Package {
        <<Aggregate Root>>
        -PackageId id
        -OrderRef orderRef
        -Status status : OPEN|SEALED|LABELED|DIVERTED
        -string[] scannedContents
        +ScanItem(sku) error
        +Seal() error
        +Weigh(expected, actual) (bool, error)
    }
    Task *-- Lease : at most one
    Task ..> Station : capability match only
    Package ..> Task : created by a PACK task's seal
```

`Task` and `Package` are linked only by `OrderRef` — a value, not a reference.
Neither aggregate holds a pointer to the other, and neither is loaded to
mutate the other. That is what keeps them separate consistency boundaries.

## Task

The unit of physical work. Its invariants are the reason this context exists.

| # | Invariant | Enforced by | Typed error | HTTP |
| --- | --- | --- | --- | --- |
| T1 | **At most one active claim.** A task with an unexpired lease cannot be claimed again. | `Task.Claim` | `task.ErrAlreadyClaimed` | `409` |
| T2 | **A claim requires matching capabilities.** The claiming station's capability set must contain every required capability. | `Task.Claim` → `CapabilitySet.HasAll` | `task.ErrCapabilityMismatch` | `422` |
| T3 | **An expired lease frees the task.** A lapsed claim returns the task to `Pending` before any further decision is made. | `Task.ExpireLeaseIfDue`, called from `Claim`, and checked in `RenewLease` / `Complete` | `task.ErrNotClaimed` on renew/complete | `409` |
| T4 | **No double-complete.** A completed task rejects every further operation. | `Task.Claim` / `RenewLease` / `Complete` | `task.ErrAlreadyCompleted` | `409` |
| T5 | **Only the claim owner may renew or complete.** | `Task.RenewLease`, `Task.Complete` | `task.ErrNotOwner` | `409` |
| T6 | **Renew/complete require an active claim.** Acting on a `Pending` task is rejected. | `Task.RenewLease`, `Task.Complete` | `task.ErrNotClaimed` | `409` |

### The ordering inside `Claim` is itself an invariant

```go
func (t *Task) Claim(stationId, stationCapabilities, now, leaseDuration) error {
    if t.status == Completed        { return ErrAlreadyCompleted }
    t.ExpireLeaseIfDue(now)         // an expired lease implicitly frees the task
    if t.status == Claimed          { return ErrAlreadyClaimed }
    if !stationCapabilities.HasAll(t.requiredCapabilities) { return ErrCapabilityMismatch }
    t.status = Claimed
    t.lease = &Lease{StationId: stationId, Expiry: now.Add(leaseDuration)}
    return nil
}
```

Expiry is evaluated **before** the already-claimed check. Reverse those two
lines and a stale claim would permanently block the task — the very failure
the lease was introduced to prevent. This is checked by a failing-path test,
not left to code review.

Note also that `now` is a *parameter*. The domain never calls `time.Now()`;
time enters through `ports.Clock` at the application layer. That is what makes
lease expiry testable with a fixed clock instead of `time.Sleep`.

### `IsAvailable` vs `ExpireLeaseIfDue`

Two closely related methods with a deliberate difference:

- `IsAvailable(now)` is a **pure query** — true if `Pending`, or `Claimed`
  with a lapsed lease. It reports what a caller *would* find, without
  mutating.
- `ExpireLeaseIfDue(now)` **mutates**, returning `true` if it freed the task,
  so the caller knows it must persist and publish `LeaseExpired`.

Repositories use the first to filter candidates; the sweep uses the second.

## Station

A work position. Its job in the model is small on purpose: hold the capability
set that filters what its holder can pull.

| # | Invariant | Enforced by | Typed error | HTTP |
| --- | --- | --- | --- | --- |
| S1 | **One occupant at a time.** A second check-in is rejected. | `Station.CheckIn` | `station.ErrOccupied` | `409` |
| S2 | **Check-out requires an occupant.** | `Station.CheckOut` | `station.ErrNotOccupied` | `409` |
| S3 | **A station cannot accept a task it lacks capabilities for.** | `Station.ValidateAccept` → `CanAccept` | `station.ErrCapabilityMismatch` | `422` |

`OccupantId` is an opaque string. There is no roster, no certification record,
no shift window — those belong to `workforce-management`, which stops at the
process-path boundary. Keeping `Station` this thin is what keeps that boundary
from leaking.

:::note Honest status
Occupancy (`CheckIn` / `CheckOut`) is fully modelled and unit-tested on the
aggregate, but no HTTP endpoint exposes it today, and `ClaimNext` does not
require a station to be occupied before it can claim. The registered
capability set is the only station state the dispatch path reads. `POST /stations`
returns `occupied` in its response, always `false` for a freshly registered
station.
:::

## Package

Pack output: an order becoming a sealed carton, then passing or failing the
SLAM weigh-check.

| # | Invariant | Enforced by | Typed error | HTTP |
| --- | --- | --- | --- | --- |
| P1 | **Cannot seal without scanned contents.** An empty carton is never sealed. | `Package.Seal` | `pack.ErrNoScannedContents` | `422` |
| P2 | **Cannot seal twice**, and cannot scan into a non-`Open` package. | `Package.Seal`, `Package.ScanItem` | `pack.ErrAlreadySealed` | `409` |
| P3 | **SLAM requires a sealed package.** | `Package.Weigh` | `pack.ErrNotSealed` | `409` |
| P4 | **SLAM runs once.** A labelled or diverted package rejects a second weigh-check. | `Package.Weigh` | `pack.ErrAlreadyProcessed` | `409` |
| P5 | **SLAM diverts on weight discrepancy.** If `\|actual − expected\| > WeightTolerance` the package becomes `DIVERTED` instead of `LABELED`. | `Package.Weigh` | *(not an error — a domain outcome)* | `200` |

P5 is worth dwelling on: a diverted package is **not an error**. It is a
successful weigh-check with a negative result, so `POST /packages/{id}/slam`
returns `200` with the package in `DIVERTED` status, and raises
`WeightDiscrepancyDetected` *and* `PackageDiverted`. Modelling it as a `4xx`
would conflate "your request was wrong" with "the carton is wrong," and the
second is a normal, expected operational event.

`WeightTolerance` is `0.05`, in whatever unit the caller supplies weights in —
the aggregate does not carry units. In practice the API examples use
kilograms.

## Consistency boundaries

Each aggregate is one transaction. A use case never mutates two aggregates
inside one consistency boundary:

- `SealPackage` **reads** a `Task` (to validate the caller owns the claim and
  that it is a `PACK` task) but only **writes** the new `Package`.
- `RunSlam` touches only the `Package`.
- `CompleteTask` touches only the `Task`.

The cross-aggregate rule "only the station holding the Pack task's claim may
seal its package" is enforced in the `SealPackage` use case rather than inside
either aggregate — it is a rule *about* two aggregates, so it belongs in the
layer that can see both, not inside one of them.

## Read models are projections, never state

Queue depth is computed from task state on every request via
`TaskRepo.CountByTypeAndStatus(ctx, taskType, task.Pending)`. There is no
counter field on any aggregate, and nothing to keep in sync. The same applies
to any future throughput metric: derive it from the events, do not store it on
the thing the events are about.
