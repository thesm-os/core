---
adr: 0019
title: Transparency Log Trees Use RFC 6962's Bytes
status: Accepted
date: 2026-09-24
supersedes: none
superseded-by: none
---

# ADR-0019: Transparency Log Trees Use RFC 6962's Bytes

## Status

Accepted

## Context

The `tlog` package implements the Merkle tree of RFC 9162 and stores it
as the tiles of C2SP tlog-tiles.

Core's tagged hashing reserves the high bit of a role for arity. Roles
`0x00` to `0x7F` are unary and accepted only by `Hasher.HashTagged`.
Roles `0x80` to `0xFF` are binary and accepted only by
`Hasher.CombineTagged`. So a leaf hash cannot equal a node hash.

RFC 6962, section 2.1, hashes a leaf as `HASH(0x00 || data)` and an
interior node as `HASH(0x01 || left || right)`. RFC 9162 keeps those
prefixes. `0x01` is in the unary half, so `CombineTagged` refuses it.

The transparency-log ecosystem verifies exactly those bytes:

- C2SP tlog-checkpoint defines the signed root as "the root of the RFC
  6962 Merkle hash tree at the specified tree size".
- C2SP tlog-witness requires a consistency proof "according to RFC
  6962, Section 2.1.2".
- C2SP tlog-tiles serves each tile at `tile/<L>/<N>[.p/<W>]` and fixes
  a full tile at 256 hashes. Full tiles are immutable, and their caching
  headers "SHOULD be long-lived".

## Decision

We will hash `tlog`'s leaves and nodes with RFC 6962's prefixes `0x00`
and `0x01` and store its tiles and entry bundles in the C2SP layout,
because independent witnesses verify only those bytes.

`NodeHash` writes both children into one buffer and calls `HashTagged`
with role `0x01`. The result is RFC 6962's node hash, computed through
core's hasher interface.

## Alternatives Considered

### Tagged roles for the node hash

Hash interior nodes with `CombineTagged` and a binary role. Core's own
chains and trees hash their nodes this way.

Rejected. The bytes would not be RFC 9162's. No C2SP witness could
verify a consistency proof over the tree, and every verifier would need
core's role assignments.

### A Merkle Mountain Range

Keep a list of perfect subtrees, with the peaks as incremental state.

Rejected. An MMR is not RFC 9162 and has no witness ecosystem or
standard proof format. Tiles give an RFC 9162 tree the same constant
writer state, and a proof reads at most two tiles per tile level.

### Import `golang.org/x/mod/sumdb/tlog`

The Go checksum database runs on it, and it implements RFC 6962 trees,
proofs and tiles.

Rejected. Its module requires `golang.org/x/tools`, and core admits
only golang.org/x modules without requirements. Its `Hash` is
`[32]byte`, so it cannot represent a SHA-384 tree. Its tile paths include
the tile height, `tile/H/L/N`, which C2SP omits.

### A tree interface

Define a `Tree` interface and let each implementation choose its
shape.

Rejected. An interface admits trees whose roots a verifier rejects.
Storage is where implementations differ, so `TileReader` is the seam.

## Consequences

**Positive:**

- With SHA-256, a C2SP witness verifies a tree built by core, and the
  bytes match `golang.org/x/mod/sumdb/tlog`. `tlog/testdata/vectors.txt`
  records that module's roots and digests of its proofs and tiles, and
  the tests compare against them.
- The tile bytes, tile paths and entry bundles follow a published
  standard, so they are frozen by the standard as well as by core's
  rule for persisted encodings.

**Negative:**

- `tlog` is the one place in core where a two-operand hash uses a unary
  role. A protocol that assigns role `0x00` or `0x01` over the same
  hasher can produce bytes that a `tlog` tree also produces. It must
  separate its hashes from `tlog`'s with a `crypto.Domain` or a
  `crypto.Framer`, as it separates any two protocols.
- A tree over a hasher other than SHA-256 is RFC 9162 with a different
  hash. C2SP witnesses do not verify it.
- `NodeHash` through the hasher interface costs 113 to 119 ns, against
  79 ns for `crypto/sha256.Sum256` over the same 65 bytes. The bulk
  paths hash through one borrowed stream per call instead.

**Neutral:**

- Core's arity rule is unchanged for every other caller of
  `HashTagged` and `CombineTagged`.

## References

- RFC-0032, transparency log trees.
- RFC-0029 and ADR-0013, tagged tree hashing.
- ADR-0015, dependency-free golang.org/x modules.
- ADR-0016, persisted encodings are frozen.
- RFC 6962, section 2.1, <https://www.rfc-editor.org/rfc/rfc6962.html>.
- RFC 9162, sections 2.1.1 to 2.1.4,
  <https://www.rfc-editor.org/rfc/rfc9162.html>.
- C2SP tlog-tiles, tlog-checkpoint and tlog-witness,
  <https://c2sp.org/tlog-tiles>.
- `golang.org/x/mod` v0.40.0: `go.mod` and `sumdb/tlog/tile.go`.
