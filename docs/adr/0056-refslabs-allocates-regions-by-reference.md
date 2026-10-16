---
adr: 0056
title: RefSlabs Allocates Regions by Reference
status: Accepted
date: 2026-10-15
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0056: RefSlabs Allocates Regions by Reference

## Status

Accepted

## Context

`Slabs.Alloc` returns a `[]byte`, and `Slabs.Free` takes the slice back. A
store that keeps one of these slices in each record has a pointer in each
record. The garbage collector then scans every record, although all the
pointers lead into a few slabs.

A radix trie keeps its leaves in `arena.List` chunks and the bytes of its
longer keys outside the leaves. If a leaf contains a slice, every chunk of
leaves becomes an object with pointers.

Probes outside this module measured the cost with Go 1.27.2 on four cores
of a Ryzen 9 9950X3D, in three runs each:

| Structure live during `runtime.GC` | Time or GC CPU per collection |
|---|---|
| 2^20 tree nodes in one slice, linked by pointer | 2.26-2.32 ms |
| 2^20 tree nodes in one slice, linked by `uint32` index | 0.045-0.052 ms |
| 2^20 tree nodes as separate heap objects | 12.7-16.1 ms |
| Empty heap | 0.043-0.047 ms |
| `btree.MapFunc[[]byte, []byte]` over one arena, 1M keys | 15.7-18.7 ms of GC CPU |
| `btree.MapFunc[uint32, uint32]` over arena offsets, 1M keys | 0.470-0.477 ms of GC CPU |

`Slabs` cannot hand out a position instead of a slice. Its free lists keep
slices. `Free(b)` takes the size class of `b` from its capacity, but it
cannot compute the slab and the offset of `b`, because Go computes neither
from a slice without `unsafe`. Core does not import `unsafe`.

## Decision

We will add `arena.RefSlabs`, an allocator with the size classes of
`Slabs` that returns a `Ref` instead of a slice, because a record that
stores a `Ref` contains no pointer:

```go
// Ref is the position of a region in a RefSlabs. The high 32 bits are the
// number of its slab plus one, and the low 32 bits are its offset in the
// slab. The zero Ref is no region.
type Ref uint64

// RefSlabs allocates regions of bytes from slabs and returns each region as
// a Ref. It takes single regions back for reuse in any order.
type RefSlabs struct{ /* unexported */ }

// NewRefSlabs returns an empty RefSlabs whose slabs are slabSize bytes.
func NewRefSlabs(slabSize int) *RefSlabs

// Alloc reserves the size class of n zeroed bytes and returns its Ref.
func (s *RefSlabs) Alloc(n int) Ref

// Bytes returns the first n bytes of the region at r.
func (s *RefSlabs) Bytes(r Ref, n int) []byte

// Free zeroes the size class of n bytes at r and keeps it for a later
// Alloc of the class.
func (s *RefSlabs) Free(r Ref, n int) error

// InUse returns the bytes of the size classes that Alloc returned and
// Free has not taken back.
func (s *RefSlabs) InUse() int

// All returns an iterator over every slab in the order of allocation.
func (s *RefSlabs) All() iter.Seq[[]byte]

// ErrRef reports a Ref whose region does not lie inside a slab of the
// RefSlabs. It classifies as errs.Invalid.
var ErrRef = errs.WithClass(errors.New("arena: the reference is outside the slabs"), errs.Invalid)
```

- The size classes, the carving of the current slab, the retirement of the
  rest of a slab and the zeroing are those of `Slabs`. Both types compute a
  class with one function.
- The free list of each class keeps `Ref` values, so a `RefSlabs` contains
  pointers only in its table of slabs.
- `Alloc` returns the zero `Ref` for n of zero or less.
- `Bytes` returns a slice of length n and the capacity of the class of n.
  It returns nil for the zero `Ref` and for a `Ref` whose region does not
  lie inside a slab of s. It does not panic, because a store calls it on
  every read.
- `Free` returns `ErrRef` for a `Ref` whose region does not lie inside a
  slab of s, and does nothing then. Like `Slabs.Free`, it cannot detect a
  second `Free` of one region, or an n of another class than the `Alloc`
  of the region.
- `NewRefSlabs` limits the slab size to 2^32 bytes, so every offset fits
  its 32 bits. A class larger than the slab size takes a slab of its own at
  offset 0, as in `Slabs`.
- The zero `RefSlabs` is empty and allocates from slabs of
  `DefaultSlabSize` bytes.

`Slabs` does not change.

## Alternatives Considered

### Reference methods on Slabs

`Slabs` would gain `AllocRef`, `Bytes` and `FreeRef` beside `Alloc` and
`Free`.

Rejected. `Free(b)` puts a slice on a free list, and `AllocRef` cannot take
that slice, because a slice does not yield its offset without `unsafe`.
Each `Slabs` would keep a second free list for each of its 457 classes,
10,968 bytes more, and the memory that one form frees would serve only that
form.

### A byte slice that the trie grows and compacts

The trie would append the bytes of each key to one `[]byte` and record
offsets. It would rewrite the slice when deleted keys take half of it.

Rejected. The trie would repeat the reuse of freed space that `Slabs`
already implements. A `Delete` that starts the rewrite copies every live
key, which is 56 MB for 1M keys of 56 bytes.

### Slices from Slabs in the records

Rejected. Every chunk of records becomes an object that the collector
scans. With 2^20 nodes linked by pointer, a collection took 2.26-2.32 ms
against 0.045-0.052 ms for nodes linked by index.

### Offsets in a slab of unsafe pointers

Rejected. Core does not import `unsafe`.

## Consequences

**Positive:**

- A store that keeps a `Ref` in each record keeps the records free of
  pointers, so the collector marks their chunks without scanning them.
- A `RefSlabs` reuses the space of a removed region for a later region of
  its class without an allocation, in any order, as a `Slabs` does.

**Negative:**

- `arena` has two allocators with the same size classes, and each needs its
  documentation and its tests.
- A `Ref` does not record its length. The caller passes n to `Bytes` and to
  `Free`. An n of another class than the region's corrupts the free list of
  that class, where `Slabs` takes the class from the capacity.
- A `RefSlabs` takes about as much memory as a `Slabs`, 11,032 bytes on a
  64-bit platform, because its 457 free lists are slice headers too.
- `Bytes` indexes the table of slabs and slices the slab on every call.
  The cost against a slice that a record keeps is not measured.

**Neutral:**

- `Slabs` keeps its contract and its cost.

## References

- RFC-0059, radix tries for prefix queries.
- ADR-0052, the size classes of `Slabs`.
- ADR-0050, which records that core does not import `unsafe`.
- `arena/slabs.go`: `classOf`, `Slabs.Alloc`, `Slabs.Free` and
  `Slabs.retire`.
