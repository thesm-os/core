---
rfc: 0062
title: Sorted Tables and Memtables for LSM Trees
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-10-15
updated: 2026-10-16
discussion: none
supersedes: none
superseded-by: none
produces-adr: ADR-0057
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# RFC-0062: Sorted Tables and Memtables for LSM Trees

## Summary

We propose the mechanisms of an LSM tree as two packages, and no engine:

- `sstable` writes and reads sorted tables: data blocks with prefix
  compression and restart points, an index block, a binary fuse filter and
  a footer, each block under a CRC32C. It also merges sorted runs, so a
  read combines memtables and tables in one order.
- `skiplist` is the memtable: an insert-only ordered map that many
  goroutines write and read at once, without locks. Its nodes and its bytes
  are in two pointer-free arenas, so 1M live entries cost the garbage
  collector nothing that a probe could measure.

An entry is a key, a 64-bit sequence and a value. Both packages order
entries by key and, for one key, from the highest sequence to the lowest.
The engine that schedules flushes and compactions belongs to the system
that runs it, outside core.

## Motivation

### The engine and core

An LSM engine is a keyed-value store, and core refuses a unified
keyed-value seam for 1.x. Its compaction picker, its stall bounds and the
timing of its flushes are the policies of the system that runs it. Core
ships the mechanisms that have one contract each: a table format, a
memtable, a merge, a filter and a codec. The filter and the codec have
proposals of their own, and this proposal uses both.

### Method of the measurements

A probe outside this module ran each benchmark with Go 1.27.2 on four cores
of an AMD Ryzen 9 9950X3D in a capped scope, three times for 200 ms, not
pinned to CPUs. The keys were random and 16 bytes long, and the values 100
bytes. Inserts filled a structure sized for the run after a collection.
Lookups hit random keys of a structure of 1M keys. The GC column is the GC
CPU time of one `runtime.GC` with 1M keys live, from `runtime/metrics`. A
test checked every structure, and four concurrent writers into the
lock-free skip list, under the race detector.

### Memtable structures

| Structure | Insert, 1 writer | Insert, 4 writers | Lookup | GC per cycle | Allocations per insert |
|---|---|---|---|---|---|
| Empty heap | — | — | — | 0.146-0.147 ms | — |
| Lock-free skip list in pointer-free arrays, without `unsafe` | 693-789 ns | 219-226 ns | 1,175-1,225 ns | 0.141-0.143 ms | 0 |
| Badger v4.9.6 skip list, with `unsafe` | 799-815 ns | 278-279 ns | 1,057-1,091 ns | 0.145-0.151 ms | 5 |
| Skip list with one heap node per key | 1,409-1,535 ns | — | 1,990-2,137 ns | 210-217 ms | 3 |
| `btree.MapFunc[[]byte, []byte]` over one arena | 634-684 ns | — | 793-799 ns | 15.7-18.7 ms | 0 |
| `btree.MapFunc[uint32, uint32]` over arena offsets | 543-578 ns | — | 777-815 ns | 0.470-0.477 ms | 0 |

The skip list without `unsafe` keeps its node words in a `[]uint32` that it
reads and writes with `sync/atomic`, and its keys and values in a `[]byte`.
It inserts in 0.85-0.99 times Badger's time with one writer and in
0.78-0.81 times with four. Its lookups take 1.08-1.16 times Badger's. A
memory profile attributes Badger's five allocations per insert to
`y.AssertTruef`, which boxes its variadic arguments on every call.

`btree` inserts and looks up faster, but it does not allow a reader while a
writer runs, and a memtable serves reads during its writes.

### Blocks, checksums and the block size

- The standard library's CRC32C checksums a block of 32 KiB in 776-786 ns,
  41.7-43.1 GB/s, faster than xxhash64 at 29.3-30.6 GB/s.
- LevelDB writes blocks of 4 KiB with a restart point every 16 keys, and
  ends each block with a type byte and a 32-bit CRC.
