---
id: 0008-mcp-inbound-adapter
title: 8. Model Context Protocol as an inbound adapter, not a new service
sidebar_label: 8. MCP inbound adapter
sidebar_position: 8
description: Expose this bounded context to the AI ecosystem via an MCP server built as a second driving adapter over the existing use cases — Streamable HTTP, official Go SDK, static bearer-key auth, curated intent-level tools.
---

# 8. Model Context Protocol as an inbound adapter, not a new service

## Status

**Accepted.** First implementation is `fulfillment-execution`; this ADR is the
reference the other four bounded contexts copy.

## Context

The platform is being connected to the AI ecosystem (Claude, Cursor, ChatGPT,
agent frameworks). The interoperability standard those clients speak is the
**Model Context Protocol (MCP)**: a client discovers a server's *tools*
(model-callable functions), *resources* (read-only context), and *prompts*
(reusable templates), then an LLM decides which to call.

The forces:

- **There is already a clean action surface.** Every capability of this service
  is an application-layer **use case** (`internal/application/usecases`), one
  struct per use case, reached through ports. The `chi` HTTP adapter is a thin
  driving adapter over exactly those use cases. An AI client needs the same
  actions the HTTP client already has.
- **The domain must not learn about MCP.** ADR-0001's dependency rule is
  load-bearing: domain depends on nothing, application depends on domain,
  adapters depend inward. A protocol whose shape is set by an external LLM
  ecosystem is precisely the kind of concern that must stay in an adapter.
- **MCP has an idiomatic Go path now.** The official **MCP Go SDK**
  (`github.com/modelcontextprotocol/go-sdk`) is a Tier-1 SDK. Building the
  server in Go keeps it in the same language, module, and quality gate as the
  rest of the service — no Python sidecar, no second toolchain.
- **The spec is versioned aggressively.** Revisions in 2025-06, 2025-11, and
  2026-07 have already deprecated features (`roots`, `sampling`, `logging` —
  SEP-2577). Whatever is built will need to track a moving contract.
- **Tools are model-controlled and can act.** Unlike an HTTP client driven by
  code we wrote, an LLM chooses *when* to call a tool and *with what arguments*.
  The spec's own guidance is emphatic: curate a small set of intent-level
  tools, treat tool invocation as requiring host consent, and guard
  state-changing tools most heavily.
- **This is an internal, non-user-facing deployment.** The servers run inside
  the `warehouse` kind cluster for agent and developer use, not on the public
  internet for end users. The MCP authorization spec permits a static bearer
  token for exactly this case; full OAuth 2.1 is required only when a server
  faces real end users.

## Decision

**We will expose this bounded context to the AI ecosystem through an MCP server
built as a second driving adapter over the existing use cases — leaving the
domain and application layers untouched.**

### The adapter, mirroring the HTTP one

A new `internal/adapters/inbound/mcp/` sits beside `internal/adapters/inbound/http/`:

```
internal/adapters/inbound/mcp/
  server.go      MCP Server wiring (Go SDK), capability registration
  tools.go       intent-level tool handlers -> call use cases
  resources.go   read-model resources (scoped, not bulk)
  prompts.go     workflow prompts (operational SOPs)
  auth.go        bearer-key auth middleware (interface; OAuth-ready seam)
  mapping.go     tool I/O <-> DTOs; RFC 7807 -> structured tool errors
```

It depends inward on `application` exactly as the HTTP adapter does. No MCP type
appears in `internal/domain/**` or `internal/application/**`. The tool handlers
call the **same** use case structs the HTTP handlers call — never a parallel
code path, never the domain directly.

### A separate `cmd/mcp` binary

The MCP server ships as its own composition root, `cmd/mcp/main.go`, reusing the
same repositories, ports, and `EVENT_PUBLISHER` wiring as `cmd/execution`. Two
deployables from one module: the HTTP service and the MCP server. This isolates
blast radius, lets the two scale independently, and keeps least-privilege clean
(the MCP process can be given a narrower footprint).

### Streamable HTTP only

