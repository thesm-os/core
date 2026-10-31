---
rfc: 0078
title: Range Tombstones and Prefix Filters of a Table
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-10-19
updated: 2026-10-19
discussion: https://github.com/thesm-os/core/issues/84
supersedes: none
superseded-by: ADR-0076
produces-adr: tbd
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# RFC-0078: Range Tombstones and Prefix Filters of a Table

## Summary

We propose version 3 of the table format of `sstable`. A table of version 3
can contain the fragments of range tombstones and a filter of the prefixes of
its keys. The filter fields of the footer contain the place of a block of
places, and that block contains the places of the filter, the fragments and
the prefix filter. Version 3 keeps the index and the filter of version 1 or
of version 2, so range tombstones and prefix filters do not need a
`PartitionSize`.

- `Writer.AddRangeDelete` adds a fragment of a range tombstone: the keys from
  a start up to an end, which the range excludes, at a sequence. The Writer
  takes fragments that do not overlap, in order.
- `Fragmenter` splits range tombstones that overlap at their overlap points,
  and passes the fragments to the Writer.
- `Reader.RangeDeletes` returns a cursor over the fragments, which the Reader
  keeps in memory.
- `WriterOptions.Prefix` takes a `PrefixExtractor`. The Writer builds a second
  `filter.Fuse` over the distinct prefixes of the keys, and records the name
  of the extractor.
- `Reader.MayContainPrefix` reports whether the table may contain a key of a
  prefix.
- `WriterOptions.MaxSize` takes `Counts`, which include the range tombstones.

## Motivation

### A delete of a range takes a tombstone for each key

A table has no place for a range tombstone, so a store deletes a range of
keys with one tombstone for each key. RFC-0062 lists it among its drawbacks:
"Range deletions are out of both formats."

The probe of issue 84 ran on core at 506c09b with Go 1.27.2. It wrote one
table of the 1,000,000 keys `a/0000000` to `a/0999999` and the 1,000 keys
`b/0000` to `b/0999`, with values of 100 bytes. A second table deleted the
keys `a/…` with one tombstone each, an entry with an empty value at a higher
sequence. A merge of both tables then read from the first key to the first
live key:

| Tables | Entries until `b/0000` | Time |
|---|---|---|
| The values and the tombstones | 2,000,001 | 57.0 ms |
| The live keys alone | 1 | 3.9 µs |

The tombstones take 14,358,985 bytes of table. A scan that crosses the range
steps over two entries for each deleted key until a compaction drops both of
them.

### A scan of a prefix reads every table that spans the prefix

The filter of a table has the hash of each distinct key and no hash of a
prefix. A scan of the keys of one prefix seeks every table whose range spans
the prefix, and reads an index partition and a data block of each.

The probe of issue 85 ran on core at 506c09b with Go 1.27.2. It wrote 64
tables, as the 64 flushes of a store of 100,000 logs. Each table contains 8
records of each of 2,000 random logs, under a key of the byte `r`, the log
in 8 bytes and the offset in 8 bytes. 1,000 scans of the records of one
random log then sought every table to the prefix of 9 bytes of the log:

| For each scan | Count |
|---|---|
| Tables that contain the log | 1.31 |
| Reads in the tables that contain the log | 2.63 |
| Reads in the tables that do not contain the log | 125.36 |

98% of the reads of a scan go to tables without a key of the prefix.

### The footer has no field for a new part

The footer of 80 bytes contains the place of the index and the place of the
filter, the number of entries, the lowest and the highest sequence, the
version and the magic number. A range tombstone and a prefix filter each
need a place, and the footer has no field for one.

### Prior art

- RocksDB stores range tombstones in "a separate meta-block for range
  tombstones". "When an SST file is opened, its range tombstones are
  fragmented and cached", and "the range tombstone blocks are always pinned
  uncompressed in memory". The RocksDB post on DeleteRange proposes a later
  format "that requires range tombstones to be stored in a fragmented form.
  This would save time when opening SST files".
