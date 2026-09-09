---
id: 0022-remove-rest-mcp-auth
slug: /adr/0022-remove-rest-mcp-auth
title: 0022. Remove the REST + MCP static-bearer auth layer
sidebar_label: 0022. Remove REST/MCP auth
description: ADR 0022 — the fleet-wide static-bearer auth layer (ADR 0021, adopting warehouse-ops-agent ADR 0005) is removed from this service's REST API and MCP server. Every route/tool is now unauthenticated; supersedes ADR 0021.
---

# 0022. Remove the REST + MCP static-bearer auth layer

## Status

Accepted — implemented in the same change that introduced this record.
Adoption record; the decision itself is fleet-wide (2026-09-09) and reverses
the earlier fleet-wide rollout this service adopted in
[0021-rest-identity-static-bearer-scopes](0021-rest-identity-static-bearer-scopes.md).

## Context

The fleet adopted a static bearer-key auth layer (read/read-write scopes,
no IdP) across every REST surface and MCP server in 2026-09-07. That layer
is not needed for the fleet's current stage, and its operational cost
(per-service key provisioning/rotation in Terraform, `AUTH_MODE` tri-state
handling, coordinating outbound peer keys across every caller) outweighs
its value right now.

## Decision

Remove the entire auth layer from this service:

- `internal/adapters/inbound/auth/` deleted.
- HTTP router (`cmd/execution`, `cmd/fulfillment-reports`) no longer mounts
  any auth middleware — every route, including the `/reports/*` surface, is
  open.
- MCP server (`cmd/mcp`) no longer requires a bearer key — every tool call
  is unauthenticated.
- The outbound `productclassification` client no longer sends an
  `Authorization` header; `INVENTORY_STORAGE_API_KEY`/similar env vars are
  gone.
- Helm chart: `auth:` values block, the `<release>-auth`/`<release>-mcp-keys`
  Secrets, and every `AUTH_MODE`/`API_READ_KEY`/`API_READWRITE_KEY`/
  `MCP_READ_KEY`/`MCP_READWRITE_KEY` env var removed from every Deployment.
- `apis/openapi.yaml`: `components.securitySchemes.bearerAuth` and every
  `security:` key removed.

## Consequences

- Every REST and MCP endpoint in this service is now open to any caller
  that can reach it on the network — access control, if needed again, must
  come from a network boundary (mTLS, service mesh policy, ingress auth) or
  a reintroduced application-layer scheme.
- Re-adopting auth later means restoring `0021-rest-identity-static-bearer-scopes`'s
  shape (or a successor design) rather than starting from a partially-wired
  state — this ADR intentionally removes it cleanly rather than just
  disabling it via `AUTH_MODE=off`, so there is no half-wired auth code to
  bit-rot.
