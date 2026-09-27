// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package btree

import (
	"cmp"
	"iter"
)

// Map is an ordered map from keys to values, ordered by [cmp.Compare]. Its
// zero value is an empty map, ready to use.
//
// # Concurrency
//
// Not safe for concurrent use. Any number of goroutines may read a map
// that no goroutine writes. A clone is a separate map, so one goroutine
// may read a clone while another writes the original.
type Map[K cmp.Ordered, V any] struct {
	t tree[K, V, natural[K]]
}

// Get returns the value of key, and reports whether key is present.
//
// # Allocation contract
//
// Zero alloc.
func (m *Map[K, V]) Get(key K) (V, bool) {
	return m.t.get(key)
}

// Has reports whether key is present.
//
// # Allocation contract
//
// Zero alloc.
func (m *Map[K, V]) Has(key K) bool {
	_, ok := m.t.get(key)

	return ok
}

// Set sets the value of key, and returns the previous value and whether
// key was present. When the map contains a key equal to key, Set replaces
// its value and keeps the stored key, as a Go map does.
//
// # Allocation contract
//
// Zero alloc, plus 1 allocation for each leaf split and 2 for each split
// of an internal node that finds the map's free list empty. After
// [Map.Clone], a write also allocates 1 object for each leaf and 2 for
// each internal node on its path that it copies for the first time.
func (m *Map[K, V]) Set(key K, value V) (V, bool) {
	return m.t.set(key, value)
}

// Update sets the value of key to fn(old, exists), where old is the value
// of key and exists reports whether key is present, and returns the new
// value. It descends the tree once and calls fn at the leaf. When fn
// writes to the map, Update stores the result with a second descent.
//
// # Allocation contract
//
// As [Map.Set].
func (m *Map[K, V]) Update(key K, fn func(old V, exists bool) V) V {
	return m.t.update(key, fn)
}

// Delete removes key, and returns its value and whether key was present.
//
// # Allocation contract
//
// Zero alloc, except after [Map.Clone]: then as [Map.Set].
func (m *Map[K, V]) Delete(key K) (V, bool) {
	return m.t.delete(key)
}

// DeleteRange removes every key in [lo, hi), and returns the number of
// keys it removed. It removes nothing when hi sorts at or before lo. It
// descends the tree once for each key it removes, and once more each time
// the next key of the range starts a new leaf.
//
// # Allocation contract
//
// As [Map.Delete].
func (m *Map[K, V]) DeleteRange(lo, hi K) int {
	return m.t.deleteRange(lo, hi)
}

// Len returns the number of keys in O(1).
func (m *Map[K, V]) Len() int {
	return m.t.len
}

// Clear removes every key. The map keeps its free lists, and every node
// that no clone shares becomes garbage.
//
// # Allocation contract
//
// Zero alloc.
func (m *Map[K, V]) Clear() {
	m.t.clear()
}

// Min returns the smallest key and its value, and reports whether the map
// has a key.
//
// # Allocation contract
//
// Zero alloc.
func (m *Map[K, V]) Min() (K, V, bool) {
	return m.t.min()
}

// Max returns the largest key and its value, and reports whether the map
// has a key.
//
// # Allocation contract
//
// Zero alloc.
func (m *Map[K, V]) Max() (K, V, bool) {
	return m.t.max()
}

// Floor returns the largest key at or before key and its value, and
// reports whether there is one.
//
// # Allocation contract
//
// Zero alloc.
func (m *Map[K, V]) Floor(key K) (K, V, bool) {
	return m.t.floor(key)
}

// Ceil returns the smallest key at or after key and its value, and reports
// whether there is one.
//
// # Allocation contract
//
// Zero alloc.
func (m *Map[K, V]) Ceil(key K) (K, V, bool) {
	return m.t.ceil(key)
}

// PopMin removes the smallest key, and returns it with its value and
// whether the map had a key.
//
// # Allocation contract
//
// As [Map.Delete].
func (m *Map[K, V]) PopMin() (K, V, bool) {
	return m.t.popMin()
}

// PopMax removes the largest key, and returns it with its value and
// whether the map had a key.
//
// # Allocation contract
//
// As [Map.Delete].
func (m *Map[K, V]) PopMax() (K, V, bool) {
	return m.t.popMax()
}

// At returns the key at index i in key order and its value, and reports
// whether 0 <= i < Len. It descends once and skips the items of the
// children before the path at each level, so it runs in O(log n).
//
// # Allocation contract
//
// Zero alloc.
func (m *Map[K, V]) At(i int) (K, V, bool) {
	return m.t.at(i)
}

