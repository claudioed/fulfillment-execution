---
id: 0024-station-location-code-and-workcenter-role-check
slug: /adr/0024-station-location-code-and-workcenter-role-check
title: 0024. Optional Station.locationCode, validated against facility-layout's WorkCenter role
sidebar_label: 0024. Station locationCode + role check
description: ADR 0024 — why Station gains an optional facility-layout LocationCode, why the registration-time role check is a synchronous read (not a Kafka projection), and why it fails open on everything except a KNOWN wrong role.
---

# 0024. Optional `Station.locationCode`, validated against facility-layout's WorkCenter role

## Status

Accepted — implemented in the same change that introduced this record.

## Context

facility-layout's own ADR-0016 introduced `LocationRole` (Storage, Dock,
WorkCenter, Yard, Staging, ...) as a first-class attribute of a
`LocationSlot`, and its `GET /locations/{locationCode}` endpoint already
returns that resolved role. That same ADR's Phase B3 follow-on list named
this exact integration: "fulfillment-execution: optional `locationCode` on
`Station`, validated against a WorkCenter role via facility-layout's
classification/role read."

This service's `Station` aggregate had no notion of where a station
physically sits. That is a real modeling gap for a service whose whole
purpose is dispatching physical work to physical positions
(`claimNext(stationId, capabilities)`): a Pack station's throughput,
travel distance to it (see wes-work-planning's own ADR-0017, "Read
facility-layout's travel graph..."), and even whether it is registered at
a sane location at all are all questions this service could not answer
without a coded location. facility-layout's `LocationRole.WorkCenter`
(ADR-0016 there) is defined as exactly "a coded location where a task-tier
activity like Pack, Sort, QC, VAS, Deconsolidate, Receive, or Kit happens"
— which is precisely what a fulfillment-execution `Station` is, from
facility-layout's point of view.

The question this ADR answers is not *whether* to consume this fact —
that follow-on was already scoped — but *how strict the check should be*,
since a wrong answer here has real operational cost in both directions:
too strict, and a legitimate station registration is blocked by a
soft-dependency's downtime; too permissive, and a genuine modeling mistake
(a Pack station registered against a storage aisle's LocationCode) goes
uncaught.

### Precedent: this is the same integration shape as two other ADRs already in this fleet

- This service's own [ADR-0010](./0010-package-segregation-and-sort-lane.md)
  already established the "live, synchronous, permissive-by-default,
  env-var-selected" outbound adapter shape for `ProductClassificationLookup`
  against inventory-storage.
- inventory-storage's `StowStock`/`LocationClassificationLookup` (its own
  ADR-0009 there) reads this exact same facility-layout endpoint family
  for a related but distinct purpose (hazmat/temperature placement), and
  established the **fail-closed** precedent for a CLASSIFIED SKU's
  placement lookup.
- wes-work-planning's ADR-0017 (`TravelDistanceLookup`) is the most recent
  sibling: a synchronous HTTP read against a different facility-layout
  endpoint, at a different single-shot call site (there: shift-plan
  commit; here: station registration), with the same "fail-open unless a
  known fact is actively wrong" posture this ADR adopts.

## Decision

**We will read a station's WorkCenter-role validity once, synchronously,
from `GET /locations/{locationCode}`, at the moment `RegisterStation`
registers or re-registers a station — and reject the registration
outright ONLY when the role is KNOWN and is explicitly something other
than WorkCenter. Any other outcome (unknown location, lookup
unavailable/erroring, no locationCode supplied at all) is fail-open: the
locationCode is still recorded, unchecked.**

1. **`locationCode` is optional, caller-supplied, and additive on
   `Station`.** A new field with a setter (`SetLocationCode`), not a
   `New`/`Rehydrate` constructor parameter, plus a separate
   `RehydrateWithLocation` function — every existing call site
   (`New`, `Rehydrate`, every test fixture) keeps compiling and behaving
   exactly as before this feature existed. This mirrors
   wes-work-planning's own `PathPlan.SetTravelDistance` (ADR-0017 there)
   precisely.
2. **Mechanism: synchronous outbound HTTP, mirroring this service's own
   `productclassification` adapter pattern exactly, not a Kafka
   projector.** A new outbound port `ports.LocationRoleLookup`
   (`GetRole(ctx, locationCode) (LocationRoleInfo, error)`), a new package
   `internal/adapters/outbound/facilitylayout/` with a plain `net/http`
   `Client` and a `PermissiveLookup` no-op default, selected via
   `LOCATION_ROLE_MODE=http|permissive` (default `permissive`), requiring
   `FACILITY_LAYOUT_BASE_URL` in `http` mode. There is no "role changed"
   domain event to consume — a LocationCode's role changes only when the
   physical layout itself changes, which is exactly the kind of static
   fact a synchronous read-through is right for, same reasoning as
   wes-work-planning's ADR-0017.
3. **The check is DELIBERATELY asymmetric, and DELIBERATELY not
   fail-closed the way inventory-storage's `StowStock` is:**
   - `Known=true, Role != "WorkCenter"` → **reject** with
     `ErrStationLocationNotWorkCenter` (422). This is the one case this
     ADR exists to catch: an operator or seed script registering a Pack
     station against a storage aisle's LocationCode is a genuine
     modeling mistake, and facility-layout has an authoritative,
     available answer that says so.
   - `Known=false` (unrecognized LocationCode) → **fail open**, recorded
     unchecked. facility-layout not yet having a coded location modeled
     is not evidence that the registration is wrong.
   - Lookup transport error, non-2xx/400/404 status, or `LocationLookup`
     nil (permissive mode, or no port wired at all) → **fail open**,
     recorded unchecked. Registering a station is not a safety-critical
     write the way `StowStock`'s bin-placement is — a pack station going
     temporarily unvalidated because facility-layout is down is a far
     smaller operational cost than a fulfillment-execution deploy or
     smoke test being unable to register ANY station because a sibling
     service happens to be unavailable.
4. **REST surface: `locationCode` is OMITTED from `StationResponse`** —
   not defaulted to `""` — when no locationCode was recorded, so a
   consumer that already treats "absent" as "not set" sees no difference
   from before this feature existed (same omit-when-unknown discipline as
   wes-work-planning's `travelDistanceM`, ADR-0017 there).
5. **Persistence: an additive, nullable `location_code` column** on the
   existing `stations` table (migration `0010`), following this repo's
   own additive-migration convention; the in-memory adapter needs no
   change since it stores the `*station.Station` pointer directly.

## Consequences

### Easier

- **A `Station` can now say where it physically sits**, closing a real
  modeling gap and setting up the two other B3 follow-ons that depend on
  a coded station location existing at all: wes-work-planning's travel
  distance estimates (ADR-0017 there) and any future console rendering of
  "which stations are in which zone."
- **Catches a real class of mistake with an authoritative answer**, when
  facility-layout is up and the location is modeled: a station registered
  against a storage aisle is now rejected outright, not silently
  accepted.
- **No new idempotency mechanism.** Nothing is persisted beyond the
  `Station` aggregate's own row; there is no event to dedupe.
- **Symmetric with this service's own ADR-0010 adapter shape**, and with
  wes-work-planning's ADR-0017. A developer who has read
  `productclassification.Client`/`PermissiveLookup` recognizes
  `facilitylayout.Client`/`PermissiveLookup` immediately;
  `LOCATION_ROLE_MODE=http|permissive` mirrors
  `PRODUCT_CLASSIFICATION_MODE` exactly.

### Harder

- **A `locationCode` recorded at registration time is not retroactively
  re-validated.** If facility-layout's role for that LocationCode changes
  later (a re-zoning, a corrected import), an already-registered
  station's `locationCode` is not automatically re-checked or flagged —
  the same accepted gap wes-work-planning's ADR-0017 documents for its
  own travel-distance hint.
- **The asymmetric fail-open design means a genuinely wrong location CAN
  still slip through** whenever facility-layout is unavailable at
  registration time, or the LocationCode is not yet modeled there. This
  is a deliberate trade (registering a station must never become
  impossible because a sibling service is down), but it means the
  WorkCenter check is a best-effort guard, not a hard guarantee.
- **`locationCode` is a bare string, not a validated facility-layout
  LocationCode type.** This service does not parse or validate the
  seven-segment LocationCode shape itself (facility-layout's own
  `GET /locations/{locationCode}` returning 400/404 covers "is this a
  real, existing code" indirectly, via fail-open); a malformed but
  never-looked-up code (empty `LocationLookup`, `LOCATION_ROLE_MODE`
  left at `permissive`) is recorded as-is.
