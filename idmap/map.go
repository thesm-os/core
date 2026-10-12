// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package idmap

import (
	"hash/maphash"
	"iter"
	"math/bits"
	"slices"

	"go.thesmos.sh/core/id"
)

// minSlots is the number of slots of the smallest table of a [Map].
const minSlots = 8

// Map is a hash map from identifiers to values, for one goroutine.
//
// K is a fixed-size identifier of [Key], or a struct type of the caller
// whose one field embeds one, such as
//
//	type ShardID struct{ id.ID128 }
//
// [New] returns a map of the identifiers of Key. [NewTyped128],
// [NewTyped160] and [NewTyped256] return a map of the struct types. A key of
// a struct type converts to its identifier for the hash, so its map hashes
// and compares the same bytes as a map of the identifier.
//
// A Map keeps its keys in a table of a power of two of slots, with open
// addressing and linear probing. A slot has a tag byte, a key and a value,
// in three arrays. A lookup starts at the home slot of the hash and walks
// forward. It compares the key only in a slot whose tag matches, and it
// stops at an empty slot. A Delete moves each later key of its run back
// into the hole, unless the home slot of that key lies between the hole and
// the key. The table keeps no tombstones, so a workload of constant size
// never rehashes. The table is at most five eighths full. A Set of a new key
// into a full table doubles the slots and moves every key.
//
// The zero Map is not usable. New, NewTyped128, NewTyped160 and NewTyped256
// return an empty map.
//
// # Concurrency
//
// A Map is not safe for concurrent use. Any number of goroutines may call
// Get, Len, All and Clone on a map that no goroutine writes.
//
// # Allocation contract
//
// [New] and [Map.Clone] allocate the map and its three arrays. NewTyped128,
// NewTyped160 and NewTyped256 also allocate the hash function of the struct
// type. Get, Delete, Len, Clear and a range over All do not allocate. Set
// allocates the three arrays of a growth, and nothing otherwise.
type Map[K comparable, V any] struct {
	// hash is the hash function of K.
	hash func(seed, K) (uint64, words)

	// tags has one tag per slot.
	tags []uint8

	// keys and vals have the key and the value of each full slot.
	keys []K
	vals []V

	// seed is the pair of secret words of the hash.
	seed seed

	// mask is the number of slots minus 1. The home slot of a hash h is
	// h & mask.
	mask uint64

	// n is the number of keys. limit is the most keys of the table, five
	// eighths of its slots.
	n, limit int
}

// New returns an empty map of the identifiers K, with room for capacity
// keys, or for one key when capacity is below 1. The map does not grow
// before a Set of a new key finds the map with capacity keys. Its table has
// the least power of two of slots, and at least 8, whose five eighths are at
// least capacity.
//
// New panics as make does when the slots for capacity exceed the memory
// that make can allocate.
//
// # Allocation contract
//
// New allocates the map and its arrays of tags, keys and values.
func New[K Key, V any](capacity int) *Map[K, V] {
	return newMap[K, V](capacity, newHash[K]())
}

// NewTyped128 returns an empty map of the struct type K, whose one field
// embeds an [id.ID128]. The map hashes the ID128 of each key, and its table
// is the table that [New] allocates for capacity.
//
// NewTyped128 panics as make does when the slots for capacity exceed the
// memory that make can allocate.
//
// # Allocation contract
//
// NewTyped128 allocates the map, its arrays of tags, keys and values, and
// the hash function of K.
func NewTyped128[K ~struct{ id.ID128 }, V any](capacity int) *Map[K, V] {
	return newMap[K, V](capacity, func(s seed, k K) (uint64, words) {
		c := struct{ id.ID128 }(k)

		return hash128(s, &c.ID128)
	})
}

// NewTyped160 returns an empty map of the struct type K, whose one field
// embeds an [id.ID160]. The map hashes the ID160 of each key, and its table
// is the table that [New] allocates for capacity.
//
// NewTyped160 panics as make does when the slots for capacity exceed the
// memory that make can allocate.
//
// # Allocation contract
//
// NewTyped160 allocates the map, its arrays of tags, keys and values, and
// the hash function of K.
func NewTyped160[K ~struct{ id.ID160 }, V any](capacity int) *Map[K, V] {
	return newMap[K, V](capacity, func(s seed, k K) (uint64, words) {
		c := struct{ id.ID160 }(k)

		return hash160(s, &c.ID160)
	})
}

// NewTyped256 returns an empty map of the struct type K, whose one field
// embeds an [id.ID256]. The map hashes the ID256 of each key, and its table
// is the table that [New] allocates for capacity.
//
// NewTyped256 panics as make does when the slots for capacity exceed the
// memory that make can allocate.
//
// # Allocation contract
//
// NewTyped256 allocates the map, its arrays of tags, keys and values, and
// the hash function of K.
func NewTyped256[K ~struct{ id.ID256 }, V any](capacity int) *Map[K, V] {
	return newMap[K, V](capacity, func(s seed, k K) (uint64, words) {
		c := struct{ id.ID256 }(k)

		return hash256(s, &c.ID256)
	})
}