- Pebble's table writer takes fragments: "Calls to DeleteRange must be made
  using already-fragmented (non-overlapping) spans and in sorted order." Its
  `keyspan.Fragmenter` splits overlapping spans "at their overlap points",
  and takes the spans "in increasing start key order". Its `SeekGE` of
  fragments moves "to the first span with an end key greater than the given
  key", and its `SeekLT` "to the last span with a start key less than the
  given key".
- RocksDB filters the prefixes of the keys of a table with a
  `prefix_extractor`, whose keys of one prefix must be "close to each others
  in the total order". It records the name of the extractor in each table.
  When that name "is different from the prefix extractor provided in the
  options, the prefix bloom filter is not used for this file".

## Detailed design

### Components

| Component | Responsibility |
|---|---|
| `Writer.AddRangeDelete` | Adds a fragment of a range tombstone, after the fragment before it |
| `RangeDeleter` | Receives fragments in order. A `Writer` is one |
| `Fragmenter` | Splits overlapping range tombstones into fragments, and passes them to a `RangeDeleter` |
| `PrefixExtractor` | Returns the prefix of a key, and the name of the extraction |
| `WriterOptions.Prefix` | The extractor of the prefix filter, or nil for none |
| `Writer` | Writes version 3 for a table with fragments or a prefix filter, and version 1 or 2 otherwise |
| `Reader` | Keeps the fragments in memory, and reads the prefix filter as it reads the key filter |
| `RangeDeleteIter` | A cursor over the fragments in both directions |
| `Reader.MayContainPrefix` | Asks the prefix filter of a table whose extractor has the name of the Reader's |
| `Summary` | Contains the count of the fragments and their bounds |
| `WriterOptions.MaxSize` | Bounds a table from its `Counts`, the fragments included |

### The format of version 3

A table of version 3 has the parts of version 1 or of version 2, a filter of
prefixes, a block of fragments and a block of places. The block of places
has a flag for an index and filters in partitions.

| Part | Version 1 | Version 2 | Version 3 |
|---|---|---|---|
| The index | One block | Partitions and a top-level index | As version 1 or version 2 |
| The filter of the keys | One block | Partitions and a top-level index | As the index |
| The filter of the prefixes | None | None | As the index, when the table has one |
| The fragments of range tombstones | None | None | One block, when the table has fragments |
| The index fields of the footer | The index block | The top-level index of the index | The index block or the top-level index of the index |
| The filter fields of the footer | The filter block | The top-level index of the filter | The block of places |

The parts of a partitioned table of version 3 follow one another in this
order: the data blocks, the filter partitions, the prefix filter partitions,
the index partitions, the block of fragments, the top-level index of the
filter, the top-level index of the prefix filter, the top-level index of the
index, the block of places and the footer. A table without partitions has
the data blocks, the block of fragments, the filter block, the prefix filter
block, the index block, the block of places and the footer. Every block has
the trailer of version 1. The footer has the layout of version 1 and the
version 3.

Each part after the data blocks or the partitions ends at the trailer before
the part after it, and the last one at the footer. A Reader checks the place
of each part from the footer back, in one order for both layouts: the block
of places, the index, the prefix filter, the filter and the block of
fragments.

The block of places has these fields, stored with the compression byte 0:

| Field | Encoding |
|---|---|
| Flags. Bit 0 is set for an index and filters in partitions | varint |
| The place of the filter block or of the top-level index of the filter | offset and length as varints, then the CRC32C as a uint32, or three zeros |
| The place of the block of fragments | the same, or three zeros |
| The place of the prefix filter block or of its top-level index | the same, or three zeros |
| The name of the prefix extractor | its length as a varint, then its bytes, or the length 0 |

The Writer writes three zeros for a part that the table does not have.
Every block has at least one byte, so a Reader takes a length of 0 for a
missing part. A Reader refuses a flag that it does not know, so a later
format cannot add a part that a Reader of version 3 skips.

The block of fragments has the layout of an index block, with a restart
point at every entry. Its entry for a fragment and one of its sequences has
the internal key of the start and the sequence, and the end as its value.
The entries follow the order of `Compare`, so the sequences of one fragment
go from the highest to the lowest. Every entry of one fragment has the same
end, and the end of a fragment is at or before the start of the next one.
The Writer stores the block with the compression byte 0. A table without
fragments has no block of fragments, so `Open` refuses a block without
entries.