- A db_bench run of 1M keys in RocksDB measured 324,178 random reads per
  second with blocks of 4 KB, 227,805 with 16 KB and 86,807 with 64 KB.
  Sequential reads rose from 4,231,537 to 5,380,403 per second over the
  same sizes (RocksDB PR #14287, hardware not stated). CockroachDB uses
  blocks of 32 KB.

### A block cache in the Go heap

CockroachDB on Pebble "often resulted in an RSS (resident set size) 2x what
it was when using RocksDB", because the default `GOGC` lets a heap with a
block cache of tens of gigabytes grow by about the size of the cache before
a collection. Pebble moved its block cache and memtables out of the Go heap
with the C allocator. Core does not use cgo. A process that keeps a large
block cache in the Go heap sets `GOMEMLIMIT`, under which the collector runs
more often as the memory of the process approaches the limit.

### Prior art

- LevelDB's data block stores each entry as the length of the key prefix
  that it shares with the previous key, the length of the rest of the key,
  the length of the value, the rest of the key and the value. A restart
  point stores a whole key, and the block ends with the offsets of its
  restart points and their count.
- LevelDB orders internal keys by user key and then by sequence number
  from the highest to the lowest, so a read at a snapshot finds the newest
  version first. It packs the kind of an entry into the low byte of a
  64-bit trailer, below a sequence of 56 bits.
- Pebble's `internal/arenaskl` and Badger's `skl` are skip lists over
  arenas, and both convert arena offsets to pointers with `unsafe`.
- RocksDB's `DynamicBloom` is a Bloom filter for memory only, with
  lock-free concurrent inserts.

## Detailed design

### Components

| Component | Responsibility |
|---|---|
| `sstable.Compare` | The order of entries: key with `bytes.Compare`, then sequence from the highest to the lowest |
| `sstable.Source` | A cursor over a sorted run of entries |
| `sstable.Writer` | Writes a table to an `io.Writer` from entries in order |
| `sstable.Reader` | Reads a table through an `io.ReaderAt`: point lookups and iterators |
| `sstable.Iter` | A cursor over the entries of a table |
| `sstable.Merger` | A cursor over the merge of several sources |
| `sstable.BlockCache` | The interface of a cache of decompressed blocks that readers share |
| `skiplist.List` | The memtable: an insert-only ordered map, safe for concurrent use |
| `skiplist.Iter` | A cursor over the entries of a list |

`sstable` imports `go.thesmos.sh/core/compress/snappy`,
`go.thesmos.sh/core/errs`, `go.thesmos.sh/core/filter`,
`go.thesmos.sh/core/hash/xxh3`, `go.thesmos.sh/core/pool` and the standard
library: `bytes`, `cmp`, `encoding/binary`, `errors`, `fmt`, `hash/crc32`,
`io`, `math`, `slices` and `strconv`.
`skiplist` imports `go.thesmos.sh/core/arena`, `go.thesmos.sh/core/errs`,
`go.thesmos.sh/core/hash/xxh3` and the standard library: `bytes`, `cmp`,
`errors`, `math`, `math/bits` and `sync/atomic`.

### Entries and their order

```go
// Compare returns -1, 0 or +1 for the order of two entries: by key as
// bytes.Compare orders them, then by seq from the highest to the lowest.
func Compare(aKey []byte, aSeq uint64, bKey []byte, bSeq uint64) int

// Source is a cursor over a run of entries in the order of Compare. A
// moved cursor is positioned on an entry or exhausted.
type Source interface {
	// First moves to the first entry and reports whether it exists.
	First() bool

	// SeekGE moves to the first entry at or after the entry (key, seq) and
	// reports whether it exists.
	SeekGE(key []byte, seq uint64) bool

	// Next moves to the next entry and reports whether it exists.
	Next() bool

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

The sequence is opaque to both packages. An engine packs its sequence
number and the kind of an entry, such as a deletion, into it. A read at the
snapshot s calls `SeekGE(key, s)`, and the first entry it finds with the
same key is the newest version at or below s.

### API of sstable

```go
// Compression is the codec of the data blocks of a table.
type Compression uint8

const (
	NoCompression     Compression = 0 // none
	SnappyCompression Compression = 1 // snappy
)

// Valid reports whether c is NoCompression or SnappyCompression.
func (c Compression) Valid() bool

// String returns "none" or "snappy", and "Compression(N)" for a value that
// is not valid.
func (c Compression) String() string

// WriterOptions configure a Writer. The zero value writes blocks of 4,096
// bytes with a restart point every 16 entries, without compression and
// without a filter.
type WriterOptions struct {
	BlockSize       int         // the size after which a data block ends; 0 means 4,096, at most 2^32 - 1
	RestartInterval int         // the entries between restart points; 0 means 16
	Compression     Compression // the codec of the data blocks
	FilterBits      int         // the fingerprint width of the filter: 0 for none, or 4, 8, 10, 12 or 16
}

// Summary describes a table.
type Summary struct {
	SmallestKey []byte // the key of the first entry
	LargestKey  []byte // the key of the last entry
	Entries     int64
	Size        int64  // the bytes of the table
	MinSeq      uint64 // the lowest sequence of any entry
	MaxSeq      uint64 // the highest sequence of any entry
	SmallestSeq uint64
	LargestSeq  uint64
}

// NewWriter returns a Writer that writes a table to w.
//
// Error modes:
//
//   - A BlockSize or RestartInterval below 0, a BlockSize above 2^32 - 1,
//     an invalid Compression or an unsupported FilterBits returns
//     ErrOptions.
func NewWriter(w io.Writer, opts WriterOptions) (*Writer, error)

// Add appends an entry. The entry must follow the previous entry in the
// order of Compare.
//
// Error modes:
//
//   - A key whose entry in the index could bring the index past 2^32 - 1
//     bytes returns ErrTooLarge, and the Writer is unchanged.
//   - An entry at or before the previous entry returns ErrOrder, and the
//     Writer is unchanged.
//   - An error of w returns an error that wraps it, and the Writer refuses
//     every later call with the same error. A write of fewer bytes without
//     an error returns an error that wraps io.ErrShortWrite.
//   - A call after Close returns ErrClosed.
func (w *Writer) Add(key []byte, seq uint64, value []byte) error

// Close writes the last data block, the filter, the index and the footer
// with one write, and returns the summary of the table. It does not close
// w.
func (w *Writer) Close() (Summary, error)

// Reset discards the table that w writes, and starts a new table on dst
// under the same options. It clears the error of an earlier write and
// ErrClosed. Reset keeps the buffers of w, so a Writer that writes many
// tables allocates the two keys of each summary once its buffers have grown.
func (w *Writer) Reset(dst io.Writer)

// ReaderOptions configure a Reader.
type ReaderOptions struct {
	Cache   BlockCache // nil reads every block from the file
	CacheID uint64     // the identity of the table in Cache
}

// BlockCache keeps decompressed blocks for the readers that share it. Its
// methods are safe for concurrent use. A block that Get returns is never
// written again.
type BlockCache interface {
	Get(id, offset uint64) ([]byte, bool)
	Add(id, offset uint64, block []byte)
}

// Open reads the footer, the index and the filter of the table of size
// bytes in r.
//
// Error modes:
//
//   - A footer, an index, a filter or a first data block that fails its
//     checksum or its layout returns ErrCorrupt.
//   - An error of r returns an error that wraps it. A read of fewer bytes
//     without an error returns an error that wraps io.ErrUnexpectedEOF.
func Open(r io.ReaderAt, size int64, opts ReaderOptions) (*Reader, error)

// Summary returns the summary of the table.
func (r *Reader) Summary() Summary

// MayContain reports whether the table may contain an entry with key. It
// returns true for every key of the table, and true for every key when the
// table has no filter.
func (r *Reader) MayContain(key []byte) bool

// Get returns the value of the first entry at or after (key, seq) when that
// entry has key, with its sequence. It appends the value to dst.
//
// Error modes:
//
//   - A block that fails its checksum, its decompression or its layout
//     returns ErrCorrupt.
//   - An error of the ReaderAt returns an error that wraps it.
func (r *Reader) Get(key []byte, seq uint64, dst []byte) (value []byte, found uint64, ok bool, err error)

// NewIter returns a cursor over the entries of the table. The caller calls
// Close on it, which returns its buffers for reuse.
func (r *Reader) NewIter() *Iter

// Iter implements Source over a table.
type Iter struct{ /* unexported */ }

// Close returns the buffers of the cursor for reuse, and returns the error
// of Err. The cursor is unusable after Close. A second Close returns nil.
func (it *Iter) Close() error

// Merger is a Source over the merge of other sources. Entries that compare
// equal yield in the order of their sources.
type Merger struct{ /* unexported */ }

// Reset replaces the sources of m. m keeps sources and its heap memory.
// Reset does not close the sources that it replaces.
func (m *Merger) Reset(sources []Source)

// Close closes every source of m that implements io.Closer, and returns
// their errors joined with errors.Join. m keeps its heap memory for the
// next Reset.
func (m *Merger) Close() error
```

A `Reader` is safe for concurrent use. An `Iter` and a `Merger` are for one
goroutine. `Merger.Close` closes each source that has a `Close` method, such
as an `Iter` of a table, and a cursor of a memtable has nothing to close. A
caller closes each such source once, either through `Merger.Close` or after
a `Reset` replaced the source. The errors are `ErrOptions`, `ErrOrder`,
`ErrClosed` and `ErrTooLarge`, which classify as `errs.Invalid`, and
`ErrCorrupt`, which classifies as `errs.Integrity`.

### API of skiplist

```go
// List is an insert-only ordered map of entries, in the order of
// sstable.Compare. Its node words and its bytes are in two arenas of fixed
// capacity that never move, so every key and value that a List returns is
// valid for the life of the List, or until Reset.
//
// # Concurrency
//
// Add, Get, Len, Size and the methods of Iter are safe for concurrent use.
// Reset is not safe concurrently with any other method.
type List struct{ /* unexported */ }

// Config configures a List. DataBytes and NodeBytes are required.
type Config struct {
	DataBytes int    // the capacity of the arena of keys and values, from 1 to 2^32 - 1 bytes
	NodeBytes int    // the capacity of the arena of nodes, from 1 to 2^32 - 1 bytes
	Seed      uint64 // the seed of the hash of the heights of the nodes
}

// New returns an empty List with the arenas of cfg. The heights of the
// nodes derive from the Seed of cfg.
//
// Error modes:
//
//   - A DataBytes or a NodeBytes below 1 or above 2^32 - 1 returns ErrSize.
func New(cfg Config) (*List, error)

// Add inserts an entry. An Add that returned is visible to every Get and
// every cursor move that starts after it.
//
// Error modes:
//
//   - An entry whose node or bytes do not fit the arenas returns ErrFull,
//     and Add does not add the entry.
//   - An entry with the key and the seq of an entry of the List returns
//     ErrExists.
func (l *List) Add(key []byte, seq uint64, value []byte) error

// Get returns the value of the first entry at or after (key, seq) when that
// entry has key, with its sequence.
func (l *List) Get(key []byte, seq uint64) (value []byte, found uint64, ok bool)

// Len returns the number of entries.
func (l *List) Len() int

// Size returns the bytes of both arenas in use.
func (l *List) Size() int

// Reset removes every entry and keeps the arenas. Every key and value that
// the List returned before is invalid after Reset.
func (l *List) Reset()

// NewIter returns a cursor over the entries of the List.
func (l *List) NewIter() *Iter

// Iter implements sstable.Source over a List. Its Err returns nil.
type Iter struct{ /* unexported */ }
```

`New` takes a `Config`, so a caller names each of the two capacities and
cannot swap them, as `radix.New` and `resilience.NewLimiter` take theirs.

`ErrFull` classifies as `errs.Transient`, because the engine freezes the
full list and adds the entry to a new one. `ErrExists` and `ErrSize`
classify as `errs.Invalid`.

### The memtable

- The node arena is an `arena.Bump[uint32]` and the data arena an
  `arena.Bump[byte]`. A node is `5 + h` words of the
  node arena: the offset and the length of its key, the length of its
  value, the high and the low half of its sequence, and one link per level.
  A link is the word offset of the next node, and 0 is no node. The head
  node at offset 0 has 20 links. Readers and writers do not need the height
  of a linked node, so a node does not store it.
- The key and the value of a node are consecutive bytes of the data arena.
- `Add` searches every level first, and returns `ErrExists` without a
  reservation when it finds the entry. It then reserves its words and its
  bytes with `Bump.Reserve`, so writers do not lock. A reservation that
  does not fit does not change its arena. When the words fit and the bytes
  do not, the words remain reserved until `Reset`.
- `Add` writes the node's fields and bytes before it links the node, and
  links it from level 0 up with a compare-and-swap of each predecessor's
  link. A failed compare-and-swap at level 0 searches every level again.
  The search finds a writer that linked the same entry first, and `Add`
  returns `ErrExists` with its node and bytes reserved. At a higher level,
  `Add` searches the level again from the predecessor before each
  compare-and-swap.
- A reader loads every link with `sync/atomic`. The compare-and-swap that
  publishes a node is synchronized before the load that observes it, so the
  reader sees the fields that the writer wrote before.
- The height of a node is 1 plus the levels that a hash of its entry
  passes, with a threshold of e^-k for level k, at most 20. The hash
  multiplies `xxh3.Hash` of the key and the sequence into 128 bits, each
  combined with a secret word that derives from the list's seed, and folds
  the halves. So a writer does not need a shared generator, and a caller
  that does not know the seed cannot choose keys of one height.

### The table format

The layout is frozen from the release that first contains it. Integers are
big-endian, and a varint is the unsigned varint of `encoding/binary`.

A table is its data blocks, its filter block, its index block and its
footer. Every block is followed by a trailer of 5 bytes: its compression
byte, 0 for none and 1 for Snappy, and the CRC32C of the stored bytes of
the block and the compression byte.

A data block contains its entries, then a `uint32` offset of each restart
point, then the `uint32` count of the restart points. An entry is:

| Field | Encoding |
|---|---|
| Bytes shared with the previous internal key | varint, 0 at a restart point |
| Bytes of the internal key after the shared ones | varint |
| Length of the value | varint |
| The internal key after the shared bytes | bytes |
| The value | bytes |

The internal key of an entry is its key followed by its sequence in 8
bytes. A data block ends with the first entry that brings its entries to
`BlockSize` bytes or more, so every restart offset is below 2^32 - 1. Like
LevelDB, the writer stores a compressed block only when compression shrinks
it by more than an eighth. It stores the other blocks with the compression
byte 0.

The index block has the layout of a data block with a restart point at
every entry. Its entry for a data block has the internal key of the last
entry of the block, and a value of two varints: the offset and the length
of the stored block without its trailer. The writer stores the index block
with the compression byte 0. Its entries have at most 2^32 - 1 bytes, so
every restart offset of the index fits in 4 bytes. `Add` returns
`ErrTooLarge` for a key whose entry in the index could pass that bound.

The filter block is the encoding of a `filter.Fuse` over `xxh3.Hash` of
each distinct key of the table, stored with the compression byte 0. A table
without a filter has no filter block.

The footer has 72 bytes:

| Offset | Bytes | Field |
|---|---|---|
| 0 | 8 | Offset of the index block |
| 8 | 8 | Length of the index block, without its trailer |
| 16 | 8 | Offset of the filter block, or 0 |
| 24 | 8 | Length of the filter block, or 0 |
| 32 | 8 | Number of entries |
| 40 | 8 | Lowest sequence |
| 48 | 8 | Highest sequence |
| 56 | 4 | Format version, 1 |
| 60 | 4 | CRC32C of bytes 0 to 59 |
| 64 | 8 | Magic number 0xf65ba2ce515d9bd3, the first 8 bytes of SHA-256 of `go.thesmos.sh/core/sstable` |

### Reads

- `Open` reads the footer, then the index block and the filter block,
  checks their CRCs and keeps both in memory. It reads the first entry of
  the first data block for `Summary.SmallestKey`, and takes the largest key
  from the last entry of the index. It does not add the first data block to
  the cache.
- `Get` checks the filter first, so a key that the table does not contain
  costs no block read with a probability of 1 - 2^-FilterBits. It then
  searches the index for the first block whose last entry is at or after
  (key, seq), reads the block and searches its restart points.
- A reader reads a block that the cache does not contain with one
  `ReadAt` of the block and its trailer, checks the CRC, decompresses it
  into a pooled buffer, and adds a copy of the decompressed block to the
  cache.
- `Iter.SeekGE` searches the index, then the restart points of the block,
  then the entries after the restart point. `Iter.Next` continues in the
  block, and moves to the next block at its end.

### The merge

`Merger` keeps its sources in a binary heap, ordered by the entry of each
under `Compare` and then by the index of the source. `First` and `SeekGE`
move every source and rebuild the heap. `Next` advances the source at the top and restores
the heap in O(log k) for k sources. A source whose `Err` is not nil
exhausts the merge, and `Merger.Err` returns that error.

### Invariants

- `Writer.Add` accepts entries in the order of `Compare` only, so a table
  contains its entries in that order and each (key, seq) at most once.
- An `Iter` of a table yields the entries that the writer added, in order.
- `Reader.Get` and `List.Get` return the entry at which `SeekGE` of a
  cursor stops, when the key of that entry equals the key.
- A `List` yields its entries in the order of `Compare` at any time, and an
  `Add` that returned is visible to every read that starts after it.
- Every byte of a table is under a CRC, so `Open` and the reads return
  `ErrCorrupt` for every change of one bit.
- A `Merger` yields the union of its sources in the order of `Compare`.

### Bounds

| Quantity | Bound |
|---|---|
| A node of a `List` | (5 + h) * 4 bytes, 26.3 bytes on average at the heights of e^-k |
| A `List` | The two arenas, each at most 2^32 bytes |
| A data block | `BlockSize` bytes plus the last entry, its restart offsets and the 5-byte trailer |
| The entries of an index block | At most 2^32 - 1 bytes |
| Memory of a `Reader` | The index block, the filter block and the summary |
| Memory of a `Writer` | The current block, its restart offsets, the index entries, 8 bytes per distinct key for the filter, and at `Close` the scratch of the filter's builder, about 32 bytes per distinct key, and a buffer of the filter block and the index block. `Reset` keeps all of them for the next table |
| Reads of a `Get` | The filter in memory, then at most one data block |
| A step of a `Merger` | O(log k) comparisons for k sources |

### Failure handling

| Condition | Function | Result |
|---|---|---|
| An entry out of order or repeated | `Writer.Add` | `ErrOrder`, the Writer unchanged |
| A key that could bring the index past 2^32 - 1 bytes | `Writer.Add` | `ErrTooLarge`, the Writer unchanged |
| A write error of the `io.Writer`, or a short write without an error | `Writer.Add`, `Writer.Close` | An error that wraps it or `io.ErrShortWrite`, and the Writer refuses every later call until `Reset` |
| A checksum, a decompression or a layout that fails | `Open`, `Reader.Get`, `Iter` moves | `ErrCorrupt`. An `Iter` is exhausted, and `Err` returns the error |
| A read error of the `io.ReaderAt`, or a short read without an error | `Open`, `Reader.Get`, `Iter` moves | An error that wraps it or `io.ErrUnexpectedEOF` |
| An entry that does not fit the arenas | `List.Add` | `ErrFull`, and no entry added |
| A key and a sequence that the List contains | `List.Add` | `ErrExists` |
| A source whose `Err` is not nil | `Merger` moves | The merge is exhausted, and `Err` returns the error |
| A source whose `Close` returns an error | `Merger.Close` | `Close` closes the other sources too, and returns the errors joined |

### Allocation contract

| Call | Objects |
|---|---|
| `NewWriter` | The Writer, and with a filter the encoding of an empty filter that checks the width |
| `Writer.Add` | 0 when the current block, the index entries and the filter's hashes have room. The first `Add` copies the smallest key |
| `Writer.Close` | The filter encoding and its scratch, the growth of the buffer for the filter and the index, and the largest key of the summary |
| `Writer.Reset` | 0. The next table reuses every buffer, so once the buffers have grown it allocates the copies of its smallest key and its largest key alone |
| `Open` | The Reader, the footer, the index block, the filter block and the keys of the summary |
| `Reader.Get`, `Reader.MayContain` | 0 without a cache once the pooled buffers have grown to the largest block, and when dst has room. With a cache, one copy of each block that the cache does not contain |
| `Reader.NewIter` and `Iter.Close` | 0 once the pool contains a cursor |
| `Iter` and `Merger` moves | 0, with the cache rule of `Get` for each new block |
| `Merger.Close` | 0 when every source closes without an error and without an allocation |
| `skiplist.New` | The List, its two `arena.Bump` values and their arrays |
| `List.Add`, `List.Get`, `List.Len`, `List.Size`, `List.Reset` | 0 |
| `List.NewIter` | The cursor |

### Tests

| # | Guarantee |
|---|---|
| 1 | A table written from generated sorted entries yields the same entries, and `SeekGE` and `Get` return what a sorted model returns, for each compression and filter setting |
| 2 | Golden files pin the bytes of tables of fixed entries for each compression and filter setting |
| 3 | `Open` and every read of a golden table return `ErrCorrupt` for every single-bit change and every truncation. A fuzz target checks that `Open` and the cursors never panic |
| 4 | `Open` and the cursors return `ErrCorrupt` for tables whose footer, index, filter or data blocks a test changed and sealed again with valid checksums, one check of the layout per case |
| 5 | `Writer.Add` returns `ErrOrder` for an entry at or before the previous one, and a failed write of the `io.Writer` stops the Writer |
| 6 | `MayContain` reports true for every key of a table |
| 7 | A `Merger` over generated runs yields their sorted union, and `SeekGE` matches the model |
| 8 | A stateful machine of `Add`, `Get` and cursor moves on a `List` returns what a sorted model returns, and concurrent histories of `Add` and `Get` are linearizable under the race detector |
| 9 | `skiplist.Iter` satisfies `sstable.Source`, and a `List` and a table of the same entries yield them in one order |
| 10 | `List.Add` returns `ErrFull` at the end of either arena and `ErrExists` for a repeated entry |
| 11 | `TestWriterAllocs`, `TestReaderAllocs`, `TestIterAllocs`, `TestMergerAllocs`, `TestListAllocs` and the benchmarks state the counts of the allocation contract |
| 12 | The benchmarks measure `List.Add` with one and four writers, `List.Get`, `runtime.GC` with 1M entries live, `Writer.Add`, `Reader.Get` with and without a cache, and iteration per entry |
| 13 | The suites cover every statement, and dokimi-mutate-go detects every mutant of both packages that an annotation does not suppress |
| 14 | `Writer.Reset` after any first table writes the bytes and the summary of a new Writer. `TestWriterAllocs` and the benchmark state 2 allocations for each table after `Reset` |
| 15 | Concurrent reads of one `Reader` through one `BlockCache` return what the entries of the table return, by a check of linearizability under the race detector |
| 16 | `Merger.Close` closes every source with a `Close` method once, skips the others and joins the errors. A second `Iter.Close` returns nil and returns the cursor to the pool once |
| 17 | `FuzzIter` and `FuzzMerger` run the machines of the cursor and the merge on the inputs of `go test -fuzz` |

## Alternatives considered

### A. An LSM engine in core

Core would ship an engine with a write-ahead log, a manifest, compaction
and a block cache.

**Why not:** the engine's API is a keyed-value store, which core refuses as
a seam for 1.x, and its compaction, stall and flush policies belong to the
system that runs it. A decision that supersedes the refusal reverses this.

### B. `btree.MapFunc` over arena offsets as the memtable

**Why not:** it inserts in 543-578 ns and looks up in 777-815 ns against
693-789 ns and 1,175-1,225 ns, but no goroutine may read it while another
writes. An engine whose memtable has one writer and no concurrent reader
reverses this.

### C. A skip list of heap nodes

**Why not:** with 1M entries live, a collection cost 210-217 ms of GC CPU,
and every insert allocated three objects.

### D. An `unsafe` arena skip list, as in Badger and Pebble

**Why not:** core does not import `unsafe`, and the skip list without it
inserts as fast with one writer and faster with four.

### E. Keys without a sequence, ordered by `bytes.Compare`

An engine would append its sequence to each key and store opaque keys.

**Why not:** the bytes of a key followed by a sequence do not order
correctly when one key is a prefix of another. "a" with a sequence and
"ab" with a sequence compare at the first byte of the sequence and the
byte "b". An engine would escape every key to fix this.

### F. A comparison function that the caller supplies

**Why not:** one order serves every LSM engine, and a table written under
one comparison and read under another returns wrong results. A comparison
function would need its name recorded in every table and checked on every
open.

### G. LevelDB's exact format

**Why not:** LevelDB packs the kind of an entry into its trailer, masks its
CRCs and builds a filter for every 2 KB of data. Core's format keeps the
sequence opaque, stores CRC32C unmasked and builds one filter per table. A
LevelDB reader would also need LevelDB's key format and Bloom filter to read
a table, so the exact block layout alone does not make the two formats
interoperate.

### H. A partitioned index

**Why not:** one index block of a table of 64 MB in blocks of 4 KiB has
about 16,000 entries, which `Open` reads once. A consumer with tables large
enough that the index dominates a read reverses this.

### I. A block cache inside the reader

**Why not:** the capacity and the eviction of a cache are the policy of the
engine. `BlockCache` takes core's `cache` or any other cache.

### J. A package of its own for the merge

`Compare`, `Source` and `Merger` would be a package that `sstable` and
`skiplist` both import.

**Why not:** the order and the cursor contract are part of the table
format, and every engine that merges runs imports `sstable` to read its
tables. `skiplist` implements `Source` without importing `sstable`, so the
memtable does not depend on the table format.

### K. One capacity for both arenas of a `List`

`New` would take one capacity and split it between the node arena and the
data arena by a fixed ratio.

**Why not:** the share of the nodes depends on the size of the entries. A
node takes 26.3 bytes on average, which is 23% of the 116 bytes of a
16-byte key and a 100-byte value, and 164% of the 16 bytes of an 8-byte key
and an 8-byte value. Under a fixed ratio, one arena fills first for every
workload off that ratio. The engine sizes both arenas from its entries.

### L. A block of table properties

A table would contain a block of named properties, as RocksDB's properties
meta block does. RocksDB writes the sizes of the data blocks, the index and
the filter, the raw sizes of the keys and the values, and the numbers of
entries and data blocks. An engine adds properties of its own through a
callback. LevelDB's table format names a stats meta block of such values as
a TODO, and LevelDB does not write it.

**Why not:** the footer states the number of entries and the lowest and the
highest sequence, and `Open` reads the smallest and the largest key. Core
does not read another property of a table. A block of named properties
needs a vocabulary of names and a rule for a name that a reader does not
know. Version 1 of the format has no such block. An engine that
needs another property, such as a count of the deletions that only the
engine recognizes, reverses this with a new version of the format.

### M. A range loop over a Source

A function `All(s Source)` would return an iterator that a `for range` loop
reads, as `btree.Map.All` and `radix.Map.Scan` do.

**Why not:** a read of a table fails with I/O and checksum errors, and a
range loop has no result for them. The caller would check `Err` after the
loop, as the cursor already requires. Each entry would also need a type of
its own for its key, its sequence and its value. A caller in core that
reads a merge in a range loop reverses this.

## Drawbacks

- The module gains two packages with two cursor types, a writer, a reader,
  a merger, a cache interface, a list and eight sentinels.
- The table format is frozen at its first release, and no other system
  reads it.
- A `List` needs two capacities, and it fails an `Add` when either arena is
  full.
- `Merger.Close` finds the `Close` of each source by a type assertion, so
  the compiler does not check that a source of an engine with resources to
  release has one.
- Cursors of both packages move forward only. Range deletions are out of
  both formats.
- A `Writer` keeps 8 bytes per distinct key until `Close`, and the filter's
  builder about 32 more: about 25 MB for a table of 640,000 keys.
- A block cache in the Go heap needs `GOMEMLIMIT` to keep the resident
  memory of the process near the cache's size.

## Open questions

None.

## Unresolved / future work

None.

## References

- google/leveldb, `table/block_builder.cc`, `table/format.h`,
  `doc/table_format.md` and `include/leveldb/options.h`,
  <https://github.com/google/leveldb>.
- facebook/rocksdb PR #14287, "Separate keys and values in data blocks",
  <https://github.com/facebook/rocksdb/pull/14287>, and
  `util/dynamic_bloom.h`.
- facebook/rocksdb wiki, "Rocksdb BlockBasedTable Format", the properties
  meta block,
  <https://github.com/facebook/rocksdb/wiki/Rocksdb-BlockBasedTable-Format>.
- cockroachdb/pebble, `docs/memory.md`,
  <https://github.com/cockroachdb/pebble/blob/master/docs/memory.md>.
- CockroachDB v26.2.8, `pkg/storage/pebble.go`, the block sizes.
- dgraph-io/badger v4.9.6, `skl/arena.go`.
- The Go Authors, "A Guide to the Go Garbage Collector", the section on the
  memory limit, <https://go.dev/doc/gc-guide>.
- The Go Authors, "The Go Memory Model", <https://go.dev/ref/mem>.
- RFC-0060, approximate-membership filters and XXH3 hashing.
- RFC-0061, block compression in the Snappy format.
- ADR-0057, `Bump` reserves values without locks.
- ADR-0012, storage is per-kind and a unified KV seam is refused.
- ADR-0018, core ships mechanisms, not lifecycles.
- ADR-0016, persisted encodings are frozen before 1.0.
