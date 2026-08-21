# Build Tasks — Fulfillment Execution

Build the full bounded context described in CLAUDE.md, in order. Keep
`go build ./...` and `go test ./...` green throughout. Read
/Users/claudioed/docs/amazon-fulfillment-ddd.md for the domain model first.

## Task 0 — Skeleton
- `go mod init github.com/claudioed/fulfillment-execution`; create the layout;
  .gitignore (bin/, .env); add chi + pgx deps.

## Task 1 — Domain (pure Go)
- shared: TaskId, StationId, CPT, Capability value objects; DomainEvent + 9 events.
- task: Task aggregate (Pick|Pack|SLAM; Pending->Claimed(lease)->Completed;
  at-most-once claim; capability match; lease expiry frees task).
- station: Station aggregate (one occupant; capability set).
- package: Package aggregate (seal needs scanned contents; SLAM weight tolerance).
- Unit tests for EVERY invariant incl. failing paths.

## Task 2 — Application
- ports: TaskRepo, StationRepo, PackageRepo, EventPublisher, Clock.
- usecases: the 8 use cases from CLAUDE.md. PULL dispatch: ClaimNext selects the
  earliest-CPT pending task matching capabilities and leases it. Depend only on
  domain + ports.
- Unit-test against in-memory adapter, including lease-expiry via a FixedClock.

## Task 3 — Outbound adapters
- memory: thread-safe impls of every port.
- postgres: pgxpool repos + migrations (task, station, package, events);
  build-tagged integration test (skip w/o DATABASE_URL).
- events: log/buffered publisher, kafka-ready interface.

## Task 4 — Inbound HTTP
- chi router + handlers for every endpoint, DTOs, domain-error->HTTP mapping, /healthz.
- httptest per endpoint against in-memory repos.

## Task 5 — Composition & ops
- cmd/execution/main.go wires env -> adapters -> use cases -> router.
- docker-compose.yml (pg16); README.md with run steps + curl examples.

## Task 6 — Verify
- build, vet, test (and `-race`) all green; gofmt clean. Confirm the four named
  invariants each have a red-path test. Do not stop until DoD in CLAUDE.md is met.
