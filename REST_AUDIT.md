# REST API Audit — Fulfillment Execution

Scope: `internal/adapters/inbound/http/` only (Richardson Maturity Level 2 —
resource nouns, correct verbs/status codes; HATEOAS explicitly out of scope).
No domain, use case, or application-layer behavior was touched.

## Stage 1 — REST principles audit and fixes

### 1. Resource nouns, not verbs, in URLs

**Violation found and fixed:** `POST /admin/expire-leases` was an RPC-style
endpoint with no resource identity — `admin` is not a resource this service
exposes, and the route wasn't scoped under `/tasks`, the collection it
actually acts on (it sweeps every Claimed task whose lease has expired).

**Fix:** renamed to `POST /tasks/expire-leases` — a collection-level action
on the `tasks` resource, the same pattern as the existing single-resource
action endpoints (`/tasks/{id}/complete`). Handler name (`PostExpireLeases`)
and use case (`ExpireLeases`) are unchanged; only the route in `router.go`
changed. Updated: `router.go`, `handlers.go` doc comment, `handlers_test.go`,
`README.md`, `CLAUDE.md`.

Every other endpoint already uses correct resource nouns
(`/tasks`, `/stations`, `/packages`, `/queues`) with verb-suffixed action
segments reserved for genuine domain commands (`/claim-next`,
`/renew-lease`, `/complete`, `/seal-package`, `/slam`) — this is correct
DDD/REST practice for non-CRUD commands and was left as-is.

> **Violations found and fixed: 2.** (1) `POST /admin/expire-leases` →
> `POST /tasks/expire-leases` (non-resource-scoped RPC endpoint). (2) missing
> `Location` headers on all three `201` responses. Plus one new,
> previously-absent behavior added: `400` validation on every DTO with
> required fields (details below).

### 2. Correct HTTP methods

