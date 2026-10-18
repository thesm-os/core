// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package blockcache

import (
	"bytes"
	"context"
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
// Unpin. The zero Cache is not usable. [New] returns a Cache.
//
// # Concurrency
//
// Safe for concurrent use.
//
// # Allocation contract
//
// Lend does not allocate. AddBlock and Add allocate the entry of the
// cache.Cache, and Get allocates the copy that it returns.
type Cache struct {
	// blocks keeps the blocks by their table and their offset.
	blocks *cache.Cache[key, *sstable.Block]
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

// Lend returns the block of the table id at offset with a reference that
// the caller releases, and reports whether the cache contains the block. It
// pins the entry of the block while it takes the reference. A block that
// leaves the cache during the Lend then keeps its bytes until the caller
// releases it. A Lend counts as a hit of the block.
//
// # Allocation contract
//
// Zero alloc.
func (c *Cache) Lend(_ context.Context, id, offset uint64) (*sstable.Block, bool) {
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
// # Allocation contract
//
// One allocation for the entry of the cache.Cache.
func (c *Cache) AddBlock(_ context.Context, id, offset uint64, b *sstable.Block) {
	b.Retain()
	c.blocks.Set(key{id, offset}, b, time.Time{})
}

// Get returns a copy of the block of the table id at offset, and reports
// whether the cache contains the block. A Reader of package sstable calls
// Lend in its place.
//
// # Allocation contract
//
// One allocation for the copy.
func (c *Cache) Get(ctx context.Context, id, offset uint64) ([]byte, bool) {
	b, ok := c.Lend(ctx, id, offset)
	if !ok {
		return nil, false
	}
	block := bytes.Clone(b.Bytes())
	b.Release()

	return block, true
}

// Add adds a copy of block as the block of the table id at offset, in a
// Block from the pool of package sstable. A Reader of package sstable calls
// AddBlock in its place.
//
// # Allocation contract
//
// One allocation for the entry of the cache.Cache, and the buffer of the
// Block when the pool of its class is empty.
func (c *Cache) Add(ctx context.Context, id, offset uint64, block []byte) {
	b := sstable.NewBlock(len(block))
	copy(b.Bytes(), block)
	c.AddBlock(ctx, id, offset, b)
	b.Release()
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
