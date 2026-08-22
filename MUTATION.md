# Mutation Testing — Domain Layer

Tool: [gremlins](https://github.com/go-gremlins/gremlins) v0.6.0, scoped to
`internal/domain/...` only (aggregate invariants — the highest-signal target
for mutation testing in this codebase, per QUALITY.md Stage 4).

## Command used

```sh
gremlins unleash ./internal/domain --workers 1 --timeout-coefficient 30
```

The first run with default flags (`gremlins unleash ./internal/domain`)
reported 18/31 mutants as `TIMED OUT` rather than `KILLED`/`LIVED`. This was a
tooling/environment artifact, not real gaps: gremlins runs `go test` once per
mutant in a fresh subprocess and, on this machine, the default per-mutant
timeout was too tight when several mutant test runs were scheduled
concurrently (`--workers` defaults to a value derived from `GOMAXPROCS`). The
underlying test suite itself is fast (`go test ./internal/domain/...`
completes in well under a second). Serializing mutant execution
(`--workers 1`) and widening the per-mutant timeout budget
(`--timeout-coefficient 30`, up from the default) eliminated every timeout
and produced a clean kill/live verdict for all 31 discovered mutants.

## Results

```
Killed: 31, Lived: 0, Not covered: 0
Timed out: 0, Not viable: 0, Skipped: 0
Test efficacy: 100.00%
Mutator coverage: 100.00%
```

- **Mutator coverage: 100%** — every mutant gremlins could generate in
  `internal/domain/...` landed on a line reachable by the existing test
  suite (0 "not covered").
- **Test efficacy: 100%** — of the mutants actually exercised, all 31 were
  killed by an existing assertion; 0 survived.

Mutants were generated across `internal/domain/package`,
`internal/domain/station`, and `internal/domain/task` using the
`CONDITIONALS_NEGATION`, `CONDITIONALS_BOUNDARY`, `ARITHMETIC_BASE`, and
`INVERT_NEGATIVES` mutators (gremlins' defaults). `internal/domain/shared` is
pure value objects/constructors with no conditional branches for these
mutators to target, so it contributed no mutants.

## Survived mutants

None. Every mutant gremlins discovered was killed, so there is nothing to
triage this round — no equivalent mutants, no untested boundaries found. This
reflects the Stage 2 coverage work: the failing-path table-driven tests added
for at-most-once claim, capability mismatch, lease expiry, no-double-complete,
and SLAM weight-diversion happen to also pin down the exact boundary/negation
conditions gremlins mutates (e.g. `>` vs `>=` on `WeightTolerance`, `==` vs
`!=` on status checks, `&&`/`||` composition in `IsAvailable` /
`ExpireLeaseIfDue`).

If a future change to the domain layer introduces a survived mutant, treat it
per QUALITY.md Stage 4: add a test if it reveals an untested behavior, or
document why it's equivalent/not worth chasing if not.
