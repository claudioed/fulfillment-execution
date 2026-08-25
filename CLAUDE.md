# Project: Fulfillment Execution (Core Bounded Context)

Turns released work into completed physical operations: the **task lifecycle**
for Pick, Pack, and SLAM. It is downstream of Work Planning (which releases work)
and issues commands to WCS/equipment. The defining design rule is **pull, not
push**: a station claims the next task (`claimNext(stationId, capabilities)`);
the system selects work, not workers. Assignment is at-most-once with a **lease**
so an unconfirmed task returns to the pool rather than vanishing.

Source of truth: `/Users/claudioed/docs/amazon-fulfillment-ddd.md` and
`/Users/claudioed/warehouse-systems-ddd.md`. Honor that ubiquitous language.

## Architecture (NON-NEGOTIABLE)

Hexagonal / Ports & Adapters. Strict dependency rule: **domain depends on
nothing; application depends on domain; adapters depend on application/domain.**
No framework or SQL types in the domain layer.

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

## Ubiquitous Language (use these exact names)

- **Task** — a unit of physical work: type Pick | Pack | SLAM. States:
  Pending -> Claimed(leased) -> Completed, or lease-expires back to Pending.
  Carries CPT (priority derives from it). At most one active claim.
- **claimNext(stationId, capabilities)** — PULL dispatch. Returns the highest-
  priority (earliest CPT) pending task the station is certified/equipped for.
  Never name a person/station in advance (no push assign(task, station)).
- **Lease** — a claim has a timeout; if not confirmed/completed before expiry the
  task returns to the pool (prevents vanished work). Lease renewal is allowed.
- **Station** — a work position with a capability set; one occupant at a time;
  capabilities must match the task handed to it.
- **Package** — pack output: an order becomes a sealed carton. **SLAM weigh-check**:
  actual weight must be within tolerance of expected, else the package is diverted.
- **Process path** — Pick / Pack / SLAM as named task types (queues), not steps.
- **Fragile (packing hint)** — `Task.Fragile()` and the derived `Package.FragileHandling()`.
  Stamped onto the Task by `wes-work-planning` at release time (from
  `inventory-storage`'s `ProductClassification`, read once upstream — this
  service never calls inventory-storage directly). `SealPackage` derives
  `FragileHandling` from the owning task's flag, not a separate caller input.
  Affects packing/downstream sortation only — it does NOT gate claiming.
- **Hazmat (station capability)** — `"hazmat"` is a real, known value of the
  existing open `Capability`/`CapabilitySet` type (see `claimNext` above).
  A Task requiring hazmat handling sets it in `requiredCapabilities`; only a
  Station registered with that capability can claim it. This already worked
  via the pre-existing generic capability-matching mechanism — no structural
  change was needed to support it (ADR-0009).

## Aggregates & invariants (enforce in domain, unit-tested)

- **Task**: at most one active claim (at-most-once); no double-complete; a claim
  requires matching capabilities; an expired lease frees the task.
- **Station**: one occupant at a time; claim rejected if capability mismatch.
- **Package**: cannot seal without scanned contents; SLAM diverts when
  |actual-expected| weight > tolerance.
- Read models (queue depth by task type, throughput) are PROJECTIONS from events.

## Domain events (past tense)

TaskCreated, TaskClaimed, LeaseExpired, TaskCompleted, ItemPicked, PackageSealed,
WeightDiscrepancyDetected, LabelApplied, PackageDiverted.

## Use cases (application layer)

1. CreateTask(type, cpt, ref, requiredCapabilities, fragile) -> Task in pool
2. ClaimNext(stationId, capabilities) -> leases + returns best-fit pending task
3. RenewLease(taskId, stationId) -> extends lease
4. CompleteTask(taskId, stationId) -> TaskCompleted (validates claim ownership)
5. SealPackage(taskId, contents) -> Package (Pack path); FragileHandling
   derived from the task's Fragile flag
6. RunSlam(packageId, actualWeight, expectedWeight) -> LabelApplied or Diverted
7. GetQueueDepth(taskType) -> read model
8. ExpireLeases(now) -> sweeps expired claims back to Pending (Clock-driven)

## REST API (inbound adapter)

- POST /tasks                                 -> CreateTask
- POST /stations                              -> RegisterStation
- POST /stations/{stationId}/claim-next       -> ClaimNext
- POST /tasks/{id}/renew-lease                -> RenewLease
- POST /tasks/{id}/complete                   -> CompleteTask
- POST /tasks/{id}/seal-package               -> SealPackage
- POST /packages/{id}/slam                    -> RunSlam
- GET  /queues/{taskType}/depth               -> GetQueueDepth
- POST /tasks/expire-leases                   -> ExpireLeases
- GET  /healthz

JSON DTOs live in the http adapter; never leak domain structs.

## Tech & standards

- Go 1.26, modules. Module path: `github.com/claudioed/fulfillment-execution`.
- chi (github.com/go-chi/chi/v5), pgx/v5 + pgxpool, golang-migrate SQL migrations.
- Config via env (DATABASE_URL, HTTP_ADDR). docker-compose.yml for Postgres 16.
- Typed domain errors mapped to HTTP status in the adapter.
- Table-driven tests: domain + application (in-memory adapter); one httptest per
  endpoint; build-tagged Postgres integration test (skipped w/o DATABASE_URL).
- gofmt/go vet clean; every package has a doc comment.

## Definition of done

- `go build ./...`, `go vet ./...`, `go test ./...` all green.
- README.md: run steps (compose/migrate/go run), endpoints w/ curl, layering note.
- These invariants each have a failing-path test: at-most-once claim,
  capability-mismatch rejection, lease-expiry frees task, SLAM weight-diversion.

## Local quality gate (run before every commit)

- Run `make check` after making changes and **before committing**. It is the
  fast self-correction loop: `fmt-check`, `vet`, `build`, `lint`, `test`.
- Run `make check-all` before pushing for the fuller gate — `check` plus the
  90% `coverage` gate, `arch-test`, and `bdd`.
- Run `make vuln` (govulncheck) when touching `go.mod`/`go.sum`; a new CVE in
  the dependency graph blocks CI.
- `make mutation-fast` reruns the blocking mutation subset locally; thresholds
  live in `.gremlins.yaml`. `make integration` needs `DATABASE_URL` and a
  running Postgres, so it is deliberately outside `check`.
- Git hooks (lefthook, `lefthook.yml`) enforce this automatically once someone
  has run `lefthook install` on the machine — but run `make check` proactively
  rather than relying on the hook firing.
- Why: this keeps quality *left*. Every command above is the same sensor CI
  runs, moved to where an agent can still self-correct, so problems are caught
  before they reach a human or the pipeline.
