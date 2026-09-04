---
id: 0018-installed-capacity-read-endpoint
slug: /adr/0018-installed-capacity-read-endpoint
title: 0018. Installed-capacity read endpoint for workforce-management's capacity ceiling
sidebar_label: 0018. Installed-capacity read endpoint
description: ADR 0018 — expose GET /capacity/{capability} as a read model over the Station registry, so workforce-management can enforce a real, live installed-capacity ceiling on CommitShiftPlan instead of trusting a caller-supplied count alone.
---

# 0018. Installed-capacity read endpoint

## Status

Accepted.

## Context

`workforce-management`'s `CommitShiftPlan` accepts a caller-supplied
`installedStations` count per path line and rejects a plan whose
`plannedHeads` exceeds it. That check is real, but the number it
validates against is only as good as whatever the caller happened to
type in — nothing ties it to the actual, physical Station registry this
repo owns. A stale or simply wrong `installedStations` value would let
a shift plan commit against capacity that does not exist (or reject one
that fits fine), and `workforce-management` has no way to know, because
it has no dependency today on this repo at all.

This repo already answers a structurally identical read-model question
for a different resource: `GET /queues/{taskType}/depth` (`GetQueueDepth`)
projects "how many Pending tasks of this type exist right now" without
mutating anything, treating an unrecognized `taskType` as a real answer
of `0` rather than a 404. Exposing "how many stations physically hold a
given capability" is the same shape of question over a different
aggregate (`Station` instead of `Task`), and the existing pattern
transfers directly.

## Decision

**Add `GET /capacity/{capability}`, a read-only projection over the
Station registry, returning how many currently-registered stations hold
`capability` — regardless of occupancy.** This is deliberately a raw
INSTALLED count (stations that CAN work a path), never a STAFFING count
(associates actively working it right now); the latter is
`workforce-management`'s own domain (`AssociateShift` /
`LaborAssignment`), and this endpoint has no opinion about it.

```go
// internal/application/ports/ports.go
type StationRepo interface {
	Save(ctx context.Context, s *station.Station) error
	FindById(ctx context.Context, id shared.StationId) (*station.Station, error)
	CountByCapability(ctx context.Context, capability shared.Capability) (int, error)
}
```

```go
// internal/application/usecases/get_installed_capacity.go
type GetInstalledCapacity struct {
	Stations ports.StationRepo
}

func (uc *GetInstalledCapacity) Execute(ctx context.Context, capability shared.Capability) (int, error) {
	return uc.Stations.CountByCapability(ctx, capability)
}
```

Wired at `GET /capacity/{capability}` (`GetInstalledCapacityHandler`),
returning `{capability, installed}`. Mirrors `GetQueueDepthHandler`'s own
contract exactly: an unrecognized `capability` is not an error, it is a
real `installed: 0` — since this is a count over existing rows, not a
lookup against a fixed enum the server enforces.

The Postgres implementation uses a single `WHERE $1 = ANY(capabilities)`
query against the existing `stations.capabilities TEXT[]` column — no
schema migration needed, no change to how a Station is stored, only a
new read over data already there.

### What this decision does NOT do

- It does not change how a Station is created, occupied, or persisted —
  `RegisterStation`, `CheckInStation`, `CheckOutStation` are untouched.
- It does not give this repo any awareness of `workforce-management`'s
  `ShiftPlan` or `plannedHeads` — this is a one-way read exposed for any
  consumer, the same as `GetQueueDepth` already is.
- It does not attempt to reconcile "installed capacity" with "currently
  occupied capacity" — that distinction stays entirely on the caller's
  side; this endpoint only ever answers the first question.

## Consequences

### Easier

- `workforce-management`'s `CommitShiftPlan` can now validate
  `plannedHeads` against this repo's REAL Station registry, live,
  closing the "trust the caller's number" gap — see
  `workforce-management`'s own ADR for the consuming side of this
  decision.
- Zero schema change: the query reads the array column that already
  exists.

### Harder

- A new public read endpoint is now part of this repo's contract surface
  and must be kept stable for `workforce-management` (and any future
  consumer) — same maintenance burden `GetQueueDepth` already carries,
  not a new category of cost.
- `workforce-management`'s `CommitShiftPlan` now has an OPTIONAL runtime
  dependency on this repo being reachable (see its own ADR for the
  fail-loud policy it applies when this endpoint cannot be reached) —
  the fail-loud choice is deliberately made on the CONSUMING side, since
  a shift-plan commit mutates real state and this fleet's own rule is
  "fail loud for anything that mutates real state."

## Verification

`internal/application/usecases/get_installed_capacity.go` +
`_test.go`: 2 tests (counts matching stations; an unrecognized capability
returns 0, not an error).

`internal/adapters/outbound/memory/station_repo.go` and
`internal/adapters/outbound/postgres/station_repo.go`:
`CountByCapability` implemented identically in both — a Go-side filter in
memory, a Postgres `ANY(array)` containment query against the real
table. `TestStationRepo_CountByCapability` proves the real Postgres query
against actual rows (gated `-tags=integration`, run against a live local
Postgres), not just the in-memory adapter's behavior.

`internal/adapters/inbound/http/handlers_test.go`: `TestGetInstalledCapacity`
(happy path) and `TestGetInstalledCapacity_UnregisteredCapability_ReturnsZeroNotError`
(mirrors `GetQueueDepthHandler`'s own zero-not-404 contract).

`go test ./... -race` (all packages, including `internal/architecture`'s
hexagonal fitness tests), `golangci-lint run ./...` (0 issues),
`go build -tags=integration ./...` / `go vet -tags=integration ./...`
(the `consumer_integration_test.go`-class pitfall this fleet has hit
before), `make check-all`, and `cd docs && npm run build` all pass.