**No violation found.** Both `GET` handlers (`GetHealthz`,
`GetQueueDepthHandler`) are read-only — neither calls a use case that
mutates state or a repo `Save`. Every mutating operation is `POST`. This
service has no revocation-style endpoint (nothing analogous to
inventory-storage's reservation revocation), so `DELETE` doesn't apply here.

### 3. Correct status codes

**Violations found and fixed — missing `Location` headers on `201`s.**
Three endpoints return `201 Created` (`POST /tasks`, `POST /stations`,
`POST /tasks/{id}/seal-package`) but none set a `Location` header pointing
at the created resource. Fixed by adding a `writeCreated` helper
(`errors.go`) that sets `Location` before writing the `201` body:

| Endpoint | Location |
|---|---|
| `POST /tasks` | `/tasks/{id}` |
| `POST /stations` | `/stations/{id}` |
| `POST /tasks/{id}/seal-package` | `/packages/{id}` (the Package it creates) |

**Violation found and fixed — no input validation on decoded DTOs (missing
`400`).** Every handler decoded its request body and, on a JSON syntax
error, correctly returned `400`. But once decoding succeeded, no handler
checked that required fields were actually present — an empty `type`,
`orderRef`, `stationId`, `taskType`, or an empty `requiredCapabilities`/
`capabilities` slice was passed straight through to the use case, which
would either panic-adjacent misbehave downstream or produce a confusing
`404`/`422` instead of a `400` that names the real problem.

**Fix:** added a `validate() string` method to every request DTO that has
required fields (`createTaskRequest`, `claimNextRequest`,
`renewLeaseRequest`, `completeTaskRequest`, `sealPackageRequest`,
`registerStationRequest`), called right after decode; a non-empty result
writes `400` with the specific missing-field message via a new
`writeBadRequest` helper. `runSlamRequest`'s two `float64` fields have no
"missing" state distinguishable from a legitimate `0`, so no DTO-level
validation was added there — a negative or nonsensical weight is a semantic
question the domain (`Package.Weigh`) doesn't currently police either, so
adding a stricter check at the HTTP layer would be a design preference
introduced by this audit, not a fix for a real bug, and was left alone
per the task's own instruction not to fix design preferences.

`sealPackageRequest.validate()` deliberately does **not** require
`contents` to be non-empty: an empty scan list is `pack.ErrNoScannedContents`,
a genuine domain invariant already mapped to `422` — duplicating it as a
`400` at the boundary would blur the 400-vs-422 line this audit is meant to
sharpen, not fix a bug.

Every other status code was already correct against the table in
REST_API_TASK.md: `200` for successful reads (`claim-next`,
`queue-depth`, `expire-leases`) and idempotent-ish updates, `204` for
actions with no response body (`renew-lease`, `complete`, `slam`), `404`
for not-found (`ErrTaskNotFound`/`ErrStationNotFound`/`ErrPackageNotFound`),
`409` for genuine state conflicts (double-claim, double-complete,
occupied station, no claimable task), `422` for semantically invalid input
(capability mismatch, no scanned contents). No mismatches found beyond the
two above.

### 4. Idempotency semantics (documented, not changed)

| Endpoint | Idempotent? | Why |
|---|---|---|
| `POST /tasks` | No | Creates a new Task with a fresh, service-generated id on every call; no client-supplied id to dedupe on. Calling twice with the same body creates two Tasks. |
| `POST /stations` | **Yes** | `RegisterStation` explicitly upserts on the client-supplied `stationId` — re-registering updates capabilities rather than erroring or duplicating. Already correct; no bug. |
| `POST /stations/{stationId}/claim-next` | No (by design) | Each call attempts to lease a *different* task from the pool; this is the intended PULL semantics, not a bug. |
| `POST /tasks/{id}/renew-lease` | Yes | Extends the same lease; repeated calls just push the expiry further out. |
| `POST /tasks/{id}/complete` | Yes (fails safe) | First call completes the task; every subsequent call on an already-completed task returns `409` rather than silently double-applying — safe to retry-and-check, not silently repeatable. |
| `POST /tasks/{id}/seal-package` | No | Creates a new Package each call (like `POST /tasks`) — calling twice on the same still-open Pack task's claim would seal twice, but the second call fails because the underlying Task's claim, once completed via other flows, would already be gone; in the direct double-call case it is not idempotent (creates a second Package). Flagged as a design point for future work, not fixed here — no client-supplied Package id exists to dedupe on, and inventing one is a design decision, not a "clear bug" the audit is meant to fix. |
| `POST /packages/{id}/slam` | Yes (fails safe) | `pack.ErrAlreadyProcessed` -> `409` on a repeat call against the same package. |
| `POST /tasks/expire-leases` | Yes | Sweeping is a pure function of current time vs. lease expiry; repeated calls are safe (a second call simply frees nothing new). |

No client-supplied-id idempotency bug was found — the one place a
client-supplied id already exists (`stationId` on `RegisterStation`) is
already handled correctly as an upsert.

### 5. Consistent JSON casing

**No violation found.** Every field across every DTO in `dto.go` is
camelCase (`orderRef`, `requiredCapabilities`, `leaseStationId`,
`leaseExpiry`, `scannedContents`, `taskType`, `actualWeight`,
`expectedWeight`, `stationId`, etc.). Verified by inspection of every
struct tag in `dto.go` — no renames were needed.

### 6. Content negotiation

Every success response already set `Content-Type: application/json` via
the shared `writeJSON` helper — confirmed, no change needed for Stage 1.
Error responses are addressed in Stage 2 below (`application/problem+json`).

### Stage 1 verification

```
go build ./...        # clean
go vet ./...           # clean
go test ./... -race    # all packages pass
golangci-lint run ./... # 0 issues
gofmt -l .              # no output
```

Existing httptest coverage for the HTTP adapter passes; the `expire-leases`
route-rename test assertion was updated (not deleted), and new tests were
added (not replacing old ones) for: `Location` header on all three `201`s,
`400` on a missing required field, and confirmation the old
`/admin/expire-leases` route now 404s.

## Stage 2 — RFC 7807 (`application/problem+json`) migration

Replaced the bespoke `{"error": "..."}` body and `errorResponse` DTO with an
RFC 7807 `problem` struct (`type`, `title`, `status`, `detail`, `instance`),
and `Content-Type: application/json` on error paths with
`Content-Type: application/problem+json`. `statusFor` (the error → HTTP
status switch) is byte-for-byte unchanged — only what gets written to the
body and the Content-Type header changed, per the task's explicit
constraint.

**Type/title lookup table** — one category per existing sentinel error,
mirroring `statusFor`'s cases exactly (`internal/adapters/inbound/http/errors.go`,
`problemTypeAndTitle`):

