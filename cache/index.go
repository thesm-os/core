// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cache

import "sync/atomic"

// minSlots is the number of slots of a new index, and the least number of
// slots of a grown one.
const minSlots = 16

// index is a hash table of entries with open addressing and linear
// probing, whose lookups take no lock.
//
// A slot is nil until an entry first takes it, contains the entry while
// the entry is in the cache, and contains the cache's tomb once its entry
// leaves. A probe starts at the slot of the hash and stops at a nil slot,
// so it passes every entry of its key and every tomb in between.
//
// # Concurrency
//
// A lookup loads each slot atomically and takes no lock. A writer locks
// the cache's mutex, fills an entry before the atomic store that publishes
// it, and replaces a removed entry with the tomb by another atomic store.
// A lookup that loads an entry therefore reads the fields that the writer
// filled. A writer that needs more slots builds a new index and publishes
// it through the cache's index pointer. No writer changes the old index
// again, so a lookup that loaded it probes an unchanging table.
//
// # Allocation contract
//
// find, insert and remove do not allocate. grow allocates the new index
// and its slots.
type index[K comparable, V any] struct {
	// slots contains the entries, a power of two of them.
	slots []atomic.Pointer[entry[K, V]]

	// mask is the number of slots minus 1, which maps a hash to a slot.
	mask uint64

	// live counts the slots that contain an entry, and used the slots
	// that contain an entry or the tomb. Only a writer that has locked the
	// cache's mutex reads or writes them.
	live, used int
}

// newIndex returns an empty index of n slots. n is a power of two.
func newIndex[K comparable, V any](n int) *index[K, V] {
	//nolint:gosec // G115: n is a positive power of two, so it converts exactly
	return &index[K, V]{slots: make([]atomic.Pointer[entry[K, V]], n), mask: uint64(n) - 1}
}

// find returns the entry of key k, whose hash is h, or nil when the index
// has none. A slot that contains tomb contains no entry. The probe visits
// each slot at most once, so find returns after len(slots) loads at most.
// It takes no lock.
func (ix *index[K, V]) find(k K, h uint64, tomb *entry[K, V]) *entry[K, V] {
	i := h & ix.mask
	for range len(ix.slots) {
		e := ix.slots[i].Load()
		if e == nil {
			return nil
		}

		if e != tomb && e.hash == h && e.key == k {
			return e
		}

		i = (i + 1) & ix.mask
	}

	return nil
}

// full reports whether one more entry would put entries or tombs in more
// than three quarters of the slots. The caller grows a full index before
// an insert, so every probe ends at a nil slot.
func (ix *index[K, V]) full() bool {
	return 4*(ix.used+1) > 3*len(ix.slots)
}

// insert stores e, an entry whose key the index does not contain, in the
// first slot of its probe that contains tomb, or in the nil slot that ends
// the probe. The index is not full. The caller has locked the mutex.
func (ix *index[K, V]) insert(e, tomb *entry[K, V]) {
	i := e.hash & ix.mask
	for range len(ix.slots) {
		s := ix.slots[i].Load()
		if s == nil {
			ix.used++

			break
		}

		if s == tomb {
			break
		}

		i = (i + 1) & ix.mask
	}

	ix.slots[i].Store(e)
	ix.live++
}

// remove replaces e in its slot with tomb. The index contains e, and the
// caller has locked the mutex. The probe of e's hash passes no nil slot
// before the slot of e, because no slot returns to nil, so remove stops at
// the first slot that contains e or nil. The slot remains used, so the
// probes of other keys still pass it.
func (ix *index[K, V]) remove(e, tomb *entry[K, V]) {
	i := e.hash & ix.mask
	for range len(ix.slots) {
		if s := ix.slots[i].Load(); s == e || s == nil {
			break
		}

		i = (i + 1) & ix.mask
	}

	if ix.slots[i].Load() == e {
		ix.slots[i].Store(tomb)
		ix.live--
	}
}

// grow returns a new index with the entries of ix and without its tombs,
// of the smallest power of two of slots that is at least minSlots and at
// least twice the number of entries plus one. A grown index is at most
// half full, so it takes as many inserts again before it is full. The
// caller has locked the mutex, and publishes the new index.
func (ix *index[K, V]) grow(tomb *entry[K, V]) *index[K, V] {
	n := minSlots
	for n < 2*(ix.live+1) {
		n *= 2
	}

	nx := newIndex[K, V](n)
	for i := range ix.slots {
		if e := ix.slots[i].Load(); e != nil && e != tomb {
			nx.insert(e, tomb)
		}
	}

	return nx
}
