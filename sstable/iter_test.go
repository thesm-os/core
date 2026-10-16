// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sstable_test

import (
	"bytes"
	"encoding/binary"
	"math"
	"slices"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/sstable"
)

// In a table of oneEntry, each block has one entry, whose header has three
// varints of one byte. These are their offsets in the block.
const (
	sharedAt   = 0
	unsharedAt = 1
	valueLenAt = 2
)

// everyEntry are the options of the tables whose data blocks a case changes:
// small blocks with a restart point at every entry, so the restart offsets
// of a block are the offsets of all its entries. The data blocks end at the
// filter block.
var everyEntry = sstable.WriterOptions{BlockSize: smallBlocks, RestartInterval: 1, FilterBits: 8}

// oneEntry are the options of the tables whose data blocks a case changes at
// their only entry: every entry ends its data block.
var oneEntry = sstable.WriterOptions{BlockSize: 1}

func TestIter(t *testing.T) {
	t.Parallel()

	t.Run("First", func(t *testing.T) {
		t.Parallel()

		t.Run("moves to the first entry", func(t *testing.T) {
			t.Parallel()
			it := openTable(t, writeTable(t, settings[2].opts, goldenTable()), sstable.ReaderOptions{}).NewIter()
			defer it.Close()
			assert.True(t, it.First(), "First finds an entry")
			expect.Equal(t, it.Key(), []byte("key 000"), "First moves to the smallest key")
			expect.Equal(t, it.Seq(), uint64(2), "First moves to the newest version of the key")
		})

		t.Run("reports false for a table of no entries", func(t *testing.T) {
			t.Parallel()
			it := openTable(t, writeTable(t, settings[0].opts, nil), sstable.ReaderOptions{}).NewIter()
			defer it.Close()
			assert.False(t, it.First(), "First finds no entry")
		})

		t.Run("clears the error that exhausted the cursor", func(t *testing.T) {
			t.Parallel()
			it := exhaustedIter(t)
			defer it.Close()
			assert.True(t, it.First(), "First finds the first entry")
			assert.NoError(t, it.Err(), "First clears the error")
		})
	})

	t.Run("SeekGE", func(t *testing.T) {
		t.Parallel()

		for _, tt := range settings {
			t.Run("moves to the first entry not before the probe in a table "+tt.name, func(t *testing.T) {
				t.Parallel()
				prop.ForAll(t, "SeekGE and the moves after it yield the entries of the model from the probe",
					func(c *prop.Case) {
						es := c.Draw(entries, "entries")
						it := openTable(c, writeTable(c, tt.opts, es), sstable.ReaderOptions{}).NewIter()
						defer it.Close()
						for range 10 {
							probe := entry{
								key: c.Draw(prop.SampledFrom(keys...), "key"),
								seq: c.Draw(prop.Integer[uint64](0, 6), "seq"),
							}
							i, _ := slices.BinarySearchFunc(es, probe, func(a, b entry) int {
								return sstable.Compare(a.key, a.seq, b.key, b.seq)
							})
							var got []entry
							for ok := it.SeekGE(probe.key, probe.seq); ok && len(got) < 3; ok = it.Next() {
								got = append(got, entry{
									key: bytes.Clone(it.Key()), seq: it.Seq(), value: bytes.Clone(it.Value()),
								})
							}
							assert.NoError(c, it.Err(), "the cursor reads the table")
							want := es[i:min(i+3, len(es))]
							assert.Equal(c, got, want, "the cursor yields the entries from the probe on",
								assert.EquateEmpty())
						}
					})
			})
		}

		t.Run("reports false past the last entry", func(t *testing.T) {
			t.Parallel()
			it := openTable(t, writeTable(t, settings[0].opts, goldenTable()), sstable.ReaderOptions{}).NewIter()
			defer it.Close()
			assert.False(t, it.SeekGE([]byte("zz"), 0), "SeekGE finds no entry")
			assert.Nil(t, it.Key(), "the cursor has no key")
		})

		t.Run("clears the error that exhausted the cursor", func(t *testing.T) {
			t.Parallel()
			it := exhaustedIter(t)
			defer it.Close()
			assert.True(t, it.SeekGE(goldenKeys[0], 2), "SeekGE finds the first entry")
			assert.NoError(t, it.Err(), "SeekGE clears the error")
		})

		// The second block of a table of everyEntry has four restart points.
		// The search over them decodes the restart point in the middle first,
		// and then the one that the order of the probe chooses.
		tests := []struct {
			name        string
			giveRestart int // the restart point of the second block whose offset the case breaks
			giveEntry   int // the entry of the second block that the probe is
		}{
			{name: "returns ErrCorrupt for the restart offset of the first entry", giveRestart: 0, giveEntry: 0},
			{name: "returns ErrCorrupt for a restart offset after the middle", giveRestart: 3, giveEntry: 3},
			{name: "returns ErrCorrupt for a restart offset before the middle", giveRestart: 1, giveEntry: 2},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				l := newLayout(t, everyEntry, goldenTable())
				b := l.blocks[1]
				block := l.data[b.off : b.off+b.n]
				offsets := restarts(block)
				assert.Length(t, offsets, 4, "the second block has four restart points")
				ikey, _ := restartEntry(block[offsets[tt.giveEntry]:])
				n := len(ikey) - seqLen
				key, seq := bytes.Clone(ikey[:n]), binary.BigEndian.Uint64(ikey[n:])
				binary.BigEndian.PutUint32(block[len(block)-4-4*len(offsets)+4*tt.giveRestart:], math.MaxUint32)
				l.seal()
				it := openTable(t, l.data, sstable.ReaderOptions{}).NewIter()
				defer it.Close()
				expect.False(t, it.SeekGE(key, seq), "SeekGE finds no entry")
				expect.ErrorIs(t, it.Err(), sstable.ErrCorrupt, "SeekGE refuses the restart offset that it decodes")
				expect.Nil(t, it.Key(), "the exhausted cursor has no key")
			})
		}
	})

	t.Run("Next", func(t *testing.T) {
		t.Parallel()

		for _, tt := range settings {
			t.Run("yields every entry of a table "+tt.name+" in order", func(t *testing.T) {
				t.Parallel()
				prop.ForAll(t, "a cursor yields the entries that the Writer added", func(c *prop.Case) {
					es := c.Draw(entries, "entries")
					it := openTable(c, writeTable(c, tt.opts, es), sstable.ReaderOptions{}).NewIter()
					defer it.Close()
					got, err := scanAll(it)
					assert.NoError(c, err, "the cursor reads the table")
					assert.Equal(c, got, es, "the cursor yields every entry in order", assert.EquateEmpty())
				})
			})
		}

		t.Run("reports false after the last entry", func(t *testing.T) {
			t.Parallel()
			it := openTable(t, writeTable(t, settings[0].opts, goldenTable()[:1]), sstable.ReaderOptions{}).NewIter()
			defer it.Close()
			assert.True(t, it.First(), "First finds the entry")
			expect.False(t, it.Next(), "Next finds no entry after the last")
			expect.False(t, it.Next(), "Next of an exhausted cursor finds no entry")
			expect.Nil(t, it.Key(), "the exhausted cursor has no key")
			expect.Equal(t, it.Seq(), uint64(0), "the exhausted cursor has no sequence")
			expect.Nil(t, it.Value(), "the exhausted cursor has no value")
		})

		t.Run("yields every entry of a table from the cache", func(t *testing.T) {
			t.Parallel()
			es := goldenTable()
			data := writeTable(t, settings[0].opts, es)
			counter := &countingReader{data: data}
			r, err := sstable.Open(counter, int64(len(data)), sstable.ReaderOptions{Cache: &mapCache{}, CacheID: 1})
			assert.NoError(t, err, "Open reads the table")
			it := r.NewIter()
			defer it.Close()
			_, err = scanAll(it)
			assert.NoError(t, err, "the first scan adds every block to the cache")
			reads := counter.reads
			got, err := scanAll(it)
			assert.NoError(t, err, "the second scan takes every block from the cache")
			expect.Equal(t, got, es, "the second scan yields every entry")
			expect.Equal(t, counter.reads, reads, "the second scan reads no block")
		})

		tests := []struct {
			name       string
			giveChange func(l *layout)
		}{
			{
				name:       "adds no block that fails its checksum to the cache",
				giveChange: func(l *layout) { l.data[l.blocks[1].off] ^= 1 },
			},
			{
				name: "adds no block of an unknown codec to the cache",
				giveChange: func(l *layout) {
					l.data[l.blocks[1].off+l.blocks[1].n] = 2
					l.seal()
				},
			},
			{
				name: "adds no block that does not decode to the cache",
				giveChange: func(l *layout) {
					l.data[l.blocks[1].off+l.blocks[1].n] = byte(sstable.SnappyCompression)
					l.seal()
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				l := newLayout(t, oneEntry, goldenTable())
				tt.giveChange(l)
				cache := &mapCache{}
				it := openTable(t, l.data, sstable.ReaderOptions{Cache: cache, CacheID: 1}).NewIter()
				defer it.Close()
				_, err := scanAll(it)
				assert.ErrorIs(t, err, sstable.ErrCorrupt, "the cursor refuses the second block")
				_, ok := cache.Get(1, uint64(l.blocks[1].off))
				expect.False(t, ok, "the cache has no block at the place of the second block")
				expect.Equal(t, cache.adds, 1, "the cursor adds the first block alone")
			})
		}
	})

	t.Run("Err", func(t *testing.T) {
		t.Parallel()

		t.Run("returns ErrCorrupt for a block that fails its checksum", func(t *testing.T) {
			t.Parallel()
			data := writeTable(t, settings[0].opts, goldenTable())
			data[len(data)/3] ^= 1
			it := openTable(t, data, sstable.ReaderOptions{}).NewIter()
			defer it.Close()
			_, err := scanAll(it)
			expect.ErrorIs(t, err, sstable.ErrCorrupt, "the cursor refuses the changed block")
			expect.False(t, it.Next(), "the cursor is exhausted after the error")
		})

		// A case changes the second data block or its entry in the index. A
		// cached case reads the changed table through a cache with every
		// block of the table before the change. A cursor that took a block of
		// a corrupt place from that cache would find the block.
		tests := []struct {
			name       string
			giveOpts   sstable.WriterOptions
			giveCached bool
			giveChange func(l *layout, block []byte)
		}{
			{
				name:     "returns ErrCorrupt for a restart count one above the room of the block",
				giveOpts: oneEntry,
				giveChange: func(_ *layout, block []byte) {
					binary.BigEndian.PutUint32(block[len(block)-4:], uint32((len(block)-4)/4+1))
				},
			},
			{
				name:     "returns ErrCorrupt for a restart offset outside the entries",
				giveOpts: oneEntry,
				giveChange: func(_ *layout, block []byte) {
					binary.BigEndian.PutUint32(block[len(block)-4-4*len(restarts(block)):], math.MaxUint32)
				},
			},
			{
				name:     "returns ErrCorrupt for a block shorter than its restart count",
				giveOpts: oneEntry,
				giveChange: func(l *layout, _ []byte) {
					replaceBlock(l, 1, []byte{0, 0})
				},
			},
			{
				name:     "returns ErrCorrupt for a varint that does not end",
				giveOpts: oneEntry,
				giveChange: func(_ *layout, block []byte) {
					for i := range binary.MaxVarintLen64 + 1 {
						block[sharedAt+i] = 0x80
					}
				},
			},
			{
				name:     "returns ErrCorrupt for a value length whose varint does not end",
				giveOpts: oneEntry,
				giveChange: func(_ *layout, block []byte) {
					for i := range binary.MaxVarintLen64 + 1 {
						block[valueLenAt+i] = 0x80
					}
				},
			},
			{
				name:     "returns ErrCorrupt for an entry that ends inside its value length",
				giveOpts: oneEntry,
				giveChange: func(l *layout, _ []byte) {
					// The entry shares no byte and has seqLen more bytes of its
					// internal key. Its value length is a varint of seqLen
					// bytes that the end of the entries cuts off. The restart
					// offset 0 and the count 1 follow the entry.
					block := slices.Concat([]byte{0, seqLen}, bytes.Repeat([]byte{0x80}, seqLen), []byte{0, 0, 0, 0},
						[]byte{0, 0, 0, 1})
					replaceBlock(l, 1, block)
				},
			},
			{
				name:       "returns ErrCorrupt for a shared prefix longer than the key before it",
				giveOpts:   oneEntry,
				giveChange: func(_ *layout, block []byte) { block[sharedAt] = 1 },
			},
			{
				name:       "returns ErrCorrupt for a key past the end of the entries",
				giveOpts:   oneEntry,
				giveChange: func(_ *layout, block []byte) { block[unsharedAt] = 0x7f },
			},
			{
				name:       "returns ErrCorrupt for a value that ends one byte past the entries",
				giveOpts:   oneEntry,
				giveChange: func(_ *layout, block []byte) { block[valueLenAt]++ },
			},
			{
				name:       "returns ErrCorrupt for an internal key shorter than a sequence",
				giveOpts:   oneEntry,
				giveChange: func(_ *layout, block []byte) { block[unsharedAt] = seqLen - 1 },
			},
			{
				name:       "returns ErrCorrupt for a block offset past the data blocks",
				giveOpts:   everyEntry,
				giveCached: true,
				giveChange: func(l *layout, _ []byte) {
					v := indexValue(l, 1)
					v[0], v[1] = 0xff, 0x7f
				},
			},
			{
				name:       "returns ErrCorrupt for a block length past the data blocks",
				giveOpts:   everyEntry,
				giveCached: true,
				giveChange: func(l *layout, _ []byte) {
					v := indexValue(l, 1)
					v[len(v)-2], v[len(v)-1] = 0xff, 0x7f
				},
			},
			{
				name:       "returns ErrCorrupt for a block that ends one byte past the data blocks",
				giveOpts:   everyEntry,
				giveCached: true,
				giveChange: func(l *layout, _ []byte) {
					setBlockLength(l, 1, l.filter.off-l.blocks[1].off-trailerLen+1)
				},
			},
			{
				name:       "returns ErrCorrupt for a block length that leaves no room for its trailer",
				giveOpts:   everyEntry,
				giveCached: true,
				giveChange: func(l *layout, _ []byte) {
					setBlockLength(l, 1, l.filter.off-trailerLen+1)
				},
			},
			{
				name:       "returns ErrCorrupt for a block offset whose varint does not end",
				giveOpts:   everyEntry,
				giveCached: true,
				giveChange: func(l *layout, _ []byte) {
					v := indexValue(l, 1)
					for i := range v {
						v[i] = 0x80
					}
				},
			},
			{
				name:       "returns ErrCorrupt for a block length whose varint does not end",
				giveOpts:   everyEntry,
				giveCached: true,
				giveChange: func(l *layout, _ []byte) {
					v := indexValue(l, 1)
					v[len(v)-1] = 0x80
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				es := goldenTable()
				l := newLayout(t, tt.giveOpts, es)
				// Every entry of the first block is a restart point, so its
				// restart points count the entries before the second block.
				first, second := l.blocks[0], l.blocks[1]
				want := es[:len(restarts(l.data[first.off:first.off+first.n]))]
				opts := sstable.ReaderOptions{}
				if tt.giveCached {
					opts = sstable.ReaderOptions{Cache: &mapCache{}, CacheID: 1}
					warm := openTable(t, l.data, opts).NewIter()
					defer warm.Close()
					_, err := scanAll(warm)
					assert.NoError(t, err, "the cursor adds every block of the table to the cache")
				}
				tt.giveChange(l, l.data[second.off:second.off+second.n])
				l.seal()
				it := openTable(t, l.data, opts).NewIter()
				defer it.Close()
				got, err := scanAll(it)
				expect.ErrorIs(t, err, sstable.ErrCorrupt, "the cursor refuses the second block")
				expect.Equal(t, got, want, "the cursor yields the entries of the first block")
			})
		}

		t.Run("returns the error of the io.ReaderAt", func(t *testing.T) {
			t.Parallel()
			data := writeTable(t, settings[0].opts, goldenTable())
			counter := &countingReader{data: data}
			_, err := sstable.Open(counter, int64(len(data)), sstable.ReaderOptions{})
			assert.NoError(t, err, "Open reads the table")
			r, err := sstable.Open(&failingReader{data: data, ok: counter.reads + 1}, int64(len(data)),
				sstable.ReaderOptions{})
			assert.NoError(t, err, "Open reads the table with the reads that it needs")
			it := r.NewIter()
			defer it.Close()
			_, err = scanAll(it)
			assert.ErrorIs(t, err, errRead, "the cursor returns the error of the read of its second block")
		})

		t.Run("returns nil after every entry", func(t *testing.T) {
			t.Parallel()
			it := openTable(t, writeTable(t, settings[3].opts, goldenTable()), sstable.ReaderOptions{}).NewIter()
			defer it.Close()
			got, err := scanAll(it)
			expect.NoError(t, err, "the cursor reads the table")
			expect.Length(t, got, goldenEntries, "the cursor yields every entry")
		})
	})

	t.Run("Close", func(t *testing.T) {
		t.Parallel()

		t.Run("returns nil for a cursor without an error", func(t *testing.T) {
			t.Parallel()
			it := openTable(t, writeTable(t, settings[0].opts, goldenTable()), sstable.ReaderOptions{}).NewIter()
			assert.True(t, it.First(), "First finds an entry")
			assert.NoError(t, it.Close(), "Close returns the cursor to the pool")
		})

		t.Run("returns the error that exhausted the cursor", func(t *testing.T) {
			t.Parallel()
			data := writeTable(t, settings[0].opts, goldenTable())
			data[len(data)/3] ^= 1
			it := openTable(t, data, sstable.ReaderOptions{}).NewIter()
			_, err := scanAll(it)
			assert.ErrorIs(t, err, sstable.ErrCorrupt, "the cursor refuses the changed block")
			assert.ErrorIs(t, it.Close(), sstable.ErrCorrupt, "Close returns the error of the cursor")
		})
	})
}

