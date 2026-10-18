// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package blockcache

import (
	"bytes"
	"context"
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
}

// Cache is a [sstable.LendingCache] over a [cache.Cache] of blocks. It costs
// a block by its footprint. It releases its reference to a block once the
// block leaves the cache.Cache, which passes a pinned block on at its last
// Unpin. It keeps a block by the identity of its table and its offset, and
// ignores the role of the block. [Cache.NewID] issues the identities of the
// tables that share the cache. The zero Cache is not usable. [New] returns a
// Cache.
//
// # Concurrency
//
// Safe for concurrent use.
//
// # Allocation contract
//
// Lend and NewID do not allocate. AddBlock and Add allocate the entry of the
// cache.Cache, and Get allocates the copy that it returns.
type Cache struct {
	// blocks keeps the blocks by their table and their offset.
	blocks *cache.Cache[key, *sstable.Block]

	// ids is the last identity that NewID returned, or 0 before the first
	// call.
	ids atomic.Uint64
}

var _ sstable.LendingCache = (*Cache)(nil)

// key is the identity of a table and the offset of a block in it.
type key struct {
	id, offset uint64
}

// New returns an empty Cache with the capacity of cfg.
//
// Error modes: a Capacity that is not positive returns [cache.ErrConfig].
//
// # Allocation contract
//
// The Cache and the memory of its cache.Cache.
func New(cfg Config) (*Cache, error) {
	blocks, err := cache.New(cache.Config[key, *sstable.Block]{
		Capacity: cfg.Capacity,
		Cost:     func(b *sstable.Block) int64 { return int64(b.Footprint()) },
		Evicted:  func(_ key, b *sstable.Block) { b.Release() },
	})
	if err != nil {
		return nil, err
	}

	return &Cache{blocks: blocks}, nil
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
// earlier block of that place. It ignores the role of the block. The cache
// takes a reference to b, and releases it once b leaves the cache. A block
// whose footprint exceeds the capacity leaves at once.
//
// # Allocation contract
//
// One allocation for the entry of the cache.Cache.
func (c *Cache) AddBlock(_ context.Context, id, offset uint64, _ sstable.BlockRole, b *sstable.Block) {
	b.Retain()
	c.blocks.Set(key{id, offset}, b, time.Time{})
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

// Len returns the number of blocks in the cache.
//
// # Allocation contract
//
// Zero alloc.
func (c *Cache) Len() int {
	return c.blocks.Len()
}
