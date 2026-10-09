// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"hash/maphash"
	"sync"
	"sync/atomic"
	"time"

	"go.thesmos.sh/core/clock"
)

// Config configures a [Cache]. Clock and Capacity are required, and Cost
// and Evicted are optional. The zero Config is invalid, because it has no
// Clock.
type Config[K comparable, V any] struct {
	// Clock reads the time against which entries expire. Set reads it once
	// per call, and Get and Pin read it only for an entry with an expiry
	// time. A test passes a fake clock and advances it.
	Clock clock.Clock

	// Cost returns the cost of a value, such as the bytes that it
	// occupies. A nil Cost costs 1 per entry, so Capacity bounds the
	// number of entries. The cache counts a negative cost as 0, and calls
	// Cost once per Set, before it takes its lock.
	Cost func(V) int64

	// Evicted receives the key and the value of every entry that leaves
	// the cache, once: an entry that the cache evicts, that expires, that
	// Delete removes, or that a Set of its key replaces, and the value of a
	// Set whose cost exceeds Capacity. The cache calls Evicted outside its
	// lock, so Evicted can call the cache. Evicted receives a pinned entry
	// at its last Unpin, on the goroutine of that Unpin. A nil Evicted is
	// not called.
	Evicted func(k K, v V)

	// Capacity bounds the sum of the costs of the entries. It must be
	// positive. The sum exceeds it only when pinned entries fill the
	// cache: a Set then keeps its new entry, and the next Set that finds
	// unpinned entries evicts down to the capacity again.
	Capacity int64
}

// Cache is a bounded map whose entries leave by eviction, expiry or
// removal. It evicts with S3-FIFO, whose queues the package documentation
// describes, and a lookup that finds its entry takes no lock.
//
// A Cache keeps three structures. A hash table of entries, the index,
// serves lookups without a lock. A small queue and a main queue order the
// entries for eviction. A ghost remembers the keys that the small queue
// evicted. A Set allocates a new entry and links it into the index and a
// queue, and the evictor removes entries from the oldest end of a queue.
// No code reuses an entry, so a lookup that loaded an entry which has
// since left reads the key and the value of that entry.
//
// The zero Cache is not usable. [New] returns a Cache.
//
// # Concurrency
//
// Safe for concurrent use. Get and Pin take no lock, and a hit on an entry
// that already counts three hits writes nothing. One mutex per Cache
// serializes Set, Delete, and a Get or Pin that finds an expired entry.
// Len and Cost read atomic counters, and take no lock.
//
// # Allocation contract
//
// Get, Pin, [Pinned.Unpin], Delete, Len and Cost do not allocate, for a key
// whose hash [maphash.Comparable] computes without an allocation: a string,
// or a value without pointers. Set allocates the entry, and the memory of
// the index and of the ghost when they grow, so a cache in a steady state
// allocates once per Set. A Set that evicts more than eight entries
// allocates the list of their callbacks.
type Cache[K comparable, V any] struct {
	// index is the current index. A writer replaces it when it grows.
	index atomic.Pointer[index[K, V]]

	clock   clock.Clock
	cost    func(V) int64
	evicted func(K, V)

	// tomb marks a slot of the index whose entry has left the cache. It is
	// an entry of its own, so it equals no entry of the cache.
	tomb *entry[K, V]

	// small, main and ghost are the queues and the ghost of S3-FIFO. Only
	// a writer that has locked mu reads or writes them.
	small, main queue[K, V]
	ghost       ghost

	// seed seeds the hash of every key, so the slots of the keys differ
	// from one Cache to the next.
	seed maphash.Seed

	// total is the sum of the costs of the entries, and entries their
	// number. Writers change them under mu, and Len and Cost read them
	// without it.
	total, entries atomic.Int64

	// mu serializes the writers: the changes to the index, the queues and
	// the ghost.
	mu sync.Mutex

	// capacity is the bound of total, and smallCapacity the cost of the
	// small queue from which the evictor examines it: a tenth of capacity,
	// and at least 1.
	capacity, smallCapacity int64
}