// TestIterAllocs checks the allocation contract of the moves of a cursor.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
func TestIterAllocs(t *testing.T) {
	r := openTable(t, writeTable(t, settings[3].opts, goldenTable()), sstable.ReaderOptions{})
	it := r.NewIter()
	defer it.Close()

	var ok bool
	expect.MaxAllocs(t, func() { ok = it.First() }, 0, "First does not allocate")
	expect.True(t, ok, "the test measures a cursor on an entry")
	expect.MaxAllocs(t, func() { ok = it.SeekGE(goldenKeys[30], 1) }, 0, "SeekGE does not allocate")
	expect.True(t, ok, "the test measures a seek to an entry")
	expect.MaxAllocs(t, func() {
		if !it.Next() {
			it.First()
		}
	}, 0, "Next does not allocate within a block or across blocks")

	var key, value []byte
	var err error
	expect.MaxAllocs(t, func() {
		key, value, err = it.Key(), it.Value(), it.Err()
	}, 0, "Key, Value and Err do not allocate")
	expect.NotEmpty(t, key, "the test measures the key of an entry")
	expect.NotEmpty(t, value, "the test measures the value of an entry")
	expect.NoError(t, err, "the test measures a cursor without an error")
}

// BenchmarkIter reports the cost of the moves of a cursor, and fails when a
// move allocates.
func BenchmarkIter(b *testing.B) {
	r := openTable(b, writeTable(b, settings[3].opts, goldenTable()), sstable.ReaderOptions{})

	b.Run("Next", func(b *testing.B) {
		it := r.NewIter()
		defer it.Close()
		assert.True(b, it.First(), "First finds an entry")
		n := 0

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			if !it.Next() {
				it.First()
			}
			n += len(it.Key())
		}

		assert.NotEqual(b, n, 0, "the benchmark measures moves to entries")
	})

	b.Run("SeekGE", func(b *testing.B) {
		it := r.NewIter()
		defer it.Close()
		var ok bool
		i := 0

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			ok = it.SeekGE(goldenKeys[i%len(goldenKeys)], 1)
			i++
		}

		assert.True(b, ok, "the benchmark measures seeks to entries")
	})
}

