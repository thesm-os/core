---
rfc: 0070
title: Links Back and Insert Hints in a Skip List
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-10-16
updated: 2026-10-16
discussion: none
supersedes: none
superseded-by: none
produces-adr: ADR-0068, ADR-0069
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# RFC-0070: Links Back and Insert Hints in a Skip List

## Summary

We propose two changes to `skiplist`. Each node gains a link to the node
before it at level 0, so `Iter.Prev` follows one link instead of a search
from the head. A new `Inserter` keeps the nodes before its last entry at each
level, so the next `Add` of a later entry searches from there instead of the
head. The order of a `List` and the semantics of its cursors do not change.

## Motivation

`Iter.Prev` searches from the head for the last node before the current
entry, so that the node layout did not change when the cursors gained their
backward moves. A descending range of an LSM tree calls `Prev` on its
memtable cursor at every step through `sstable.Merger`, so a full scan back
does about n log n work where a scan forward does n.

Measured at v0.24.0 on a List of 100,000 entries of 16-byte keys and
100-byte values, in three rounds:

| Move | Time |
|---|---|
| A scan forward with `Next` | 0.86 to 0.88 ms |
| A scan back with `Prev` | 9.5 to 12.2 ms |
| `Iter.Next` in `BenchmarkIter` | 10.4 ns |
| `Iter.Prev` in `BenchmarkIter` | 91.8 ns |

`Add` spends most of its time in the search. A CPU profile of random `Add`
calls puts 79% of the time in `compare`, on the cache misses of the nodes of
the search. An `Add` of keys in order costs 121 ns, against 666 ns for random
keys, because the nodes of its search are in the cache. A batch whose keys
are in order or clustered still searches every level from the head for each
entry.

### Prior art

- Pebble's skip list links both ways at every level. Its `IterPrev` takes
  3.88 ns and its `IterNext` 3.97 ns (`internal/arenaskl/README.md` of
  cockroachdb/pebble at 70b50023). An insert sets the link back of its
  successor with a second compare-and-swap, and helps a writer that has not
  set its own link back yet (`internal/arenaskl/skl.go:219-282`).
- LevelDB, RocksDB and Badger search from the head for `Prev`. The RocksDB
  pull request facebook/rocksdb#5535 added links back. It took db_bench's
  descending seeks of 100 moves each from 730 to 84 µs per operation, and it
  was closed without a merge.
- Pebble's `Inserter` keeps the splice of its last insert, "avoiding
  recomputing the splice from scratch when inserts are sequential"
  (`internal/arenaskl/skl.go:88-99`). RocksDB's `InsertWithHint` costs
  "amortized O(log D), where D is the distance from the splice to the
  inserted key" (`memtable/inlineskiplist.h` of facebook/rocksdb).

## Detailed design

### The link back

A node has 8 header words, then its links. The new word 7 is the link back:
the index of the node before the node at level 0, or 0 for the head.

```go
// wordBack is the word of the link back: the node before the node at
// level 0, or 0 for the head. Writers set it with compare-and-swap, and a
// cursor that finds it stale walks forward from it.
wordBack = 7
// nodeHeader is the number of words before the links of a node.
nodeHeader = 8
```

- A node takes 4 bytes more. The nodes take 38.3 bytes on average, and the
  tallest node takes 112 bytes. `Cost` and `MaxCost` count the word, so the
  code of an engine that budgets with them does not change.
- `Add` links level 0 in three steps:
  1. It writes the link back of the new node, the node before it, and its
     link at level 0, the node after it.
  2. A compare-and-swap of the link of the node before it publishes the new
     node, as today.
  3. A compare-and-swap moves the link back of the node after it from the
     node before it to the new node. Its result is not checked.
- Pebble's insert takes one more step before it publishes a node. It sets
  the link back of the node after it for a writer that has not set that
  link yet, because a cursor of Pebble follows a link back without a check.
  A cursor of a `List` checks the link and walks forward from a stale one.
  The step would only make a stale link rarer, and each `Add` would load
  one more word.
- The link back of a node is always a node before it, because each value
  that a writer or a cursor writes into it is a node before the node. A link
  back can be stale: a writer can link another node between the two after
  the link was set.
- `Iter.Prev` reads the link back b of the current node x. While the link of
  b at level 0 is not x, b moves to the node after it. b is then the last
  node before x, and `Prev` moves there. Each step of the walk moves the
  link back of x to the next b with a compare-and-swap. Level 0 links every
  node in order and no node leaves a `List`, so the walk ends at the node
  before x.
- `Prev` then yields every entry whose `Add` returned before the move, and
  the entries that writers link while it moves, as the forward moves do.
  `Last` and `SeekLT` search as today.

### The Inserter

