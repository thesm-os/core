---
rfc: 0072
title: Read-Ahead of a Table Cursor
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-10-17
updated: 2026-10-17
discussion: none
supersedes: none
superseded-by: none
produces-adr: ADR-0072
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# RFC-0072: Read-Ahead of a Table Cursor

## Summary

We propose that a cursor of a table can read many data blocks with one read
of its `File`. `IterOptions.ReadAhead` sets the bytes of such a read. A
cursor that reads a block then reads the bytes of the blocks after it too,
or of the blocks before it when it moves back, and later moves take those
blocks without a read. The zero value reads each block alone, as a cursor
does today.

## Motivation

A cursor reads each data block that the cache does not contain with one
`ReadAt` of the `File`. A scan of a table of blocks of 4 KiB makes one read
for each 4 KiB. A read of the file of the operating system costs a
system call. A `File` that waits for a permit of a table of open files, or
that reads from a remote store, adds that wait to every read.

Measured on a table of 262,144 entries of 16-byte keys and 100-byte random
values in Snappy blocks of 4 KiB, a file of 33 MB on XFS. A scan reads every
block under NoFill. The variant with reads of 256 KiB wraps the `File` in a
type that reads 256 KiB at the offset of each block outside its last read.

| Scan | A read of each block | Reads of 256 KiB |
|---|---|---|
| Cold, after `POSIX_FADV_DONTNEED` of the file | 8.8 ms | 6.4 ms |
| Warm, from the page cache | 4.0 ms | 3.5 ms |
| With 20 µs added to each read | 159 ms | 6.0 ms |

Linux reads ahead for a sequential read of a file already, and the reads of
256 KiB still save 27% of the cold scan and 13% of the warm scan.
For a `File` with a cost per read, the scan makes about 124 reads instead of
about 7,900, and takes 26 times less time.

A wrapper of the `File` cannot do this for the engine. A `File` is safe for
concurrent use, because the cursors of a Reader read at once, and it cannot
tell which reads belong to which scan.

### Prior art

- RocksDB's iterators read ahead after more than two reads of a table file:
  "The readahead starts at 8KB and doubles on every additional read up to
  256KB." `ReadOptions.readahead_size` sets a size for the reads of an
  iterator instead (`include/rocksdb/options.h` of facebook/rocksdb at
  a3b0f187ff).

## Detailed design

```go
type IterOptions struct {
	// NoFill as today.
	NoFill bool

	// ReadAhead is the number of bytes of a read of the cursor from the
	// File. A read of a data block that the cache does not contain reads
	// ReadAhead bytes from the start of the block, or up to the end of the
	// block when the block is before the bytes of the last read. A later
	// move takes a block inside those bytes without a read. A ReadAhead of
	// the size of a block or less reads the block alone, and so does 0.
	ReadAhead int
}
```

- A read never starts before the first data block or ends after the last
  one.
- A block inside the bytes of the last read passes the same checks of its
  CRC32C and its codec as a block of its own read. A cursor without NoFill
  adds it to the cache, as it adds a block that it read alone.
- The cursor keeps the bytes of its last read until `Close`. `Close` drops
  them, so a cursor in the pool of cursors does not keep them for a cursor
  of a lookup.
- A seek to a block outside the bytes of the last read reads ReadAhead bytes
  again. ReadAhead suits a scan, such as the scan of a compaction, and not a
  cursor of random seeks.
- `Reader.Get` reads its block alone, as today.

### Allocation contract

A cursor with ReadAhead allocates the buffer of its reads at its first read
of more than one block. Its later reads reuse the buffer, and `Close`
releases it.

### Tests

- The machine of a cursor draws a ReadAhead of 0, of 1 KiB or of 64 KiB, and
  checks every move against the sorted model.
- A scan forward and a scan back with ReadAhead read the File fewer times
  than without it, and yield every entry.
- A cursor with a cache and ReadAhead adds each block that it reads to the
  cache.
- An error of the File during a read of ReadAhead bytes exhausts the cursor,
  and Err returns an error that wraps it.
- The allocation tests and the benchmarks state the ceiling above.

## Alternatives considered

### A. An automatic read-ahead, as in RocksDB

A cursor would read ahead after two reads of consecutive blocks, with reads
that double from 8 KiB to 256 KiB.

**Why not:** each cursor would grow its buffer without the knowledge of the
engine, which budgets the memory of its scans. The engine knows which
cursors scan, as it knows the scans that set NoFill.

### B. A read-ahead in the File of the engine

**Why not:** a `File` serves the cursors of a Reader at once, and it cannot
tell the reads of one scan from the reads of another.

### C. Reads of the next blocks by the places of the index

A read would cover whole blocks: the blocks whose places the index states
after the current block, up to ReadAhead bytes.

**Why not:** the bytes from the start of a block cover the same blocks
without a walk of the index, and a block that the bytes cut off is read on
its own move.

## Drawbacks

- A cursor with ReadAhead keeps ReadAhead bytes until `Close`.
- A seek outside the bytes of the last read reads ReadAhead bytes, of which
  the seek uses one block.
- `IterOptions` gains a field.

## Open questions

None.

## Unresolved / future work

None.

## References

- facebook/rocksdb at a3b0f187ff, `include/rocksdb/options.h`
  (`ReadOptions.readahead_size`).
- RFC-0068, a context in each read and write of a table.
