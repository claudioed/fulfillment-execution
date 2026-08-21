# Fulfillment Execution

The task-lifecycle core bounded context for Pick, Pack, and SLAM. Downstream
of Work Planning (which releases work); issues commands to WCS/equipment.
Dispatch is **pull, not push**: a station calls `claimNext(stationId,
capabilities)` and the system selects the best-fit pending task — the system
never names a station in advance. Claims are at-most-once and time-boxed by a
**lease**: an unconfirmed claim expires back to the pool rather than
vanishing.

See `CLAUDE.md` for the full architecture and ubiquitous language, and
`/Users/claudioed/docs/amazon-fulfillment-ddd.md` for the source domain model.

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

## API

All endpoints accept/return JSON.

### Create a task (put work in the pool)

```sh
curl -sX POST localhost:8080/tasks \
  -H 'Content-Type: application/json' \
  -d '{
        "type": "PICK",
        "cpt": "2026-01-01T18:00:00Z",
        "orderRef": "order-42",
        "requiredCapabilities": ["pick"]
      }'
```

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
curl -sX POST localhost:8080/admin/expire-leases
```

### Health check

```sh
curl -s localhost:8080/healthz
```

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