A prefix filter has the layout of the key filter. A filter partition of
prefixes is the encoding of a `filter.Fuse` over the `xxh3.Hash` of each
distinct prefix of a range of the table. The Writer cuts it at the counts of
`filter.MaxFuseSize`, as it cuts a key filter. The top-level index of the
prefix filter has an entry for each partition, with the last prefix of the
partition and the sequence 0.

### Fragments

A range tombstone covers the keys from its start up to its end, which the
range excludes, at a sequence. The sequence is opaque, as the sequence of an
entry is. Core does not compare it with the sequences of entries, and the
engine applies each fragment to the entries that it covers.

`Writer.AddRangeDelete` takes one sequence of one fragment at a time:

- A call with the start and the end of the call before it follows that call
  when its sequence is lower.
- A call whose start is at or after the end of the call before it starts a
  new fragment.
- Any other call returns `ErrOrder`. A start at or after its end returns
  `ErrRange`.

The fragments of a table then do not overlap, so a search of their starts
finds the fragment of a key.

A `Fragmenter` takes range tombstones that overlap, in the order of their
starts, and passes their fragments to its `RangeDeleter` in such calls. It
keeps the tombstones that cover the start of the next fragment. Each `Add` of
a later start passes the fragments before that start, which it splits at
every end before it, and `Flush` passes the rest. A fragment contains each
sequence once, so the Fragmenter drops a second tombstone of one sequence
over the same keys. The Fragmenter passes at most 2n-1 fragments of n
tombstones. Every bound of a fragment is a bound of a tombstone.

```text
tombstones           fragments
a--------e @ 9       a--c @ 9
    c--------g @ 7   c--e @ 9, 7
          e----i @ 5 e--g @ 7, 5
                     g--i @ 5
```

### Prefixes

A `PrefixExtractor` returns the prefix of a key, and its `Name` returns the
name of the extraction. The Writer calls `Prefix` once for each distinct
key.

- A prefix must be a byte prefix of its key, and the prefixes of keys in the
  order of `bytes.Compare` must not fall. The keys of one prefix are then
  consecutive in a table. `Add` returns `ErrPrefix` for a prefix that breaks
  either rule, and the Writer is unchanged.
- A key without a prefix has no hash in the prefix filter.
- The prefix filter takes the width of `FilterBits`. `NewWriter` returns
  `ErrOptions` for a `Prefix` without `FilterBits` and for an empty name.
- The Writer records the name in the block of places. A Reader whose
  `ReaderOptions.Prefix` has another name does not read the prefix filter,
  because a reader that assumes another extraction would skip tables with
  keys of the prefix.

### API

