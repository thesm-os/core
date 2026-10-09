// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package btree

import (
	"iter"
	"slices"
)

// MapFunc is an ordered map from keys to values, ordered by the function
// that [NewMapFunc] receives. Each method has the contract of the method
// of [Map] with the same name, with that function in place of
// [cmp.Compare]. A search calls the function at most 7 times for each node
// that it visits.
//
// The zero MapFunc has no order, and a method that compares two keys
// panics on it. A caller creates a MapFunc with NewMapFunc.
//
// # Concurrency
//
// Not safe for concurrent use. Any number of goroutines may read a map
// that no goroutine writes. A clone is a separate map, so one goroutine
// may read a clone while another writes the original.
type MapFunc[K, V any] struct {
	t tree[K, V, custom[K]]
}

// NewMapFunc returns an empty map ordered by cmp. cmp returns a negative
// number when a sorts before b, zero when a and b are the same key, and a
// positive number when a sorts after b. It must define a total order: a
// function that does not makes lookups miss keys and iterators yield keys
// out of order, and the map does not detect it.
//
// NewMapFunc panics when cmp is nil.
func NewMapFunc[K, V any](cmp func(a, b K) int) *MapFunc[K, V] {
	if cmp == nil {
		//nolint:forbidigo // a map without an order is a programmer error at construction
		panic("btree: NewMapFunc with a nil comparison function")
	}

	return &MapFunc[K, V]{t: tree[K, V, custom[K]]{order: cmp}}
}

// Get returns the value of key, and reports whether key is present. See
// [Map.Get].
func (m *MapFunc[K, V]) Get(key K) (V, bool) {
	return m.t.get(key)
}

// Has reports whether key is present. See [Map.Has].
func (m *MapFunc[K, V]) Has(key K) bool {
	_, ok := m.t.get(key)

	return ok
}

// Set sets the value of key, and returns the previous value and whether
// key was present. See [Map.Set].
func (m *MapFunc[K, V]) Set(key K, value V) (V, bool) {
	return m.t.set(key, value)
}

// Update sets the value of key to fn(old, exists), and returns the new
// value. See [Map.Update].
func (m *MapFunc[K, V]) Update(key K, fn func(old V, exists bool) V) V {
	return m.t.update(key, fn)
}

// Delete removes key, and returns its value and whether key was present.
// See [Map.Delete].
func (m *MapFunc[K, V]) Delete(key K) (V, bool) {
	return m.t.delete(key)
}

// DeleteRange removes every key in [lo, hi), and returns the number of
// keys it removed. See [Map.DeleteRange].
func (m *MapFunc[K, V]) DeleteRange(lo, hi K) int {
	return m.t.deleteRange(lo, hi)
}

// Len returns the number of keys in O(1).
func (m *MapFunc[K, V]) Len() int {
	return m.t.len
}

// Clear removes every key. See [Map.Clear].
func (m *MapFunc[K, V]) Clear() {
	m.t.clear()
}

// Reset removes every key, and keeps the nodes of the map for the inserts
// that follow. See [Map.Reset].
func (m *MapFunc[K, V]) Reset() {
	m.t.reset()
}

// Min returns the smallest key and its value, and reports whether the map
// has a key. See [Map.Min].
func (m *MapFunc[K, V]) Min() (K, V, bool) {
	return m.t.min()
}

// Max returns the largest key and its value, and reports whether the map
// has a key. See [Map.Max].
func (m *MapFunc[K, V]) Max() (K, V, bool) {
	return m.t.max()
}

// Floor returns the largest key at or before key and its value, and
// reports whether there is one. See [Map.Floor].
func (m *MapFunc[K, V]) Floor(key K) (K, V, bool) {
	return m.t.floor(key)
}

// Ceil returns the smallest key at or after key and its value, and reports
// whether there is one. See [Map.Ceil].
func (m *MapFunc[K, V]) Ceil(key K) (K, V, bool) {
	return m.t.ceil(key)
}

// PopMin removes the smallest key, and returns it with its value and
// whether the map had a key. See [Map.PopMin].
func (m *MapFunc[K, V]) PopMin() (K, V, bool) {
	return m.t.popMin()
}

// PopMax removes the largest key, and returns it with its value and
// whether the map had a key. See [Map.PopMax].
func (m *MapFunc[K, V]) PopMax() (K, V, bool) {
	return m.t.popMax()
}

// At returns the key at index i in key order and its value, and reports
// whether 0 <= i < Len. See [Map.At].
func (m *MapFunc[K, V]) At(i int) (K, V, bool) {
	return m.t.at(i)
}

// Rank returns the number of keys that sort before key. See [Map.Rank].
func (m *MapFunc[K, V]) Rank(key K) int {
	return m.t.rank(key)
}

// All returns an iterator over the keys and values in ascending key order.
// See [Map.All].
func (m *MapFunc[K, V]) All() iter.Seq2[K, V] {
	return m.t.walk
}

// Keys returns an iterator over the keys in ascending order. See
// [Map.Keys].
func (m *MapFunc[K, V]) Keys() iter.Seq[K] {
	return func(yield func(K) bool) {
		m.t.walk(func(k K, _ V) bool { return yield(k) })
	}
}

// Values returns an iterator over the values in ascending key order. See
// [Map.Values].
func (m *MapFunc[K, V]) Values() iter.Seq[V] {
	return func(yield func(V) bool) {
		m.t.walk(func(_ K, v V) bool { return yield(v) })
	}
}

// Backward returns an iterator over the keys and values in descending key
// order. See [Map.Backward].
func (m *MapFunc[K, V]) Backward() iter.Seq2[K, V] {
	return m.t.walkBack
}

// Range returns an iterator over the keys in [lo, hi) and their values, in
// ascending key order. See [Map.Range].
func (m *MapFunc[K, V]) Range(lo, hi K) iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		m.t.walkRange(lo, hi, yield)
	}
}

// Ascend returns an iterator over the keys at or after from and their
// values, in ascending key order. See [Map.Ascend].
func (m *MapFunc[K, V]) Ascend(from K) iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		m.t.walkFrom(from, yield)
	}
}

// Descend returns an iterator over the keys at or before from and their
// values, in descending key order. See [Map.Descend].
func (m *MapFunc[K, V]) Descend(from K) iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		m.t.walkBackFrom(from, yield)
	}
}

// Clone returns a map with the same keys, values and order in O(1). See
// [Map.Clone].
func (m *MapFunc[K, V]) Clone() *MapFunc[K, V] {
	return &MapFunc[K, V]{t: m.t.clone()}
}

// custom orders keys by a comparison function, which returns a negative
// number, zero or a positive number as a sorts before, at or after b.
type custom[K any] func(a, b K) int

// search returns the index of the first of keys at or after key, and
// reports whether that key equals key.
func (c custom[K]) search(keys []K, key K) (int, bool) {
	return slices.BinarySearchFunc(keys, key, c)
}

// route returns the number of keys at or before key.
func (c custom[K]) route(keys []K, key K) int {
	i, found := slices.BinarySearchFunc(keys, key, c)
	if found {
		i++
	}

	return i
}

// less reports whether a sorts before b.
func (c custom[K]) less(a, b K) bool {
	return c(a, b) < 0
}
