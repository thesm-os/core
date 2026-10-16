---
adr: 0072
title: A Cursor Reads Ahead on Request
status: Accepted
date: 2026-10-17
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0072: A Cursor Reads Ahead on Request

## Status

Accepted

## Context

A cursor of a table read each data block that the cache did not contain
with one read of its `File`. A scan of a file of 33 MB in blocks of 4 KiB
took 8.8 ms cold and 4.0 ms warm with a read for each block, and 6.4 ms and
3.5 ms with reads of 256 KiB. With 20 µs added to each read, as a remote
store or a permit adds it, the scan took 159 ms and 6.0 ms.

## Decision

We will let a cursor read ReadAhead bytes of data blocks with one read when
`IterOptions.ReadAhead` asks for it, because a scan then makes one read for
many blocks.

- A read of a block reads ReadAhead bytes from the start of the block, or up
  to its end for a block before the bytes of the last read.
- A later move takes a block inside those bytes without a read, after the
  checks of its CRC32C and its codec.
- `Close` drops the bytes, so a pooled cursor does not keep them.
- ReadAhead of 0 reads each block alone.

## Alternatives Considered

### An automatic read-ahead, as in RocksDB

Rejected. Each cursor would grow its buffer without the knowledge of the
engine, which knows which cursors scan.

### A read-ahead in the File of the engine

Rejected. A `File` serves the cursors of a Reader at once and cannot tell the
reads of one scan from those of another.

## Consequences

**Positive:**

- A scan with ReadAhead of 256 KiB makes about one read for each 64 blocks of
  4 KiB. The scan of the file of 33 MB took 6.3 ms cold, 3.2 ms warm and 5.7
  ms with 20 µs added to each read, against 8.6 ms, 4.0 ms and 159 ms with a
  read for each block.

**Negative:**

- A cursor with ReadAhead keeps ReadAhead bytes until `Close`.
- A seek outside the bytes of the last read reads ReadAhead bytes for one
  block.

**Neutral:**

- The table format and `Reader.Get` do not change.

## References

- RFC-0072, read-ahead of a table cursor.
- facebook/rocksdb at a3b0f187ff, `include/rocksdb/options.h`.
