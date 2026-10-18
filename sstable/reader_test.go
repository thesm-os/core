// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sstable_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
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

	// readGet is the name of the read of the machine that calls Get. The
	// reads that seek have the names of their moves.
	readGet = "Get"

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

	t.Run("Open", func(t *testing.T) {
		t.Parallel()

		for _, tt := range goldenFiles {
			t.Run("returns ErrCorrupt for every truncation of the golden table for "+tt.name, func(t *testing.T) {
				t.Parallel()
				data := writeTable(t, tt.opts, goldenTable())
				for n := range len(data) {
					_, err := sstable.Open(t.Context(), bytesFile(data[:n]), int64(n), sstable.ReaderOptions{})
					expect.ErrorIs(t, err, sstable.ErrCorrupt, "Open refuses the first "+strconv.Itoa(n)+" bytes")
				}
			})

			t.Run("returns ErrCorrupt for every flipped bit of the golden table for "+tt.name, func(t *testing.T) {
				t.Parallel()
				es := goldenTable()
				data := writeTable(t, tt.opts, es)
				changed := make([]byte, len(data))
				for bit := range 8 * len(data) {
					copy(changed, data)
					changed[bit/8] ^= 1 << (bit % 8)
					r, err := sstable.Open(t.Context(), bytesFile(changed), int64(len(changed)),
						sstable.ReaderOptions{})
					if err == nil {
						err = readAll(t.Context(), r, es)
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
				name:     "returns ErrCorrupt for a footer of another version",
				giveOpts: settings[1].opts,
				giveChange: func(l *layout) {
					binary.BigEndian.PutUint32(l.data[len(l.data)-footerLen+footerVersion:], 2)
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
			data := writeTable(t, settings[1].opts, goldenTable())
			counter := &countingFile{data: data}
			_, err := sstable.Open(t.Context(), counter, int64(len(data)), sstable.ReaderOptions{})
			assert.NoError(t, err, "Open reads the table")
			for ok := range counter.reads {
				_, err := sstable.Open(t.Context(), &failingFile{data: data, ok: ok}, int64(len(data)),
					sstable.ReaderOptions{})
				expect.ErrorIs(t, err, errRead, "Open returns the error of its read "+strconv.Itoa(ok+1))
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
			var buf bufferSink
			w, err := sstable.NewWriter(&buf, settings[3].opts)
			assert.NoError(t, err, "NewWriter accepts the options")
			for _, e := range goldenTable() {
				assert.NoError(t, w.Add(t.Context(), e.key, e.seq, e.value), "Add adds the entry")
			}
			want, err := w.Close(t.Context())
			assert.NoError(t, err, "Close writes the table")
			assert.Equal(t, openTable(t, buf.Bytes(), sstable.ReaderOptions{}).Summary(), want,
				"the Reader returns the summary of the Writer")
		})
	})

	t.Run("Footprint", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the capacity of every buffer in bytes", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Footprint returns the bytes of the index block, the filter block and the summary keys",
				func(c *prop.Case) {
					opts := c.Draw(prop.SampledFrom(settings...), "setting").opts
					l := newLayout(c, opts, c.Draw(entries, "entries"))
					r := openTable(c, l.data, sstable.ReaderOptions{})
					s := r.Summary()
					assert.Equal(c, r.Footprint(), l.index.n+l.filter.n+cap(s.SmallestKey)+cap(s.LargestKey),
						"Footprint adds the stored lengths of the two blocks to the capacity of the two keys")
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
				opts := c.Draw(prop.SampledFrom(oneEntry, settings[1].opts, settings[3].opts), "options")
				l := newLayout(c, opts, c.Draw(tables, "entries"))
				var lower, upper []byte
				if p := c.Draw(bound, "lower"); p != nil {
					lower = *p
				}
				if p := c.Draw(bound, "upper"); p != nil {
					upper = *p
				}
				// The first block is the first whose last key is at or after
				// lower. The last block is the first whose last key is at or
				// after upper, or the last block of the table.
				first := slices.IndexFunc(l.blocks, func(b place) bool { return bytes.Compare(b.last, lower) >= 0 })
				last := slices.IndexFunc(l.blocks, func(b place) bool {
					return upper != nil && bytes.Compare(b.last, upper) >= 0
				})
				if last < 0 {
					last = len(l.blocks) - 1
				}
				var want sstable.Span
				if first >= 0 && (upper == nil || bytes.Compare(lower, upper) < 0) {
					end := l.blocks[last].off + l.blocks[last].n + trailerLen
					want = sstable.Span{Blocks: last - first + 1, Bytes: int64(end - l.blocks[first].off)}
				}
				got, err := openTable(c, l.data, sstable.ReaderOptions{}).Span(lower, upper)
				assert.NoError(c, err, "Span searches a valid index")
				assert.Equal(c, got, want, "Span counts the blocks from the block of lower to the block of upper")
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
				r := openTable(t, writeTable(t, settings[1].opts, goldenTable()), sstable.ReaderOptions{})
				got, err := r.Span(tt.giveLower, tt.giveUpper)
				assert.NoError(t, err, "Span searches a valid index")
				assert.Equal(t, got, sstable.Span{}, "an empty range has no blocks")
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
				got, err := openTable(t, l.data, sstable.ReaderOptions{}).Span(tt.giveLower, tt.giveUpper)
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
				_, err := openTable(t, l.data, sstable.ReaderOptions{}).Span(tt.giveLower, tt.giveUpper)
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
				_, err := openTable(t, l.data, sstable.ReaderOptions{}).Span(nil, []byte("b"))
				assert.ErrorIs(t, err, sstable.ErrCorrupt, "Span refuses the place of the block of b")
			})
		}
	})

	t.Run("MayContain", func(t *testing.T) {
		t.Parallel()

		t.Run("reports true for every key of a table with a filter", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "MayContain reports true for the key of every entry", func(c *prop.Case) {
				es := c.Draw(entries, "entries")
				r := openTable(c, writeTable(c, settings[1].opts, es), sstable.ReaderOptions{})
				for _, e := range es {
					assert.True(c, r.MayContain(e.key), "MayContain reports true for a key of the table")
				}
			})
		})

		t.Run("reports true for a key that a table without a filter does not contain", func(t *testing.T) {
			t.Parallel()
			r := openTable(t, writeTable(t, settings[0].opts, goldenTable()), sstable.ReaderOptions{})
			assert.True(t, r.MayContain([]byte("absent")), "a table without a filter may contain every key")
		})

		t.Run("reports false for most keys that a table with a filter does not contain", func(t *testing.T) {
			t.Parallel()
			data := writeTable(t, sstable.WriterOptions{FilterBits: 16}, goldenTable())
			r := openTable(t, data, sstable.ReaderOptions{})
			admitted := 0
			for i := range absentKeys {
				if r.MayContain([]byte("absent " + strconv.Itoa(i))) {
					admitted++
				}
			}
			assert.InRange(t, admitted, 0, falsePositives, "a filter of 16 bits admits about 1 key in 65,536")
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

		t.Run("returns dst for a key that the filter refuses", func(t *testing.T) {
			t.Parallel()
			r := openTable(t, writeTable(t, sstable.WriterOptions{FilterBits: 16}, goldenTable()),
				sstable.ReaderOptions{})
			assert.False(t, r.MayContain([]byte(absentKey)), "the filter refuses the key")
			dst := []byte("prefix")
			got, _, ok, err := r.Get(t.Context(), []byte(absentKey), 1, dst)
			assert.NoError(t, err, "Get reads no block")
			expect.False(t, ok, "Get finds no entry")
			expect.Equal(t, got, dst, "Get returns dst unchanged", assert.ByIdentity())
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
			assert.False(t, r.MayContain([]byte(absentKey)), "the filter refuses the key")
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
	dst := make([]byte, 0, 256)

	t.Run("Footprint", func(t *testing.T) {
		r := openTable(t, data, sstable.ReaderOptions{})
		n := 0
		expect.MaxAllocs(t, func() { n = r.Footprint() }, 0, "Footprint does not allocate")
		assert.NotEqual(t, n, 0, "the test measures the footprint of a table")
	})

	t.Run("Span", func(t *testing.T) {
		r := openTable(t, data, sstable.ReaderOptions{})
		lower, upper := []byte("key 010"), []byte("key 050")
		var got sstable.Span
		var err error
		expect.MaxAllocs(t, func() { got, err = r.Span(lower, upper) }, 0, "Span does not allocate")
		expect.NoError(t, err, "the test measures a Span of a valid index")
		expect.NotEqual(t, got.Blocks, 0, "the test measures the blocks of a range")
	})

	t.Run("Get", func(t *testing.T) {
		for _, tt := range []struct {
			name  string
			cache sstable.BlockCache
		}{
			{name: "without a cache"},
			{name: "from a cache", cache: &mapCache{}},
			{name: "from a lending cache", cache: lendingCache(t, 4)},
		} {
			t.Run(tt.name, func(t *testing.T) {
				r := openTable(t, data, sstable.ReaderOptions{Cache: tt.cache, CacheID: 1})
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
		r := openTable(t, data, sstable.ReaderOptions{})
		var ok bool
		expect.MaxAllocs(t, func() { ok = r.MayContain([]byte("key 030")) }, 0, "MayContain does not allocate")
		expect.True(t, ok, "the test measures a key of the table")
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
	data := l.data
	dst := make([]byte, 0, 256)

	b.Run("Get", func(b *testing.B) {
		for _, tt := range []struct {
			name  string
			cache sstable.BlockCache
		}{
			{name: "without a cache"},
			{name: "from a cache", cache: &mapCache{}},
			// The lending cache has room for every block of the table.
			{name: "from a lending cache", cache: lendingCache(b, len(l.blocks))},
		} {
			b.Run(tt.name, func(b *testing.B) {
				r := openTable(b, data, sstable.ReaderOptions{Cache: tt.cache, CacheID: 1})
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
		r := openTable(b, data, sstable.ReaderOptions{})
		var ok bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			ok = r.MayContain([]byte("key 030"))
		}

		assert.True(b, ok, "the benchmark measures a key of the table")
	})

	b.Run("Span", func(b *testing.B) {
		// A table of spanEntries values of 100 bytes in blocks of 4 KiB has
		// about 35 entries in each of its 2,703 blocks.
		value := bytes.Repeat([]byte{'v'}, 100)
		es := make([]entry, 0, spanEntries)
		for i := range spanEntries {
			es = append(es, entry{key: fmt.Appendf(nil, spanKey, i), seq: 1, value: value})
		}
		r := openTable(b, writeTable(b, sstable.WriterOptions{BlockSize: 4096, FilterBits: 8}, es),
			sstable.ReaderOptions{})

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
		for _, tt := range tests {
			b.Run(tt.name, func(b *testing.B) {
				var got sstable.Span
				var err error

				c := bench.Start(b).MaxAllocs(0)
				defer c.End()

				for c.Loop() {
					got, err = r.Span(tt.giveLower, tt.giveUpper)
				}

				assert.NoError(b, err, "the benchmark measures a Span of a valid index")
				assert.Equal(b, got.Blocks, tt.wantBlocks, "the benchmark measures the blocks of the range")
			})
		}
	})
}

// readSteps writes drawn entries into a table under a drawn setting, and
// opens the table with cache. Clients read the table through the one Reader
// at once: Get, and the entries after a SeekGE or before a SeekLT of a
// cursor. Each read returns the entries that it finds as text, and an error
// returns its own text. The spec computes the text of each read from the
// entries, so the history of the reads is linearizable when every read
// returns what the entries return.
func readSteps(c *prop.Case, cache sstable.BlockCache) {
	es := c.Draw(entries, "entries")
	opts := c.Draw(prop.SampledFrom(settings...), "setting").opts
	r := openTable(c, writeTable(c, opts, es), sstable.ReaderOptions{Cache: cache, CacheID: 1})

	read := func(name string) stateful.Action[struct{}] {
		return stateful.Action[struct{}]{
			Name: name,
			Input: func(c *prop.Case, _ struct{}) any {
				return entry{
					key: c.Draw(prop.SampledFrom(keys...), "key"),
					seq: c.Draw(prop.Integer[uint64](0, 6), "seq"),
				}
			},
			Run: func(c *prop.Case, client int, in any) {
				p := in.(entry)
				call := c.History().Invoke(client, name, []any{p.key, p.seq})
				var text []byte
				if name == readGet {
					value, seq, ok, err := r.Get(c.Context(), p.key, p.seq, nil)
					if err != nil {
						text = fmt.Append(text, err)
					} else if ok {
						text = fmt.Appendf(text, "%q %d %q;", p.key, seq, value)
					}
					call.OK(string(text))

					return
				}
				it := r.NewIter(sstable.IterOptions{})
				ok := name == moveSeekGE && it.SeekGE(c.Context(), p.key, p.seq) ||
					name == moveSeekLT && it.SeekLT(c.Context(), p.key, p.seq)
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

	stateful.Steps(c, stateful.Machine[struct{}]{
		Spec: history.Spec[struct{}]{
			Initial: func() struct{} { return struct{}{} },
			Next: func(state struct{}, op history.Operation) []struct{} {
				p := entry{key: op.Args[0].([]byte), seq: op.Args[1].(uint64)}
				i, _ := slices.BinarySearchFunc(es, p, func(a, b entry) int {
					return sstable.Compare(a.key, a.seq, b.key, b.seq)
				})
				var want []byte
				switch op.Name {
				case readGet:
					if i < len(es) && bytes.Equal(es[i].key, p.key) {
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
				if op.Returned(string(want)) {
					return []struct{}{state}
				}

				return nil
			},
		},
		Actions: []stateful.Action[struct{}]{read(readGet), read(moveSeekGE), read(moveSeekLT)},
	}, stateful.Clients(readers))
}

// openChanged writes a golden table under a drawn setting and sets up to
// four drawn bytes to drawn values. It cuts the table to a drawn length, then
// opens and reads it. Open and every read return ErrCorrupt or succeed.
func openChanged(c *prop.Case) {
	es := goldenTable()
	data := writeTable(c, c.Draw(prop.SampledFrom(goldenFiles...), "setting").opts, es)
	for range c.Draw(prop.Integer(0, 4), "changes") {
		data[c.Draw(prop.Integer(0, len(data)-1), "index")] = c.Draw(prop.Integer[byte](0, 255), "byte")
	}
	data = data[:c.Draw(prop.Integer(0, len(data)), "length")]

	r, err := sstable.Open(c.Context(), bytesFile(data), int64(len(data)), sstable.ReaderOptions{})
	if err != nil {
		assert.ErrorIs(c, err, sstable.ErrCorrupt, "Open refuses a changed table with ErrCorrupt")

		return
	}
	if err := readAll(c.Context(), r, es); err != nil {
		assert.ErrorIs(c, err, sstable.ErrCorrupt, "a read refuses a changed block with ErrCorrupt")
	}
}
