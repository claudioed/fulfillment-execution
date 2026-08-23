---
id: 0006-arch-go-architecture-fitness-tests
title: 6. arch-go fitness tests to enforce the dependency rule
sidebar_label: 6. Architecture fitness tests
sidebar_position: 6
description: Encode the hexagonal dependency rule as executable Go tests with arch-go, and gate CI on them, rather than relying on code review.
---

# 6. arch-go fitness tests to enforce the dependency rule

## Status

**Accepted.** Added as Task 14 (`ARCH_TEST_TASK.md`), which also introduced the
blocking `arch-test` CI job and wired it into the `docker-publish` gate.

## Context

[ADR-0001](./0001-hexagonal-ports-and-adapters.md) established a dependency
rule and `CLAUDE.md` labels it **NON-NEGOTIABLE**. But until this decision, the
rule existed only as prose in a markdown file, enforced entirely by code
review.

That is a weak enforcement mechanism for a rule whose violations are
individually harmless and collectively fatal. Nobody plans to import `pgx` into
the domain. What happens is smaller: a use case imports a concrete repository
"just for this one type"; an HTTP handler reaches for a Postgres helper because
it is right there; a domain type grows a `json:` tag because it was convenient.
Each is a one-line diff that looks innocuous in review, and each is
individually easy to justify. The rule erodes by increments, and by the time it
is visibly broken the fix is a refactor rather than a revert.

Forces:

- **Go's compiler cannot express this.** Import cycles are rejected, but
  `domain → adapters` is a perfectly legal acyclic import. Nothing structural
  prevents it.
- **`internal/` gives no intra-module protection.** It stops external modules
  from importing these packages; it says nothing about which of *our own*
  packages may import which.
- **The properties the layering buys are the ones that erode first.** Testing
  invariants without infrastructure, swapping storage, running both inbound
  paths through the same rules — all of it depends on the rule holding
  everywhere, not mostly.
- **Review attention is a scarce resource.** Asking reviewers to check import
  direction on every PR spends it on something a machine does perfectly.
- **The JVM world already solved this** with ArchUnit, and the pattern —
  architecture as executable tests — is well proven.

### Alternatives considered

**`depguard` in golangci-lint.** Already available in the toolchain, and it can
forbid imports by package pattern. It works at the *linter* level with
per-package deny lists, which becomes verbose for a five-rule bidirectional
matrix, and it expresses "package X may not import Y" rather than
"this layer may only depend on that layer."

**A custom `go list`-based script.** Full control, zero dependencies, and a
bespoke tool nobody else maintains — with its own bugs and no community.

**Continue with code review.** Free, and demonstrably how the rule erodes.

## Decision

**We will encode the hexagonal dependency rule as executable Go tests using
[arch-go](https://github.com/arch-go/arch-go), and gate CI on them.**

`internal/architecture/architecture_test.go` — a test-only package, so nothing
in the production build depends on it — declares five subtests under
`TestHexagonalDependencyRules`:

| Subtest | Rule |
| --- | --- |
| `domain has no internal dependencies except domain` | `**.domain.**` may only depend on `**.domain.**` |
| `application depends only on domain` | `**.application.**` may only depend on `**.domain.**` and its own subpackages |
| `inbound adapters do not depend on outbound adapters` | `**.inbound.**` must not reach `**.outbound.**` |
| `outbound adapters do not depend on inbound adapters` | the mirror rule |
| `only cmd wires every layer together` | wiring stays in the composition root |

Rules are expressed as `configuration.DependenciesRule` values with
`ShouldOnlyDependsOn.Internal` patterns, checked via
`archgo.CheckArchitecture(moduleInfo, config)`.

A dedicated **`arch-test` CI job** runs them on every push and pull request,
and is listed in `docker-publish`'s `needs` — so an architecture violation
does not merely fail a check, it prevents an image reaching Docker Hub.

The second rule deserves a note: `**.application.**` permits the application's
own subpackages (`ports` and `usecases`) to depend on each other, because
`usecases` legitimately imports `ports`. The rule constrains what the layer may
reach *outward* to, not its internal cohesion.

All five passed on first run — the codebase had zero pre-existing violations,
which is the outcome that makes the rule worth locking in rather than
negotiating.

## Consequences

### Easier

- **The rule is enforced continuously and impersonally.** A violating import
  fails a build with a specific message, on the PR that introduced it, when the
  fix is one line.
- **Reviewers stop policing imports.** Attention moves to the things review is
  actually good at.
- **The architecture is discoverable from the code.** A newcomer can read five
  named subtests and know the layering, without trusting that a markdown file
  is current.
- **Refactors are safe.** Moving a package cannot silently break the layering.
- **It composes with the release gate.** Being in `docker-publish`'s `needs`
  makes the rule a shipping precondition, not advisory.

### Harder

- **A build-time dependency on a small third-party tool.** `arch-go` is not
  widely used the way ArchUnit is; if it stops being maintained the rules need
  reimplementing. Confined to a test-only package, so the blast radius is one
  file.
- **The glob syntax is subtle.** arch-go's patterns use `.` as a segment
  separator and translate to permissive regexes, so a pattern can match more
  than it appears to. Rules need verifying against the real module rather than
  being read as literal path globs.
- **Test files are outside the check.** arch-go loads packages with
  `Tests: false`, so a `_test.go` file crossing layers is not flagged. That is
  usually right — test code legitimately wires layers together — but it does
  mean the rule covers production imports only.
- **Rules must be maintained alongside the structure.** A new top-level layer
  needs a new rule, or it is silently unconstrained.
- **Full-module analysis on every run.** Five subtests each load the whole
  module. Fast at this size; it is not free.
- **It cannot catch semantic leakage.** A domain type gaining a `json:` tag
  imports nothing and passes every rule, while still coupling the domain to a
  wire format. The fitness tests enforce *direction*, not *purity*.
