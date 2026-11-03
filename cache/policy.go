// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package cache

import "time"

// queueID names the queue of an entry. An entry whose queue is the zero
// queueID is not in a queue.
type queueID uint8

// The queues of S3-FIFO, and the place of an entry that the evictor set
// aside.
const (
	// inSmall is the queue that admits a new entry whose key the ghost
	// does not remember.
	inSmall queueID = 1

	// inMain is the queue of the entries that were hit while they were in
	// the small queue, and of the new entries whose key the ghost
	// remembers.
	inMain queueID = 2

	// setAside names an entry that the evictor found pinned and took out
	// of both queues. The entry returns to the main queue at the first
	// eviction past the capacity after its last unpin.
	setAside queueID = 3
)

// queue is a FIFO queue of entries, linked through their prev and next
// fields. An entry joins at the head, and the evictor examines the tail,
// the oldest entry. The zero queue is empty, and has the zero queueID.
//
// # Concurrency
//
// Only the holder of the cache's lock reads or writes a queue.
//
// # Allocation contract
//
// push and remove do not allocate.
type queue[K comparable, V any] struct {
	// head is the newest entry and tail the oldest, both nil when the
	// queue is empty.
	head, tail *entry[K, V]

	// cost is the sum of the costs of the entries of the queue, and count
	// their number.
	cost  int64
	count int

	// id is the queueID that push writes into each entry.
	id queueID
}

// push adds e, an entry of no queue, at the head of q, and adds its cost.
func (q *queue[K, V]) push(e *entry[K, V]) {
	e.prev, e.next = q.head, nil
	if q.head != nil {
		q.head.next = e
	} else {
		q.tail = e
	}

	q.head = e
	q.cost += e.cost
	q.count++
	e.queue = q.id
}

// remove takes e out of q, which contains it, and subtracts its cost. e is
// then of no queue.
func (q *queue[K, V]) remove(e *entry[K, V]) {
	if e.prev != nil {
		e.prev.next = e.next
	} else {
		q.tail = e.next
	}

	if e.next != nil {
		e.next.prev = e.prev
	} else {
		q.head = e.prev
	}

	e.prev, e.next = nil, nil
	q.cost -= e.cost
	q.count--
}

// ghost remembers the hashes of the keys whose entries the small queue
// evicted, up to the capacity of the main queue in cost. A Set of a key
// that the ghost remembers admits its entry to the main queue, so a key
// that returns soon after its eviction skips the small queue.
//
// The ghost keeps 64-bit hashes, not keys, so two keys of one hash share
// a memory, with a probability of about n/2^64 for n remembered keys.
//
// # Concurrency
//
// Only the holder of the cache's lock reads or writes the ghost.
//
// # Allocation contract
//
// contains does not allocate. add allocates when the ring is full, which
// doubles it, and when count grows.
type ghost struct {
	// count counts the remembered hashes by value, so contains costs one
	// map lookup.
	count map[uint64]int32

	// ring contains the remembered hashes in the order of add: n of them,
	// from start, wrapping around the end.
	ring []ghostEntry

	start, n int

	// cost is the sum of the costs of the remembered hashes, at most
	// capacity, which is the capacity of the main queue.
	cost, capacity int64
}

// ghostEntry is one remembered hash, with the cost of the entry whose key
// it hashes.
type ghostEntry struct {
	hash uint64
	cost int64
}

// add remembers h, the hash of a key whose entry of cost c left the small
// queue, and forgets the oldest hashes until the costs fit the capacity.
// It does not remember h when c alone exceeds the capacity.
func (g *ghost) add(h uint64, c int64) {
	if c > g.capacity {
		return
	}

	for g.cost+c > g.capacity {
		g.forget()
	}

	if g.n == len(g.ring) {
		ring := make([]ghostEntry, max(minSlots, 2*len(g.ring)))
		for i := range g.n {
			ring[i] = g.ring[(g.start+i)%len(g.ring)]
		}

		g.ring, g.start = ring, 0
	}

	g.ring[(g.start+g.n)%len(g.ring)] = ghostEntry{hash: h, cost: c}
	g.n++
	g.cost += c
	g.count[h]++
}

// forget drops the oldest remembered hash, and its cost. The ghost
// remembers at least one hash.
func (g *ghost) forget() {
	old := g.ring[g.start]
	g.start = (g.start + 1) % len(g.ring)
	g.n--
	g.cost -= old.cost

	if g.count[old.hash] == 1 {
		delete(g.count, old.hash)

		return
	}

	g.count[old.hash]--
}

