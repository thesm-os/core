---
rfc: 0071
title: A Bounded Workspace for a Table Writer
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-10-17
updated: 2026-10-17
discussion: none
supersedes: none
superseded-by: none
produces-adr: ADR-0071
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# RFC-0071: A Bounded Workspace for a Table Writer

## Summary

We propose a bound on the memory that an `sstable.Writer` keeps between
tables, and a count of that memory. `WriterOptions.MaxRetained` sets the
bound in bytes. `Writer.Footprint` returns the bytes that the buffers of the
Writer take, and `filter.FuseBuilder.Footprint` returns the bytes of the
scratch of a filter builder. A Writer whose footprint exceeds the bound
releases its buffers after `Close` writes a table, and at `Reset`. The zero
bound keeps every buffer, as a Writer does today.

## Motivation

`Writer.Reset` keeps the buffers of a Writer and the scratch of its filter
builder for the next table. A Writer that writes many tables of one size
then adds entries without an allocation. The buffers follow the largest
table that the Writer wrote, and they remain at that size while the Writer
writes small tables or waits for the next one.

Measured before this change, with Snappy blocks and a filter of 10 bits:

| Phase | Keys alone | Values of 100 bytes |
|---|---|---|
| Large table | 1,000,000 entries, 14.3 MB | 56,000 entries, 1.1 MB |
| Heap at the peak of the large table | 46.5 MB | 3.8 MB |
| Heap after its Close and a Reset | 44.6 MB | 2.8 MB |
| Heap after a small table of 10,000 entries | 44.6 MB | 2.8 MB |
| Heap of the idle Writer | 44.6 MB | 2.8 MB |

The Writer keeps about 45 bytes for each entry of its largest table. The
scratch of the filter builder takes about 34 bytes of them, as the
allocation contract of `filter.FuseBuilder` states, and the hashes of the
filter 8 more. With small entries, a Writer keeps three times the size of
its largest table. An engine that keeps one Writer for each compaction or
flush keeps that memory for every Writer, and it cannot read the size of a
Writer to budget its memory.

### Prior art

- Go's `fmt` keeps its printers in a `sync.Pool` and drops a buffer above
  64 KiB before it pools a printer: "If the buffer is larger than the limit,
  we drop the buffer and recycle just the printer" (`src/fmt/print.go:155-167`
  of Go 1.27.2, golang/go#23199).
- A `bytes.Buffer` and a `strings.Builder` never shrink. A caller drops them
  to release their memory.

## Detailed design

### The bound

```go
type WriterOptions struct {
	// BlockSize, RestartInterval, Compression and FilterBits as today.

	// MaxRetained bounds the memory that a Writer keeps for its next table,
	// in bytes. When Footprint exceeds MaxRetained after Close writes a
	// table, or at Reset, the Writer releases its buffers and the scratch of
	// its filter builder, and the next table grows them again. 0 keeps every
	// buffer.
	MaxRetained int
}
```

- `NewWriter` returns `ErrOptions` for a negative `MaxRetained`.
- `Close` releases the buffers after it wrote the table and built its
  summary. A `Close` that fails keeps them, and `Reset` then applies the
  bound.
- `Reset` releases the buffers when the footprint exceeds the bound, and
  keeps them otherwise, as today.
- A release sets every buffer of the Writer to nil and gives the Writer a
  new filter builder. The garbage collector then frees the arrays.

### The count

```go
// Footprint returns the bytes of memory that the buffers of w take: the
// capacity of the buffers of the current block, of the index, of the
// hashes of the filter and of the compressed block, and the scratch of the
// filter builder. It does not count the keys of a Summary, because Close
// returns copies of them.
//
// # Allocation contract
//
// Zero-alloc.
func (w *Writer) Footprint() int

// Footprint returns the bytes of the scratch memory that b keeps between
// builds: the capacity of each of its arrays.
//
// # Allocation contract
//
// Zero-alloc.
func (b *FuseBuilder) Footprint() int
```

- `Footprint` counts capacities, not lengths, because a buffer keeps its
  capacity between tables.
- `Footprint` after a release returns 0.

### Allocation contract

A Writer whose footprint remains within the bound keeps its contract: a table
after `Reset` allocates the two keys of its summary once the buffers have
grown. A table after a release allocates the growth of its buffers again.

### Tests

- `Footprint` of a new Writer is 0 and grows with the entries of a table.
  `FuseBuilder.Footprint` grows with the hashes of a build.
- A Writer under a bound keeps its footprint across `Close` and `Reset`. A
  Writer above the bound returns 0 from `Footprint` after `Close` and after
  `Reset`.
- A Writer with a bound of 0 keeps every buffer, as today.
- `NewWriter` returns `ErrOptions` for a negative bound.
- A table after a release has the bytes of a table of a new Writer.
- The allocation tests and the benchmarks state the ceilings above. A probe
  measures the sequence of a large table, a small table and an idle Writer,
  with and without a bound: the peak heap, the heap that each phase leaves
  and the time per entry.

## Alternatives considered

### A. A method that releases the buffers

`Writer.Trim()` would release the buffers when the caller calls it.

**Why not:** every engine would have to place the call. A Writer that an
engine keeps is bounded at `Close`, and a Writer that it drops is freed by
the garbage collector.

### B. Buffers shrunk to the last table

**Why not:** the last table can be the large one, so the memory that a
Writer keeps still follows its largest recent table.

### C. A pool of buffers that the Writers share

**Why not:** a `sync.Pool` empties at the second collection after a put, so
the allocations of a run of compactions would depend on the timing of the
garbage collector. The Writers of one engine also write tables of different
sizes at once.

### D. A default bound

**Why not:** a default would change the allocations of every caller that
writes many tables of one size. The engine knows its memory budget, and 0
keeps the behavior of today.

### E. A release at every Close

**Why not:** each table would grow its buffers from zero, so a Writer that
writes many tables of one size would allocate them for every table.

### F. A bound for each buffer

**Why not:** a memory budget is a total in bytes, and the scratch of the
filter builder is the largest part. A bound for each buffer would take a
setting for each of nine arrays.

## Drawbacks

- A table after a release allocates its buffers again as they grow.
- `WriterOptions` gains a field, and `Writer` and `filter.FuseBuilder` gain a
  method each.
- `Footprint` counts the arrays that a Writer refers to. The garbage
  collector frees the arrays of a release only at its next cycle.

## Open questions

None.

## Unresolved / future work

None.

## References

- The fmt package of Go 1.27.2, `src/fmt/print.go` (`pp.free`).
- golang/go#23199, <https://github.com/golang/go/issues/23199>.
- RFC-0062, sorted tables and memtables for LSM trees.
