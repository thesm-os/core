---
adr: 0061
title: A List Reports the Cost of an Entry
status: Accepted
date: 2026-10-16
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0061: A List Reports the Cost of an Entry

## Status

Accepted

## Context

The bytes that `skiplist.List.Add` reserves depend on the height of the
node, which derives from a hash of the entry and the seed of the list. An
`Add` of a 16-byte key and a 100-byte value took 148 to 184 bytes. An engine
that reserves the memory of a batch at its admission had to reserve the
largest node for each entry, and a memtable then admitted 32.9% fewer
entries than fit in it.

## Decision

We will add `List.Cost(key []byte, seq uint64, valueLen int) (dataBytes,
nodeBytes int)`, because an engine then reserves the exact bytes of each
entry before its `Add`.

- `dataBytes` is the length of the key plus `valueLen`. `nodeBytes` is 4
  bytes for each of the 7 words of the header and each link of the node.
- The results have the order of the capacities of `skiplist.Config`.
- An `Add` that succeeds grows `Size` by the sum.
- A `valueLen` below 0 counts as 0, and one above 2^32 - 1 counts as
  2^32 - 1.
- `Cost` reads only the seed, so it is safe for concurrent use with every
  method, `Reset` included, and it does not allocate.

## Alternatives Considered

### A reservation of the largest node

Rejected. A memtable admits 32.9% fewer entries of 116 bytes.

### Heights from the sequence alone

Rejected. A caller that knows how heights derive can choose entries of one
height.

### A method that returns the height

Rejected. The caller would repeat the layout of a node.

## Consequences

**Positive:**

- A memtable admits as many entries as fit in its arenas while its engine
  reserves each entry at admission.

**Negative:**

- An engine that calls `Cost` and then `Add` hashes each key twice.

**Neutral:**

- The cost of an entry differs between two lists of different seeds.

## References

- RFC-0065, the cost of a skip list entry.
- Issue 57, <https://github.com/thesm-os/core/issues/57>.
