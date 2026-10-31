---
rfc: 0081
title: Fragment Partitions, Version Suffixes and Checks of a Table
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-10-31
updated: 2026-10-31
discussion: https://github.com/thesm-os/core/issues/105, https://github.com/thesm-os/core/issues/106, https://github.com/thesm-os/core/issues/107
supersedes: none
superseded-by: none
produces-adr: ADR-0076
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# RFC-0081: Fragment Partitions, Version Suffixes and Checks of a Table

## Summary

We propose three changes to the tables of `sstable` and one to the lists of
`skiplist`.

- A table in partitions cuts its block of fragments into fragment
  partitions. A Reader keeps the top-level index of the fragment partitions
  in memory. It reads each partition through the cache under the
  `BlockRole` `FragmentBlock`. The moves of `RangeDeleteIter` take a
  context, and `RangeDeleteIter.Err` returns the error of a read.
- `WriterOptions`, `ReaderOptions`, `Merger` and `skiplist.Config` take
  `SuffixBytes`, the width of a version at the end of each key.
  `CompareWithSuffix` orders keys by their bytes before the version, then by
  version from the highest to the lowest, then by sequence. The filter of
  the keys hashes each key without its version. A table records its width.
  `Open` returns `ErrSuffix` for a table of another width.
- `Reader.Check` reads every block that a read of the Reader can load from
  the File. It calls a function of the caller with each block that fails
  its checks and continues with the next block.

Both changes of the format add a flag to the block of places of format
version 3. A table without fragment partitions and without versions keeps
its bytes.

## Motivation

### The fragments of a table take memory outside the cache

A Reader keeps the block of fragments of its table in memory. Its
`Footprint` grows by 49 bytes for each fragment. The probe of issue 105 ran
on core at v0.35.0 with Go 1.27.3. It wrote a table of logs with two
records each, under the keys `log/<log>/<record>`, in partitions of
4 KiB. A second table has the same entries and one tombstone for each log.
The tombstone deletes the first record of its log:

| Logs | `Footprint` without tombstones | `Footprint` with a tombstone for each log |
|---|---|---|
| 10,000 | 464 B | 490,508 B |
| 100,000 | 4,091 B | 4,904,135 B |
| 1,000,000 | 39,903 B | 49,039,947 B |

An engine of 10 million logs bounds the memory of an idle log by the blocks
that its block cache keeps. One truncation of each log takes 490 MB of
fragments in the Readers of the engine, outside the capacity of the cache.
Alternative K of RFC-0078 kept the fragments out of the
cache, because each check of the fragments would take a lookup of the
cache.

### A key sorts after the versions of a longer key

`Compare` orders keys by `bytes.Compare`. A store with versions writes the
version of each item after its key. RocksDB writes a user-defined timestamp
in the same place. With versions, a key that is a prefix of a longer key
sorts after the longer key when the next byte of the longer key is below
the first byte of the version.

The probe of issue 106 ran on core at v0.35.0 with Go 1.27.3. It wrote the
key `a` at the version 5 and the key `a\x00` at the version 9. Each key has
the complement of its version after it in big-endian order, so the newer
versions of a key sort first:

```text
order of the table: "a\x00" "a"
SeekGE of the newest version of "a" finds the user key "a\x00"
```

A read of `a` seeks the newest version of `a`. It finds `a\x00` and reports
`a` missing. A scan returns `a\x00` before `a`. A `skiplist.List` orders its
keys in the same way. The filter of a table hashes each key with its
version, so a lookup of the filter finds a key only at a version that the
caller knows.

A store escapes each key today, so that no escaped key is a prefix of
another. The escape adds 2 bytes to each key and 1 byte for each 0x00 byte
of the key. The cost of a memtable entry then counts bytes that the store
did not write. The store also decodes each key that it returns.

### A cursor stops at the first damaged block

A scrub of a store reads every block of a table. It continues after each
damaged region and reports each region with its keys. A cursor of a Reader
ends at the first data block that fails its checks, and its `Err` returns
`ErrCorrupt`. The caller then cannot tell the place of the block or its
keys. The index of the table contains the last key and the place of every
data block, but no call returns them.

The probe of issue 107 wrote a table of 64 entries of 1 KiB in data blocks
and index partitions of 4 KiB. It flipped one byte in the data blocks 3 and
10 of the 16 data blocks:

