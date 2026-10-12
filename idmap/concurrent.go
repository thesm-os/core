// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package idmap

import (
	"math/bits"
	"runtime"
	"sync/atomic"

	"go.thesmos.sh/core/id"
)

// bucketSlots is the number of slots of a bucket of a [Concurrent].
const bucketSlots = 4

// Word is the type set of the values of a [Concurrent]: integers of 32 and
// 64 bits. A value is one word, so a reader loads it atomically.
type Word interface {
	~int64 | ~uint64 | ~int32 | ~uint32
}

// Concurrent is a hash map of a fixed capacity from identifiers to
// integers, safe for concurrent use. Its readers take no lock.
//
// A Concurrent keeps its keys in buckets of four slots, a power of two of
// them, with linear probing over the buckets. Every field of a bucket is a
// word of sync/atomic:
//
//   - A sequence number, which is odd while a writer changes a slot of the
//     bucket.
//   - A home lock, which one writer at a time takes to add or remove the
//     keys whose home is the bucket.
//   - An overflow count, the number of keys whose walk from their home
//     passed the bucket because it was full.
//   - A tag word, with one tag per slot, and the words of the key and the
//     value of each slot.
//
// Keys do not move, so the buckets keep no tombstones. A lookup walks from
// the home bucket of its key while the overflow count of the bucket it
// leaves is above zero. The map never grows. It has buckets for twice its
// capacity, so it is at most half full.
//
// The zero Concurrent is not usable. [NewConcurrent] returns an empty map.
//
// # Concurrency
//
// Load, Store, Delete and Len are safe for concurrent use. Load, Store and
// Delete are linearizable.
//
//   - Load reads a bucket in one snapshot: it loads the sequence number,
//     waits while it is odd, reads the tags and the slots it needs, and
//     loads the sequence number again. It reads the bucket again when the
//     number changed. Load takes no lock.
//   - Store and Delete lock the home bucket of their key. A writer changes
//     a slot under the sequence number of its bucket, which it makes odd.
//   - Store of a new key increments the overflow count of each full bucket
//     that it passes before it publishes the key, so a reader that comes
//     after a reader that found a key also finds it. Delete removes its key
//     before it decrements those counts.
//
// A writer that the scheduler preempts while the sequence number of a
// bucket is odd delays the readers of that bucket. They yield until the
// writer finishes.
//
// # Allocation contract
//
// [NewConcurrent] allocates the map and its buckets, and for an ID160 or an
// ID256 key the array of the key words past the first 16 bytes. Load,
// Store, Delete and Len do not allocate.
type Concurrent[K Key, V Word] struct {
	// hash is the hash function of K.
	hash func(seed, K) (uint64, words)

	// yield runs in each round of a wait for a lock or for a stable bucket.
	// NewConcurrent sets it to runtime.Gosched. It is a field, so the tests
	// of this package can end a wait from inside it on one goroutine.
	yield func()

	buckets []bucket

	// rest has the words of the key of each slot past the first two, extra
	// words per slot: none for an ID128, one for an ID160 and two for an
	// ID256. The slot s of the bucket i starts at (i*bucketSlots+s)*extra.
	rest []atomic.Uint64

	// seed is the pair of secret words of the hash.
	seed seed

	// mask is the number of buckets minus 1. The home bucket of a hash h is
	// h & mask.
	mask uint64

	// limit is the capacity, and extra the words per slot of rest.
	limit int64
	extra uint64

	// The padding puts n on another cache line than the fields that every
	// Load reads, so the writes of Store and Delete to n do not evict them.
	_ [64]byte

	// n counts the keys and the reservations of the Stores of new keys that
	// are in progress.
	n atomic.Int64
}

