// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"context"
	"hash/maphash"
	"sync"
	"sync/atomic"
	"time"

	"go.thesmos.sh/core/clock"
)

// Config configures a [Cache]. Capacity is required, and Clock, Cost and
// Evicted are optional. The zero Config is invalid, because its Capacity is
// 0.
type Config[K comparable, V any] struct {
	// Clock reads the time against which entries expire. Set and Expire
	// read it once per call. Get, Pin and each lookup of GetOrLoad read it
	// only for an entry with an expiry time. GetOrLoad reads it once more to
	// store the value of a load. A test passes a fake clock and advances it.
	//
	// A nil Clock makes a cache whose entries do not expire. Its Set panics
	// for an expiry time other than zero, and its Expire returns 0.
	Clock clock.Clock

	// Cost returns the cost of a value, such as the bytes that it
	// occupies. A nil Cost costs 1 per entry, so Capacity bounds the
	// number of entries. The cache counts a negative cost as 0, and calls
	// Cost once per Set, before it takes its lock.
	Cost func(V) int64

	// Evicted receives the key and the value of every entry that leaves
	// the cache, once: an entry that the cache evicts, that expires, that
	// Delete removes, or that a Set or SetPinned of its key replaces, and
	// the value of a Set or SetPinned whose cost exceeds Capacity. The
	// cache calls Evicted outside its lock, so Evicted can call the cache.
	// Evicted receives a pinned entry at its last Unpin, on the goroutine
	// of that Unpin. A nil Evicted is not called.
	Evicted func(k K, v V)

	// Capacity bounds the sum of the costs of the entries. It must be
	// positive. The sum exceeds it only when pinned entries fill the
	// cache: a Set then keeps its new entry. The last Unpin of an entry,
	// and the next Set that finds unpinned entries, evict down to the
	// capacity again.
	Capacity int64
}

// flight is a load of a missing entry that runs, and the callers of
// [Cache.GetOrLoad] that wait for it. The load writes value and err once,
// before it closes done, and each waiting caller reads them after done is
// closed. The mutex of the cache guards waiters.
type flight[V any] struct {
	// done is closed once the load has returned.
	done chan struct{}

	// cancel ends the context of the load.
	cancel context.CancelFunc

	value V
	err   error

	// waiters is the number of the callers that wait for the load.
	waiters int
}

