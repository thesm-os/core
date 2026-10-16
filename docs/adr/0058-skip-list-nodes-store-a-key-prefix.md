---
adr: 0058
title: Skip List Nodes Store a Key Prefix
status: Accepted
date: 2026-10-16
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0058: Skip List Nodes Store a Key Prefix

## Status

Accepted

## Context

A node of a `skiplist.List` was 5 + h words of the node arena: the offset
and the length of its key, the length of its value, the two halves of its
sequence and one link per level. The key and the value of the node are in
the data arena, a separate array. A search compares its key with each node
that it visits. Each visit reads the offset of the key from the node, and
then the key from the data arena.

Pebble and Badger allocate a key right after its node in one arena, and both
read the arena through `unsafe`. Core does not import `unsafe`, and without
it a `[]uint32` cannot return the bytes of a key as a `[]byte`.

A probe outside this module compared the skip list of 0ea2ef5 with two
variants that store a prefix of each key in its node. It ran with Go 1.27.2
on a Ryzen 9 9950X3D in a scope capped at four cores. Each comparison ran 6
rounds of 300 ms, and every other round ran the variants in the reverse
order. `Get` looked up random keys of a `List` of 1M entries. `Add` inserted
16-byte keys with 100-byte values with one writer.

| Keys | Operation | 5 + h words | 8-byte prefix, 7 + h words | 4-byte prefix, 6 + h words |
|---|---|---|---|---|
| Random | `Get` | 1,238-1,281 ns | 815-853 ns | 750 ns |
| Random | `Add` | 807-830 ns | 445-476 ns | 405 ns |
| First 4 bytes equal | `Get` | 1,247 ns | 847 ns | 1,428 ns |
| First 4 bytes equal | `Add` | 914 ns | 501 ns | 930 ns |
| First 8 bytes equal | `Get` | 1,265-1,302 ns | 1,357-1,376 ns | not measured |
| First 8 bytes equal | `Add` | 856-873 ns | 918-950 ns | not measured |

## Decision

We will store the first 8 bytes of each key in two words of its node,
because a search then reads a key from the data arena only when the first 8
bytes of the two keys are equal.

- A node is 7 + h words: the offset and the length of its key, the length
  of its value, the high and the low half of its sequence, the high and the
  low half of the prefix of its key, and one link per level. The head node
  has 27 words.
- The prefix of a key is its first 8 bytes as a big-endian integer. A key of
  fewer than 8 bytes is padded with zero bytes.
- Keys whose prefixes differ order as their prefixes order. Keys with equal
  prefixes order as `bytes.Compare` orders them. The order of the entries
  does not change.
- A node takes 34.3 bytes on average at the heights of e^-k.

The API of the List does not change. This decision supersedes the node of
5 + h words that RFC-0062 describes.

## Alternatives Considered

### The node of 5 + h words

Rejected. On random keys, `Get` took 1.5 times as long as with the prefix of
8 bytes. An `Add` with one writer took 1.7 to 1.8 times as long.

### A prefix of 4 bytes in 6 + h words

Rejected. It was the fastest variant on random keys. Keys that start with a
table identifier of 4 bytes have equal first 4 bytes, and the prefix of 4
bytes lost its whole gain on them. The prefix of 8 bytes kept its gain there
for 4 more bytes per node.

### A copy of the key in the node arena

Rejected. `Get`, `Iter.Key` and `Iter.Value` return slices of the data
arena, so the data arena keeps the key too. Every node then grows by the
length of its key.

### A prefix of 16 bytes in 9 + h words

Rejected. It takes 8 bytes per node more than the prefix of 8 bytes. It
gains only for keys that share 8 to 15 leading bytes.

## Consequences

**Positive:**

- On random keys and on keys whose first 4 bytes are equal, `Get` took
  32-34% less time. An `Add` with one writer took 43-45% less, and an `Add`
  with four writers 15-24% less.

**Negative:**

- A node takes 34.3 bytes on average against 26.3 bytes. An entry of a
  16-byte key and a 100-byte value grows by 5.6% from 142.3 to 150.3 bytes.
- Keys that share their first 8 bytes gain nothing. URLs and paths below
  one directory are such keys. One of two runs measured an `Add` 11% slower
  on them.
- An engine that sizes its node arena by the average of 26.3 bytes gets
  `ErrFull` at 77% of the entries that it planned for.
- A mutant that replaces the comparison of the prefixes with false is
  equivalent, because `bytes.Compare` returns the same order. An annotation
  suppresses it, so no test detects a change that removes the comparison of
  the prefixes.

**Neutral:**

- The API of the List, the allocation contract and the order of the entries
  do not change.

## References

- RFC-0063, key prefixes in skip list nodes.
- RFC-0062, sorted tables and memtables for LSM trees.
- `skiplist/list.go`: `List`, `New`, `Add`, `compare` and `keyPrefix`.
- cockroachdb/pebble at 70b50023f8fe, `internal/arenaskl/node.go`,
  `newRawNode`.
- dgraph-io/badger v4.9.6, `skl/skl.go`, `newNode`.