// exhaustedIter returns a cursor that ErrCorrupt exhausted at the second
// block of a table of oneEntry. The cursor reads the first block of the
// table without an error.
func exhaustedIter(tb assert.TB) *sstable.Iter {
	tb.Helper()

	l := newLayout(tb, oneEntry, goldenTable())
	l.data[l.blocks[1].off] ^= 1
	it := openTable(tb, l.data, sstable.ReaderOptions{}).NewIter()
	_, err := scanAll(it)
	assert.ErrorIs(tb, err, sstable.ErrCorrupt, "the cursor refuses the second block")

	return it
}

// indexValue returns the value of the entry of the index for the data block
// i of l: the offset and the length of the block as varints. The value is a
// part of the bytes of l, so a case changes the table through it.
func indexValue(l *layout, i int) []byte {
	index := l.data[l.index.off : l.index.off+l.index.n]
	_, value := restartEntry(index[restarts(index)[i]:])

	return value
}

// setBlockLength writes n as the length of the data block i into the index
// of l. The varint of n has the width of the varint that it replaces, so the
// index keeps its layout.
func setBlockLength(l *layout, i, n int) {
	v := indexValue(l, i)
	_, width := binary.Uvarint(v)
	binary.PutUvarint(v[width:], uint64(n))
}

// replaceBlock replaces the data block i of l with the block b without
// compression. l.seal fills the checksum of its trailer. b is not longer
// than the block, and the varint of its length has the width of the varint
// of the length of the block.
func replaceBlock(l *layout, i int, b []byte) {
	off := l.blocks[i].off
	copy(l.data[off:], b)
	l.data[off+len(b)] = byte(sstable.NoCompression)
	setBlockLength(l, i, len(b))
	l.blocks[i].n = len(b)
}
