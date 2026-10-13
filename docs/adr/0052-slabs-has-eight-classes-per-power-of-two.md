---
adr: 0052
title: Slabs Has Eight Size Classes per Power of Two
status: Accepted
date: 2026-10-13
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0052: Slabs Has Eight Size Classes per Power of Two

## Status

Accepted

## Context

`Slabs.Alloc` rounded each request up to a power of two of at least
`MinClass`. A slice of 1,025 bytes took 2,048 bytes of a slab. A store
whose values are a little above a power of two used about twice the memory
of its values. As the store grew, it also touched twice the pages of new
memory, and the kernel took a page fault for each new page.

We measured fills of a new `NewSlabs(DefaultSlabSize)` with 500,000 blocks
of one size and a copy into each block. Each fill ran alone on one CPU of a
Ryzen 9 9950X3D with Go 1.27.2. A fill of 1,025-byte blocks took 238.8 ns
per block and 251,131 page faults. A fill of 1,024-byte blocks took
125.3 ns per block and 125,943 page faults.

Go's runtime chooses its size classes so that rounding a request up to the
next class wastes at most 12.5%. From 128 to 4,096 bytes, its generator
steps the classes by an eighth of the power of two below them.

## Decision

We will give `Slabs` eight size classes between each power of two from
`MinClass` up because they bound the slab memory of a slice above
`MinClass` at less than 1.125 times its length.

- `MinClass` remains the smallest class and the class of every length up
  to `MinClass`.
- The classes above 2^e are 2^e plus one to eight times 2^(e-3). Every
  class is a multiple of 8 bytes, and every power of two from `MinClass`
  up remains a class.
- `Alloc` and `Free` compute the class and the index of its free list from
  the length or the capacity. `Free` returns `ErrSizeClass` for a capacity
  that is not a class.
- When the current slab has no room for a class, `Alloc` carves the rest of
  the slab into the largest classes that fit.
- A freed slice serves only its own class.

## Alternatives Considered

### Classes of powers of two

`Slabs` would keep one class per power of two.

Rejected. A store of blocks a little above a power of two uses twice the
memory and twice the page faults of its blocks.

### Four classes per power of two

The classes above 2^e would be 2^e plus one to four times 2^(e-2). The
waste of a slice would then be less than 25%.

Rejected. A block of 1,070 bytes would take 1,280 bytes instead of 1,152,
so a store of such blocks would use 11% more memory. The free lists of
four classes would take 5,472 bytes less per `Slabs`. One slab of
`DefaultSlabSize` is 1 MiB.

### A table of classes

`Free` would find the class of a capacity in a table, as Go's runtime does.

Rejected. The classes of `Slabs` follow one formula, so arithmetic computes
the class and its index without a table that must match the formula. Go's
runtime needs its table because it merges and widens classes by the pages
of a span.

### The next larger class for an empty free list

When the free list of a class is empty, `Alloc` would take a slice from the
free list of a larger class before it carves a new slice.

Rejected. No measurement of a store shows `Alloc` allocating new slabs
while the free lists of other classes contain much of the memory. The
capacity of a slice would also no longer be the class of its length. Such
a measurement of a store whose values change size would reverse this
decision.

## Consequences

**Positive:**

- A block between two powers of two takes less of a slab and fewer new
  pages. The medians of five fills on each side are:

  | Block size | Slab bytes per block | ns per `Alloc` and copy | Page faults of the fill |
  |---|---|---|---|
  | 1,024 | 1,024 → 1,024 | 125.3 → 125.2 | 125,943 → 125,944 |
  | 1,025 | 2,048 → 1,152 | 238.8 → 142.7 | 251,131 → 141,638 |
  | 1,070 | 2,048 → 1,152 | 239.9 → 142.2 | 251,121 → 141,633 |
  | 1,536 | 2,048 → 1,536 | 242.5 → 187.2 | 251,139 → 188,755 |
  | 2,048 | 2,048 → 2,048 | 237.2 → 241.6 | 251,136 → 251,120 |

- `Free` zeroes a smaller class for such a block. An `Alloc` and a `Free`
  of 1,070 bytes take 5.96 ns instead of 13.29 ns.

**Negative:**

- An `Alloc` and a `Free` of a class that did not change take 0.8 to
  1.2 ns more because the class takes more arithmetic. At 64 bytes they
  take 3.49 ns instead of 2.67 ns, and at 2,048 bytes 14.46 ns instead of
  13.29 ns.
- A `Slabs` takes 11,032 bytes instead of 1,600 on a 64-bit platform. It
  has 457 free lists instead of 64. `NewSlabs` allocates 12,288 bytes
  instead of 1,792.
- A slice above `MinClass` has less capacity beyond its length. An append
  past the capacity moves the slice out of the slab.
- Only an `Alloc` of its own class takes a freed slice. So in a store whose
  values change size, more freed memory remains on the free lists. `InUse`
  does not count the free lists, so a store that bounds its capacity by
  `InUse` does not see that memory.

**Neutral:**

- Every power of two from `MinClass` up remains a class, so a slice whose
  length is such a power keeps its capacity.

## References

- `arena/slabs.go`: `classOf`, `Slabs.Alloc`, `Slabs.Free` and
  `Slabs.retire`.
- Go 1.27.2, `src/runtime/_mkmalloc/mksizeclasses.go`: the package comment
  and `makeClasses`.
