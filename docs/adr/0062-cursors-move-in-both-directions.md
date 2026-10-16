---
adr: 0062
title: Cursors Move in Both Directions
status: Accepted
date: 2026-10-16
supersedes: none
superseded-by: ADR-0068
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0062: Cursors Move in Both Directions

## Status

Accepted

[ADR-0068](0068-a-memtable-cursor-moves-back-by-a-link.md) supersedes the
decision on the memtable cursor. The decisions on the cursor of a table and
on the `Merger` remain in force.

## Context

The cursors of `sstable` and `skiplist` moved forward only, so a descending
range of an LSM tree collected its entries and reversed them. Its memory
and its time to the first entry grew with the range.

## Decision

We will add `Last`, `SeekLT` and `Prev` to `sstable.Source`, and implement
them in `sstable.Iter`, `skiplist.Iter` and `sstable.Merger`, because a
merge then moves backward over its tables and its memtables.

- A table cursor moves back by scanning to the restart point before its
  entry, and decoding forward to the entry before it.
- A memtable cursor moves back by a search from the head for the last node
  before its entry. The node layout does not change.
- A `Merger` keeps the direction of its last move. The backward order is
  the reverse of the forward order, ties included. A change of direction
  seeks every source other than the current one to the current entry.

This decision supersedes the cursors that move forward only, which RFC-0062
lists as a drawback.

## Alternatives Considered

### A ReverseSource beside Source

Rejected. A `Merger` would check its sources at run time, and every source
of core moves in both directions.

### Links back in the skip list

Rejected. Every node would grow by a word, and an `Add` would publish the
link back with a second compare-and-swap.

### Collect and reverse

Rejected. The memory and the latency of a descending range grow with the
range.

## Consequences

**Positive:**

- A descending range keeps one block of each table in memory, and returns
  its first entry after the moves of its sources.

**Negative:**

- `Source` has 10 methods, and a source of an engine implements the
  backward moves too.
- A `Prev` of a memtable costs a search, about the time of a `Get`.

**Neutral:**

- The table format and the node layout do not change.

## References

- RFC-0066, cursors that move backward.
- RFC-0062, sorted tables and memtables for LSM trees.
- Issue 56, <https://github.com/thesm-os/core/issues/56>.
