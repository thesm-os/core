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
// # Identities
//
// The tables that share a Cache need different identities, because a Reader
// does not check the CRC32C of a block that the Cache lends. [Cache.NewID]
// returns 1 at its first call and one more at each call after it. A consumer
// takes one for each table that it opens through the Cache, and passes it as
// the CacheID of [sstable.ReaderOptions]. The Cache keeps a block by the
// identity and the offset alone. Lend ignores the [sstable.BlockRole] that
// it receives.
//
// # Capacity
//
// The capacity of a Cache bounds the sum of the footprints of its blocks.
// The footprint of a block is the capacity of its buffer, which exceeds the
// block by at most an eighth. [Cache.Cost] returns the sum.
//
// # Pins
//
// [Cache.PinTable] makes the Cache pin the index partitions, the filter
// partitions and the fragment partitions of a table that AddBlock adds, while
// their footprints fit the PinnedCapacity of [Config]. The pinned partitions count against the
// capacity, so the data blocks take the bytes that the pins leave. The
// evictor does not remove a pinned partition until [Cache.UnpinTable], so a
// table that readers use rarely does not read its partitions again. A
// consumer pins a table before it opens the Reader of the table, and unpins
// it before it deletes the table. [Cache.Pinned] returns the bytes of the
// pins.
//
// # Concurrency
//
// A Cache is safe for concurrent use. Lend takes no lock, because a lookup
// in a cache.Cache takes none. AddBlock of a partition, PinTable and
// UnpinTable lock a mutex of the Cache.
//
// # Allocation contract
//
// Lend, NewID and Pinned do not allocate. AddBlock and Add allocate the entry
// of the cache.Cache, and Add takes its Block from the pool. AddBlock of a
// pinned partition also allocates the growth of the map of the pins of its
// table. Get allocates the copy that it returns.
//
// # Errors
//
// [New] returns [cache.ErrConfig] for a Config with a Capacity that is not
// positive, and for a PinnedCapacity below 0 or above the Capacity.
//
// # Dependency position
//
// Imports bytes, context, sync, sync/atomic and time from the standard
// library, and go.thesmos.sh/core/cache and go.thesmos.sh/core/sstable from
// this module.
package blockcache
