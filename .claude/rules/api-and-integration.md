# API Surface & Cross-Service Integration

## REST API (inbound adapter) — 14 operations, `apis/openapi.yaml`

- POST /tasks                                 -> CreateTask
- GET  /tasks?orderRef=                       -> GetTasksByOrderRef
- POST /stations                              -> RegisterStation
- POST /stations/{stationId}/claim-next       -> ClaimNext
- POST /stations/{stationId}/check-in         -> CheckInStation
- POST /stations/{stationId}/check-out        -> CheckOutStation
- POST /tasks/{id}/renew-lease                -> RenewLease
- POST /tasks/{id}/complete                   -> CompleteTask
- POST /tasks/{id}/seal-package                -> SealPackage
- POST /packages/{id}/slam                    -> RunSlam
- GET  /queues/{taskType}/depth               -> GetQueueDepth
- GET  /capacity/{capability}                 -> GetInstalledCapacity (ADR-0018)
- POST /tasks/expire-leases                   -> ExpireLeases
- GET  /healthz

JSON DTOs live in the http adapter; never leak domain structs. Errors are
RFC 7807 `application/problem+json` (ADR-0005).

## The `orderRef` cross-service contract

`GET /tasks?orderRef=` is the read side backing the fleet's cross-service
Order Lifecycle console screen — see ADR-0002 in `warehouse-ops-agent`'s docs
and this repo's own adoption-record ADR-0013. The `orderRef` query param is
**not** order-management's plain order id — it is wes-work-planning's own
per-line WorkUnit id (`<orderId>-line-<lineNo>`), because `Task.OrderRef` is
stamped from the `WorkReleased` Kafka payload's `work_unit_id` field, not
from any order id directly. Callers needing "every task for order X" must
first resolve that order's WorkUnit ids via wes-work-planning's
`GET /work-units?reference=`, then call this endpoint once per WorkUnit (the
console-bff does exactly this). Returns every task for that WorkUnit id
including retried legs, array-shaped, side-effect-free.

## CORS (ADR-0013)

`go-chi/cors` middleware is enabled on every route, allowing
`CORS_ALLOWED_ORIGINS` (env, default
`http://localhost:5173,http://localhost:5184` — the `warehouse-console` shell
and this service's own `fulfillment-mfe` remote).

## Auth (fleet REST identity — currently REMOVED)

ADR-0021 introduced static-bearer-key REST identity with read/read-write
scopes across the fleet; ADR-0022 **removed** the REST + MCP static-bearer
auth layer from this service again (chore/remove-security-layer, merged to
develop). Check `docs/docs/adr/0022-remove-rest-mcp-auth.md` for current
status before assuming any bearer-auth requirement is live — do not
resurrect ADR-0021's design without re-checking this.

## Events published (AsyncAPI: `apis/asyncapi.yaml`)

CloudEvents 1.0 structured envelope; `type` = 
`com.warehouse.<subdomain>.<bounded-context>.<entity>.<Event>`, e.g.
`com.warehouse.wes.fulfillment-execution.task.TaskClaimed`. Channel:
`warehouse.fulfillment-execution.events` (per the spec — see divergence note
below for what's actually on the wire).

| Event | `type` suffix | `data` fields | On the wire today? |
| --- | --- | --- | --- |
| `TaskCreated` | `task.TaskCreated` | `taskId` | No |
| `TaskClaimed` | `task.TaskClaimed` | `taskId`, `stationId` | No |
| `LeaseExpired` | `task.LeaseExpired` | `taskId` | No |
| **`TaskCompleted`** | `task.TaskCompleted` | `taskId`, `stationId`, `workUnitId`, `associateId`, `durationSeconds` | **Yes** |
| `ItemPicked` | `task.ItemPicked` | `taskId` | No — not raised by any use case either |
| `PackageSealed` | `package.PackageSealed` | `packageId` | No |
| `WeightDiscrepancyDetected` | `package.WeightDiscrepancyDetected` | `packageId`, `expectedWeight`, `actualWeight` | No |
| `LabelApplied` | `package.LabelApplied` | `packageId` | No |
| `PackageDiverted` | `package.PackageDiverted` | `packageId` | No |

`workUnitId` on `TaskCompleted` is what makes the feedback loop to Work
Planning work: the publisher looks the task back up through `ports.TaskRepo`
and reads `OrderRef()` (populated from `WorkReleased.data.work_unit_id` at
creation), so Work Planning gets back exactly the id it sent and can call
`RecordCompletion(workUnitId)`. `associateId`/`durationSeconds` are for the
labor-performance context (ADR-0014); both are soft/optional (omitted when
unavailable — e.g. a robot station never checks anyone in).

## Events consumed

| Source context | Topic | `event_type` | Effect here |
| --- | --- | --- | --- |
| `wes-work-planning` | `warehouse.work-planning.events` | `WorkReleased` | Creates a `Task` via `CreateTask` |

`WorkReleased` arrives in the flat platform envelope (not CloudEvents); the
Anti-Corruption Layer maps `data.path_id` -> `task.Type` via the process-path
catalogue (ADR-0017: `pick*`->PICK, `pack*`->PACK, `slam*`->SLAM, `rebin*`->
REBIN by prefix match, not exact match — a real path id looks like
`pick-zone-a`, not bare `pick`). Idempotent via `ProcessedEvents.MarkProcessed`
(Postgres primary-key-backed dedup; in-memory adapter uses a mutex map).
Consumer group: `fulfillment-execution`.

## KNOWN DIVERGENCE: AsyncAPI spec vs. actual wire format

Documented (not hidden) in `docs/docs/api-reference/events.md`. The
**publisher today still uses the flat platform envelope**, not the
CloudEvents shape the spec describes:

| | `apis/asyncapi.yaml` (target) | `internal/adapters/outbound/kafka/publisher.go` (today) |
| --- | --- | --- |
| Channel/topic | `warehouse.fulfillment-execution.events` | `warehouse.fulfillment.events` |
| Envelope | CloudEvents 1.0 structured | flat platform envelope |
| Type field | `type: com.warehouse...TaskCompleted` | `event_type: "TaskCompleted"` |
| Id field | `id` | `event_id` |
| Timestamp | `time` | `occurred_at` |
| Source | `/warehouse/fulfillment-execution` | `fulfillment-execution` |
| Payload keys | `taskId`, `stationId`, `workUnitId` | `task_id`, `station_id`, `work_unit_id` |

The live integration with `wes-work-planning` works correctly against the
flat envelope on `warehouse.fulfillment.events` — it is the AsyncAPI document
describing the *intended future* contract, not the current wire format.
Migrating the publisher to CloudEvents is outstanding work; both producer and
consumer sides would move together since the topic name also changes.
**This is narrative/documentation staleness only — no code fix needed**, just
keep the divergence note current if either side changes.

## Transactional outbox (ADR-0020)

`internal/adapters/outbound/postgres/outbox_publisher.go` +
`outbox_relay.go` feed both the integration topic and the analytics topic
from one outbox table with an in-process relay — this repo is the fleet's
REFERENCE implementation for the outbox pattern (see
`process-path-management`'s PR #7 for the original, and the fleet skill's
`transactional-outbox-rollout.md` for the multi-service rollout brief).