```go
package sstable

// RangeDeleter receives the fragments of range tombstones in the order of a
// table. A *Writer is a RangeDeleter.
type RangeDeleter interface {
	// AddRangeDelete adds the fragment of the keys from start up to end,
	// which the range excludes, at the sequence seq.
	AddRangeDelete(ctx context.Context, start, end []byte, seq uint64) error
}

// AddRangeDelete adds a fragment of a range tombstone. The fragment must
// follow the fragment before it in the order of RangeDeleter. The Writer
// writes the block of fragments at Close.
func (w *Writer) AddRangeDelete(ctx context.Context, start, end []byte, seq uint64) error

// Fragmenter splits range tombstones that overlap at their overlap points,
// and passes the fragments to a RangeDeleter in order.
type Fragmenter struct{ /* unexported */ }

// NewFragmenter returns a Fragmenter that passes its fragments to dst.
func NewFragmenter(dst RangeDeleter) (*Fragmenter, error)

// Add adds the range tombstone of the keys from start up to end at seq. Its
// start must be at or after the start of the tombstone before it. Add
// passes every fragment before start to the RangeDeleter.
func (f *Fragmenter) Add(ctx context.Context, start, end []byte, seq uint64) error

// Flush passes every fragment that remains to the RangeDeleter.
func (f *Fragmenter) Flush(ctx context.Context) error

// Reset discards the tombstones of f, and keeps its RangeDeleter and its
// buffers for the tombstones of the next table.
func (f *Fragmenter) Reset()

// PrefixExtractor returns the prefix of a key for the prefix filter of a
// table. The Writer records the Name of the extraction in each table.
type PrefixExtractor interface {
	// Name returns the name of the extraction.
	Name() string

	// Prefix returns the prefix of key, and reports false for a key without
	// a prefix.
	Prefix(key []byte) ([]byte, bool)
}

type WriterOptions struct {
	// ...the fields of version 2

	// Prefix is the extractor of the prefixes of the prefix filter. With nil
	// the Writer does not write a prefix filter.
	Prefix PrefixExtractor
}

type ReaderOptions struct {
	// ...the fields of version 2

	// Prefix is the extractor of the prefixes that MayContainPrefix
	// receives. The Reader compares its Name with the name in the table, and
	// does not read a prefix filter of another name.
	Prefix PrefixExtractor
}

type Summary struct {
	// ...the fields of version 1, whose MinSeq and MaxSeq also cover the
	// sequences of the fragments

	RangeDeletes     int64  // the entries of the block of fragments, one for each sequence of a fragment
	RangeDeleteStart []byte // the start of the first fragment
	RangeDeleteEnd   []byte // the end of the last fragment, which the range excludes
}

// RangeDeletes returns a cursor over the fragments of the table, from a
// pool. The Reader keeps the fragments in memory, so the cursor reads no
// block.
func (r *Reader) RangeDeletes() *RangeDeleteIter

// RangeDeleteIter is a cursor over the fragments of a table in the order of
// their starts. Each sequence of a fragment is a position of its own, from
// the highest sequence to the lowest.
type RangeDeleteIter struct{ /* unexported */ }

// SeekGE moves to the first position of the first fragment whose end is
// after key: the fragment that covers key, or the first fragment after it.
func (it *RangeDeleteIter) SeekGE(key []byte) bool

// SeekLT moves to the last position of the last fragment whose start is
// before key.
func (it *RangeDeleteIter) SeekLT(key []byte) bool

// First, Last, Next and Prev move as the moves of an Iter do, without a
// context. Start, End and Seq return the fragment and the sequence of the
// position, and Close returns the cursor to the pool. Open checks every
// entry of the block of fragments, so a move cannot fail.

// MayContainPrefix reports whether the table may contain a key whose prefix
// is p. It reports true for every prefix of the table. It also reports true
// when the table has no prefix filter, and when the name of its extraction
// differs from the name of the extractor of ReaderOptions.
// It reads the prefix filter partition of p of a partitioned table under
// ctx, through the cache, as MayContain reads a key filter partition.
func (r *Reader) MayContainPrefix(ctx context.Context, p []byte) (bool, error)

// Counts are the counts of the parts of a table that MaxSize bounds.
type Counts struct {
	Entries          int64 // the most entries
	KeyBytes         int64 // the most bytes of the keys of the entries, in all
	ValueBytes       int64 // the most bytes of the values, in all
	Longest          int   // the most bytes of one key
	RangeDeletes     int64 // the most calls of AddRangeDelete
	RangeDeleteBytes int64 // the most bytes of the starts and the ends of those calls, in all
}

// MaxSize returns the most bytes of a table that a Writer under o writes
// for c.
func (o WriterOptions) MaxSize(c Counts) (int64, error)
```

`ErrRange` and `ErrPrefix` classify as `errs.Invalid`. `AddRangeDelete`
returns `ErrTooLarge` for a fragment whose entry could bring the block of
fragments past 2^32 - 1 bytes, `ErrClosed` after Close, and the error of an
earlier write of the Writer. `NewFragmenter` returns `ErrOptions` for a nil
`RangeDeleter`. A Fragmenter returns `ErrRange` and `ErrOrder` for its own
rules, and the error of its `RangeDeleter`.

No release contains `MaxSize`, so this design replaces its four counts with
`Counts`. The section of RFC-0077 on `MaxSize` changes with it.

### Reads

