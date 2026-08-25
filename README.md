# Fulfillment Execution

> **⚠️ Study project.** This repository is an educational exercise in
> Domain-Driven Design applied to warehouse management/execution systems. It
> follows real industry-standard patterns and terminology (WMS/WES/WCS,
> pull-based dispatch, CloudEvents, RFC 7807, hexagonal architecture) but is
> **not a production system** and is **not affiliated with, endorsed by, or
> representative of Amazon or any other company**.

The task-lifecycle core bounded context for Pick, Pack, and SLAM. Downstream
of Work Planning (which releases work); issues commands to WCS/equipment.
Dispatch is **pull, not push**: a station calls `claimNext(stationId,
capabilities)` and the system selects the best-fit pending task — the system
never names a station in advance. Claims are at-most-once and time-boxed by a
**lease**: an unconfirmed claim expires back to the pool rather than
vanishing.

See `CLAUDE.md` for the full architecture and ubiquitous language, and
`/Users/claudioed/docs/amazon-fulfillment-ddd.md` for the source domain model.

## Documentation

Full documentation site: **<https://claudioed.github.io/fulfillment-execution/>**

It covers the business context and ubiquitous language, the DDD model
(subdomain classification, aggregates and invariants, domain events), an API
reference generated from `apis/openapi.yaml` plus a hand-written Events page
from `apis/asyncapi.yaml`, the ecosystem context map, and the Architecture
Decision Records. Source lives in `docs/` and deploys via
`.github/workflows/docs.yml`.

## Architecture

Hexagonal / Ports & Adapters, with a strict dependency rule: **domain depends
on nothing; application depends on domain; adapters depend on
application/domain.**

```
cmd/execution/               main.go — composition root
internal/
  domain/
    task/                    Task aggregate (Pick|Pack|SLAM lifecycle, lease)
    station/                 Station aggregate (occupant, capabilities)
    package/                 Package aggregate (pack -> sealed; SLAM weigh-check)
    shared/                  value objects: TaskId, StationId, CPT, Capability, events
  application/
    ports/                   OUT: TaskRepo, StationRepo, PackageRepo, EventPublisher, Clock
    usecases/                one struct per use case
  adapters/
    inbound/http/            chi handlers, DTOs, error mapping
    outbound/postgres/       pgxpool repos + migrations
    outbound/memory/         in-memory repos for tests/local
    outbound/events/         log/buffered publisher (kafka-ready iface)
migrations/                  golang-migrate SQL files
```

`internal/domain/package` is a Go package named `pack` (not `package`, which
is a reserved keyword) — imported as `pack "github.com/claudioed/fulfillment-execution/internal/domain/package"`.

## Run

### In-memory (no database required)

```sh
go run ./cmd/execution
```

The service starts on `:8080` using in-memory adapters whenever
`DATABASE_URL` is unset.

### With Postgres

```sh
docker compose up -d postgres
export DATABASE_URL="postgres://fulfillment:fulfillment@localhost:5432/fulfillment_execution?sslmode=disable"
go run ./cmd/execution
```

On startup, `main.go` applies every migration under `migrations/` via
`golang-migrate` before serving traffic. To manage migrations independently
with the `golang-migrate` CLI instead:

```sh
migrate -path migrations -database "$DATABASE_URL" up
```

### Configuration

| Env var        | Default | Purpose                          |
|----------------|---------|-----------------------------------|
| `HTTP_ADDR`    | `:8080` | HTTP listen address               |
| `DATABASE_URL` | (unset) | Postgres DSN; unset selects memory adapters |
| `KAFKA_BROKERS` | `localhost:9092` | Comma-separated Kafka broker list, used by both the `WorkReleased` consumer and the `TaskCompleted` publisher |
| `EVENT_PUBLISHER` | `log` | `log` publishes domain events to stdout only; `kafka` additionally publishes `TaskCompleted` to `warehouse.fulfillment.events` |
| `LOG_LEVEL`    | `info`  | `debug` \| `info` \| `warn` \| `error`, case-insensitive |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | OTel Collector's OTLP/gRPC address (see [Observability](#observability)) |
| `OTEL_SERVICE_NAME` | `fulfillment-execution` | `service.name` resource attribute |
| `SERVICE_VERSION` | `dev` | `service.version` resource attribute |
| `ENVIRONMENT`  | `local` | `deployment.environment.name` resource attribute |

