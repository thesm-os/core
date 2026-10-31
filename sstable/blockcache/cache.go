// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package blockcache

import (
	"bytes"
	"context"
	"sync"
	"sync/atomic"
	"time"

	"go.thesmos.sh/core/cache"
	"go.thesmos.sh/core/sstable"
)

// Config configures a [Cache].
type Config struct {
	// Capacity bounds the sum of the footprints of the blocks of the cache,
	// in bytes. It must be positive.
	Capacity int64

	// PinnedCapacity bounds the sum of the footprints of the partitions that
	// the cache pins for the tables of [Cache.PinTable], in bytes. The pinned
	// partitions count against Capacity, so PinnedCapacity must be from 0 to
	// Capacity. 0 pins no partition.
	PinnedCapacity int64
}

// Cache is a [sstable.LendingCache] over a [cache.Cache] of blocks. It costs
// a block by its footprint. It releases its reference to a block once the
// block leaves the cache.Cache, which passes a pinned block on at its last
// Unpin. It keeps a block by the identity of its table and its offset.
// [Cache.NewID] issues the identities of the tables that share the cache.
//
// With [Cache.PinTable], a consumer selects a table whose index partitions,
// filter partitions and fragment partitions the Cache stores with a pin of
// [cache.Cache.SetPinned], while their footprints fit the PinnedCapacity of
// [Config]. The evictor does
// not remove a pinned partition until [Cache.UnpinTable]. The Cache keeps the
// [cache.Pinned] of each pinned partition in a map of the pinned tables, by
// its offset.
//
// The zero Cache is not usable. [New] returns a Cache.
//
// # Concurrency
//
// Safe for concurrent use. Lend takes no lock. A mutex guards the map of the
// pinned tables and the sum of the footprints of the pins. AddBlock of a
// partition, PinTable and UnpinTable lock it, and Pinned reads the sum from
// an atomic.
//
// # Allocation contract
//
// Lend, NewID and Pinned do not allocate. AddBlock and Add allocate the
// entry of the cache.Cache, and the growth of the map of its table for a
// pinned partition. Get allocates the copy that it returns.
type Cache struct {
	// blocks keeps the blocks by their table and their offset.
	blocks *cache.Cache[key, *sstable.Block]

	// tables are the pinned partitions of each table of PinTable, by their
	// offset. mu guards it.
	tables map[uint64]map[uint64]cache.Pinned[key, *sstable.Block]

	// ids is the last identity that NewID returned, or 0 before the first
	// call.
	ids atomic.Uint64

	// pinned is the sum of the footprints of the pinned partitions. A write
	// locks mu, and Pinned reads it without the lock.
	pinned atomic.Int64

	// pinnedCapacity is the PinnedCapacity of the Config.
	pinnedCapacity int64

	mu sync.Mutex
}

var _ sstable.LendingCache = (*Cache)(nil)

// key is the identity of a table and the offset of a block in it.
type key struct {
	id, offset uint64
}

// New returns an empty Cache with the capacities of cfg.
//
// Error modes, each [cache.ErrConfig]:
//
//   - A Capacity that is not positive.
//   - A PinnedCapacity below 0 or above the Capacity.
//
// # Allocation contract
//
// The Cache, the map of the pinned tables and the memory of its cache.Cache.
func New(cfg Config) (*Cache, error) {
	if cfg.PinnedCapacity < 0 || cfg.PinnedCapacity > cfg.Capacity {
		return nil, cache.ErrConfig
	}
	blocks, err := cache.New(cache.Config[key, *sstable.Block]{
		Capacity: cfg.Capacity,
		Cost:     func(b *sstable.Block) int64 { return int64(b.Footprint()) },
		Evicted:  func(_ key, b *sstable.Block) { b.Release() },
	})
	if err != nil {
		return nil, err
	}

	return &Cache{
		blocks:         blocks,
		tables:         map[uint64]map[uint64]cache.Pinned[key, *sstable.Block]{},
		pinnedCapacity: cfg.PinnedCapacity,
	}, nil
}

// NewID returns an identity for a table that reads through the cache. A
// consumer passes it as the CacheID of the [sstable.ReaderOptions] of the
// table. NewID returns 1 at its first call and one more at each call after
// it, so it never returns an identity twice. It never returns 0, so none of
// its identities equals the CacheID of a Reader that sets none. NewID does
// not reuse the identity of a table whose blocks left the cache, and each
// cache counts its identities apart from every other cache.
//
// # Allocation contract
//
// Zero alloc.
func (c *Cache) NewID() uint64 {
	return c.ids.Add(1)
}

// PinTable makes the cache pin each index partition, filter partition and
// fragment partition of the table id that AddBlock adds from now on, while
// the footprints of the pinned partitions fit PinnedCapacity. The evictor does not remove a pinned
// partition until [Cache.UnpinTable] of id, and a partition beyond
// PinnedCapacity enters the cache without a pin. A partition that the cache
// contains before PinTable does not get a pin, so a consumer calls PinTable
// before it opens the Reader of the table, whose Open does not add a block. A
// second PinTable of a table does not change the cache.
func (c *Cache) PinTable(id uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.tables[id]; !ok {
		c.tables[id] = map[uint64]cache.Pinned[key, *sstable.Block]{}
	}
}