- `Open` of a table of version 3 reads the footer and then the block of
  places. It reads the index and the key filter as version 1 or version 2
  reads them, by the flag. It reads the block of fragments, checks every
  entry of it and keeps it. It reads the prefix filter block or the
  top-level index of the prefix filter only for a `ReaderOptions.Prefix` of
  the name in the table, and keeps it as it keeps the key filter or its
  top-level index. It compares the CRC32C of each block with the CRC32C in
  the block of places or in the footer.
- `RangeDeletes` searches the restart points of the block of fragments in
  memory. A move takes no context, does not read a block and cannot fail.
- `MayContainPrefix` searches the top-level index of the prefix filter for
  the first partition whose last prefix is at or after p, and reads the
  partition through the cache with the role `FilterBlock`.
- `Get`, `MayContain`, `Span` and the cursors over entries read a table of
  version 3 as they read version 1 or version 2. A fragment does not hide an
  entry from them.
- `Footprint` adds the block of fragments and the prefix filter or its
  top-level index.

A read of a key at the snapshot s by an engine checks the fragments of the
tables that are newer than the table of the entry. The positions of one
fragment have one end, and no other fragment has that end:

```go
it := r.RangeDeletes()
defer it.Close()
ok := it.SeekGE(key) && bytes.Compare(it.Start(), key) <= 0
for end := it.End(); ok && bytes.Equal(it.End(), end); ok = it.Next() {
	// The fragment covers key at it.Seq(), from the highest sequence down.
	// The engine compares it.Seq() with s and with the sequence of the entry.
}
```

### The bound of a table

`MaxSize` adds three parts to the parts that RFC-0077 lists:

- Each call of `AddRangeDelete` takes an entry of the block of fragments at
  a restart point with no shared prefix. The entry has three varints, the
  start, a sequence and the end. The starts and the ends take at most
  `RangeDeleteBytes` bytes in all. The block adds a uint32 for each restart
  point, one for their count and a trailer.
- A table has at most one prefix for each entry, so the prefix filter takes
  the bound of the key filter. A partitioned table adds the top-level index
  of the prefix filter. A prefix is a byte prefix of its key, so the keys of
  that index take at most the bytes of the keys of the entries.
- The block of places takes the flags, three places, the name of the
  extractor and a trailer. The offset of each part and the length of a
  top-level index take at most the bytes of the varint of the bound of the
  table. The length of a filter block takes the bytes of the varint of the
  largest filter, and the length of the block of fragments the bytes of the
  varint of its bound. A missing part takes two bytes.

### Invariants

- A table of version 1 or version 2 keeps its bytes. The Writer writes
  version 3 only for a table with a fragment or a prefix filter.
- The fragments of a table do not overlap and follow the order of their
  starts. The sequences of one fragment fall and do not repeat.
- `RangeDeletes` yields the fragments that `AddRangeDelete` added, in order.
- `MayContainPrefix` reports true for every prefix of the keys of a table
  that a Reader of the same extraction reads.
- A table of version 3 returns the entries, the `Get` results and the spans
  of a table of version 1 or version 2 of the same entries.
- Every byte of a table of version 3 is under a CRC32C, and the block of
  places has the CRC32C of each part that it places.
- `MaxSize` of the counts of a table is at least the size of the table.

### Bounds

| Quantity | Bound |
|---|---|
| The memory of a Reader | The parts of version 1 or version 2, the block of fragments, and the prefix filter or its top-level index |
| The memory of a Writer | The parts of version 1 or version 2, the block of fragments until Close, and the hashes and the partitions of the prefix filter |
| The memory of a Fragmenter | The tombstones that start at the last start, with their keys |
| The entries of the fragments of n tombstones | At most n entries for tombstones that do not overlap, and n² for n nested tombstones |
| A move of a `RangeDeleteIter` | No read, and a search of the restart points for a seek |
| The reads of `MayContainPrefix` | One prefix filter partition of a partitioned table, through the cache |

### Failure handling