// Cache is a bounded map whose entries leave by eviction, expiry or
// removal. It evicts with S3-FIFO, whose queues the package documentation
// describes, and a lookup that finds its entry takes no lock.
//
// A Cache keeps four structures. A hash table of entries, the index,
// serves lookups without a lock. A small queue and a main queue order the
// entries for eviction. A ghost remembers the keys that the small queue
// evicted. A heap orders the entries that have an expiry time for
// [Cache.Expire]. A Set allocates a new entry and links it into the index
// and a queue, and into the heap when the entry has an expiry time. The
// evictor removes entries from the oldest end of a queue, and sets a
// pinned entry aside, out of both queues, until its last Unpin. No code
// reuses an entry, so a lookup that loaded an entry which has since left
// reads the key and the value of that entry.
//
// The zero Cache is not usable. [New] returns a Cache.
//
// # Concurrency
//
// Safe for concurrent use. Get, Pin and a hit of GetOrLoad take no lock. A
// hit on an entry that already counts three hits does not write to the
// entry. One mutex per Cache serializes Set, SetPinned, Delete, Expire, a
// Get or Pin that finds an expired entry, the miss and the store of a
// GetOrLoad, and the last [Pinned.Unpin] of an entry while the sum of the
// costs exceeds the capacity. The last Unpin of an entry that the
// evictor set aside adds the entry to a list of released entries without
// the lock. The next eviction past the capacity takes that list under the
// lock. Len and Cost read atomic counters without the lock. Capacity reads
// a field that New sets, also without the lock.
//
// # Allocation contract
//
// Get, Pin, [Pinned.Unpin], Delete, Len, Cost, Capacity and a hit of
// GetOrLoad do not allocate, for a key whose hash [maphash.Comparable]
// computes without an allocation: a string, or a value without pointers. A
// miss of GetOrLoad allocates as [Cache.GetOrLoad] states. Set and SetPinned
// allocate the entry, and the memory of the index, the ghost and the heap
// when they grow.
// In a steady state, the tombs of the entries that left still fill the
// index. The cache then replaces the index with a new one of 2 objects. An
// index of n slots takes at least n/4 Sets to fill. A Set, SetPinned or
// Unpin that evicts more than eight entries allocates the list of their
// callbacks. Expire does not allocate.
type Cache[K comparable, V any] struct {
	// index is the current index. A writer replaces it when it grows.
	index atomic.Pointer[index[K, V]]

	// released is the newest of the released entries: the entries that the
	// evictor set aside and whose last pin has ended, linked through their
	// next fields. A last Unpin adds an entry without the lock, and restore
	// takes them all under it.
	released atomic.Pointer[entry[K, V]]

	// clock, cost and evicted are the Clock, Cost and Evicted of the Config.
	// clock is nil in a cache whose entries do not expire.
	clock   clock.Clock
	cost    func(V) int64
	evicted func(K, V)

	// tomb marks a slot of the index whose entry has left the cache. It is
	// an entry of its own, so it equals no entry of the cache.
	tomb *entry[K, V]

	// flights are the loads of GetOrLoad that run, by key. A Set, SetPinned
	// or Delete of a key takes its load out, so the load does not store its
	// value. The map is nil until the first load. Guarded by mu.
	flights map[K]*flight[V]

	// expiry orders the entries that have an expiry time by that time.
	// Only a writer that has locked mu reads or writes it.
	expiry expiry[K, V]

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
// Error modes: a Capacity that is not positive returns [ErrConfig]. It
// classifies as [go.thesmos.sh/core/errs.Invalid].
//
// # Allocation contract
//
// The Cache, an index of 16 slots and the map of the ghost.
func New[K comparable, V any](cfg Config[K, V]) (*Cache[K, V], error) {
	if cfg.Capacity <= 0 {
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

// GetOrLoad returns the value of k, and loads a missing value once for the
// callers that miss k together. A hit returns the value as Get does, and
// does not call load. The first caller that misses k starts load on a
// goroutine of its own. Each later caller of k waits for the result of that
// load, as the callers of golang.org/x/sync/singleflight wait.
//
// load receives k and a context with the values of the context of the
// caller that started the load. That context ends once load returns, or once
// every caller that waits for the load has stopped waiting. load returns the
// value of k and the expiry time of its entry, which is the zero time for an
// entry that does not expire. A load that returns a value stores it as Set
// does, and each waiting caller receives the value. A load that returns an
// error does not store a value, and each waiting caller receives the error.
// The next caller of k then starts a new load.
//
// A Set, SetPinned or Delete of k while its load runs discards the value of
// the load. The waiting callers still receive the value, and the cache keeps
// the state that the write left.
//
// A caller stops waiting when its context ends, and returns the cause of its
// context. On a miss, a caller whose context ended before the call returns
// the cause without a load. Each caller of one k passes a load that computes
// the same value, because a caller can receive the value of a load that
// another caller passed.
//
// Error modes:
//
//   - The error of load.
//   - [ErrLoad] for a nil load on a miss. It classifies as
//     [go.thesmos.sh/core/errs.Invalid].
//   - [ErrLoad] for a load that succeeds with an expiry time in a cache
//     without a [Config.Clock]. The cache does not store the value of such a
//     load.
//   - The cause of ctx when ctx ends first.
//
// # Panics
//
// A load that panics crashes the process from its own goroutine.
//
// # Allocation contract
//
// A hit allocates nothing, as Get does, when load is a value that the caller
// made before the call, such as a field. A miss that starts a load allocates
// 6 objects: the state of the load, its channel, the closure that starts its
// goroutine, its two contexts and the cancel function of its context. The
// store of its value allocates as Set does. A miss that waits for a load
// that runs allocates nothing.
func (c *Cache[K, V]) GetOrLoad(
	ctx context.Context, k K, load func(ctx context.Context, k K) (V, time.Time, error),
) (V, error) {
	if e := c.find(k); e != nil {
		e.touch()

		return e.value, nil
	}

	var zero V
	if err := context.Cause(ctx); err != nil {
		return zero, err
	}

	if load == nil {
		return zero, ErrLoad
	}

	f, e := c.join(ctx, k, load)
	if e != nil {
		e.touch()

		return e.value, nil
	}

	select {
	case <-f.done:
		return f.value, f.err
	case <-ctx.Done():
		c.stopWaiting(k, f)

		return zero, context.Cause(ctx)
	}
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
// An entry with an expiry time also joins the heap that [Cache.Expire]
// reads. The heap adds an entry, and removes an entry that has an expiry
// time, in O(log n) steps for n entries with an expiry time.
//
// Set cannot fail. In a cache without a [Config.Clock], Set panics for an
// expiry time other than zero. The panic leaves the cache unchanged.
//
// # Allocation contract
//
// One allocation for the entry. The index, the ghost and the heap allocate
// when they grow, and the list of callbacks when Set evicts more than eight
// entries.
func (c *Cache[K, V]) Set(k K, v V, expires time.Time) {
	e, now := c.newEntry(k, v, expires)
	c.store(e, now)
}

// SetPinned stores v under k as [Cache.Set] does, and returns the new
// entry with one pin. The entry has its pin before the cache publishes it.
// The evictor does not evict the entry until [Pinned.Unpin], so the caller
// uses v until then. A Set or Delete of k still removes the entry from the
// cache. Evicted then receives the entry at its last Unpin.
//
// SetPinned counts no hit on the entry, because a store is not a lookup.
// An entry without a lookup leaves the cache at the first examination
// after its Unpin.
//
// A value whose cost exceeds the capacity is not stored. SetPinned removes
// the entry of k and evicts nothing else. It returns a Pinned of v whose
// Unpin passes v to Evicted.
//
// SetPinned cannot fail. In a cache without a [Config.Clock], SetPinned
// panics for an expiry time other than zero. The panic leaves the cache
// unchanged.
//
// # Allocation contract
//
// As [Cache.Set]. The Pinned is a value.
func (c *Cache[K, V]) SetPinned(k K, v V, expires time.Time) Pinned[K, V] {
	e, now := c.newEntry(k, v, expires)
	e.state.Store(1)
	c.store(e, now)

	return Pinned[K, V]{c: c, e: e}
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

	// A load of k that runs must not store its value after the Delete.
	delete(c.flights, k)

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

// Expire removes every expired entry and returns the number of entries
// that it removed. Evicted receives each entry after Expire releases its
// lock. Evicted receives a pinned entry at its last Unpin instead. The cache
// does not start a goroutine for expiry. Its owner calls Expire from a
// timer of its own.
//
// Expire reads the clock once and removes the entries in the order of
// their expiry times. It removes at most eight entries each time it locks
// the cache. It passes those entries to Evicted before it locks the cache
// again, so a concurrent Set waits for at most eight removals. Each removal
// takes O(log n) steps for n entries with an expiry time.
//
// Expire cannot fail. In a cache without a [Config.Clock], Expire returns 0
// and does not read a clock.
//
// # Allocation contract
//
// Zero alloc, apart from what Evicted allocates.
func (c *Cache[K, V]) Expire() int {
	if c.clock == nil {
		return 0
	}

	now := c.clock.Time()
	removed := 0

	for {
		var gone victims[K, V]

		c.mu.Lock()

		n := 0
		for ; n < len(gone.few); n++ {
			e := c.expiry.first()
			if e == nil || !e.expired(now) {
				break
			}

			c.detach(e)
			if e.leave() {
				gone.add(e)
			}
		}

		c.mu.Unlock()

		gone.notify(c.evicted)
		removed += n

		if n < len(gone.few) {
			return removed
		}
	}
}

// Len returns the number of entries in the cache. It counts the expired
// entries that neither a lookup nor Expire has removed yet, and does not
// count the pinned entries that have left. It reads an atomic counter, so
// under concurrent writers its result may already be out of date.
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

// Capacity returns the Capacity of the Config of the cache. The sum that
// [Cache.Cost] returns is at most the capacity, unless pinned entries take
// it past. The capacity does not change after [New], so Capacity reads it
// without a lock.
//
// # Allocation contract
//
// Zero alloc.
func (c *Cache[K, V]) Capacity() int64 {
	return c.capacity
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

// newEntry returns a new entry of k and v that expires at expires, and the
// time of the cache's clock. The entry has the cost of v and the hash of k.
// The time is zero in a cache without a Clock. newEntry takes no lock. It
// reads the clock once and calls Cost once.
//
// In a cache without a Clock, newEntry panics for an expiry time other
// than zero. The cache is unchanged after the panic.
func (c *Cache[K, V]) newEntry(k K, v V, expires time.Time) (*entry[K, V], time.Time) {
	var now time.Time
	if c.clock != nil {
		now = c.clock.Time()
	} else if !expires.IsZero() {
		panic( //nolint:forbidigo // an expiry time in a cache without a Clock is a programmer error
			"cache: an expiry time in a cache without a Clock",
		)
	}

	cost := int64(1)
	if c.cost != nil {
		cost = max(c.cost(v), 0)
	}

	return &entry[K, V]{key: k, value: v, expires: expires, hash: maphash.Comparable(c.seed, k), cost: cost}, now
}

// store adds e to the cache in place of the entry of its key as put does,
// under the lock, and passes the entries that leave to Evicted after it
// releases the lock. e is a new entry from newEntry, and now is the time
// that newEntry read. SetPinned pins e before it calls store, so store
// publishes a pinned e and the evictor keeps it until its last Unpin.
func (c *Cache[K, V]) store(e *entry[K, V], now time.Time) {
	var gone victims[K, V]

	c.mu.Lock()
	c.put(e, now, &gone)
	c.mu.Unlock()

	gone.notify(c.evicted)
}

// join counts the caller as a waiter of the load of k that runs, or of a new
// load of k that it starts with load. The context of a new load has the
// values of ctx, and its cancel function ends it. join returns the entry of
// k instead when a Set or a load stored one after the lookup of the caller
// missed. join locks the mutex.
func (c *Cache[K, V]) join(
	ctx context.Context, k K, load func(ctx context.Context, k K) (V, time.Time, error),
) (*flight[V], *entry[K, V]) {
	h := maphash.Comparable(c.seed, k)

	c.mu.Lock()
	defer c.mu.Unlock()

	if e := c.index.Load().find(k, h, c.tomb); e != nil && (e.expires.IsZero() || !e.expired(c.clock.Time())) {
		return nil, e
	}

	f := c.flights[k]
	if f == nil {
		//nolint:gosec // G118: run and stopWaiting call f.cancel. gosec does not follow a field of a generic type.
		loadCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		f = &flight[V]{done: make(chan struct{}), cancel: cancel}

		if c.flights == nil {
			c.flights = make(map[K]*flight[V])
		}

		c.flights[k] = f

		go c.run(loadCtx, k, f, load)
	}

	f.waiters++

	return f, nil
}

// run calls load for k on the goroutine of the load f. run stores the value
// of a load that succeeds as put does, unless a Set, a SetPinned or a Delete
// of k took f out of the loads, or every caller stopped waiting. A load that
// succeeds with an expiry time in a cache without a Clock fails with ErrLoad.
// run then hands the result to the waiting callers, ends the context of the
// load, and passes the entries that left to Evicted.
func (c *Cache[K, V]) run(
	ctx context.Context, k K, f *flight[V], load func(ctx context.Context, k K) (V, time.Time, error),
) {
	v, expires, err := load(ctx, k)
	if err == nil && c.clock == nil && !expires.IsZero() {
		err = ErrLoad
	}

	var (
		gone victims[K, V]
		e    *entry[K, V]
		now  time.Time
	)

	if err == nil {
		e, now = c.newEntry(k, v, expires)
	}

	c.mu.Lock()

	if c.flights[k] == f {
		delete(c.flights, k)

		if e != nil {
			c.put(e, now, &gone)
		}
	}

	c.mu.Unlock()

	f.value, f.err = v, err
	close(f.done)
	f.cancel()
	gone.notify(c.evicted)
}

// stopWaiting ends the wait of one caller of the load f of k. The last
// caller that stops takes f out of the loads, so the next caller of k starts
// a new load, and it ends the context of f. The value of f then does not go
// into the cache.
func (c *Cache[K, V]) stopWaiting(k K, f *flight[V]) {
	c.mu.Lock()

	f.waiters--
	last := f.waiters == 0

	if last && c.flights[k] == f {
		delete(c.flights, k)
	}

	c.mu.Unlock()

	if last {
		f.cancel()
	}
}

// put adds e to the cache in place of the entry of its key, and evicts other
// entries as Set documents. It takes a load of the key out of the loads, so
// the load does not store its value over e. The caller has locked the mutex,
// and passes the entries that put adds to gone to Evicted after it releases
// the lock.
//
// put puts e in the slot of the old entry of its key with one atomic store,
// so a Get of the key that runs meanwhile finds the old entry or e.
//
// An e whose cost exceeds the capacity leaves the cache at once. put adds
// such an e to gone when it has no pin. The last Unpin of e passes it to
// Evicted otherwise.
func (c *Cache[K, V]) put(e *entry[K, V], now time.Time, gone *victims[K, V]) {
	delete(c.flights, e.key)

	ix := c.index.Load()
	old := ix.find(e.key, e.hash, c.tomb)
	fits := e.cost <= c.capacity

	switch {
	case old != nil && fits:
		ix.replace(old, e)
		c.unlink(old)
		c.link(e)
	case old != nil:
		c.detach(old)
	case fits:
		c.attach(e)
	}

	if old != nil && old.leave() {
		gone.add(old)
	}

	if !fits {
		if e.leave() {
			gone.add(e)
		}
	} else {
		c.evict(now, e, gone)
	}
}

// shrink evicts down to the capacity as a Set does, and passes the entries
// that it evicts to Evicted after it releases the lock. The last Unpin of
// an entry calls it while the sum of the costs exceeds the capacity, so the
// sum exceeds the capacity only while pinned entries fill the cache. shrink
// reads the clock once, so the evictor removes an expired entry whatever
// its hits.
func (c *Cache[K, V]) shrink() {
	var now time.Time
	if c.clock != nil {
		now = c.clock.Time()
	}

	var gone victims[K, V]

	c.mu.Lock()
	c.evict(now, nil, &gone)
	c.mu.Unlock()

	gone.notify(c.evicted)
}

// attach adds e, an entry whose key the index does not contain, to the
// index, growing the index first when it is full, and links it as link
// does. The caller has locked the mutex.
func (c *Cache[K, V]) attach(e *entry[K, V]) {
	ix := c.index.Load()
	if ix.full() {
		ix = ix.grow(c.tomb)
		c.index.Store(ix)
	}

	ix.insert(e, c.tomb)
	c.link(e)
}

// link adds e to the queue that admits it: the main queue when the ghost
// remembers its key, and the small queue otherwise. It adds e to the heap
// of expiry times when e has an expiry time, and adds the cost of e to the
// total. link does not change the index. The caller has locked the mutex.
func (c *Cache[K, V]) link(e *entry[K, V]) {
	if c.ghost.contains(e.hash) {
		c.main.push(e)
	} else {
		c.small.push(e)
	}

	if !e.expires.IsZero() {
		c.expiry.push(e)
	}

	c.total.Add(e.cost)
	c.entries.Add(1)
}

// detach removes e, an entry of the cache, from the index and unlinks it
// as unlink does. detach does not mark e as having left, which the caller
// does with leave or evict. The caller has locked the mutex.
func (c *Cache[K, V]) detach(e *entry[K, V]) {
	c.index.Load().remove(e, c.tomb)
	c.unlink(e)
}

// unlink removes e from its queue and from the heap of expiry times, and
// subtracts its cost from the total. An entry that the evictor set aside
// is in neither queue, so unlink sets its queueID to zero, and restore then
// skips the entry. unlink does not change the index. The caller has locked
// the mutex.
func (c *Cache[K, V]) unlink(e *entry[K, V]) {
	switch e.queue {
	case inSmall:
		c.small.remove(e)
	case inMain:
		c.main.remove(e)
	default:
		e.queue = 0
	}

	if !e.expires.IsZero() {
		c.expiry.remove(e)
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
