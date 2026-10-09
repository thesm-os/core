---
adr: 0012
title: Storage Is Per-Kind; a Unified KV Seam Is Refused
status: Accepted
date: 2026-08-05
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0012: Storage Is Per-Kind; a Unified KV Seam Is Refused

## Status

Accepted

## Context

RFC-0022 proposed `store.KV`, a unified keyed-storage seam, and was
withdrawn before acceptance on its own analysis: `KV` is the
intersection of a relational store, an object store, and a cache,
and an intersection discards precisely what each member is good at.
The withdrawal left the replacement direction — per-kind seams,
each argued on its own terms — stated but undecided.

Two later findings independently reached the same conclusion. A
kind test over four discriminators (absence-normality, transfer,
addressing, mutability) sorts real storage into kinds that answer
all four distinctly; a unified KV leaves absence
consumer-dependent — a registry says error, a cache says normal —
which is the signature of an intersection, not a kind. And
foundation, designed without reference to RFC-0022, shipped the
intersection and then needed `Cache`, `VersionedCache`,
`VersionedStore`, *and* `Store` beside it: the union fissioned into
kinds under its own consumers' weight.

The question has now been answered identically from three
directions. What remains is recording the answer so it cannot
return as a surprise proposal.

## Decision

`core` ships storage as per-kind seams, each proposed by its own
RFC with its own laws and conformance suite: content-addressed
(`cas`, RFC-0027), named-and-streamed (`blob`), and change
observation (`watch`). A unified keyed-value seam is refused, and
the refusal is final for 1.x. Small-value use cases land on `blob`
with small bodies; the compare-and-swap vocabulary stays in
`version`, consumed by the kinds rather than by a kind of its own.

## Alternatives Considered

### A unified KV with capability sub-interfaces

RFC-0022's shape: one core interface, optional capabilities for
enumeration, streaming, prefixes.

Rejected by RFC-0022's own withdrawal: when most callers declare
the superset, the subset bought a name; and the kinds differ in
*contract* (whether absence is an error, whether values stream),
which capabilities cannot express — a capability adds methods, it
cannot change what an existing method means.

### KV as the smallest common seam, kinds layered above

Ship the intersection anyway as a lowest common denominator.

Rejected: the intersection's contract is underdetermined at exactly
the points that corrupt data when guessed — stale-write behaviour
and absence semantics land inside retry loops, and a wrong answer
there corrupts rather than fails. A seam whose central semantics
are consumer-dependent standardises the divergence it exists to
prevent.

### Defer again rather than refuse

Leave the question open for a future consumer to reopen.

Rejected: three independent derivations produced the same answer,
and an open question with a settled answer invites re-litigation at
RFC cost each time. Refusal is recorded as final for 1.x; a 2.x
proposal must supersede this ADR with new arguments, which is
exactly the bar re-opening should carry.

## Consequences

**Positive:**

- Each kind's laws are falsifiable in that kind's own terms —
  MUST-verify means something for `cas` and nothing for a cache —
  so conformance suites assert contracts instead of hedges.
- Adapters implement the kind they actually are. An object store
  is not asked to pretend absence is normal; a cache is not asked
  to pretend it is an error.
- The refusal closes RFC-0022's withdrawal into a decision, and
  future proposals inherit the kind test as their admission bar.

**Negative:**

- A consumer wanting exactly "small versioned values under my own
  keys" has no dedicated seam and uses `blob` with small bodies,
  paying the streaming shape (an `io.Reader` wrap) for values that
  fit in memory.
- Finality binds 1.x: if a genuine kv-shaped kind emerges — one
  with distinct answers to all four discriminators — admitting it
  costs a supersession of this ADR, not just an RFC.

**Neutral:**

- The kind test itself is a judgement aid, not a mechanised gate;
  it narrows the argument a proposal must win, as ADR-0005's bar
  does for primitives generally.
