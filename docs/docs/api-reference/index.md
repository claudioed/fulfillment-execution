---
id: index
title: API Reference
sidebar_label: Overview
sidebar_position: 1
slug: /api-reference/
description: All ten REST endpoints and the event contract — conventions, status-code mapping, and RFC 7807 error shapes.
---

# API Reference

This service exposes **two contracts**, both of which live in `apis/` in the
repository and are linted by Spectral on every push:

| Contract | File | Rendered here |
| --- | --- | --- |
| Synchronous REST | `apis/openapi.yaml` (OpenAPI 3.0.3) | [REST API](./rest/fulfillment-execution-api.info.mdx) — generated directly from the spec |
| Asynchronous events | `apis/asyncapi.yaml` (AsyncAPI 2.6.0) | [Events](./events.md) — hand-authored from the spec |

The REST pages under **REST API** are generated from the real spec file at
build time by `docusaurus-plugin-openapi-docs`. They are not transcribed by
hand, so they cannot drift from the contract that CI validates.

## Endpoint coverage: 10 / 10

Cross-checked against `internal/adapters/inbound/http/router.go`:

| Method | Path | Use case | Tag |
| --- | --- | --- | --- |
| `POST` | `/tasks` | `CreateTask` | Tasks |
| `POST` | `/stations` | `RegisterStation` | Stations |
| `POST` | `/stations/{stationId}/claim-next` | `ClaimNext` | Stations, Tasks |
| `POST` | `/tasks/{id}/renew-lease` | `RenewLease` | Tasks |
| `POST` | `/tasks/{id}/complete` | `CompleteTask` | Tasks |
| `POST` | `/tasks/{id}/seal-package` | `SealPackage` | Tasks, Packages |
| `POST` | `/packages/{id}/slam` | `RunSlam` | Packages |
| `GET` | `/queues/{taskType}/depth` | `GetQueueDepth` | Tasks |
| `POST` | `/tasks/expire-leases` | `ExpireLeases` | Tasks |
| `GET` | `/healthz` | *(liveness)* | System |

Every route registered on the chi router appears in `apis/openapi.yaml`, and
every path in the spec is a real route. No orphans in either direction.

## Conventions

**Richardson Maturity Level 2, deliberately not Level 3.** Resource nouns,
correct verbs, correct status codes, `Location` headers on creation — but no
HATEOAS. Clients are expected to know the URL structure rather than discover
it from response links, and the spec says so explicitly.

**Actions are POSTs on sub-resources.** `claim-next`, `renew-lease`,
`complete`, `seal-package`, `slam` and `expire-leases` are state transitions
with side effects, not resource replacements — so they are `POST` on a
sub-path, not `PUT` on a field. `POST /stations/{stationId}/claim-next` in
particular reads correctly as "this station asks for its next task," which is
the pull semantics made visible in the URL.

**Domain structs never leak.** Every request and response body is a DTO
defined in `internal/adapters/inbound/http/dto.go`. Aggregates are unexported
internally and are mapped explicitly on the way out.

**Validation happens twice, for different reasons.** DTO-level validation
catches malformed requests (missing `stationId`, empty `type`) and returns
`400`. Domain-level invariants (`ErrNoScannedContents`, `ErrCapabilityMismatch`)
return `422` — the request was well-formed, the *operation* was not allowed.
`sealPackageRequest.validate()` shows the line clearly: it checks `stationId`
is present but deliberately does **not** check that `contents` is non-empty,
because "you cannot seal an empty carton" is a domain rule, not a syntax
error.

## Errors: RFC 7807 `application/problem+json`

Every error response — without exception — is an RFC 7807 problem document:

```json
{
  "type": "https://errors.fulfillment-execution.warehouse-systems.dev/task-already-claimed",
  "title": "Task already claimed by another station",
  "status": 409,
  "detail": "task: already claimed",
  "instance": "/tasks/task-8a1f/complete"
}
```

`type` is a stable identifier a client can branch on — a URI, but not a
fetchable document (RFC 7807 §3.1 explicitly permits this). `title` is the
category-level human summary; `detail` is the specific error text; `instance`
is the request path that produced it. On bare collection-create endpoints
(`POST /tasks`, `POST /stations`) `instance` is omitted, because the path
identifies no specific resource.

Rationale is in [ADR-0005](../adr/0005-rfc-7807-problem-details.md).

### Status-code mapping

The mapping lives in `internal/adapters/inbound/http/errors.go` and is
exhaustive over the typed error set.

| Status | Meaning here | Errors mapped |
| --- | --- | --- |
| `400` | Malformed or incomplete request body | invalid JSON, missing required DTO field (`invalid-request`) |
| `404` | Referenced aggregate does not exist | `ErrTaskNotFound`, `ErrStationNotFound`, `ErrPackageNotFound` |
| `409` | Well-formed request, but the aggregate's **state** forbids it | `ErrAlreadyClaimed`, `ErrAlreadyCompleted`, `ErrNotClaimed`, `ErrNotOwner`, `ErrOccupied`, `ErrNotOccupied`, `ErrAlreadySealed`, `ErrAlreadyProcessed`, `ErrNotSealed`, `ErrNoClaimableTask` |
| `422` | Well-formed request, but the **content** violates a domain rule | `ErrCapabilityMismatch` (task and station), `ErrNoScannedContents`, `ErrWrongTaskType` |
| `500` | Anything unmapped | fallback (`internal-error`) |

The `409` versus `422` split is the one worth internalising: `409` means *"try
again later or against different state"* — the task is claimed **now**, the
lease may lapse and free it. `422` means *"this will never work as asked"* —
the station lacks `hazmat` certification and retrying changes nothing.

`ErrNoClaimableTask` is mapped to `409` rather than `404` on purpose: the pool
is a valid resource that is momentarily empty of work this station can do, not
a missing resource. An idle station polls again; it does not treat the answer
as a permanent failure.

### Every problem type

| Slug | Status | Title |
| --- | --- | --- |
| `invalid-request` | 400 | The request is malformed or missing a required field |
| `task-not-found` | 404 | Task not found |
| `station-not-found` | 404 | Station not found |
| `package-not-found` | 404 | Package not found |
| `task-already-claimed` | 409 | Task already claimed by another station |
| `task-already-completed` | 409 | Task already completed |
| `task-not-claimed` | 409 | Task is not currently claimed |
| `task-not-owner` | 409 | Station does not own the active claim on this task |
| `station-occupied` | 409 | Station is already occupied |
| `station-not-occupied` | 409 | Station is not occupied |
| `package-already-sealed` | 409 | Package already sealed |
| `package-already-processed` | 409 | Package SLAM already processed |
| `package-not-sealed` | 409 | Package must be sealed before SLAM |
| `no-claimable-task` | 409 | No claimable task for station capabilities |
| `task-capability-mismatch` | 422 | Station capabilities do not match task requirements |
| `station-capability-mismatch` | 422 | Capabilities do not match |
| `package-no-scanned-contents` | 422 | Cannot seal a package without scanned contents |
| `wrong-task-type` | 422 | Wrong task type for this operation |
| `internal-error` | 500 | Internal server error |

All types share the base URI
`https://errors.fulfillment-execution.warehouse-systems.dev/`.

## Not on this API

- **Work release.** `wes-work-planning` decides what to release; it arrives
  over Kafka, not HTTP.
- **Inventory reservations.** `inventory-storage` owns stock truth.
- **WCS / equipment commands.** A separate command channel, not built.
