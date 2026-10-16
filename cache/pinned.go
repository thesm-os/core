// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package cache

// Pinned is an entry of a [Cache] with one pin, which [Cache.Pin] and
// [Cache.SetPinned] return. The cache does not evict the entry until
// Unpin, and the caller uses the value until then, also when a Set or
// Delete of its key removes the entry from the cache. The zero Pinned has
// no entry.
//
// # Concurrency
//
// A Pinned is a value, and every copy refers to the same pin, so the caller
// calls Unpin once per Pin or SetPinned, as it unlocks a mutex once per
// lock. Value is safe for concurrent use until Unpin.
//
// # Allocation contract
//
// A Pinned is two pointers, passed by value, so Pin, Value and Unpin do
// not allocate. Unpin allocates only the list of the callbacks of more than
// eight entries that it evicts.
type Pinned[K comparable, V any] struct {
	c *Cache[K, V]
	e *entry[K, V]
}

// Value returns the value of the entry, and the zero value for the zero
// Pinned. The value is the one that the entry had when Pin or SetPinned
// returned, also after a Set of its key stores a new one.
func (p Pinned[K, V]) Value() V {
	if p.e == nil {
		var zero V

		return zero
	}

	return p.e.value
}

// Unpin removes the pin. When the entry has left the cache and this was
// its last pin, Unpin passes it to Evicted, on the goroutine that calls
// Unpin. Unpin of the zero Pinned does nothing. A second Unpin of the same
// pin removes another holder's pin, so the cache can then evict an entry
// that is still in use.
//
// When this was the last pin of the entry and the sum of the costs exceeds
// the capacity, Unpin evicts down to the capacity as a Set does. It locks
// the cache for the eviction, and passes the entries that it evicts to
// Evicted after it releases the lock. An Unpin while the sum is at most the
// capacity takes no lock.
//
// # Allocation contract
//
// Zero alloc, apart from what Evicted allocates. An Unpin that evicts more
// than eight entries allocates the list of their callbacks.
func (p Pinned[K, V]) Unpin() {
	if p.e == nil {
		return
	}

	last, left := p.e.unpin()
	if last && left {
		p.c.notify(p.e)
	}
	if last && p.c.total.Load() > p.c.capacity {
		p.c.shrink()
	}
}
