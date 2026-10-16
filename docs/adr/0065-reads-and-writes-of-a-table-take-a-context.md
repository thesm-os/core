---
adr: 0065
title: Reads and Writes of a Table Take a Context
status: Accepted
date: 2026-10-16
supersedes: none
superseded-by: ADR-0070
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0065: Reads and Writes of a Table Take a Context

## Status

Accepted

[ADR-0070](0070-a-cursor-checks-its-context-before-each-block.md)
supersedes the consequence that a move that does not read a block ignores
its context. The decision to pass the context of the caller to every call
that can read or write a table remains in force.

## Context

No call of `sstable` took a context. A `Reader` read every block through an
`io.ReaderAt` and a `Writer` wrote through an `io.Writer`, so a read that
waited for a permit, a budget or a remote store did not end with the context
of its caller.

## Decision

We will pass the context of the caller to every call that can read or write
a table, because a wait for a file then ends with the context of its caller.

- A `Reader` reads a `File` and a `Writer` writes a `Sink`. `File.ReadAt` and
  `Sink.Write` take a context.
- `Open`, `Reader.Get`, `Writer.Add`, `Writer.Close` and the two methods of
  a `BlockCache` take a context.
- Each move of a `Source` takes the context of the read that it can make. A
  `Merger` passes it to its sources, and `skiplist.Iter` ignores it.
- The table format does not change.

## Alternatives Considered

### A context for each cursor

Rejected. The cursor would keep the context in its struct, and an engine
whose cursor takes a context in each move would create its table cursors
again for each move.

### A file and a cache for each cursor, with io.ReaderAt

Rejected. Each caller would keep the context of its read in the struct of
its file, and the writes would take no context.

## Consequences

**Positive:**

- A read or a write of a table that waits ends when the context of its
  caller ends.
- A cache counts and traces each lookup under the context of its read.

**Negative:**

- Every caller of `Open`, `Get`, the moves, the methods of a `BlockCache` and
  the calls of a `Writer` changes.
- `skiplist.Iter` takes a context that it does not use.
- A file of the operating system needs a type of one method to become a
  `File`.

**Neutral:**

- A move that does not read a block ignores its context.
- The calls allocate as they did before.

## References

- RFC-0068, a context in each read and write of a table.
- Issue 62, <https://github.com/thesm-os/core/issues/62>.
