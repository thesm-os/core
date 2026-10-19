// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sstable_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"maps"
	"math"
	"math/rand/v2"
	"runtime"
	"slices"
	"strconv"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.thesmos.sh/core/sstable"
	"go.thesmos.sh/core/sstable/blockcache"
)

// The workload of the check of the false positives of a filter.
const (
	// absentKeys is the number of keys that the table does not contain.
	absentKeys = 10000

	// falsePositives is the most of them that a filter of 16 bits may admit.
	// The expectation is 0.15.
	falsePositives = 5
)

// changeContract is the contract of openChanged, which TestReader and
// FuzzOpen check.
const changeContract = "Open and every read of a changed table return the entries or ErrCorrupt, and never panic"

// The machine of concurrent reads.
const (
	// readContract is the contract of readSteps.
	readContract = "concurrent reads of one Reader must return what one sequence of reads returns"

	// readGet, readMayContain and readSpan are the names of the reads of the
	// machine that call Get, MayContain and Span. The reads that seek have
	// the names of their moves.
	readGet        = "Get"
	readMayContain = "MayContain"
	readSpan       = "Span"

	// seekReads is the number of entries that a read of the machine reads
	// after its seek.
	seekReads = 3

	// readers is the number of clients of the machine besides the first.
	readers = 3
)

// absentKey is a key that the golden table does not contain. It sorts
// between two keys of the table. A filter of 16 bits refuses it, and a
// lookup that skips the filter reads the block of the keys around it.
const absentKey = "key 030a"

// The entries that the cases of Span change in the index of the golden table
// under oneEntry. The table has a data block for each of its 120 entries.
// The search for the lower "key 000" compares the entries 60, 30, 15, 7, 3, 1
// and 0. The search for an upper after every key starts at the first block.
// It compares the entries 0, 1, 3, 7, 15, 31 and 63, then searches the
// entries 64 to 119.
const (
	// lowerSearchEntry is an entry that the search for the lower "key 000"
	// compares, and the search for an upper after every key does not.
	lowerSearchEntry = 30

	// upperSearchEntry is an entry that the search for an upper after every
	// key compares, and the search for the lower "key 000" does not.
	upperSearchEntry = 63

	// key058Entry and key059Entry are the entries of the blocks of key 058
	// and key 059 at the sequence 2. The value of each ends less than 127
	// bytes before the end of the entries of the index.
	key058Entry = 116
	key059Entry = 118
)

// missStride is the number of keys from the key of one Get to the next in
// the cases that miss a cache of four blocks. Fourteen entries of the golden
// table span about three blocks of smallBlocks.
const missStride = 7

// The table of the benchmark of Span.
const (
	// spanEntries is the number of its entries.
	spanEntries = 100_000

	// spanKey is the format of the key of its entry i.
	spanKey = "key %08d"
)

// shortFile is a File that returns one byte less than each read, without
// an error.
type shortFile struct {
	data []byte
}

var _ sstable.File = shortFile{}

// ReadAt reads one byte less than p at off and returns a nil error.
func (r shortFile) ReadAt(_ context.Context, p []byte, off int64) (int, error) {
	n, _ := bytes.NewReader(r.data).ReadAt(p[:len(p)-1], off)

	return n, nil
}

// lookup is the offset and the role of a block that a roleCache lent or
// received.
type lookup struct {
	offset uint64
	role   sstable.BlockRole
}

// roleCache is a LendingCache over a blockcache.Cache that records the
// offset and the role of each Lend and each AddBlock in the order of the
// calls. A case reads through it on one goroutine.
type roleCache struct {
	*blockcache.Cache

	lends, adds []lookup
}

var _ sstable.LendingCache = (*roleCache)(nil)

// Lend records the offset and the role of the block, and lends the block of
// the cache.
func (c *roleCache) Lend(ctx context.Context, id, offset uint64, role sstable.BlockRole) (*sstable.Block, bool) {
	c.lends = append(c.lends, lookup{offset: offset, role: role})

	return c.Cache.Lend(ctx, id, offset, role)
}

// AddBlock records the offset and the role of the block, and adds b to the
// cache.
func (c *roleCache) AddBlock(ctx context.Context, id, offset uint64, role sstable.BlockRole, b *sstable.Block) {
	c.adds = append(c.adds, lookup{offset: offset, role: role})
	c.Cache.AddBlock(ctx, id, offset, role, b)
}

