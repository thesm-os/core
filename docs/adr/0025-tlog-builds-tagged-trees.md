---
adr: 0025
title: tlog Builds Tagged Trees
status: Accepted
date: 2026-09-27
supersedes: none
superseded-by: none
---

# ADR-0025: tlog Builds Tagged Trees

## Status

Accepted

## Context

`crypto.Hasher.CombineTagged` hashes two digests under a binary role,
so that a protocol can build trees whose leaves and interior nodes never
collide. Core had no function that builds such a tree. `tlog` built only
the tree of RFC 9162, whose node prefix `0x01` is a unary role, because
C2SP witnesses verify only those bytes.

A protocol that commits to more than one tree over one hasher gives each
tree a node role of its own. No interior node of one tree then equals an
interior node of another tree, or of an RFC 9162 tree. One such protocol
needs proofs over three trees:

| Tree | Proof |
|---|---|
| The 64 KiB chunks of a payload | A reader proves one chunk against the payload's digest |
| The parents of an entry | An auditor proves one parent |
| A batch of up to 1,000 checkpoints | Each checkpoint contains its path in the batch |

`tlog` implements RFC 9162's split, audit path and verification, and
tests them against roots and proofs recorded from
`golang.org/x/mod/sumdb/tlog`. The split, the path and the verification
work with any node hash, so each of the protocol's trees is an RFC 9162
tree with another node hash.

With SHA-256 on an AMD Ryzen 9 9950X3D and Go 1.27.1, one path over
1,000 leaves takes 115 µs, because a path costs about one node hash per
leaf. The paths of a batch of 1,000 checkpoints take 111 to 115 ms.

## Decision

We will build tagged trees in `tlog`, as `TaggedRoot`,
`TaggedInclusionProof`, `VerifyTaggedInclusion` and a `TaggedTree` that
keeps every node, because `tlog` already implements RFC 9162's split,
path and verification against recorded vectors, and a batch needs every
path of its tree.

## Alternatives Considered

### A copy in each protocol

A protocol copies `InclusionProof` and `VerifyInclusion` and replaces
the node hash.

Rejected. Every copy is another implementation of the path and its
verification, and only core's is tested against recorded vectors.

### A tree interface over a node hash

A `Tree` interface, or a tree type parameterised by its node hash, would
serve RFC 9162's tree and the tagged trees.

Rejected. An interface admits trees whose roots a verifier rejects. The
node role is the only part that differs between the trees, so a `Role`
parameter is enough.

### The functions in package crypto

`TaggedRoot` beside `Role` and `CombineTagged`.

Rejected. `crypto` cannot import `tlog`, which imports it, so a tree in
`crypto` needs a second copy of the split and the path.

### One path per call

`TaggedInclusionProof` alone, as `InclusionProof` serves RFC 9162's
trees in memory.

Rejected. The paths of a batch of 1,000 checkpoints take 111 to 115 ms.
A `TaggedTree` hashes each of the batch's 999 interior nodes once and
returns all 1,000 paths in 148 µs.

### Shared loops with the RFC 9162 functions

`Root` and `TaggedRoot` share one fold, and `VerifyInclusion` and
`VerifyTaggedInclusion` share one loop. Each takes the node hash as a
function value or as a type parameter.

Rejected after measurement. With a function value, `Root` over 4,096
leaves and `VerifyInclusion` each took 5.9% longer. With a type parameter
they took 7.3% and 6.6% longer. The tagged functions have their own fold
and loop, which call `CombineTagged` directly. They share the split, the
leaf ranges of the path and the verifier's shift with the RFC 9162
functions.

## Consequences

**Positive:**

- A protocol builds, proves and verifies tagged trees with core's tested
  split, path and verification.
- A `TaggedTree` returns every path of a batch of 1,000 leaves in
  148 µs. Reused for batches of at most the same size, it allocates
  nothing.
- `VerifyTaggedInclusion` returns `ErrProof` for a proof hash whose size
  differs from the leaf's, so a proof from an untrusted source never
  makes `CombineTagged` panic.
- `Root`, `InclusionProof` and `VerifyInclusion` are unchanged.

**Negative:**

- `tlog` contains two kinds of tree. C2SP witnesses verify RFC 9162's
  tree under SHA-256. Only a protocol's own verifiers check a tagged
  tree.
- The fold and the inclusion loop exist twice, once per node hash. Tests
  keep the copies equal: a test hasher whose `CombineTagged` returns RFC
  9162's node hash turns a tagged tree into the recorded tree, and the
  tagged functions must match the recorded roots and proofs through it.
- A `TaggedTree` keeps about twice as many digests as leaves: 2,001
  digests of 65 bytes for 1,000 leaves.
- A tagged tree has no consistency proof. Adding one is a function
  beside `ConsistencyProof`.

**Neutral:**

- The `tlog` package documentation describes both kinds of tree.

## References

- RFC-0029, domain-separated tree hashing.
- RFC-0032, transparency log trees.
- ADR-0013, tagged tree hashing on the hasher interface.
- ADR-0019, transparency log trees use RFC 6962's bytes.
- ADR-0026, a tagged tree over no leaves has no root.
- RFC 9162, section 2.1, <https://www.rfc-editor.org/rfc/rfc9162.html>.
- `tlog/tagged.go` and `tlog/tagged_test.go`.
