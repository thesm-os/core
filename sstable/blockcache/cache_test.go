// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package blockcache_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"slices"
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
	// opLend and opAdd are its actions on data blocks, and opPin, opUnpin
	// and opAddPartition its actions on the partitions of the table.
	opLend         = "lend"
	opAdd          = "add"
	opPin          = "pin"
	opUnpin        = "unpin"
	opAddPartition = "add a partition"

	// offsets is the number of offsets of data blocks that its calls choose
	// from, twice the blocks that its cache keeps. The partitions are at the
	// offsets after them.
	offsets = 2 * fewBlocks

	// clients is the number of its clients besides the first.
	clients = 3
)

// The pins of the cases and of the machine of pins.
const (
	// pinnedBlocks is the number of blocks that the PinnedCapacity of a
	// pinning cache bounds.
	pinnedBlocks = 2

	// other is the identity of a table that no case pins and whose offsets
	// have no partition. The cases pin table and table+1.
	other = table + 2

	// churn is the number of data blocks of other that a case adds to evict
	// every block without a pin: three times the blocks of the cache.
	churn = 3 * fewBlocks

	// opPinTable, opUnpinTable, opAddBlock and opChurn are the actions of the
	// machine of pins.
	opPinTable   = "PinTable"
	opUnpinTable = "UnpinTable"
	opAddBlock   = "AddBlock"
	opChurn      = "churn"
)

// pinRoles is the role of each offset of a table in the machine of pins.
// One offset of a table has one role, so the machine never adds a data
// block at the offset of a partition.
var pinRoles = []sstable.BlockRole{sstable.DataBlock, sstable.IndexBlock, sstable.FilterBlock, sstable.IndexBlock}

// place is the identity of a table and the offset of a block in it.
type place struct {
	table, offset uint64
}

// pinned is a partition that the model of the machine of pins pins: the
// pattern of its bytes and its footprint.
type pinned struct {
	pattern   uint64
	footprint int64
}

// addition is the input of an AddBlock of the machine of pins: the place of
// the block and its size in bytes.
type addition struct {
	place

	size int
}

// tableFile is a File over the bytes of a table that counts its reads.
type tableFile struct {
	data  []byte
	reads int
}

var _ sstable.File = (*tableFile)(nil)

// ReadAt reads data at off and counts the read.
func (f *tableFile) ReadAt(_ context.Context, p []byte, off int64) (int, error) {
	f.reads++

	return bytes.NewReader(f.data).ReadAt(p, off)
}

// tableSink is a Sink that appends the bytes of a table to its buffer.
type tableSink struct {
	bytes.Buffer
}

var _ sstable.Sink = (*tableSink)(nil)

