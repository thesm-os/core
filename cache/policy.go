// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package cache

import "time"

// queueID names the queue of an entry. The zero queueID names no queue.
type queueID uint8

// The two queues of S3-FIFO.
const (
	// inSmall is the queue that admits a new entry whose key the ghost
	// does not remember.
	inSmall queueID = 1

	// inMain is the queue of the entries that were hit while they were in
	// the small queue, and of the new entries whose key the ghost
	// remembers.
	inMain queueID = 2
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

// evict removes entries other than keep until the sum of the costs of the
// entries of c is at most its capacity, or only pinned entries and keep
// remain, and adds to gone each removed entry without a pin. keep is the
// entry that the calling Set added, so a Set that finds every other entry
// pinned stores its entry past the capacity, and the next Set evicts it
// like any other. The caller has locked the mutex, and reads now from the
// cache's clock once for the call.
//
// Each step examines one entry. A step moves an entry from the small queue
// to the main queue, takes a hit from an entry of the main queue, or
// removes the entry. With n entries, an entry that no reader hits
// meanwhile leaves within (maxFreq+2)*n steps. Readers can keep hitting
// entries while evict runs, so the steps after those force the removal of
// each unpinned entry whatever its hits, two per entry. The number of
// steps is fixed when evict starts, so evict returns even when every entry
// is pinned.
func (c *Cache[K, V]) evict(now time.Time, keep *entry[K, V], gone *victims[K, V]) {
	n := c.small.count + c.main.count
	for i := range (maxFreq + 4) * n {
		if c.total.Load() <= c.capacity {
			return
		}

		//dokimi:mutate-skip ror-boundary: a forced step differs only while readers hit entries during evict, which no test orders
		c.examine(now, keep, gone, i >= (maxFreq+2)*n)
	}
}

// examine examines the oldest entry of the small queue while the entries
// of the small queue cost a tenth of the capacity or the main queue is
// empty, and the oldest entry of the main queue otherwise. The caller has
// locked the mutex, and calls examine only while the sum of the costs
// exceeds the capacity, so the queue that examine examines contains an
// entry of positive cost.
//
// examine keeps keep and a pinned entry. Unless force is set, it also
// keeps an entry that was hit and has not expired. A kept entry moves to
// the head of the main queue: an entry from the small queue starts there
// without hits, and an entry of the main queue loses one hit, which a
// concurrent hit can restore. examine removes every other entry from the
// cache, adds it to gone, and the ghost remembers the key of an entry that
// it removed from the small queue.
func (c *Cache[K, V]) examine(now time.Time, keep *entry[K, V], gone *victims[K, V], force bool) {
	q := &c.main
	if c.small.cost >= c.smallCapacity || c.main.tail == nil {
		q = &c.small
	}

	// evict refuses a pinned entry, so the pins need no check of their own.
	e := q.tail
	hit := e.freq.Load() > 0 && !force && !e.expired(now)
	if e == keep || hit || !e.evict() {
		q.remove(e)
		if q.id == inSmall {
			e.freq.Store(0)
		} else if e.freq.Load() > 0 {
			e.freq.Add(-1)
		}

		c.main.push(e)

		return
	}

	c.detach(e)
	if q.id == inSmall {
		c.ghost.add(e.hash, e.cost)
	}

	gone.add(e)
}