// newMap returns an empty map whose keys hash with hash, under a new seed.
// Its table has the least power of two of slots, and at least 8, whose five
// eighths are at least capacity, or 1 when capacity is below 1. Each word of
// the seed is the hash of the empty string under a new random seed of
// hash/maphash.
//
// # Allocation contract
//
// newMap allocates the map and its arrays of tags, keys and values.
func newMap[K comparable, V any](capacity int, hash func(seed, K) (uint64, words)) *Map[K, V] {
	// Without this line, a capacity below 1 gives bits.Len a value of 64
	// bits. The shift by 64 gives 0, and max gives minSlots as well.
	//dokimi:mutate-skip sbr-delete: without it, max gives minSlots for a capacity below 1 as well
	capacity = max(capacity, 1)

	// The least power of two of at least capacity needs at most one
	// doubling, and the check below makes it. A start from capacity+1 ends
	// with the same slots.
	//dokimi:mutate-skip aor: a start from the power of two of capacity+1 gives the same slots after the doubling check
	slots := max(minSlots, 1<<bits.Len(uint(capacity-1)))
	if slots/8*5 < capacity {
		slots *= 2
	}

	m := &Map[K, V]{
		hash: hash,
		seed: seed{maphash.String(maphash.MakeSeed(), ""), maphash.String(maphash.MakeSeed(), "")},
	}
	m.alloc(slots)

	return m
}

// Get returns the value of key, and reports whether key is present.
//
// # Allocation contract
//
// Zero alloc.
func (m *Map[K, V]) Get(key K) (V, bool) {
	// Get walks the run itself and does not call get. The compiler joins the
	// two returns of an inlined get, and Get then takes 7.0 ns in place of
	// 5.3 ns for a lookup in 10,000 keys.
	h, _ := m.hash(m.seed, key)
	tag := uint8(h>>tagShift) | fullTag

	for i := h & m.mask; ; i = (i + 1) & m.mask {
		if m.tags[i] == 0 {
			var zero V

			return zero, false
		}

		// The comparison of the tags spares a comparison of the keys in the
		// slots of other keys.
		//dokimi:mutate-skip ror-true: a slot of another tag has another key, which the comparison of the keys refuses
		if m.tags[i] == tag {
			if m.keys[i] == key {
				return m.vals[i], true
			}
		}
	}
}

// Set sets the value of key, and returns the previous value and whether
// key was present. A Set of a new key into a map that has as many keys as
// five eighths of its slots first doubles the slots and moves every key.
//
// # Allocation contract
//
// Zero alloc, except for the three arrays of a growth.
func (m *Map[K, V]) Set(key K, value V) (V, bool) {
	h, _ := m.hash(m.seed, key)
	tag := uint8(h>>tagShift) | fullTag

	i := h & m.mask
	for ; m.tags[i] != 0; i = (i + 1) & m.mask {
		//dokimi:mutate-skip ror-true: a slot of another tag has another key, which the comparison of the keys refuses
		if m.tags[i] == tag {
			if m.keys[i] == key {
				old := m.vals[i]
				m.vals[i] = value

				return old, true
			}
		}
	}

	if m.n == m.limit {
		m.grow()
		i = m.free(h)
	}

	m.tags[i], m.keys[i], m.vals[i] = tag, key, value
	m.n++

	var zero V

	return zero, false
}

// Delete removes key, and returns its value and whether key was present.
// Each later key of the run of key moves back into the hole that key
// leaves, unless the home slot of that key lies between the hole and the
// key, so every key remains reachable from its home slot without an empty
// slot on the way. The slot that the last move empties drops its value, so
// the map refers to no value that it removed.
//
// # Allocation contract
//
// Zero alloc.
func (m *Map[K, V]) Delete(key K) (V, bool) {
	h, _ := m.hash(m.seed, key)

	return m.remove(h, &key)
}

// Len returns the number of keys.
//
// # Allocation contract
//
// Zero alloc.
func (m *Map[K, V]) Len() int {
	return m.n
}

// Clear removes every key, and keeps the slots of the table. It drops the
// values, so the map refers to no value that it removed.
//
// # Allocation contract
//
// Zero alloc.
func (m *Map[K, V]) Clear() {
	clear(m.tags)
	clear(m.vals)
	m.n = 0
}

// Clone returns a copy of m. The copy has the slots and the seed of m, so
// every key keeps its slot and Clone hashes no key: it copies the three
// arrays. Clone takes time in proportion to the slots of m. A write to the
// copy does not change m, and a write to m does not change the copy. Each
// value of the copy is an assignment of the value of m, so a pointer in
// the copy points to the same object as the pointer in m.
//
// # Allocation contract
//
// Clone allocates the copy and its three arrays.
func (m *Map[K, V]) Clone() *Map[K, V] {
	c := *m
	c.tags = slices.Clone(m.tags)
	c.keys = slices.Clone(m.keys)
	c.vals = slices.Clone(m.vals)

	return &c
}

