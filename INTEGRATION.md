# Cross-service integration (additive — Task 7, do NOT touch existing domain code)

This service CONSUMES `WorkReleased` from wes-work-planning and turns each one
into a Task via the existing `CreateTask` use case — this IS the intended use
of that use case, so call it directly (no new use case needed). Strictly
additive: new adapter only, no change to existing aggregates/invariants.

## Envelope (identical across all four warehouse-systems services)

```json
{
  "event_id": "uuid-v4",
  "event_type": "WorkReleased",
  "occurred_at": "2026-08-21T22:00:00Z",
  "source": "wes-work-planning",
  "data": {"path_id": "...", "work_unit_id": "...", "cpt": "RFC3339", "ref": "..."}
}
```

## Kafka

- Client library: `github.com/segmentio/kafka-go`.
- Broker: `KAFKA_BROKERS` env var (default `localhost:9092`). A shared broker
  already runs via `~/warehouse-systems/docker-compose.kafka.yml` — connect to
  it, do not add your own Kafka service to this repo's docker-compose.yml.
- New inbound package `internal/adapters/inbound/kafka/` — a consumer on topic
  `warehouse.work-planning.events`, filtering for `event_type == "WorkReleased"`.
- Mapping: `WorkReleased.data.path_id` → task type is NOT derivable from
  path_id alone in general, but for this integration assume the path_id string
  itself carries the task type as a prefix convention (e.g. "pick-*" → Pick,
  "pack-*" → Pack, "slam-*" → SLAM); default to Pick if no prefix matches, and
  say so explicitly in the README as a known simplification for this round.
  `WorkReleased.data.work_unit_id` → task's `ref`. `WorkReleased.data.cpt` →
  task's CPT. Required capabilities: derive from task type (e.g. Pick needs
  "pick" capability) matching the capability names Workforce Management uses.
- Consumer calls the existing `CreateTask` use case with these mapped fields.

## Idempotency (required — Kafka is at-least-once)

Add a new Postgres table `processed_events (event_id TEXT PRIMARY KEY,
processed_at TIMESTAMPTZ)` via a new migration. Before calling `CreateTask` for
a consumed message, attempt to insert its `event_id`; if it already exists,
skip (ack/commit anyway) — do not create a duplicate Task for the same
`WorkReleased` event. In-memory adapter: thread-safe `map[string]struct{}`.
Unit-test: consuming the same `WorkReleased` event_id twice creates exactly
one Task.

## Definition of done for Task 7

- New consumer adapter compiles and is unit-tested (feed it a fake envelope,
  assert exactly one Task created; feed the same event_id twice, assert still
  exactly one).
- Existing full suite (`go build ./...`, `go vet ./...`, `go test ./...`,
  `go test ./... -race`) still green, unchanged.
- README gains an "Integration" section: topic consumed, exact JSON schema
  above, the path_id-prefix simplification called out explicitly, the
  `KAFKA_BROKERS` env var.
- Do a REAL smoke test: with the shared broker running, publish a
  `WorkReleased`-shaped message via `kafka-console-producer.sh` (or a small Go
  one-off) to `warehouse.work-planning.events` and confirm a new Task appears
  via `GET /queues/{taskType}/depth` before declaring done.

---

## Task 8 — Publish TaskCompleted (additive, do NOT touch existing domain code)

This service now ALSO PUBLISHES `TaskCompleted` to close the control loop back
to Work Planning (the drum-buffer-rope feedback edge: Execution -> Orchestration).
Strictly additive: a new outbound adapter hook, no change to existing
aggregates, invariants, or use cases (including `CompleteTask` itself — do not
alter its logic, only add a publish call using its existing return value).

### Envelope

```json
{
  "event_id": "uuid-v4",
  "event_type": "TaskCompleted",
  "occurred_at": "2026-08-21T22:00:00Z",
  "source": "fulfillment-execution",
  "data": {"task_id": "...", "station_id": "...", "work_unit_id": "..."}
}
```

`work_unit_id` is the completed Task's `OrderRef()` — recall from Task 7 that
`WorkReleased.data.work_unit_id` was mapped into the Task's `ref` (OrderRef) at
creation time. So `OrderRef()` on the completed Task IS the original
`work_unit_id` from Work Planning. The domain event `TaskCompleted` itself only
carries `TaskId`/`StationId` (no OrderRef) — so the Kafka publisher adapter
must look the Task back up via the existing `TaskRepo` port to read its
`OrderRef()` before publishing, exactly the same pattern already used in
inventory-storage's Kafka publisher for `ReservationRevoked` (which backfills
sku/quantity via a repo lookup since its domain event is thin too). Do not
change the `TaskCompleted` domain event's fields — enrich only in the adapter.

### Kafka

- Reuse the SAME `internal/adapters/outbound/kafka/` publisher package added
  for other purposes if one exists from a prior round, or create it now if this
  is the first outbound Kafka adapter in this repo — check first, this repo may
  currently only have the Task 7 CONSUMER, not yet a publisher.
- Topic: `warehouse.fulfillment.events`.
- Publish `TaskCompleted` when the existing `CompleteTask` use case succeeds.
  Hook this the same way `EVENT_PUBLISHER` selection already works elsewhere in
  this codebase (log default, kafka opt-in via `EVENT_PUBLISHER=kafka`) — if
  this repo does not yet have that env-driven selection wired into
  `CompleteTask`'s publisher, add it now, consistent with the existing
  `ports.EventPublisher` interface used by other use cases.

Downstream consumer: wes-work-planning calls its existing `RecordCompletion`
use case with `WorkUnitId = data.work_unit_id`.

### Definition of done for Task 8

- New/extended Kafka publisher unit-tested for `TaskCompleted`: asserts the
  envelope shape including the `work_unit_id` enrichment via `TaskRepo` lookup.
- Existing full suite (`go build ./...`, `go vet ./...`, `go test ./...`,
  `go test ./... -race`) still green, unchanged, including Task 7's consumer.
- README's Integration section gains this new topic published, exact schema.
- REAL smoke test: with the shared broker running and `EVENT_PUBLISHER=kafka`,
  create a task, claim it, complete it via the running binary's HTTP API, and
  confirm a `TaskCompleted` message with the correct `work_unit_id` lands on
  `warehouse.fulfillment.events` via `kafka-console-consumer.sh` before
  declaring done.