// Rank returns the number of keys that sort before key. It descends once
// and adds the item counts of the children before the path at each level,
// so it runs in O(log n).
//
// # Allocation contract
//
// Zero alloc.
func (m *Map[K, V]) Rank(key K) int {
	return m.t.rank(key)
}

// All returns an iterator over the keys and values in ascending key order.
//
// A write to the map during the iteration does not end it. After a write,
// the iteration continues at the first key after the last key it yielded,
// so it yields each key at most once and in order. It yields a key that a
// write inserts after that key, and does not yield a key that a write
// deletes before the iteration gets to it.
//
// # Allocation contract
//
// Zero alloc for a range loop that the compiler inlines the iterator into.
func (m *Map[K, V]) All() iter.Seq2[K, V] {
	return m.t.walk
}

// Keys returns an iterator over the keys in ascending order. A write
// during the iteration has the effect it has on [Map.All].
//
// # Allocation contract
//
// As [Map.All].
func (m *Map[K, V]) Keys() iter.Seq[K] {
	return func(yield func(K) bool) {
		m.t.walk(func(k K, _ V) bool { return yield(k) })
	}
}

// Values returns an iterator over the values in ascending key order. A
// write during the iteration has the effect it has on [Map.All].
//
// # Allocation contract
//
// As [Map.All].
func (m *Map[K, V]) Values() iter.Seq[V] {
	return func(yield func(V) bool) {
		m.t.walk(func(_ K, v V) bool { return yield(v) })
	}
}

// Backward returns an iterator over the keys and values in descending key
// order. After a write during the iteration, the iteration continues at
// the last key before the last key it yielded.
//
// # Allocation contract
//
// As [Map.All].
func (m *Map[K, V]) Backward() iter.Seq2[K, V] {
	return m.t.walkBack
}

// Range returns an iterator over the keys in [lo, hi) and their values, in
// ascending key order. It yields nothing when hi sorts at or before lo. A
// write during the iteration has the effect it has on [Map.All].
//
// # Allocation contract
//
// As [Map.All].
func (m *Map[K, V]) Range(lo, hi K) iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		m.t.walkRange(lo, hi, yield)
	}
}

// Ascend returns an iterator over the keys at or after from and their
// values, in ascending key order. A write during the iteration has the
// effect it has on [Map.All].
//
// # Allocation contract
//
// As [Map.All].
func (m *Map[K, V]) Ascend(from K) iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		m.t.walkFrom(from, yield)
	}
}

// Descend returns an iterator over the keys at or before from and their
// values, in descending key order. A write during the iteration has the
// effect it has on [Map.Backward].
//
// # Allocation contract
//
// As [Map.All].
func (m *Map[K, V]) Descend(from K) iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		m.t.walkBackFrom(from, yield)
	}
}

// Clone returns a map with the same keys and values in O(1). Both maps
// share every node until one of them writes to a node, which the write
// copies. Each map then allocates the nodes that its writes copy.
//
// # Allocation contract
//
// One allocation, the new map.
func (m *Map[K, V]) Clone() *Map[K, V] {
	return &Map[K, V]{t: m.t.clone()}
}

// natural orders keys by [cmp.Compare], and compares them with [cmp.Less].
// For an integer key, cmp.Less compiles to one comparison instruction.
//
// search and route contain the loop of [slices.BinarySearch]. The compiler
// does not inline slices.BinarySearch into their GC shape instances, and
// the second call on every level of a descent made Map.Get 4% slower.
type natural[K cmp.Ordered] struct{}

// search returns the index of the first of keys at or after key, and
// reports whether that key equals key.
func (natural[K]) search(keys []K, key K) (int, bool) {
	lo, hi := 0, len(keys)
	for lo < hi {
		h := int(uint(lo+hi) >> 1)
		if cmp.Less(keys[h], key) {
			lo = h + 1
		} else {
			hi = h
		}
	}

	return lo, lo < len(keys) && !cmp.Less(key, keys[lo])
}

// route returns the number of keys at or before key.
func (natural[K]) route(keys []K, key K) int {
	lo, hi := 0, len(keys)
	for lo < hi {
		h := int(uint(lo+hi) >> 1)
		if cmp.Less(key, keys[h]) {
			hi = h
		} else {
			lo = h + 1
		}
	}

	return lo
}

// less reports whether a sorts before b.
func (natural[K]) less(a, b K) bool {
	return cmp.Less(a, b)
}