func TestReader(t *testing.T) {
	t.Parallel()

	t.Run("returns what one sequence of reads returns for concurrent reads through one cache", func(t *testing.T) {
		t.Parallel()
		prop.ForAll(t, readContract, func(c *prop.Case) { readSteps(c, &mapCache{}) })
	})

	t.Run("returns what sequential reads return for concurrent reads through a lending cache", func(t *testing.T) {
		t.Parallel()
		// The cache keeps two blocks, so the reads evict the blocks of each
		// other and the pool lends the buffers of the evicted blocks again.
		prop.ForAll(t, readContract, func(c *prop.Case) { readSteps(c, lendingCache(c, 2)) })
	})

	t.Run("BlockRole", func(t *testing.T) {
		t.Parallel()

		t.Run("Valid", func(t *testing.T) {
			t.Parallel()

			tests := []struct {
				name string
				give sstable.BlockRole
				want bool
			}{
				{name: "reports true for DataBlock", give: sstable.DataBlock, want: true},
				{name: "reports true for IndexBlock", give: sstable.IndexBlock, want: true},
				{name: "reports true for FilterBlock", give: sstable.FilterBlock, want: true},
				{name: "reports false for the zero BlockRole", give: 0, want: false},
				{name: "reports false for a value above FilterBlock", give: 4, want: false},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					assert.Equal(t, tt.give.Valid(), tt.want, "Valid accepts the three roles alone")
				})
			}
		})

		t.Run("String", func(t *testing.T) {
			t.Parallel()

			tests := []struct {
				name string
				give sstable.BlockRole
				want string
			}{
				{name: "returns data for DataBlock", give: sstable.DataBlock, want: "data"},
				{name: "returns index for IndexBlock", give: sstable.IndexBlock, want: "index"},
				{name: "returns filter for FilterBlock", give: sstable.FilterBlock, want: "filter"},
				{name: "returns the number of a value that is not valid", give: 0, want: "BlockRole(0)"},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					assert.Equal(t, tt.give.String(), tt.want, "String names the role")
				})
			}
		})
	})

	t.Run("Open", func(t *testing.T) {
		t.Parallel()

		for _, tt := range goldenFiles {
			t.Run("returns ErrCorrupt for every truncation of the golden table for "+tt.name, func(t *testing.T) {
				t.Parallel()
				data := writeTable(t, tt.opts, goldenTable(), tt.dels...)
				opts := sstable.ReaderOptions{Prefix: tt.opts.Prefix}
				for n := range len(data) {
					_, err := sstable.Open(t.Context(), bytesFile(data[:n]), int64(n), opts)
					expect.ErrorIs(t, err, sstable.ErrCorrupt, "Open refuses the first "+strconv.Itoa(n)+" bytes")
				}
			})

			t.Run("returns ErrCorrupt for every flipped bit of the golden table for "+tt.name, func(t *testing.T) {
				t.Parallel()
				es := goldenTable()
				data := writeTable(t, tt.opts, es, tt.dels...)
				changed := make([]byte, len(data))
				for bit := range 8 * len(data) {
					copy(changed, data)
					changed[bit/8] ^= 1 << (bit % 8)
					r, err := sstable.Open(t.Context(), bytesFile(changed), int64(len(changed)),
						sstable.ReaderOptions{Prefix: tt.opts.Prefix})
					if err == nil {
						err = readAll(t.Context(), r, es, tt.opts.Prefix)
					}
					expect.ErrorIs(t, err, sstable.ErrCorrupt,
						"Open or a read detects the flip of bit "+strconv.Itoa(bit))
				}
			})
		}

		tests := []struct {
			name       string
			giveOpts   sstable.WriterOptions
			giveChange func(l *layout)
		}{
			{
				name:     "returns ErrCorrupt for a footer of version 0",
				giveOpts: settings[1].opts,
				giveChange: func(l *layout) {
					binary.BigEndian.PutUint32(l.data[len(l.data)-footerLen+footerVersion:], 0)
				},
			},
			{
				name:     "returns ErrCorrupt for a footer of version 4",
				giveOpts: settings[1].opts,
				giveChange: func(l *layout) {
					binary.BigEndian.PutUint32(l.data[len(l.data)-footerLen+footerVersion:], placedVersion+1)
				},
			},
			{
				name:     "returns ErrCorrupt for an index that ends before the footer",
				giveOpts: settings[1].opts,
				giveChange: func(l *layout) {
					binary.BigEndian.PutUint64(l.data[len(l.data)-footerLen+footerIndexLen:], uint64(l.index.n-1))
				},
			},
			{
				name:     "returns ErrCorrupt for an index longer than the table",
				giveOpts: settings[0].opts,
				giveChange: func(l *layout) {
					// The offset is the end of the index minus its length, in
					// arithmetic modulo 2^64.
					f := l.data[len(l.data)-footerLen:]
					end := uint64(len(l.data) - footerLen - trailerLen)
					binary.BigEndian.PutUint64(f[footerIndexLen:], end+1)
					binary.BigEndian.PutUint64(f[footerIndexOff:], math.MaxUint64)
				},
			},
			{
				name:     "returns ErrCorrupt for an index offset past the end of the table",
				giveOpts: settings[0].opts,
				giveChange: func(l *layout) {
					binary.BigEndian.PutUint64(l.data[len(l.data)-footerLen+footerIndexOff:], math.MaxUint64)
				},
			},
			{
				name:     "returns ErrCorrupt for a filter that ends before the index",
				giveOpts: settings[1].opts,
				giveChange: func(l *layout) {
					binary.BigEndian.PutUint64(l.data[len(l.data)-footerLen+footerFilterLen:], uint64(l.filter.n-1))
				},
			},
			{
				name:     "returns ErrCorrupt for a filter longer than the data before the index",
				giveOpts: settings[1].opts,
				giveChange: func(l *layout) {
					// The offset is the end of the filter minus its length, in
					// arithmetic modulo 2^64.
					f := l.data[len(l.data)-footerLen:]
					binary.BigEndian.PutUint64(f[footerFilterLen:], uint64(l.index.off-trailerLen+1))
					binary.BigEndian.PutUint64(f[footerFilterOff:], math.MaxUint64)
				},
			},
			{
				name:     "returns ErrCorrupt for a filter offset past the end of the table",
				giveOpts: settings[1].opts,
				giveChange: func(l *layout) {
					binary.BigEndian.PutUint64(l.data[len(l.data)-footerLen+footerFilterOff:], math.MaxUint64)
				},
			},
			{
				name:     "returns ErrCorrupt for a filter offset of a table without a filter",
				giveOpts: settings[0].opts,
				giveChange: func(l *layout) {
					binary.BigEndian.PutUint64(l.data[len(l.data)-footerLen+footerFilterOff:], 1)
				},
			},
			{
				name:       "returns ErrCorrupt for a filter CRC32C of a table without a filter",
				giveOpts:   settings[0].opts,
				giveChange: func(l *layout) { binary.BigEndian.PutUint32(l.filter.parent, 1) },
			},
			{
				name:       "returns ErrCorrupt for a CRC32C in the footer that differs from the index",
				giveOpts:   settings[1].opts,
				giveChange: func(l *layout) { l.index.parent[0] ^= 1 },
			},
			{
				name:       "returns ErrCorrupt for a CRC32C in the footer that differs from the filter",
				giveOpts:   settings[1].opts,
				giveChange: func(l *layout) { l.filter.parent[0] ^= 1 },
			},
			{
				name:     "returns ErrCorrupt for an index without room for its restart offsets",
				giveOpts: settings[1].opts,
				giveChange: func(l *layout) {
					binary.BigEndian.PutUint32(l.data[l.index.off+l.index.n-4:], math.MaxUint32)
				},
			},
			{
				name:     "returns ErrCorrupt for a last entry of the index outside its entries",
				giveOpts: settings[1].opts,
				giveChange: func(l *layout) {
					binary.BigEndian.PutUint32(l.data[l.index.off+l.index.n-8:], math.MaxUint32)
				},
			},
			{
				name:       "returns ErrCorrupt for a filter encoding of another version",
				giveOpts:   settings[1].opts,
				giveChange: func(l *layout) { l.data[l.filter.off]++ },
			},
			{
				name:     "returns ErrCorrupt for a first data block of an unknown codec",
				giveOpts: settings[3].opts,
				giveChange: func(l *layout) {
					// The first block of the compressed table has the Snappy
					// format, so the codec byte alone is wrong.
					l.data[l.blocks[0].off+l.blocks[0].n] = 2
				},
			},
			{
				name:       "returns ErrCorrupt for a first data block that does not decode",
				giveOpts:   settings[1].opts,
				giveChange: func(l *layout) { l.data[l.blocks[0].off+l.blocks[0].n] = byte(sstable.SnappyCompression) },
			},
			{
				name:     "returns ErrCorrupt for a top-level index of the filter without room for its restart offsets",
				giveOpts: settings[5].opts,
				giveChange: func(l *layout) {
					binary.BigEndian.PutUint32(l.data[l.filter.off+l.filter.n-4:], math.MaxUint32)
				},
			},
			{
				name:     "returns ErrCorrupt for a first top-level entry of the filter that does not decode",
				giveOpts: settings[5].opts,
				giveChange: func(l *layout) {
					// A restart point does not share a prefix with the key before it.
					top := l.data[l.filter.off : l.filter.off+l.filter.n]
					top[restarts(top)[0]] = 1
				},
			},
			{
				name:     "returns ErrCorrupt for a first partition offset past the partitions",
				giveOpts: settings[5].opts,
				giveChange: func(l *layout) {
					// The offset of the first filter partition has a varint of two
					// bytes.
					top := l.data[l.filter.off : l.filter.off+l.filter.n]
					_, value := restartEntry(top[restarts(top)[0]:])
					value[0], value[1] = 0xff, 0x7f
				},
			},
			{
				name:     "returns ErrCorrupt for a first partition offset whose varint does not end",
				giveOpts: settings[4].opts,
				giveChange: func(l *layout) {
					// A table without a filter starts its partitions with the
					// first index partition.
					top := l.data[l.index.off : l.index.off+l.index.n]
					_, value := restartEntry(top[restarts(top)[0]:])
					for i := range value {
						value[i] = 0x80
					}
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				l := newLayout(t, tt.giveOpts, goldenTable())
				tt.giveChange(l)
				l.seal()
				_, err := sstable.Open(t.Context(), bytesFile(l.data), int64(len(l.data)), sstable.ReaderOptions{})
				assert.ErrorIs(t, err, sstable.ErrCorrupt, "Open refuses the changed table")
			})
		}

		// The cases change a table of format version 3 with the fragments of
		// goldenDeletes, and open it with goldenPrefix. The block of fragments
		// has the five fragments of goldenDeletes. Its entries 1 and 2 are one
		// fragment from key 015 up to key 020 at 9 and at 7.
		placed := []struct {
			name       string
			giveOpts   sstable.WriterOptions
			giveChange func(l *layout)
		}{
			{
				name:     "returns ErrCorrupt for a block of places that ends before the footer",
				giveOpts: settings[7].opts,
				giveChange: func(l *layout) {
					binary.BigEndian.PutUint64(l.data[len(l.data)-footerLen+footerFilterLen:], uint64(l.places.n-1))
				},
			},
			{
				name:       "returns ErrCorrupt for a CRC32C in the footer that differs from the block of places",
				giveOpts:   settings[7].opts,
				giveChange: func(l *layout) { l.places.parent[0] ^= 1 },
			},
			{
				name:       "returns ErrCorrupt for a block of places with an unknown flag",
				giveOpts:   settings[7].opts,
				giveChange: func(l *layout) { l.data[l.places.off] = partitionedFlag << 1 },
			},
			{
				name:     "returns ErrCorrupt for a block of places whose flags do not end",
				giveOpts: settings[7].opts,
				giveChange: func(l *layout) {
					for i := range l.places.n {
						l.data[l.places.off+i] = 0x80
					}
				},
			},
			{
				name:     "returns ErrCorrupt for a block of places whose place of the filter does not end",
				giveOpts: settings[7].opts,
				giveChange: func(l *layout) {
					for i := 1; i < l.places.n; i++ {
						l.data[l.places.off+i] = 0x80
					}
				},
			},
			{
				name:     "returns ErrCorrupt for a block of places without room for the CRC32C of a place",
				giveOpts: settings[1].opts,
				giveChange: func(l *layout) {
					// The length of the missing filter of prefixes takes every byte
					// of the block up to the length of the name.
					_, n := placeFields(l, 2)
					end := l.places.off + l.places.n
					for i := n; i < end-2; i++ {
						l.data[i] = 0x80
					}
					l.data[end-2] = 1
				},
			},
			{
				name:     "returns ErrCorrupt for a block of places that ends after its last place",
				giveOpts: settings[1].opts,
				giveChange: func(l *layout) {
					// The length 0 of the missing filter of prefixes takes two bytes,
					// so the block ends at the CRC32C of the filter of prefixes.
					_, n := placeFields(l, 2)
					l.data[n], l.data[n+1] = 0x80, 0
				},
			},
			{
				name:     "returns ErrCorrupt for a block of places with a byte after the name",
				giveOpts: settings[7].opts,
				giveChange: func(l *layout) {
					l.data[l.places.off+l.places.n-len(firstSix)-1]--
				},
			},
			{
				name:     "returns ErrCorrupt for a block of places whose name passes its end",
				giveOpts: settings[7].opts,
				giveChange: func(l *layout) {
					l.data[l.places.off+l.places.n-len(firstSix)-1]++
				},
			},
			{
				name:     "returns ErrCorrupt for an index that ends before the block of places",
				giveOpts: settings[7].opts,
				giveChange: func(l *layout) {
					binary.BigEndian.PutUint64(l.data[len(l.data)-footerLen+footerIndexLen:], uint64(l.index.n-1))
				},
			},
			{
				name:     "returns ErrCorrupt for a filter of prefixes that ends before the index",
				giveOpts: settings[7].opts,
				giveChange: func(l *layout) {
					// Each length keeps the bytes of its varint.
					_, n := placeFields(l, 2)
					binary.PutUvarint(l.data[n:], uint64(l.prefix.n-1))
				},
			},
			{
				name:     "returns ErrCorrupt for a filter that ends before the filter of prefixes",
				giveOpts: settings[7].opts,
				giveChange: func(l *layout) {
					_, n := placeFields(l, 0)
					binary.PutUvarint(l.data[n:], uint64(l.filter.n-1))
				},
			},
			{
				name:     "returns ErrCorrupt for a block of fragments that ends before the filter",
				giveOpts: settings[7].opts,
				giveChange: func(l *layout) {
					_, n := placeFields(l, 1)
					binary.PutUvarint(l.data[n:], uint64(l.fragments.n-1))
				},
			},
			{
				name:     "returns ErrCorrupt for an offset of a missing filter of prefixes",
				giveOpts: settings[1].opts,
				giveChange: func(l *layout) {
					off, _ := placeFields(l, 2)
					l.data[off] = 1
				},
			},
			{
				name:       "returns ErrCorrupt for a CRC32C in the block of places that differs from the filter",
				giveOpts:   settings[7].opts,
				giveChange: func(l *layout) { l.filter.parent[0] ^= 1 },
			},
			{
				name:       "returns ErrCorrupt for a CRC32C in the block of places that differs from the block of fragments",
				giveOpts:   settings[7].opts,
				giveChange: func(l *layout) { l.fragments.parent[0] ^= 1 },
			},
			{
				name:       "returns ErrCorrupt for a CRC32C in the block of places that differs from the filter of prefixes",
				giveOpts:   settings[7].opts,
				giveChange: func(l *layout) { l.prefix.parent[0] ^= 1 },
			},
			{
				name:       "returns ErrCorrupt for a filter of prefixes of another encoding version",
				giveOpts:   settings[7].opts,
				giveChange: func(l *layout) { l.data[l.prefix.off]++ },
			},
			{
				name:     "returns ErrCorrupt for a top-level index of the filter of prefixes without room for its restart offsets",
				giveOpts: settings[8].opts,
				giveChange: func(l *layout) {
					binary.BigEndian.PutUint32(l.data[l.prefix.off+l.prefix.n-4:], math.MaxUint32)
				},
			},
			{
				name:     "returns ErrCorrupt for a block of fragments without room for its restart offsets",
				giveOpts: settings[7].opts,
				giveChange: func(l *layout) {
					binary.BigEndian.PutUint32(l.data[l.fragments.off+l.fragments.n-4:], math.MaxUint32)
				},
			},
			{
				name:     "returns ErrCorrupt for a block of fragments without fragments",
				giveOpts: settings[7].opts,
				giveChange: func(l *layout) {
					binary.BigEndian.PutUint32(l.data[l.fragments.off+l.fragments.n-4:], 0)
				},
			},
			{
				name:     "returns ErrCorrupt for a fragment whose entry does not decode",
				giveOpts: settings[7].opts,
				giveChange: func(l *layout) {
					// A restart point does not share a prefix with the key before
					// it.
					block := l.data[l.fragments.off : l.fragments.off+l.fragments.n]
					block[restarts(block)[1]] = 1
				},
			},
			{
				name:     "returns ErrCorrupt for a fragment whose start is at its end",
				giveOpts: settings[7].opts,
				giveChange: func(l *layout) {
					// The end key 015 of the first fragment becomes key 010.
					_, end := fragmentEntry(l, 0)
					end[len(end)-1] = '0'
				},
			},
			{
				name:     "returns ErrCorrupt for a fragment whose start is after its end",
				giveOpts: settings[7].opts,
				giveChange: func(l *layout) {
					// The end key 015 of the first fragment becomes key 005.
					_, end := fragmentEntry(l, 0)
					end[len(end)-2] = '0'
				},
			},
			{
				name:     "returns ErrCorrupt for a fragment at the sequence of the entry before it",
				giveOpts: settings[7].opts,
				giveChange: func(l *layout) {
					ikey, _ := fragmentEntry(l, 2)
					binary.BigEndian.PutUint64(ikey[len(ikey)-seqLen:], 9)
				},
			},
			{
				name:     "returns ErrCorrupt for a fragment above the sequence of the entry before it",
				giveOpts: settings[7].opts,
				giveChange: func(l *layout) {
					ikey, _ := fragmentEntry(l, 2)
					binary.BigEndian.PutUint64(ikey[len(ikey)-seqLen:], 10)
				},
			},
			{
				name:     "returns ErrCorrupt for a fragment that starts before the end of the fragment before it",
				giveOpts: settings[7].opts,
				giveChange: func(l *layout) {
					// The start key 020 of the fourth fragment becomes key 019.
					ikey, _ := fragmentEntry(l, 3)
					ikey[len("key 0")], ikey[len("key 01")] = '1', '9'
				},
			},
			{
				name:     "returns ErrCorrupt for a fragment of the start before it with another end",
				giveOpts: settings[7].opts,
				giveChange: func(l *layout) {
					// The end key 020 of the third fragment becomes key 021, and so
					// does the start of the fourth fragment, so the fragments after
					// the third follow it.
					_, end := fragmentEntry(l, 2)
					end[len(end)-1] = '1'
					ikey, _ := fragmentEntry(l, 3)
					ikey[len("key 02")] = '1'
				},
			},
			{
				name:     "returns ErrCorrupt for a fragment of the end before it with another start",
				giveOpts: settings[7].opts,
				giveChange: func(l *layout) {
					// The start key 015 of the third fragment becomes key 016.
					ikey, _ := fragmentEntry(l, 2)
					ikey[len("key 01")] = '6'
				},
			},
		}
		for _, tt := range placed {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				l := newLayout(t, tt.giveOpts, goldenTable(), goldenDeletes...)
				tt.giveChange(l)
				l.seal()
				_, err := sstable.Open(t.Context(), bytesFile(l.data), int64(len(l.data)),
					sstable.ReaderOptions{Prefix: goldenPrefix})
				assert.ErrorIs(t, err, sstable.ErrCorrupt, "Open refuses the changed table")
			})
		}

		t.Run("reads the filter of prefixes only for a Reader of its extraction", func(t *testing.T) {
			t.Parallel()
			data := writeTable(t, settings[7].opts, goldenTable(), goldenDeletes...)
			reads := make([]int, 0, 3)
			for _, prefix := range []sstable.PrefixExtractor{goldenPrefix, fixedPrefix{name: firstSixAgain, n: 6}, nil} {
				counter := &countingFile{data: data}
				_, err := sstable.Open(t.Context(), counter, int64(len(data)), sstable.ReaderOptions{Prefix: prefix})
				assert.NoError(t, err, "Open reads the table")
				reads = append(reads, counter.reads)
			}
			assert.Equal(t, reads, []int{reads[0], reads[0] - 1, reads[0] - 1},
				"a Reader of another extraction or of none reads one block less")
		})

		t.Run("returns ErrCorrupt for a filter before an index at the start of the table", func(t *testing.T) {
			t.Parallel()
			// A table of no entries without a filter has its index at offset
			// 0. The filter offset is that offset minus the trailer and the
			// length of the filter, in arithmetic modulo 2^64.
			l := newLayout(t, settings[0].opts, nil)
			f := l.data[len(l.data)-footerLen:]
			binary.BigEndian.PutUint64(f[footerFilterLen:], 1)
			binary.BigEndian.PutUint64(f[footerFilterOff:], math.MaxUint64-trailerLen)
			l.seal()
			_, err := sstable.Open(t.Context(), bytesFile(l.data), int64(len(l.data)), sstable.ReaderOptions{})
			assert.ErrorIs(t, err, sstable.ErrCorrupt, "Open refuses the changed table")
		})

		t.Run("returns ErrCorrupt for a data block at the place of the first data block", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Open refuses a valid data block at the place of the first data block", func(c *prop.Case) {
				es := c.Draw(uniform, "entries")
				l := newLayout(c, oneEntry, es)
				b := l.blocks[c.Draw(prop.Integer(1, len(l.blocks)-1), "block")]
				copy(l.data[l.blocks[0].off:], l.data[b.off:b.off+b.n+trailerLen])
				_, err := sstable.Open(c.Context(), bytesFile(l.data), int64(len(l.data)), sstable.ReaderOptions{})
				assert.ErrorIs(c, err, sstable.ErrCorrupt, "Open refuses the block at the place of the first")
			})
		})

		foreign := []struct {
			name      string
			givePlace func(l *layout) place
		}{
			{
				name:      "returns ErrCorrupt for the filter block of another table",
				givePlace: func(l *layout) place { return l.filter },
			},
			{
				name:      "returns ErrCorrupt for the index block of another table",
				givePlace: func(l *layout) place { return l.index },
			},
		}
		for _, tt := range foreign {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				prop.ForAll(t, "Open refuses a valid block of another table of the same layout", func(c *prop.Case) {
					es := c.Draw(uniform, "entries")
					// The keys of the other table differ in their third byte, so
					// the two tables have one layout and different blocks.
					others := make([]entry, 0, len(es))
					for _, e := range es {
						key := slices.Concat(e.key[:2], []byte{'z'}, e.key[3:])
						others = append(others, entry{key: key, seq: e.seq, value: e.value})
					}
					l, other := newLayout(c, settings[1].opts, es), newLayout(c, settings[1].opts, others)
					to, from := tt.givePlace(l), tt.givePlace(other)
					assert.Equal(c, from.off, to.off, "the block of the other table has the same offset")
					assert.Equal(c, from.n, to.n, "the block of the other table has the same length")
					copy(l.data[to.off:], other.data[from.off:from.off+from.n+trailerLen])
					_, err := sstable.Open(c.Context(), bytesFile(l.data), int64(len(l.data)), sstable.ReaderOptions{})
					assert.ErrorIs(c, err, sstable.ErrCorrupt, "Open refuses the block of the other table")
				})
			})
		}

		t.Run("returns the error of each read of the File", func(t *testing.T) {
			t.Parallel()
			for _, setting := range []sstable.WriterOptions{settings[1].opts, settings[7].opts, settings[8].opts} {
				data := writeTable(t, setting, goldenTable(), goldenDeletes...)
				opts := sstable.ReaderOptions{Prefix: setting.Prefix}
				counter := &countingFile{data: data}
				_, err := sstable.Open(t.Context(), counter, int64(len(data)), opts)
				assert.NoError(t, err, "Open reads the table")
				for ok := range counter.reads {
					_, err := sstable.Open(t.Context(), &failingFile{data: data, ok: ok}, int64(len(data)), opts)
					expect.ErrorIs(t, err, errRead, "Open returns the error of its read "+strconv.Itoa(ok+1))
				}
			}
		})

		t.Run("returns io.ErrUnexpectedEOF for a read of fewer bytes without an error", func(t *testing.T) {
			t.Parallel()
			data := writeTable(t, sstable.WriterOptions{}, goldenTable())
			_, err := sstable.Open(t.Context(), shortFile{data: data}, int64(len(data)), sstable.ReaderOptions{})
			assert.ErrorIs(t, err, io.ErrUnexpectedEOF, "Open refuses the short read")
		})

		t.Run("returns the error of the context of its reads", func(t *testing.T) {
			t.Parallel()
			data := writeTable(t, settings[1].opts, goldenTable())
			assert.HonoursCancellation(t, func(ctx context.Context) error {
				_, err := sstable.Open(ctx, bytesFile(data), int64(len(data)), sstable.ReaderOptions{})

				return err
			}, "Open returns the error that the File returns for a context that ended")
		})

		t.Run("returns only ErrCorrupt for a table with changed bytes", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, changeContract, openChanged)
		})
	})

	t.Run("Summary", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the summary that Close returned", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "the Reader returns the summary of the Writer", func(c *prop.Case) {
				var buf bufferSink
				w, err := sstable.NewWriter(&buf, c.Draw(prop.SampledFrom(settings...), "setting").opts)
				assert.NoError(c, err, "NewWriter accepts the options")
				for _, e := range c.Draw(entries, "entries") {
					assert.NoError(c, w.Add(c.Context(), e.key, e.seq, e.value), "Add adds the entry")
				}
				for _, d := range c.Draw(fragments, "fragments") {
					assert.NoError(c, w.AddRangeDelete(c.Context(), d.start, d.end, d.seq),
						"AddRangeDelete adds the fragment")
				}
				want, err := w.Close(c.Context())
				assert.NoError(c, err, "Close writes the table")
				assert.Equal(c, openTable(c, buf.Bytes(), sstable.ReaderOptions{}).Summary(), want,
					"the Reader returns the summary of the Writer")
			})
		})
	})

	t.Run("Footprint", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the capacity of every buffer in bytes", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Footprint returns the bytes of the blocks that the Reader keeps and the summary keys",
				func(c *prop.Case) {
					opts := c.Draw(prop.SampledFrom(settings...), "setting").opts
					l := newLayout(c, opts, c.Draw(entries, "entries"), c.Draw(fragments, "fragments")...)
					var prefix sstable.PrefixExtractor
					if c.Draw(prop.Boolean(), "reader of the extraction") {
						prefix = opts.Prefix
					}
					r := openTable(c, l.data, sstable.ReaderOptions{Prefix: prefix})
					want := l.index.n + l.filter.n + l.fragments.n
					if prefix != nil {
						want += l.prefix.n
					}
					s := r.Summary()
					want += cap(s.SmallestKey) + cap(s.LargestKey) + cap(s.RangeDeleteStart) + cap(s.RangeDeleteEnd)
					assert.Equal(c, r.Footprint(), want,
						"Footprint adds the lengths of the blocks that the Reader keeps to the capacity of the keys")
				})
		})
	})

	t.Run("Span", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the blocks that the index states for the range", func(t *testing.T) {
			t.Parallel()
			bound := prop.Optional(prop.OneOf(prop.SampledFrom(keys...), prop.Bytes(prop.MaxSize(20))))
			// The long entries have index entries whose headers take more than
			// three bytes.
			tables := prop.OneOf(entries, prop.Just(longTable()))
			prop.ForAll(t, "Span counts from the block of lower to the block of upper", func(c *prop.Case) {
				opts := c.Draw(prop.SampledFrom(oneEntry, oneEntryParts, settings[1].opts, settings[3].opts,
					settings[4].opts, settings[6].opts, settings[7].opts, settings[8].opts), "options")
				l := newLayout(c, opts, c.Draw(tables, "entries"), c.Draw(fragments, "fragments")...)
				var lower, upper []byte
				if p := c.Draw(bound, "lower"); p != nil {
					lower = *p
				}
				if p := c.Draw(bound, "upper"); p != nil {
					upper = *p
				}
				got, err := openTable(c, l.data, sstable.ReaderOptions{}).Span(c.Context(), lower, upper)
				assert.NoError(c, err, "Span searches a valid index")
				assert.Equal(c, got, spanOf(l, lower, upper),
					"Span counts the blocks from the block of lower to the block of upper")
			})
		})

		tests := []struct {
			name      string
			giveLower []byte
			giveUpper []byte
		}{
			{
				name:      "returns the zero Span for an upper equal to lower",
				giveLower: []byte("key 010"),
				giveUpper: []byte("key 010"),
			},
			{
				name:      "returns the zero Span for an upper before lower",
				giveLower: []byte("key 010"),
				giveUpper: []byte("key 005"),
			},
			{
				name:      "returns the zero Span for a lower after the last key",
				giveLower: []byte("zzz"),
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				for _, opts := range []sstable.WriterOptions{settings[1].opts, settings[5].opts} {
					r := openTable(t, writeTable(t, opts, goldenTable()), sstable.ReaderOptions{})
					got, err := r.Span(t.Context(), tt.giveLower, tt.giveUpper)
					expect.NoError(t, err, "Span searches a valid index")
					expect.Equal(t, got, sstable.Span{}, "an empty range has no blocks")
				}
			})
		}

		// The first block of the table ends with key a at the highest
		// sequence, which equals the entry that each search looks for.
		highest := []struct {
			name       string
			giveLower  []byte
			giveUpper  []byte
			wantBlocks int
		}{
			{
				name:       "counts the block whose last entry is lower at the highest sequence",
				giveLower:  []byte("a"),
				wantBlocks: 2,
			},
			{
				name:       "counts the block whose last entry is upper at the highest sequence",
				giveUpper:  []byte("a"),
				wantBlocks: 1,
			},
		}
		for _, tt := range highest {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				l := newLayout(t, oneEntry, []entry{
					{key: []byte("a"), seq: math.MaxUint64, value: []byte("v")},
					{key: []byte("b"), seq: 1, value: []byte("v")},
				})
				last := l.blocks[tt.wantBlocks-1]
				got, err := openTable(t, l.data, sstable.ReaderOptions{}).Span(t.Context(), tt.giveLower, tt.giveUpper)
				assert.NoError(t, err, "Span searches a valid index")
				assert.Equal(t, got, sstable.Span{Blocks: tt.wantBlocks, Bytes: int64(last.off + last.n + trailerLen)},
					"Span counts from the first block")
			})
		}

		corrupt := []struct {
			name       string
			giveEntry  int
			giveChange func(l *layout, entry []byte)
			giveLower  []byte
			giveUpper  []byte
		}{
			{
				name:      "returns ErrCorrupt for a malformed index key on the search for lower",
				giveEntry: lowerSearchEntry,
				// A restart point does not share a prefix with the key before it.
				giveChange: func(_ *layout, entry []byte) { entry[0] = 1 },
				giveLower:  []byte("key 000"),
				giveUpper:  []byte("zzz"),
			},
			{
				name:       "returns ErrCorrupt for a malformed index key on the search for upper",
				giveEntry:  upperSearchEntry,
				giveChange: func(_ *layout, entry []byte) { entry[0] = 1 },
				giveUpper:  []byte("zzz"),
			},
			{
				name:      "returns ErrCorrupt for a malformed place of the first block",
				giveEntry: key058Entry,
				// A value of 127 bytes passes the end of the entries.
				giveChange: func(_ *layout, entry []byte) { entry[2] = 0x7f },
				giveLower:  []byte("key 058"),
				giveUpper:  []byte("key 059"),
			},
			{
				name:       "returns ErrCorrupt for a malformed place of the last block",
				giveEntry:  key059Entry,
				giveChange: func(_ *layout, entry []byte) { entry[2] = 0x7f },
				giveUpper:  []byte("key 059"),
			},
			{
				name:      "returns ErrCorrupt for a last block that ends at the start of the first block",
				giveEntry: key059Entry,
				// The value follows the header of 3 bytes and the internal key.
				// Both offsets have a varint of two bytes.
				giveChange: func(l *layout, entry []byte) {
					first, last := l.blocks[key058Entry], l.blocks[key059Entry]
					binary.PutUvarint(entry[3+int(entry[1]):], uint64(first.off-last.n-trailerLen))
				},
				giveLower: []byte("key 058"),
				giveUpper: []byte("key 059"),
			},
		}
		for _, tt := range corrupt {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				l := newLayout(t, oneEntry, goldenTable())
				index := l.data[l.index.off : l.index.off+l.index.n]
				tt.giveChange(l, index[restarts(index)[tt.giveEntry]:])
				l.seal()
				_, err := openTable(t, l.data, sstable.ReaderOptions{}).Span(t.Context(), tt.giveLower, tt.giveUpper)
				assert.ErrorIs(t, err, sstable.ErrCorrupt, "Span refuses the changed index")
			})
		}

		// The cases change the index entry of the middle block of a table of
		// three blocks of one entry each. The entry has a header of 3 bytes,
		// an internal key of 9 bytes and a value of 6 bytes. The value
		// contains the offset and the length of the block as varints of one
		// byte, then its CRC32C. The restart offsets of the index follow its
		// entries in 16 bytes, so a value of 127 bytes passes the end of the
		// index block.
		places := []struct {
			name       string
			giveChange func(l *layout, header, value []byte)
		}{
			{
				name:       "returns ErrCorrupt for an index value past the end of the index block",
				giveChange: func(_ *layout, header, _ []byte) { header[2] = 0x7f },
			},
			{
				name:       "returns ErrCorrupt for an index value with a byte after its CRC32C",
				giveChange: func(_ *layout, header, _ []byte) { header[2]++ },
			},
			{
				name: "returns ErrCorrupt for an index value whose length varint does not end",
				giveChange: func(_ *layout, header, value []byte) {
					header[2] = 5
					copy(value[1:5], []byte{0x80, 0x80, 0x80, 0x80})
				},
			},
			{
				name:       "returns ErrCorrupt for a block longer than the data blocks",
				giveChange: func(_ *layout, _, value []byte) { value[1] = 0x7f },
			},
			{
				name: "returns ErrCorrupt for a block whose trailer passes the end of the data blocks",
				giveChange: func(l *layout, _, value []byte) {
					value[1] = byte(l.index.off - trailerLen + 1)
				},
			},
			{
				name: "returns ErrCorrupt for a block that ends after the data blocks",
				giveChange: func(l *layout, _, value []byte) {
					value[0] = byte(l.index.off - l.blocks[1].n - trailerLen + 1)
				},
			},
		}
		for _, tt := range places {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				l := newLayout(t, oneEntry, []entry{
					{key: []byte("a"), seq: 1, value: []byte("v")},
					{key: []byte("b"), seq: 1, value: []byte("v")},
					{key: []byte("c"), seq: 1, value: []byte("v")},
				})
				index := l.data[l.index.off : l.index.off+l.index.n]
				entry := index[restarts(index)[1]:]
				tt.giveChange(l, entry[:3], entry[3+int(entry[1]):])
				l.seal()
				_, err := openTable(t, l.data, sstable.ReaderOptions{}).Span(t.Context(), nil, []byte("b"))
				assert.ErrorIs(t, err, sstable.ErrCorrupt, "Span refuses the place of the block of b")
			})
		}

		// The cases change the golden table under oneEntryParts. Its 20 index
		// partitions have 6 blocks each, of three keys. The search of the
		// top-level index for the lower "key 000" compares its entry 10 first.
		// The lower "key 006" is in the third partition, and the upper "key
		// 010" in the fourth.
		partitions := []struct {
			name       string
			giveChange func(l *layout)
			giveLower  []byte
			giveUpper  []byte
		}{
			{
				name: "returns ErrCorrupt for a malformed top-level key on the search for lower",
				giveChange: func(l *layout) {
					// A restart point does not share a prefix with the key before
					// it.
					top := l.data[l.index.off : l.index.off+l.index.n]
					top[restarts(top)[10]] = 1
				},
				giveLower: []byte("key 000"),
			},
			{
				name: "returns ErrCorrupt for a lower after the last block of its partition",
				giveChange: func(l *layout) {
					// The top-level entry of the second partition has the key 006
					// in place of its last key 005.
					top := l.data[l.index.off : l.index.off+l.index.n]
					ikey, _ := restartEntry(top[restarts(top)[1]:])
					ikey[len("key 00")]++
				},
				giveLower: []byte("key 006"),
			},
			{
				name: "returns ErrCorrupt for a malformed count of the blocks before the partition of lower",
				giveChange: func(l *layout) {
					// The varint of the count continues through the bytes of the
					// CRC32C.
					v := indexValue(l, 1)
					for i := range crcLen + 1 {
						v[len(v)-1-i] = 0x80
					}
				},
				giveLower: []byte("key 006"),
			},
			{
				name: "returns ErrCorrupt for a count of blocks that puts the last block before the first",
				giveChange: func(l *layout) {
					// The count of the blocks up to the end of the third partition
					// has a varint of one byte.
					v := indexValue(l, 2)
					v[len(v)-crcLen-1] = 0
				},
				giveLower: []byte("key 006"),
				giveUpper: []byte("key 010"),
			},
			{
				name: "returns ErrCorrupt for a malformed place of a block in the partition of lower",
				giveChange: func(l *layout) {
					p := l.indexParts[1]
					part := l.data[p.off : p.off+p.n]
					_, value := restartEntry(part[restarts(part)[0]:])
					for i := range value {
						value[i] = 0x80
					}
				},
				giveLower: []byte("key 003"),
			},
			{
				name: "returns ErrCorrupt for a malformed place of a block in the partition of upper",
				giveChange: func(l *layout) {
					// The third entry of the second partition is the block of key
					// 004 at the sequence 2.
					p := l.indexParts[1]
					part := l.data[p.off : p.off+p.n]
					_, value := restartEntry(part[restarts(part)[2]:])
					for i := range value {
						value[i] = 0x80
					}
				},
				giveLower: []byte("key 000"),
				giveUpper: []byte("key 004"),
			},
		}
		for _, tt := range partitions {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				l := newLayout(t, oneEntryParts, goldenTable())
				assert.Length(t, l.indexParts, 20, "the table has 20 index partitions")
				tt.giveChange(l)
				l.seal()
				_, err := openTable(t, l.data, sstable.ReaderOptions{}).Span(t.Context(), tt.giveLower, tt.giveUpper)
				assert.ErrorIs(t, err, sstable.ErrCorrupt, "Span refuses the changed partition")
			})
		}

		t.Run("returns the error of the context of its read of a partition", func(t *testing.T) {
			t.Parallel()
			r := openTable(t, writeTable(t, settings[5].opts, goldenTable()), sstable.ReaderOptions{})
			assert.HonoursCancellation(t, func(ctx context.Context) error {
				_, err := r.Span(ctx, []byte("key 010"), []byte("key 050"))

				return err
			}, "Span returns the error of a context that ended before its read of a partition")
		})

		t.Run("returns the error of the File for a partition", func(t *testing.T) {
			t.Parallel()
			data := writeTable(t, settings[5].opts, goldenTable())
			counter := &countingFile{data: data}
			_, err := sstable.Open(t.Context(), counter, int64(len(data)), sstable.ReaderOptions{})
			assert.NoError(t, err, "Open reads the table")
			r, err := sstable.Open(t.Context(), &failingFile{data: data, ok: counter.reads}, int64(len(data)),
				sstable.ReaderOptions{})
			assert.NoError(t, err, "Open reads the table with the reads that it needs")
			_, err = r.Span(t.Context(), []byte("key 010"), []byte("key 050"))
			assert.ErrorIs(t, err, errRead, "Span returns the error of the read of a partition")
		})

		t.Run("lends the index partition of each bound with IndexBlock", func(t *testing.T) {
			t.Parallel()
			l := newLayout(t, settings[5].opts, goldenTable())
			cache := &roleCache{Cache: lendingCache(t, 4)}
			r := openTable(t, l.data, sstable.ReaderOptions{Cache: cache, CacheID: 1})
			lower, upper := []byte("key 010"), []byte("key 050")
			assert.NotEqual(t, placeOf(l.indexParts, lower), placeOf(l.indexParts, upper),
				"the two bounds are in two index partitions")
			_, err := r.Span(t.Context(), lower, upper)
			assert.NoError(t, err, "Span reads the index partitions")
			assert.Equal(t, cache.lends, []lookup{
				{offset: placeOf(l.indexParts, lower), role: sstable.IndexBlock},
				{offset: placeOf(l.indexParts, upper), role: sstable.IndexBlock},
			}, "Span lends the index partitions of lower and upper with IndexBlock")
		})
	})

	t.Run("MayContain", func(t *testing.T) {
		t.Parallel()

		t.Run("reports true for every key of a table with a filter", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "MayContain reports true for the key of every entry", func(c *prop.Case) {
				es := c.Draw(entries, "entries")
				opts := c.Draw(prop.SampledFrom(settings[1].opts, settings[5].opts, oneEntryParts), "options")
				r := openTable(c, writeTable(c, opts, es), sstable.ReaderOptions{})
				for _, e := range es {
					may, err := r.MayContain(c.Context(), e.key)
					assert.NoError(c, err, "MayContain reads the filter")
					assert.True(c, may, "MayContain reports true for a key of the table")
				}
			})
		})

		for _, tt := range []struct {
			name string
			opts sstable.WriterOptions
		}{settings[0], settings[4]} {
			t.Run("reports true for a key that a table "+tt.name+" does not contain", func(t *testing.T) {
				t.Parallel()
				r := openTable(t, writeTable(t, tt.opts, goldenTable()), sstable.ReaderOptions{})
				may, err := r.MayContain(t.Context(), []byte("absent"))
				assert.NoError(t, err, "MayContain reads no filter")
				assert.True(t, may, "a table without a filter may contain every key")
			})
		}

		for _, tt := range []struct {
			name     string
			giveOpts sstable.WriterOptions
		}{
			{
				name:     "reports false for most keys that a table with a filter does not contain",
				giveOpts: sstable.WriterOptions{FilterBits: 16},
			},
			{
				name:     "reports false for most keys that a table with filter partitions does not contain",
				giveOpts: sstable.WriterOptions{FilterBits: 16, PartitionSize: partitionBytes},
			},
		} {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				r := openTable(t, writeTable(t, tt.giveOpts, goldenTable()), sstable.ReaderOptions{})
				admitted := 0
				for i := range absentKeys {
					may, err := r.MayContain(t.Context(), []byte("absent "+strconv.Itoa(i)))
					assert.NoError(t, err, "MayContain reads the filter")
					if may {
						admitted++
					}
				}
				assert.InRange(t, admitted, 0, falsePositives, "a filter of 16 bits admits about 1 key in 65,536")
			})
		}

		t.Run("reports false without a read for a key after the last key of a table in partitions",
			func(t *testing.T) {
				t.Parallel()
				data := writeTable(t, settings[5].opts, goldenTable())
				counter := &countingFile{data: data}
				r, err := sstable.Open(t.Context(), counter, int64(len(data)), sstable.ReaderOptions{})
				assert.NoError(t, err, "Open reads the table")
				reads := counter.reads
				may, err := r.MayContain(t.Context(), []byte("zz"))
				assert.NoError(t, err, "MayContain finds no partition")
				expect.False(t, may, "a key after the last key has no partition")
				expect.Equal(t, counter.reads, reads, "MayContain reads no partition")
			})

		t.Run("returns the error of the context of its read", func(t *testing.T) {
			t.Parallel()
			r := openTable(t, writeTable(t, settings[5].opts, goldenTable()), sstable.ReaderOptions{})
			assert.HonoursCancellation(t, func(ctx context.Context) error {
				_, err := r.MayContain(ctx, []byte("key 030"))

				return err
			}, "MayContain returns the error of a context that ended before its read")
		})

		t.Run("returns the error of the File", func(t *testing.T) {
			t.Parallel()
			data := writeTable(t, settings[5].opts, goldenTable())
			counter := &countingFile{data: data}
			_, err := sstable.Open(t.Context(), counter, int64(len(data)), sstable.ReaderOptions{})
			assert.NoError(t, err, "Open reads the table")
			r, err := sstable.Open(t.Context(), &failingFile{data: data, ok: counter.reads}, int64(len(data)),
				sstable.ReaderOptions{})
			assert.NoError(t, err, "Open reads the table with the reads that it needs")
			_, err = r.MayContain(t.Context(), []byte("key 030"))
			assert.ErrorIs(t, err, errRead, "MayContain returns the error of the read of the partition")
		})

		t.Run("returns ErrCorrupt for a filter partition that does not parse", func(t *testing.T) {
			t.Parallel()
			l := newLayout(t, settings[5].opts, goldenTable())
			assert.NotEmpty(t, l.filterParts, "the table has filter partitions")
			// The first byte of an encoding is the version of its format.
			l.data[l.filterParts[0].off]++
			l.seal()
			r := openTable(t, l.data, sstable.ReaderOptions{})
			_, err := r.MayContain(t.Context(), goldenKeys[0])
			expect.ErrorIs(t, err, sstable.ErrCorrupt, "MayContain refuses the partition")
			_, _, _, err = r.Get(t.Context(), goldenKeys[0], 2, nil)
			expect.ErrorIs(t, err, sstable.ErrCorrupt, "Get refuses the partition")
		})

		t.Run("returns ErrCorrupt for a top-level entry of the filter that does not decode", func(t *testing.T) {
			t.Parallel()
			l := newLayout(t, settings[5].opts, goldenTable())
			assert.Length(t, l.filterParts, 2, "the table has two filter partitions")
			// A restart point does not share a prefix with the key before it.
			// Open decodes the first entry alone, and the search of a key
			// compares the second entry first.
			top := l.data[l.filter.off : l.filter.off+l.filter.n]
			top[restarts(top)[1]] = 1
			l.seal()
			_, err := openTable(t, l.data, sstable.ReaderOptions{}).MayContain(t.Context(), goldenKeys[0])
			assert.ErrorIs(t, err, sstable.ErrCorrupt, "MayContain refuses the entry")
		})

		t.Run("returns ErrCorrupt for a CRC32C in the top-level index that differs from the partition",
			func(t *testing.T) {
				t.Parallel()
				l := newLayout(t, settings[5].opts, goldenTable())
				l.filterParts[0].parent[0] ^= 1
				l.seal()
				_, err := openTable(t, l.data, sstable.ReaderOptions{}).MayContain(t.Context(), goldenKeys[0])
				assert.ErrorIs(t, err, sstable.ErrCorrupt, "MayContain refuses the partition")
			})

		t.Run("returns ErrCorrupt for a filter partition outside the partitions", func(t *testing.T) {
			t.Parallel()
			// The second filter partition has the keys after key 050. Open reads
			// the first entry of the top-level index of the filter alone. The
			// offset of the second partition has a varint of two bytes.
			l := newLayout(t, settings[5].opts, goldenTable())
			assert.Length(t, l.filterParts, 2, "the table has two filter partitions")
			top := l.data[l.filter.off : l.filter.off+l.filter.n]
			_, value := restartEntry(top[restarts(top)[1]:])
			value[0], value[1] = 0xff, 0x7f
			l.seal()
			_, err := openTable(t, l.data, sstable.ReaderOptions{}).MayContain(t.Context(), goldenKeys[55])
			assert.ErrorIs(t, err, sstable.ErrCorrupt, "MayContain refuses the place of the partition")
		})

		t.Run("lends the filter partition of its key with FilterBlock", func(t *testing.T) {
			t.Parallel()
			l := newLayout(t, settings[5].opts, goldenTable())
			cache := &roleCache{Cache: lendingCache(t, 4)}
			r := openTable(t, l.data, sstable.ReaderOptions{Cache: cache, CacheID: 1})
			key := []byte("key 030")
			may, err := r.MayContain(t.Context(), key)
			assert.NoError(t, err, "MayContain reads the filter partition")
			assert.True(t, may, "MayContain reports true for a key of the table")
			assert.Equal(t, cache.lends, []lookup{{offset: placeOf(l.filterParts, key), role: sstable.FilterBlock}},
				"MayContain lends the filter partition of the key with FilterBlock")
		})
	})

	t.Run("MayContainPrefix", func(t *testing.T) {
		t.Parallel()

		t.Run("reports true for the prefix of every key of a table", func(t *testing.T) {
			t.Parallel()
			options := prop.SampledFrom(settings[1].opts, settings[5].opts, oneEntryParts, prefixParts)
			prop.ForAll(t, "MayContainPrefix reports true for the prefix of every key", func(c *prop.Case) {
				es := c.Draw(entries, "entries")
				opts := c.Draw(options, "options")
				prefix := fixedPrefix{name: firstBytes, n: c.Draw(prop.Integer(0, 3), "prefix length")}
				opts.Prefix = prefix
				r := openTable(c, writeTable(c, opts, es), sstable.ReaderOptions{Prefix: prefix})
				for _, e := range es {
					p, ok := prefix.Prefix(e.key)
					if !ok {
						continue
					}
					may, err := r.MayContainPrefix(c.Context(), p)
					assert.NoError(c, err, "MayContainPrefix reads the filter of prefixes")
					assert.True(c, may, "MayContainPrefix reports true for a prefix of the table")
				}
			})
		})

		for _, tt := range []struct {
			name     string
			giveOpts sstable.WriterOptions
		}{
			{
				name:     "reports false for most prefixes that a table does not contain",
				giveOpts: sstable.WriterOptions{FilterBits: 16, Prefix: goldenPrefix},
			},
			{
				name:     "reports false for most prefixes that a table in partitions does not contain",
				giveOpts: sstable.WriterOptions{FilterBits: 16, PartitionSize: partitionBytes, Prefix: goldenPrefix},
			},
		} {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				data := writeTable(t, tt.giveOpts, goldenTable())
				r := openTable(t, data, sstable.ReaderOptions{Prefix: goldenPrefix})
				admitted := 0
				for i := range absentKeys {
					may, err := r.MayContainPrefix(t.Context(), []byte("absent "+strconv.Itoa(i)))
					assert.NoError(t, err, "MayContainPrefix reads the filter of prefixes")
					if may {
						admitted++
					}
				}
				assert.InRange(t, admitted, 0, falsePositives, "a filter of 16 bits admits about 1 prefix in 65,536")
			})
		}

		readers := []struct {
			name       string
			giveOpts   sstable.WriterOptions
			givePrefix sstable.PrefixExtractor
		}{
			{
				name:       "reports true for every prefix for a Reader of another extraction",
				giveOpts:   settings[7].opts,
				givePrefix: fixedPrefix{name: firstSixAgain, n: 6},
			},
			{
				name:     "reports true for every prefix for a Reader without an extraction",
				giveOpts: settings[8].opts,
			},
			{
				name:       "reports true for every prefix of a table without a filter of prefixes",
				giveOpts:   settings[1].opts,
				givePrefix: goldenPrefix,
			},
		}
		for _, tt := range readers {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				data := writeTable(t, tt.giveOpts, goldenTable())
				r := openTable(t, data, sstable.ReaderOptions{Prefix: tt.givePrefix})
				may, err := r.MayContainPrefix(t.Context(), []byte("absent"))
				assert.NoError(t, err, "MayContainPrefix reads no filter")
				assert.True(t, may, "the Reader may find a key of every prefix")
			})
		}

		t.Run("reports false without a read for a prefix after the last prefix of a table in partitions",
			func(t *testing.T) {
				t.Parallel()
				data := writeTable(t, settings[8].opts, goldenTable())
				counter := &countingFile{data: data}
				opts := sstable.ReaderOptions{Prefix: goldenPrefix}
				r, err := sstable.Open(t.Context(), counter, int64(len(data)), opts)
				assert.NoError(t, err, "Open reads the table")
				reads := counter.reads
				may, err := r.MayContainPrefix(t.Context(), []byte("zz"))
				assert.NoError(t, err, "MayContainPrefix finds no partition")
				expect.False(t, may, "a prefix after the last prefix has no partition")
				expect.Equal(t, counter.reads, reads, "MayContainPrefix reads no partition")
			})

		t.Run("reads the partition of its prefix alone", func(t *testing.T) {
			t.Parallel()
			data := writeTable(t, prefixParts, goldenTable())
			opts := sstable.ReaderOptions{Prefix: goldenPrefix}
			counter := &countingFile{data: data}
			_, err := sstable.Open(t.Context(), counter, int64(len(data)), opts)
			assert.NoError(t, err, "Open reads the table")
			r, err := sstable.Open(t.Context(), &failingFile{data: data, ok: counter.reads + 1}, int64(len(data)), opts)
			assert.NoError(t, err, "Open reads the table with the reads that it needs")
			may, err := r.MayContainPrefix(t.Context(), []byte("key 03"))
			expect.NoError(t, err, "MayContainPrefix reads the partition of its prefix")
			expect.True(t, may, "MayContainPrefix reports true for a prefix of the table")
			_, err = r.MayContainPrefix(t.Context(), []byte("key 05"))
			expect.ErrorIs(t, err, errRead, "MayContainPrefix of a prefix of another partition reads that partition")
		})

		t.Run("returns the error of the context of its read", func(t *testing.T) {
			t.Parallel()
			data := writeTable(t, settings[8].opts, goldenTable())
			r := openTable(t, data, sstable.ReaderOptions{Prefix: goldenPrefix})
			assert.HonoursCancellation(t, func(ctx context.Context) error {
				_, err := r.MayContainPrefix(ctx, []byte("key 03"))

				return err
			}, "MayContainPrefix returns the error of a context that ended before its read")
		})

		t.Run("returns ErrCorrupt for a partition that does not parse", func(t *testing.T) {
			t.Parallel()
			l := newLayout(t, prefixParts, goldenTable())
			assert.Length(t, l.prefixParts, 3, "the filter of prefixes has three partitions")
			// The first byte of an encoding is the version of its format.
			l.data[l.prefixParts[0].off]++
			l.seal()
			_, err := openTable(t, l.data, sstable.ReaderOptions{Prefix: goldenPrefix}).
				MayContainPrefix(t.Context(), []byte("key 00"))
			assert.ErrorIs(t, err, sstable.ErrCorrupt, "MayContainPrefix refuses the partition")
		})

		t.Run("lends the partition of its prefix with FilterBlock", func(t *testing.T) {
			t.Parallel()
			l := newLayout(t, prefixParts, goldenTable())
			cache := &roleCache{Cache: lendingCache(t, 4)}
			r := openTable(t, l.data, sstable.ReaderOptions{Cache: cache, CacheID: 1, Prefix: goldenPrefix})
			p := []byte("key 03")
			may, err := r.MayContainPrefix(t.Context(), p)
			assert.NoError(t, err, "MayContainPrefix reads the partition of its prefix")
			assert.True(t, may, "MayContainPrefix reports true for a prefix of the table")
			assert.Equal(t, cache.lends, []lookup{{offset: placeOf(l.prefixParts, p), role: sstable.FilterBlock}},
				"MayContainPrefix lends the partition of its prefix with FilterBlock")
		})

		t.Run("skips the tables without a key of the prefix apart from false positives", func(t *testing.T) {
			t.Parallel()
			// The tables of a store of 1,024 logs contain the records of 64
			// logs each. The key of a record is the byte r, the log in 8 bytes
			// and the record in 8 bytes, and the prefix of the log takes 9
			// bytes. A filter of 8 bits admits about one prefix in 256.
			const tables, logs, logsPerTable, records = 16, 1024, 64, 8
			prefix := fixedPrefix{name: firstBytes, n: 9}
			opts := sstable.WriterOptions{BlockSize: 4096, FilterBits: 8, PartitionSize: 1024, Prefix: prefix}
			rng := rand.New(rand.NewPCG(1, 2))
			readers := make([]*sstable.Reader, 0, tables)
			has := make([]map[uint64]bool, 0, tables)
			for range tables {
				chosen := map[uint64]bool{}
				for len(chosen) < logsPerTable {
					chosen[rng.Uint64N(logs)] = true
				}
				es := make([]entry, 0, logsPerTable*records)
				for _, log := range slices.Sorted(maps.Keys(chosen)) {
					for record := range uint64(records) {
						key := binary.BigEndian.AppendUint64(binary.BigEndian.AppendUint64([]byte{'r'}, log), record)
						es = append(es, entry{key: key, seq: 1})
					}
				}
				readers = append(readers, openTable(t, writeTable(t, opts, es), sstable.ReaderOptions{Prefix: prefix}))
				has = append(has, chosen)
			}
			admitted := 0
			for log := range uint64(logs) {
				p := binary.BigEndian.AppendUint64([]byte{'r'}, log)
				for i, r := range readers {
					may, err := r.MayContainPrefix(t.Context(), p)
					assert.NoError(t, err, "MayContainPrefix reads the filter of prefixes")
					if has[i][log] {
						assert.True(t, may, "MayContainPrefix reports true for a table with a key of the prefix")
					} else if may {
						admitted++
					}
				}
			}
			assert.InRange(t, admitted, 0, tables*logs/128, "a filter of 8 bits admits about 1 prefix in 256")
		})
	})

	t.Run("RangeDeletes", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a cursor over the fragments of the table in their order", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "RangeDeletes yields the fragments that AddRangeDelete added", func(c *prop.Case) {
				dels := c.Draw(fragments, "fragments")
				opts := c.Draw(prop.SampledFrom(settings...), "setting").opts
				r := openTable(c, writeTable(c, opts, c.Draw(entries, "entries"), dels...), sstable.ReaderOptions{})
				it := r.RangeDeletes()
				defer it.Close()
				var got []rangeDelete
				for ok := it.First(); ok; ok = it.Next() {
					got = append(got,
						rangeDelete{start: bytes.Clone(it.Start()), end: bytes.Clone(it.End()), seq: it.Seq()})
				}
				assert.Equal(c, got, dels, "RangeDeletes yields the fragments in their order", assert.EquateEmpty())
			})
		})

		t.Run("returns a cursor without positions for a table without fragments", func(t *testing.T) {
			t.Parallel()
			it := openTable(t, writeTable(t, settings[1].opts, goldenTable()), sstable.ReaderOptions{}).RangeDeletes()
			defer it.Close()
			expect.False(t, it.First(), "First finds no fragment")
			expect.False(t, it.Last(), "Last finds no fragment")
			expect.False(t, it.SeekGE([]byte("key 000")), "SeekGE finds no fragment")
			expect.False(t, it.SeekLT([]byte("key 999")), "SeekLT finds no fragment")
		})

		t.Run("returns one fragment for the tombstone of a range of 10,000 keys", func(t *testing.T) {
			t.Parallel()
			// A table contains the 10,000 keys a/0000000 and on and the 100 keys
			// b/0000 and on. A newer table deletes the keys a/... with one
			// tombstone at the sequence 2. A read of the first key finds the
			// fragment that covers it, and a seek to the end of the fragment
			// finds the first live key.
			const deleted, live = 10_000, 100
			value := bytes.Repeat([]byte{'v'}, 100)
			es := make([]entry, 0, deleted+live)
			for i := range deleted {
				es = append(es, entry{key: fmt.Appendf(nil, "a/%07d", i), seq: 1, value: value})
			}
			for i := range live {
				es = append(es, entry{key: fmt.Appendf(nil, "b/%04d", i), seq: 1, value: value})
			}
			older := openTable(t, writeTable(t, settings[5].opts, es), sstable.ReaderOptions{})
			var sink bufferSink
			w, err := sstable.NewWriter(&sink, settings[5].opts)
			assert.NoError(t, err, "NewWriter accepts the options")
			f, err := sstable.NewFragmenter(w)
			assert.NoError(t, err, "NewFragmenter accepts the Writer")
			assert.NoError(t, f.Add(t.Context(), []byte("a/"), []byte("a0"), 2),
				"Add adds the tombstone of the keys a/")
			assert.NoError(t, f.Flush(t.Context()), "Flush passes the fragment to the Writer")
			sum, err := w.Close(t.Context())
			assert.NoError(t, err, "Close writes the table of the tombstone")
			assert.Equal(t, sum.RangeDeletes, int64(1), "the delete of the range takes one fragment")

			it := openTable(t, sink.Bytes(), sstable.ReaderOptions{}).RangeDeletes()
			defer it.Close()
			assert.True(t, it.SeekGE(es[0].key), "SeekGE finds the fragment of the first key")
			expect.Equal(t, it.Start(), []byte("a/"), "the fragment starts at the start of the tombstone")
			expect.Equal(t, it.Seq(), uint64(2), "the fragment has the sequence of the tombstone")
			cursor := older.NewIter(sstable.IterOptions{})
			defer cursor.Close()
			assert.True(t, cursor.SeekGE(t.Context(), it.End(), math.MaxUint64),
				"a seek to the end of the fragment finds an entry")
			assert.Equal(t, cursor.Key(), es[deleted].key,
				"the seek to the end of the fragment finds the first live key")
		})
	})

	t.Run("reads a table of format version 3 as the table without its parts of version 3", func(t *testing.T) {
		t.Parallel()
		const contract = "a table with fragments and prefixes reads as the table of its entries alone"
		prop.ForAll(t, contract, func(c *prop.Case) {
			es := c.Draw(entries, "entries")
			opts := c.Draw(prop.SampledFrom(settings[:7]...), "setting").opts
			plain := parseLayout(writeTable(c, opts, es))
			if opts.FilterBits != 0 {
				opts.Prefix = fixedPrefix{name: firstBytes, n: c.Draw(prop.Integer(0, 3), "prefix length")}
			}
			placed := newLayout(c, opts, es, c.Draw(fragments, "fragments")...)
			assert.Equal(c, placed.blocks, plain.blocks, "the tables have the places of the data blocks")
			r := openTable(c, placed.data, sstable.ReaderOptions{})
			without := openTable(c, plain.data, sstable.ReaderOptions{})
			it := r.NewIter(sstable.IterOptions{})
			defer it.Close()
			got, err := scanAll(c.Context(), it)
			assert.NoError(c, err, "a scan reads the table")
			assert.Equal(c, got, es, "the table contains the entries", assert.EquateEmpty())
			bound := prop.Optional(prop.SampledFrom(keys...))
			for range 10 {
				key, seq := c.Draw(prop.SampledFrom(keys...), "key"), c.Draw(prop.Integer[uint64](0, 6), "seq")
				value, s, ok, err := r.Get(c.Context(), key, seq, nil)
				assert.NoError(c, err, "Get reads the table")
				wantValue, wantSeq, wantOK, err := without.Get(c.Context(), key, seq, nil)
				assert.NoError(c, err, "Get reads the table without the parts of version 3")
				assert.Equal(c, ok, wantOK, "Get finds an entry where it finds one in the other table")
				assert.Equal(c, s, wantSeq, "Get returns the sequence of the other table")
				assert.Equal(c, value, wantValue, "Get returns the value of the other table")
				var lower, upper []byte
				if p := c.Draw(bound, "lower"); p != nil {
					lower = *p
				}
				if p := c.Draw(bound, "upper"); p != nil {
					upper = *p
				}
				span, err := r.Span(c.Context(), lower, upper)
				assert.NoError(c, err, "Span searches the index of the table")
				wantSpan, err := without.Span(c.Context(), lower, upper)
				assert.NoError(c, err, "Span searches the index of the other table")
				assert.Equal(c, span, wantSpan, "Span returns the Span of the other table")
			}
		})
	})

	t.Run("Get", func(t *testing.T) {
		t.Parallel()

		for _, tt := range settings {
			t.Run("returns what a sorted model returns for a table "+tt.name, func(t *testing.T) {
				t.Parallel()
				prop.ForAll(t, "Get returns the first entry at or after its key and sequence when it has the key",
					func(c *prop.Case) {
						es := c.Draw(entries, "entries")
						r := openTable(c, writeTable(c, tt.opts, es), sstable.ReaderOptions{})
						for range 20 {
							probe := entry{
								key: c.Draw(prop.SampledFrom(keys...), "key"),
								seq: c.Draw(prop.Integer[uint64](0, 6), "seq"),
							}
							value, seq, ok, err := r.Get(c.Context(), probe.key, probe.seq, nil)
							assert.NoError(c, err, "Get reads the table")
							i, _ := slices.BinarySearchFunc(es, probe, func(a, b entry) int {
								return sstable.Compare(a.key, a.seq, b.key, b.seq)
							})
							if i == len(es) || !bytes.Equal(es[i].key, probe.key) {
								assert.False(c, ok,
									"Get finds no entry when the entry at or after the probe has another key")

								continue
							}
							assert.True(c, ok, "Get finds the entry")
							assert.Equal(c, value, es[i].value, "Get returns the value of the entry",
								assert.EquateEmpty())
							assert.Equal(c, seq, es[i].seq, "Get returns the sequence of the entry")
						}
					})
			})
		}

		t.Run("appends the value to dst", func(t *testing.T) {
			t.Parallel()
			r := openTable(t, writeTable(t, settings[2].opts, goldenTable()), sstable.ReaderOptions{})
			got, _, ok, err := r.Get(t.Context(), []byte("key 000"), 2, []byte("prefix "))
			assert.NoError(t, err, "Get reads the table")
			assert.True(t, ok, "Get finds the entry")
			assert.Equal(t, got, []byte("prefix value of key 000 at 2. "), "Get keeps the bytes of dst")
		})

		t.Run("returns dst for a key that the table does not contain", func(t *testing.T) {
			t.Parallel()
			r := openTable(t, writeTable(t, settings[0].opts, goldenTable()), sstable.ReaderOptions{})
			dst := []byte("prefix")
			got, _, ok, err := r.Get(t.Context(), []byte("missing"), 1, dst)
			assert.NoError(t, err, "Get reads the table")
			expect.False(t, ok, "Get finds no entry")
			expect.Equal(t, got, dst, "Get returns dst unchanged", assert.ByIdentity())
		})

		for _, tt := range []struct {
			name     string
			giveOpts sstable.WriterOptions
		}{
			{name: "returns dst for a key that the filter refuses", giveOpts: sstable.WriterOptions{FilterBits: 16}},
			{
				name:     "returns dst for a key that a filter partition refuses",
				giveOpts: sstable.WriterOptions{FilterBits: 16, PartitionSize: partitionBytes},
			},
		} {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				r := openTable(t, writeTable(t, tt.giveOpts, goldenTable()), sstable.ReaderOptions{})
				may, err := r.MayContain(t.Context(), []byte(absentKey))
				assert.NoError(t, err, "MayContain reads the filter")
				assert.False(t, may, "the filter refuses the key")
				dst := []byte("prefix")
				got, _, ok, err := r.Get(t.Context(), []byte(absentKey), 1, dst)
				assert.NoError(t, err, "Get reads no data block")
				expect.False(t, ok, "Get finds no entry")
				expect.Equal(t, got, dst, "Get returns dst unchanged", assert.ByIdentity())
			})
		}

		t.Run("reads the filter partition alone for a key that the partition refuses", func(t *testing.T) {
			t.Parallel()
			data := writeTable(t, sstable.WriterOptions{FilterBits: 16, PartitionSize: partitionBytes}, goldenTable())
			counter := &countingFile{data: data}
			_, err := sstable.Open(t.Context(), counter, int64(len(data)), sstable.ReaderOptions{})
			assert.NoError(t, err, "Open reads the table")
			failing := &failingFile{data: data, ok: counter.reads + 1}
			r, err := sstable.Open(t.Context(), failing, int64(len(data)), sstable.ReaderOptions{})
			assert.NoError(t, err, "Open reads the table with the reads that it needs")
			_, _, ok, err := r.Get(t.Context(), []byte(absentKey), 1, nil)
			expect.NoError(t, err, "Get reads the filter partition for a key that the partition refuses")
			expect.False(t, ok, "Get finds no entry")
			_, _, _, err = r.Get(t.Context(), []byte("key 001"), 2, nil)
			expect.ErrorIs(t, err, errRead, "Get of a key of the table reads its index partition and its block")
		})

		t.Run("reports false without a read for a key that the filter refuses", func(t *testing.T) {
			t.Parallel()
			data := writeTable(t, sstable.WriterOptions{FilterBits: 16}, goldenTable())
			counter := &countingFile{data: data}
			_, err := sstable.Open(t.Context(), counter, int64(len(data)), sstable.ReaderOptions{})
			assert.NoError(t, err, "Open reads the table")
			failing := &failingFile{data: data, ok: counter.reads}
			r, err := sstable.Open(t.Context(), failing, int64(len(data)), sstable.ReaderOptions{})
			assert.NoError(t, err, "Open reads the table with the reads that it needs")
			may, err := r.MayContain(t.Context(), []byte(absentKey))
			assert.NoError(t, err, "MayContain asks the filter in memory")
			assert.False(t, may, "the filter refuses the key")
			_, _, ok, err := r.Get(t.Context(), []byte(absentKey), 1, nil)
			expect.NoError(t, err, "Get reads no block for a key that the filter refuses")
			expect.False(t, ok, "Get finds no entry")
			_, _, _, err = r.Get(t.Context(), []byte("key 001"), 2, nil)
			expect.ErrorIs(t, err, errRead, "Get of a key of the table reads its block")
		})

		t.Run("returns a block of the cache without a read", func(t *testing.T) {
			t.Parallel()
			data := writeTable(t, settings[2].opts, goldenTable())
			counter := &countingFile{data: data}
			_, err := sstable.Open(t.Context(), counter, int64(len(data)), sstable.ReaderOptions{})
			assert.NoError(t, err, "Open reads the table")
			cache := &mapCache{}
			r, err := sstable.Open(t.Context(), &failingFile{data: data, ok: counter.reads + 1}, int64(len(data)),
				sstable.ReaderOptions{Cache: cache, CacheID: 7})
			assert.NoError(t, err, "Open reads the table with the reads that it needs")
			for range 2 {
				_, _, ok, getErr := r.Get(t.Context(), []byte("key 059"), 1, nil)
				assert.NoError(t, getErr, "Get reads the block once and then takes it from the cache")
				assert.True(t, ok, "Get finds the entry")
			}
			expect.Equal(t, cache.adds, 1, "Get adds the block that it read to the cache")
			_, _, _, err = r.Get(t.Context(), []byte("key 000"), 2, nil)
			expect.ErrorIs(t, err, errRead, "Get of a block that the cache does not contain reads it")
		})

		t.Run("adds the partitions that it reads to the cache under its context", func(t *testing.T) {
			t.Parallel()
			cache := &mapCache{}
			data := writeTable(t, settings[5].opts, goldenTable())
			r := openTable(t, data, sstable.ReaderOptions{Cache: cache, CacheID: 1})
			_, _, ok, err := r.Get(context.WithValue(t.Context(), callKey{}, readGet), []byte("key 030"), 1, nil)
			assert.NoError(t, err, "Get reads the table")
			assert.True(t, ok, "Get finds the entry")
			assert.Equal(t, cache.adds, 3, "Get adds the filter partition, the index partition and the block")
			assert.Equal(t, cache.call, any(readGet), "the cache receives the context of Get")
		})

		t.Run("adds the block that it reads to the cache under its context", func(t *testing.T) {
			t.Parallel()
			cache := &mapCache{}
			data := writeTable(t, settings[0].opts, goldenTable())
			r := openTable(t, data, sstable.ReaderOptions{Cache: cache, CacheID: 1})
			_, _, ok, err := r.Get(context.WithValue(t.Context(), callKey{}, readGet), []byte("key 030"), 1, nil)
			assert.NoError(t, err, "Get reads the block")
			assert.True(t, ok, "Get finds the entry")
			assert.Equal(t, cache.adds, 1, "Get adds the block to the cache")
			assert.Equal(t, cache.call, any(readGet), "the cache receives the context of Get")
		})

		t.Run("looks its block up in the cache under its context", func(t *testing.T) {
			t.Parallel()
			cache := &mapCache{}
			data := writeTable(t, settings[0].opts, goldenTable())
			r := openTable(t, data, sstable.ReaderOptions{Cache: cache, CacheID: 1})
			_, _, ok, err := r.Get(t.Context(), []byte("key 030"), 1, nil)
			assert.NoError(t, err, "the first Get adds the block to the cache")
			assert.True(t, ok, "the first Get finds the entry")
			_, _, ok, err = r.Get(context.WithValue(t.Context(), callKey{}, readGet), []byte("key 030"), 1, nil)
			assert.NoError(t, err, "the second Get takes the block from the cache")
			assert.True(t, ok, "the second Get finds the entry")
			assert.Equal(t, cache.adds, 1, "the second Get does not add a block")
			assert.Equal(t, cache.call, any(readGet), "the cache receives the context of the second Get")
		})

		t.Run("lends each block of the key with the role of the block", func(t *testing.T) {
			t.Parallel()
			l := newLayout(t, settings[5].opts, goldenTable())
			cache := &roleCache{Cache: lendingCache(t, 4)}
			r := openTable(t, l.data, sstable.ReaderOptions{Cache: cache, CacheID: 1})
			key := []byte("key 030")
			_, _, ok, err := r.Get(t.Context(), key, 2, nil)
			assert.NoError(t, err, "Get reads the table")
			assert.True(t, ok, "Get finds the entry")
			assert.Equal(t, cache.lends, []lookup{
				{offset: placeOf(l.filterParts, key), role: sstable.FilterBlock},
				{offset: placeOf(l.indexParts, key), role: sstable.IndexBlock},
				{offset: placeOf(l.blocks, key), role: sstable.DataBlock},
			}, "Get lends the filter partition, the index partition and the data block of the key with their roles")
		})

		t.Run("adds each block that misses with the role of its Lend", func(t *testing.T) {
			t.Parallel()
			cache := &roleCache{Cache: lendingCache(t, 4)}
			data := writeTable(t, settings[5].opts, goldenTable())
			r := openTable(t, data, sstable.ReaderOptions{Cache: cache, CacheID: 1})
			_, _, ok, err := r.Get(t.Context(), []byte("key 030"), 2, nil)
			assert.NoError(t, err, "Get reads the table")
			assert.True(t, ok, "Get finds the entry")
			assert.Length(t, cache.lends, 3, "Get lends the filter partition, the index partition and the data block")
			assert.Equal(t, cache.adds, cache.lends, "Get adds each block that missed with the role of its Lend")
		})

		t.Run("lends the data block of a table without partitions with DataBlock", func(t *testing.T) {
			t.Parallel()
			l := newLayout(t, settings[1].opts, goldenTable())
			cache := &roleCache{Cache: lendingCache(t, 4)}
			r := openTable(t, l.data, sstable.ReaderOptions{Cache: cache, CacheID: 1})
			key := []byte("key 030")
			_, _, ok, err := r.Get(t.Context(), key, 2, nil)
			assert.NoError(t, err, "Get reads the table")
			assert.True(t, ok, "Get finds the entry")
			assert.Equal(t, cache.lends, []lookup{{offset: placeOf(l.blocks, key), role: sstable.DataBlock}},
				"Get lends the data block alone, because the index block and the filter block are in memory")
		})

		t.Run("returns the error of the context of its read", func(t *testing.T) {
			t.Parallel()
			r := openTable(t, writeTable(t, settings[0].opts, goldenTable()), sstable.ReaderOptions{})
			assert.HonoursCancellation(t, func(ctx context.Context) error {
				_, _, _, err := r.Get(ctx, []byte("key 030"), 1, nil)

				return err
			}, "Get returns the error that the File returns for a context that ended")
		})

		t.Run("returns the error of the context for a block of the cache", func(t *testing.T) {
			t.Parallel()
			r := warmReader(t, writeTable(t, settings[0].opts, goldenTable()))
			assert.HonoursCancellation(t, func(ctx context.Context) error {
				_, _, _, err := r.Get(ctx, []byte("key 030"), 1, nil)

				return err
			}, "Get returns the error of a context that ended for a block that the cache contains")
		})

		t.Run("returns the error of the File", func(t *testing.T) {
			t.Parallel()
			data := writeTable(t, settings[0].opts, goldenTable())
			counter := &countingFile{data: data}
			_, err := sstable.Open(t.Context(), counter, int64(len(data)), sstable.ReaderOptions{})
			assert.NoError(t, err, "Open reads the table")
			failing := &failingFile{data: data, ok: counter.reads}
			r, err := sstable.Open(t.Context(), failing, int64(len(data)), sstable.ReaderOptions{})
			assert.NoError(t, err, "Open reads the table with the reads that it needs")
			_, _, _, err = r.Get(t.Context(), []byte("key 030"), 1, nil)
			assert.ErrorIs(t, err, errRead, "Get returns the error of the read")
		})

		t.Run("returns ErrCorrupt for a key of a data block at the place of another", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Get refuses a valid data block at the place of the block of its key", func(c *prop.Case) {
				es := c.Draw(uniform, "entries")
				l := newLayout(c, oneEntry, es)
				to := c.Draw(prop.Integer(1, len(l.blocks)-1), "place")
				from := c.Draw(prop.Integer(0, len(l.blocks)-2), "block")
				if from >= to {
					from++
				}
				b := l.blocks[from]
				copy(l.data[l.blocks[to].off:], l.data[b.off:b.off+b.n+trailerLen])
				r := openTable(c, l.data, sstable.ReaderOptions{})
				_, _, _, err := r.Get(c.Context(), es[to].key, es[to].seq, nil)
				assert.ErrorIs(c, err, sstable.ErrCorrupt, "Get refuses the block at the place of the block of its key")
			})
		})
	})

	t.Run("NewIter", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a cursor on no entry", func(t *testing.T) {
			t.Parallel()
			it := openTable(t, writeTable(t, settings[0].opts, goldenTable()), sstable.ReaderOptions{}).
				NewIter(sstable.IterOptions{})
			defer it.Close()
			expect.Nil(t, it.Key(), "the new cursor has no key")
			expect.False(t, it.Next(t.Context()), "the new cursor has no next entry")
		})
	})
}

