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
	"go.dokimi.dev/assert/stateful"

	"go.thesmos.sh/core/sstable"
)

// In a table of oneEntry, each block has one entry, whose header has three
// varints of one byte. These are their offsets in the block.
const (
	sharedAt   = 0
	unsharedAt = 1
	valueLenAt = 2
)

// iterContract is the contract of iterSteps, which TestIter and FuzzIter
// check.
const iterContract = "every move must put the cursor on the entry of a sorted model of the table"

// everyEntry are the options of the tables whose data blocks a case changes:
// small blocks with a restart point at every entry, so the restart offsets
// of a block are the offsets of all its entries. The data blocks end at the
// filter block.
var everyEntry = sstable.WriterOptions{BlockSize: smallBlocks, RestartInterval: 1, FilterBits: 8}

func TestIter(t *testing.T) {
	t.Parallel()

	t.Run("returns what a sorted model returns under any sequence of moves", func(t *testing.T) {
		t.Parallel()
		prop.ForAll(t, iterContract, iterSteps)
	})

	// The second block of a table of everyEntry has four restart points. The
	// search of a seek over them decodes the restart point in the middle
	// first, and then the one that the order of the probe chooses.
	brokenRestarts := []struct {
		name        string
		giveRestart int // the restart point of the second block whose offset the case breaks
		giveEntry   int // the entry of the second block that the probe is
	}{
		{name: "returns ErrCorrupt for the restart offset of the first entry", giveRestart: 0, giveEntry: 0},
		{name: "returns ErrCorrupt for a restart offset after the middle", giveRestart: 3, giveEntry: 3},
		{name: "returns ErrCorrupt for a restart offset before the middle", giveRestart: 1, giveEntry: 2},
	}

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

	t.Run("Last", func(t *testing.T) {
		t.Parallel()

		t.Run("moves to the last entry", func(t *testing.T) {
			t.Parallel()
			it := openTable(t, writeTable(t, settings[2].opts, goldenTable()), sstable.ReaderOptions{}).NewIter()
			defer it.Close()
			assert.True(t, it.Last(), "Last finds an entry")
			expect.Equal(t, it.Key(), goldenKeys[len(goldenKeys)-1], "Last moves to the largest key")
			expect.Equal(t, it.Seq(), uint64(1), "Last moves to the oldest version of the key")
		})

		t.Run("reports false for a table of no entries", func(t *testing.T) {
			t.Parallel()
			it := openTable(t, writeTable(t, settings[0].opts, nil), sstable.ReaderOptions{}).NewIter()
			defer it.Close()
			assert.False(t, it.Last(), "Last finds no entry")
		})

		t.Run("clears the error that exhausted the cursor", func(t *testing.T) {
			t.Parallel()
			it := exhaustedIter(t)
			defer it.Close()
			assert.True(t, it.Last(), "Last finds the last entry")
			assert.NoError(t, it.Err(), "Last clears the error")
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

		for _, tt := range brokenRestarts {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				it, key, seq := brokenRestart(t, tt.giveRestart, tt.giveEntry)
				defer it.Close()
				expect.False(t, it.SeekGE(key, seq), "SeekGE finds no entry")
				expect.ErrorIs(t, it.Err(), sstable.ErrCorrupt, "SeekGE refuses the restart offset that it decodes")
				expect.Nil(t, it.Key(), "the exhausted cursor has no key")
			})
		}
	})

	t.Run("SeekLT", func(t *testing.T) {
		t.Parallel()

		for _, tt := range settings {
			t.Run("moves to the last entry before the probe in a table "+tt.name, func(t *testing.T) {
				t.Parallel()
				prop.ForAll(t, "SeekLT and the moves back after it yield the entries of the model before the probe",
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
							for ok := it.SeekLT(probe.key, probe.seq); ok && len(got) < 3; ok = it.Prev() {
								got = append(got, entry{
									key: bytes.Clone(it.Key()), seq: it.Seq(), value: bytes.Clone(it.Value()),
								})
							}
							assert.NoError(c, it.Err(), "the cursor reads the table")
							want := slices.Clone(es[max(i-3, 0):i])
							slices.Reverse(want)
							assert.Equal(c, got, want, "the cursor yields the entries before the probe back",
								assert.EquateEmpty())
						}
					})
			})
		}

		t.Run("reports false before the first entry", func(t *testing.T) {
			t.Parallel()
			it := openTable(t, writeTable(t, settings[0].opts, goldenTable()), sstable.ReaderOptions{}).NewIter()
			defer it.Close()
			assert.False(t, it.SeekLT(goldenKeys[0], 2), "SeekLT finds no entry")
			assert.Nil(t, it.Key(), "the cursor has no key")
		})

		t.Run("moves to the last entry for a probe past the last entry", func(t *testing.T) {
			t.Parallel()
			it := openTable(t, writeTable(t, settings[0].opts, goldenTable()), sstable.ReaderOptions{}).NewIter()
			defer it.Close()
			assert.True(t, it.SeekLT([]byte("zz"), 0), "SeekLT finds an entry")
			expect.Equal(t, it.Key(), goldenKeys[len(goldenKeys)-1], "SeekLT moves to the largest key")
			expect.Equal(t, it.Seq(), uint64(1), "SeekLT moves to the oldest version of the key")
		})

		t.Run("clears the error that exhausted the cursor", func(t *testing.T) {
			t.Parallel()
			it := exhaustedIter(t)
			defer it.Close()
			assert.True(t, it.SeekLT([]byte("zz"), 0), "SeekLT finds the last entry")
			assert.NoError(t, it.Err(), "SeekLT clears the error")
		})

		t.Run("moves to the last entry of a block whose entries are before the probe", func(t *testing.T) {
			t.Parallel()
			// The second block has the entry of key 000 at sequence 3, which is
			// before the last entry of its index entry, key 000 at sequence 1.
			l := newLayout(t, oneEntry, goldenTable())
			block := slices.Concat([]byte{0, byte(len(goldenKeys[0]) + seqLen), 1}, goldenKeys[0],
				binary.BigEndian.AppendUint64(nil, 3), []byte("x"), []byte{0, 0, 0, 0, 0, 0, 0, 1})
			replaceBlock(l, 1, block)
			l.seal()
			it := openTable(t, l.data, sstable.ReaderOptions{}).NewIter()
			defer it.Close()
			assert.True(t, it.SeekLT(goldenKeys[0], 1), "SeekLT finds an entry")
			expect.Equal(t, it.Seq(), uint64(3), "SeekLT moves to the entry of the second block")
			expect.Equal(t, it.Value(), []byte("x"), "SeekLT moves to the value of the entry")
		})

		for _, tt := range brokenRestarts {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				it, key, seq := brokenRestart(t, tt.giveRestart, tt.giveEntry)
				defer it.Close()
				expect.False(t, it.SeekLT(key, seq), "SeekLT finds no entry")
				expect.ErrorIs(t, it.Err(), sstable.ErrCorrupt, "SeekLT refuses the restart offset that it decodes")
				expect.Nil(t, it.Key(), "the exhausted cursor has no key")
			})
		}

		t.Run("returns ErrCorrupt for a restart offset of the index that its search decodes", func(t *testing.T) {
			t.Parallel()
			// The search of the index decodes its middle restart point first.
			// Open reads the last restart point of the index alone.
			l := newLayout(t, everyEntry, goldenTable())
			index := l.data[l.index.off : l.index.off+l.index.n]
			count := len(restarts(index))
			binary.BigEndian.PutUint32(index[len(index)-4-4*count+4*(count/2):], math.MaxUint32)
			l.seal()
			it := openTable(t, l.data, sstable.ReaderOptions{}).NewIter()
			defer it.Close()
			expect.False(t, it.SeekLT(goldenKeys[30], 1), "SeekLT finds no entry")
			expect.ErrorIs(t, it.Err(), sstable.ErrCorrupt, "SeekLT refuses the restart offset of the index")
		})
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

		t.Run("reports false for a cursor that moved before the first entry", func(t *testing.T) {
			t.Parallel()
			it := openTable(t, writeTable(t, settings[0].opts, goldenTable()), sstable.ReaderOptions{}).NewIter()
			defer it.Close()
			assert.False(t, it.SeekLT(goldenKeys[0], 2), "SeekLT finds no entry before the first")
			assert.False(t, it.Next(), "Next of the exhausted cursor finds no entry")
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

	t.Run("Prev", func(t *testing.T) {
		t.Parallel()

		for _, tt := range settings {
			t.Run("yields every entry of a table "+tt.name+" from the last", func(t *testing.T) {
				t.Parallel()
				prop.ForAll(t, "a cursor yields the entries that the Writer added from the last back",
					func(c *prop.Case) {
						es := c.Draw(entries, "entries")
						it := openTable(c, writeTable(c, tt.opts, es), sstable.ReaderOptions{}).NewIter()
						defer it.Close()
						got, err := scanBack(it)
						assert.NoError(c, err, "the cursor reads the table")
						want := slices.Clone(es)
						slices.Reverse(want)
						assert.Equal(c, got, want, "the cursor yields every entry in reverse order",
							assert.EquateEmpty())
					})
			})
		}

		t.Run("reports false before the first entry", func(t *testing.T) {
			t.Parallel()
			it := openTable(t, writeTable(t, settings[0].opts, goldenTable()[:1]), sstable.ReaderOptions{}).NewIter()
			defer it.Close()
			assert.True(t, it.Last(), "Last finds the entry")
			expect.False(t, it.Prev(), "Prev finds no entry before the first")
			expect.False(t, it.Prev(), "Prev of an exhausted cursor finds no entry")
			expect.Nil(t, it.Key(), "the exhausted cursor has no key")
		})

		t.Run("reports false for a cursor that moved past the last entry", func(t *testing.T) {
			t.Parallel()
			it := openTable(t, writeTable(t, settings[0].opts, goldenTable()), sstable.ReaderOptions{}).NewIter()
			defer it.Close()
			assert.False(t, it.SeekGE([]byte("zz"), 0), "SeekGE finds no entry past the last")
			assert.False(t, it.Prev(), "Prev of the exhausted cursor finds no entry")
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
			_, err = scanBack(it)
			assert.NoError(t, err, "the first scan adds every block to the cache")
			reads := counter.reads
			got, err := scanBack(it)
			assert.NoError(t, err, "the second scan takes every block from the cache")
			want := slices.Clone(es)
			slices.Reverse(want)
			expect.Equal(t, got, want, "the second scan yields every entry")
			expect.Equal(t, counter.reads, reads, "the second scan reads no block")
		})

		t.Run("returns ErrCorrupt for entries that do not end at the current entry", func(t *testing.T) {
			t.Parallel()
			// The first block has two entries, of the keys a and b. Its second
			// restart point is at the value of the first entry, which starts
			// with the header of an entry. That entry ends 2 bytes after the
			// start of the second entry.
			l := newLayout(t, oneEntry, goldenTable())
			header := []byte{0, 1 + seqLen, 0}
			first := slices.Concat([]byte{0, 1 + seqLen, 10}, []byte("a"), make([]byte, seqLen),
				header, make([]byte, 7))
			second := slices.Concat(header, []byte("b"), make([]byte, seqLen))
			restartPoints := []byte{0, 0, 0, 0, 0, 0, 0, byte(len(first) - 10), 0, 0, 0, 2}
			replaceBlock(l, 0, slices.Concat(first, second, restartPoints))
			l.seal()
			it := openTable(t, l.data, sstable.ReaderOptions{}).NewIter()
			defer it.Close()
			assert.True(t, it.First(), "First finds the entry of the key a")
			assert.True(t, it.Next(), "Next finds the entry of the key b")
			expect.False(t, it.Prev(), "Prev finds no entry")
			expect.ErrorIs(t, it.Err(), sstable.ErrCorrupt, "Prev refuses entries that do not end at the current entry")
		})

		t.Run("returns ErrCorrupt for an entry before the current one that does not decode", func(t *testing.T) {
			t.Parallel()
			// The second block has three entries, and restart points at the
			// first and the third. The second entry shares 10 bytes with a key
			// of 9 bytes, so a move back from the third entry decodes the
			// first entry and refuses the second.
			l := newLayout(t, oneEntry, goldenTable())
			first := slices.Concat([]byte{0, 1 + seqLen, 0}, []byte("a"), make([]byte, seqLen))
			second := []byte{2 + seqLen, 1, 0, 0}
			third := slices.Concat([]byte{0, 1 + seqLen, 0}, []byte("c"), make([]byte, seqLen))
			restartPoints := []byte{0, 0, 0, 0, 0, 0, 0, byte(len(first) + len(second)), 0, 0, 0, 2}
			replaceBlock(l, 1, slices.Concat(first, second, third, restartPoints))
			l.seal()
			it := openTable(t, l.data, sstable.ReaderOptions{}).NewIter()
			defer it.Close()
			got, err := scanBack(it)
			expect.ErrorIs(t, err, sstable.ErrCorrupt, "Prev refuses the second entry of the second block")
			assert.NotEmpty(t, got, "the cursor yields the entries after the second block")
			expect.Equal(t, got[len(got)-1].key, []byte("c"), "the cursor yields the third entry before the error")
		})
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

		t.Run("returns ErrCorrupt for a data block at the place of another", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "a cursor refuses a valid data block at the place of another data block",
				func(c *prop.Case) {
					es := c.Draw(uniform, "entries")
					l := newLayout(c, oneEntry, es)
					to := c.Draw(prop.Integer(1, len(l.blocks)-1), "place")
					from := c.Draw(prop.Integer(0, len(l.blocks)-2), "block")
					if from >= to {
						from++
					}
					b := l.blocks[from]
					copy(l.data[l.blocks[to].off:], l.data[b.off:b.off+b.n+trailerLen])
					it := openTable(c, l.data, sstable.ReaderOptions{}).NewIter()
					defer it.Close()
					got, err := scanAll(it)
					assert.ErrorIs(c, err, sstable.ErrCorrupt, "the cursor refuses the block at the place")
					assert.Equal(c, got, es[:to], "the cursor yields the entries before the place")
				})
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
					v[len(v)-crcLen-2], v[len(v)-crcLen-1] = 0xff, 0x7f
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
					// The varint continues through the bytes of the CRC32C.
					v := indexValue(l, 1)
					for i := range crcLen + 1 {
						v[len(v)-1-i] = 0x80
					}
				},
			},
			{
				name:       "returns ErrCorrupt for an index value without the varint of the length",
				giveOpts:   everyEntry,
				giveCached: true,
				giveChange: func(l *layout, _ []byte) {
					// The offset of the second block has a varint of two bytes,
					// which takes four bytes with two more bytes of no value. The
					// four bytes after it end no varint.
					v := indexValue(l, 1)
					v[1] |= 0x80
					v[2], v[3] = 0x80, 0
					for i := range crcLen {
						v[4+i] = 0x80
					}
				},
			},
			{
				name:       "returns ErrCorrupt for an index value with bytes after the CRC32C",
				giveOpts:   everyEntry,
				giveCached: true,
				giveChange: func(l *layout, _ []byte) {
					// The offset of the second block has a varint of two bytes.
					// Without the high bit of its first byte, the varint ends
					// after one byte, and its second byte is the length.
					v := indexValue(l, 1)
					v[0] &^= 0x80
				},
			},
			{
				name:       "returns ErrCorrupt for an index value without room for its CRC32C",
				giveOpts:   everyEntry,
				giveCached: true,
				giveChange: func(l *layout, _ []byte) {
					// The length of the second block has a varint of two bytes.
					// With the high bit of its last byte, the varint ends at the
					// first byte of the CRC32C.
					v := indexValue(l, 1)
					v[len(v)-crcLen-1] |= 0x80
					v[len(v)-crcLen] = 0
				},
			},
			{
				name:     "returns ErrCorrupt for a CRC32C in the index that differs from the block",
				giveOpts: everyEntry,
				giveChange: func(l *layout, _ []byte) {
					l.blocks[1].parent[0] ^= 1
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				es := goldenTable()
				l := newLayout(t, tt.giveOpts, es)
				// Every entry of the first two blocks is a restart point, so
				// their restart points count the entries before the second
				// block and after it.
				first, second := l.blocks[0], l.blocks[1]
				before := len(restarts(l.data[first.off : first.off+first.n]))
				want := es[:before]
				wantBack := slices.Clone(es[before+len(restarts(l.data[second.off:second.off+second.n])):])
				slices.Reverse(wantBack)
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
				back, err := scanBack(it)
				expect.ErrorIs(t, err, sstable.ErrCorrupt, "the cursor refuses the second block from the last entry")
				expect.Equal(t, back, wantBack, "the cursor yields the entries after the second block back")
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
			expect.ErrorIs(t, err, errRead, "the cursor returns the error of the read of its second block")
			_, err = scanBack(it)
			expect.ErrorIs(t, err, errRead, "the cursor returns the error of the read of its last block")
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

		t.Run("returns nil for a second Close", func(t *testing.T) {
			t.Parallel()
			data := writeTable(t, settings[0].opts, goldenTable())
			data[len(data)/3] ^= 1
			it := openTable(t, data, sstable.ReaderOptions{}).NewIter()
			_, err := scanAll(it)
			assert.ErrorIs(t, err, sstable.ErrCorrupt, "the cursor refuses the changed block")
			assert.ErrorIs(t, it.Close(), sstable.ErrCorrupt, "the first Close returns the error of the cursor")
			assert.NoError(t, it.Close(), "the second Close finds no cursor to return")
		})

		t.Run("returns the cursor to the pool once for a second Close", func(t *testing.T) {
			t.Parallel()
			r := openTable(t, writeTable(t, settings[0].opts, goldenTable()), sstable.ReaderOptions{})
			it := r.NewIter()
			assert.NoError(t, it.Close(), "the first Close returns the cursor to the pool")
			assert.NoError(t, it.Close(), "the second Close returns nil")
			first, second := r.NewIter(), r.NewIter()
			defer first.Close()
			defer second.Close()
			assert.NotEqual(t, first, second, "the pool hands out the closed cursor once", assert.ByIdentity())
		})
	})
}

// FuzzIter checks the machine of iterSteps on the move sequences that a
// fuzzer finds.
func FuzzIter(f *testing.F) {
	prop.Fuzz(f, iterContract, iterSteps)
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
	expect.MaxAllocs(t, func() { ok = it.Last() }, 0, "Last does not allocate")
	expect.True(t, ok, "the test measures a cursor on the last entry")
	expect.MaxAllocs(t, func() { ok = it.SeekGE(goldenKeys[30], 1) }, 0, "SeekGE does not allocate")
	expect.True(t, ok, "the test measures a seek to an entry")
	expect.MaxAllocs(t, func() { ok = it.SeekLT(goldenKeys[30], 1) }, 0, "SeekLT does not allocate")
	expect.True(t, ok, "the test measures a seek back to an entry")
	expect.MaxAllocs(t, func() {
		if !it.Next() {
			it.First()
		}
	}, 0, "Next does not allocate within a block or across blocks")
	expect.MaxAllocs(t, func() {
		if !it.Prev() {
			it.Last()
		}
	}, 0, "Prev does not allocate within a block or across blocks")

	var key, value []byte
	var err error
	expect.MaxAllocs(t, func() {
		key, value, err = it.Key(), it.Value(), it.Err()
	}, 0, "Key, Value and Err do not allocate")
	expect.NotEmpty(t, key, "the test measures the key of an entry")
	expect.NotEmpty(t, value, "the test measures the value of an entry")
	expect.NoError(t, err, "the test measures a cursor without an error")

	// The second block of the table is a Snappy block whose preamble states
	// 1 MiB and whose elements decode to 1 byte.
	l := newLayout(t, oneEntry, goldenTable())
	replaceBlock(l, 1, append(binary.AppendUvarint(nil, 1<<20), 0x00, 'a'))
	l.data[l.blocks[1].off+l.blocks[1].n] = byte(sstable.SnappyCompression)
	l.seal()
	bad := openTable(t, l.data, sstable.ReaderOptions{}).NewIter()
	defer bad.Close()
	expect.MaxAllocs(t, func() {
		ok := bad.First()
		for ok {
			ok = bad.Next()
		}
	}, 0, "a cursor does not allocate the length that the preamble of a corrupt block states")
	expect.ErrorIs(t, bad.Err(), sstable.ErrCorrupt, "the test measures a cursor that refuses the second block")
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

	b.Run("Prev", func(b *testing.B) {
		it := r.NewIter()
		defer it.Close()
		assert.True(b, it.Last(), "Last finds an entry")
		n := 0

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			if !it.Prev() {
				it.Last()
			}
			n += len(it.Key())
		}

		assert.NotEqual(b, n, 0, "the benchmark measures moves to entries")
	})

	b.Run("Last", func(b *testing.B) {
		it := r.NewIter()
		defer it.Close()
		var ok bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			ok = it.Last()
		}

		assert.True(b, ok, "the benchmark measures moves to the last entry")
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

	b.Run("SeekLT", func(b *testing.B) {
		it := r.NewIter()
		defer it.Close()
		var ok bool
		i := 0

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			// The keys after the first have an entry before them.
			ok = it.SeekLT(goldenKeys[1+i%(len(goldenKeys)-1)], 1)
			i++
		}

		assert.True(b, ok, "the benchmark measures seeks back to entries")
	})
}

