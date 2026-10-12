// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package idmap

import (
	"iter"

	"go.thesmos.sh/core/id"
)

// Typed is a hash map for one goroutine, from identifiers of a type of the
// caller to values. K is a struct type whose one field embeds [id.ID], such
// as
//
//	type StreamID struct{ id.ID }
//
// An [id.ID] has 16, 20 or 32 bytes, or none for [id.Zero]. Typed keeps the
// keys of each size in a [Map] of that size, keyed by [id.ID128], [id.ID160]
// or [id.ID256]. A lookup converts the key to the identifier of its size,
// and hashes and compares the bytes of that identifier in the Map of that
// size. The first Set of a key of a size creates the Map of that size with
// room for the capacity that [NewTyped] received. The value of the key whose
// identifier is id.Zero is beside the three maps.
//
// The zero Typed is not usable. NewTyped returns an empty map.
//
// # Concurrency
//
// A Typed is not safe for concurrent use. Any number of goroutines may call
// Get, Len, All and Clone on a map that no goroutine writes.
//
// # Allocation contract
//
// NewTyped allocates the map. The first Set of a key of each size allocates
// the Map of that size and its three arrays. [Typed.Clone] allocates the
// copy, and the copy of each Map of the map with its three arrays. Get,
// Delete, Len, Clear and a range over All do not allocate. Set allocates the
// three arrays of a growth, and nothing otherwise.
type Typed[K ~struct{ id.ID }, V any] struct {
	// m128, m160 and m256 have the keys of 16, 20 and 32 bytes. Each is nil
	// before the first Set of a key of its size.
	m128 *Map[id.ID128, V]
	m160 *Map[id.ID160, V]
	m256 *Map[id.ID256, V]

	// zero is the value of the key whose identifier is id.Zero.
	zero V

	// capacity is the room for keys of each Map that a first Set creates.
	capacity int

	// hasZero reports whether the map contains the key whose identifier is
	// id.Zero.
	hasZero bool
}

// NewTyped returns an empty map of the struct type K, whose one field
// embeds an [id.ID]. The first Set of a key of each size creates the Map of
// that size with room for capacity keys, as [New] does.
//
// # Allocation contract
//
// NewTyped allocates the map.
func NewTyped[K ~struct{ id.ID }, V any](capacity int) *Typed[K, V] {
	return &Typed[K, V]{capacity: capacity}
}

// Get returns the value of key, and reports whether key is present.
//
// # Allocation contract
//
// Zero alloc.
func (t *Typed[K, V]) Get(key K) (V, bool) {
	k := struct{ id.ID }(key).ID

	if f, ok := k.As128(); ok && t.m128 != nil {
		h, _ := t.m128.hash(t.m128.seed, f)

		return t.m128.get(h, &f)
	}

	if f, ok := k.As160(); ok && t.m160 != nil {
		h, _ := t.m160.hash(t.m160.seed, f)

		return t.m160.get(h, &f)
	}

	if f, ok := k.As256(); ok && t.m256 != nil {
		h, _ := t.m256.hash(t.m256.seed, f)

		return t.m256.get(h, &f)
	}

	if k.IsZero() {
		return t.zero, t.hasZero
	}

	var zero V

	return zero, false
}

// Set sets the value of key, and returns the previous value and whether key
// was present. The first Set of a key of a size creates the Map of that
// size. A Set of a new key into a Map that has as many keys as five eighths
// of its slots first doubles the slots of that Map.
//
// # Allocation contract
//
// Zero alloc, except for the Map and the three arrays of the first Set of a
// size, and for the three arrays of a growth.
func (t *Typed[K, V]) Set(key K, value V) (V, bool) {
	k := struct{ id.ID }(key).ID

	if f, ok := k.As128(); ok {
		if t.m128 == nil {
			t.m128 = New[id.ID128, V](t.capacity)
		}

		h, _ := t.m128.hash(t.m128.seed, f)

		return t.m128.set(h, &f, value)
	}

	if f, ok := k.As160(); ok {
		if t.m160 == nil {
			t.m160 = New[id.ID160, V](t.capacity)
		}

		h, _ := t.m160.hash(t.m160.seed, f)

		return t.m160.set(h, &f, value)
	}

	if f, ok := k.As256(); ok {
		if t.m256 == nil {
			t.m256 = New[id.ID256, V](t.capacity)
		}

		h, _ := t.m256.hash(t.m256.seed, f)

		return t.m256.set(h, &f, value)
	}

	old, had := t.zero, t.hasZero
	t.zero, t.hasZero = value, true

	return old, had
}

