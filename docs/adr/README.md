# adr/

Architecture Decision Records for `core`. One file per architectural
decision significant enough to be remembered years later.

## Format

[Michael Nygard's ADR format][nygard], minimally extended with
frontmatter for searchability.

```
NNNN-short-name.md
```

- `NNNN` is a zero-padded monotonic counter. Never reused, never
  renumbered, never deleted. ADR-0042 stays ADR-0042 forever even if
  superseded or proven wrong.
- `short-name` is lowercase kebab, ~40 chars max. A human reading the
  filename should understand the decision it covers.

## Status values

- `Proposed` — drafted, not yet accepted. Rare in this flow (proposals
  typically live in `rfc/`; ADRs generally land with `Accepted` once
  the decision is made).
- `Accepted` — the current decision. Most ADRs live here.
- `Superseded` — a later ADR overrides this one. The superseded ADR
  stays on disk with its status updated and a pointer to the
  superseder in frontmatter.
- `Deprecated` — the decision no longer applies but no successor
  replaces it (the concern itself went away).

ADRs are NEVER edited in place once accepted. Supersede them with a
new ADR that explains the change; the original remains as historical
evidence of what was true at a point in time.

## Relationship to RFCs

An RFC that produces architectural decisions lands accompanied by one
or more ADRs. The RFC captures the debate and alternatives; each ADR
captures ONE decision crisply.

Decisions small enough to skip the RFC stage (single load-bearing
call, no plausible alternatives worth side-by-side comparison) go
straight to ADR.

## Template

Start from [`docs/templates/ADR.md`](../templates/ADR.md).

## Index

| ADR | Title | Status |
|-----|-------|--------|
| [0000](0000-record-architectural-decisions.md) | Record Architectural Decisions | Accepted |
| [0001](0001-stdlib-only-dependencies.md) | Stdlib-Only Dependencies | Superseded by [0006](0006-stdlib-only-scope-test-dependencies.md) |
| [0002](0002-single-module-layout.md) | Single Module Layout | Accepted |
| [0003](0003-apache-2-0-with-spdx-headers.md) | Apache 2.0 with SPDX Short-Form Headers | Accepted |
| [0004](0004-library-only-release-via-plain-tags.md) | Library-Only Release via Plain Tags | Accepted |
| [0005](0005-primitive-set-chosen-for-coherence.md) | The Primitive Set Is Chosen for Coherence | Accepted |
| [0006](0006-stdlib-only-scope-test-dependencies.md) | Stdlib-Only Scope: Test Dependencies | Accepted |
| [0007](0007-zero-digest-is-valid-chain-genesis.md) | The Zero Digest Is a Valid Chain Genesis | Accepted |
| [0008](0008-core-defines-contracts-that-describe-io.md) | Core Defines Contracts That Describe IO | Accepted |
| [0009](0009-logging-is-log-slog.md) | Logging Is log/slog | Accepted |
| [0010](0010-one-package-name-one-concept.md) | One Package Name, One Concept | Accepted |
| [0011](0011-test-doubles-named-for-behaviour.md) | Test Doubles Are Named for Their Behaviour | Accepted |

[nygard]: https://cognitect.com/blog/2011/11/15/documenting-architecture-decisions