// contains reports whether g remembers h.
func (g *ghost) contains(h uint64) bool {
	return g.count[h] != 0
}

// evict removes entries other than keep while the sum of the costs of the
// entries of c exceeds its capacity, and adds to gone each removed entry
// without a pin. It stops when the small and the main queue are empty, or
// after a number of steps that it fixes when it starts. keep is the entry
// that the calling Set added, so a Set that finds every other entry pinned
// stores its entry past the capacity. The next Set, or the last Unpin of
// an entry, evicts it like any other. The last Unpin passes a nil keep. The
// caller has locked the mutex, and reads now from the cache's clock once
// for the call.
//
// Past the capacity, evict first moves the released entries back to the
// main queue with restore. Each step then examines one entry. A step moves
// an entry from the small queue to the main queue, takes a hit from an
// entry of the main queue, sets a pinned entry aside, or removes the entry.
// A pinned entry leaves both queues until its last Unpin, so a Set past the
// capacity examines each pinned entry once per pin. With n entries in the
// queues, an entry that readers do not hit meanwhile leaves within
// (maxFreq+2)*n steps. Readers can keep hitting entries while evict runs,
// so the steps after those force the removal of each unpinned entry
// whatever its hits, two per entry.
func (c *Cache[K, V]) evict(now time.Time, keep *entry[K, V], gone *victims[K, V]) {
	if c.total.Load() <= c.capacity {
		return
	}

	c.restore()

	n := c.small.count + c.main.count
	for i := range (maxFreq + 4) * n {
		if c.total.Load() <= c.capacity || c.small.tail == nil && c.main.tail == nil {
			return
		}

		//dokimi:mutate-skip ror-boundary: a forced step differs only while readers hit entries during evict, which no test orders
		c.examine(now, keep, gone, i >= (maxFreq+2)*n)
	}
}

// restore moves each released entry, an entry that the evictor set aside
// and whose last pin has ended since, to the head of the main queue. A Set
// or Delete of its key can have removed such an entry from the cache, and
// restore skips it. restore reads the list before it swaps it, so an
// eviction without released entries writes nothing to the list. The caller
// has locked the mutex.
func (c *Cache[K, V]) restore() {
	for c.released.Load() != nil {
		for e := c.released.Swap(nil); e != nil; {
			next := e.next
			if e.queue == setAside {
				c.main.push(e)
			}

			e = next
		}
	}
}

// examine examines the oldest entry of the small queue while the entries
// of the small queue cost a tenth of the capacity or the main queue is
// empty, and the oldest entry of the main queue otherwise. The caller has
// locked the mutex, and calls examine only while the sum of the costs
// exceeds the capacity and a queue has an entry, so the queue that examine
// examines has one.
//
// examine keeps keep and a pinned entry. Unless force is set, it also
// keeps an entry that was hit and has not expired. A kept entry leaves its
// queue. An entry from the small queue loses its hits, and an entry of the
// main queue loses one hit. A concurrent hit can restore that hit. examine
// then sets a pinned entry other than keep aside, out of both queues, and
// moves every other kept entry to the head of the main queue. examine
// removes every entry that it does not keep from the cache, adds it to
// gone, and the ghost remembers the key of an entry that it removed from
// the small queue.
func (c *Cache[K, V]) examine(now time.Time, keep *entry[K, V], gone *victims[K, V], force bool) {
	q := &c.main
	if c.small.cost >= c.smallCapacity || c.main.tail == nil {
		q = &c.small
	}

	// evict refuses a pinned entry, so the pins do not need a check of their
	// own.
	e := q.tail
	hit := e.freq.Load() > 0 && !force && !e.expired(now)
	if e != keep && !hit && e.evict() {
		c.detach(e)
		if q.id == inSmall {
			c.ghost.add(e.hash, e.cost)
		}

		gone.add(e)

		return
	}

	q.remove(e)
	if q.id == inSmall {
		e.freq.Store(0)
	} else if e.freq.Load() > 0 {
		e.freq.Add(-1)
	}

	if e != keep && e.markAside() {
		e.queue = setAside

		return
	}

	c.main.push(e)
}

// release adds e, an entry that the evictor set aside and whose last pin
// has ended, to the released entries that the next eviction past the
// capacity moves back to the main queue. It takes no lock. The caller has
// cleared the aside bit of e with clearAside, so no other call adds e until
// the evictor sets it aside again.
func (c *Cache[K, V]) release(e *entry[K, V]) {
	for {
		head := c.released.Load()
		e.next = head
		if c.released.CompareAndSwap(head, e) {
			return
		}
	}
}