// Delete removes key, and returns its value and whether key was present. It
// drops the value, so the map refers to no value that it removed.
//
// # Allocation contract
//
// Zero alloc.
func (t *Typed[K, V]) Delete(key K) (V, bool) {
	k := struct{ id.ID }(key).ID

	if f, ok := k.As128(); ok && t.m128 != nil {
		h, _ := t.m128.hash(t.m128.seed, f)

		return t.m128.remove(h, &f)
	}

	if f, ok := k.As160(); ok && t.m160 != nil {
		h, _ := t.m160.hash(t.m160.seed, f)

		return t.m160.remove(h, &f)
	}

	if f, ok := k.As256(); ok && t.m256 != nil {
		h, _ := t.m256.hash(t.m256.seed, f)

		return t.m256.remove(h, &f)
	}

	var zero V

	if k.IsZero() {
		old, had := t.zero, t.hasZero
		t.zero, t.hasZero = zero, false

		return old, had
	}

	return zero, false
}

// Len returns the number of keys.
//
// # Allocation contract
//
// Zero alloc.
func (t *Typed[K, V]) Len() int {
	n := 0
	if t.hasZero {
		n++
	}

	if t.m128 != nil {
		n += t.m128.n
	}

	if t.m160 != nil {
		n += t.m160.n
	}

	if t.m256 != nil {
		n += t.m256.n
	}

	return n
}

// Clear removes every key, and keeps the slots of each Map. It drops the
// values, so the map refers to no value that it removed.
//
// # Allocation contract
//
// Zero alloc.
func (t *Typed[K, V]) Clear() {
	if t.m128 != nil {
		t.m128.Clear()
	}

	if t.m160 != nil {
		t.m160.Clear()
	}

	if t.m256 != nil {
		t.m256.Clear()
	}

	var zero V

	t.zero, t.hasZero = zero, false
}

// Clone returns a copy of t. Each Map of the copy is a [Map.Clone] of the
// Map of t, so Clone hashes no key. A write to the copy does not change t,
// and a write to t does not change the copy. Each value of the copy is an
// assignment of the value of t.
//
// # Allocation contract
//
// Clone allocates the copy, and the copy of each Map of t with its three
// arrays.
func (t *Typed[K, V]) Clone() *Typed[K, V] {
	c := *t
	if t.m128 != nil {
		c.m128 = t.m128.Clone()
	}

	if t.m160 != nil {
		c.m160 = t.m160.Clone()
	}

	if t.m256 != nil {
		c.m256 = t.m256.Clone()
	}

	return &c
}

// All returns an iterator over the keys and values. It yields the key whose
// identifier is [id.Zero] first, then the keys of 16, 20 and 32 bytes, each
// size in the order of [Map.All].
//
// The loop body may delete the key that it received, and the iteration
// still yields every other key once. Any other Set or Delete during the
// iteration may make it skip a key or yield a key twice.
//
// # Allocation contract
//
// Zero alloc for a range loop that the compiler inlines the iterator into.
func (t *Typed[K, V]) All() iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		if t.hasZero && !yield(K{}, t.zero) {
			return
		}

		if t.m128 != nil {
			for f, v := range t.m128.All() {
				if !yield(K{f.ID()}, v) {
					return
				}
			}
		}

		if t.m160 != nil {
			for f, v := range t.m160.All() {
				if !yield(K{f.ID()}, v) {
					return
				}
			}
		}

		if t.m256 != nil {
			for f, v := range t.m256.All() {
				if !yield(K{f.ID()}, v) {
					return
				}
			}
		}
	}
}
