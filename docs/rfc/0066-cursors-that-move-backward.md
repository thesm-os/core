---
rfc: 0066
title: Cursors That Move Backward
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-10-16
updated: 2026-10-16
discussion: https://github.com/thesm-os/core/issues/56
supersedes: none
superseded-by: none
produces-adr: ADR-0062
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# RFC-0066: Cursors That Move Backward

## Summary

We propose that the cursors of `sstable` and `skiplist` move backward as well
as forward. `sstable.Source` gains `Last`, `SeekLT` and `Prev`, and
`sstable.Iter`, `skiplist.Iter` and `sstable.Merger` implement them. A
descending range of an LSM tree then merges its memtables and its tables
backward, with the memory of one block per table and no collection of the
range. The table format and the node layout of the skip list do not change.

## Motivation

An engine on these packages ranges over its keys in both orders. Today every
cursor moves forward only:

| Type | Moves |
|---|---|
| `*sstable.Iter` | First, SeekGE, Next |
| `*sstable.Merger` | First, SeekGE, Next |
| `*skiplist.Iter` | First, SeekGE, Next |
| `sstable.Source` | First, SeekGE, Next |

A descending range on forward cursors collects the entries of the range and
reverses them. Its memory and its time to the first entry grow with the
range.

### Prior art

- LevelDB's `Iterator` has `SeekToFirst`, `SeekToLast`, `Seek`, `Next` and
  `Prev`, and every iterator of LevelDB implements both directions. Pebble's
  `InternalIterator` has `First`, `Last`, `SeekGE`, `SeekLT`, `Next` and
  `Prev` in one interface.
- LevelDB's block cursor moves back by scanning to the restart point before
  the current entry, and decoding forward from there to the entry before it.
- LevelDB's skip list has no links back. Its `Prev` searches from the head
  for the last node before the current key. Pebble's skip list keeps a link
  back beside the link forward at every level, so its `Prev` takes one link.
- LevelDB's `MergingIterator` changes direction by seeking every child other
  than the current one to the current key. Before `Prev` it then moves each
  such child back by one entry, or to its last entry when the seek
  exhausted it.

## Detailed design

### Source

```go
// Source is a cursor over a run of entries in the order of Compare. A move
// positions the cursor on an entry or exhausts it. An Iter of a table, a
// Merger and the cursor of a memtable are Sources.
type Source interface {
	// First moves to the first entry and reports whether it exists.
	First() bool

	// Last moves to the last entry and reports whether it exists.
	Last() bool

	// SeekGE moves to the first entry at or after the entry (key, seq) and
	// reports whether it exists.
	SeekGE(key []byte, seq uint64) bool

	// SeekLT moves to the last entry before the entry (key, seq) and reports
	// whether it exists.
	SeekLT(key []byte, seq uint64) bool

	// Next moves to the next entry and reports whether it exists. Next of a
	// cursor on no entry reports false.
	Next() bool

	// Prev moves to the entry before the current one and reports whether it
	// exists. Prev of a cursor on no entry reports false.
	Prev() bool

	// Key returns the key of the current entry. It is valid until the next
	// move of the cursor.
	Key() []byte

	// Seq returns the sequence of the current entry.
	Seq() uint64

	// Value returns the value of the current entry. It is valid until the
	// next move of the cursor.
	Value() []byte

	// Err returns the error that exhausted the cursor, or nil.
	Err() error
}
```

### The cursor of a table

- `Last` moves the index to its last entry, reads that data block and moves
  to its last entry.
- `SeekLT` searches the index for the first block whose last entry is at or
  after (key, seq), as `SeekGE` does, and moves to the last entry of the
  block before (key, seq). When the block has no such entry, it moves to the
  last entry of the block before. When no block qualifies, it moves to the
  last entry of the table.
- `Prev` scans back to the restart point before the current entry, and
  decodes forward to the entry before it. At the first entry of a block it
  reads the block before, through the previous entry of the index, and moves
  to its last entry. A `Prev` inside a block decodes at most
  `RestartInterval` entries.
- Every entry of the index is a restart point, so the index moves back by
  one restart point.

