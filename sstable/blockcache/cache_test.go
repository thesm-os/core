// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package blockcache_test

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.thesmos.sh/core/cache"
	"go.thesmos.sh/core/sstable"
	"go.thesmos.sh/core/sstable/blockcache"
)

// The cases use blocks of blockBytes bytes in buffers of the class of
// blockBytes.
const (
	// blockBytes is the size of each block.
	blockBytes = 4096

	// blockFootprint is the footprint of a block of blockBytes.
	blockFootprint = 4096

	// fewBlocks is the number of blocks that the cache of a case keeps.
	fewBlocks = 4

	// table is the identity of the table of every block.
	table = 7

	// role is the role of every block, except in the case that adds a block
	// under another role.
	role = sstable.DataBlock
)

// The concurrent calls of NewID.
const (
	// opNewID is the name of the calls in the history.
	opNewID = "NewID"

	// idClients is the number of the clients that call NewID at once.
	idClients = 4

	// identities is the number of the identities that each client takes.
	identities = 64
)

// The machine of concurrent lends and adds.
const (
	// opLend and opAdd are its actions.
	opLend = "lend"
	opAdd  = "add"

	// offsets is the number of offsets that its calls choose from, twice
	// the blocks that its cache keeps.
	offsets = 2 * fewBlocks

	// clients is the number of its clients besides the first.
	clients = 3
)