// UnpinTable removes the pins of the partitions of the table id, and
// subtracts their footprints from [Cache.Pinned]. The partitions remain in
// the cache, and the evictor removes them as other blocks. A consumer calls
// UnpinTable before it deletes the table. The pinned partitions of a table
// that it deletes without UnpinTable keep their bytes until the consumer
// drops the cache. UnpinTable of a table without pins does not change the
// cache.
func (c *Cache) UnpinTable(id uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, p := range c.tables[id] {
		c.pinned.Add(-int64(p.Value().Footprint()))
		p.Unpin()
	}
	delete(c.tables, id)
}

// Lend returns the block of the table id at offset with a reference that
// the caller releases, and reports whether the cache contains the block. It
// ignores the role of the block. It pins the entry of the block while it
// takes the reference. A block that leaves the cache during the Lend then
// keeps its bytes until the caller releases it. A Lend counts as a hit of the
// block.
//
// # Allocation contract
//
// Zero alloc.
func (c *Cache) Lend(_ context.Context, id, offset uint64, _ sstable.BlockRole) (*sstable.Block, bool) {
	p, ok := c.blocks.Pin(key{id, offset})
	if !ok {
		return nil, false
	}
	b := p.Value()
	b.Retain()
	p.Unpin()

	return b, true
}

// AddBlock adds b as the block of the table id at offset, in place of an
// earlier block of that place. The cache takes a reference to b, and
// releases it once b leaves the cache. A block whose footprint exceeds the
// capacity leaves at once.
//
// An index partition, a filter partition or a fragment partition of a table
// of [Cache.PinTable] enters with a pin when its footprint fits the rest of
// PinnedCapacity. When the cache pins the partition at offset already,
// AddBlock does not store b, so the cache keeps one pin and one block of each
// partition. A data block enters without a pin.
//
// # Allocation contract
//
// One allocation for the entry of the cache.Cache. A pinned partition also
// allocates the growth of the map of the pins of its table.
func (c *Cache) AddBlock(_ context.Context, id, offset uint64, role sstable.BlockRole, b *sstable.Block) {
	k := key{id, offset}
	if role != sstable.DataBlock {
		c.mu.Lock()
		pins, ok := c.tables[id]
		_, done := pins[offset]
		n := int64(b.Footprint())
		fits := ok && !done && c.pinned.Load()+n <= c.pinnedCapacity
		if fits {
			b.Retain()
			pins[offset] = c.blocks.SetPinned(k, b, time.Time{})
			c.pinned.Add(n)
		}
		c.mu.Unlock()
		if fits || done {
			return
		}
	}
	b.Retain()
	c.blocks.Set(k, b, time.Time{})
}

// Get returns a copy of the block of the table id at offset, and reports
// whether the cache contains the block. It pins the entry of the block while
// it copies the bytes. A Reader of package sstable calls Lend in its place.
//
// # Allocation contract
//
// One allocation for the copy.
func (c *Cache) Get(_ context.Context, id, offset uint64) ([]byte, bool) {
	p, ok := c.blocks.Pin(key{id, offset})
	if !ok {
		return nil, false
	}
	block := bytes.Clone(p.Value().Bytes())
	p.Unpin()

	return block, true
}

// Add adds a copy of block as the block of the table id at offset, in a
// Block from the pool of package sstable. The cache takes the reference of
// the new Block. A Reader of package sstable calls AddBlock in its place.
//
// # Allocation contract
//
// One allocation for the entry of the cache.Cache, and the buffer of the
// Block when the pool of its class is empty.
func (c *Cache) Add(_ context.Context, id, offset uint64, block []byte) {
	b := sstable.NewBlock(len(block))
	copy(b.Bytes(), block)
	c.blocks.Set(key{id, offset}, b, time.Time{})
}

// Capacity returns the Capacity of the Config of the cache.
//
// # Allocation contract
//
// Zero alloc.
func (c *Cache) Capacity() int64 {
	return c.blocks.Capacity()
}

// Cost returns the sum of the footprints of the blocks in the cache. It is
// at most the capacity, unless the pins of the Lend calls in progress take
// it past.
//
// # Allocation contract
//
// Zero alloc.
func (c *Cache) Cost() int64 {
	return c.blocks.Cost()
}

// Pinned returns the sum of the footprints of the partitions that the cache
// pins for the tables of [Cache.PinTable]. It is at most the PinnedCapacity
// of [Config], and a part of [Cache.Cost]. It reads an atomic, so under
// concurrent writers its result may already be out of date.
//
// # Allocation contract
//
// Zero alloc.
func (c *Cache) Pinned() int64 {
	return c.pinned.Load()
}

// Len returns the number of blocks in the cache.
//
// # Allocation contract
//
// Zero alloc.
func (c *Cache) Len() int {
	return c.blocks.Len()
}