| Read | Result |
|---|---|
| A cursor forward from `First` | 12 entries up to `k11`, then `ErrCorrupt` |
| A cursor back from `Last` | 20 entries down to `k44`, then `ErrCorrupt` |
| `SeekGE` to `k24` | The cursor finds `k24` |

The data blocks 4 to 9 contain 24 intact entries from `k16` to `k39`. A
scrub that does not know their keys reads none of them. A repair then
copies 6 intact blocks beside the 2 damaged ones.

### Prior art

- Pebble reads the range deletions of a table through its block cache.
  `Reader.NewRawRangeDelIter` calls `readRangeDelBlock`. That function
  reads the block with `blockReader.Read` under the kind
  `blockkind.RangeDel`.
- RocksDB's `BytewiseComparatorWithU64Ts` compares two keys without their
  timestamps first: "For the same user key with different timestamps,
  larger (newer) timestamp comes first." Its `RangeTombstone` keeps the
  timestamp of a range tombstone at the end of its start key and of its end
  key. A `FragmentedRangeTombstoneList` keeps a timestamp beside each
  sequence of a fragment.
- Pebble's `Comparer.Split` "returns the length of the prefix of the user
  key that corresponds to the key portion of an MVCC encoding scheme to
  enable the use of prefix bloom filters."
- Pebble's `Reader.ValidateBlockChecksums` reads the data blocks, the index
  partitions, the filter, the range deletions and the metadata blocks of a
  table in the order of their offsets. It returns the first error.

## Detailed design

### Components

| Component | Responsibility |
|---|---|
| `Writer` | Cuts the fragments of a table in partitions into fragment partitions, orders keys by the width of its options and records the width |
| `Reader` | Keeps the top-level index of the fragment partitions and refuses a table of another width at `Open` |
| `RangeDeleteIter` | Reads the fragment partitions through the cache under the context of each move |
| `FragmentBlock` | The `BlockRole` of a fragment partition |
| `CompareWithSuffix` | Orders entries whose keys end with a version |
| `Fragmenter` | Splits range tombstones at their keys without versions |
| `Merger.SuffixBytes` | The width of the order of a merge |
| `skiplist.Config.SuffixBytes` | The width of the order of a List |
| `Reader.Check`, `BlockDamage` | Reads every block that a read can load and reports each block that fails its checks |
| `ErrSuffix` | Refuses a table whose width differs from the width of the Reader |

### The format

The block of places of format version 3 has two more flags:

| Bit | Name | Meaning |
|---|---|---|
| 0 | Partitions | The index and the filters are in partitions, as RFC-0078 states |
| 1 | Fragment partitions | The place of the fragments states the top-level index of the fragment partitions |
| 2 | Versions | A varint of the width of the versions follows the name of the extractor |

- The Writer sets bit 1 for a table in partitions with fragments. It sets
  bit 2 for a `SuffixBytes` above 0. A table with versions has a block of
  places, so its format version is 3 also without fragments and without a
  filter of prefixes.
- A Reader refuses a flag that it does not know, so a Reader of v0.36.0
  refuses bit 1 and bit 2.
- `Open` refuses bit 1 without bit 0 and without a place of the fragments.
  It refuses bit 2 with a width of 0, and a byte after the width.
- A table of version 3 in partitions without bit 1 has one block of
  fragments. A Reader reads such a table as before.

A table in partitions with fragments has its parts in this order:

1. The data blocks.
2. The filter partitions and the partitions of the filter of prefixes.
3. The index partitions.
4. The fragment partitions.
5. The top-level index of the fragments.
6. The top-level indexes of the filter, of the filter of prefixes and of
   the index.
7. The block of places and the footer.

A fragment partition has the layout of the block of fragments. The Writer
ends a partition at the first fragment after the entries of the partition
take `PartitionSize` bytes or more, so the positions of one fragment are in
one partition. The top-level index of the fragments has an entry for
each partition. Its key is the end of the last fragment of the partition
with the sequence 0. Its value is the offset, the length and the count of
the entries of the fragments up to the end of the partition as varints.
The CRC32C of the partition follows them as a uint32.

### Fragment partitions