func TestCache(t *testing.T) {
	t.Parallel()

	t.Run("New", func(t *testing.T) {
		t.Parallel()

		t.Run("returns cache.ErrConfig for a Capacity that is not positive", func(t *testing.T) {
			t.Parallel()
			c, err := blockcache.New(blockcache.Config{})
			assert.ErrorIs(t, err, cache.ErrConfig, "New must refuse a Capacity of 0")
			assert.Nil(t, c, "a refused configuration must return no Cache")
		})

		t.Run("returns an empty Cache", func(t *testing.T) {
			t.Parallel()
			c := newCache(t)
			expect.Equal(t, c.Len(), 0, "a new Cache must contain no block")
			expect.Equal(t, c.Cost(), int64(0), "a new Cache must cost nothing")
		})
	})

	t.Run("NewID", func(t *testing.T) {
		t.Parallel()

		t.Run("returns 1 at its first call", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, newCache(t).NewID(), uint64(1), "the first identity of a Cache must be 1")
		})

		t.Run("returns one more than the identity of the call before", func(t *testing.T) {
			t.Parallel()
			c := newCache(t)
			var last uint64
			assert.Accumulates(t, func(struct{}) error {
				last = c.NewID()

				return nil
			}, struct{}{}, func() int { return int(last) }, "each NewID must return one more than the NewID before it")
		})

		t.Run("returns the identities of one sequence of calls to concurrent callers", func(t *testing.T) {
			t.Parallel()
			c := newCache(t)
			h := history.New()
			outcomes := history.Concurrently(idClients, 10*time.Second, func(client int) (any, error) {
				for range identities {
					call := h.Invoke(client, opNewID, nil)
					call.OK(c.NewID())
				}

				return nil, nil
			})
			for _, o := range outcomes {
				assert.True(t, o.Finished, "every client must finish within the time of Concurrently")
			}
			history.Linearizable(t, h, history.Spec[uint64]{
				Initial: func() uint64 { return 0 },
				Next: func(last uint64, op history.Operation) []uint64 {
					if !op.Returned(last + 1) {
						return nil
					}

					return []uint64{last + 1}
				},
			}, "concurrent calls of NewID must return 1, 2, 3 and on in one order of the calls")
		})
	})

	t.Run("Lend", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the block that AddBlock added", func(t *testing.T) {
			t.Parallel()
			c := newCache(t)
			b := filledBlock(0)
			c.AddBlock(t.Context(), table, 0, role, b)
			got, ok := c.Lend(t.Context(), table, 0, role)
			assert.True(t, ok, "Lend must find the added block")
			expect.Equal(t, got, b, "Lend must return the added Block", assert.ByIdentity())
			got.Release()
			b.Release()
		})

		t.Run("returns the block that AddBlock added under another role", func(t *testing.T) {
			t.Parallel()
			c := newCache(t)
			b := filledBlock(0)
			c.AddBlock(t.Context(), table, 0, sstable.IndexBlock, b)
			got, ok := c.Lend(t.Context(), table, 0, sstable.DataBlock)
			assert.True(t, ok, "Lend must find the block whatever role AddBlock received")
			expect.Equal(t, got, b, "Lend must return the added Block", assert.ByIdentity())
			got.Release()
			b.Release()
		})

		t.Run("reports false for a block that the cache does not contain", func(t *testing.T) {
			t.Parallel()
			c := newCache(t)
			got, ok := c.Lend(t.Context(), table, 0, role)
			expect.False(t, ok, "Lend must miss an absent block")
			expect.Nil(t, got, "a miss must return no Block")
		})

		t.Run("keeps the bytes of a lent block after the block leaves the cache", func(t *testing.T) {
			t.Parallel()
			c := newCache(t)
			b := filledBlock(0)
			c.AddBlock(t.Context(), table, 0, role, b)
			b.Release()
			lent, ok := c.Lend(t.Context(), table, 0, role)
			assert.True(t, ok, "Lend must find the added block")
			// Each AddBlock replaces the block of offset 0, and the block that
			// it replaces returns its buffer to the pool for the next one.
			for pattern := range uint64(2 * fewBlocks) {
				other := filledBlock(pattern + 1)
				c.AddBlock(t.Context(), table, 0, role, other)
				other.Release()
			}
			assert.True(t, repeats(lent, 0), "the lent block must keep its bytes until its Release")
			lent.Release()
		})
	})

	t.Run("AddBlock", func(t *testing.T) {
		t.Parallel()

		t.Run("keeps a reference of its own to the block", func(t *testing.T) {
			t.Parallel()
			c := newCache(t)
			b := filledBlock(0)
			c.AddBlock(t.Context(), table, 0, role, b)
			b.Release()
			for offset := range uint64(fewBlocks - 1) {
				other := filledBlock(offset + 1)
				c.AddBlock(t.Context(), table, offset+1, role, other)
				other.Release()
			}
			got, ok := c.Lend(t.Context(), table, 0, role)
			assert.True(t, ok, "the cache must keep the block after the caller released it")
			expect.True(t, repeats(got, 0), "the block must keep its bytes while the cache keeps it")
			got.Release()
		})

		t.Run("replaces the block of its key", func(t *testing.T) {
			t.Parallel()
			c := newCache(t)
			first, second := filledBlock(0), filledBlock(1)
			c.AddBlock(t.Context(), table, 0, role, first)
			c.AddBlock(t.Context(), table, 0, role, second)
			got, ok := c.Lend(t.Context(), table, 0, role)
			assert.True(t, ok, "Lend must find the block")
			expect.Equal(t, got, second, "Lend must return the later Block", assert.ByIdentity())
			expect.Equal(t, c.Len(), 1, "a replaced block must leave the cache")
			got.Release()
			first.Release()
			second.Release()
		})
	})

	t.Run("Get", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a copy of the block", func(t *testing.T) {
			t.Parallel()
			c := newCache(t)
			b := filledBlock(0)
			c.AddBlock(t.Context(), table, 0, role, b)
			got, ok := c.Get(t.Context(), table, 0)
			assert.True(t, ok, "Get must find the added block")
			assert.Equal(t, got, b.Bytes(), "Get must return the bytes of the block")
			got[0]++
			expect.True(t, repeats(b, 0), "a write to the copy must not change the block")
			b.Release()
		})

		t.Run("reports false for a block that the cache does not contain", func(t *testing.T) {
			t.Parallel()
			c := newCache(t)
			got, ok := c.Get(t.Context(), table, 0)
			expect.False(t, ok, "Get must miss an absent block")
			expect.Nil(t, got, "a miss must return no bytes")
		})
	})

	t.Run("Add", func(t *testing.T) {
		t.Parallel()

		t.Run("adds a copy of the block", func(t *testing.T) {
			t.Parallel()
			c := newCache(t)
			b := filledBlock(0)
			block := bytes.Clone(b.Bytes())
			b.Release()
			// The pool lends the buffer of the last Release first, so Add
			// copies over the bytes of another offset.
			other := filledBlock(1)
			other.Release()
			c.Add(t.Context(), table, 0, block)
			block[0]++
			got, ok := c.Lend(t.Context(), table, 0, role)
			assert.True(t, ok, "Lend must find the added block")
			expect.True(t, repeats(got, 0), "a write to the bytes of Add must not change the block")
			got.Release()
		})
	})

	t.Run("Capacity", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the Capacity of its Config", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, newCache(t).Capacity(), int64(fewBlocks*blockFootprint),
				"Capacity must return the Capacity of the Config")
		})
	})

	t.Run("Cost", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the sum of the footprints of the blocks", func(t *testing.T) {
			t.Parallel()
			c := newCache(t)
			small, large := sstable.NewBlock(blockBytes/2), filledBlock(1)
			c.AddBlock(t.Context(), table, 0, role, small)
			c.AddBlock(t.Context(), table, 1, role, large)
			assert.Equal(t, c.Cost(), int64(small.Footprint()+large.Footprint()),
				"Cost must sum the footprints of the blocks")
			small.Release()
			large.Release()
		})
	})

	t.Run("Len", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the number of blocks", func(t *testing.T) {
			t.Parallel()
			c := fullCache(t)
			assert.Equal(t, c.Len(), fewBlocks, "Len must count the blocks")
		})
	})

	t.Run("lends blocks whose bytes do not change while goroutines add other blocks", func(t *testing.T) {
		t.Parallel()
		prop.ForAll(t, "every lent block must keep the bytes of its offset", lendSteps)
	})
}

