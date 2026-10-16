---
adr: 0071
title: A Writer Keeps a Bounded Workspace
status: Accepted
date: 2026-10-17
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0071: A Writer Keeps a Bounded Workspace

## Status

Accepted

## Context

`sstable.Writer.Reset` keeps the buffers of a Writer and the scratch of its
filter builder for the next table. The buffers follow the largest table
that the Writer wrote. A Writer that wrote a table of 1,000,000 keys of 14.3
MB kept 44.6 MB through a small table and while it waited. An engine could
not read the memory of a Writer to budget it.

## Decision

We will bound the memory that a Writer keeps with
`WriterOptions.MaxRetained`, and count it with `Writer.Footprint`, because an
engine then budgets each Writer and keeps the reuse of buffers within the
budget.

- `Writer.Footprint` returns the capacity in bytes of the buffers of the
  Writer and of the scratch of its filter builder, which
  `filter.FuseBuilder.Footprint` returns.
- A Writer whose footprint exceeds `MaxRetained` releases its buffers after
  `Close` writes a table, and at `Reset`.
- `MaxRetained` of 0 keeps every buffer. `NewWriter` returns `ErrOptions`
  for a negative bound.

## Alternatives Considered

### A method that releases the buffers

Rejected. Every engine would have to place the call, and a Writer that an
engine keeps is bounded at `Close` already.

### A pool of buffers that the Writers share

Rejected. A `sync.Pool` empties at the second collection after a put, so the
allocations of a run of compactions would depend on the garbage collector.

### A default bound

Rejected. A default would change the allocations of every caller that writes
many tables of one size.

### A release at every Close

Rejected. A Writer that writes many tables of one size would allocate its
buffers for every table.

## Consequences

**Positive:**

- An engine reads the memory of each Writer and bounds what an idle Writer
  keeps. With a bound of 1 MiB, the Writer of a table of 1,000,000 keys kept
  0.51 MB through the small tables after it and while it waited, instead of
  44.6 MB.
- A Writer under its bound keeps the allocations of today.

**Negative:**

- A table after a release allocates its buffers again as they grow. The
  first table of 10,000 entries after a release took 59 to 79 ns per entry,
  against 48 to 63 ns with the buffers kept.
- `WriterOptions` gains a field, and `Writer` and `filter.FuseBuilder` gain a
  method each.

**Neutral:**

- The table format does not change.

## References

- RFC-0071, a bounded workspace for a table writer.
- The fmt package of Go 1.27.2, `src/fmt/print.go` (`pp.free`).
