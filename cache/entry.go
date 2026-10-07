// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"sync/atomic"
	"time"
)

// maxFreq is the largest number of hits that an entry counts between two
// examinations by the evictor. S3-FIFO counts to 3, so an entry of the
// main queue that readers keep hitting survives three examinations
// without a new hit.
const maxFreq = 3

// removed is the bit of an entry's state that marks an entry that has left
// the cache. The bits below it count the pins of the entry, so a state of
// exactly removed is an entry that has left and has no pin.
const removed = 1 << 62

// entry is one key and value of a [Cache].
//
// The fields key, value, expires, hash and cost do not change once the
// entry is in the index, so a lookup reads them without the lock: the
// writer fills them before the atomic store that publishes the entry. The
// fields prev, next and queue are read and written only by the holder of
// the cache's lock. state and freq are atomic, and both lookups and the
// evictor change them.
//
// # Allocation contract
//
// [Cache.Set] allocates one entry per call. No code reuses an entry, so a
// lookup that holds a pointer to an entry that has left reads its own key
// and value, never those of another entry.
type entry[K comparable, V any] struct {
	key     K
	value   V
	expires time.Time

	// prev links the entry to the next older entry of its queue, and next
	// to the next newer one. Both are nil outside a queue.
	prev, next *entry[K, V]

	// hash is the hash of key under the cache's seed.
	hash uint64

	// cost is the cost of the entry, at least 0.
	cost int64

	// state counts the pins of the entry, and has the removed bit set once
	// the entry has left the cache.
	state atomic.Int64

	// freq counts the hits of the entry since the evictor last examined
	// it, from 0 to maxFreq.
	freq atomic.Int32

	// queue is the queue that contains the entry.
	queue queueID
}

// touch counts a hit of e, up to maxFreq, with one compare-and-swap. A hit
// on an entry at maxFreq only loads freq, so the goroutines that hit a hot
// entry do not write to its memory. Two hits at the same time can count
// once, because S3-FIFO needs to know whether an entry was hit, not how
// often.
func (e *entry[K, V]) touch() {
	if f := e.freq.Load(); f < maxFreq {
		e.freq.CompareAndSwap(f, f+1)
	}
}

// expired reports whether e has expired at now: whether it has an expiry
// time, and now is at or after it.
func (e *entry[K, V]) expired(now time.Time) bool {
	return !e.expires.IsZero() && !now.Before(e.expires)
}

// pin adds a pin to e, and reports false when e has left the cache. It
// retries its compare-and-swap while other pins and unpins change the
// count, and fails at once when the removed bit is set, so no pin follows
// the removal of an entry.
func (e *entry[K, V]) pin() bool {
	for {
		s := e.state.Load()
		if s&removed != 0 {
			return false
		}

		if e.state.CompareAndSwap(s, s+1) {
			return true
		}
	}
}

// unpin removes a pin from e, which has one, and reports whether e has
// left the cache and has no pin left, so that the caller passes it to the
// callback. Exactly one of leave, evict and unpin reports an entry.
func (e *entry[K, V]) unpin() bool {
	return e.state.Add(-1) == removed
}

// leave marks e as having left the cache, and reports whether e had no
// pin, so that the caller passes it to the callback. When e has pins, the
// last unpin reports it instead. The caller calls leave once per entry,
// after it took the entry out of the index.
func (e *entry[K, V]) leave() bool {
	return e.state.Or(removed) == 0
}

// evict marks e as having left the cache when it has no pin, and reports
// whether it did. A pin that races with it either comes first, and evict
// fails, or fails because e has left, so the evictor never removes a
// pinned entry.
func (e *entry[K, V]) evict() bool {
	return e.state.CompareAndSwap(0, removed)
}

// victims collects the entries that leave the cache while the mutex is
// locked, so that their callbacks run after the mutex is unlocked.
//
// # Allocation contract
//
// The first eight entries go into an array of the victims value, which a
// caller keeps on its stack, so a Set that evicts at most eight entries
// does not allocate for them. Each entry after the eighth appends to more.
type victims[K comparable, V any] struct {
	// few contains the first n entries, in the order of add.
	few [8]*entry[K, V]

	// more contains the entries after the first eight, in the order of
	// add.
	more []*entry[K, V]

	n int
}

// add appends e, an entry whose callback is due.
func (v *victims[K, V]) add(e *entry[K, V]) {
	if v.n < len(v.few) {
		v.few[v.n] = e
		v.n++

		return
	}

	v.more = append(v.more, e)
}

// notify passes the key and the value of each entry to evicted, in the
// order of add. The caller has not locked the cache's mutex, so evicted
// can call the cache. A nil evicted does nothing.
func (v *victims[K, V]) notify(evicted func(K, V)) {
	if evicted == nil {
		return
	}

	for _, e := range v.few[:v.n] {
		evicted(e.key, e.value)
	}

	for _, e := range v.more {
		evicted(e.key, e.value)
	}
}