// FuzzOpen checks openChanged on the changes that a fuzzer finds.
func FuzzOpen(f *testing.F) {
	prop.Fuzz(f, changeContract, openChanged)
}

// TestReaderAllocs checks the allocation contract of the methods of a
// Reader on a hot path. MaxAllocs counts the allocations of the whole
// process, so the test does not run in parallel.
func TestReaderAllocs(t *testing.T) {
	data := writeTable(t, settings[3].opts, goldenTable())
	parts := writeTable(t, settings[6].opts, goldenTable())
	dst := make([]byte, 0, 256)

	t.Run("Footprint", func(t *testing.T) {
		r := openTable(t, data, sstable.ReaderOptions{})
		n := 0
		expect.MaxAllocs(t, func() { n = r.Footprint() }, 0, "Footprint does not allocate")
		assert.NotEqual(t, n, 0, "the test measures the footprint of a table")
	})

	t.Run("Span", func(t *testing.T) {
		for _, tt := range []struct {
			name string
			data []byte
		}{
			{name: "of a table", data: data},
			{name: "of a table in partitions", data: parts},
		} {
			t.Run(tt.name, func(t *testing.T) {
				r := openTable(t, tt.data, sstable.ReaderOptions{})
				lower, upper := []byte("key 010"), []byte("key 050")
				var got sstable.Span
				var err error
				expect.MaxAllocs(t, func() { got, err = r.Span(t.Context(), lower, upper) }, 0,
					"Span does not allocate once its pooled cursor has grown")
				expect.NoError(t, err, "the test measures a Span of a valid index")
				expect.NotEqual(t, got.Blocks, 0, "the test measures the blocks of a range")
			})
		}
	})

	t.Run("Get", func(t *testing.T) {
		for _, tt := range []struct {
			name  string
			data  []byte
			cache sstable.BlockCache
		}{
			{name: "without a cache", data: data},
			{name: "from a cache", data: data, cache: &mapCache{}},
			{name: "from a lending cache", data: data, cache: lendingCache(t, 4)},
			{name: "of a table in partitions without a cache", data: parts},
			{name: "of a table in partitions from a cache", data: parts, cache: &mapCache{}},
			{name: "of a table in partitions from a lending cache", data: parts, cache: lendingCache(t, 4)},
		} {
			t.Run(tt.name, func(t *testing.T) {
				r := openTable(t, tt.data, sstable.ReaderOptions{Cache: tt.cache, CacheID: 1})
				var ok bool
				var err error
				expect.MaxAllocs(t, func() { _, _, ok, err = r.Get(t.Context(), []byte("key 030"), 1, dst) }, 0,
					"Get does not allocate once its pooled cursor has grown and its block is in the cache")
				expect.NoError(t, err, "the test measures a Get that reads")
				expect.True(t, ok, "the test measures a Get of an entry")
			})
		}

		t.Run("of blocks that a lending cache does not contain", func(t *testing.T) {
			r := openTable(t, data, sstable.ReaderOptions{Cache: lendingCache(t, 4), CacheID: 1})
			i := 0
			var ok bool
			var err error
			expect.MaxAllocs(t, func() {
				_, _, ok, err = r.Get(t.Context(), goldenKeys[missStride*i%len(goldenKeys)], 1, dst)
				i++
			}, 1, "a Get that misses a lending cache allocates the entry of the cache alone")
			expect.NoError(t, err, "the test measures Gets that read")
			expect.True(t, ok, "the test measures Gets of entries")
		})

		t.Run("of blocks that a lending cache evicts after a hit", func(t *testing.T) {
			r := openTable(t, data, sstable.ReaderOptions{Cache: lendingCache(t, 4), CacheID: 1})
			i := 0
			var ok bool
			var miss, hit error
			// The collections empty the pools of the buffers that earlier tests
			// released. A Get that does not release a lent block then allocates
			// a buffer for each later fill.
			runtime.GC()
			runtime.GC()
			expect.MaxAllocs(t, func() {
				// The first Get adds the block of the key, and the second takes
				// the block from the cache.
				key := goldenKeys[missStride*i%len(goldenKeys)]
				_, _, _, miss = r.Get(t.Context(), key, 1, dst)
				_, _, ok, hit = r.Get(t.Context(), key, 1, dst)
				i++
			}, 1, "a Get releases a lent block, so the block returns to its pool when the cache evicts it")
			expect.NoError(t, miss, "the test measures Gets that read")
			expect.NoError(t, hit, "the test measures Gets that take their block from the cache")
			expect.True(t, ok, "the test measures Gets of entries")
		})
	})

	t.Run("MayContain", func(t *testing.T) {
		for _, tt := range []struct {
			name string
			data []byte
		}{
			{name: "of a table", data: data},
			{name: "of a table in partitions", data: parts},
		} {
			t.Run(tt.name, func(t *testing.T) {
				r := openTable(t, tt.data, sstable.ReaderOptions{})
				var ok bool
				var err error
				expect.MaxAllocs(t, func() { ok, err = r.MayContain(t.Context(), []byte("key 030")) }, 0,
					"MayContain does not allocate once its pooled cursor has grown")
				expect.NoError(t, err, "the test measures a MayContain that reads")
				expect.True(t, ok, "the test measures a key of the table")
			})
		}
	})

	t.Run("MayContainPrefix", func(t *testing.T) {
		for _, tt := range []struct {
			name string
			opts sstable.WriterOptions
		}{
			{name: "of a table", opts: settings[7].opts},
			{name: "of a table in partitions", opts: settings[8].opts},
		} {
			t.Run(tt.name, func(t *testing.T) {
				r := openTable(t, writeTable(t, tt.opts, goldenTable()), sstable.ReaderOptions{Prefix: goldenPrefix})
				p := []byte("key 03")
				var ok bool
				var err error
				expect.MaxAllocs(t, func() { ok, err = r.MayContainPrefix(t.Context(), p) }, 0,
					"MayContainPrefix does not allocate once its pooled cursor has grown")
				expect.NoError(t, err, "the test measures a MayContainPrefix that reads")
				expect.True(t, ok, "the test measures a prefix of the table")
			})
		}
	})

	t.Run("RangeDeletes", func(t *testing.T) {
		r := openTable(t, writeTable(t, settings[7].opts, goldenTable(), goldenDeletes...), sstable.ReaderOptions{})
		key := []byte("key 017")
		var seqs uint64
		// The collections empty the pool of the cursors that earlier tests
		// closed, so a cursor that Close does not return allocates in the
		// count.
		runtime.GC()
		runtime.GC()
		expect.MaxAllocs(t, func() {
			it := r.RangeDeletes()
			for ok := it.SeekGE(key); ok && bytes.Compare(it.Start(), key) <= 0; ok = it.Next() {
				seqs += it.Seq()
			}
			it.Close()
		}, 0, "RangeDeletes takes a cursor of the pool, and its moves do not allocate")
		expect.NotEqual(t, seqs, uint64(0), "the test measures the fragments of a key")
	})

	t.Run("NewIter", func(t *testing.T) {
		r := openTable(t, data, sstable.ReaderOptions{})
		var err error
		expect.MaxAllocs(t, func() { err = r.NewIter(sstable.IterOptions{}).Close() }, 0,
			"NewIter takes a cursor of the pool")
		expect.NoError(t, err, "the test measures a Close that succeeds")
	})
}