// New returns an empty Cache over cfg, whose ghost remembers keys up to
// nine tenths of the capacity.
//
// Error modes: a nil Clock and a Capacity that is not positive return
// [ErrConfig], classified [go.thesmos.sh/core/errs.Invalid].
//
// # Allocation contract
//
// The Cache, an index of 16 slots and the map of the ghost.
func New[K comparable, V any](cfg Config[K, V]) (*Cache[K, V], error) {
	if cfg.Clock == nil || cfg.Capacity <= 0 {
		return nil, ErrConfig
	}

	c := &Cache[K, V]{
		clock:         cfg.Clock,
		cost:          cfg.Cost,
		evicted:       cfg.Evicted,
		tomb:          new(entry[K, V]),
		seed:          maphash.MakeSeed(),
		small:         queue[K, V]{id: inSmall},
		main:          queue[K, V]{id: inMain},
		capacity:      cfg.Capacity,
		smallCapacity: max(cfg.Capacity/10, 1),
	}
	c.ghost.count = make(map[uint64]int32)
	c.ghost.capacity = cfg.Capacity - c.smallCapacity
	c.index.Store(newIndex[K, V](minSlots))

	return c, nil
}

// Get returns the value of k, and reports whether the cache has an entry
// of k that has not expired. A hit counts toward the entry's hits, which
// keep it in the cache through evictions. A Get that finds an expired
// entry removes it from the cache, passes it to Evicted when it has no
// pin, and returns the zero value and false.
//
// Get cannot fail. A Get that runs at the same time as a Set or a Delete of
// k returns the value before the change or after it.
//
// # Allocation contract
//
// Zero alloc for a key whose hash [maphash.Comparable] computes without an
// allocation, apart from what Evicted allocates for an expired entry.
func (c *Cache[K, V]) Get(k K) (V, bool) {
	e := c.find(k)
	if e == nil {
		var zero V

		return zero, false
	}

	e.touch()

	return e.value, true
}

// Pin returns the entry of k, pinned, and reports whether the cache has
// an entry of k that has not expired. A Pin counts as a hit, as Get does.
//
// The cache does not evict a pinned entry, and the entry counts toward the
// capacity, until [Pinned.Unpin]. A Set or Delete of k still removes the
// entry from the cache, so a later Get misses, and the caller of Pin keeps
// using the value. Evicted receives the entry at its last Unpin. A Pin
// that finds an expired entry removes it, as Get does, and reports false.
// A Pin that races with the removal of the entry reports false.
//
// # Allocation contract
//
// As [Cache.Get]. The Pinned is a value.
func (c *Cache[K, V]) Pin(k K) (Pinned[K, V], bool) {
	e := c.find(k)
	if e == nil || !e.pin() {
		return Pinned[K, V]{}, false
	}

	e.touch()

	return Pinned[K, V]{c: c, e: e}, true
}

// Set stores v under k, in place of the entry of k, with an expiry time
// after which the entry is a miss. The zero time never expires. The new
// entry joins the main queue when the ghost remembers k, and the small
// queue otherwise. Set then evicts other entries until the sum of the
// costs is at most the capacity, or only pinned entries and the new entry
// remain, in which case the cache keeps the new entry past the capacity.
// Evicted receives the entry that Set replaces, and then every entry that
// it evicts, after Set releases its lock.
//
// A value whose cost exceeds the capacity is not stored: Set removes the
// entry of k and passes v to Evicted, and evicts nothing else. An expiry
// time at or before the clock's time stores an entry that the next Get
// misses.
//
// Set cannot fail.
//
// # Allocation contract
//
// One allocation for the entry. The index and the ghost allocate when they
// grow, and the list of callbacks when Set evicts more than eight entries.
func (c *Cache[K, V]) Set(k K, v V, expires time.Time) {
	cost := int64(1)
	if c.cost != nil {
		cost = max(c.cost(v), 0)
	}

	e := &entry[K, V]{key: k, value: v, expires: expires, hash: maphash.Comparable(c.seed, k), cost: cost}
	now := c.clock.Time()

	var gone victims[K, V]

	c.mu.Lock()

	if old := c.index.Load().find(k, e.hash, c.tomb); old != nil {
		c.detach(old)
		if old.leave() {
			gone.add(old)
		}
	}

	if cost > c.capacity {
		gone.add(e)
	} else {
		c.attach(e)
		c.evict(now, e, &gone)
	}

	c.mu.Unlock()

	gone.notify(c.evicted)
}

