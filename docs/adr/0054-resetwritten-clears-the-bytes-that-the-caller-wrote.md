---
adr: 0054
title: ResetWritten Clears the Bytes That the Caller Wrote
status: Accepted
date: 2026-10-15
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0054: ResetWritten Clears the Bytes That the Caller Wrote

## Status

Accepted

## Context

`Arena.Reset` zeroes every byte up to the length of the arena. It also
zeroes the bytes up to the dirty mark that a `TruncateTo` rewind or a
failed `AppendVia` left past the length. The arena counts the whole
region of an `Alloc` as written, because it cannot see which bytes of
the region the caller writes.

A write buffer whose readers follow offsets needs its capacity as one
region, because `Alloc` and `Bytes` cap each slice at the length of the
arena. Its writer takes the capacity with one `Alloc` and fills a
prefix. Every Reset then clears the whole capacity. The first Reset of a
new arena also writes every page of the capacity, so pages that the
caller never used become resident.

A probe ran with Go 1.27.2 on one CPU of a Ryzen 9 9950X3D. Each
iteration took an `Alloc`, wrote 100 bytes into it and called Reset. The
case whole allocated the capacity, and the case used allocated the 100
bytes. Each case ran three times for 500 ms:

| Capacity | ns per iteration, whole | ns per iteration, used |
|---|---|---|
| 1 MiB | 4,794 to 4,854 | 3.78 to 4.07 |
| 64 MiB | 877,720 to 880,097 | 3.81 to 4.08 |
| 256 MiB | 3,503,114 to 3,526,209 | 3.79 to 4.07 |

The first Reset of a new arena of 256 MiB, after one `Alloc` of its
capacity and a write of 100 bytes, raised the peak resident memory of
the process from 24 MiB to 259 MiB.

## Decision

We will add `Arena.ResetWritten(n int)`. It ends a lifecycle as Reset
does, for a caller that wrote only the first n bytes since the previous
Reset:

- ResetWritten zeroes the first n bytes and the bytes below the dirty
  mark. It leaves the rest of the length as it is.
- An n above the length counts as the length. Every region of `Alloc`
  and `Append` ends at or before the length.
- A negative n counts as the length too, so ResetWritten then clears
  what Reset clears. ResetWritten does not panic, because it runs where
  a pooled value is released. A panic there ends the process.
- Every byte of the length at or past n must be zero. ResetWritten does
  not check it, because the check would read the bytes that it skips. A
  byte that the caller wrote past n remains in the spare capacity, and the
  next `Alloc` returns it in place of a zero.

Reset keeps its contract. It calls ResetWritten with the length.

## Alternatives Considered

### An Alloc for each record

The arena would count only the bytes in use.

Rejected. A reader that follows offsets needs the capacity as one slice.
The writer would publish a new slice to its readers after every record.

### Slabs

The buffer would come from `Slabs`.

Rejected. `Slabs.Free` zeroes the whole size class of a slice, so a
buffer of the same size costs the same clear.

### A byte slice that the caller clears itself

Rejected. The caller would repeat in its own code the invariant of
`Arena` that the spare capacity is zero.

### An arena that records the end of every Append

The arena would clear up to the end of the last `Append`, so a caller
could not pass an n below it.

Rejected. Every Append would write one more field. The writes into an
`Alloc` region would still be the caller's to report.

### A panic for a negative n

Rejected. A negative n is a bug of the caller, but the call runs on the
release path of a pooled value. The release of a value that readers
pin runs on the goroutine of its last reader. A full clear keeps the
spare capacity zero at the cost of one Reset.

## Consequences

**Positive:**

- A caller that writes a prefix of a large region clears only the prefix.
  The probe through ResetWritten took 3.97 to 4.56 ns per iteration at
  each of the three capacities.
- The first ResetWritten of a new arena touches only the pages that the
  caller wrote. After the first ResetWritten of the arena of 256 MiB, the
  peak resident memory of the process was still 23 MiB.

**Negative:**

- ResetWritten does not check its precondition. A caller that passes an
  n below its last write leaves old bytes in the spare capacity of a
  pooled arena, and the next `Alloc` of another user returns them.
- The arena has two methods that end a lifecycle.

**Neutral:**

- Reset clears what it cleared before, at the same cost. An Append of
  1,024 bytes and a Reset took 10.31 ns before the change and 10.32 ns
  after it. benchstat found no difference in 12 runs of each (p=0.788).

## References

- Issue 50, <https://github.com/thesm-os/core/issues/50>.
- `arena/lifecycle.go`: `Reset` and `ResetWritten`. `arena/arena.go`:
  `Alloc` and the field `dirty`. `arena/appendvia.go`: `AppendVia` and
  `TruncateTo`.
- Go 1.27.2, `runtime/mheap.go:327-337` and `:1063-1114`: each heap arena
  records in `zeroedBase` the first byte that no allocation has used, and
  `allocNeedsZero` skips the clear of an allocation above it.