// BenchmarkReader reports the cost of the methods of a Reader on a hot
// path, and fails when a method allocates.
func BenchmarkReader(b *testing.B) {
	l := newLayout(b, settings[3].opts, goldenTable())
	lp := newLayout(b, settings[6].opts, goldenTable())
	data := l.data
	dst := make([]byte, 0, 256)

	b.Run("Get", func(b *testing.B) {
		// Each lending cache has room for every block and every partition of
		// its table.
		for _, tt := range []struct {
			name  string
			data  []byte
			cache sstable.BlockCache
		}{
			{name: "without a cache", data: data},
			{name: "from a cache", data: data, cache: &mapCache{}},
			{name: "from a lending cache", data: data, cache: lendingCache(b, len(l.blocks))},
			{name: "of a table in partitions without a cache", data: lp.data},
			{name: "of a table in partitions from a cache", data: lp.data, cache: &mapCache{}},
			{
				name: "of a table in partitions from a lending cache", data: lp.data,
				cache: lendingCache(b, len(lp.blocks)+len(lp.indexParts)+len(lp.filterParts)),
			},
		} {
			b.Run(tt.name, func(b *testing.B) {
				r := openTable(b, tt.data, sstable.ReaderOptions{Cache: tt.cache, CacheID: 1})
				var ok bool
				var err error
				i := 0

				c := bench.Start(b).MaxAllocs(0)
				defer c.End()

				for c.Loop() {
					_, _, ok, err = r.Get(b.Context(), goldenKeys[i%len(goldenKeys)], 1, dst)
					i++
				}

				assert.NoError(b, err, "the benchmark measures Gets that read")
				assert.True(b, ok, "the benchmark measures Gets of entries")
			})
		}

		b.Run("of blocks that a lending cache does not contain", func(b *testing.B) {
			r := openTable(b, data, sstable.ReaderOptions{Cache: lendingCache(b, 4), CacheID: 1})
			var ok bool
			var err error
			i := 0

			c := bench.Start(b).MaxAllocs(1)
			defer c.End()

			for c.Loop() {
				_, _, ok, err = r.Get(b.Context(), goldenKeys[missStride*i%len(goldenKeys)], 1, dst)
				i++
			}

			assert.NoError(b, err, "the benchmark measures Gets that read")
			assert.True(b, ok, "the benchmark measures Gets of entries")
		})

		b.Run("of blocks that a lending cache evicts after a hit", func(b *testing.B) {
			r := openTable(b, data, sstable.ReaderOptions{Cache: lendingCache(b, 4), CacheID: 1})
			var ok bool
			var miss, hit error
			i := 0

			c := bench.Start(b).MaxAllocs(1)
			defer c.End()

			for c.Loop() {
				key := goldenKeys[missStride*i%len(goldenKeys)]
				_, _, _, miss = r.Get(b.Context(), key, 1, dst)
				_, _, ok, hit = r.Get(b.Context(), key, 1, dst)
				i++
			}

			assert.NoError(b, miss, "the benchmark measures Gets that read")
			assert.NoError(b, hit, "the benchmark measures Gets that take their block from the cache")
			assert.True(b, ok, "the benchmark measures Gets of entries")
		})
	})

	b.Run("MayContain", func(b *testing.B) {
		for _, tt := range []struct {
			name string
			data []byte
		}{
			{name: "of a table", data: data},
			{name: "of a table in partitions", data: lp.data},
		} {
			b.Run(tt.name, func(b *testing.B) {
				r := openTable(b, tt.data, sstable.ReaderOptions{})
				// A conversion in the loop would copy the key in each call,
				// because the key of a table in partitions reaches the
				// comparisons of a search.
				key := []byte("key 030")
				var ok bool
				var err error

				c := bench.Start(b).MaxAllocs(0)
				defer c.End()

				for c.Loop() {
					ok, err = r.MayContain(b.Context(), key)
				}

				assert.NoError(b, err, "the benchmark measures a MayContain that reads")
				assert.True(b, ok, "the benchmark measures a key of the table")
			})
		}
	})

	b.Run("MayContainPrefix", func(b *testing.B) {
		for _, tt := range []struct {
			name string
			opts sstable.WriterOptions
		}{
			{name: "of a table", opts: settings[7].opts},
			{name: "of a table in partitions", opts: settings[8].opts},
		} {
			b.Run(tt.name, func(b *testing.B) {
				r := openTable(b, writeTable(b, tt.opts, goldenTable()), sstable.ReaderOptions{Prefix: goldenPrefix})
				p := []byte("key 03")
				var ok bool
				var err error

				c := bench.Start(b).MaxAllocs(0)
				defer c.End()

				for c.Loop() {
					ok, err = r.MayContainPrefix(b.Context(), p)
				}

				assert.NoError(b, err, "the benchmark measures a MayContainPrefix that reads")
				assert.True(b, ok, "the benchmark measures a prefix of the table")
			})
		}
	})

	b.Run("RangeDeletes", func(b *testing.B) {
		// The key is in the fragment from key 015 up to key 020 at the
		// sequences 9 and 7.
		r := openTable(b, writeTable(b, settings[7].opts, goldenTable(), goldenDeletes...), sstable.ReaderOptions{})
		key := []byte("key 017")
		var seqs uint64

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			seqs = 0
			it := r.RangeDeletes()
			for ok := it.SeekGE(key); ok && bytes.Compare(it.Start(), key) <= 0; ok = it.Next() {
				seqs += it.Seq()
			}
			it.Close()
		}

		assert.Equal(b, seqs, uint64(16), "the benchmark measures the two sequences of the fragment of the key")
	})

	b.Run("Span", func(b *testing.B) {
		// A table of spanEntries values of 100 bytes in blocks of 4 KiB has
		// about 35 entries in each of its 2,703 blocks.
		value := bytes.Repeat([]byte{'v'}, 100)
		es := make([]entry, 0, spanEntries)
		for i := range spanEntries {
			es = append(es, entry{key: fmt.Appendf(nil, spanKey, i), seq: 1, value: value})
		}
		tables := []struct {
			name string
			opts sstable.WriterOptions
		}{
			{name: "of a table", opts: sstable.WriterOptions{BlockSize: 4096, FilterBits: 8}},
			{
				name: "of a table in partitions",
				opts: sstable.WriterOptions{BlockSize: 4096, FilterBits: 8, PartitionSize: 4096},
			},
		}

		tests := []struct {
			name       string
			giveLower  []byte
			giveUpper  []byte
			wantBlocks int
		}{
			{
				name:       "one block",
				giveLower:  fmt.Appendf(nil, spanKey, 50_000),
				giveUpper:  fmt.Appendf(nil, spanKey, 50_010),
				wantBlocks: 1,
			},
			{
				name:       "ten blocks",
				giveLower:  fmt.Appendf(nil, spanKey, 50_000),
				giveUpper:  fmt.Appendf(nil, spanKey, 50_340),
				wantBlocks: 10,
			},
			{
				name:       "a quarter",
				giveLower:  fmt.Appendf(nil, spanKey, 25_000),
				giveUpper:  fmt.Appendf(nil, spanKey, 50_000),
				wantBlocks: 677,
			},
			{name: "every block", wantBlocks: 2703},
		}
		for _, table := range tables {
			b.Run(table.name, func(b *testing.B) {
				r := openTable(b, writeTable(b, table.opts, es), sstable.ReaderOptions{})
				for _, tt := range tests {
					b.Run(tt.name, func(b *testing.B) {
						var got sstable.Span
						var err error

						c := bench.Start(b).MaxAllocs(0)
						defer c.End()

						for c.Loop() {
							got, err = r.Span(b.Context(), tt.giveLower, tt.giveUpper)
						}

						assert.NoError(b, err, "the benchmark measures a Span of a valid index")
						assert.Equal(b, got.Blocks, tt.wantBlocks, "the benchmark measures the blocks of the range")
					})
				}
			})
		}
	})
}