```go
// Inserter adds entries to a List, and keeps the node before its last entry
// at each level. An Add of an entry after the last entry searches each level
// from that node instead of the head, so an Inserter that adds entries in
// order, or close to the last one, searches a few nodes per level. An Add of
// an earlier entry searches from the head, as List.Add does.
//
// The zero Inserter is ready for use with any List. An Inserter used with
// another List, or with a List that Reset emptied, searches from the head
// once and keeps the nodes of that List from then on.
//
// # Concurrency
//
// An Inserter is for one goroutine. Other goroutines can add to the List at
// the same time, through List.Add or Inserters of their own.
//
// # Allocation contract
//
// Zero-alloc.
type Inserter struct{ /* unexported: the List, its resets and a node per level */ }

// Add inserts the entry of key, seq and value into l, with the errors of
// List.Add.
func (ins *Inserter) Add(l *List, key []byte, seq uint64, value []byte) error
```

- An `Inserter` keeps the `List`, the count of its resets at the last `Add`
  and one node per level: about 100 bytes on the stack of its caller.
- The hint applies when the `List` and its count of resets are the ones of
  the last `Add`, and the entry is after the last entry. The search of a
  level then starts at the kept node of the level, which is before the
  entry. A level above the kept levels starts from the head, as today.
- The kept node of a level is at or after the kept nodes above it. A search
  that moves past the kept node of its level can end past the kept nodes
  below it. The levels below then start from the nodes that the search
  finds, as a search from the head does. Without that rule an `Add` far
  after the last entry would walk every node between the two at level 0.
- After an `Add` that succeeds, the kept node of each level of the new node
  is the new node, and the kept node of each higher level is the node before
  it that the search found. A failed `Add` keeps the hint as it was. No node
  leaves a `List` before `Reset`, so a kept node remains a valid start.
- `List.Add` adds through an `Inserter` without a hint, so both share one
  path.

### Tests

- The internal tests write a stale link back into a node, and check that
  `Prev` walks forward from it, returns the node before, and repairs the
  link.
- The machine of the `List` adds through one `Inserter` and through
  `List.Add`, and checks every move against the sorted model. Its invariant
  checks that each link back is the node before its node.
- Writers that add at once while a scanner scans forward and back check
  that each scan yields every entry whose `Add` returned before the scan, in
  order. The writers add the same entries, neighbouring entries, and entries
  whose nodes have every level.
- An `Inserter` across a `Reset`, across two Lists and with keys in
  descending order yields the entries that `List.Add` yields. Two internal
  tests break the links of the head, and of the node of the last entry, to
  check where the search of each level starts.
- `TestIterAllocs`, `TestInserterAllocs` and the benchmarks state a ceiling
  of 0 allocations for `Prev` and `Inserter.Add`. The benchmarks move through
  65,536 entries in both directions, and add keys in order, keys that share
  their first 8 bytes and random keys, with and without an `Inserter`.

## Alternatives considered

### A. Links back at every level, as in Pebble

**Why not:** `Prev` reads level 0 alone, and `Last` and `SeekLT` search from
the top. Links back at every level would take 4 bytes and a
compare-and-swap for each level of a node, 6.3 bytes on average instead of
4, and no move would read the links above level 0.

### B. The search from the head

**Why not:** a scan back of 100,000 entries takes 11 to 14 times as long as
a scan forward, and a descending merge calls `Prev` at every step.

### C. A cursor that keeps the nodes of its last search

**Why not:** the nodes of a search give a start before the current entry,
but the node before the entry still takes a search of each level. A link
back takes one read.

### D. One hint for each key prefix, as in RocksDB

**Why not:** a map of prefixes needs a prefix extractor and memory for each
prefix. One `Inserter` for each batch serves a batch in key order.

### E. One hint inside the List, as RocksDB's sequential splice

**Why not:** every writer would share it, so concurrent writers would
contend on it and overwrite each other's hint.

## Drawbacks

- A node takes 4 bytes more: 11.7% more node arena on average, and the
  tallest node takes 112 bytes instead of 108. `MaxCost` and `Cost` return
  the larger values.
- An `Add` before the last entry makes one more compare-and-swap.
- A `Prev` that finds a stale link writes the node.
- `skiplist` gains a type and its method.

## Open questions

None.

## Unresolved / future work

None.

## References

- cockroachdb/pebble at 70b50023, `internal/arenaskl/skl.go` (the `Inserter`
  and the insert of a node) and `internal/arenaskl/README.md` (`IterNext`
  and `IterPrev`).
- facebook/rocksdb, `memtable/inlineskiplist.h` (`InsertWithHint`).
- facebook/rocksdb#5535, <https://github.com/facebook/rocksdb/pull/5535>.
- RFC-0066, cursors that move backward.
- RFC-0063, key prefixes in skip list nodes.
