---
adr: 0070
title: A Cursor Checks Its Context Before Each Block
status: Accepted
date: 2026-10-17
supersedes: ADR-0065
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0070: A Cursor Checks Its Context Before Each Block

## Status

Accepted

Supersedes the consequence of
[ADR-0065](0065-reads-and-writes-of-a-table-take-a-context.md) that a move
that does not read a block ignores its context. The decision to pass the
context of the caller to every call that can read or write a table remains
in force.

## Context

A move of `sstable.Iter` passes its context to the `File` and to the
`BlockCache`. The implementations that wait are in the engine, such as a
`File` that waits for a permit of a table of open files. A move whose block
the cache contained did not read, so it ignored its context. A scan of
blocks that the cache contained ran to the end of the table after its
context ended. A scan of a table that the cache did not contain stopped
at the next read. `BlockCache.Get` does not return an error, so a cache
cannot end a scan either.

## Decision

We will check the context of a move before each block that the move needs,
because a scan then stops at the next block after its context ended whether
the cache contains the block.

- A move that needs a block returns false when its context ended, before it
  looks the block up in the cache. `Err` then returns the error of the
  context.
- `Reader.Get` returns the error of its context when it needs a block.
- A move within the current block ignores its context, as before.
- A `Writer` passes its context to the `Sink` at each block, as before.

## Alternatives Considered

### A check in each move

Rejected. A move within a block does not wait. A check there would add a
call of `Err` to each `Next` and `Prev`, and the next block stops the scan
anyway.

### A check in the cache

Rejected. `BlockCache.Get` reports a block or a miss without an error, so a
cache cannot end a move.

### The loop of the caller alone

Rejected. A loop that relies on the moves to stop at its context stops at
the next block of a table that the cache does not contain, and runs to the
end of a table that the cache contains.

## Consequences

**Positive:**

- A scan stops within one block after its context ended, whether the cache
  contains the blocks.
- A read under a context that ended returns the same error for a table in
  the cache and for a table on disk.

**Negative:**

- A move that needs a block calls `Err` on its context. For a context of
  `context.WithCancel` in Go 1.27.2 that is a call through the interface and
  an atomic load.
- A read that the cache would have served returns an error after its
  context ended. A read that must finish takes a context that does not end.
  `context.WithoutCancel` returns such a context.

**Neutral:**

- The table format and the signatures do not change.

## References

- RFC-0068, a context in each read and write of a table.
- ADR-0065, reads and writes of a table take a context.
- The context package of Go 1.27.2, `src/context/context.go`
  (`cancelCtx.Err`).
