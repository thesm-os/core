// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package btree

import (
	"cmp"
	"iter"
)

// Set is an ordered set of keys, ordered by [cmp.Compare]. Its zero value
// is an empty set, ready to use. A Set is a B+ tree whose values take no
// space, because an array of struct{} has size 0.
//
// # Concurrency
//
// Not safe for concurrent use. Any number of goroutines may read a set
// that no goroutine writes. A clone is a separate set, so one goroutine
// may read a clone while another writes the original.
type Set[K cmp.Ordered] struct {
	t tree[K, struct{}, natural[K]]
}

// Add adds key, and reports whether key was absent.
//
// # Allocation contract
//
// As [Map.Set].
func (s *Set[K]) Add(key K) bool {
	_, present := s.t.set(key, struct{}{})

	return !present
}

// Has reports whether key is present.
//
// # Allocation contract
//
// Zero alloc.
func (s *Set[K]) Has(key K) bool {
	_, ok := s.t.get(key)

	return ok
}

// Delete removes key, and reports whether key was present.
//
// # Allocation contract
//
// As [Map.Delete].
func (s *Set[K]) Delete(key K) bool {
	_, present := s.t.delete(key)

	return present
}

// DeleteRange removes every key in [lo, hi), and returns the number of
// keys it removed. It removes nothing when hi sorts at or before lo.
//
// # Allocation contract
//
// As [Map.Delete].
func (s *Set[K]) DeleteRange(lo, hi K) int {
	return s.t.deleteRange(lo, hi)
}

// Len returns the number of keys in O(1).
func (s *Set[K]) Len() int {
	return s.t.len
}

// Clear removes every key. The set keeps its free lists, and every node
// that no clone shares becomes garbage.
//
// # Allocation contract
//
// Zero alloc.
func (s *Set[K]) Clear() {
	s.t.clear()
}

// Min returns the smallest key, and reports whether the set has a key.
//
// # Allocation contract
//
// Zero alloc.
func (s *Set[K]) Min() (K, bool) {
	k, _, ok := s.t.min()

	return k, ok
}

// Max returns the largest key, and reports whether the set has a key.
//
// # Allocation contract
//
// Zero alloc.
func (s *Set[K]) Max() (K, bool) {
	k, _, ok := s.t.max()

	return k, ok
}

// Floor returns the largest key at or before key, and reports whether
// there is one.
//
// # Allocation contract
//
// Zero alloc.
func (s *Set[K]) Floor(key K) (K, bool) {
	k, _, ok := s.t.floor(key)

	return k, ok
}

// Ceil returns the smallest key at or after key, and reports whether there
// is one.
//
// # Allocation contract
//
// Zero alloc.
func (s *Set[K]) Ceil(key K) (K, bool) {
	k, _, ok := s.t.ceil(key)

	return k, ok
}

// PopMin removes the smallest key, and returns it and whether the set had
// a key.
//
// # Allocation contract
//
// As [Map.Delete].
func (s *Set[K]) PopMin() (K, bool) {
	k, _, ok := s.t.popMin()

	return k, ok
}

// PopMax removes the largest key, and returns it and whether the set had
// a key.
//
// # Allocation contract
//
// As [Map.Delete].
func (s *Set[K]) PopMax() (K, bool) {
	k, _, ok := s.t.popMax()

	return k, ok
}

// At returns the key at index i in key order, and reports whether
// 0 <= i < Len. It runs in O(log n).
//
// # Allocation contract
//
// Zero alloc.
func (s *Set[K]) At(i int) (K, bool) {
	k, _, ok := s.t.at(i)

	return k, ok
}

// Rank returns the number of keys that sort before key. It runs in
// O(log n).
//
// # Allocation contract
//
// Zero alloc.
func (s *Set[K]) Rank(key K) int {
	return s.t.rank(key)
}

// All returns an iterator over the keys in ascending order. A write during
// the iteration has the effect it has on [Map.All].
//
// # Allocation contract
//
// As [Map.All].
func (s *Set[K]) All() iter.Seq[K] {
	return func(yield func(K) bool) {
		s.t.walk(func(k K, _ struct{}) bool { return yield(k) })
	}
}

// Backward returns an iterator over the keys in descending order. A write
// during the iteration has the effect it has on [Map.Backward].
//
// # Allocation contract
//
// As [Map.All].
func (s *Set[K]) Backward() iter.Seq[K] {
	return func(yield func(K) bool) {
		s.t.walkBack(func(k K, _ struct{}) bool { return yield(k) })
	}
}

// Range returns an iterator over the keys in [lo, hi) in ascending order.
// It yields nothing when hi sorts at or before lo. A write during the
// iteration has the effect it has on [Map.All].
//
// # Allocation contract
//
// As [Map.All].
func (s *Set[K]) Range(lo, hi K) iter.Seq[K] {
	return func(yield func(K) bool) {
		s.t.walkRange(lo, hi, func(k K, _ struct{}) bool { return yield(k) })
	}
}

// Ascend returns an iterator over the keys at or after from in ascending
// order. A write during the iteration has the effect it has on [Map.All].
//
// # Allocation contract
//
// As [Map.All].
func (s *Set[K]) Ascend(from K) iter.Seq[K] {
	return func(yield func(K) bool) {
		s.t.walkFrom(from, func(k K, _ struct{}) bool { return yield(k) })
	}
}

// Descend returns an iterator over the keys at or before from in
// descending order. A write during the iteration has the effect it has on
// [Map.Backward].
//
// # Allocation contract
//
// As [Map.All].
func (s *Set[K]) Descend(from K) iter.Seq[K] {
	return func(yield func(K) bool) {
		s.t.walkBackFrom(from, func(k K, _ struct{}) bool { return yield(k) })
	}
}

// Clone returns a set with the same keys in O(1). Both sets share every
// node until one of them writes to a node, which the write copies.
//
// # Allocation contract
//
// One allocation, the new set.
func (s *Set[K]) Clone() *Set[K] {
	return &Set[K]{t: s.t.clone()}
}
