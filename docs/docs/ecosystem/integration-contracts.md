---
id: integration-contracts
title: Integration contracts
sidebar_label: Integration contracts
sidebar_position: 2
description: The exact topics, envelopes, mappings, idempotency guarantees and configuration behind this service's live Kafka edges.
---

# Integration contracts

The operational detail behind the Kafka edges on the
[Context map](./context-map.md). Everything here is taken from the adapter
code and `INTEGRATION.md`, not from intent.

## Topic summary

| Direction | Topic | Event | Adapter |
| --- | --- | --- | --- |
| Consume | `warehouse.work-planning.events` | `WorkReleased` | `internal/adapters/inbound/kafka/consumer.go` |
| Publish | `warehouse.fulfillment.events` | `TaskCompleted`, `TaskCPTMissed`, `PackageManifested` | `internal/adapters/outbound/kafka/publisher.go` |
| Consume (opt-in) | `warehouse.process-path-management.events` | process-path catalogue events | `internal/adapters/outbound/kafkacatalog` — only when `PATH_CATALOGUE_SOURCE=kafka` |

Client library on both sides: `github.com/segmentio/kafka-go` (pure Go, no
cgo). Broker list comes from `KAFKA_BROKERS`, default `localhost:9092`.

A shared broker for the whole platform runs in the `warehouse-infra` kind
cluster, exposed on the host at `localhost:9092`. This repo's own
`docker-compose.yml` deliberately defines **only Postgres** — adding a second
broker would fragment the platform's integration testing.

## Inbound: `WorkReleased`

### Envelope

