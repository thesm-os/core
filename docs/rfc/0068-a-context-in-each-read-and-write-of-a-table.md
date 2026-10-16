---
rfc: 0068
title: A Context in Each Read and Write of a Table
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-10-16
updated: 2026-10-16
discussion: https://github.com/thesm-os/core/issues/62
supersedes: none
superseded-by: none
produces-adr: ADR-0065, ADR-0066
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# RFC-0068: A Context in Each Read and Write of a Table

## Summary

We propose that every call of `sstable` that can read or write a table takes
a `context.Context`, and that a cursor can read without filling the block
cache. `Open`, `Reader.Get`, the moves of every `Source`, the methods of a
`BlockCache` and the calls of a `Writer` take the context of their caller. A
`Reader` reads a `File` and a `Writer` writes a `Sink`, which are
`io.ReaderAt` and `io.Writer` with a context. `IterOptions.NoFill` gives a
cursor the blocks that the cache contains without adding the blocks that it
reads. The table format does not change.

## Motivation

A read or a write of a table can wait for a permit of a table of open files,
for a budget of disk I/O or for a remote store. Such a wait ends when the
context of its caller ends. No call of `sstable` takes a context. A `Reader`
reads every block through the `io.ReaderAt` of `Open`, and a `Writer` writes
through an `io.Writer`. A caller can pass the context of a read only in the
struct of its file. The context package advises against that: "Do not store
Contexts inside a struct type; instead, pass a Context explicitly to each
function that needs it." The storage interfaces of core take a context in
each call, as `blob.Store` and `tlog.TileReader` do.

A compaction that reads a table through the `Reader` of the reads adds every
block that it reads to the cache, and evicts the blocks of the reads. A
second `Reader` without a cache reads the footer, the index and the filter
again. Issue 62 measured a table of 16,384 entries of 1 KiB in 1,024 blocks
of 16 KiB at v0.23.0:

| Call | Result |
|---|---|
| One scan through a Reader with a cache | 1,024 blocks of 17,014,301 bytes added to the cache |
| One scan through a cache that keeps nothing | 1,025 allocations, of which 1,024 copy a block for `BlockCache.Add` |
| `Open` of a second Reader without a cache | 79,067 bytes in 4 reads |

### Prior art

- Pebble passes a context to every read of a table, through
  `objstorage.Readable.ReadAt(ctx context.Context, p []byte, off int64)
  error`. It binds the context of a cursor when it creates the cursor with
  `Reader.NewPointIter(ctx, IterOptions)`, and the moves of its
  `InternalIterator` take no context.
- LevelDB and RocksDB give each read `ReadOptions.fill_cache`: "Callers may
  wish to set this field to false for bulk scans."
- golang/go#20280 proposes a context for the interfaces of package io. It
  is open.

## Detailed design

### Files and sinks

```go
// File is the storage of a table that a Reader reads. ReadAt has the
// contract of io.ReaderAt. A ReadAt that waits, such as for a permit or a
// budget, ends its wait with the error of ctx.
type File interface {
	ReadAt(ctx context.Context, p []byte, off int64) (int, error)
}

// Sink receives the bytes of the table that a Writer writes. Write has the
// contract of io.Writer. A Write that waits ends its wait with the error of
// ctx.
type Sink interface {
	Write(ctx context.Context, p []byte) (int, error)
}
```

An `*os.File` or a `bytes.Reader` becomes a `File` through a type with one
method that drops the context.

### Reads

```go
func Open(ctx context.Context, f File, size int64, opts ReaderOptions) (*Reader, error)
func (r *Reader) Get(ctx context.Context, key []byte, seq uint64, dst []byte) ([]byte, uint64, bool, error)
func (r *Reader) NewIter(opts IterOptions) *Iter

// IterOptions configure a cursor. The zero value adds the blocks that the
// cursor reads to the cache of the Reader.
type IterOptions struct {
	// NoFill makes the cursor take the blocks that the cache contains, and
	// read every other block without adding it, as a scan of a compaction
	// needs.
	NoFill bool
}

type BlockCache interface {
	Get(ctx context.Context, id, offset uint64) ([]byte, bool)
	Add(ctx context.Context, id, offset uint64, block []byte)
}
```

- `Open` reads the footer, the index, the filter and the first data block
  under its context.
- A cursor reads a block under the context of the move that needs the
  block, and looks the block up in the cache under the same context.
- A cursor with `NoFill` reads a block that the cache does not contain into
  a buffer of the cursor, and does not copy the block.
- `Get` adds the block that it reads to the cache, as a cursor without
  `NoFill` does.