// readSteps writes drawn entries into a table under a drawn setting, and
// opens the table with cache. Clients read the table through the one Reader
// at once: Get, MayContain, Span, and the entries after a SeekGE or before a
// SeekLT of a cursor with or without NoFill. Each read returns what it finds
// as text, and an error returns its own text. The spec computes the text of
// each read from the entries and the layout of the table, so the history of
// the reads is linearizable when every read returns what the entries return.
// The filter decides MayContain of a key that the table does not contain, so
// the spec takes that answer from a Reader without a cache.
func readSteps(c *prop.Case, cache sstable.BlockCache) {
	es := c.Draw(entries, "entries")
	data := writeTable(c, c.Draw(prop.SampledFrom(settings...), "setting").opts, es)
	l := parseLayout(data)
	r := openTable(c, data, sstable.ReaderOptions{Cache: cache, CacheID: 1})
	plain := openTable(c, data, sstable.ReaderOptions{})
	admitted := make(map[string]bool, len(keys))
	for _, key := range keys {
		may, err := plain.MayContain(c.Context(), key)
		assert.NoError(c, err, "MayContain reads the filter of the table")
		admitted[string(key)] = may
	}
	key, seq, bound := prop.SampledFrom(keys...), prop.Integer[uint64](0, 6), prop.Optional(prop.SampledFrom(keys...))

	// seek returns the read of the cursor move name, which reads seekReads
	// entries from its target on or back.
	seek := func(name string) stateful.Action[struct{}] {
		return stateful.Action[struct{}]{
			Name: name,
			Input: func(c *prop.Case, _ struct{}) any {
				return []any{c.Draw(key, "key"), c.Draw(seq, "seq"), c.Draw(prop.Boolean(), "no fill")}
			},
			Run: func(c *prop.Case, client int, in any) {
				args := in.([]any)
				call := c.History().Invoke(client, name, args)
				k, s := args[0].([]byte), args[1].(uint64)
				it := r.NewIter(sstable.IterOptions{NoFill: args[2].(bool)})
				ok := name == moveSeekGE && it.SeekGE(c.Context(), k, s) ||
					name == moveSeekLT && it.SeekLT(c.Context(), k, s)
				var text []byte
				for n := 0; ok && n < seekReads; n++ {
					text = fmt.Appendf(text, "%q %d %q;", it.Key(), it.Seq(), it.Value())
					if name == moveSeekGE {
						ok = it.Next(c.Context())
					} else {
						ok = it.Prev(c.Context())
					}
				}
				if err := it.Close(); err != nil {
					text = fmt.Append(text, err)
				}
				call.OK(string(text))
			},
		}
	}

	actions := []stateful.Action[struct{}]{
		{
			Name: readGet,
			Input: func(c *prop.Case, _ struct{}) any {
				return []any{c.Draw(key, "key"), c.Draw(seq, "seq")}
			},
			Run: func(c *prop.Case, client int, in any) {
				args := in.([]any)
				call := c.History().Invoke(client, readGet, args)
				k := args[0].([]byte)
				value, s, ok, err := r.Get(c.Context(), k, args[1].(uint64), nil)
				var text []byte
				if err != nil {
					text = fmt.Append(text, err)
				} else if ok {
					text = fmt.Appendf(text, "%q %d %q;", k, s, value)
				}
				call.OK(string(text))
			},
		},
		{
			Name: readMayContain,
			Input: func(c *prop.Case, _ struct{}) any {
				return []any{c.Draw(key, "key")}
			},
			Run: func(c *prop.Case, client int, in any) {
				args := in.([]any)
				call := c.History().Invoke(client, readMayContain, args)
				may, err := r.MayContain(c.Context(), args[0].([]byte))
				if err != nil {
					call.OK(err.Error())

					return
				}
				call.OK(strconv.FormatBool(may))
			},
		},
		{
			Name: readSpan,
			Input: func(c *prop.Case, _ struct{}) any {
				var lower, upper []byte
				if p := c.Draw(bound, "lower"); p != nil {
					lower = *p
				}
				if p := c.Draw(bound, "upper"); p != nil {
					upper = *p
				}

				return []any{lower, upper}
			},
			Run: func(c *prop.Case, client int, in any) {
				args := in.([]any)
				call := c.History().Invoke(client, readSpan, args)
				s, err := r.Span(c.Context(), args[0].([]byte), args[1].([]byte))
				if err != nil {
					call.OK(err.Error())

					return
				}
				call.OK(fmt.Sprintf("%d %d", s.Blocks, s.Bytes))
			},
		},
		seek(moveSeekGE),
		seek(moveSeekLT),
	}

	stateful.Steps(c, stateful.Machine[struct{}]{
		Spec: history.Spec[struct{}]{
			Initial: func() struct{} { return struct{}{} },
			Next: func(state struct{}, op history.Operation) []struct{} {
				var want []byte
				if op.Name == readSpan {
					s := spanOf(l, op.Args[0].([]byte), op.Args[1].([]byte))
					want = fmt.Appendf(want, "%d %d", s.Blocks, s.Bytes)
				} else {
					// The other reads take a key. MayContain looks for the first
					// entry of the key, and the others take a sequence.
					p := entry{key: op.Args[0].([]byte), seq: math.MaxUint64}
					if op.Name != readMayContain {
						p.seq = op.Args[1].(uint64)
					}
					i, _ := slices.BinarySearchFunc(es, p, func(a, b entry) int {
						return sstable.Compare(a.key, a.seq, b.key, b.seq)
					})
					found := i < len(es) && bytes.Equal(es[i].key, p.key)
					switch op.Name {
					case readMayContain:
						want = strconv.AppendBool(want, found || admitted[string(p.key)])
					case readGet:
						if found {
							want = fmt.Appendf(want, "%q %d %q;", p.key, es[i].seq, es[i].value)
						}
					case moveSeekGE:
						for _, e := range es[i:min(i+seekReads, len(es))] {
							want = fmt.Appendf(want, "%q %d %q;", e.key, e.seq, e.value)
						}
					default:
						for _, e := range slices.Backward(es[max(i-seekReads, 0):i]) {
							want = fmt.Appendf(want, "%q %d %q;", e.key, e.seq, e.value)
						}
					}
				}
				if op.Returned(string(want)) {
					return []struct{}{state}
				}

				return nil
			},
		},
		Actions: actions,
	}, stateful.Clients(readers))
}