// TestCacheAllocs checks the allocation contract of a Cache whose blocks
// fill it. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
func TestCacheAllocs(t *testing.T) {
	t.Run("Lend", func(t *testing.T) {
		t.Run("of a block in the cache", func(t *testing.T) {
			c := fullCache(t)
			var ok bool
			expect.MaxAllocs(t, func() {
				var b *sstable.Block
				b, ok = c.Lend(t.Context(), table, 0, role)
				b.Release()
			}, 0, "a Lend of a block in the cache and its Release must not allocate")
			expect.True(t, ok, "the test must measure a hit")
		})

		t.Run("of a block that AddBlock replaces after the Release", func(t *testing.T) {
			c := fullCache(t)
			var ok bool
			expect.MaxAllocs(t, func() {
				var b *sstable.Block
				b, ok = c.Lend(t.Context(), table, 0, role)
				b.Release()
				next := sstable.NewBlock(blockBytes)
				c.AddBlock(t.Context(), table, 0, role, next)
				next.Release()
			}, 1, "the replaced block must return its buffer to the pool for the next block")
			expect.True(t, ok, "the test must measure a hit")
		})
	})

	t.Run("AddBlock", func(t *testing.T) {
		c := fullCache(t)
		offset := uint64(fewBlocks)
		expect.MaxAllocs(t, func() {
			b := sstable.NewBlock(blockBytes)
			c.AddBlock(t.Context(), table, offset, role, b)
			b.Release()
			offset++
		}, 1, "an AddBlock that evicts a block must allocate the entry of the cache alone")
		expect.Equal(t, c.Len(), fewBlocks, "the test must measure AddBlock calls that evict")
	})

	t.Run("Get", func(t *testing.T) {
		t.Run("of a block in the cache", func(t *testing.T) {
			c := fullCache(t)
			var ok bool
			expect.MaxAllocs(t, func() { _, ok = c.Get(t.Context(), table, 0) }, 1,
				"a Get of a block in the cache must allocate the copy alone")
			expect.True(t, ok, "the test must measure a hit")
		})

		t.Run("of a block that AddBlock replaces after the copy", func(t *testing.T) {
			c := fullCache(t)
			var ok bool
			expect.MaxAllocs(t, func() {
				_, ok = c.Get(t.Context(), table, 0)
				next := sstable.NewBlock(blockBytes)
				c.AddBlock(t.Context(), table, 0, role, next)
				next.Release()
			}, 2, "the replaced block must return its buffer to the pool for the next block")
			expect.True(t, ok, "the test must measure a hit")
		})
	})

	t.Run("Add", func(t *testing.T) {
		c := fullCache(t)
		block := make([]byte, blockBytes)
		offset := uint64(fewBlocks)
		expect.MaxAllocs(t, func() {
			c.Add(t.Context(), table, offset, block)
			offset++
		}, 1, "an Add that evicts a block must allocate the entry of the cache alone")
		expect.Equal(t, c.Len(), fewBlocks, "the test must measure Add calls that evict")
	})
}

