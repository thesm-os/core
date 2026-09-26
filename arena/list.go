// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package arena

import (
	"fmt"
	"iter"
)

// Chunk sizes of a [List]. Element i is at offset i&listMask of chunk
// i>>listShift.
const (
	// listShift is log2 of listChunk.
	listShift = 12

	// listChunk is the number of elements in every chunk after the first,
	// and the most the first chunk grows to.
	listChunk = 4096

	// listMask selects the offset of an element within its chunk.
	listMask = 4095

	// listFirst is the number of elements in the first chunk of a new List.
	// The first chunk doubles from it until it reaches listChunk.
	listFirst = 1
)

// List is an append-only sequence of T in chunks. The first chunk starts
// with room for one element and doubles in size each time it fills, until
// it has room for 4,096, so a short List costs as little as a slice. Every
// later chunk is allocated whole, with room for 4,096 elements. Once the
// first chunk is full, every chunk keeps its address, and [List.Append]
// writes only the new element.
//
// The zero List is empty and ready to use.
//
// # Element addresses
//
// [List.Ptr] returns the address of an element. An element past the first
// 4,096 keeps its address from its Append until [List.Truncate] drops it.
// An element of the first chunk keeps its address once the first chunk has
// room for 4,096 elements. Before that, an Append that doubles the first
// chunk copies its elements to a new array, and an address or a slice from
// [List.Chunks] taken earlier refers to the old copy.
//
// # Reading
//
// [List.At] resolves the chunk of element i and copies the element, which
// costs more than indexing a slice. [List.Ptr] returns the address
// instead, so a read of one field of a large element copies only that
// field. [List.Chunks] visits the elements in order as fast as ranging
// over a slice.
//
// # Truncation
//
// [List.Truncate] keeps every chunk. A List that is truncated and filled
// again reuses its chunks, as a slice reuses its capacity after s[:0], and
// [List.Cap] does not change. Assigning the zero List releases every chunk.
//
// # Concurrency
//
// A List is not safe for concurrent use.
//
// # Allocation contract
//
// [List.Append] allocates only when the List grows past [List.Cap]: one
// chunk per 4,096 elements, the doublings of the first chunk, and the
// growth of the chunk index. [List.At], [List.Ptr], [List.Len], [List.Cap],
// [List.All], [List.Chunks] and [List.Truncate] do not allocate.
type List[T any] struct {
	// chunks[k] contains elements k<<listShift to (k+1)<<listShift - 1.
	// Every chunk after the first has listChunk elements.
	chunks [][]T

	// cur is the chunk that contains element base, the first element of
	// the chunk that Append writes into. It is nil before the first Append
	// and after a Truncate to the end of the last chunk.
	cur  []T
	base int

	// off is the offset in cur of the next element, so the List has
	// base+off elements.
	off int

	// Append compares off with end, a copy of len(cur), because the field
	// costs the compiler's inliner less than len(cur) and keeps Append
	// within its budget.
	end int
}

// Append adds v at index [List.Len].
//
// # Allocation contract
//
// Zero alloc when the List has room for the element: for every Append
// while [List.Len] is below [List.Cap], which includes every Append into
// the chunks a [List.Truncate] kept.
func (l *List[T]) Append(v T) {
	if l.off == l.end {
		l.grow()
	}
	l.cur[l.off] = v
	l.off++
}

// grow makes room for the element at index Len: the first chunk of a new
// List, a first chunk of twice the size, a new chunk of listChunk
// elements, or a chunk that Truncate kept.
func (l *List[T]) grow() {
	n := l.base + l.off
	k, off := n>>listShift, n&listMask

	if k == len(l.chunks) {
		size := listChunk
		if k == 0 {
			size = listFirst
		}
		l.chunks = append(l.chunks, make([]T, size))
	} else if off == len(l.chunks[k]) {
		// Only the first chunk can be full here: every later chunk is
		// allocated with all listChunk elements, so an offset in it is
		// always below its length.
		first := make([]T, min(listChunk, 2*off))
		copy(first, l.chunks[k])
		l.chunks[k] = first
	}

	l.cur, l.base, l.off = l.chunks[k], k<<listShift, off
	l.end = len(l.cur)
}

// Len returns the number of elements in the List.
func (l *List[T]) Len() int {
	return l.base + l.off
}

// Cap returns the number of elements the List stores without allocating:
// the length of the first chunk plus 4,096 for every later chunk.
func (l *List[T]) Cap() int {
	if len(l.chunks) == 0 {
		return 0
	}

	return len(l.chunks[0]) + (len(l.chunks)-1)<<listShift
}

// At returns the element at index i.
//
// Panics with the runtime's index error when i is outside [0, Len()), as
// an index expression on a slice does.
func (l *List[T]) At(i int) T {
	if j := i - l.base; j >= 0 {
		// Slicing cur to the elements written makes an index at or past
		// Len fail the bounds check.
		return l.cur[:l.off][j]
	}

	return l.chunks[i>>listShift][i&listMask]
}

// Ptr returns the address of the element at index i. See [List] for how
// long the element keeps that address.
//
// Panics with the runtime's index error when i is outside [0, Len()), as
// [List.At] does.
func (l *List[T]) Ptr(i int) *T {
	if j := i - l.base; j >= 0 {
		return &l.cur[:l.off][j]
	}

	return &l.chunks[i>>listShift][i&listMask]
}

// All returns an iterator over the index and the value of every element,
// in index order. [List.Chunks] visits the same elements in less time,
// because it yields a chunk at a time.
func (l *List[T]) All() iter.Seq2[int, T] {
	return func(yield func(int, T) bool) {
		i := 0
		for c := range l.Chunks() {
			for _, v := range c {
				if !yield(i, v) {
					return
				}
				i++
			}
		}
	}
}

// Chunks returns an iterator over the elements a chunk at a time, in index
// order. Every yielded slice except the last has 4,096 elements. The
// capacity of each slice is its length, so an append to it cannot write
// into the List.
//
// A yielded slice shares memory with the List, with the exception for the
// first chunk that [List] describes under element addresses. [List.Truncate]
// zeroes the dropped elements in place, and a later [List.Append] writes
// over them.
func (l *List[T]) Chunks() iter.Seq[[]T] {
	return func(yield func([]T) bool) {
		left := l.Len()
		for _, c := range l.chunks {
			if left == 0 {
				return
			}

			m := min(len(c), left)
			if !yield(c[:m:m]) {
				return
			}
			left -= m
		}
	}
}

// Truncate drops the elements at index n and later. It sets each dropped
// element to the zero value, so the List no longer references what the
// elements referenced. Every chunk remains allocated for later appends.
//
// Panics when n is outside [0, Len()].
//
// # Allocation contract
//
// Zero alloc. Runs in time proportional to the number of dropped elements.
func (l *List[T]) Truncate(n int) {
	end := l.Len()
	if n < 0 || n > end {
		panic(fmt.Sprintf( //nolint:forbidigo // a length outside the List is a programmer error
			"arena: List.Truncate length %d out of range [0:%d]", n, end,
		))
	}

	for i := n; i < end; {
		c := l.chunks[i>>listShift][i&listMask:]
		c = c[:min(len(c), end-i)]
		clear(c)
		i += len(c)
	}

	l.cur, l.base, l.off, l.end = nil, n, 0, 0
	if k := n >> listShift; k < len(l.chunks) {
		l.cur, l.base, l.off = l.chunks[k], k<<listShift, n&listMask
		l.end = len(l.cur)
	}
}
