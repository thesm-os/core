---
adr: 0005
title: The Primitive Set Is Chosen for Coherence
status: Accepted
date: 2026-08-03
supersedes: none
superseded-by: none
---

# ADR-0005: The Primitive Set Is Chosen for Coherence

## Status

Accepted

## Context

`core` is the standard library of the thesmos ecosystem. Two
incompatible bars have been applied to it in practice.

RFC-0007 §C and RFC-0008 §D each declined to ship a primitive on the
grounds that it had no demonstrated producer — "declarations without
producers". Both were right about the specific proposals, but the
reasoning generalises into a demand-driven bar: a primitive enters
`core` when enough callers have converged on needing it.

That bar is wrong for a foundation, and the reason is timing. A
primitive that arrives after convergence arrives after every caller has
already invented a spelling, and those spellings are not private
implementation details — they are wire formats, persisted values, and
column types that outlive the decision to standardise. The divergence
that happens in the interval is permanent in a way the eventual
standard is not.

The counter-consideration is real and is recorded here rather than
elsewhere: Go's own standard library accreted, and its best contracts
(`io.Reader`, `fs.FS`, `context.Context`, `slog.Handler`) were all
extracted after their shape was proven in the wild. Its worst
decisions were the a priori ones — `net.Error.Temporary()`, deprecated
as "not well-defined", is the closest analogue to anything `core`
proposes.

The distinction that resolves it: Go's compatibility promise makes a
wrong stdlib shape permanent. `core` is pre-1.0 and versioned, so a
wrong shape is correctable until it is tagged. The window in which
coherence is cheap is exactly the window `core` is in now.

## Decision

`core`'s primitive set is chosen for coherence of the layer model, not
per-item demonstrated demand. A primitive belongs in `core` when its
contract is right and its layer has a hole, whether or not a caller has
asked for it.

This supersedes the demand-driven bar implied by RFC-0007 §C and
RFC-0008 §D as a general rule. Those RFCs' specific rejections stand;
their stated reasoning does not generalise.

The bar that replaces it has three parts. A primitive enters `core`
when: its contract admits one correct shape rather than a policy
choice; it is expressible over the standard library alone; and it ships
with a conformance suite that makes the contract enforceable rather
than aspirational.

## Alternatives Considered

### Demand-driven: wait for consumer convergence

Rejected. Convergence is exactly what does not happen — each consumer
converges on its own spelling, and by the time three exist the cost of
unifying them is three migrations of persisted data, not one API
change. The interval is where the damage is done.

### Coverage-driven: fill every layer completely

Rejected. There is no stopping point. "Distributed services need X" can
be argued for a scheduler, a config loader, a feature-flag seam and a
money type, each defensibly. The coherence bar is a layer model with
edges, not an ambition to be complete.

### Accretion, following Go's own history

Rejected as the governing rule, accepted as a constraint on pace. Go
accreted under a permanent compatibility promise, which made extraction
the only safe order. `core` is pre-1.0 and can correct a shape before
it is tagged. What survives from this alternative is the sequencing
discipline in ADR-0008 and the conformance requirement above: land
primitives one at a time, each with its suite, rather than as a batch.

## Consequences

**Positive:**

- Wire formats and persisted values across the ecosystem are decided
  once, in one place, before they are written rather than after.
- The layer model gives a principled answer to "does this belong in
  `core`" that is not "has anyone asked".
- Consumers adopting `core` late still find the contract they would
  have needed early.

**Negative:**

- A larger pre-1.0 surface than a demand-driven `core` would have, and
  therefore more that can be wrong. Every primitive shipped under this
  ADR is a shape that must be lived with or breakingly corrected.
- Primitives will ship with no in-tree consumer, so the first real use
  is also the first real test of the contract. The conformance-suite
  requirement is the mitigation and it is not a complete one.
- Reviewer effort concentrates in RFCs rather than in code review,
  because the contract is the artefact and the implementation is
  usually small.

**Neutral:**

- The three-part bar is itself a judgement call per proposal. It
  narrows the argument; it does not mechanise it.
