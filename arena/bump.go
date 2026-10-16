// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package arena

import "sync/atomic"

// Bump is a fixed array of values of type T that goroutines reserve in
// ranges without locks. [Bump.Reserve] moves the end of the reserved values
// with a compare-and-swap that keeps the end at or below the capacity. The
// array never grows or moves, so a reserved range keeps its position until
// [Bump.Reset].
//
// A goroutine writes only the values that its Reserve returned. A goroutine
// that reads the values of another synchronizes with it first, such as
// through an atomic store and load of a word that publishes them.
//
// The zero Bump has a capacity of 0.
//
// # Concurrency
//
// Reserve, [Bump.Values] and [Bump.Len] are safe for concurrent use. Reset
// is not safe concurrently with any other method.
//
// # Allocation contract
//
// [NewBump] allocates the Bump and its array. The methods do not allocate.
type Bump[T any] struct {
	// values is the array. Its length is the capacity of the Bump.
	values []T

	// end is the number of reserved values. It is at most len(values).
	end atomic.Uint64
}

// NewBump returns an empty [Bump] with room for capacity values. A negative
// capacity counts as 0.
//
// # Allocation contract
//
// Allocates the Bump and its array of capacity values.
func NewBump[T any](capacity int) *Bump[T] {
	return &Bump[T]{values: make([]T, max(capacity, 0))}
}

// Reserve reserves the next n values and returns the index of the first in
// [Bump.Values]. It reports false and changes nothing when fewer than n
// values remain, and for an n below 0. A Reserve of 0 values returns the
// number of reserved values.
//
// The reserved values are zero: they are the values of a new array, or
// values that [Bump.Reset] zeroed. When another goroutine moves the end
// first, the compare-and-swap of Reserve fails, and Reserve tries again from
// the new end.
//
// # Allocation contract
//
// Zero-alloc.
func (b *Bump[T]) Reserve(n int) (int, bool) {
	//dokimi:mutate-skip sbr-delete,ror-false: a negative n converts to 2^63 or more, which the check of the room refuses too
	if n < 0 {
		return 0, false
	}
	for {
		end := b.end.Load()
		if uint64(n) > uint64(len(b.values))-end {
			return 0, false
		}
		if b.end.CompareAndSwap(end, end+uint64(n)) {
			//nolint:gosec // G115: end is at most len(b.values)
			return int(end), true
		}
	}
}

// Values returns every value of the array, reserved or not. The slice has
// the length and the capacity of the array, and every call returns the same
// array.
//
// # Allocation contract
//
// Zero-alloc.
func (b *Bump[T]) Values() []T {
	return b.values
}

// Len returns the number of reserved values. It is at most the capacity.
//
// # Allocation contract
//
// Zero-alloc.
func (b *Bump[T]) Len() int {
	//nolint:gosec // G115: the end is at most len(b.values)
	return int(b.end.Load())
}

// Reset zeroes the reserved values and ends every reservation, so the next
// Reserve starts at index 0. It keeps the array, and takes time in
// proportion to the reserved values.
//
// # Allocation contract
//
// Zero-alloc.
func (b *Bump[T]) Reset() {
	clear(b.values[:b.end.Load()])
	b.end.Store(0)
}