// spanOf returns the Span of the range from lower to upper of the table of l,
// from the last keys of its data blocks. The first block is the first whose
// last key is at or after lower. The last block is the first whose last key
// is at or after upper, or the last block of the table. A range whose upper
// is at or before lower has the zero Span, and so does a range whose lower
// is after every block.
func spanOf(l *layout, lower, upper []byte) sstable.Span {
	first := slices.IndexFunc(l.blocks, func(b place) bool { return bytes.Compare(b.last, lower) >= 0 })
	if first < 0 || upper != nil && bytes.Compare(lower, upper) >= 0 {
		return sstable.Span{}
	}
	last := slices.IndexFunc(l.blocks, func(b place) bool { return upper != nil && bytes.Compare(b.last, upper) >= 0 })
	if last < 0 {
		last = len(l.blocks) - 1
	}
	end := l.blocks[last].off + l.blocks[last].n + trailerLen

	return sstable.Span{Blocks: last - first + 1, Bytes: int64(end - l.blocks[first].off)}
}

// placeFields returns the offsets in the table of the varints of the offset
// and of the length of the part i of the block of places of l: 0 for the
// filter, 1 for the block of fragments and 2 for the filter of prefixes. The
// flags of the block are its first varint.
func placeFields(l *layout, i int) (off, n int) {
	at := l.places.off
	_, k := binary.Uvarint(l.data[at:])
	for range i + 1 {
		at += k
		_, n1 := binary.Uvarint(l.data[at:])
		_, n2 := binary.Uvarint(l.data[at+n1:])
		off, n, k = at, at+n1, n1+n2+crcLen
	}

	return off, n
}

