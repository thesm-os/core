---
adr: 0013
title: Tagged Tree Hashing on the Hasher Interface
status: Accepted
date: 2026-08-15
supersedes: ADR-0007
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0013: Tagged Tree Hashing on the Hasher Interface

## Status

Accepted

Supersedes [ADR-0007](0007-zero-digest-is-valid-chain-genesis.md) on
two of its grounds: the arithmetic premise on which it rejected an
operand prefix is false, corrected below, and the method that premise
governed no longer exists. ADR-0007's sentinel is superseded by
[ADR-0014](0014-genesis-sentinel-is-deleted.md); its wire-form rule
survives there unchanged.

## Context

RFC-0029 established the defect. `Hash(data)` is `H(data)` and
`Combine(l, r)` is `H(l ‖ r)` over the same function, so a
caller-chosen payload of exactly two digest widths hashes to a
legitimate interior node. Anyone holding two sibling digests from an
honest proof can present their concatenation as a leaf, serve a shorter
authentication path, and have an entry that was never written verify.
The attack needs no collision search. RFC 6962 §2.1 closed this class
for certificate transparency in 2013 with a distinct byte before leaves
and before nodes.

Two questions inside that fix are architectural rather than
implementation detail, and both were contested during review.

**Where the operation lives.** This module has already reasoned about
escape analysis once, in `HashDomain`'s pooled length buffer: a slice
written through `Stream.Write` escapes, because the compiler cannot see
through an interface method. A free function over `Hasher` would escape
both operands of a combine — two heap allocations on the operation a
Merkle tree performs N−1 times per batch — where a method on the
concrete hasher builds a stack buffer and allocates nothing.

**How role disjointness is guaranteed.** A caller-assigned prefix does
not close the defect by itself. `HashTagged(t, l ‖ r)` and
`CombineTagged(t, l, r)` are the same bytes, so a protocol assigning
one role to both a leaf and a node has the original attack back. A rule
instructing protocol designers to keep them disjoint is precisely the
documentation-only defence this change exists to replace.

ADR-0007 rejected framing `Combine`'s operands with a leading byte
because it "pushes the input past 64 bytes and destroys the
single-block property `Combine` is built around". That premise is
arithmetically false. SHA-256 padding appends `0x80` and an eight-byte
length (FIPS 180-4 §5.1.1), so a 64-byte input needs 73 bytes of
message plus mandatory padding, forcing a 128-byte padded length and
two compressions; a 65-byte input pads to the same 128. There was never
a single-block property to destroy, and the same holds across every
hasher this module ships.

## Decision

`crypto.Hasher` carries the tagged operations as methods, and `Combine`
is deleted:

```go
HashTagged(r Role, data []byte) Digest
CombineTagged(r Role, left, right Digest) Digest
```

`Role` is a `uint8` whose high bit is reserved by the mechanism, not by
convention: roles `0x00`–`0x7F` are unary and accepted only by
`HashTagged`; roles `0x80`–`0xFF` are binary and accepted only by
`CombineTagged`; each method panics on the other half. A leaf role and
a node role cannot share a first byte, so the cross-arity second
preimage is unrepresentable rather than forbidden.

The layout is `H(r ‖ operands)` with no length framing — the role is
one byte and a hasher's digest width is fixed, so there is nothing
variable to frame. `core` ships no `Role` constants, as it ships no
domain constants for `HashDomain`: the vocabulary belongs to the
protocol.

`Hash` survives unchanged for content addressing, where the address
must be the digest of the bytes and nothing else.

## Alternatives Considered

### Free functions over `Hasher`

`HashTagged(h Hasher, r Role, data []byte) Digest` beside the
interface, additive and implemented once.

Rejected. Writing `left.Bytes()` to `Stream.Write` escapes the `Digest`
the slice points into, so a combine costs two heap allocations where
the method form costs zero. The interface grows by two methods across
six implementations once; the helper taxes every call forever.

### A full `Domain` per hash instead of a byte

Prefix each hash with the `Domain{Name, Version}` vocabulary, so tree
hashing inherits cross-protocol separation and versioning.

Rejected. It prices an interior node at a name, a version and their
framing to separate protocols that a tree's own operands cannot reach.
Leaves enter a tree from one protocol; the confusion being closed is
between roles inside it. A protocol needing cross-protocol separation
composes `Framer` where its data enters, once.

### Reuse `HashDomain`

`HashDomain(h, domain, left.Bytes(), right.Bytes())`, needing no
interface change at all.

Rejected. It length-prefixes every part, adding sixteen bytes of
framing per interior node to disambiguate two operands of fixed, equal,
hasher-determined width, and it escapes for the reason above. Framing
is right for variable parts and wrong for a pair of digests.

### RFC 6962's shape — fixed `0x00` and `0x01`

Bake two prefixes into the mechanism with nothing for a caller to
assign, as certificate transparency does.

Rejected as insufficient, not as wrong. Two values cannot distinguish
the roles one protocol holds: a chain link, a batch node and an
accumulator node are all interior and must not collide with each other.
What 6962 gets right is that its separation is structural rather than
disciplinary, and the arity bit keeps exactly that property — it is
6962's `0x00`/`0x01`, fixed and refused when wrong, while the remaining
seven bits carry a multiplicity 6962 never needed.

### Keep untagged `Combine` beside the tagged forms

The additive shape: add two methods, leave `Combine` in place for
consumers that may hold it.

Rejected. No such consumer can be named — `Combine` has no call site in
this repository outside its own implementations and their conformance
and bench surfaces — while its interface documentation actively
instructs the construction being closed. An out-of-module holder breaks
at compile time, which is the visible failure, pre-1.0, and is
regenerating its artefacts under this change anyway.

## Consequences

**Positive:**

- The forgery this decision exists to close is unconstructible rather
  than discouraged: the two operations cannot hash the same first byte,
  and the guard is a refusal every conformance suite can test.
- Domain separation costs zero additional compression invocations on
  the combine path of every hasher this module ships, so the security
  property is free where it is used most.
- A protocol expresses its own role vocabulary — chain link, batch
  node, accumulator node, fork — instead of borrowing a two-value
  scheme that cannot tell them apart.
- Genesis stops being a special case: a chain's first link is a unary
  role over one operand, which is what lets
  [ADR-0014](0014-genesis-sentinel-is-deleted.md) delete the sentinel
  rather than annotate it.

**Negative:**

- This is a breaking interface change. Six implementations in this
  module, the cryptotest stub and generated surfaces, and any
  implementation outside lose `Combine` and gain two methods; every
  artefact hashed under the old scheme must be regenerated.
- The arity reservation halves the role space to 128 per arity and
  costs one byte comparison per call.
- One disciplinary rule survives and cannot be mechanised: a unary role
  must be applied to inputs of one kind, or a caller controlling a
  digest-width payload holds a preimage of the other construction.
  Arity is a property of the call site and checkable at the boundary;
  what a role is applied to is a property of the registry and visible
  only in design review.
- Two ways to hash remain. Choosing `Hash` for a tree leaf is still
  expressible and still wrong — but the interior-node half of the
  confusion is gone, and a second preimage needed both halves.

**Neutral:**

- `Role` is intra-protocol. Cross-protocol separation remains `Domain`
  and `Framer`, composed at the boundary where data enters.
- Consumers that assigned binary roles below `0x80` renumber into the
  high half and regenerate their vectors, which is mechanical.
