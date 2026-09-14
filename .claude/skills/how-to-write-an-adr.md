# How to write an ADR

Use when a change is architecturally significant — a new bounded-context
integration, a reversal of a prior decision, a cross-repo contract change,
or anything a future reader would otherwise have to reverse-engineer from
the diff. Not every change needs one: a bug fix or a routine feature
addition inside an already-decided architecture doesn't.

## Numbering and location

`docs/docs/adr/NNNN-kebab-case-title.md`, four-digit zero-padded,
sequential — check the highest existing number
(`git ls-tree --name-only origin/develop -- docs/docs/adr/` and pick the
next integer, never reuse or guess). This repo is currently at ADR-0024
(`0024-station-location-code-and-workcenter-role-check.md`) — the next
one is 0025. `docs/docs/adr/index.md` explains the format to readers AND
carries the full numbered table; add your new row there too (step 5
below) or the sidebar/table silently omits the new record even though
the file itself renders fine.

## Frontmatter (Docusaurus needs all five fields)

```yaml
---
id: NNNN-kebab-case-title
slug: /adr/NNNN-kebab-case-title
title: "NN. Title (a short noun phrase, matching the heading)"
sidebar_label: "NN. Short label for the nav sidebar"
sidebar_position: NN
description: "One or two sentences — this shows up in search and link
  previews, so make it stand alone without the rest of the doc."
---
```

`id`/`slug` are the full kebab-case filename (minus `.md`); `title`/
`sidebar_label` repeat the number as plain text (`"17. ..."`, not `#17`);
`sidebar_position` is the bare integer. Getting these inconsistent is the
most common cause of a broken sidebar entry or 404 after merge — verify
by running the docs build (see below) before opening the PR.

## Format: Michael Nygard's template

```markdown
# NNNN. Title (a short noun phrase)

## Status
Accepted | Proposed | Deprecated | Superseded by ADR-XXXX

## Context
The forces at play — technical, business, constraints — that make this
decision necessary. Write in the past tense, as if explaining to someone
who wasn't there. State the alternatives seriously considered, not just
the one chosen; a reader six months from now needs to know a simpler
option was weighed and rejected, not assume nobody thought of it.

## Decision
What was actually decided, stated as an active, present-tense
declaration ("we will...", not "we might..."). Be specific about the
mechanism, not just the intent — this section should let a reader
implement the same decision from scratch without asking follow-up
questions.

## Consequences
What becomes easier, what becomes harder, and what future work this
creates or forecloses. Be honest about the downsides — an ADR that only
lists benefits reads as marketing, not a decision record.
```

The `## Decision` section is the part worth the most editing effort: see
ADR-0017 (`docs/docs/adr/0017-process-path-catalogue-as-configuration.md`)
for a model example — it states the exact mechanism (a boot-time YAML
catalogue replacing a path_id-prefix guess), names the fail-loud-at-boot
design explicitly, includes real code snippets from the actual files
touched (`pathcatalog.Catalogue`, `filecatalog.Load`,
`consumer.go`'s `HandleMessage`), and is specific enough that a reader
could implement the same decision from scratch. Also note ADR-0017's
"Addendum" section, added after merge once an exact-match bug was found in
production reasoning (`MatchPrefix` family-matching vs. exact string
match) — an accepted ADR can gain a dated addendum documenting a
correction without violating immutability; it does not rewrite the
original Decision text.

## Superseding an earlier ADR

Don't edit the old ADR's Decision section. Add a `## Status` line noting
`Superseded by ADR-XXXX` on the OLD one (a one-line patch), and open the
new ADR referencing it. See ADR-0022
(`0022-remove-rest-mcp-auth.md`) for the exact wording pattern
superseding ADR-0021's REST identity decision — a real supersession this
repo already carries, not a hypothetical.

## Cross-repo decisions: use a companion ADR, not one repo's private opinion

When a decision genuinely spans two bounded-context repos (e.g. this
service's process-path catalogue in ADR-0017, which wes-work-planning and
workforce-management independently mirror against the same
`warehouse-infra`-published YAML file), write ONE ADR per repo, each
referencing the other explicitly as the companion decision with a
one-line description of the split of responsibility — ADR-0017's own
Decision section states plainly "wes-work-planning and
workforce-management mirror this same port + loader shape against the
identical YAML file, in their own follow-up PRs — this ADR documents the
pattern; each repository's own ADR cross-references this one rather than
duplicating the reasoning." Don't write the decision once in one repo and
expect the other repo's readers to find it; each bounded context's docs
site is read independently.

## After writing: regenerate and verify the docs build

```bash
cd docs
npm ci
npm run build   # onBrokenLinks / onBrokenAnchors are both 'throw' — this
                 # WILL fail if the frontmatter/slug is wrong or a
                 # cross-reference link is broken
```

A broken ADR link or malformed frontmatter fails the build with a clear
Docusaurus error, not a silent 404 — always run this locally before
opening the PR. This repo's `.github/workflows/docs.yml` deploys the
docs site but does not currently gate ADR frontmatter errors on the PR
itself the way `docs-api-drift` gates OpenAPI drift — running the build
locally is the only check catching this before merge.