| Sentinel error | `type` slug | `title` |
|---|---|---|
| `usecases.ErrTaskNotFound` | `task-not-found` | Task not found |
| `usecases.ErrStationNotFound` | `station-not-found` | Station not found |
| `usecases.ErrPackageNotFound` | `package-not-found` | Package not found |
| `task.ErrAlreadyClaimed` | `task-already-claimed` | Task already claimed by another station |
| `task.ErrAlreadyCompleted` | `task-already-completed` | Task already completed |
| `task.ErrNotClaimed` | `task-not-claimed` | Task is not currently claimed |
| `task.ErrNotOwner` | `task-not-owner` | Station does not own the active claim on this task |
| `station.ErrOccupied` | `station-occupied` | Station is already occupied |
| `station.ErrNotOccupied` | `station-not-occupied` | Station is not occupied |
| `pack.ErrAlreadySealed` | `package-already-sealed` | Package already sealed |
| `pack.ErrAlreadyProcessed` | `package-already-processed` | Package SLAM already processed |
| `pack.ErrNotSealed` | `package-not-sealed` | Package must be sealed before SLAM |
| `usecases.ErrNoClaimableTask` | `no-claimable-task` | No claimable task for station capabilities |
| `task.ErrCapabilityMismatch` | `task-capability-mismatch` | Station capabilities do not match task requirements |
| `station.ErrCapabilityMismatch` | `station-capability-mismatch` | Capabilities do not match |
| `pack.ErrNoScannedContents` | `package-no-scanned-contents` | Cannot seal a package without scanned contents |
| `usecases.ErrWrongTaskType` | `wrong-task-type` | Wrong task type for this operation |
| (unmapped / unexpected error) | `internal-error` | Internal server error |

Two HTTP-layer-only categories (not domain sentinels, since they never reach
a use case) round out the table: `invalid-request` for both malformed-JSON
and missing-required-field `400`s, since both are the same category of
problem — "the request as sent cannot be processed" — just with a different
`detail`.

**`instance` handling**: every problem response sets `instance` to
`r.URL.Path`, except a `400` from `POST /tasks` or `POST /stations` —
collection-create endpoints whose path has no segment identifying a specific
resource — where `instance` is omitted (`json:",omitempty"`), per the task
spec's explicit carve-out.

**Tests**: added `assertProblemDetails` (`handlers_test.go`) — checks
`Content-Type: application/problem+json` and every RFC 7807 field — and
wired it into `TestPostClaimNext_NoWorkReturns409` (409),
`TestPostRenewLease_NotFoundOnUnknownTask` (404),
`TestFullPickLifecycle_ClaimThenComplete`'s double-complete case (409), and
`TestPostTask_MissingRequiredField_Returns400` (400, no-instance case). No
test in this repo asserted the old `{"error":...}` shape to begin with
(prior tests checked only status codes) — grep confirms zero remaining
production references to `errorResponse`/`"error":` (see verification
below).

**README.md**: added a new "Every error response uses RFC 7807..." section
under `## API` with a worked example, since the repo had no prior example of
an error response body to update in place.

### Stage 2 verification

```
go build ./...          # clean
go vet ./...             # clean
go test ./... -race      # all packages pass
golangci-lint run ./...  # 0 issues
gofmt -l .                # no output
grep -rn "errorResponse\|\"error\":" --include="*.go" internal/ cmd/ | grep -v _test.go
# → zero matches
```

**Live curl evidence** (in-memory server, `go run ./cmd/execution`):

404 — `POST /tasks/does-not-exist/renew-lease`:
```
HTTP/1.1 404 Not Found
Content-Type: application/problem+json

{"type":"https://errors.fulfillment-execution.warehouse-systems.dev/task-not-found","title":"Task not found","status":404,"detail":"usecases: task not found","instance":"/tasks/does-not-exist/renew-lease"}
```

409 — `POST /stations/s1/claim-next` (station registered, no claimable
Pick task in the pool):
```
HTTP/1.1 409 Conflict
Content-Type: application/problem+json

{"type":"https://errors.fulfillment-execution.warehouse-systems.dev/no-claimable-task","title":"No claimable task for station capabilities","status":409,"detail":"usecases: no claimable task for station capabilities","instance":"/stations/s1/claim-next"}
```

Both confirm valid RFC 7807 JSON with the correct `application/problem+json`
Content-Type, on both the 404 and 409 branches of `statusFor`.

## Stage 3 — OpenAPI 3.0.3 documentation

`openapi.yaml` written at the repo root. Every operation has an
`operationId`, `summary` + a real domain-grounded `description` (PULL
dispatch, at-most-once lease, CPT priority, SLAM tolerance — pulled from
CLAUDE.md and the domain code, not generic filler), `tags`, full
request/response schemas with `format`s and realistic examples using this
repo's own ubiquitous language (`station-1`, `order-42`, `sku-1`/`sku-2`,
`PICK`/`PACK`/`SLAM`, and the real `uuidLike()` id format from
`cmd/execution/main.go`, e.g. `20260101T120000.000000000`), and every error
response `$ref`s the single shared `components/schemas/Problem` schema
rather than repeating the shape per endpoint.

**Route coverage — 10/10.** Verified by parsing `router.go`'s route table
and cross-checking each `(method, path)` against `openapi.yaml`'s `paths`:

```
GET  /healthz
POST /stations
POST /tasks
POST /stations/{stationId}/claim-next
POST /tasks/{id}/renew-lease
POST /tasks/{id}/complete
POST /tasks/{id}/seal-package
POST /packages/{id}/slam
GET  /queues/{taskType}/depth
POST /tasks/expire-leases
→ 10/10 routes documented
```

