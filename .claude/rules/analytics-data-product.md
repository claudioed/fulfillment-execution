# Analytics Data Product (ADR-0012)

Additive read side built from this service's OWN domain events. The OLTP
domain/application layers are NOT modified and must NOT import the analytics
store (arch-test enforces this).

- `internal/analytics/report/` depends on nothing.
- Events are fanned to a SEPARATE topic `warehouse.fulfillment.analytics` by
  a dedicated outbound adapter (`outbound/kafka/analytics_publisher.go`). The
  integration topic `warehouse.fulfillment.events` and its publisher are
  untouched by this. Task-scoped events are enriched with `task_type` via a
  TaskRepo lookup (domain events stay thin).
- SEPARATE analytical Postgres (`ANALYTICS_DATABASE_URL`), own migrations
  (`migrations/analytics/`), read-only role for the reader.
- Three processes:
  - `cmd/execution` (OLTP)
  - `cmd/fulfillment-projector` — the ONLY writer of the analytical DB;
    consumes the analytics topic from FirstOffset, idempotent on `event_id`
  - `cmd/fulfillment-reports` — read-only reader
- Report: Throughput & Lease-Health, keyed task-type × station × hour.
  - `GET /reports/throughput?from&to&taskType&stationId&granularity` (reports binary)
  - `GET /reports/throughput/freshness` (lag vs real time)
  - MCP tool `get_fulfillment_throughput_report` (calls the reports REST;
    never opens the analytical DB directly)

## Standard metrics convention (ADR-0019)

Tier 1 baseline + Tier 2 naming convention applied fleet-wide; see
`internal/observability/metrics.go` and `httpmetrics.go` for this service's
implementation, and ADR-0019 for the naming rules to follow when adding a
new metric.