## Observability

Traces and metrics are exported over **OTLP/gRPC** to an OpenTelemetry
Collector; the service does not expose a Prometheus scrape endpoint of its
own — the Collector handles Prometheus exposition for the whole fleet. A
Collector is expected at `OTEL_EXPORTER_OTLP_ENDPOINT` (default
`localhost:4317`); in the `warehouse-infra` kind cluster the Helm chart
points that at the in-cluster Collector Service
(`otel-collector.observability.svc.cluster.local:4317`).

**An unreachable Collector is never fatal.** The OTLP exporters dial lazily
and no blocking dial option is set, so with nothing listening the service
still starts and serves at full speed — telemetry is simply dropped.

### What gets exported

| Signal | What |
|--------|------|
| Traces | One server span per HTTP request (via `otelchi`), named after the **route pattern** (`POST /tasks/{id}/complete`) rather than the raw path; a child span per Postgres query/batch/copy/acquire (via `otelpgx`), carrying the parameterised SQL — query values are never recorded; `kafka.publish <topic>` / `kafka.consume <topic>` spans around the Kafka boundary |
| Metrics | `http.server.request.duration` (histogram, seconds, by route + method + status); `fulfillment.tasks.claimed` and `fulfillment.tasks.completed` counters attributed by `task.type` (Pick \| Pack \| SLAM); pgxpool connection gauges; Go runtime metrics (goroutines, GC, memory) |
| Logs | Structured JSON on stdout. Any log emitted while a span is active also carries `trace_id` and `span_id`, so a log line links straight to its trace |

The two task counters are incremented inside the `ClaimNext` and
`CompleteTask` **use cases**, not in the HTTP handler, so they count the real
domain events — a rejected claim or a rejected completion does not count.

### Distributed tracing across services

Trace context crosses the Kafka boundary in the message headers (W3C
`traceparent`), injected on publish and extracted on consume. A `WorkReleased`
published by `wes-work-planning` and the `Task` created from it here are
therefore parts of a **single trace**, as is the `TaskCompleted` this service
publishes back onto `warehouse.fulfillment.events`.

### Seeing it locally

```sh
OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4317 LOG_LEVEL=info go run ./cmd/execution
```

Every request line then looks like:

```json
{"time":"...","level":"INFO","msg":"http request","method":"POST","path":"/tasks",
 "status":201,"trace_id":"8812c36621d214139a08949823716b93","span_id":"14ddf02dbd8913ba"}
```

## Local development / quality gate

Every sensor CI runs is available locally through the `Makefile`, so problems
get caught and self-corrected before they reach the pipeline:

```sh
make help          # list every target
make check         # FAST pre-commit loop: fmt-check, vet, build, lint, test
make check-all     # pre-push gate: check + coverage (90% gate), arch-test, bdd
make vuln          # govulncheck ./... — known CVEs in deps and the Go stdlib
make mutation-fast # blocking mutation subset (./internal/domain/task)
make integration   # needs DATABASE_URL and a running Postgres
```

