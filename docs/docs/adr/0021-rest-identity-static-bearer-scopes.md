---
id: 0021-rest-identity-static-bearer-scopes
slug: /adr/0021-rest-identity-static-bearer-scopes
title: 0021. Adopt the fleet REST identity — static bearer keys with read/read-write scopes
sidebar_label: 0021. REST identity (adoption)
description: ADR 0021 — fulfillment-execution's adoption record for the fleet-wide decision (warehouse-ops-agent ADR 0005) to protect every REST surface with the same static bearer key + read/read-write scope posture the MCP adapter has carried since ADR 0008, rolled out behind AUTH_MODE=enforce|log|off.
---

# 0021. Adopt the fleet REST identity — static bearer keys with read/read-write scopes

## Status

**Superseded by [0022-remove-rest-mcp-auth](0022-remove-rest-mcp-auth.md)**
(2026-09-09) — the fleet-wide static-bearer auth layer was removed
entirely. Accepted — implemented in the same change that introduced this record.
Adoption record; the decision itself is fleet-wide and lives in
**warehouse-ops-agent ADR 0005** (*Fleet REST identity: static bearer keys
with read/read-write scopes, no IdP*).

## Decision (as applied here)

This service adopts ADR 0005 unchanged. One `internal/adapters/inbound/auth`
package (the fleet template, copied verbatim per the no-shared-code rule)
holds `Authenticator`, `StaticKeyAuth`, `Scope` and the chi `Middleware`;
the MCP adapter (ADR 0008) now aliases those types instead of carrying its
own copy, so the repository has exactly one identity implementation serving
both surfaces. `cmd/execution` mounts the middleware on every route except
`GET /healthz` with the fleet policy — `GET`/`HEAD`/`OPTIONS` need `read`,
everything else `read-write`; `cmd/fulfillment-reports` mounts it with
`read` fixed for every `/reports/*` route. Keys come from `API_READ_KEY` /
`API_READWRITE_KEY` (falling back to `MCP_READ_KEY` / `MCP_READWRITE_KEY`),
`AUTH_MODE=enforce|log|off` is the rollout gate, and the composition roots
default to `enforce` when any key is configured and to `off` with a WARN
when none is — so local runs and the existing handler tests are untouched.
Failures are this service's usual RFC 7807 problems (ADR 0005 here):
`…/unauthenticated` 401 with `WWW-Authenticate`, `…/insufficient-scope`
403. The outbound product-classification client (ADR 0010) gains an
optional bearer (`INVENTORY_STORAGE_API_KEY`) for when inventory-storage
enforces. The chart carries `auth.mode` / `auth.readKey` /
`auth.readWriteKey` / `auth.existingSecret` and `inventoryStorage.apiKey`;
warehouse-infra generates and injects the keys and drives the
`log` → `enforce` rollout. Rollback at any step is configuration only.
