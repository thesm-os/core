---
adr: 0026
title: A Tagged Tree Over No Leaves Has No Root
status: Accepted
date: 2026-09-27
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0026: A Tagged Tree Over No Leaves Has No Root

## Status

Accepted

## Context

RFC 9162 defines the root of a tree over no leaves as HASH() over no
input, and `tlog.Root` returns it. A tagged tree separates its interior
nodes from the nodes of other trees by its node role, but HASH() has no
role byte. HASH() is the same value in three places:

- The empty tagged tree under every node role.
- The empty RFC 9162 tree over the same hasher.
- `Hash(nil)`, the content address of zero bytes.

A protocol has trees that cannot be empty, such as a batch or the
parents of an entry, and trees that can, such as one bucket of a
materialised view. What an empty tree commits to is protocol policy, as
the node roles are.

## Decision

We will make `TaggedRoot`, `TaggedTree.Reset` and `TaggedTree.Root`
panic for a tree over no leaves, because every root of a tagged tree is
a leaf or a node hashed under its role, and no value for an empty tree
is either.

## Alternatives Considered

### HASH() over no input

The root that RFC 9162 defines for an empty tree, which `Root` returns.

Rejected. Every empty tagged tree would have the root of every other
empty tagged tree, of the empty RFC 9162 tree and of the content address
of zero bytes. A protocol that commits to that root commits to a value
that other constructions also produce.

### An error return

`TaggedRoot` returns an error, such as `ErrRange`, for no leaves.

Rejected. Only the caller's own logic produces an empty tree, so every
call site would handle an error that it can rule out. Core's hashing
panics on such a precondition violation, as `CombineTagged` does for a
unary role.

### The zero Digest

`TaggedRoot` returns the zero `Digest` for no leaves.

Rejected. The zero `Digest` is the uninitialised value and is valid
nowhere. `CombineTagged` refuses it, so a protocol that combined such a
root into a parent would panic there, one call later and further from
the cause.

## Consequences

**Positive:**

- Every root that `TaggedRoot` returns is a leaf or a node hashed under
  the tree's role. Tagged trees under different roles never share a
  root that neither protocol chose.
- The rule matches `CombineTagged`. A precondition violation panics in
  the first test that exercises it.

**Negative:**

- A protocol whose tree can be empty tests for the empty case before
  every call and supplies its own value. Core does not check that
  value.
- `TaggedRoot` panics where `Root` returns HASH(), so the two functions
  differ on no leaves.

**Neutral:**

- `TaggedInclusionProof` and `TaggedTree.InclusionProof` return
  `ErrRange` for every index of an empty tree, and
  `VerifyTaggedInclusion` returns `ErrRange` for a size of zero, as the
  RFC 9162 functions do.

## References

- ADR-0014, the genesis sentinel is deleted, not documented.
- ADR-0025, tlog builds tagged trees.
- RFC 9162, section 2.1, <https://www.rfc-editor.org/rfc/rfc9162.html>.
- `tlog/tagged.go`: `TaggedRoot`, `TaggedTree` and `requireLeaves`.
