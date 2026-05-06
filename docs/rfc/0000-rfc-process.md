---
rfc: 0000
title: RFC Process
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-05-06
updated: 2026-05-06
discussion: (self-hosted; this RFC is meta)
supersedes: none
superseded-by: none
produces-adr: ADR-0000
---

# RFC-0000: RFC Process

## Summary

Establish a Request-for-Comments process for architectural proposals
in `core`. Proposals live in `docs/rfc/`, follow the template in
`docs/templates/RFC.md`, and flow through a defined lifecycle from
Draft to one of four terminal statuses (Accepted, Rejected,
Withdrawn, Superseded).

## Motivation

`core` defines contracts that propagate to every thesmos library.
Proposals affecting these contracts need:

1. **Visibility.** A proposal that changes a `core` interface must be
   visible to every downstream consumer (`testkit`, `thesmos`,
   `space`, …) BEFORE the code lands, not after.
2. **Alternatives on the record.** When "why didn't we do X?" comes
   up a year later, the answer needs to be recoverable. Without
   RFCs, the answer is "we probably considered it, but who
   remembers."
3. **A durable record of rejected proposals.** Bad ideas recur. An
   on-disk "we considered this in RFC-0042 and rejected it because
   Y" shortens the debate when it recurs.
4. **Separation of discussion from decision.** ADRs are crisp,
   single-decision, immutable records. RFCs carry the debate:
   alternatives, drawbacks, open questions, trade-offs. An accepted
   RFC typically produces one or more ADRs.

## Detailed design

### Filename + location

`docs/rfc/NNNN-short-name.md`

- `NNNN` is a zero-padded monotonic counter, permanent across every
  status transition. Withdrawn RFCs keep their numbers.
- `short-name` is lowercase kebab, approximately 40 chars max,
  describing the proposal in a skimmable phrase.

### Frontmatter

Every RFC begins with YAML frontmatter:

```yaml
---
rfc: NNNN
title: Short human-readable title
author: Name
status: Draft | Review | Accepted | Rejected | Withdrawn | Superseded
created: YYYY-MM-DD
updated: YYYY-MM-DD
discussion: <link to PR / issue>
supersedes: RFC-NNNN | none
superseded-by: RFC-NNNN | none
produces-adr: ADR-NNNN, ADR-NNNN | tbd | none
---
```

### Body sections

Every RFC follows the template in `docs/templates/RFC.md`:

1. **Summary** — one paragraph, what's being proposed.
2. **Motivation** — what problem, why now, what breaks without it.
3. **Detailed design** — the proposal itself, with code / diagrams.
4. **Alternatives considered** — what else was evaluated and why
   not.
5. **Drawbacks** — honest accounting of what the proposal costs.
6. **Open questions** — things the RFC doesn't answer yet.
7. **Unresolved / future work** — noted but out of scope.

Sections can be combined or shortened where the topic is small, but
all seven are considered in every RFC. An RFC with no "Alternatives
considered" or "Drawbacks" sections is suspect.

### Lifecycle

```
Draft → Review → {Accepted, Rejected, Withdrawn}
Accepted may later be Superseded by a newer RFC.
```

- **Draft**: author is still writing. No PR open yet, or PR is in
  draft state.
- **Review**: author opens a PR titled `RFC-NNNN: title`. The PR
  body summarises the proposal; discussion happens in PR comments.
  Reviewers read the full RFC doc and comment against the specific
  sections.
- **Accepted**: RFC is merged as the agreed direction. The PR that
  merges it is the canonical discussion record. The author (or a
  named implementer) is responsible for producing the resulting ADRs
  as the implementation lands.
- **Rejected**: RFC is merged with `status: Rejected`. The Rejection
  reasoning goes into a new terminal "Rejection" section at the
  bottom of the RFC.
- **Withdrawn**: author pulls it before a decision. RFC is merged
  with `status: Withdrawn` and a brief "Why withdrawn" section.
- **Superseded**: a later RFC changes the direction. The old RFC's
  frontmatter `status` flips to `Superseded` and `superseded-by`
  points at the newer RFC; the new RFC's `supersedes` points back.

### Relationship to ADRs

RFCs answer "what and why"; ADRs answer "what did we decide."

- An Accepted RFC typically produces 1-5 ADRs, one per load-bearing
  decision extracted from the proposal. The RFC is the record of the
  debate; each ADR is the crisp record of one outcome.
- Small proposals (single decision, no plausible alternatives worth
  side-by-side comparison) skip the RFC stage and go directly to
  ADR.
- Repository hygiene, tooling, and conventions do not produce either
  RFCs or ADRs — they're just PRs.
- The `produces-adr` frontmatter field tracks this: `tbd` during
  Review, populated with the actual ADR numbers once they land.

## Alternatives considered

### A. No formal process; continue with PR descriptions

PR descriptions are ephemeral and rarely read post-merge. Within a
year, the "why" behind a decision is gone. Rejected.

### B. Only ADRs, no RFCs

ADRs are single-decision and immutable — they're the wrong shape for
multi-alternative proposals with open trade-offs. Squeezing a design
debate into an ADR produces either a bloated ADR or one that omits
the alternatives. Keep ADRs crisp; use RFCs for the debate. Rejected.

### C. Issues / discussions on the git host

Issues and discussions are not durable across git-host migrations
and are not version-controlled alongside the code they describe. A
proposal about `core` should live in the same repo as `core`.
Rejected.

### D. External RFC process (a separate `rfcs` repo)

Separate repos add friction (cross-repo PRs for the code that
implements an accepted RFC; separate permissioning). We're not at
the scale where this pays off. Reconsider if RFC volume grows beyond
~20 active proposals at once. Deferred.

## Drawbacks

- Overhead: writing an RFC takes time (typically 2-8 hours for a
  non-trivial proposal). Mitigated by the template + the "when to
  write an RFC" guidance in `docs/rfc/README.md`.
- Bureaucracy risk: the process could slow down work if applied to
  small changes. The "when to write an RFC" section explicitly
  carves out implementation details, bug fixes, naming cleanups,
  single-decision architectural calls, and repository hygiene to
  prevent this.

## Open questions

- **Minimum review period.** Do we require at least N business days
  between Review and Accepted? Current stance: no hard minimum;
  reviewer sign-offs are the gate. Revisit if merges feel rushed.
- **Sign-off requirements.** How many approving reviewers? Current
  stance: at least one CODEOWNER of every package the RFC touches.

## Unresolved / future work

- Automated RFC index generation when count grows past ~20.
- Tooling to detect `supersedes` / `superseded-by` consistency.