// bucket is one bucket of a [Concurrent]: the words of four slots and of
// the locks and the counts that protect them.
type bucket struct {
	// seq is even while the slots are stable, and odd while a writer changes
	// a slot.
	seq atomic.Uint64

	// home is 1 while a writer adds or removes a key whose home is the
	// bucket, and 0 otherwise.
	home atomic.Uint32

	// overflow is the number of keys whose walk from their home passed the
	// bucket. A walk does not continue after a bucket whose overflow is 0.
	overflow atomic.Uint32

	// tags has the tag of slot s in its byte s.
	tags atomic.Uint32

	// keys has the first two words of the key of each full slot, and vals
	// its value.
	keys [bucketSlots][2]atomic.Uint64
	vals [bucketSlots]atomic.Uint64
}

// NewConcurrent returns an empty map for up to capacity keys, or for one
// key when capacity is below 1. It has the least power of two of buckets
// whose slots are at least twice capacity.
//
// NewConcurrent panics as make does when the buckets for capacity exceed
// the memory that make can allocate.
//
// # Allocation contract
//
// NewConcurrent allocates the map and its buckets, and for an ID160 or an
// ID256 key the array of the key words past the first 16 bytes.
func NewConcurrent[K Key, V Word](capacity int) *Concurrent[K, V] {
	capacity = max(capacity, 1)
	need := (2*capacity + bucketSlots - 1) / bucketSlots
	n := uint64(1) << bits.Len(uint(need-1))

	var zero K

	//nolint:gosec // G115: a key has 16 to 32 bytes, so the words past 16 bytes are 0 to 2
	extra := uint64((len(zero) - id.Size128 + 7) / 8)

	c := &Concurrent[K, V]{
		buckets: make([]bucket, n),
		rest:    make([]atomic.Uint64, n*bucketSlots*extra),
		yield:   runtime.Gosched,
		mask:    n - 1,
		limit:   int64(capacity),
		extra:   extra,
	}
	c.hash, c.seed = newHash[K]()

	return c
}

// Load returns the value of key, and reports whether key is present.
//
// # Allocation contract
//
// Zero alloc.
func (c *Concurrent[K, V]) Load(key K) (V, bool) {
	h, w := c.hash(c.seed, key)
	tag := uint32(h>>tagShift) | fullTag

	i := h & c.mask
	for range len(c.buckets) {
		if s, v := c.find(i, tag, w); s < bucketSlots {
			return V(v), true
		}

		if c.buckets[i].overflow.Load() == 0 {
			break
		}

		i = (i + 1) & c.mask
	}

	return 0, false
}

// Store sets the value of key.
//
// Error modes: a new key in a map of capacity keys returns [ErrFull],
// classified [go.thesmos.sh/core/errs.Transient], and leaves the map
// unchanged. The count of keys includes the new keys of the Stores in
// progress.
//
// # Allocation contract
//
// Zero alloc.
func (c *Concurrent[K, V]) Store(key K, value V) error {
	h, w := c.hash(c.seed, key)
	tag := uint32(h>>tagShift) | fullTag
	home := h & c.mask

	hb := &c.buckets[home]
	c.lockHome(hb)
	defer hb.home.Store(0)

	// Only a writer that holds the home lock adds or removes key, so key
	// remains in the slot where the walk finds it.
	i := home
	for range len(c.buckets) {
		if s, _ := c.find(i, tag, w); s < bucketSlots {
			b := &c.buckets[i]
			c.lockSlots(b)
			b.vals[s].Store(uint64(value))
			b.seq.Add(1)

			return nil
		}

		if c.buckets[i].overflow.Load() == 0 {
			break
		}

		i = (i + 1) & c.mask
	}

	if c.n.Add(1) > c.limit {
		c.n.Add(-1)

		return ErrFull
	}

	// The map has at most capacity keys in twice as many slots, so the walk
	// finds an empty slot.
	for i = home; ; i = (i + 1) & c.mask {
		b := &c.buckets[i]
		c.lockSlots(b)

		tags := b.tags.Load()
		for s := range uint64(bucketSlots) {
			if (tags>>(8*s))&0xff == 0 {
				b.keys[s][0].Store(w.w0)
				b.keys[s][1].Store(w.w1)

				if c.extra > 0 {
					rest := c.rest[(i*bucketSlots+s)*c.extra:][:c.extra]
					rest[0].Store(w.w2)
					if c.extra > 1 {
						rest[1].Store(w.w3)
					}
				}

				b.vals[s].Store(uint64(value))
				b.tags.Store(tags | tag<<(8*s))
				b.seq.Add(1)

				return nil
			}
		}

		b.seq.Add(1)
		b.overflow.Add(1)
	}
}