- `Open` reads the top-level index of the fragments and the first fragment
  partition. The last entry of the top-level index gives `RangeDeleteEnd`
  and `RangeDeletes`. The first partition gives `RangeDeleteStart`. `Open`
  does not add the partition to the cache.
- A move of a `RangeDeleteIter` that needs a partition checks its context
  first, as a move of an `Iter` does. It then looks the partition up in the
  cache under `FragmentBlock`. On a miss it reads the partition from the
  File.
- The cursor checks every entry of a partition that it reads from the File,
  before it adds the partition to the cache. The first fragment of the
  partition starts at or after the end of the last fragment before it. Its
  last fragment ends at the key of its top-level entry. The count of its
  entries matches the counts of the top-level index.
- A move reports false for a partition that fails, a context that ended or
  an error of the File. `Err` then returns `ErrCorrupt`, the error of the
  context or an error that wraps the error of the File.
- `SeekGE` searches the top-level index for the first partition whose last
  end is after the key. It reads that partition alone.
- `SeekLT` searches the top-level index for the first partition whose last
  end is at or after the key. The last fragment that starts before the key
  is in that partition or is the last fragment of the partition before it.
  `SeekLT` reads at most these two partitions.
- A cursor of a table without fragment partitions searches the block of
  fragments in memory. Its moves ignore their context and do not fail.
- `Footprint` counts the top-level index of the fragments in place of the
  block of fragments.
- A `blockcache.Cache` pins a fragment partition of a table of `PinTable`
  as it pins an index partition.

The probe of issue 105 measures the tables of this design with Go 1.27.3.
The tables without tombstones keep their bytes. The fragments take 0.5
bytes of `Footprint` for each tombstone, against 49 bytes in the block of
fragments:

| Logs | `Footprint` without tombstones | `Footprint` with a tombstone for each log |
|---|---|---|
| 10,000 | 464 B | 5,630 B |
| 100,000 | 4,091 B | 57,219 B |
| 1,000,000 | 39,903 B | 572,398 B |

### Version suffixes

With a `SuffixBytes` of `w`, the last `w` bytes of a key are its version.
`CompareWithSuffix` first orders two entries by the bytes of their keys
before their versions, as `bytes.Compare` orders them. It then orders their
versions in the reverse order of `bytes.Compare`. Last it orders their
sequences from the highest to the lowest. A version of `w` bytes in
big-endian order orders as an unsigned integer, from the highest to the
lowest. A key of fewer than `w` bytes has no bytes before its version, and
all its bytes are its version. The order is total over every key, so the
package refuses no key for its length. A `SuffixBytes` of 0 orders
as `Compare` orders.

- The Writer orders the entries by `CompareWithSuffix`. A key is distinct
  from the key before it when their bytes before the version differ, so the
  filter of the keys has the hash of each key without its version. The
  Writer passes each key without its version to the `Prefix` of the
  options.
- `Writer.AddRangeDelete` takes a start that ends with the version of the
  fragment and an end without a version. The positions of one fragment have
  one start without its version and one end. They follow one another by
  version and then by sequence, from the highest to the lowest. The block
  of fragments stores each position as before, with the start and its
  version in the key of the entry.
- `NewFragmenter` takes the width. A `Fragmenter` splits its tombstones at
  their starts without their versions and at their ends. A fragment
  contains each version and sequence of the tombstones that cover it once.
- `Open` compares the width of the table with the `SuffixBytes` of
  `ReaderOptions`. It returns `ErrSuffix` when they differ. A table of
  version 1 or version 2 has the width 0.
- `Reader.Get` returns the entry of the key with its version, as before.
  `Get` and `MayContain` ask the filter for the key without its version, so
  an engine that reads the newest version of a key at a snapshot asks
  `MayContain` with any version of the key. `MayContainPrefix` takes a
  prefix of a key without its version.
- The seeks of a `RangeDeleteIter` compare the starts and the ends of the
  fragments with the key without its version.
- A `Merger` with a `SuffixBytes` orders its sources by
  `CompareWithSuffix`.
- A `skiplist.List` with a `SuffixBytes` orders its entries in the same
  order. The prefix of 8 bytes in a node is the prefix of the key without
  its version.

An engine checks the fragments of a newer table for an entry of the key
`k` at the version `v` and the sequence `s` with this loop:

```go
it := r.RangeDeletes()
defer it.Close()
user := k[:len(k)-w]
for ok := it.SeekGE(ctx, k); ok && bytes.Compare(it.Start()[:len(it.Start())-w], user) <= 0; ok = it.Next(ctx) {
	// The fragment covers k at the version it.Start()[len(it.Start())-w:]
	// and the sequence it.Seq(), from the highest version down. The engine
	// compares them with v and s.
}
if err := it.Err(); err != nil {
	return err
}
```

The fragment of `SeekGE` ends after `k`. The next fragment starts at or
after that end, so the loop stops at the last position of the fragment of
`SeekGE`.

### Checks

`Reader.Check` reads each data block of the index. Of a table in partitions
it also reads each index partition, each filter partition and each fragment
partition. It reads the partitions of the filter of prefixes only when the
Reader reads that filter. It reads every block from the File. It does not
look the block up in the cache, and it does not add the block.

A block fails its checks when its CRC32C differs from the CRC32C of its
trailer or of its parent. It also fails when it does not decompress or
when its layout fails:

- A data block has valid restart points and entries that decode in the
  order of the table. Its first entry is after the last entry of the block
  before it. Its last entry is the entry of its index entry.
- An index partition has entries that decode in the order of the table,
  each with the place of a data block. Its first entry is after the last
  entry of the partition before it. Its last entry is the entry of its
  top-level entry. Its number of blocks matches the counts of the
  top-level index.
- A filter partition parses as a filter.
- A fragment partition passes the checks of a partition that a cursor
  reads.

`Check` calls the function of the caller with a `BlockDamage` for each
block that fails. The function receives the role and the place of the
block, and the bounds of its keys that the index states:

| Role | `After` | `Last` | Keys of the block |
|---|---|---|---|
| `DataBlock` | The key of the index entry before the block | The key of the index entry of the block | From `After` up to `Last` |
| `IndexBlock` | The key of the top-level entry before the partition | The key of the top-level entry of the partition | The keys of its data blocks, from `After` up to `Last` |
| `FilterBlock` | The last key or prefix of the partition before it | The last key or prefix of the partition | The keys or prefixes after `After` up to `Last` |
| `FragmentBlock` | The end of the last fragment before the partition | The end of the last fragment of the partition | The fragments of the partition start at or after `After` and end at or before `Last` |

- `After` is nil for the first block of its kind. The bounds of a data
  block include `After` itself, because two versions of one key can be in
  two blocks.
- The keys of a filter partition and of a fragment partition of a table
  with versions are keys without versions.
- A damaged index partition hides the places of its data blocks. `Check`
  reports the partition and continues with the next partition.
- An entry of the index block or of a top-level index that does not decode
  returns `ErrCorrupt`. `Open` checked the checksums of those blocks, so
  such an entry is a fault of the Writer. `Check` cannot place the blocks
  after it.
- The error of the function, the error of the context and an error of the
  File end `Check` with that error.
- `Check` does not read the parts that the Reader keeps in memory again. No
  read of the Reader loads them. A scrub that must check them on the disk
  opens the table again.

### API

