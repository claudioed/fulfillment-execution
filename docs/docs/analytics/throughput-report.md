---
id: throughput-report
title: Throughput & Lease-Health Report
sidebar_label: Throughput report
description: The fulfillment-execution analytical data product — a Throughput & Lease-Health read model built from the service's own domain events, served read-only over REST and MCP. Contract, grain, inputs, freshness SLA, and versioning.
---

# Throughput & Lease-Health Report

The analytical **data product** owned by Fulfillment Execution. It is built
entirely from this service's own domain events (never another service's
database) and served read-only. See [ADR-0012](../adr/0012-analytical-data-product.md)
for the decision and the `warehouse-infra/docs/analytics/` Envelope v1 contract
and governance charter for the cross-service rules.

## Name & owner

- **Report:** Throughput & Lease-Health.
- **Owner:** the Fulfillment Execution service/team (the same team that owns the
  OLTP write model).

## Grain

One row per **(task type × station × hour bucket)**, where `hourBucket` is the
UTC hour the row aggregates. Metrics per row:

| Metric | Meaning |
|---|---|
| `completions` | Count of `TaskCompleted` in the bucket. |
| `avgClaimToCompleteSeconds` | Mean seconds from a task's claim to its completion, over completions that had a recorded claim. |
| `leaseExpiries` | Count of `LeaseExpired` (unconfirmed claims that timed out back to the pool). |
| `weighCheckDiverts` | Count of `WeightDiscrepancyDetected` (SLAM weigh-check diversions). |

## Inputs (analytics topic events)

Consumed from **`warehouse.fulfillment.analytics`** (the dedicated analytics
topic, separate from the integration topic — Envelope v1):

| `event_type` | Contributes |
|---|---|
| `TaskClaimed` | claim timestamp (for claim→complete latency) |
| `TaskCompleted` | `completions`, claim→complete latency |
| `LeaseExpired` | `leaseExpiries` |
| `WeightDiscrepancyDetected` | `weighCheckDiverts` |

`task_type` is enriched onto task-scoped events by the publisher via a `TaskRepo`
lookup (the domain events themselves stay thin). `TaskCreated`, `ItemPicked`,
`PackageSealed`, `LabelApplied`, and `PackageDiverted` are published to the topic
but do not currently move this report; the projector acknowledges them without
projecting.

## Interface

### REST (served by `cmd/fulfillment-reports`, read-only)

```
GET /reports/throughput?from=<RFC3339>&to=<RFC3339>&taskType=&stationId=&granularity=hour
GET /reports/throughput/freshness
GET /healthz
```

- `from`, `to` — **required**, RFC3339, `[from, to)` compared against `hourBucket`.
- `taskType`, `stationId` — optional exact-match filters.
- `granularity` — optional, defaults to `hour`.

Response (`200`):

```json
{
  "rows": [
    {
      "taskType": "PICK",
      "stationId": "S-7",
      "hourBucket": "2026-08-26T14:00:00Z",
      "completions": 42,
      "avgClaimToCompleteSeconds": 73.5,
      "leaseExpiries": 1,
      "weighCheckDiverts": 0
    }
  ]
}
```

Freshness (`200`):

```json
{ "lagSeconds": 4.2 }
```

Errors use RFC 7807 `application/problem+json`, consistent with the OLTP API
([ADR-0005](../adr/0005-rfc-7807-problem-details.md)).

### MCP (curated, read-only)

Tool **`get_fulfillment_throughput_report`** — same filters as the REST endpoint;
it calls the reports REST rather than opening the analytical database. Exposed by
the existing `cmd/mcp` server (Streamable HTTP), consistent with
[ADR-0008](../adr/0008-mcp-inbound-adapter.md).

## Freshness SLA

- **Definition:** `lagSeconds` = now − age of the most recently applied event.
- **Target:** p95 event-to-report lag **< 30s** under normal load.
- **Exposed:** `GET /reports/throughput/freshness`.
- Breaching the SLA is an operational signal (projector lag / consumer down), not
  a correctness bug — the report catches up when the projector does.

## Versioning

- Additive fields (new optional row metric, new query filter) are non-breaking.
- A breaking change to a row's shape or meaning is a new endpoint/tool version.
- The analytics event contract versions independently via the Envelope
  `schema_version` and the analytics topic suffix (see Envelope v1).

## Runbook notes

- **Two processes, one writer.** `cmd/fulfillment-projector` is the only writer of
  the analytical DB; `cmd/fulfillment-reports` connects read-only. The OLTP
  `cmd/execution` never opens the analytical DB.
- **Empty on first deploy.** The report is empty until events flow. To backfill
  history, replay `warehouse.fulfillment.analytics` from earliest into a fresh
  projector; Kafka retention must cover the desired window.
- **Eventual consistency.** The report is a projection, not a real-time view; it
  meets the freshness SLA, not transactional consistency.
