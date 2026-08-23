---
id: events
title: Events (AsyncAPI)
sidebar_label: Events
sidebar_position: 3
description: The CloudEvents envelope, the type naming convention, and every event this service publishes and consumes — from the real apis/asyncapi.yaml.
---

# Events

The asynchronous contract is `apis/asyncapi.yaml` (**AsyncAPI 2.6.0**), linted
in CI by Spectral alongside the OpenAPI spec. This page is hand-authored from
that file — everything below is traceable to it or to the adapter code, and
where the two disagree, that is called out rather than smoothed over.

## The CloudEvents envelope

Every message on the published API is a **CloudEvents 1.0** envelope in
*structured* content mode: the whole event — context attributes and `data`
together — is the JSON message payload.

`defaultContentType: application/cloudevents+json`

### Context attributes

| Attribute | Required | Type | Meaning |
| --- | --- | --- | --- |
| `specversion` | ✅ | `"1.0"` | CloudEvents spec version |
| `id` | ✅ | UUID | Unique per source; the deduplication key |
| `source` | ✅ | URI-reference | The producing context — `/warehouse/fulfillment-execution` |
| `type` | ✅ | string | The DDD coordinates of the fact (see below) |
| `subject` | | string | The aggregate instance id the event is about |
| `time` | | RFC 3339 | When the occurrence happened |
| `datacontenttype` | | `application/json` | Content type of `data` |
| `data` | ✅ | object | Event-specific payload |

### The `type` naming convention

```
com.warehouse.<subdomain>.<bounded-context>.<entity>.<EventName>
```

For this service, `<subdomain>` is `wes` and `<bounded-context>` is
`fulfillment-execution`. `<entity>` is the aggregate — `task` or `package`:

```
com.warehouse.wes.fulfillment-execution.task.TaskCompleted
com.warehouse.wes.fulfillment-execution.package.WeightDiscrepancyDetected
```

The point of encoding the full coordinates in `type` is that a consumer can
route or filter **purely on `type`**, without deserialising `data` — and the
name itself tells you which context owns the fact and which aggregate raised
it.

### Example

```json
{
  "specversion": "1.0",
  "id": "3c4d5e6f-7a8b-4c9d-0e1f-2a3b4c5d6e7f",
  "source": "/warehouse/fulfillment-execution",
  "type": "com.warehouse.wes.fulfillment-execution.task.TaskCompleted",
  "subject": "task-8a1f",
  "time": "2026-08-22T14:04:00Z",
  "datacontenttype": "application/json",
  "data": {
    "taskId": "task-8a1f",
    "stationId": "station-03",
    "workUnitId": "wu-8a1f"
  }
}
```

## Channel

| Channel | Operation | Description |
| --- | --- | --- |
| `warehouse.fulfillment-execution.events` | `subscribe` (`consumeFulfillmentExecutionEvents`) | Every integration event emitted by this context, keyed by aggregate id. Consumers demultiplex on `type`. |

Server: `kafka.warehouse-systems.internal:9092`, protocol `kafka`, configured
per service via `KAFKA_BROKERS`.

## Events published

All nine domain events are in the catalogue. **Only `TaskCompleted` is
actually on the wire today** — the rest are documented as the intended
contract and are in-process only, exactly as each message's own description in
`asyncapi.yaml` states.

| Event | `type` suffix | `data` fields | On the wire today? |
| --- | --- | --- | --- |
| `TaskCreated` | `task.TaskCreated` | `taskId` | No |
| `TaskClaimed` | `task.TaskClaimed` | `taskId`, `stationId` | No |
| `LeaseExpired` | `task.LeaseExpired` | `taskId` | No |
| **`TaskCompleted`** | `task.TaskCompleted` | `taskId`, `stationId`, `workUnitId` | **Yes** |
| `ItemPicked` | `task.ItemPicked` | `taskId` | No — and not raised by any use case either |
| `PackageSealed` | `package.PackageSealed` | `packageId` | No |
| `WeightDiscrepancyDetected` | `package.WeightDiscrepancyDetected` | `packageId`, `expectedWeight`, `actualWeight` | No |
| `LabelApplied` | `package.LabelApplied` | `packageId` | No |
| `PackageDiverted` | `package.PackageDiverted` | `packageId` | No |

`expectedWeight` and `actualWeight` are `number` / `format: double`, in
kilograms in every example. Everything else is a string.

### `TaskCompleted` and the `workUnitId` enrichment

`workUnitId` is the field that makes the feedback loop work. The *domain*
event carries only `TaskId` and `StationId`; the publisher looks the task back
up through `ports.TaskRepo` and reads `OrderRef()` — which was populated from
`WorkReleased.data.work_unit_id` when the task was created. So the value
Work Planning gets back is exactly the one it sent, and it can call
`RecordCompletion(workUnitId)` directly.

## Events consumed

