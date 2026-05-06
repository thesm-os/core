---
adr: 0000
title: Record Architectural Decisions
status: Accepted
date: 2026-05-06
supersedes: none
superseded-by: none
---

# ADR-0000: Record Architectural Decisions

## Status

Accepted

## Context

`core` is the foundation that every thesmos library depends on, and
its interfaces become contracts the moment they're published. The
"why" behind those contracts has to be recoverable years after the
decision is made — otherwise future contributors either re-litigate
old ground or accidentally reverse a load-bearing choice without
realising it.

We need a lightweight record of every architectural decision: what
was decided, when, and why.

## Decision

We will use Architecture Decision Records, following Michael Nygard's
format, for every architectural decision that someone might later
reasonably ask "why?" about.

- ADRs live in `docs/adr/`.
- Filenames are `NNNN-short-name.md` with a zero-padded monotonic
  counter.
- Each ADR follows the template in `docs/templates/ADR.md`: Status,
  Context, Decision, Alternatives, Consequences.
- ADRs are immutable once accepted. To change a decision, write a new
  ADR that supersedes the old one. Update the old ADR's `status`
  frontmatter to `Superseded` and its `superseded-by` to the new
  ADR's number.
- The counter starts at ADR-0000 (this document) and never resets.

## Alternatives Considered

### No formal decision records

Rely on PR descriptions and Slack threads. Rejected: knowledge
evaporates when people leave; a year-old Slack thread is not
discoverable by a new contributor.

### RFCs only (no separate ADRs)

Record decisions inside the RFC that produced them. Rejected: an RFC
captures debate and alternatives — it's long. An ADR captures one
decision crisply. Searching for "what did we decide about X" is
faster when the answer is a 30-line ADR than buried in page 7 of an
RFC.

### Wiki-based decision log

Use a shared wiki (Notion, Confluence). Rejected: wikis drift from
the code. Files in `docs/adr/` are reviewed in the same PR as the
code they govern, versioned in the same repo, and searchable with
`grep`.

## Consequences

**Positive:**

- Future contributors understand the reasoning behind load-bearing
  decisions without interviewing the people who made them.
- Decisions are searchable: `grep -r "why did we" docs/adr/` is a
  legitimate knowledge-retrieval strategy.
- "We already considered that and rejected it" becomes a recoverable
  claim with evidence.
- ADRs pair naturally with RFCs (`docs/rfc/`): an RFC is the
  proposal + discussion; an ADR is the resulting decision.

**Negative:**

- Architecturally significant PRs now carry a small doc-writing
  overhead. Worth it.
- Maintainers must agree on "what counts as significant." We err on
  the side of writing the ADR when in doubt.

**Neutral:**

- ADRs are not specs and not RFCs. Setup decisions, conventions, and
  obvious tool choices do not need ADRs.