// fragmentEntry returns the internal key and the end of the entry i of the
// block of fragments of l, as parts of the bytes of the table. Every entry of
// the block is a restart point.
func fragmentEntry(l *layout, i int) (ikey, end []byte) {
	block := l.data[l.fragments.off : l.fragments.off+l.fragments.n]

	return restartEntry(block[restarts(block)[i]:])
}

// placeOf returns the offset of the first of places whose last key is at or
// after key. Of the data blocks, the index partitions or the filter
// partitions of a table, it is the place that a lookup of key at its highest
// sequence reads.
func placeOf(places []place, key []byte) uint64 {
	i := slices.IndexFunc(places, func(p place) bool { return bytes.Compare(p.last, key) >= 0 })

	return uint64(places[i].off)
}

// openChanged writes a golden table under a drawn setting and sets up to
// four drawn bytes to drawn values. It cuts the table to a drawn length, then
// opens and reads it with the extraction of the setting. Open and every read
// return ErrCorrupt or succeed.
func openChanged(c *prop.Case) {
	es := goldenTable()
	setting := c.Draw(prop.SampledFrom(goldenFiles...), "setting")
	data := writeTable(c, setting.opts, es, setting.dels...)
	for range c.Draw(prop.Integer(0, 4), "changes") {
		data[c.Draw(prop.Integer(0, len(data)-1), "index")] = c.Draw(prop.Integer[byte](0, 255), "byte")
	}
	data = data[:c.Draw(prop.Integer(0, len(data)), "length")]

	opts := sstable.ReaderOptions{Prefix: setting.opts.Prefix}
	r, err := sstable.Open(c.Context(), bytesFile(data), int64(len(data)), opts)
	if err != nil {
		assert.ErrorIs(c, err, sstable.ErrCorrupt, "Open refuses a changed table with ErrCorrupt")

		return
	}
	if err := readAll(c.Context(), r, es, setting.opts.Prefix); err != nil {
		assert.ErrorIs(c, err, sstable.ErrCorrupt, "a read refuses a changed block with ErrCorrupt")
	}
}