// Delete removes key, and reports whether key was present.
//
// # Allocation contract
//
// Zero alloc.
func (c *Concurrent[K, V]) Delete(key K) bool {
	h, w := c.hash(c.seed, key)
	tag := uint32(h>>tagShift) | fullTag
	home := h & c.mask

	hb := &c.buckets[home]
	c.lockHome(hb)
	defer hb.home.Store(0)

	i := home
	for range len(c.buckets) {
		if s, _ := c.find(i, tag, w); s < bucketSlots {
			b := &c.buckets[i]
			c.lockSlots(b)
			b.tags.Store(b.tags.Load() &^ (0xff << (8 * s)))
			b.seq.Add(1)

			// The key is gone before the overflow counts of the buckets
			// that its walk passed decrease, so no reader stops before a key
			// that it would find.
			for j := home; j != i; j = (j + 1) & c.mask {
				c.buckets[j].overflow.Add(^uint32(0))
			}

			c.n.Add(-1)

			return true
		}

		if c.buckets[i].overflow.Load() == 0 {
			break
		}

		i = (i + 1) & c.mask
	}

	return false
}

// Len returns the number of keys. The count includes the new keys of the
// Stores in progress and the keys of the Deletes in progress.
//
// # Allocation contract
//
// Zero alloc.
func (c *Concurrent[K, V]) Len() int {
	return int(c.n.Load())
}

// find returns the slot of the bucket i whose key has the words w, and the
// value of that slot. It compares the words only in a slot whose tag equals
// tag. It returns bucketSlots when the bucket does not contain the key. It
// reads the bucket in one snapshot: it reads the bucket again when a writer
// changed a slot during the read.
func (c *Concurrent[K, V]) find(i uint64, tag uint32, w words) (uint64, uint64) {
	b := &c.buckets[i]

	for {
		seq := b.seq.Load()
		if seq&1 != 0 {
			c.yield()

			continue
		}

		slot, val := uint64(bucketSlots), uint64(0)

		tags := b.tags.Load()
		for s := range uint64(bucketSlots) {
			if (tags>>(8*s))&0xff == tag && b.keys[s][0].Load() == w.w0 && b.keys[s][1].Load() == w.w1 &&
				(c.extra == 0 || c.sameRest(i, s, w)) {

				slot, val = s, b.vals[s].Load()

				break
			}
		}

		// The sequence number differs from seq only when a writer changed a
		// slot between the two loads.
		//dokimi:mutate-skip ror-true: a write between the two loads of the sequence number is a race that no test orders
		if b.seq.Load() == seq {
			return slot, val
		}
	}
}

// sameRest reports whether the words of the key of the slot s of the bucket
// i past its first two words equal w.w2 and w.w3. The key is an ID160 or an
// ID256, which has such words.
func (c *Concurrent[K, V]) sameRest(i, s uint64, w words) bool {
	rest := c.rest[(i*bucketSlots+s)*c.extra:][:c.extra]

	return rest[0].Load() == w.w2 && (c.extra == 1 || rest[1].Load() == w.w3)
}

// lockHome waits until no other writer adds or removes a key whose home is
// b, and then takes the home lock of b.
func (c *Concurrent[K, V]) lockHome(b *bucket) {
	for !b.home.CompareAndSwap(0, 1) {
		c.yield()
	}
}

// lockSlots waits until no other writer changes a slot of b, and then makes
// the sequence number of b odd. The writer makes it even again with an add
// of 1 once its change is complete.
func (c *Concurrent[K, V]) lockSlots(b *bucket) {
	for {
		seq := b.seq.Load()
		if seq&1 == 0 && b.seq.CompareAndSwap(seq, seq+1) {
			return
		}

		c.yield()
	}
}
