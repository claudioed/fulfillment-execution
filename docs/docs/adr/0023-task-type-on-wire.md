---
id: 0023-task-type-on-wire
title: 23. Put the completed task's own type on TaskCompleted's wire payload
sidebar_label: 23. task_type on the wire
sidebar_position: 23
description: Adds an additive, optional task_type field to TaskCompleted's Kafka payload, closing a documented labor-performance wire gap (ADR-0014) that left per-task-type utilization permanently null.
---

# 23. Put the completed task's own type on `TaskCompleted`'s wire payload

## Status

**Accepted.**

## Context

`TaskCompletedData` (`internal/adapters/outbound/kafka/publisher.go`) has
carried `task_id`, `station_id`, `work_unit_id`, `associate_id`, and
`duration_seconds` since ADR-0014, but never the completed task's own
`type` (`PICK`/`PACK`/`SLAM`/`REBIN`) — a fact this service has always
had, on the same `Task` aggregate `WorkUnitId` is already resolved from.

This absence was a real, documented gap in `labor-performance`, the
consumer of this event: its `ParseTaskTypeLenient("")` always resolves to
`""` (unclassified) because the wire never carries anything else,
verified concretely at
`labor-performance/internal/adapters/inbound/kafka/consumer.go`'s
`taskCompletedData` struct and its own doc comment, which names this
exact gap and points back here. The practical consequence, discovered
live while verifying labor-performance/workforce-management/warehouse-
ops-agent's idleness-and-utilization feature (labor-performance ADR-0014,
workforce-management ADR-0020, warehouse-ops-agent ADR-0008) in the
running cluster: `GetTaskTypeUtilization("PICK", ...)` always returned
`utilizationPct: null` even with real, correctly-computed idle/task
seconds flowing through the pipeline, because every `TaskPerformance` row
labor-performance ever recorded was bucketed under the empty task type,
never under `PICK` specifically. The idleness feature's own math was
correct; it simply had no key to group by.

The forces at play:

- **This service is the sole owner of `Task.Type()`.** No other context
  should ever need to ask this service synchronously "what type was task
  X" — that is exactly the shape of coupling ADR-0014 already rejected
  for `AssociateId`/`DurationSeconds` (a new query endpoint instead of
  enriching the existing event), and the same reasoning applies here
  unchanged.
- **The value is already loaded.** `Encode()` already calls
  `p.Tasks.FindById(ctx, tc.TaskId)` to resolve `WorkUnitId` — reading
  `t.Type()` off that same `*task.Task` costs nothing extra: no new repo
  dependency, no new query, no new failure mode.
- **Must be additive and non-breaking.** `TaskCompletedData` is a live
  wire contract with a real downstream consumer already deployed
  (labor-performance). A new, `omitempty` string field is invisible to
  any consumer that doesn't look for it — exactly the same shape of
  change ADR-0014 made twice already for `associate_id`/
  `duration_seconds`.
- **Must degrade the same way every other enrichment on this envelope
  does.** When the completed task cannot be found (a lookup miss),
  `WorkUnitId`/`DurationSeconds` already fall back to their zero values
  rather than failing the publish. `TaskType` needed the identical
  degrade-gracefully behavior — never a reason to fail or skip
  publishing a real "this task finished" fact.

## Decision

**Add `TaskType string` (JSON `task_type`, `omitempty`) to
`TaskCompletedData`, populated from the already-loaded `Task`'s `Type()`
inside `Encode()` — no new repo dependency, no domain event change.**

```go
type TaskCompletedData struct {
    TaskId          string `json:"task_id"`
    StationId       string `json:"station_id"`
    WorkUnitId      string `json:"work_unit_id"`
    AssociateId     string `json:"associate_id,omitempty"`
    DurationSeconds int64  `json:"duration_seconds,omitempty"`
    TaskType        string `json:"task_type,omitempty"`
}
```

```go
var workUnitId string
var durationSeconds int64
var taskType string
if t != nil {
    workUnitId = string(t.OrderRef())
    if claimedAt := t.ClaimedAt(); claimedAt != nil {
        durationSeconds = int64(tc.OccurredAt().Sub(*claimedAt).Seconds())
    }
    taskType = string(t.Type())
}
```

`TaskType` is `""` (omitted on the wire) only when the completed task
cannot be found by `TaskRepo.FindById` — the same lookup-miss path
`WorkUnitId` already degrades through. Unlike `AssociateId` (a genuinely
optional fact — some stations have no occupant) or `DurationSeconds` (a
genuinely optional fact — some tasks predate the `claimedAt` column),
every real, findable `Task` always has a `Type()`; an empty `TaskType` on
the wire is therefore a much rarer signal ("this task's own record is
gone") than an empty `AssociateId`/`DurationSeconds` ever is.

`Task.Type` (`PICK`/`PACK`/`SLAM`/`REBIN`) is published verbatim, with no
translation layer — it already matches labor-performance's own
`shared.TaskType` enum (`PICK`/`PACK`/`SLAM`) exactly, byte for byte,
confirmed against `labor-performance/internal/domain/shared/shared.go`'s
own doc comment ("mirrors fulfillment-execution's task.Type enum
exactly"). `REBIN` is the one value this service models that
labor-performance's `ParseTaskTypeLenient` does not recognize; that is
labor-performance's own scoping decision (it only defines engineered
labor standards for PICK/PACK/SLAM today), not something this ADR
changes — a `REBIN` `TaskCompleted` event will still record correctly,
just with `TaskType` resolving to `""` (unclassified) on the consumer
side, exactly as an unrecognized value already does today.

## Consequences

### Easier

- **Closes a real, previously-documented gap with zero new
  infrastructure.** No new topic, no new endpoint, no schema version
  bump, no new repo dependency — one struct field and three lines in an
  already-existing enrichment loop.
- **labor-performance's per-task-type utilization/idleness buckets
  (`get_task_type_utilization`, `GetUtilization`) start populating for
  real** the moment this ships and labor-performance's own consumer is
  updated to read the field (see the companion labor-performance PR) —
  no coordinated migration or backfill needed, since old
  `TaskPerformanceRecorded` rows simply stay bucketed under `""` and new
  ones bucket correctly going forward.
- **workforce-management's idle-share-triggered `ProposePathPlan` trim
  (ADR-0020) and warehouse-ops-agent's utilization correlation
  (ADR-0008) both become fully live** once real per-task-type data
  exists, closing the loop the idleness feature's three ADRs each
  independently flagged as "pending this separate gap."

### Harder

- **No backfill for historical rows.** Every `TaskPerformanceRecorded`
  event fired before this change (and before labor-performance consumes
  the new field) is permanently bucketed under the empty task type in
  labor-performance's store — this ADR does not retroactively reclassify
  anything, by design (task type at the time of an already-completed,
  already-recorded task cannot be recovered from this service after the
  fact if the original completion predates this change).
- **A `REBIN` completion still resolves to unclassified on the consumer
  side** until labor-performance separately chooses to model REBIN as a
  fourth engineered-labor-standard task type — a real, known, and
  deliberately out-of-scope-here limitation, not a bug in this change.
