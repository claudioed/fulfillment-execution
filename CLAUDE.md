# Project: Fulfillment Execution (Core Bounded Context)

Turns released work into completed physical operations: the **task lifecycle**
for Pick, Pack, SLAM, and Rebin. Downstream of Work Planning (which releases
work); issues commands to WCS/equipment (seam only — no WCS tier exists yet).
Defining design rule: **pull, not push** — a station claims the next task
(`claimNext(stationId, capabilities)`); the system selects work, not workers.
Assignment is at-most-once with a **lease**, so an unconfirmed task returns to
the pool rather than vanishing.

Source of truth for the ubiquitous language: `/Users/claudioed/docs/amazon-fulfillment-ddd.md`
and `/Users/claudioed/warehouse-systems-ddd.md`. Honor those exact names.

**Study project**, not production: real DDD/hexagonal patterns, not affiliated
with any company (see README.md banner).

## Project Overview

- Module: `github.com/claudioed/fulfillment-execution`, Go 1.26.
- Three long-running processes + one MCP server (see Architecture below).
- REST inbound API, Kafka inbound consumer (`WorkReleased`), Postgres storage,
  Kafka outbound integration + analytics events.
- Also owns a browser micro-frontend remote, `web/` (`fulfillment-mfe`) —
  entirely separate build, not part of the Go module or its quality gate.
- Full docs site (generated OpenAPI reference, hand-written AsyncAPI/Events
  page, DDD artifacts, ADRs): <https://claudioed.github.io/fulfillment-execution/>,
  source in `docs/`, deploy workflow `.github/workflows/docs.yml`.

## Architecture (NON-NEGOTIABLE)

Hexagonal / Ports & Adapters. Strict dependency rule: **domain depends on
nothing; application depends on domain; adapters depend on
application/domain.** No framework or SQL types in the domain layer
(enforced by `make arch-test`, ADR-0006).

```
cmd/execution/               main.go — OLTP composition root
cmd/fulfillment-projector/   analytics WRITER: analytics topic -> analytical DB
cmd/fulfillment-reports/     analytics READ-ONLY READER: serves GET /reports/...
cmd/mcp/                     MCP server (adds the report tool)
internal/
  domain/
    task/                    Task aggregate (Pick|Pack|SLAM|Rebin lifecycle, lease)
    station/                 Station aggregate (occupant, capabilities)
    package/                 Package aggregate (pack -> sealed; SLAM weigh-check; segregation)
    consolidation/           OrderConsolidation — Rebin fan-in tracker (execution-scoped only)
    pathcatalog/             Process-path catalogue model (prefix-match lookup, ADR-0017)
    shared/                  value objects: TaskId, StationId, CPT, Capability, events
  analytics/report/          analytical read model + store ports (ADR-0012)
  application/
    ports/                   OUT: TaskRepo, StationRepo, PackageRepo, EventPublisher,
                              Clock, ProductClassificationLookup, EquipmentCommandPort (ACL seam)
    usecases/                one struct per use case
  adapters/
    inbound/http/            chi handlers, DTOs, error mapping (OLTP + reports)
    inbound/kafka/           WorkReleased consumer + analytics projector consumer
    inbound/mcp/             MCP tools (incl. get_fulfillment_throughput_report)
    outbound/postgres/       pgxpool repos + migrations + transactional outbox (ADR-0020)
    outbound/analyticsstore/ analytical DB writer + read-only reader
    outbound/memory/         in-memory repos for tests/local
    outbound/kafka/          integration publisher + analytics publisher
    outbound/kafkacatalog/   process-path catalogue Kafka-config consumer
    outbound/filecatalog/    process-path catalogue file-config loader
    outbound/productclassification/ live per-SKU DOT hazard lookup client + permissive fallback
    outbound/events/         log/buffered/multi publisher
migrations/                  golang-migrate SQL files
migrations/analytics/        analytical schema migrations
web/                         fulfillment-mfe: Vite + React Module Federation remote (see rules/frontend-mfe.md)
charts/fulfillment-execution/ Helm chart (deployed by warehouse-infra's local.services map)
```