// All returns an iterator over the keys and values, in the order of their
// slots.
//
// The iteration walks the slots backward from an empty slot. A Delete
// moves keys only from slots after the hole up to the next empty slot, and
// the iteration has visited those slots. The loop body may therefore
// delete the key that it received, and the iteration still yields every
// other key once. Any other Set or Delete during the iteration may make it
// skip a key or yield a key twice.
//
// # Allocation contract
//
// Zero alloc for a range loop that the compiler inlines the iterator into.
func (m *Map[K, V]) All() iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		mask := len(m.tags) - 1

		// A table is at most five eighths full, so it has an empty slot.
		empty := 0
		for m.tags[empty] != 0 {
			empty++
		}

		for i := (empty - 1) & mask; i != empty; i = (i - 1) & mask {
			if m.tags[i] != 0 && !yield(m.keys[i], m.vals[i]) {
				return
			}
		}
	}
}

// get returns the value of *key, whose hash is h, and reports whether *key
// is present. A [Typed] computes h with the hash function of m, and calls get
// in place of Get with a pointer to the identifier of its key.
//
// get takes the key by pointer. The compiler inlines get, and the comparison
// of an inlined array parameter assembles the key from its single bytes. The
// comparison through the pointer loads the key as two to four words.
func (m *Map[K, V]) get(h uint64, key *K) (V, bool) {
	tag := uint8(h>>tagShift) | fullTag

	for i := h & m.mask; ; i = (i + 1) & m.mask {
		if m.tags[i] == 0 {
			var zero V

			return zero, false
		}

		// The comparison of the tags spares a comparison of the keys in the
		// slots of other keys.
		//dokimi:mutate-skip ror-true: a slot of another tag has another key, which the comparison of the keys refuses
		if m.tags[i] == tag {
			if m.keys[i] == *key {
				return m.vals[i], true
			}
		}
	}
}

// set sets the value of *key, whose hash is h, as Set describes. A [Typed]
// calls it in place of Set. It takes the key by pointer for the reason that
// get states.
func (m *Map[K, V]) set(h uint64, key *K, value V) (V, bool) {
	tag := uint8(h>>tagShift) | fullTag

	i := h & m.mask
	for ; m.tags[i] != 0; i = (i + 1) & m.mask {
		//dokimi:mutate-skip ror-true: a slot of another tag has another key, which the comparison of the keys refuses
		if m.tags[i] == tag {
			if m.keys[i] == *key {
				old := m.vals[i]
				m.vals[i] = value

				return old, true
			}
		}
	}

	if m.n == m.limit {
		m.grow()
		i = m.free(h)
	}

	m.tags[i], m.keys[i], m.vals[i] = tag, *key, value
	m.n++

	var zero V

	return zero, false
}

// remove removes *key, whose hash is h, as Delete describes. The keys that
// it moves back hash with the hash function of m. It takes the key by
// pointer for the reason that get states.
func (m *Map[K, V]) remove(h uint64, key *K) (V, bool) {
	tag := uint8(h>>tagShift) | fullTag

	i := h & m.mask
	for m.tags[i] != tag || m.keys[i] != *key {
		if m.tags[i] == 0 {
			var zero V

			return zero, false
		}

		i = (i + 1) & m.mask
	}

	old := m.vals[i]

	for j := (i + 1) & m.mask; m.tags[j] != 0; j = (j + 1) & m.mask {
		hj, _ := m.hash(m.seed, m.keys[j])

		// (j-hj)&m.mask is the distance from the home slot of the key at j
		// to j. The key moves into the hole at i unless its home slot lies
		// after i, because a walk from that home slot does not pass i.
		if (j-hj)&m.mask >= (j-i)&m.mask {
			m.tags[i], m.keys[i], m.vals[i] = m.tags[j], m.keys[j], m.vals[j]
			i = j
		}
	}

	var zero V

	m.tags[i], m.vals[i] = 0, zero
	m.n--

	return old, true
}

// alloc gives m empty arrays of slots slots, and the mask and the limit of
// that table. slots is a power of two of at least minSlots.
func (m *Map[K, V]) alloc(slots int) {
	m.tags = make([]uint8, slots)
	m.keys = make([]K, slots)
	m.vals = make([]V, slots)

	//nolint:gosec // G115: slots is a positive power of two, so slots-1 converts exactly
	m.mask = uint64(slots - 1)
	m.limit = slots / 8 * 5
}

// free returns the first empty slot of the walk from the home slot of the
// hash h. The table has an empty slot.
func (m *Map[K, V]) free(h uint64) uint64 {
	i := h & m.mask
	for m.tags[i] != 0 {
		i = (i + 1) & m.mask
	}

	return i
}

// grow moves every key into a table of twice the slots. A moved key keeps
// its tag, because the hash of its key does not change.
func (m *Map[K, V]) grow() {
	tags, keys, vals := m.tags, m.keys, m.vals
	m.alloc(2 * len(tags))

	for i, tag := range tags {
		// An empty slot has the tag 0 and a zero value. A move of it writes an
		// empty slot, and a later Set overwrites that slot.
		//dokimi:mutate-skip ror-true: a move of an empty slot writes an empty slot of the new table
		if tag != 0 {
			h, _ := m.hash(m.seed, keys[i])
			j := m.free(h)
			m.tags[j], m.keys[j], m.vals[j] = tag, keys[i], vals[i]
		}
	}
}
