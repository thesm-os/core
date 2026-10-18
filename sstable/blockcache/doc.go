// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package blockcache implements a cache of the data blocks of the tables of
// package [go.thesmos.sh/core/sstable]. The cache lends its blocks to the
// readers, and the buffer of a block that leaves the cache returns to the
// pool of package sstable.
//
// A [Cache] is a [sstable.LendingCache] over a [cache.Cache] of
// [sstable.Block] values. A Reader that reads through a Cache copies a block
// that misses into a Block from the pool and adds that Block. The Cache
// releases its reference to a block that it evicts. The buffer then returns
// to the pool once the last cursor that reads the block moves on, so a miss
// allocates the entry of the cache.Cache and no block.
//
// # Capacity
//
// The capacity of a Cache bounds the sum of the footprints of its blocks.
// The footprint of a block is the capacity of its buffer, which exceeds the
// block by at most an eighth. [Cache.Cost] returns the sum.
//
// # Concurrency
//
// A Cache is safe for concurrent use. Lend takes no lock, because a lookup
// in a cache.Cache takes none.
//
// # Allocation contract
//
// Lend does not allocate. AddBlock and Add allocate the entry of the
// cache.Cache, and Add takes its Block from the pool. Get allocates the copy
// that it returns.
//
// # Errors
//
// [New] returns [cache.ErrConfig] for a Config with a Capacity that is not
// positive.
//
// # Dependency position
//
// Imports bytes, context and time from the standard library, and
// go.thesmos.sh/core/cache and go.thesmos.sh/core/sstable from this module.
package blockcache