// Delete removes the entry of k from the cache, and reports whether the
// cache had one. Evicted receives the entry after Delete releases its
// lock, or at its last Unpin when it is pinned. An expired entry counts
// as an entry.
//
// Delete cannot fail.
//
// # Allocation contract
//
// Zero alloc for a key whose hash [maphash.Comparable] computes without an
// allocation, apart from what Evicted allocates.
func (c *Cache[K, V]) Delete(k K) bool {
	h := maphash.Comparable(c.seed, k)

	c.mu.Lock()

	e := c.index.Load().find(k, h, c.tomb)
	if e != nil {
		c.detach(e)
	}

	c.mu.Unlock()

	if e == nil {
		return false
	}

	if e.leave() {
		c.notify(e)
	}

	return true
}

// Len returns the number of entries in the cache, expired entries that no
// lookup has found yet included, and pinned entries that have left
// excluded. It reads an atomic counter, so under concurrent writers its
// result may already be out of date.
//
// # Allocation contract
//
// Zero alloc.
func (c *Cache[K, V]) Len() int {
	return int(c.entries.Load())
}

// Cost returns the sum of the costs of the entries that [Cache.Len]
// counts. It is at most the capacity, unless pinned entries take it past.
// It reads an atomic counter, so under concurrent writers its result may
// already be out of date.
//
// # Allocation contract
//
// Zero alloc.
func (c *Cache[K, V]) Cost() int64 {
	return c.total.Load()
}

// find returns the entry of k, or nil when the cache has none. When the
// entry has expired, find takes the lock, removes the entry unless a
// writer already did, releases the lock, passes the entry to Evicted when
// it has no pin, and returns nil. find reads the clock only for an entry
// with an expiry time.
//
// A writer that removed the entry first marks it as having left too, and
// leave reports an entry once, so exactly one of the two passes the entry
// to Evicted.
func (c *Cache[K, V]) find(k K) *entry[K, V] {
	h := maphash.Comparable(c.seed, k)

	e := c.index.Load().find(k, h, c.tomb)
	if e == nil || e.expires.IsZero() || !e.expired(c.clock.Time()) {
		return e
	}

	c.mu.Lock()

	if c.index.Load().find(k, h, c.tomb) == e {
		c.detach(e)
	}

	c.mu.Unlock()

	if e.leave() {
		c.notify(e)
	}

	return nil
}

// attach adds e, an entry whose key the index does not contain, to the
// index, growing the index first when it is full, and to the queue that
// admits it: the main queue when the ghost remembers its key, and the
// small queue otherwise. It adds the cost of e to the total. The caller
// has locked the mutex.
func (c *Cache[K, V]) attach(e *entry[K, V]) {
	ix := c.index.Load()
	if ix.full() {
		ix = ix.grow(c.tomb)
		c.index.Store(ix)
	}

	ix.insert(e, c.tomb)

	if c.ghost.contains(e.hash) {
		c.main.push(e)
	} else {
		c.small.push(e)
	}

	c.total.Add(e.cost)
	c.entries.Add(1)
}

// detach removes e, an entry of the cache, from the index and from its
// queue, and subtracts its cost from the total. It does not mark e as
// having left, which the caller does with leave or evict. The caller has
// locked the mutex.
func (c *Cache[K, V]) detach(e *entry[K, V]) {
	c.index.Load().remove(e, c.tomb)

	if e.queue == inSmall {
		c.small.remove(e)
	} else {
		c.main.remove(e)
	}

	c.total.Add(-e.cost)
	c.entries.Add(-1)
}

// notify passes the key and the value of e to Evicted, when the Config set
// one. The caller has not locked the cache's mutex.
func (c *Cache[K, V]) notify(e *entry[K, V]) {
	if c.evicted != nil {
		c.evicted(e.key, e.value)
	}
}