// Write appends p to the buffer.
func (s *tableSink) Write(_ context.Context, p []byte) (int, error) {
	return s.Buffer.Write(p)
}

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
			expect.Equal(t, c.Pinned(), int64(0), "a new Cache must pin nothing")
		})

		pinned := []struct {
			name string
			give int64
		}{
			{name: "returns cache.ErrConfig for a negative PinnedCapacity", give: -1},
			{
				name: "returns cache.ErrConfig for a PinnedCapacity above the Capacity",
				give: fewBlocks*blockFootprint + 1,
			},
		}
		for _, tt := range pinned {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				cfg := blockcache.Config{Capacity: fewBlocks * blockFootprint, PinnedCapacity: tt.give}
				c, err := blockcache.New(cfg)
				assert.ErrorIs(t, err, cache.ErrConfig, "New must refuse a PinnedCapacity outside 0 to the Capacity")
				assert.Nil(t, c, "a refused configuration must return no Cache")
			})
		}

		t.Run("returns a Cache for a PinnedCapacity of the Capacity", func(t *testing.T) {
			t.Parallel()
			c, err := blockcache.New(blockcache.Config{
				Capacity: fewBlocks * blockFootprint, PinnedCapacity: fewBlocks * blockFootprint,
			})
			assert.NoError(t, err, "New must accept a PinnedCapacity of the Capacity")
			c.PinTable(table)
			for offset := range uint64(fewBlocks) {
				addBlock(t, c, table, offset, sstable.IndexBlock)
			}
			assert.Equal(t, c.Pinned(), int64(fewBlocks*blockFootprint), "the partitions must take the whole Capacity")
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

	t.Run("PinTable", func(t *testing.T) {
		t.Parallel()

		roles := []struct {
			name string
			give sstable.BlockRole
		}{
			{name: "pins an index partition that AddBlock adds after the call", give: sstable.IndexBlock},
			{name: "pins a filter partition that AddBlock adds after the call", give: sstable.FilterBlock},
		}
		for _, tt := range roles {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				c := pinningCache(t)
				c.PinTable(table)
				addBlock(t, c, table, 0, tt.give)
				evictUnpinned(t, c)
				assert.True(t, hits(t, c, table, 0), "data blocks must not evict a pinned partition")
			})
		}

		t.Run("does not pin a data block", func(t *testing.T) {
			t.Parallel()
			c := pinningCache(t)
			c.PinTable(table)
			addBlock(t, c, table, 0, sstable.DataBlock)
			expect.Equal(t, c.Pinned(), int64(0), "a data block must not count against PinnedCapacity")
			evictUnpinned(t, c)
			expect.False(t, hits(t, c, table, 0), "data blocks must evict a data block of a pinned table")
		})

		t.Run("does not pin a partition beyond the PinnedCapacity", func(t *testing.T) {
			t.Parallel()
			c := pinningCache(t)
			c.PinTable(table)
			for offset := range uint64(pinnedBlocks + 1) {
				addBlock(t, c, table, offset, sstable.IndexBlock)
			}
			expect.Equal(t, c.Pinned(), int64(pinnedBlocks*blockFootprint), "the pins must fill PinnedCapacity")
			evictUnpinned(t, c)
			for offset := range uint64(pinnedBlocks) {
				expect.True(t, hits(t, c, table, offset), "data blocks must not evict a pinned partition")
			}
			expect.False(t, hits(t, c, table, pinnedBlocks), "data blocks must evict the partition over PinnedCapacity")
		})

		t.Run("does not pin a partition that the cache contains before the call", func(t *testing.T) {
			t.Parallel()
			c := pinningCache(t)
			addBlock(t, c, table, 0, sstable.IndexBlock)
			c.PinTable(table)
			evictUnpinned(t, c)
			assert.False(t, hits(t, c, table, 0), "data blocks must evict a partition that PinTable did not pin")
		})

		t.Run("does not pin the partitions of another table", func(t *testing.T) {
			t.Parallel()
			c := pinningCache(t)
			c.PinTable(table)
			addBlock(t, c, table+1, 0, sstable.IndexBlock)
			evictUnpinned(t, c)
			assert.False(t, hits(t, c, table+1, 0), "data blocks must evict a partition of a table without pins")
		})

		t.Run("keeps the pins of a table that it pinned before", func(t *testing.T) {
			t.Parallel()
			c := pinningCache(t)
			c.PinTable(table)
			addBlock(t, c, table, 0, sstable.IndexBlock)
			c.PinTable(table)
			expect.Equal(t, c.Pinned(), int64(blockFootprint), "a second PinTable must keep the pinned bytes")
			c.UnpinTable(table)
			evictUnpinned(t, c)
			expect.False(t, hits(t, c, table, 0), "UnpinTable must remove the pin that the first PinTable made")
		})

		t.Run("keeps the partitions of a rarely read table through the reads of the other tables", func(t *testing.T) {
			t.Parallel()
			// Each of 16 tables has 4,000 entries of 100 bytes in blocks and
			// partitions of 4 KiB: an index partition and two filter
			// partitions. Gets of every 100th key read each partition once. A
			// round of 500 Gets of the first 15 tables then adds more data
			// blocks than the cache keeps beside the pinned partitions, and a
			// Get of the last table follows each round.
			const tables, entries, stride, round, rounds = 16, 4000, 100, 500, 20
			c, err := blockcache.New(blockcache.Config{Capacity: 1 << 20, PinnedCapacity: 1 << 19})
			assert.NoError(t, err, "New must accept a PinnedCapacity below the Capacity")
			var sink tableSink
			opts := sstable.WriterOptions{BlockSize: 4096, FilterBits: 8, PartitionSize: 4096}
			w, err := sstable.NewWriter(&sink, opts)
			assert.NoError(t, err, "NewWriter must accept the options")
			value := bytes.Repeat([]byte{'v'}, 100)
			for i := range entries {
				assert.NoError(t, w.Add(t.Context(), fmt.Appendf(nil, "key %06d", i), 1, value),
					"Add must add the entry")
			}
			_, err = w.Close(t.Context())
			assert.NoError(t, err, "Close must write the table")

			// The tables have the bytes of one table and identities of their
			// own.
			readers, files := make([]*sstable.Reader, 0, tables), make([]*tableFile, 0, tables)
			for range tables {
				id := c.NewID()
				c.PinTable(id)
				f := &tableFile{data: sink.Bytes()}
				r, err := sstable.Open(t.Context(), f, int64(len(f.data)), sstable.ReaderOptions{Cache: c, CacheID: id})
				assert.NoError(t, err, "Open must read the table")
				readers, files = append(readers, r), append(files, f)
			}
			get := func(r *sstable.Reader, i int) {
				_, _, ok, err := r.Get(t.Context(), fmt.Appendf(nil, "key %06d", i), 1, nil)
				assert.NoError(t, err, "Get must read the table")
				assert.True(t, ok, "Get must find the key")
			}
			for _, r := range readers {
				for i := 0; i < entries; i += stride {
					get(r, i)
				}
			}
			rng := rand.New(rand.NewPCG(1, 2))
			last := files[tables-1]
			reads := make([]int, 0, rounds)
			for range rounds {
				for range round {
					get(readers[rng.IntN(tables-1)], rng.IntN(entries))
				}
				before := last.reads
				get(readers[tables-1], rng.IntN(entries))
				reads = append(reads, last.reads-before)
			}
			assert.Equal(t, reads, slices.Repeat([]int{1}, rounds),
				"each Get of the rarely read table must read its data block alone")
		})
	})

	t.Run("UnpinTable", func(t *testing.T) {
		t.Parallel()

		t.Run("lets data blocks evict the partitions of the table", func(t *testing.T) {
			t.Parallel()
			c := pinningCache(t)
			c.PinTable(table)
			addBlock(t, c, table, 0, sstable.IndexBlock)
			c.UnpinTable(table)
			expect.Equal(t, c.Pinned(), int64(0), "UnpinTable must subtract the bytes of the partitions")
			evictUnpinned(t, c)
			expect.False(t, hits(t, c, table, 0), "data blocks must evict an unpinned partition")
		})

		t.Run("stops the pins of the partitions that AddBlock adds after the call", func(t *testing.T) {
			t.Parallel()
			c := pinningCache(t)
			c.PinTable(table)
			c.UnpinTable(table)
			addBlock(t, c, table, 0, sstable.IndexBlock)
			expect.Equal(t, c.Pinned(), int64(0), "a partition after UnpinTable must not count against PinnedCapacity")
			evictUnpinned(t, c)
			expect.False(t, hits(t, c, table, 0), "data blocks must evict a partition after UnpinTable")
		})

		t.Run("keeps the pins of the other tables", func(t *testing.T) {
			t.Parallel()
			c := pinningCache(t)
			c.PinTable(table)
			c.PinTable(table + 1)
			addBlock(t, c, table, 0, sstable.IndexBlock)
			addBlock(t, c, table+1, 0, sstable.IndexBlock)
			c.UnpinTable(table)
			expect.Equal(t, c.Pinned(), int64(blockFootprint), "the partition of the other table must keep its bytes")
			evictUnpinned(t, c)
			expect.True(t, hits(t, c, table+1, 0), "data blocks must not evict the partition of the other table")
		})

		t.Run("does not change the pins of a cache for a table without pins", func(t *testing.T) {
			t.Parallel()
			c := pinningCache(t)
			c.PinTable(table)
			addBlock(t, c, table, 0, sstable.IndexBlock)
			c.UnpinTable(table + 1)
			assert.Equal(t, c.Pinned(), int64(blockFootprint), "UnpinTable of another table must keep the pins")
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

		t.Run("keeps the first block of a pinned partition that a second AddBlock adds again", func(t *testing.T) {
			t.Parallel()
			c := pinningCache(t)
			c.PinTable(table)
			first, second := filledBlock(0), filledBlock(1)
			c.AddBlock(t.Context(), table, 0, sstable.IndexBlock, first)
			c.AddBlock(t.Context(), table, 0, sstable.IndexBlock, second)
			got, ok := c.Lend(t.Context(), table, 0, sstable.IndexBlock)
			assert.True(t, ok, "Lend must find the pinned partition")
			expect.Equal(t, got, first, "Lend must return the Block of the pin", assert.ByIdentity())
			expect.Equal(t, c.Len(), 1, "the second Block must not enter the cache")
			expect.Equal(t, c.Pinned(), int64(blockFootprint), "the partition must count against PinnedCapacity once")
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

	t.Run("Pinned", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the sum of the footprints of the pinned partitions", func(t *testing.T) {
			t.Parallel()
			c := pinningCache(t)
			c.PinTable(table)
			small, large := sstable.NewBlock(blockBytes/2), filledBlock(1)
			c.AddBlock(t.Context(), table, 0, sstable.IndexBlock, small)
			c.AddBlock(t.Context(), table, 1, sstable.FilterBlock, large)
			assert.Equal(t, c.Pinned(), int64(small.Footprint()+large.Footprint()),
				"Pinned must sum the footprints of the pinned partitions")
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

	t.Run("pins partitions by a model of the pins under any sequence of calls", func(t *testing.T) {
		t.Parallel()
		prop.ForAll(t, "every partition that the model pins must remain in the cache", pinSteps)
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
		t.Run("of a data block", func(t *testing.T) {
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

		t.Run("of a pinned partition", func(t *testing.T) {
			// The cache pins every partition of the test, so no Release returns
			// a buffer to the pool, and setup builds each Block outside the
			// count.
			c, err := blockcache.New(blockcache.Config{
				Capacity: 1024 * blockFootprint, PinnedCapacity: 1024 * blockFootprint,
			})
			assert.NoError(t, err, "New must accept a PinnedCapacity of the Capacity")
			c.PinTable(table)
			offset := uint64(0)
			expect.MaxAllocsWithSetup(t, func() *sstable.Block { return sstable.NewBlock(blockBytes) },
				func(b *sstable.Block) {
					c.AddBlock(t.Context(), table, offset, sstable.IndexBlock, b)
					b.Release()
					offset++
				}, 1, "an AddBlock of a pinned partition must allocate the entry of the cache and the growth of the pins")
			expect.Equal(t, c.Pinned(), int64(offset)*blockFootprint, "the test must measure pinned partitions")
		})
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
		b.Run("of a data block", func(b *testing.B) {
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

		b.Run("of a pinned partition", func(b *testing.B) {
			// The tablePartitions partitions of each table fill PinnedCapacity,
			// and the next table evicts them after UnpinTable, so the pool lends
			// their buffers again once the warm-up has filled the cache. The map
			// of the pins of a table grows a few times for its partitions.
			const tablePartitions = 64
			c, err := blockcache.New(blockcache.Config{
				Capacity: 2 * tablePartitions * blockFootprint, PinnedCapacity: tablePartitions * blockFootprint,
			})
			assert.NoError(b, err, "New must accept a PinnedCapacity below the Capacity")
			id, offset := uint64(table), uint64(0)
			c.PinTable(id)

			bc := bench.Start(b).Warmup(4 * tablePartitions).MaxAllocs(1)
			defer bc.End()

			for bc.Loop() {
				if offset == tablePartitions {
					c.UnpinTable(id)
					id, offset = id+1, 0
					c.PinTable(id)
				}
				blk := sstable.NewBlock(blockBytes)
				c.AddBlock(b.Context(), id, offset, sstable.IndexBlock, blk)
				blk.Release()
				offset++
			}

			assert.Equal(b, c.Pinned(), int64(offset)*blockFootprint, "the benchmark must measure pinned partitions")
		})
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

// pinningCache returns an empty Cache with room for fewBlocks blocks of
// blockBytes, which pins up to pinnedBlocks of them.
func pinningCache(tb assert.TB) *blockcache.Cache {
	tb.Helper()

	c, err := blockcache.New(blockcache.Config{
		Capacity: fewBlocks * blockFootprint, PinnedCapacity: pinnedBlocks * blockFootprint,
	})
	assert.NoError(tb, err, "New must accept a PinnedCapacity below the Capacity")

	return c
}

// addBlock adds the block of filledBlock(offset) at offset of the table id
// under role, and releases the reference of the caller.
func addBlock(tb assert.TB, c *blockcache.Cache, id, offset uint64, role sstable.BlockRole) {
	tb.Helper()

	b := filledBlock(offset)
	c.AddBlock(assert.Context(tb), id, offset, role, b)
	b.Release()
}

// evictUnpinned adds churn data blocks of the table other, which evict every
// block of the cache that has no pin.
func evictUnpinned(tb assert.TB, c *blockcache.Cache) {
	tb.Helper()

	for offset := range uint64(churn) {
		addBlock(tb, c, other, offset, sstable.DataBlock)
	}
}

// hits reports whether c lends the block at offset of the table id, and
// releases the block that it lends.
func hits(tb assert.TB, c *blockcache.Cache, id, offset uint64) bool {
	tb.Helper()

	b, ok := c.Lend(assert.Context(tb), id, offset, sstable.IndexBlock)
	if ok {
		b.Release()
	}

	return ok
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

// lendSteps runs a machine of Lend, AddBlock, PinTable and UnpinTable on a
// Cache of fewBlocks blocks, which pins up to pinnedBlocks of them, with
// clients that call it at once. AddBlock adds a block that filledBlock fills
// for its offset: a data block at an offset below offsets, and an index
// partition at an offset after them. The calls evict blocks, and the pool
// lends their buffers again. Lend checks that the block that it lends keeps
// the bytes of its offset until its Release.
func lendSteps(pc *prop.Case) {
	c := pinningCache(pc)

	offset := func(pc *prop.Case, _ struct{}) any { return pc.Draw(prop.Integer[uint64](0, offsets-1), "offset") }
	add := func(pc *prop.Case, offset uint64, role sstable.BlockRole) {
		b := filledBlock(offset)
		c.AddBlock(pc.Context(), table, offset, role, b)
		b.Release()
	}
	stateful.Steps(pc, stateful.Machine[struct{}]{
		Actions: []stateful.Action[struct{}]{
			{
				Name:  opAdd,
				Input: offset,
				Run:   func(pc *prop.Case, _ int, in any) { add(pc, in.(uint64), role) },
			},
			{
				Name:  opAddPartition,
				Input: offset,
				Run:   func(pc *prop.Case, _ int, in any) { add(pc, offsets+in.(uint64), sstable.IndexBlock) },
			},
			{
				Name:  opPin,
				Input: func(*prop.Case, struct{}) any { return nil },
				Run:   func(*prop.Case, int, any) { c.PinTable(table) },
			},
			{
				Name:  opUnpin,
				Input: func(*prop.Case, struct{}) any { return nil },
				Run:   func(*prop.Case, int, any) { c.UnpinTable(table) },
			},
			{
				Name:  opLend,
				Input: func(pc *prop.Case, _ struct{}) any { return pc.Draw(prop.Integer[uint64](0, 2*offsets-1), "offset") },
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

// pinSteps runs a machine of PinTable, UnpinTable, AddBlock and data blocks
// of the table other on a Cache that pins up to pinnedBlocks blocks.
// AddBlock adds a block of a drawn table, offset and size, under the role of
// its offset in pinRoles, with a pattern that no other call uses. A model
// keeps the tables of PinTable and the partitions that the cache pins: a
// partition of a pinned table that the cache does not pin yet, while the
// footprints fit PinnedCapacity. After every step the invariant checks that
// Pinned returns the sum of the footprints of the model, that the cache
// lends every partition of the model with its pattern, and that Cost is at
// most the Capacity.
func pinSteps(pc *prop.Case) {
	c := pinningCache(pc)
	tables := map[uint64]bool{}
	pins := map[place]pinned{}
	var sum int64
	pattern := uint64(churn)

	id := func(pc *prop.Case, _ struct{}) any { return pc.Draw(prop.Integer[uint64](table, table+1), "table") }
	stateful.Steps(pc, stateful.Machine[struct{}]{
		Actions: []stateful.Action[struct{}]{
			{
				Name:  opPinTable,
				Input: id,
				Run: func(_ *prop.Case, _ int, in any) {
					c.PinTable(in.(uint64))
					tables[in.(uint64)] = true
				},
			},
			{
				Name:  opUnpinTable,
				Input: id,
				Run: func(_ *prop.Case, _ int, in any) {
					c.UnpinTable(in.(uint64))
					delete(tables, in.(uint64))
					for at, p := range pins {
						if at.table == in.(uint64) {
							delete(pins, at)
							sum -= p.footprint
						}
					}
				},
			},
			{
				Name: opAddBlock,
				Input: func(pc *prop.Case, _ struct{}) any {
					return addition{
						table:  pc.Draw(prop.Integer[uint64](table, table+1), "table"),
						offset: pc.Draw(prop.Integer[uint64](0, uint64(len(pinRoles)-1)), "offset"),
						size:   pc.Draw(prop.SampledFrom(blockBytes, blockBytes/2), "size"),
					}
				},
				Run: func(pc *prop.Case, _ int, in any) {
					give := in.(addition)
					pattern++
					b := sstable.NewBlock(give.size)
					for i := 0; i < give.size; i += 8 {
						binary.LittleEndian.PutUint64(b.Bytes()[i:], pattern)
					}
					role := pinRoles[give.offset]
					c.AddBlock(pc.Context(), give.table, give.offset, role, b)
					footprint := int64(b.Footprint())
					b.Release()

					_, done := pins[give.place]
					if role != sstable.DataBlock && tables[give.table] && !done &&
						sum+footprint <= pinnedBlocks*blockFootprint {

						pins[give.place] = pinned{pattern: pattern, footprint: footprint}
						sum += footprint
					}
				},
			},
			{
				Name:  opChurn,
				Input: func(*prop.Case, struct{}) any { return nil },
				Run:   func(pc *prop.Case, _ int, _ any) { evictUnpinned(pc, c) },
			},
		},
		Invariant: func(pc *prop.Case, _ struct{}) {
			assert.Equal(pc, c.Pinned(), sum, "Pinned must return the bytes of the pins of the model")
			for at, p := range pins {
				b, ok := c.Lend(pc.Context(), at.table, at.offset, pinRoles[at.offset])
				assert.True(pc, ok, "the cache must lend every partition that it pins")
				assert.True(pc, repeats(b, p.pattern), "a pinned partition must keep the bytes of its AddBlock")
				b.Release()
			}
			assert.InRange(pc, c.Cost(), 0, float64(c.Capacity()), "the blocks must cost at most the Capacity")
		},
	})
}