### The cursor of a memtable

- `Last` follows the links of each level from the top of the list, until
  the next node is 0.
- `SeekLT` runs the search of `SeekGE`, which ends at the last node before
  (key, seq), and moves to that node. The head node is no entry.
- `Prev` is `SeekLT` of the current entry. It costs one search: O(log n)
  steps for n entries, about the time of a `Get`. A `Get` of a list of 1M
  random 16-byte keys took 815-853 ns.
- A backward move yields the entries that it finds linked, as a forward
  move does. The node layout does not change.

### The merge

A `Merger` keeps the direction of its last move. The backward order is the
reverse of the forward order: an entry before the entries that precede it,
and of entries that compare equal, the one of the higher source first.

- `Last` and `SeekLT` move every source and build the heap in the backward
  order.
- A `Prev` after a forward move, at the entry e of the source i, moves every
  other source j to its last place before (e, i) in the forward order. Each
  source j seeks e with `SeekGE`. A source on an entry equal to e with j
  below i keeps that entry. Every other source moves back with `Prev`, or
  to its last entry with `Last` when the seek exhausted it. The source i
  then moves back, and the heap is built in the backward order.
- A `Next` after a backward move mirrors this. Each source j other than i
  seeks e with `SeekGE`. A source on an entry equal to e with j below i
  moves past it with `Next`, and the others keep their entry. The source i
  then moves forward, and the heap is built in the forward order.
- A move in the same direction moves the source at the top and restores the
  heap with O(log k) comparisons for k sources, as `Next` does today.
- A source whose `Err` is not nil exhausts the merge, in both directions.

### Allocation contract

The backward moves allocate as the forward moves do: nothing once the
buffers of a table cursor have grown to its largest block, apart from the
copy of a block that a move adds to the cache.

### Tests

- A property moves each kind of cursor through random sequences of all six
  moves and checks each entry against a sorted model of the run. The runs of
  the `Merger` share entries, so the model also checks the order of entries
  that compare equal.
- The stateful machine of the `List` gains `Last`, `SeekLT` and `Prev`.
- The cases of corrupt tables check the backward moves for `ErrCorrupt`,
  as they check the forward moves.
- `TestIterAllocs`, `TestMergerAllocs` and the benchmarks state the
  ceilings of the backward moves.

## Alternatives considered

### A. Collect and reverse

**Why not:** the memory and the time to the first entry of a descending
range grow with the range.

### B. Links back in the skip list, as in Pebble

Each node would keep a link back at level 0, and `Prev` would take one link.

**Why not:** every node grows by a word, and an `Add` publishes the link back
with a second compare-and-swap that a concurrent `Add` can make stale. In a
descending scan of a merge, only the memtables search for each step back,
and they contain the newest part of the data.

### C. A ReverseSource beside Source

`ReverseSource` would embed `Source` and add the backward moves.

**Why not:** a `Merger` would check at run time that each of its sources
moves backward, and fail a backward move when one does not. Every source of
core moves in both directions, as every iterator of LevelDB and Pebble does.
No release contains `sstable`, so `Source` can gain methods without breaking
a caller.

### D. Backward moves on the Merger alone

**Why not:** a merge that moves backward needs every source to move backward.

## Drawbacks

- `Source` grows from 7 to 10 methods. A source of an engine implements the
  backward moves too.
- A `Prev` of a memtable costs a search, about the time of a `Get`.
- A change of direction seeks every source of a merge.

## Open questions

None.

## Unresolved / future work

None.

## References

- Issue 56, <https://github.com/thesm-os/core/issues/56>.
- google/leveldb, `include/leveldb/iterator.h`, `table/block.cc`
  (`Block::Iter::Prev`), `db/skiplist.h` and `table/merger.cc`
  (`MergingIterator::Next` and `Prev`), <https://github.com/google/leveldb>.
- cockroachdb/pebble at 70b50023f8fe, `internal/base/iterator.go`
  (`InternalIterator`) and `internal/arenaskl/node.go` (`links`).
- RFC-0062, sorted tables and memtables for LSM trees.
- RFC-0063, key prefixes in skip list nodes.