// BenchmarkCache reports the cost of the methods of a Cache whose blocks
// fill it, and fails when a method allocates more than its contract allows.
func BenchmarkCache(b *testing.B) {
	b.Run("Lend", func(b *testing.B) {
		c := fullCache(b)
		var ok bool

		bc := bench.Start(b).MaxAllocs(0)
		defer bc.End()

		for bc.Loop() {
			var blk *sstable.Block
			blk, ok = c.Lend(b.Context(), table, 0, role)
			blk.Release()
		}

		assert.True(b, ok, "the benchmark must measure hits")
	})

	b.Run("AddBlock", func(b *testing.B) {
		c := fullCache(b)
		offset := uint64(fewBlocks)

		bc := bench.Start(b).MaxAllocs(1)
		defer bc.End()

		for bc.Loop() {
			blk := sstable.NewBlock(blockBytes)
			c.AddBlock(b.Context(), table, offset, role, blk)
			blk.Release()
			offset++
		}

		assert.Equal(b, c.Len(), fewBlocks, "the benchmark must measure AddBlock calls that evict")
	})
}

// newCache returns an empty Cache with room for fewBlocks blocks of
// blockBytes.
func newCache(tb testing.TB) *blockcache.Cache {
	tb.Helper()

	c, err := blockcache.New(blockcache.Config{Capacity: fewBlocks * blockFootprint})
	assert.NoError(tb, err, "New must accept a positive Capacity")

	return c
}

// fullCache returns a Cache whose fewBlocks blocks at the offsets 0 to
// fewBlocks-1 fill it. The cache keeps the only reference to each block.
func fullCache(tb testing.TB) *blockcache.Cache {
	tb.Helper()

	c := newCache(tb)
	for offset := range uint64(fewBlocks) {
		b := filledBlock(offset)
		c.AddBlock(tb.Context(), table, offset, role, b)
		b.Release()
	}

	return c
}

// filledBlock returns a block of blockBytes whose bytes repeat offset as a
// little-endian uint64, with one reference.
func filledBlock(offset uint64) *sstable.Block {
	b := sstable.NewBlock(blockBytes)
	for i := 0; i < blockBytes; i += 8 {
		binary.LittleEndian.PutUint64(b.Bytes()[i:], offset)
	}

	return b
}

// repeats reports whether every 8 bytes of b repeat offset as a
// little-endian uint64, as filledBlock writes them.
func repeats(b *sstable.Block, offset uint64) bool {
	for i := 0; i < len(b.Bytes()); i += 8 {
		if binary.LittleEndian.Uint64(b.Bytes()[i:]) != offset {
			return false
		}
	}

	return true
}

// lendSteps runs a machine of Lend and AddBlock on a Cache of fewBlocks
// blocks, with clients that call it at once. AddBlock adds a block that
// filledBlock fills for its offset, so the calls evict blocks and the pool
// lends their buffers again. Lend checks that the block that it lends keeps
// the bytes of its offset until its Release.
func lendSteps(pc *prop.Case) {
	c, err := blockcache.New(blockcache.Config{Capacity: fewBlocks * blockFootprint})
	assert.NoError(pc, err, "New must accept a positive Capacity")

	offset := func(pc *prop.Case, _ struct{}) any { return pc.Draw(prop.Integer[uint64](0, offsets-1), "offset") }
	stateful.Steps(pc, stateful.Machine[struct{}]{
		Actions: []stateful.Action[struct{}]{
			{
				Name:  opAdd,
				Input: offset,
				Run: func(pc *prop.Case, _ int, in any) {
					b := filledBlock(in.(uint64))
					c.AddBlock(pc.Context(), table, in.(uint64), role, b)
					b.Release()
				},
			},
			{
				Name:  opLend,
				Input: offset,
				Run: func(pc *prop.Case, _ int, in any) {
					b, ok := c.Lend(pc.Context(), table, in.(uint64), role)
					if !ok {
						return
					}
					assert.True(pc, repeats(b, in.(uint64)), "a lent block must keep the bytes of its offset")
					b.Release()
				},
			},
		},
	}, stateful.Clients(clients))
}
