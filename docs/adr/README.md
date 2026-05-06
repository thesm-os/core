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
| [0001](0001-stdlib-only-dependencies.md) | Stdlib-Only Dependencies | Accepted |
| [0002](0002-single-module-layout.md) | Single Module Layout | Accepted |
| [0003](0003-apache-2-0-with-spdx-headers.md) | Apache 2.0 with SPDX Short-Form Headers | Accepted |
| [0004](0004-library-only-release-via-plain-tags.md) | Library-Only Release via Plain Tags | Accepted |

[nygard]: https://cognitect.com/blog/2011/11/15/documenting-architecture-decisions