```go
package sstable

// FragmentBlock is the role of a fragment partition of a table in
// partitions.
const FragmentBlock BlockRole = 4 // fragments

type WriterOptions struct {
	// ...the fields of version 3

	// SuffixBytes is the width of the version at the end of each key. The
	// Writer orders the entries by CompareWithSuffix of the width. 0 orders
	// them by Compare.
	SuffixBytes int
}

type ReaderOptions struct {
	// ...the fields of version 3

	// SuffixBytes is the width of the versions of the table. Open returns
	// ErrSuffix for a table of another width.
	SuffixBytes int
}

// CompareWithSuffix returns -1, 0 or +1 for the order of the entry (aKey,
// aSeq) and the entry (bKey, bSeq). Each key ends with a version of
// suffixBytes bytes.
func CompareWithSuffix(aKey []byte, aSeq uint64, bKey []byte, bSeq uint64, suffixBytes int) int

type Merger struct {
	// SuffixBytes is the width of the versions of the keys of the sources.
	SuffixBytes int

	// ...unexported fields
}

// NewFragmenter returns a Fragmenter that passes its fragments to dst. The
// starts of its tombstones end with a version of suffixBytes bytes.
func NewFragmenter(dst RangeDeleter, suffixBytes int) (*Fragmenter, error)

// The moves of a RangeDeleteIter take the context of the read of a
// partition.
func (it *RangeDeleteIter) First(ctx context.Context) bool
func (it *RangeDeleteIter) Last(ctx context.Context) bool
func (it *RangeDeleteIter) SeekGE(ctx context.Context, key []byte) bool
func (it *RangeDeleteIter) SeekLT(ctx context.Context, key []byte) bool
func (it *RangeDeleteIter) Next(ctx context.Context) bool
func (it *RangeDeleteIter) Prev(ctx context.Context) bool

// Err returns the error that exhausted the cursor, or nil.
func (it *RangeDeleteIter) Err() error

// BlockDamage is a block of a table that fails its checksum, its
// decompression or its layout.
type BlockDamage struct {
	Err          error     // an error that wraps ErrCorrupt
	After, Last  []byte    // the bounds of the keys of the block
	Offset, Size int64     // the place of the block, and the size of the block and its trailer
	Role         BlockRole // the role of the block in the table
}

// Check reads every block that a read of r can load from the File under
// ctx. It calls fn with each block that fails its checks, and continues
// with the next block.
func (r *Reader) Check(ctx context.Context, fn func(*BlockDamage) error) error

// ErrSuffix is returned by Open for a table whose width of the versions
// differs from the SuffixBytes of ReaderOptions.
var ErrSuffix error
```

```go
package skiplist

type Config struct {
	// ...the fields of RFC-0076

	// SuffixBytes is the width of the version at the end of each key. The
	// List orders its entries as sstable.CompareWithSuffix orders them.
	SuffixBytes int
}
```

`NewWriter` and `NewFragmenter` return `ErrOptions` for a negative width.
`skiplist.New` returns `ErrSize` for it. `ErrSuffix` classifies as
`errs.Invalid`.

### The bound of a table

`MaxSize` adds two parts to the parts that RFC-0078 lists:

- A table in partitions with `RangeDeletes` ends a fragment partition at
  the first fragment after `PartitionSize` bytes. It has at most one
  partition for each `PartitionSize` bytes of entries, and one more. Each
  partition adds a count of its restart points and a trailer. The
  top-level index of the fragments has an entry for each partition. The
  entry takes the end of a fragment, the sequence 0, its header, the offset
  and the length of the partition, the count of the entries and the CRC32C.
  The ends of the entries take at most `RangeDeleteBytes` bytes in all.
- A `SuffixBytes` above 0 gives every table a block of places. The varint
  of the width follows the name of the extractor.

### Invariants

- A table without fragments in partitions and without versions keeps its
  bytes. A Reader reads the tables of v0.36.0 as before.
- A `RangeDeleteIter` yields the positions that `AddRangeDelete` added, in
  their order, in both layouts.
- The positions of one fragment are in one partition.
- The Reader of a table in partitions keeps memory for each fragment
  partition and not for each fragment.
- A table, a List and a Merger of one width yield their entries in the
  order of `CompareWithSuffix`.
- `MayContain` reports true for every version of every key of a table.
- `Check` reports each block that fails its checks once. It reports no
  block that passes them.

### Bounds

| Quantity | Bound |
|---|---|
| The memory of the fragments of a Reader of a table in partitions | The top-level index of the fragments: one entry for each partition |
| The reads of a move of a `RangeDeleteIter` | One fragment partition through the cache, two for `SeekLT` |
| The reads of `Open` of a table in partitions with fragments | The reads of RFC-0078, the top-level index of the fragments and the first fragment partition |
| The reads of `Check` | Each data block and each partition once, from the File |
| The comparisons of two keys with versions | Two comparisons of bytes: the keys without their versions, then the versions |

### Failure handling

| Condition | Function | Result |
|---|---|---|
| A negative `SuffixBytes` | `NewWriter`, `MaxSize`, `NewFragmenter` | `ErrOptions` |
| A negative `SuffixBytes` | `skiplist.New` | `ErrSize` |
| A table of another width | `Open` | `ErrSuffix` |
| A block of places with bit 1 without bit 0 or without fragments, a width of 0, or a byte after the width | `Open` | `ErrCorrupt` |
| A top-level index of the fragments without entries, or a first fragment partition that fails its checks | `Open` | `ErrCorrupt` |
| A fragment partition that fails its checks | The moves of `RangeDeleteIter` | false. `Err` returns `ErrCorrupt` |
| A context that ended before the read of a partition | The moves of `RangeDeleteIter` | false. `Err` returns the error of the context |
| A block that fails its checks | `Check` | A call of the function. `Check` continues |
| An entry of an index in memory that does not decode | `Check` | `ErrCorrupt` |