| Condition | Function | Result |
|---|---|---|
| A start at or after its end | `AddRangeDelete`, `Fragmenter.Add` | `ErrRange`, and no change |
| A fragment that does not follow the one before it | `AddRangeDelete` | `ErrOrder`, and the Writer is unchanged |
| A tombstone that starts before the one before it | `Fragmenter.Add` | `ErrOrder`, and no change |
| A prefix that is not a byte prefix of its key, or that falls | `Writer.Add` | `ErrPrefix`, and the Writer is unchanged |
| A `Prefix` without `FilterBits`, or with an empty name | `NewWriter`, `MaxSize` | `ErrOptions` |
| A nil `RangeDeleter` | `NewFragmenter` | `ErrOptions` |
| A block of places with an unknown flag, a place outside its part of the table, or a CRC32C that differs | `Open` | `ErrCorrupt` |
| A block of fragments without entries, with an entry that does not decode, or with a fragment that does not follow the one before it | `Open` | `ErrCorrupt` |
| A prefix filter partition that does not check | `MayContainPrefix` | `ErrCorrupt` |

### Allocation contract

| Call | Objects |
|---|---|
| `Writer.AddRangeDelete` | 0 when the block of fragments has room |
| `Writer.Add` with a `Prefix` | As without, and what `Prefix` allocates |
| `Fragmenter.Add`, `Fragmenter.Flush` | 0 when the buffers of the tombstones have room |
| `Open` of version 3 | The parts of version 1 or version 2, the block of places and the name of the extraction, the block of fragments and the prefix filter or its top-level index |
| `Reader.RangeDeletes`, `RangeDeleteIter` moves and `Close` | 0 once the pool has a cursor |
| `MayContainPrefix` | As `MayContain` |

### Tests

| # | Guarantee |
|---|---|
| 1 | The fragments of a Fragmenter over drawn tombstones in the order of their starts cover each key with the sequences of the tombstones that cover it, do not overlap and follow the order of their starts |
| 2 | `AddRangeDelete` returns `ErrOrder` for each fragment that does not follow the one before it, and `ErrRange` for an empty range, and the Writer is unchanged |
| 3 | A table of drawn entries and fragments returns the fragments in both directions, and `SeekGE` and `SeekLT` return what a sorted model returns |
| 4 | A tombstone over the 10,000 keys of a range takes one fragment, and a seek of the older table to the end of the fragment finds the first live key |
| 5 | `MayContainPrefix` reports true for every prefix of a table of drawn keys and a drawn extractor |
| 6 | Of 16 tables of 64 logs each out of 1,024, a prefix filter of 8 bits reports true for each table with a key of a log, and for at most 128 of the 15,360 checks of a table without one. `MayContainPrefix` reads the partition of its prefix alone |
| 7 | `Add` returns `ErrPrefix` for a prefix that is not a byte prefix of its key and for a prefix that falls |
| 8 | A Reader of another extraction reports true for every prefix |
| 9 | A table of version 3 returns the entries, the `Get` results and the spans of a table of version 1 or version 2 of the same entries |
| 10 | Golden tables of version 3, partitioned and not, keep their bytes, and every truncation and every flipped bit returns `ErrCorrupt` |
| 11 | The golden tables of version 1 and version 2 keep their bytes |
| 12 | `MaxSize` is at least the size of each table of drawn entries, fragments and extractors |
| 13 | The allocation tests and the benchmarks state the counts of the allocation contract |
| 14 | The suites pass under the race detector, cover every statement, and dokimi-mutate-go detects every mutant |

## Alternatives considered

### A. A tombstone for each key

A store would keep deleting a range with a tombstone for each key, as it
does today.

**Why not:** the probe of issue 84 steps over 2,000,001 entries in 57.0 ms to
the first live key, and its tombstones take 14,358,985 bytes.

### B. A file of range tombstones beside each table

An engine would keep the range tombstones of a table in a file of its own.

**Why not:** every read and every compaction would open two files for each
table, and the engine would keep the bounds of both in step.

### C. Tombstones as they arrive, fragmented at Open

The Writer would store the tombstones as they arrive. `Open` would fragment
them, as RocksDB does.

**Why not:** each `Open` would repeat the fragmentation and allocate the
fragments. RocksDB's post on DeleteRange names a format that stores
fragments for that reason.

### D. A Writer that fragments

Issue 84 proposes that the Writer fragments the tombstones that
`AddRangeDelete` receives.

