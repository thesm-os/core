---
adr: 0066
title: A Cursor Reads Without Filling the Cache
status: Accepted
date: 2026-10-16
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0066: A Cursor Reads Without Filling the Cache

## Status

Accepted

## Context

A cursor of a `Reader` added every block that it read to the cache of the
Reader. A compaction that read a table through the Reader of the reads
evicted the blocks of the reads, and copied each block for the cache. A
second `Reader` without a cache read the footer, the index and the filter
again. Issue 62 measured a table of 16,384 entries of 1 KiB at v0.23.0. One
scan added 1,024 blocks of 17,014,301 bytes to the cache, and the `Open` of
a second Reader read 79,067 bytes.

## Decision

We will give `Reader.NewIter` an `IterOptions` whose `NoFill` makes the
cursor read without adding blocks to the cache, because a compaction then
shares the Reader of the reads without evicting their blocks.

- A cursor with `NoFill` takes the blocks that the cache contains.
- It reads every other block into a buffer of the cursor and does not copy
  the block.
- A cursor of the zero `IterOptions` adds each block that it reads to the
  cache.
- `Reader.Get` adds the block that it reads to the cache.

## Alternatives Considered

### A fill flag in ReaderOptions

Rejected. The flag would apply to every cursor of a Reader, so the reads and
the compactions of one table could not share the Reader.

### A second Reader for each kind of read

Rejected. Each `Open` reads the footer, the index and the filter again,
79,067 bytes for the table of issue 62, and allocates them again.

## Consequences

**Positive:**

- A compaction shares the Reader of the reads without evicting their blocks.
- A scan with `NoFill` does not allocate a copy of a block.

**Negative:**

- Every caller of `NewIter` passes an `IterOptions`.
- A cursor with `NoFill` reads a block again each time that it returns to
  the block.

**Neutral:**

- The table format does not change.

## References

- RFC-0068, a context in each read and write of a table.
- Issue 62, <https://github.com/thesm-os/core/issues/62>.
- google/leveldb at bb74ef7, `include/leveldb/options.h`
  (`ReadOptions.fill_cache`).
