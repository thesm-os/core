// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package arena

import "reflect"

// Aligned returns n zeroed bytes whose first byte is at an address that is a
// multiple of align. A write with direct I/O needs such a buffer. On Linux,
// statx reports the memory alignment of a file for direct I/O.
//
// Aligned allocates n+align-1 bytes and returns the n bytes from the first
// multiple of align among them. align does not need to be a power of two. The
// garbage collector does not move a heap object, so the slice keeps its
// address for its lifetime. The length and the capacity of the slice are n,
// so an append past n copies the bytes to a new array at any address.
//
// For an n of 0 or below, Aligned returns nil. Aligned counts an align below
// 1 as 1. For the alignment 0 that statx reports for a file without direct
// I/O, it returns bytes at any address.
//
// # Preconditions
//
// n+align-1 must not exceed the largest slice that make allocates. make
// panics for a larger length, as for any slice.
//
// # Allocation contract
//
// Aligned makes one allocation of n+align-1 bytes. The runtime rounds it up
// to a size class, so Aligned(4096, 4096) takes 8,192 bytes. For an n of 0 or
// below, Aligned does not allocate.
func Aligned(n, align int) []byte {
	if n <= 0 {
		return nil
	}
	align = max(align, 1)
	// With align-1 bytes beyond n, the slice from the first multiple of align
	// has room for n bytes.
	//dokimi:mutate-skip aor: a larger allocation returns the same slice, and BenchmarkAligned checks its bytes
	extra := align - 1
	raw := make([]byte, n+extra)
	// Aligned uses the address as an integer alone. It does not convert the
	// address back to a pointer.
	off := (align - int(reflect.ValueOf(&raw[0]).Pointer()%uintptr(align))) % align
	return raw[off : off+n : off+n]
}