### Allocation contract

| Call | Objects |
|---|---|
| `CompareWithSuffix` | 0 |
| `Writer.Add`, `Writer.AddRangeDelete` with versions or fragment partitions | As before: 0 once the buffers have room |
| A move of a `RangeDeleteIter` | 0 once the buffers of the cursor have grown to the largest partition, apart from the copy or the `Block` of a partition that the cache does not contain |
| `Fragmenter.Add`, `Fragmenter.Flush` with versions | 0 once the buffers of the tombstones have room |
| `Check` of a table that passes | 0 once the pool of the states of `Check` has a state whose buffers have grown to the largest block and the largest partition |
| `Check` of a table with a damaged block | As for a table that passes, apart from what the function of the caller allocates. The state contains the `BlockDamage`, and its keys are parts of the blocks of the index |

### Tests

| # | Guarantee |
|---|---|
| 1 | A table of drawn entries and fragments in partitions returns the fragments in both directions. `SeekGE` and `SeekLT` return what a sorted model returns, for partitions of a few entries |
| 2 | The moves of a cursor over fragment partitions return the error of the context, `ErrCorrupt` for a partition that fails and the error of the File |
| 3 | The `Footprint` of the probe of issue 105 grows with the fragment partitions. A LendingCache receives each partition under `FragmentBlock` |
| 4 | `CompareWithSuffix` is a total order that equals `Compare` at the width 0. It orders `a` before `a\x00` at every version |
| 5 | A table, a List and a Merger of drawn keys with versions yield the order of `CompareWithSuffix`. Their seeks find what a sorted model finds |
| 6 | `MayContain` reports true for every version of every key of a table with versions. `Open` returns `ErrSuffix` for another width |
| 7 | A Fragmenter over drawn tombstones with versions covers each key with each version and sequence of the tombstones that cover it |
| 8 | `Check` reports each block of a drawn set of damaged blocks once, with its role, its place and its bounds. It reports nothing for a table that passes |
| 9 | `Check` reports a damaged index partition and continues with the data blocks of the next partition |
| 10 | `Check` reads from the File and not from the cache. It returns the error of the function, of the context and of the File |
| 11 | Golden tables with fragment partitions and with versions keep their bytes. Every truncation and every flipped bit returns `ErrCorrupt` at `Open`, at a read or at `Check` |
| 12 | The golden tables of RFC-0078 read as before. The golden table of version 3 in partitions with fragments reads with its one block of fragments |
| 13 | `MaxSize` is at least the size of each table of drawn entries, fragments, widths and options |
| 14 | The allocation tests and the benchmarks state the counts of the allocation contract |
| 15 | The suites pass under the race detector and cover every statement. dokimi-mutate-go detects every mutant |

## Alternatives considered

### A. Fragments in memory for every table

The Reader would keep the block of fragments in memory. RFC-0078 chose
this layout in its alternative K.

**Why not:** the probe of issue 105 takes 49 bytes for each fragment
outside the cache: 49,039,947 bytes for 1,000,000 logs with one truncation
each.

### B. Fragments through the cache for every table

A table without partitions would read its block of fragments through the
cache too.

**Why not:** a Reader of a table without partitions keeps its index block
and its filter block in memory. Its memory grows with its keys anyway. Its
fragments would take a lookup of the cache for each check and save no
memory that the index does not already take.

### C. A fragment partition at any entry

The Writer would end a fragment partition at the first entry after
`PartitionSize` bytes, also between two positions of one fragment.

**Why not:** a fragment whose positions span two partitions takes two
reads for `SeekGE` and for `SeekLT`. A partition of whole fragments takes
one read for `SeekGE`. It takes two reads for `SeekLT` only when the key is
before the first fragment of its partition.

### D. A top-level index of the starts

The top-level index of the fragments would have the start of the last
fragment of each partition. The top-level index of the index has the last
entry of each partition in the same way.

