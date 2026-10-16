---
adr: 0069
title: An Inserter Searches from Its Last Entry
status: Accepted
date: 2026-10-16
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0069: An Inserter Searches from Its Last Entry

## Status

Accepted

## Context

`List.Add` searched every level from the head. A CPU profile put 79% of a
random `Add` in the comparisons of that search. An `Add` of keys in order
took 121 ns, against 666 ns for random keys, and it still searched every
level from the head for each entry.

## Decision

We will add `skiplist.Inserter`, which keeps the node before its last entry
at each level, because an `Add` of a later entry then searches a few nodes
per level instead of every level from the head.

- `Inserter.Add(l, key, seq, value)` has the errors of `List.Add`. The zero
  `Inserter` is ready for use.
- The hint applies when the `List` and the count of its resets are those of
  the last `Add`, and the entry is after the last entry. Any other `Add`
  searches from the head.
- An `Add` that succeeds keeps the new node at each of its levels, and the
  node before it at each higher level. A failed `Add` keeps the hint.
- `List.Add` adds through an `Inserter` without a hint.

## Alternatives Considered

### One hint for each key prefix

Rejected. A map of prefixes needs a prefix extractor and memory for each
prefix, and one `Inserter` for each batch serves a batch in key order.

### One hint inside the List

Rejected. Concurrent writers would contend on it and overwrite each other's
hint.

## Consequences

**Positive:**

- A batch whose keys are in order, or close to each other, searches a few
  nodes per level for each entry.

**Negative:**

- `skiplist` gains a type of about 100 bytes and its method.
- An `Inserter` is for one goroutine, and the compiler does not check that.

**Neutral:**

- An `Add` of an entry before the last one costs what `List.Add` costs.

## References

- RFC-0070, links back and insert hints in a skip list.
- cockroachdb/pebble at 70b50023, `internal/arenaskl/skl.go:88-99`.
- facebook/rocksdb, `memtable/inlineskiplist.h` (`InsertWithHint`).