The flat platform envelope shared across the fleet's integrating services:

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
    "ref": "order-4471",
    "fragile": false,
    "gift_wrap": false
  }
}
```

`fragile` and `gift_wrap` are optional packing hints (default `false`).

### The translation (Anti-Corruption Layer)

| From | To | How |
| --- | --- | --- |
| `data.path_id` | `task.Type` | process-path catalogue lookup, longest `matchPrefix` wins — see below |
| `data.work_unit_id` | `shared.OrderRef` | direct |
| `data.cpt` | `shared.CPT` | RFC 3339 timestamp |
| *(from the matched path)* | `shared.CapabilitySet` | the path definition's `requiredCapabilities` |
| `data.fragile` / `data.gift_wrap` | `Task.Fragile` / `Task.GiftWrap` | direct, default `false` |
| `data.ref` | — | decoded, not mapped |

The consumer then calls the **existing** `CreateTask` use case. No new use
case was introduced for the Kafka path — creating a task from released work
*is* what `CreateTask` is for, and giving the Kafka path its own parallel use
case would have meant two code paths that must stay in agreement.

:::info `path_id` resolves through the process-path catalogue
Since [ADR-0017](../adr/0017-process-path-catalogue-as-configuration.md) the consumer calls `PathCatalogue.Lookup(path_id)`:
the **longest** declared `matchPrefix` that prefixes the id (case-insensitive)
wins, and the matched definition supplies both the task type (its `id`, e.g.
`PICK`, `PACK`, `SLAM`, `REBIN`) and the required capabilities. A `path_id`
that matches nothing is a **hard error** — the message is not acked as a
silent Pick task, which is what the retired prefix-guessing code used to do.

The catalogue comes from `PATH_CATALOGUE_SOURCE`: `file` (default) loads
`PATH_CATALOGUE_FILE` once at boot; `kafka` replays
`warehouse.process-path-management.events` into memory and follows live
changes. The `warehouse-infra` kind cluster runs the `kafka` source; its
`config/process-paths/sortable-fc.yaml` is kept only as the rollback target
and as a handy file for local `go run`.
:::

### Idempotency

Kafka is at-least-once, so redelivery is expected, not exceptional. Before
creating anything, the consumer calls:

```go
isNew, err := c.Processed.MarkProcessed(ctx, env.EventId)
if !isNew {
    return nil   // already applied by a prior delivery — ack anyway
}
```

`MarkProcessed` returns `true` **only** if this call newly recorded the id, so
the check-and-record is a single atomic operation rather than a
read-then-write race.

| Adapter | Mechanism |
| --- | --- |
| Postgres | `processed_events (event_id TEXT PRIMARY KEY, processed_at TIMESTAMPTZ)` — the primary-key constraint *is* the deduplication |
| Memory | mutex-guarded `map[string]struct{}` |

A unit test feeds the same `event_id` twice and asserts exactly one task
exists.

### Failure handling

`Run` logs a handling error and continues the loop, so a single malformed
message cannot wedge the consumer. Note the trade-off: there is **no dead
letter queue** — a message that fails to process is logged and dropped, and
because `MarkProcessed` runs *before* `CreateTask`, an event whose task
creation fails will be treated as already-processed on redelivery. That is a
deliberate simplicity choice at this stage, and a real deployment would want
either a DLQ or marking-after-success with a compensating uniqueness check.

Consumer group: `fulfillment-execution`.

## Outbound: `TaskCompleted`

### Selection

The publisher is chosen at the composition root:

```go
if getenv("EVENT_PUBLISHER", "log") == "kafka" {
    kafkaPublisher = outboundkafka.NewPublisher(kafkaBrokers, taskRepo, uuidLike)
    publisher = kafkaPublisher
} else {
    publisher = events.NewLogPublisher(nil)
}
```

Both satisfy `ports.EventPublisher`, so **no use case knows which one it
got** — the default stays `log` so tests and local runs need no broker.

### Envelope on the wire

```json
{
  "event_id": "uuid-v4",
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

Message key: the task id, so all events for one task land on the same
partition and preserve order.

### The enrichment

`TaskCompleted` as a domain event carries only `TaskId` and `StationId`. The
publisher backfills the third field:

```go
t, err := p.Tasks.FindById(ctx, tc.TaskId)
workUnitId = string(t.OrderRef())
```

`OrderRef` was populated from `WorkReleased.data.work_unit_id` when the task
was created, so the value returned is exactly the one Work Planning sent —
it can call `RecordCompletion(workUnitId)` without a join.

The enrichment happens **in the adapter**, never on the domain event. The
correlation key exists because a specific consumer needs it; letting that
requirement shape the domain model would be the tail wagging the dog. The same
repo-lookup pattern is used by `inventory-storage`'s publisher for
`ReservationRevoked`, so it is a platform convention.

Events other than `TaskCompleted` passed to this publisher are **skipped**,
not errored — they are simply not part of the published integration contract
yet.

## Configuration

| Env var | Default | Effect |
| --- | --- | --- |
| `KAFKA_BROKERS` | `localhost:9092` | Comma-separated broker list for **both** the consumer and the publisher |
| `EVENT_PUBLISHER` | `log` | `kafka` swaps in the Kafka publisher for `ports.EventPublisher` |
| `DATABASE_URL` | *(unset)* | Unset selects in-memory repositories **including** `ProcessedEvents` — idempotency then holds only for the lifetime of the process |

That last row matters operationally: running with `EVENT_PUBLISHER=kafka` but
without `DATABASE_URL` gives you in-memory deduplication, so a restart forgets
which events were processed and redelivered messages will create duplicate
tasks. The consumer starts regardless of storage choice.

## Verifying it end to end

With the shared broker running:

```bash
# 1. Inbound — publish a WorkReleased, confirm a Task appears
kafka-console-producer.sh --broker-list localhost:9092 \
  --topic warehouse.work-planning.events <<'EOF'
{"event_id":"11111111-1111-4111-8111-111111111111","event_type":"WorkReleased","occurred_at":"2026-08-23T09:00:00Z","source":"wes-work-planning","data":{"path_id":"pick-zone-a","work_unit_id":"wu-8a1f","cpt":"2026-08-23T18:00:00Z","ref":"order-4471"}}
EOF
curl -sS localhost:8080/queues/PICK/depth      # depth increments by exactly 1

# 2. Send the identical event_id again — depth must NOT change

# 3. Outbound — with EVENT_PUBLISHER=kafka, drive the flow over HTTP
kafka-console-consumer.sh --bootstrap-server localhost:9092 \
  --topic warehouse.fulfillment.events --from-beginning
# then register a station, claim-next, complete — and watch the message land
# with work_unit_id == "wu-8a1f"
```

## Contract linting

Both `apis/openapi.yaml` and `apis/asyncapi.yaml` are linted by
[Spectral](https://stoplight.io/open-source/spectral) in the `api-lint` CI
job, using `.spectral.yaml` and `.spectral.asyncapi.yaml`. A contract change
that breaks either ruleset fails the build before it can reach a consumer.

The one thing linting cannot catch is the divergence between the AsyncAPI
document's CloudEvents envelope and the publisher's current flat envelope —
Spectral validates the spec's internal consistency, not the code's conformance
to it. That gap is documented in full on the [Events](../api-reference/events.md)
page.