The single supported transport is **Streamable HTTP**, stateless where the SDK
allows. We do not ship stdio builds; local desktop-client use goes through the
same HTTP endpoint. One transport is one thing to secure, trace, and test.

### Curated, intent-level tools — not one tool per endpoint

Tools are designed around decisions an agent makes, not around REST endpoints.
Mechanically wrapping all ten HTTP routes would overwhelm the model — the
documented number-one MCP anti-pattern. The pilot surface for this context:

- `get_queue_status` (read) — depth and oldest CPT per process path.
- `find_claimable_work` (read) — what a station could claim right now.
- `diagnose_stuck_tasks` (read) — leases near expiry and why.
- `complete_task` (write, annotated destructive) — wraps the `CompleteTask`
  use case; the existing at-most-once / ownership / lease invariants make a
  model-invoked completion safe by construction.

Resources expose existing read models as **scoped** context contracts
(`queue://fulfillment/{processPath}/status`), never a database dump. Prompts
encode operational SOPs (e.g. `triage_backlog`: how to read the queue, when to
escalate, what "done" means).

### Static bearer-key auth, behind an OAuth-ready seam

`auth.go` validates a per-client API key (from a Kubernetes Secret) on every
request; missing or invalid key returns `401`; the key is never logged. Two key
classes — read-only and read-write — gate write tools without an IdP. The
middleware is an **interface**, so an OAuth 2.1 resource-server implementation
(short-lived tokens, `.well-known` discovery, no token passthrough) can drop in
later without touching any tool handler. See ADR-0009 if/when that upgrade is
taken.

### Reuse the existing observability

The adapter is instrumented with the same OpenTelemetry setup as the HTTP and
Kafka boundaries: a span per tool call (tool name, scope, outcome), counters for
invocations and denials. MCP calls appear in Jaeger and Grafana next to HTTP
requests, continuing the same distributed traces.

## Consequences

### Easier

- **The domain and application layers do not change at all.** MCP is purely
  additive; the dependency rule (ADR-0001) is preserved and checked by the
  existing arch-go fitness tests (ADR-0006).
- **One action surface, two protocols.** HTTP and MCP call the same use cases,
  so behaviour — including every invariant — is identical regardless of caller.
- **Model-invoked writes are safe by construction.** At-most-once claiming
  (ADR-0003) and ownership checks already reject a double `complete_task`; the
  RFC 7807 error (ADR-0005) surfaces as a clean structured tool error.
- **It stays in Go, in one quality gate.** The MCP adapter is unit-tested to the
  same ≥90% bar, linted, and CI-gated like every other package.
- **The auth upgrade is contained.** Moving to OAuth later is an adapter change
  behind a stable interface, not a rewrite.

### Harder

- **A second deployable to run and secure.** `cmd/mcp` is another binary, image,
  Helm release, and ingress. The isolation is deliberate but it is real
  operational surface that did not exist before.
- **Auth is deliberately minimal.** A static bearer key is appropriate for an
  internal, non-user-facing server, but it does **not** cover user-facing,
  multi-tenant use. The servers must stay in-cluster until the OAuth seam is
  taken. Recording that boundary is the point.
- **The MCP spec is a moving target.** Aggressive versioning and deprecations
  mean the SDK must be pinned and revisited; features like `roots`/`sampling`
  are already deprecated and must be avoided in favour of tool parameters.
- **Tool curation is an ongoing discipline, not a one-time choice.** Nothing in
  the compiler stops a future PR from adding a tool per endpoint. The MCP
  governance charter (`docs/mcp/governance-charter.md`) and a CI lint on tool
  count/annotations exist to hold the line; without them the surface degrades.
- **LLM-chosen arguments are untrusted input.** Every tool handler must validate
  its inputs defensively — the caller is a model, not our own code — which is
  stricter than what the HTTP DTO layer assumes.
- **`complete_task` is a state change an autonomous agent can trigger.** It is
  annotated destructive, scope-gated, and rate-limited, and the spec expects
  host-side consent, but the residual risk of an agent completing the wrong task
  is higher than for a human-driven HTTP call. The domain invariants bound the
  damage; they do not eliminate the judgement risk.
