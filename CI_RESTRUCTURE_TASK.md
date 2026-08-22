# CI Workflow Restructure — Task 12

Rewrite `.github/workflows/ci.yml` in THIS repo to follow the structure given
below (provided by the user as the canonical template). This is a
restructure/hardening of the EXISTING workflow, not a from-scratch rewrite —
you must ADAPT it to this specific repo's real values and PRESERVE the
`openapi-lint` job that Task 11 already added (the template below doesn't
include it because it predates Task 11 — keep it as a 5th job, unchanged
from its current form unless you spot a genuine improvement worth making
consistent with the rest of this restructure, e.g. timeout-minutes).

## The template to follow (adapt values, don't copy blindly)

```yaml
permissions:
  contents: read

concurrency:
  group: ${{ github.workflow }}-${{ github.ref }}
  cancel-in-progress: ${{ github.event_name == 'pull_request' }}

defaults:
  run:
    shell: bash

jobs:
  lint:
    runs-on: ubuntu-latest
    timeout-minutes: 10
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: false          # golangci-lint-action manages its own cache
      - uses: golangci/golangci-lint-action@v7
        with:
          version: v2.13.1
          args: --timeout=5m

  test:
    runs-on: ubuntu-latest
    timeout-minutes: 20
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Build and vet
        run: |
          set -euo pipefail
          go build ./...
          go vet ./...

      - name: Unit tests with coverage
        run: |
          set -euo pipefail
          go test ./... -race \
            -coverprofile=coverage.out \
            -coverpkg=./internal/domain/...,./internal/application/...

      - name: Coverage gate
        env:
          THRESHOLD: "90"
        run: |
          set -euo pipefail
          COVERAGE=$(go tool cover -func=coverage.out | awk '/^total:/ {print $3}' | tr -d '%')
          echo "### Coverage: ${COVERAGE}% (gate: ${THRESHOLD}%)" >> "$GITHUB_STEP_SUMMARY"
          if awk -v c="$COVERAGE" -v t="$THRESHOLD" 'BEGIN { exit !(c < t) }'; then
            echo "::error::coverage ${COVERAGE}% is below the ${THRESHOLD}% gate"
            exit 1
          fi

      - uses: actions/upload-artifact@v4
        if: always()
        with:
          name: coverage
          path: coverage.out

  integration:
    runs-on: ubuntu-latest
    timeout-minutes: 20
    services:
      postgres:
        image: postgres:16
        env:
          POSTGRES_USER: <THIS REPO'S USER>
          POSTGRES_PASSWORD: <THIS REPO'S PASSWORD>
          POSTGRES_DB: <THIS REPO'S DB>
        ports: ["5432:5432"]
        options: >-
          --health-cmd pg_isready
          --health-interval 5s
          --health-timeout 3s
          --health-retries 10
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - name: Integration tests
        env:
          DATABASE_URL: "<THIS REPO'S DATABASE_URL>"
        run: go test -tags=integration ./... -race -count=1

  mutation:
    runs-on: ubuntu-latest
    timeout-minutes: 90
    if: github.event_name == 'workflow_dispatch' || github.event_name == 'schedule'
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - name: Install gremlins
        run: go install github.com/go-gremlins/gremlins/cmd/gremlins@<PIN>
      - name: Mutation testing (domain layer)
        run: gremlins unleash ./internal/domain --workers 1 --timeout-coefficient 30
```

## Required adaptations for THIS repo

1. **Top-level `on:` block**: keep the EXISTING one from this repo's current
   `ci.yml` (push/pull_request to main, weekly schedule, workflow_dispatch) —
   the template above only shows `jobs:` onward, it doesn't redefine
   triggers. Do not lose the schedule/workflow_dispatch triggers that gate
   the `mutation` job.

2. **`go-version-file: go.mod`**: replace every hardcoded `go-version: "1.26"`
   with `go-version-file: go.mod` as shown — this reads the actual version
   from this repo's `go.mod` instead of a hardcoded string, so it can't drift
   out of sync. Verify this repo's `go.mod` has a `go` directive that
   resolves correctly with `actions/setup-go@v5` (it should — standard Go
   module files all have one).

3. **Postgres credentials + `DATABASE_URL`**: use THIS repo's actual values
   already present in the current `ci.yml` (check it first) — do not invent
   new ones.