- An error of a `File` exhausts the cursor, and `Err` returns an error that
  wraps it. `errors.Is(err, context.Canceled)` then reports true for a read
  that ended with its context.
- A move that does not read a block ignores its context. A scan of blocks
  that the cache contains runs to the end of the table after its context
  ended.

### Cursors

```go
type Source interface {
	First(ctx context.Context) bool
	Last(ctx context.Context) bool
	SeekGE(ctx context.Context, key []byte, seq uint64) bool
	SeekLT(ctx context.Context, key []byte, seq uint64) bool
	Next(ctx context.Context) bool
	Prev(ctx context.Context) bool
	Key() []byte
	Seq() uint64
	Value() []byte
	Err() error
}
```

- Each move takes the context of the read that it can make. `Source` keeps
  its 10 methods.
- A `Merger` passes the context of each move to the moves of its sources.
- `skiplist.Iter` takes the context of each move and ignores it, because a
  memtable does not wait.

### Writes

```go
func NewWriter(w Sink, opts WriterOptions) (*Writer, error)
func (w *Writer) Add(ctx context.Context, key []byte, seq uint64, value []byte) error
func (w *Writer) Close(ctx context.Context) (Summary, error)
func (w *Writer) Reset(dst Sink)
```

`Add` writes a data block under its context when the entry ends the block,
and `Close` writes the rest of the table under its context. An error of the
`Sink` keeps its effect: every later call returns it.

### Allocation contract

A context is an interface value of two words, so each call allocates as it
does today. A cursor with `NoFill` does not allocate a copy of a block that
the cache does not contain.

### Migration

`sstable` and `skiplist` are in v0.22.0 and v0.23.0, so the change breaks
their API. Before v1.0.0 a minor release of core can do that. A caller passes
its context to each call, wraps its file in a `File`, and wraps the
destination of a `Writer` in a `Sink`. The table format and the node layout
do not change.

### Tests

- A `File` and a `Sink` that return the error of a context that ended make
  `Open`, `Get`, each move, `Add` and `Close` return an error that wraps
  `context.Canceled`.
- A cache that records the context of each call receives the context of the
  move or of the `Get`. The machine of a cursor checks it after every move.
- The machine of a `Merger` checks that each move passes its context to the
  sources that it moves.
- A cursor with `NoFill` takes the blocks that the cache contains. It does
  not add a block, and it does not allocate a copy.
- The machines, the fuzz targets, the `Allocs` tests and the benchmarks pass
  a context to every move.

## Alternatives considered

### A. A file and a cache for each cursor, with io.ReaderAt

`NewIterFrom(file io.ReaderAt, cache BlockCache)` would take a file and a
cache for each cursor.

**Why not:** each caller keeps the context of its read in the struct of its
file, and the writes take no context.

### B. A context for each cursor, as in Pebble

`NewIter(ctx, opts)` would bind the context, and the moves would take none.
`Source` and `skiplist.Iter` would not change.

**Why not:** the cursor keeps the context in its struct. An engine whose
cursor takes a context in each move then has two choices. It creates its
table cursors again for each move, or it keeps the context in a struct as
well. Each move can read a block, and that read needs the context of the
move.

### C. A fill flag in ReaderOptions

**Why not:** the flag applies to every cursor of a Reader. The reads and the
compactions of one table then cannot share the Reader.

### D. A second Reader for each kind of read

**Why not:** each `Open` reads the footer, the index and the filter again.
For the table of issue 62 that is 79,067 bytes, which `Open` allocates
again.

## Drawbacks

- Every caller of `Open`, `Get`, `NewIter`, the moves, the methods of a
  `BlockCache`, `NewWriter`, `Add`, `Close` and `Reset` changes.
- `skiplist.Iter` takes a context that it does not use.
- A file of the operating system needs a type of one method to become a
  `File`.
- A move that does not read a block does not stop at a context that ended.

## Open questions

None.

## Unresolved / future work

None.

## References

- Issue 62, <https://github.com/thesm-os/core/issues/62>.
- The context package of Go 1.27.2, `src/context/context.go`.
- cockroachdb/pebble at a7b8321, `objstorage/objstorage.go`
  (`Readable.ReadAt`), `sstable/reader.go` (`Reader.NewPointIter`) and
  `internal/base/iterator.go` (`InternalIterator`).
- google/leveldb at bb74ef7, `include/leveldb/options.h`
  (`ReadOptions.fill_cache`).
- golang/go#20280, <https://github.com/golang/go/issues/20280>.
- RFC-0062, sorted tables and memtables for LSM trees.
- RFC-0066, cursors that move backward.