// iterSteps writes drawn entries into a table of a drawn layout, and moves
// one cursor of the table through a machine of its six moves. The model of
// the cursor is a place in the entries. A move off either end leaves the
// cursor on no entry, and Next and Prev report false there.
func iterSteps(c *prop.Case) {
	es := c.Draw(entries, "entries")
	opts := sstable.WriterOptions{
		BlockSize:       c.Draw(prop.Integer(1, 512), "block size"),
		RestartInterval: c.Draw(prop.Integer(1, 8), "restart interval"),
		Compression:     c.Draw(prop.SampledFrom(sstable.NoCompression, sstable.SnappyCompression), "compression"),
		FilterBits:      c.Draw(prop.SampledFrom(0, 8), "filter bits"),
	}
	var cache sstable.BlockCache
	if c.Draw(prop.Boolean(), "cache") {
		cache = &mapCache{}
	}
	it := openTable(c, writeTable(c, opts, es), sstable.ReaderOptions{Cache: cache, CacheID: 1}).NewIter()
	defer it.Close()

	at, on, ok := 0, false, false
	probe := func(c *prop.Case, _ struct{}) any {
		return entry{key: c.Draw(prop.SampledFrom(keys...), "key"), seq: c.Draw(prop.Integer[uint64](0, 6), "seq")}
	}

	stateful.Steps(c, stateful.Machine[struct{}]{
		Actions: []stateful.Action[struct{}]{
			{
				Name: moveFirst,
				Run: func(*prop.Case, int, any) {
					ok = it.First()
					at, on = 0, len(es) != 0
				},
			},
			{
				Name: moveLast,
				Run: func(*prop.Case, int, any) {
					ok = it.Last()
					at, on = len(es)-1, len(es) != 0
				},
			},
			{
				Name:  moveSeekGE,
				Input: probe,
				Run: func(_ *prop.Case, _ int, in any) {
					p := in.(entry)
					ok = it.SeekGE(p.key, p.seq)
					at, _ = slices.BinarySearchFunc(es, p, func(a, b entry) int {
						return sstable.Compare(a.key, a.seq, b.key, b.seq)
					})
					on = at < len(es)
				},
			},
			{
				Name:  moveSeekLT,
				Input: probe,
				Run: func(_ *prop.Case, _ int, in any) {
					p := in.(entry)
					ok = it.SeekLT(p.key, p.seq)
					i, _ := slices.BinarySearchFunc(es, p, func(a, b entry) int {
						return sstable.Compare(a.key, a.seq, b.key, b.seq)
					})
					at, on = i-1, i > 0
				},
			},
			{
				Name:   moveNext,
				Weight: 3,
				Run: func(*prop.Case, int, any) {
					ok = it.Next()
					at++
					on = on && at < len(es)
				},
			},
			{
				Name:   movePrev,
				Weight: 3,
				Run: func(*prop.Case, int, any) {
					ok = it.Prev()
					at--
					on = on && at >= 0
				},
			},
		},
		Invariant: func(c *prop.Case, _ struct{}) {
			assert.NoError(c, it.Err(), "the cursor reads the table")
			assert.Equal(c, ok, on, "a move reports whether the model has an entry at its place")
			if !on {
				assert.Nil(c, it.Key(), "a cursor on no entry has no key")

				return
			}
			assert.Equal(c, entry{key: it.Key(), seq: it.Seq(), value: it.Value()}, es[at],
				"the cursor is on the entry of the model", assert.EquateEmpty())
		},
	})
}

// brokenRestart writes the golden table under everyEntry, and breaks the
// offset of the restart point restart of its second block. It returns a
// cursor of the table and the key and the sequence of the entry e of the
// second block, the probe of a seek.
func brokenRestart(tb assert.TB, restart, e int) (*sstable.Iter, []byte, uint64) {
	tb.Helper()

	l := newLayout(tb, everyEntry, goldenTable())
	b := l.blocks[1]
	block := l.data[b.off : b.off+b.n]
	offsets := restarts(block)
	assert.Length(tb, offsets, 4, "the second block has four restart points")
	ikey, _ := restartEntry(block[offsets[e]:])
	n := len(ikey) - seqLen
	key, seq := bytes.Clone(ikey[:n]), binary.BigEndian.Uint64(ikey[n:])
	binary.BigEndian.PutUint32(block[len(block)-4-4*len(offsets)+4*restart:], math.MaxUint32)
	l.seal()

	return openTable(tb, l.data, sstable.ReaderOptions{}).NewIter(), key, seq
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
// i of l: the offset and the length of the block as varints, and its CRC32C.
// The value is a part of the bytes of l, so a case changes the table through
// it.
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
