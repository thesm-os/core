---
adr: 0068
title: A Memtable Cursor Moves Back by a Link
status: Accepted
date: 2026-10-16
supersedes: ADR-0062
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0068: A Memtable Cursor Moves Back by a Link

## Status

Accepted

Supersedes the decision of
[ADR-0062](0062-cursors-move-in-both-directions.md) that a memtable cursor
moves back by a search from the head. The decisions of ADR-0062 on the
cursor of a table and on the `Merger` remain in force.

## Context

`skiplist.Iter.Prev` searched from the head for the node before the current
entry, so that the node layout did not change. A descending merge calls
`Prev` on its memtable cursor at every step, so a full scan back did about n
log n work. On a List of 100,000 entries a scan forward took 0.86 to 0.88 ms
and a scan back 9.5 to 12.2 ms.

## Decision

We will give each node of a `List` a link back at level 0, because
`Iter.Prev` then follows one link instead of a search from the head.

- A node has 8 header words, and word 7 is the link back. The nodes take
  38.3 bytes on average, and the tallest node takes 112 bytes.
- `Add` publishes the node at level 0, then moves the link back of the next
  node to the new node with a compare-and-swap whose result it does not
  check.
- A link back is always a node before the node, and it can be stale. `Prev`
  walks forward from it to the node before the current one, and repairs a
  stale link.
- `Last` and `SeekLT` search from the top, as before.

## Alternatives Considered

### Links back at every level

Rejected. `Prev` reads level 0 alone, so the links above it would take 6.3
bytes per node on average instead of 4, and a compare-and-swap per level,
for no move.

### The search from the head

Rejected. A scan back of 100,000 entries took 11 to 14 times as long as a
scan forward.

### A cursor that keeps the nodes of its last search

Rejected. The node before the current entry still takes a search of each
level.

### The help step of Pebble's insert

Rejected. Pebble's insert sets the link back of the next node for a writer
that has not set it yet, because a cursor of Pebble follows a link back
without a check. A cursor of a `List` walks forward from a stale link, so
the step would only make a stale link rarer, and each `Add` would load one
more word.

## Consequences

**Positive:**

- `Prev` follows one link when no writer linked a node before the current
  one since its link back was set.

**Negative:**

- A node takes 4 bytes more, and `MaxCost` returns 112 node bytes instead of
  108.
- An `Add` before the last entry makes one more compare-and-swap.
- A `Prev` that finds a stale link back writes the node.

**Neutral:**

- The order of a `List` and the semantics of its cursors do not change.

## References

- RFC-0070, links back and insert hints in a skip list.
- cockroachdb/pebble at 70b50023, `internal/arenaskl/skl.go:219-282`.
- facebook/rocksdb#5535, <https://github.com/facebook/rocksdb/pull/5535>.
