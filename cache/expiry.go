// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package cache

// expiry orders the entries that have an expiry time by that time, in a
// binary min-heap. The entry at the root expires first, and no entry
// expires before its parent. Each entry records its position in its slot,
// so a removal from any position takes O(log n) steps for n entries. A
// push of an entry that expires no earlier than its parent takes one
// comparison, which is the case of a cache whose entries share one time to
// live.
//
// The zero expiry is an empty heap.
//
// # Concurrency
//
// Only the holder of the cache's lock reads or writes the heap and the
// slots of its entries.
//
// # Allocation contract
//
// push allocates when the heap grows, which doubles it. remove and first
// do not allocate.
type expiry[K comparable, V any] struct {
	// heap contains the entries in the order of a binary heap: the
	// children of the entry at position i are at 2i+1 and 2i+2.
	heap []*entry[K, V]
}

// push adds e, an entry with an expiry time that the heap does not
// contain.
func (x *expiry[K, V]) push(e *entry[K, V]) {
	x.heap = append(x.heap, e)
	x.up(e, len(x.heap)-1)
}

// remove takes e, an entry of the heap, out of the heap. The last entry of
// the heap takes the position of e, and moves down or up to its place.
func (x *expiry[K, V]) remove(e *entry[K, V]) {
	last := len(x.heap) - 1
	moved := x.heap[last]
	x.heap[last] = nil
	x.heap = x.heap[:last]

	if moved == e {
		return
	}

	if !x.down(moved, e.slot) {
		x.up(moved, e.slot)
	}
}

// first returns the entry that expires first, and nil for an empty heap.
func (x *expiry[K, V]) first() *entry[K, V] {
	if len(x.heap) == 0 {
		return nil
	}

	return x.heap[0]
}

// up stores e at position i or above it. It moves each parent that
// expires after e one level down, and stores e below the first parent that
// does not.
func (x *expiry[K, V]) up(e *entry[K, V], i int) {
	for i > 0 {
		p := (i - 1) / 2
		parent := x.heap[p]

		if !e.expires.Before(parent.expires) {
			break
		}

		x.heap[i], parent.slot = parent, i
		i = p
	}

	x.heap[i], e.slot = e, i
}

// down stores e at position i or below it, and reports whether e moved
// down. It moves the child that expires first one level up while that
// child expires before e.
func (x *expiry[K, V]) down(e *entry[K, V], i int) bool {
	start, n := i, len(x.heap)

	for {
		c := 2*i + 1
		if c >= n {
			break
		}

		if r := c + 1; r < n && x.heap[r].expires.Before(x.heap[c].expires) {
			c = r
		}

		child := x.heap[c]
		if !child.expires.Before(e.expires) {
			break
		}

		x.heap[i], child.slot = child, i
		i = c
	}

	x.heap[i], e.slot = e, i

	return i > start
}