| Source context | Topic | `event_type` | Effect here |
| --- | --- | --- | --- |
| `wes-work-planning` | `warehouse.work-planning.events` | `WorkReleased` | Creates a `Task` via the `CreateTask` use case |

`WorkReleased` arrives in the **flat platform envelope**, not CloudEvents:

```json
{
  "event_id": "uuid-v4",
  "event_type": "WorkReleased",
  "occurred_at": "2026-08-21T22:00:00Z",
  "source": "wes-work-planning",
  "data": {
    "path_id": "pick-zone-a",
    "work_unit_id": "wu-8a1f",
    "cpt": "2026-08-23T18:00:00Z",
    "ref": "order-4471"
  }
}
```

Mapping into this context's model (the Anti-Corruption Layer):

| From | To | How |
| --- | --- | --- |
| `data.path_id` | `task.Type` | prefix: `pick-*`→`PICK`, `pack-*`→`PACK`, `slam-*`→`SLAM`, default `PICK` |
| `data.work_unit_id` | `shared.OrderRef` | direct |
| `data.cpt` | `shared.CPT` | RFC 3339 → `time.Time` |
| *(from type)* | `shared.CapabilitySet` | `PICK`→`{pick}`, `PACK`→`{pack}`, `SLAM`→`{slam}` |
| `data.ref` | *(unused)* | decoded but not mapped — `work_unit_id` is the correlation key |

The consumer filters on `event_type == "WorkReleased"` and silently ignores
everything else on the topic, so new event types upstream cannot break it.

### Idempotency

Kafka delivers at least once, so the consumer is idempotent by construction.
Before creating a task it calls `ProcessedEvents.MarkProcessed(ctx, event_id)`,
which returns `true` only if this call newly recorded the id:

- **New id** → create the `Task`.
- **Already present** → skip and acknowledge anyway.

Postgres backs this with `processed_events (event_id TEXT PRIMARY KEY,
processed_at TIMESTAMPTZ)` — the primary-key constraint *is* the deduplication,
so it is atomic rather than a read-then-write race. The in-memory adapter uses
a mutex-guarded map. A unit test feeds the same `event_id` twice and asserts
exactly one task exists.

Consumer group: `fulfillment-execution`. A handling error is logged and the
loop continues, so one malformed message cannot wedge the consumer.

## Known divergence: spec vs. wire

:::warning The published spec and the current publisher do not match
This is a real, unresolved gap and is documented rather than hidden.

| | `apis/asyncapi.yaml` (target contract) | `internal/adapters/outbound/kafka/publisher.go` (today) |
| --- | --- | --- |
| Channel / topic | `warehouse.fulfillment-execution.events` | `warehouse.fulfillment.events` |
| Envelope | CloudEvents 1.0 structured | flat platform envelope |
| Type field | `type: com.warehouse.wes.fulfillment-execution.task.TaskCompleted` | `event_type: "TaskCompleted"` |
| Id field | `id` | `event_id` |
| Timestamp | `time` | `occurred_at` |
| Source | `/warehouse/fulfillment-execution` | `fulfillment-execution` |
| Payload keys | `taskId`, `stationId`, `workUnitId` | `task_id`, `station_id`, `work_unit_id` |

What actually goes on the wire today:

```json
{
  "event_id": "…",
  "event_type": "TaskCompleted",
  "occurred_at": "2026-08-22T14:04:00Z",
  "source": "fulfillment-execution",
  "data": {
    "task_id": "task-8a1f",
    "station_id": "station-03",
    "work_unit_id": "wu-8a1f"
  }
}
```

`wes-work-planning`'s consumer reads **this** shape from
`warehouse.fulfillment.events`, so the live integration is correct and
working — it is the AsyncAPI document that describes where the platform is
going, not where the publisher is. Migrating the publisher to CloudEvents is
outstanding work; both sides would have to move together, since the topic name
changes too.
:::

## Smoke-testing the real thing

With the shared broker from `~/warehouse-systems/docker-compose.kafka.yml`
running:

```bash
# Consume: publish a WorkReleased and watch a Task appear
kafka-console-producer.sh --broker-list localhost:9092 \
  --topic warehouse.work-planning.events <<'EOF'
{"event_id":"11111111-1111-4111-8111-111111111111","event_type":"WorkReleased","occurred_at":"2026-08-23T09:00:00Z","source":"wes-work-planning","data":{"path_id":"pick-zone-a","work_unit_id":"wu-8a1f","cpt":"2026-08-23T18:00:00Z","ref":"order-4471"}}
EOF
curl -sS localhost:8080/queues/PICK/depth

# Publish: complete a task with EVENT_PUBLISHER=kafka and watch it land
kafka-console-consumer.sh --bootstrap-server localhost:9092 \
  --topic warehouse.fulfillment.events --from-beginning
```

Sending the same `event_id` twice must still produce exactly one task — that
is the idempotency guarantee, and it is worth verifying by hand at least once.