**Error responses per endpoint** were derived by reading each use case's
possible return paths (not guessed) — e.g. `renew-lease`/`complete` can
return `task.ErrAlreadyCompleted`/`ErrNotClaimed`/`ErrNotOwner` (all `409`)
per `task.go`'s `RenewLease`/`Complete`; `claim-next` can only return
`ErrStationNotFound` (`404`) or `ErrNoClaimableTask` (`409`) since
`ClaimNext.Execute` swallows per-candidate `Claim` errors internally and
only surfaces the aggregate "nothing worked" case; `seal-package` can
surface `422` two ways (`ErrWrongTaskType` from the use case,
`pack.ErrNoScannedContents` from the domain) plus `409`
(`task.ErrNotOwner`); `slam` can return `422` (`pack.ErrNotSealed`) or `409`
(`pack.ErrAlreadyProcessed`). `station.ErrOccupied`/`ErrNotOccupied` are
not documented on any operation because no current route reaches
`Station.CheckIn`/`CheckOut` — they exist in the domain but aren't wired to
HTTP yet.

### Validation

```
python3 -c "import yaml; yaml.safe_load(open('openapi.yaml'))"   # OK
npx --yes @redocly/cli lint openapi.yaml
```

Redocly lint (`@redocly/cli`, via `npx`, network available):
**0 errors**, 5 warnings, all left as warnings per the task's own guidance
("warnings are fine to leave"):

- `info-license` — no SPDX `license` block in `info`; this is an internal
  bounded-context service, not a published package, so a license field
  doesn't apply.
- `no-server-example.com` — flags `http://localhost:8080` as a
  placeholder-looking host; it's the correct, required local-dev server per
  the task spec (`servers: at minimum http://localhost:8080 for local dev`).
- `operation-4xx-response` ×3, on `GET /healthz`, `GET /queues/{taskType}/depth`,
  and `POST /tasks/expire-leases` — these three operations genuinely have no
  domain 4xx path (healthz never errors; queue-depth treats an unknown
  `taskType` as a legitimate 0, not a 404; expire-leases takes no body and
  has no not-found/conflict case) — only `200`/`500` are real outcomes, so
  no error response was invented just to satisfy the rule.

One real error class was found and fixed: Redocly's default ruleset flags
every operation lacking a `security` requirement (10 errors, one per
operation). This service has no auth middleware today (confirmed against
`router.go`) — fixed by adding an explicit root-level `security: []`,
which accurately documents "no security scheme" instead of leaving it
undefined (undefined reads as an oversight to tooling; `[]` reads as a
deliberate statement). This is a docs-only change (`openapi.yaml`); no
Go code was touched to fix it.

## Stage 4 — GitHub Actions: openapi-lint job (Spectral)

Added `.spectral.yaml` (extends `spectral:oas`, escalates `operation-operationId`,
`operation-description`, `operation-tags`, `info-description`,
`oas3-api-servers` from warning to error) and a new `openapi-lint` job to
`.github/workflows/ci.yml`, added alongside (not replacing) the existing
`lint-test` and `mutation` jobs — neither was touched. It inherits the
workflow-level `on:` block (push/PR to `main`, no `if:` skip condition), so
it is a real blocking gate, matching `lint-test`.

Local verification:
```
spectral lint openapi.yaml --ruleset .spectral.yaml --fail-severity=warn
# → "No results with a severity of 'warn' or higher found!" (exit 0)
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/ci.yml'))"
# → valid YAML
```

**Real GitHub Actions verification.** Task 11's own branch
(`task-11-rest-api-hardening`) is a PR against `task-10-quality-engineering`
(PR #2, stacked on the still-open PR #1), not `main` — so the workflow's
`push`/`pull_request: branches: [main]` triggers don't fire on this PR
automatically. To get the strongest verification the task calls for anyway,
the workflow was manually dispatched on this branch
(`gh workflow run ci.yml --ref task-11-rest-api-hardening`;
`workflow_dispatch` also runs `mutation`, which is otherwise
schedule/dispatch-only and not part of Task 11's gating) and watched to
completion with `gh run watch --exit-status`:

```
✓ openapi-lint in 25s
✓ lint-test in 2m27s
✓ mutation in 54s
```

All three jobs green on GitHub's real runners
(https://github.com/claudioed/fulfillment-execution/actions/runs/32578709734).
Once PR #1 merges to `main` and PR #2 is retargeted/merged, the same
`openapi-lint` job will run automatically on every future push/PR to `main`
via the inherited trigger — no further wiring needed.
