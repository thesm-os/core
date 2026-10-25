---
adr: 0074
title: Aligned Reads Addresses Through reflect
status: Accepted
date: 2026-10-25
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0074: Aligned Reads Addresses Through reflect

## Status

Accepted

## Context

A write with direct I/O needs a buffer at an address that is a multiple of
the memory alignment of its file. On Linux, `statx` reports that alignment in
`stx_dio_mem_align` and reports 0 for a file without direct I/O. The probe of
issue 90 wrote 4,096 bytes with `O_DIRECT` to a file on XFS with an alignment
of 4,096. The write from an aligned buffer succeeded. The write from a buffer
one byte past an aligned address failed with `EINVAL`.

The Go specification gives a `[]byte` the alignment of a byte and does not
state the layout of the heap. The runtime of go1.27.3 places each object of a
size class at a multiple of the minimum alignment of the class
(`internal/runtime/gc/sizeclasses.go`). The class of 512 bytes has a minimum
alignment of 512, so one object of the class in eight is at a multiple of
4,096.

ADR-0050 records that core does not import `unsafe`, so core does not read an
address with `unsafe.SliceData`. No package of core allocates at an
alignment. `reflect.Value.Pointer` returns the value of a pointer as a
`uintptr`, and it makes the object of the pointer escape to the heap
(`reflect/value.go` of go1.27.3). The garbage collector of go1.27.3 is
non-compacting (`runtime/mgc.go`), so a heap object keeps its address.

Prior art allocates more bytes than the buffer needs and starts the buffer at
the first aligned address among them:

- `AlignedBlock` of ncw/directio allocates the size of the block plus the
  alignment. It reads the address through `unsafe` and requires an alignment
  that is a power of two.
- `AlignedBuffer::AllocateNewBuffer` of RocksDB rounds the capacity up to the
  alignment and allocates the capacity plus the alignment.
  `AlignedBuffer::Alignment` asserts that the alignment is a power of two.

## Decision

We will add `arena.Aligned` and read the address of its allocation through
`reflect.Value.Pointer`, because core does not import `unsafe`:

```go
// Aligned returns n zeroed bytes whose first byte is at an address that is a
// multiple of align.
func Aligned(n, align int) []byte
```

- `Aligned` allocates n+align-1 bytes. It computes the offset of the first
  multiple of align from the address and returns the n bytes at the offset.
  It uses the address as an integer alone. Core does not convert a `uintptr`
  back to a pointer.
- Any positive align works, because any align consecutive addresses contain
  one multiple of align.
- For an n of 0 or below, `Aligned` returns nil and does not allocate. An
  align below 1 counts as 1. For the alignment 0 of a file without direct
  I/O, `Aligned` returns bytes at any address.
- `Aligned` does not return an error. `pool.NewPool`, `pool.NewResetPool` and
  `pool.NewBounded` take a constructor without an error result. A pool of
  aligned buffers calls `Aligned` in its constructor.
- The length and the capacity of the slice are n, as for `Arena.Append`. An
  `append` past n copies the bytes to an array at any address.

## Alternatives Considered

### unsafe.SliceData

`Aligned` would read the address with `unsafe.SliceData`, as ncw/directio
reads it with `unsafe.Pointer`.

Rejected. Core does not import `unsafe`, and `reflect.Value.Pointer` returns
the same integer.

### An error for an alignment that is not a power of two

`Aligned` would return `ErrConfig` for a negative n and for an align that is
not a power of two. Issue 90 proposed this form.

Rejected. A pool calls `Aligned` in a constructor without an error result,
and each caller would check an error that its fixed arguments rule out. The
arithmetic of the offset works for any positive align.

### Memory outside the Go heap

`Aligned` would map pages with `mmap`, or with `VirtualAlloc` on Windows.

Rejected. The garbage collector does not free such memory, so each buffer
needs a call that releases it. Each system needs its own call. A mapping
starts at a page boundary, so a larger alignment needs a larger mapping.

### An allocation of n bytes first

`Aligned` would allocate n bytes and return them when the runtime placed them
at a multiple of align. Otherwise it would allocate n+align-1 bytes. For k
from 1 to 16, the probe of issue 90 found each allocation of k times 4,096
bytes at a multiple of 4,096.

Rejected. The count of allocations would depend on the layout of the runtime.
An allocation at another address would cost a second allocation.

### A pool of aligned buffers in core

Rejected. A `pool.NewBounded` whose constructor calls `Aligned` is that pool.

## Consequences

**Positive:**

- A writer with direct I/O allocates its buffers in core at the alignment
  that its file reports.
- `Aligned` composes with each pool of core because it does not return an
  error.

**Negative:**

- An allocation takes up to align-1 bytes more than its slice, and the
  runtime rounds it up to a size class. `Aligned(4096, 4096)` requests 8,191
  bytes, and go1.27.3 allocates 8,192.
- The alignment of a slice depends on a heap that does not move its objects.
  The Go specification does not state that property, and a compacting
  collector would move the slices of `Aligned`.
- A caller that passes an align other than the alignment of its file gets a
  slice, and its write fails with `EINVAL`.
- `arena` imports `reflect`.

**Neutral:**

- ADR-0056 states that Go computes neither the slab nor the offset of a slice
  without `unsafe`. With `reflect.Value.Pointer`, Go computes both from the
  address of the slice. ADR-0056 still applies, because the records of a
  `RefSlabs` contain no pointer.

## References

- Issue 90, <https://github.com/thesm-os/core/issues/90>.
- ADR-0050, which records that core does not import `unsafe`.
- ADR-0056, `RefSlabs` allocates regions by reference.
- statx(2), `stx_dio_mem_align`,
  <https://man7.org/linux/man-pages/man2/statx.2.html>.
- Go 1.27.3, `reflect/value.go`, `Value.Pointer`.
- Go 1.27.3, `runtime/mgc.go`, the overview of the collector.
- Go 1.27.3, `internal/runtime/gc/sizeclasses.go`, the size classes.
- ncw/directio at 743c0ba8bd, `direct_io.go`, `AlignedBlock`,
  <https://github.com/ncw/directio/blob/743c0ba8bd9692ab9640be560d53d7ecbf0c4f10/direct_io.go#L37-L57>.
- facebook/rocksdb at ea31360536, `util/aligned_buffer.h`,
  `AlignedBuffer::AllocateNewBuffer`,
  <https://github.com/facebook/rocksdb/blob/ea313605367281013c2c81158069e88411516357/util/aligned_buffer.h#L163-L196>.