4. **Coverage command upgrade**: note the template's `test` job runs `go test
   ./... -race -coverprofile=coverage.out -coverpkg=./internal/domain/...,./internal/application/...`
   — this is a MEANINGFUL upgrade from however this repo currently computes
   coverage (which likely tests only `./internal/domain/...
   ./internal/application/...` directly). The new approach runs the ENTIRE
   test suite (including HTTP adapter tests, etc.) while still scoping the
   COVERAGE GATE specifically to domain+application via `-coverpkg`. Adopt
   this exactly as shown — it's strictly better (broader test execution,
   same coverage scope) and replaces the old "Full test suite" step too
   (this one command now does both jobs). Remove the old separate
   build/vet/full-suite step if it becomes redundant with this — but the
   template still has an explicit separate `Build and vet` step BEFORE the
   coverage step, keep that ordering.

5. **`integration` job — CHECK YOUR OWN INTEGRATION TEST FILES FIRST**: some
   of the four repos in this workspace have integration tests that
   self-migrate (call `Migrate(...)`/`RunMigrations(...)` directly inside the
   test, e.g. via a `TestMain` or at the top of each test function) and some
   don't. Check this repo's `internal/adapters/outbound/postgres/*_test.go`
   files (the ones with `//go:build integration`) for a call to a Migrate/
   RunMigrations function:
   - If integration tests DO self-migrate: use the template's `integration`
     job exactly as shown (no separate migration step needed).
   - If integration tests do NOT self-migrate (i.e. they assume migrations
     are already applied — check for a comment like "requires migrations
     already applied" or absence of any Migrate call): you MUST keep an
     explicit migration step BEFORE the "Integration tests" step, matching
     whatever this repo's CURRENT `ci.yml` already does for that (check it -
     likely a `go run github.com/golang-migrate/migrate/v4/cmd/migrate@...`
     invocation). Do not drop this or the integration job will fail — verify
     by actually running the job's steps locally against a live Postgres
     before declaring done.
   - Also note the template changes integration test invocation from
     whatever this repo currently uses (likely `-v`, no `-race`) to
     `-race -count=1` (no `-v`) — adopt this. `-count=1` disables Go's test
     result caching, guaranteeing the integration tests actually run fresh
     every time rather than potentially reporting a stale cached pass.

6. **`mutation` job — gremlins version pin**: the template shows
   `@<PIN>` as a placeholder with a comment "pin, not @latest" (the literal
   text in the message the user sent you says `@v0.5.0` as an example, but
   that is NOT necessarily the version to use — check what's actually
   available and already verified working). Run `go list -m -versions
   github.com/go-gremlins/gremlins` to see available tags, and pin to the
   LATEST stable tag from that list (at the time this task was written, that
   was v0.6.0 — verify it's still the latest when you run this, tags may
   have changed). Do not use `@latest` (matches the template's explicit
   instruction), and do not blindly copy an older version number without
   checking — the point of pinning is reproducibility with a KNOWN GOOD
   version, not an arbitrarily old one.

7. **`openapi-lint` job (Task 11, PRESERVE)**: keep this job exactly as it
   currently exists in this repo's `ci.yml` (Spectral lint against
   `openapi.yaml`), as a 5th job alongside the four above. Consider (but only
   apply if it doesn't change behavior) adding `timeout-minutes: 10` to it
   for consistency with the new timeout-minutes pattern the other jobs now
   have — this is a nice-to-have, not required.

8. **Top-level additions**: add `permissions: contents: read`,
   `concurrency:` (exactly as shown — this cancels in-progress PR runs when a
   new commit is pushed to the same PR, but does NOT cancel push-to-main
   runs, which is correct/safe), and `defaults: run: shell: bash` at the
   workflow root, exactly as the template shows.

## Verification (do not skip)

- `python3 -c "import yaml; yaml.safe_load(open('.github/workflows/ci.yml'))"`
  — valid YAML.
- Locally run every command that appears in each job's `run:` steps against
  this repo (build, vet, the new coverage command, the integration test
  command against a real local Postgres, golangci-lint, spectral lint) —
  confirm every single one exits 0 BEFORE pushing. This is the same bar as
  every prior task in this workspace: do not trust that a YAML file "looks
  right", prove the actual commands work.
- Push (following this repo's established branch convention — check
  `git log`: all four repos are currently on `main` with no open PRs as of
  this task, so push directly to `main` unless you find otherwise) and use
  `gh run watch` to confirm ALL FIVE jobs (lint, test, integration,
  openapi-lint — plus confirm mutation correctly stays skipped on a push
  event) pass for real on GitHub's actual runners. This is the strongest
  verification and is required, not optional, given `gh` is authenticated
  and available.

## Definition of done

- `.github/workflows/ci.yml` restructured per the template, all 7
  adaptations above applied correctly and explicitly reasoned about (not
  just copy-pasted).
- Every job's commands verified locally before push.
- Pushed to `main`, and `gh run watch` confirms lint/test/integration/
  openapi-lint all green on GitHub's real runners, mutation correctly
  skipped.
- Report: what changed, the gremlins version you pinned and why, whether
  this repo's integration tests self-migrate or needed the extra migration
  step, and confirmation all five jobs are green on GitHub.
