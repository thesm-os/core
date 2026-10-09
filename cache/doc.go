// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package cache implements a bounded map whose entries leave by eviction,
// expiry or removal.
//
// A [Cache] bounds the sum of the costs of its entries. The cost of an
// entry is 1, or a size that [Config.Cost] computes, such as the bytes of
// its value. A lookup that finds its entry takes no lock and does not
// allocate, so goroutines that hit the cache at the same time do not wait
// for each other.
//
// # Eviction
//
// The cache evicts with S3-FIFO. A small FIFO queue of a tenth of the
// capacity admits each new entry, a main FIFO queue keeps the entries that
// were hit, and a ghost remembers the keys that the small queue evicted
// last, up to the capacity of the main queue. The evictor examines the
// oldest entry of the small queue while the entries of that queue cost a
// tenth of the capacity, and of the main queue otherwise:
//
//   - An entry of the small queue that was hit moves to the main queue, and
//     one that was not leaves the cache, and the ghost remembers its key.
//   - An entry of the main queue that was hit loses one hit and moves to
//     the head of the main queue, and one that was not leaves the cache.
//   - A Set of a key that the ghost remembers admits its entry to the main
//     queue.
//
// A hit counts up to 3 on its entry, without a lock. An expired entry
// leaves when the evictor examines it, whatever its hits.
//
// # Pins
//
// [Cache.Pin] returns a [Pinned] entry, which the cache does not evict
// until [Pinned.Unpin]. The sum of the costs exceeds the capacity only
// while pinned entries fill the cache: a Set then keeps its new entry past
// the capacity, and a later Set evicts down to the capacity again. Set and
// Delete still remove a pinned entry from the cache, and its holder keeps
// using the value until it calls Unpin.
//
// # Expiry
//
// An entry expires at the time that Set gives it, read through
// [Config.Clock], and the zero time never expires. An expired entry is a
// miss, and it leaves the cache at the next Get or Pin that finds it, or
// when the evictor examines it.
//
// # Callbacks
//
// [Config.Evicted] receives every entry that leaves the cache, once,
// outside the cache's lock: by eviction, expiry, Delete, or a Set of its
// key, and the value of a Set whose cost exceeds the capacity. Evicted
// receives an entry that leaves while pinned at its last Unpin, so the
// callback can release the value, such as closing a key keeper.
//
// # Concurrency
//
// A Cache is safe for concurrent use. Get and Pin take no lock. Set and
// Delete take one lock per Cache, and so does a Get or Pin that removes an
// expired entry.
//
// # Allocation contract
//
// Get, Pin, [Pinned.Unpin], Delete, Len and Cost do not allocate for a key
// whose hash [hash/maphash.Comparable] computes without an allocation,
// such as a string or a value without pointers. Set allocates the entry,
// and the memory of the index and of the ghost when they grow.
//
// # Errors
//
// [New] returns [ErrConfig], classified
// [go.thesmos.sh/core/errs.Invalid], for a Config without a Clock or with a
// Capacity that is not positive.
//
// # Dependency position
//
// Imports errors, hash/maphash, sync, sync/atomic and time from the
// standard library, and go.thesmos.sh/core/clock and
// go.thesmos.sh/core/errs from this module.
package cache