**Why not:** the check of the fragments of a read calls `SeekGE`. `SeekGE`
searches for the first fragment whose end is after the key. A search of the
starts could not tell whether the last fragment of the partition before
covers the key, because it does not have the end of that fragment.

### E. Escaped keys

The store would keep escaping each key, as it does today.

**Why not:** the escape adds bytes to each key, and the cost of a memtable
entry counts them. The store decodes each key that it reads. With the
escape, a lookup of the filter still finds a key only at a version that the
caller knows.

### F. A comparer in the options

`WriterOptions` would take a function or an interface that orders keys.
Pebble's `Comparer` is such a type.

**Why not:** a field of a function type makes `WriterOptions` a type that
`==` does not compare. RFC-0078 rejects such a field in its alternative H.
The width of a version is the one parameter that a store with versions
needs. A table records an integer and no code.

### G. A version at the end of each fragment

The end of a fragment would end with the version of the fragment too.
RocksDB keeps the timestamp in both keys of a range tombstone.

**Why not:** a fragment has one version for each position, and its end is
the same for every position. The reads compare the end without a version.
A version at the end would add `w` bytes to each entry that the reads
ignore.

### H. A Get of the newest version

`Reader.Get` would return the first entry whose key without its version
matches. A read at a snapshot would then find the newest version at or
below it.

**Why not:** `Get` would then need to return the version that it found. Its
results do not contain a version. An engine that reads the newest version
of a key calls `MayContain` and the `SeekGE` of a cursor. The cursor
returns the whole key.

### I. A check that stops at the first damaged block

`Check` would return at the first block that fails. Pebble's
`ValidateBlockChecksums` returns at its first error.

**Why not:** a scrub would then read a table once for each damaged block. A
repair could not copy the regions of all damaged blocks of a table at once.

### J. A check through the cache

`Check` would take the blocks that the cache contains. A cursor with
`NoFill` takes them too.

**Why not:** a block in the cache passed its checks when a Reader read it.
A scrub would then not check the bytes of that block on the disk.

### K. A format version 4

A table with fragment partitions or with versions would have format
version 4 and a footer of another layout.

**Why not:** RFC-0078 designed the flags of the block of places for parts
that a Reader must not skip. A Reader of version 3 refuses an unknown flag,
so the flags of fragment partitions and of versions fit the footer of
version 3.

## Drawbacks

- The moves of `RangeDeleteIter` take a context and can fail. Every caller
  of them changes. `NewFragmenter` takes the width, and every caller of it
  changes too.
- A check of the fragments of a table in partitions takes a lookup of the
  cache. A miss reads a partition. Alternative K of RFC-0078 avoided that
  lookup. `Summary` gives the bounds of the fragments, so an engine skips a
  table whose fragments do not cover the key without a lookup.
- `Open` of a table in partitions with fragments reads two more blocks.
- A comparison of keys with versions compares twice. A `skiplist.List` with
  versions computes the prefix of each key without its version.
- `Check` reads every block of the table from the File, also the blocks
  that the cache contains.

## Open questions

None.

## Unresolved / future work

None.

## References

- Issue 105, <https://github.com/thesm-os/core/issues/105>.
- Issue 106, <https://github.com/thesm-os/core/issues/106>.
- Issue 107, <https://github.com/thesm-os/core/issues/107>.
- RFC-0066, cursors that move backward.
- RFC-0075, blocks that a cache lends to readers.
- RFC-0077, partitioned index and filters of a table.
- RFC-0078, range tombstones and prefix filters of a table: the block of
  places and alternatives H and K.
- RFC-0079, pinned partitions in a block cache.
- ADR-0070, a cursor checks its context before each block.
- RocksDB at 87d4427, `util/comparator.cc`
  (`ComparatorWithU64TsImpl::Compare`), `db/dbformat.h` (`RangeTombstone`)
  and `db/range_tombstone_fragmenter.h`
  (`FragmentedRangeTombstoneList`),
  <https://github.com/facebook/rocksdb/tree/87d442749a6a9f0fe5b9f07de7346466f8dd5acf>.
- cockroachdb/pebble at a995e89, `sstable/reader.go`
  (`NewRawRangeDelIter`, `ValidateBlockChecksums`) and
  `internal/base/comparer.go` (`Split`),
  <https://github.com/cockroachdb/pebble/tree/a995e890c06ef86c583927906e16e7f313083a3f>.