Full domain vocabulary, aggregate invariants, domain events, and use-case list
are in `.claude/rules/ubiquitous-language.md` — load it before touching
`internal/domain/` or `internal/application/usecases/`.

## Key Commands

```bash
make check       # fast pre-commit gate: fmt-check vet build lint test
make check-all   # check + coverage(90%) + arch-test + bdd — run before pushing
make vuln        # govulncheck; run when touching go.mod/go.sum
make mutation-fast  # blocking gremlins subset; thresholds in .gremlins.yaml
make integration    # needs DATABASE_URL + running Postgres; excluded from check
lefthook install    # one-time: activates pre-commit/pre-push hooks
```

```bash
# Docs site (Docusaurus) — see rules/docs-and-api-drift.md for the full
# regeneration procedure and why it is NOT wired into `npm run build`.
cd docs && npm ci
npx docusaurus gen-api-docs fulfillment   # regenerate REST reference from apis/openapi.yaml
npm run build                              # verify no broken links before pushing
```

```bash
docker-compose up -d          # local Postgres 16 (this repo)
# Shared Kafka: ~/warehouse-systems/docker-compose.kafka.yml (fleet-wide, one broker)
go run ./cmd/execution         # OLTP API
go run ./cmd/fulfillment-projector  # analytics writer
go run ./cmd/fulfillment-reports    # analytics reader API
go run ./cmd/mcp                    # MCP server
```

## REST API surface

14 operations across Tasks / Stations / Packages / System. Full endpoint list,
the `orderRef` cross-service contract, and CORS config are in
`.claude/rules/api-and-integration.md`. Spec: `apis/openapi.yaml`. Generated
docs: `docs/docs/api-reference/rest/*.api.mdx` — **must be regenerated by hand**
after any `apis/openapi.yaml` change (see that rule file).

## Code Standards / Testing

- Go 1.26, modules; chi (`go-chi/chi/v5`); pgx/v5 + pgxpool; golang-migrate.
- Config via env (`DATABASE_URL`, `HTTP_ADDR`, `ANALYTICS_DATABASE_URL`, mode
  flags like `PRODUCT_CLASSIFICATION_MODE=http|permissive`).
- Typed domain errors mapped to HTTP status (RFC 7807 `application/problem+json`,
  ADR-0005) in the adapter layer only.
- Table-driven tests: domain + application (in-memory adapter); one httptest
  per endpoint; build-tagged Postgres integration test (`-tags=integration`,
  skipped without `DATABASE_URL`; Kafka integration tests use testcontainers,
  never a shared external broker).
- gofmt/go vet clean; every package has a doc comment.
- Definition of done: `go build ./...`, `go vet ./...`, `go test ./...` green;
  README run steps current; failing-path tests exist for at-most-once claim,
  capability-mismatch rejection, lease-expiry, SLAM weight-diversion, and
  package segregation rejection.

## Further reading (`.claude/rules/`)

- `ubiquitous-language.md` — full glossary, aggregates & invariants, domain
  events, use cases (the DDD core — read before any domain-layer change).
- `api-and-integration.md` — full REST endpoint table, the `orderRef`
  cross-service contract, CORS, events consumed/published, the known
  AsyncAPI-spec-vs-wire divergence.
- `analytics-data-product.md` — ADR-0012 analytics topic/DB/report design.
- `frontend-mfe.md` — `web/` fulfillment-mfe micro-frontend remote.
- `docs-and-api-drift.md` — how the Docusaurus docs site is built, why
  OpenAPI reference regeneration is a manual step, and the drift-check
  procedure for `apis/openapi.yaml` / `apis/asyncapi.yaml`.
- ADRs: `docs/docs/adr/0001` through `0022` — read the index at
  `docs/docs/adr/index.md` before assuming a decision is undocumented.