**Why not:** the bytes of the table would depend on the overlaps of the
tombstones, which the engine cannot count before the write. `MaxSize` could
bound n tombstones only by n² entries. With the `Fragmenter` apart, the
engine counts the fragments before it reserves the space, and a compaction
that merges fragments of its inputs fragments them once.

### E. A block of named places

The block of places would map names to places, as the metaindex block of
LevelDB and RocksDB does.

**Why not:** names need a vocabulary and a rule for a name that a Reader
does not know, which RFC-0062 rejects in its alternative L. A Reader that
skipped an unknown part could miss tombstones. Fixed places and the flags
make a Reader refuse what it does not know.

### F. A longer footer for version 3

The footer of version 3 would have a field for each new part.

**Why not:** a Reader reads the last 80 bytes before it knows the version. A
longer footer would need a second read or a guess of its length, and a
checksum of its own.

### G. Prefixes in the key filter

The Writer would add the hash of each prefix to the key filter, as RocksDB
adds prefixes to its whole-key filter.

**Why not:** the partitions of the key filter end at keys. A prefix whose
keys span two partitions would need its hash in both. The added hashes would
also raise the share of false positives of `Get`.

### H. A prefix function and a name in two fields

`WriterOptions` would take `Prefix func(key []byte) []byte` and a
`PrefixName string`, as issue 85 proposes.

**Why not:** a field of a function type makes `WriterOptions` a type that
`==` does not compare, and the name could differ from the function. A nil
slice for a key without a prefix would also differ from an empty prefix
only by its pointer.

### I. Range tombstones in partitioned tables alone

Version 3 would have the layout of version 2 alone.

**Why not:** a table without partitions could not contain a range
tombstone, and a store of small tables would take partitions to delete a
range. The flag of the block of places keeps the two features apart.

### J. Bounds of the summary over the entries and the fragments

Issue 84 proposes that `SmallestKey` and `LargestKey` cover the fragments
too.

**Why not:** `LargestKey` would be the key of an entry in one table and the
excluded end of a fragment in another. `LargestSeq` would then have no
entry. `RangeDeleteStart` and `RangeDeleteEnd` are the bounds of the
fragments. The engine places a table by both pairs.

### K. Fragments through the cache

A Reader would lend the block of fragments from the cache under a
`BlockRole` of its own.

**Why not:** an engine checks the fragments of each newer table for each
read. Each check would then take a lookup of the cache. RocksDB keeps the
blocks of range tombstones in memory.

## Drawbacks

- `sstable` reads three format versions, and its tests cover each.
- The Reader keeps the block of fragments in memory, so `Footprint` grows
  with the fragments of a table.
- n nested tombstones take n² entries, because a fragment contains each
  sequence that covers it.
- The engine passes tombstones that overlap through a `Fragmenter`.
- `Open` of version 3 reads up to three more blocks: the block of places, the
  block of fragments and the prefix filter or its top-level index.
- `Writer.Add` calls `Prefix` once for each distinct key, and checks its
  prefix.
- `MaxSize` changes its four counts into `Counts`.

## Open questions

None.

## Unresolved / future work

None.

## References

- Issue 84, <https://github.com/thesm-os/core/issues/84>, and issue 85,
  <https://github.com/thesm-os/core/issues/85>.
- RFC-0060, approximate-membership filters and XXH3 hashing.
- RFC-0062, sorted tables and memtables: the format of version 1, its
  drawbacks and alternatives F and L.
- RFC-0064, checksums of table blocks in their parents.
- RFC-0077, partitioned index and filters of a table.
- RocksDB, DeleteRange: A New Native RocksDB Operation,
  <https://rocksdb.org/blog/2018/11/21/delete-range.html>.
- RocksDB, Prefix Seek, <https://github.com/facebook/rocksdb/wiki/Prefix-Seek>.
- cockroachdb/pebble at a7b8321871, `sstable/writer.go` (`DeleteRange`),
  `internal/keyspan/fragmenter.go` (`Fragmenter`) and
  `internal/keyspan/iter.go` (`FragmentIterator`),
  <https://github.com/cockroachdb/pebble/tree/a7b8321871e3e3c19be21eaf42a6aaa7e2d04f23>.