Git hooks are managed by [lefthook](https://github.com/evilmartians/lefthook)
(`lefthook.yml`): `pre-commit` runs `make fmt-check vet lint`, `pre-push` runs
`make check`. Activate them once per clone:

```sh
brew install lefthook          # or: go install github.com/evilmartians/lefthook@latest
lefthook install
```

## Tests

```sh
go build ./...
go vet ./...
go test ./...
go test -race ./...
gofmt -l .            # must print nothing

# Postgres integration test (needs a running database)
docker compose up -d postgres
export DATABASE_URL="postgres://fulfillment:fulfillment@localhost:5432/fulfillment_execution?sslmode=disable"
go test -tags integration ./internal/adapters/outbound/postgres/...
```

## BDD / Acceptance tests

Business-readable acceptance tests are written in Gherkin and executed with
[godog](https://github.com/cucumber/godog), the official Cucumber
implementation for Go. The feature files live under `features/`:

| Feature file | Covers |
| --- | --- |
| `features/claim_next.feature` | `claimNext` pull dispatch: earliest-CPT selection, capability mismatch, at-most-once claiming |
| `features/lease.feature` | Lease renewal before expiry, and lease expiry returning a Task to the pool |
| `features/complete_task.feature` | Completing a claimed Task, and rejecting a non-owner |
| `features/pack_slam.feature` | Sealing a Package, and the SLAM weigh-check applying a label vs diverting |

The step definitions live in `features_test.go` at the repo root. They are
black-box: each scenario spins up the real chi router (in-memory repositories,
buffered event publisher, fixed Clock) behind an `httptest` server and drives
it with real HTTP calls to the endpoints documented under [API](#api). Every
scenario gets a fresh server and fresh state.

Run them locally with:

```sh
go test ./... -run TestFeatures -v
```

CI runs the same command in the `bdd` job.

## API

All endpoints accept/return JSON. Every error response uses
[RFC 7807](https://www.rfc-editor.org/rfc/rfc7807) Problem Details
(`Content-Type: application/problem+json`) instead of a bespoke shape:

```json
{
  "type": "https://errors.fulfillment-execution.warehouse-systems.dev/task-not-found",
  "title": "Task not found",
  "status": 404,
  "detail": "usecases: task not found",
  "instance": "/tasks/does-not-exist/renew-lease"
}
```

`type` identifies the error category (does not need to resolve to a real
page), `title` is a fixed human summary for that category, `status`
duplicates the HTTP status code, `detail` is the specific error text for
this occurrence, and `instance` is the request path that produced it
(omitted for a validation error on a bare collection-create endpoint like
`POST /tasks`, which has no path segment identifying a specific resource).
See `apis/openapi.yaml`'s `components/schemas/Problem` for the full schema.

### Create a task (put work in the pool)

```sh
curl -sX POST localhost:8080/tasks \
  -H 'Content-Type: application/json' \
  -d '{
        "type": "PICK",
        "cpt": "2026-01-01T18:00:00Z",
        "orderRef": "order-42",
        "requiredCapabilities": ["pick"],
        "fragile": false
      }'
```

`fragile` is optional (defaults to `false`); it is a packing hint normally
stamped by `wes-work-planning` at release time, not something a human caller
typically sets by hand. `requiredCapabilities` may include `hazmat` — see
[Integration](#integration) below and `docs/docs/adr/0009-fragile-and-hazmat-handling-flags.md`
for why that needs no special handling beyond the value itself.

### Register a station

```sh
curl -sX POST localhost:8080/stations \
  -H 'Content-Type: application/json' \
  -d '{"stationId": "station-1", "capabilities": ["pick"]}'
```

Creates a station with the given capabilities, or updates its capability set
if `stationId` already exists (idempotent re-registration, e.g. recertifying
a station). Closes the pull-dispatch gap for HTTP-only smoke testing: without
this endpoint there was no way to create a station over HTTP, so `claimNext`
below always failed with "station not found" against a freshly-started server.

### claimNext — a station pulls the best-fit pending task

```sh
curl -sX POST localhost:8080/stations/station-1/claim-next \
  -H 'Content-Type: application/json' \
  -d '{"taskType": "PICK"}'
```

Returns the highest-priority (earliest CPT) pending task the station is
certified/equipped for, and leases it to that station. Returns `409` if the
pool has nothing the station can accept right now.

### Renew a lease

```sh
curl -sX POST localhost:8080/tasks/{taskId}/renew-lease \
  -H 'Content-Type: application/json' \
  -d '{"stationId": "station-1"}'
```

### Complete a task

```sh
curl -sX POST localhost:8080/tasks/{taskId}/complete \
  -H 'Content-Type: application/json' \
  -d '{"stationId": "station-1"}'
```

### Seal a package (Pack path)

```sh
curl -sX POST localhost:8080/tasks/{taskId}/seal-package \
  -H 'Content-Type: application/json' \
  -d '{"stationId": "station-1", "contents": ["sku-1", "sku-2"]}'
```

### Run SLAM (Scan, Label, Apply, Manifest)

```sh
curl -sX POST localhost:8080/packages/{packageId}/slam \
  -H 'Content-Type: application/json' \
  -d '{"actualWeight": 2.02, "expectedWeight": 2.0}'
```

Applies the shipping label if the actual weight is within tolerance of
expected; otherwise the package is diverted.

### Queue depth (read model)

```sh
curl -s localhost:8080/queues/PICK/depth
```

### Sweep expired leases (Clock-driven)

```sh
curl -sX POST localhost:8080/tasks/expire-leases
```

### Health check

```sh
curl -s localhost:8080/healthz
```

## Integration

This service **consumes** `WorkReleased` events published by `wes-work-planning`
and turns each one into a Task via the existing `CreateTask` use case — this
is the intended use of that use case, so the Kafka consumer
(`internal/adapters/inbound/kafka`) calls it directly rather than going
through a new one. It also **publishes** `TaskCompleted` back to Work
Planning to close the control loop (drum-buffer-rope feedback edge:
Execution -> Orchestration).

- **Consumed topic**: `warehouse.work-planning.events` (consumer group `fulfillment-execution`)
- **Published topic**: `warehouse.fulfillment.events`
- **Broker**: `KAFKA_BROKERS` env var, default `localhost:9092`. This connects
  to the shared broker started via `~/warehouse-systems/docker-compose.kafka.yml`;
  this repo's own `docker-compose.yml` does not run Kafka.
- **Client library**: `github.com/segmentio/kafka-go`.

### Envelope

Identical across all four warehouse-systems services:

```json
{
  "event_id": "uuid-v4",
  "event_type": "WorkReleased",
  "occurred_at": "2026-08-21T22:00:00Z",
  "source": "wes-work-planning",
  "data": {"path_id": "...", "work_unit_id": "...", "cpt": "RFC3339", "ref": "...", "fragile": false}
}
```

Messages whose `event_type` isn't `"WorkReleased"` are ignored. `data.fragile`
is optional — see the Mapping section below.

### Mapping (known simplification)

`path_id` does not carry the task type in general. This integration assumes,
as a simplification for this round, that `path_id` carries the task type as a
string prefix: `"pick-*"` → Pick, `"pack-*"` → Pack, `"slam-*"` → SLAM;
anything else defaults to **Pick**. The rest of the mapping:

| WorkReleased field     | Task field                                          |
|-------------------------|------------------------------------------------------|
| `data.path_id` (prefix) | task type (Pick/Pack/SLAM, default Pick)              |
| `data.work_unit_id`     | `ref`                                                 |
| `data.cpt`               | `cpt`                                                 |
| `data.fragile` (optional, default `false`) | `fragile` — a packing hint, see below |
| task type                | required capabilities (`pick`, `pack`, or `slam`)     |

**`data.fragile` is optional** (another known simplification, same shape as
the `path_id`-prefix rule above): it is sourced from
`inventory-storage`'s `ProductClassification` concept and stamped by
`wes-work-planning` at release time, but any already-documented producer that
predates this field simply omits it, and the consumer defaults to `false`
rather than rejecting the message. When present and `true`, it means the
order line was classified Fragile upstream; this service threads it straight
into the created `Task.Fragile` flag (no interpretation), and later derives
`Package.FragileHandling` from it in `SealPackage` when the Pack task's
contents are sealed. It does not affect claiming — a fragile task is claimed
by capability match exactly like any other. It also has nothing to do with
hazmat: hazmat is a `requiredCapabilities` value (`hazmat`), matched by the
existing generic capability-set mechanism in `Task.Claim` / `RegisterStation`
— no code change was needed there, only documentation (see
`docs/docs/adr/0009-fragile-and-hazmat-handling-flags.md`).

### Idempotency

Kafka delivery is at-least-once. Before calling `CreateTask`, the consumer
tries to record the event's `event_id` in a `processed_events` table
(Postgres) or an in-memory set (no `DATABASE_URL`); if the id is already
present, the message is skipped (and still acked/committed) instead of
creating a duplicate Task. See
`internal/adapters/inbound/kafka/consumer_test.go` —
`TestHandleMessage_DoubleDeliveryCreatesExactlyOneTask`.

### Smoke test

With the shared broker running (`docker compose -f ~/warehouse-systems/docker-compose.kafka.yml up -d`)
and this service running (`go run ./cmd/execution`):

```sh
docker exec -i warehouse-kafka kafka-console-producer.sh \
  --broker-list localhost:9092 --topic warehouse.work-planning.events <<'EOF'
{"event_id":"smoke-1","event_type":"WorkReleased","occurred_at":"2026-08-21T22:00:00Z","source":"wes-work-planning","data":{"path_id":"pick-smoke","work_unit_id":"wu-smoke-1","cpt":"2026-08-21T23:00:00Z","ref":"release-smoke"}}
EOF

curl -s localhost:8080/queues/PICK/depth
```

`depth` should have increased by 1.

### Publishing TaskCompleted

When `EVENT_PUBLISHER=kafka`, the outbound adapter
(`internal/adapters/outbound/kafka`) publishes a `TaskCompleted` message to
`warehouse.fulfillment.events` every time the `CompleteTask` use case
succeeds. `CompleteTask` itself is unchanged — it still just calls
`Publisher.Publish` with the domain event it always raised; only which
`ports.EventPublisher` implementation is wired in at startup changes.

The domain event `TaskCompleted` carries only `TaskId`/`StationId`. Work
Planning's downstream `RecordCompletion` use case needs the original
`work_unit_id` (the Task's `OrderRef`, set from `WorkReleased.data.work_unit_id`
at creation time — see the consumer mapping above), so the publisher adapter
looks the Task back up via `TaskRepo` before publishing and enriches the
envelope with it. This mirrors the same repo-lookup-enrichment pattern
inventory-storage's Kafka publisher uses for `ReservationRevoked`.

#### Envelope

```json
{
  "event_id": "uuid-v4",
  "event_type": "TaskCompleted",
  "occurred_at": "2026-08-21T22:00:00Z",
  "source": "fulfillment-execution",
  "data": {"task_id": "...", "station_id": "...", "work_unit_id": "..."}
}
```

Downstream: `wes-work-planning` calls its `RecordCompletion` use case with
`WorkUnitId = data.work_unit_id`.

#### Smoke test

With the shared broker running (`docker compose -f ~/warehouse-systems/docker-compose.kafka.yml up -d`)
and this service running with Kafka publishing enabled:

```sh
EVENT_PUBLISHER=kafka go run ./cmd/execution
```

In another terminal, start a consumer on the published topic, then drive a
task through the full lifecycle over HTTP:

```sh
docker exec -i warehouse-kafka kafka-console-consumer.sh \
  --bootstrap-server localhost:9092 --topic warehouse.fulfillment.events --from-beginning &

curl -sX POST localhost:8080/tasks -H 'Content-Type: application/json' \
  -d '{"type":"PICK","cpt":"2026-01-01T18:00:00Z","orderRef":"order-smoke-1","requiredCapabilities":["pick"]}'

curl -sX POST localhost:8080/stations/station-smoke/claim-next -H 'Content-Type: application/json' \
  -d '{"taskType":"PICK"}'
# note the "id" field of the returned task as TASK_ID

curl -sX POST localhost:8080/tasks/TASK_ID/complete -H 'Content-Type: application/json' \
  -d '{"stationId":"station-smoke"}'
```

The consumer should print a `TaskCompleted` message whose `data.work_unit_id`
is `"order-smoke-1"`.

## Invariants covered by failing-path tests

- **At-most-once claim**: `internal/domain/task/task_test.go` —
  `TestClaim_RejectsSecondClaimWhileLeaseActive`; also exercised end-to-end in
  `internal/application/usecases/usecases_test.go` —
  `TestClaimNext_AtMostOnce_SecondStationCannotClaimSameTask`.
- **Capability-mismatch rejection**: `internal/domain/task/task_test.go` —
  `TestClaim_RejectsCapabilityMismatch`; `internal/domain/station/station_test.go` —
  `TestValidateAccept_RejectsCapabilityMismatch`.
- **Lease-expiry frees the task**: `internal/domain/task/task_test.go` —
  `TestClaim_ExpiredLeaseFreesTaskForNewClaim`; also
  `internal/application/usecases/usecases_test.go` —
  `TestClaimNext_ExpiredLeaseReturnsTaskToPool` and
  `TestExpireLeases_SweepsExpiredClaimsBackToPending`, both driven by a
  `FixedClock`.
- **SLAM weight-diversion**: `internal/domain/package/package_test.go` —
  `TestWeigh_DivertsOutsideTolerance`.
