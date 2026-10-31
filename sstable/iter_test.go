// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sstable_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"runtime"
	"slices"
	"testing"
	"weak"

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

// aheadBytes is the ReadAhead of the cases of reads ahead: about four blocks
// of a table of smallBlocks.
const aheadBytes = 1024

// indexAllocs bounds the allocations of the index of a lending cache of four
// blocks in a scan of the golden table under settings[3]. The cache builds a
// new index of two objects, the index and its slots, when the tombs of the
// evicted blocks fill the slots. The hash seed of the cache places the
// tombs, and 30 seeds measured 3 to 5 such allocations per scan. A profile
// of the scan attributes them to the index of cache.Cache, and one more
// allocation to its entry for each block.
const indexAllocs = 6

// everyEntry are the options of the tables whose data blocks a case changes:
// small blocks with a restart point at every entry, so the restart offsets
// of a block are the offsets of all its entries. The data blocks end at the
// filter block.
var everyEntry = sstable.WriterOptions{BlockSize: smallBlocks, RestartInterval: 1, FilterBits: 8}

// shortOnceFile is a File over data whose read number short copies the bytes
// that it asks for, and returns one byte less with errRead. Its other reads
// return every byte.
type shortOnceFile struct {
	data  []byte
	short int
	reads int
}

var _ sstable.File = (*shortOnceFile)(nil)

// ReadAt reads the bytes of data at off, and fails the read number short.
func (r *shortOnceFile) ReadAt(_ context.Context, p []byte, off int64) (int, error) {
	r.reads++
	n, err := bytes.NewReader(r.data).ReadAt(p, off)
	if r.reads == r.short {
		return n - 1, errRead
	}

	return n, err
}

// emptyCache is a BlockCache that does not keep a block. Get reports false
// for every block, and Add drops the block.
type emptyCache struct{}

var _ sstable.BlockCache = emptyCache{}

// Get reports false.
func (emptyCache) Get(context.Context, uint64, uint64) ([]byte, bool) {
	return nil, false
}

// Add drops the block.
func (emptyCache) Add(context.Context, uint64, uint64, []byte) {}

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
			it := openTable(t, writeTable(t, settings[2].opts, goldenTable()), sstable.ReaderOptions{}).
				NewIter(sstable.IterOptions{})
			defer it.Close()
			assert.True(t, it.First(t.Context()), "First finds an entry")
			expect.Equal(t, it.Key(), []byte("key 000"), "First moves to the smallest key")
			expect.Equal(t, it.Seq(), uint64(2), "First moves to the newest version of the key")
		})

		t.Run("reports false for a table of no entries", func(t *testing.T) {
			t.Parallel()
			it := openTable(t, writeTable(t, settings[0].opts, nil), sstable.ReaderOptions{}).
				NewIter(sstable.IterOptions{})
			defer it.Close()
			assert.False(t, it.First(t.Context()), "First finds no entry")
		})

		t.Run("clears the error that exhausted the cursor", func(t *testing.T) {
			t.Parallel()
			it := exhaustedIter(t)
			defer it.Close()
			assert.True(t, it.First(t.Context()), "First finds the first entry")
			assert.NoError(t, it.Err(), "First clears the error")
		})

		t.Run("returns the error of the context of its read", func(t *testing.T) {
			t.Parallel()
			it := openTable(t, writeTable(t, settings[0].opts, goldenTable()), sstable.ReaderOptions{}).
				NewIter(sstable.IterOptions{})
			defer it.Close()
			assert.HonoursCancellation(t, func(ctx context.Context) error {
				assert.False(t, it.First(ctx), "First finds no entry")

				return it.Err()
			}, "First returns the error that the File returns for a context that ended")
		})

		t.Run("returns the error of the context for a block of the cache", func(t *testing.T) {
			t.Parallel()
			it := warmReader(t, writeTable(t, settings[0].opts, goldenTable())).NewIter(sstable.IterOptions{})
			defer it.Close()
			assert.HonoursCancellation(t, func(ctx context.Context) error {
				assert.False(t, it.First(ctx), "First finds no entry")

				return it.Err()
			}, "First returns the error of a context that ended for a block that the cache contains")
		})
	})

	t.Run("Last", func(t *testing.T) {
		t.Parallel()

		t.Run("moves to the last entry", func(t *testing.T) {
			t.Parallel()
			it := openTable(t, writeTable(t, settings[2].opts, goldenTable()), sstable.ReaderOptions{}).
				NewIter(sstable.IterOptions{})
			defer it.Close()
			assert.True(t, it.Last(t.Context()), "Last finds an entry")
			expect.Equal(t, it.Key(), goldenKeys[len(goldenKeys)-1], "Last moves to the largest key")
			expect.Equal(t, it.Seq(), uint64(1), "Last moves to the oldest version of the key")
		})

		t.Run("reports false for a table of no entries", func(t *testing.T) {
			t.Parallel()
			it := openTable(t, writeTable(t, settings[0].opts, nil), sstable.ReaderOptions{}).
				NewIter(sstable.IterOptions{})
			defer it.Close()
			assert.False(t, it.Last(t.Context()), "Last finds no entry")
		})

		t.Run("clears the error that exhausted the cursor", func(t *testing.T) {
			t.Parallel()
			it := exhaustedIter(t)
			defer it.Close()
			assert.True(t, it.Last(t.Context()), "Last finds the last entry")
			assert.NoError(t, it.Err(), "Last clears the error")
		})

		t.Run("returns the error of the context of its read", func(t *testing.T) {
			t.Parallel()
			it := openTable(t, writeTable(t, settings[0].opts, goldenTable()), sstable.ReaderOptions{}).
				NewIter(sstable.IterOptions{})
			defer it.Close()
			assert.HonoursCancellation(t, func(ctx context.Context) error {
				assert.False(t, it.Last(ctx), "Last finds no entry")

				return it.Err()
			}, "Last returns the error that the File returns for a context that ended")
		})

		t.Run("returns the error of the context for a block of the cache", func(t *testing.T) {
			t.Parallel()
			it := warmReader(t, writeTable(t, settings[0].opts, goldenTable())).NewIter(sstable.IterOptions{})
			defer it.Close()
			assert.HonoursCancellation(t, func(ctx context.Context) error {
				assert.False(t, it.Last(ctx), "Last finds no entry")

				return it.Err()
			}, "Last returns the error of a context that ended for a block that the cache contains")
		})
	})

	t.Run("SeekGE", func(t *testing.T) {
		t.Parallel()

		t.Run("moves to the first entry not before the probe in a table with versions", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "SeekGE and Next yield the entries of a model in the order of CompareWithSuffix",
				func(c *prop.Case) {
					es := c.Draw(versionedEntries, "entries")
					opts := c.Draw(prop.SampledFrom(settings[:7]...), "setting").opts
					opts.SuffixBytes = versionBytes
					it := openTable(c, writeTable(c, opts, es), sstable.ReaderOptions{SuffixBytes: versionBytes}).
						NewIter(sstable.IterOptions{})
					defer it.Close()
					ctx := c.Context()
					for range 10 {
						probe := entry{
							key: binary.BigEndian.AppendUint64(bytes.Clone(c.Draw(prop.SampledFrom(keys...), "key")),
								c.Draw(prop.Integer[uint64](0, 4), "version")),
							seq: c.Draw(prop.Integer[uint64](0, 4), "seq"),
						}
						i, _ := slices.BinarySearchFunc(es, probe, func(a, b entry) int {
							return sstable.CompareWithSuffix(a.key, a.seq, b.key, b.seq, versionBytes)
						})
						var got []entry
						for ok := it.SeekGE(ctx, probe.key, probe.seq); ok && len(got) < 3; ok = it.Next(ctx) {
							got = append(got,
								entry{key: bytes.Clone(it.Key()), seq: it.Seq(), value: bytes.Clone(it.Value())})
						}
						assert.NoError(c, it.Err(), "the cursor reads the table")
						assert.Equal(c, got, es[i:min(i+3, len(es))], "the cursor yields the entries from the probe on",
							assert.EquateEmpty())
					}
				})
		})

		for _, tt := range settings {
			t.Run("moves to the first entry not before the probe in a table "+tt.name, func(t *testing.T) {
				t.Parallel()
				prop.ForAll(t, "SeekGE and the moves after it yield the entries of the model from the probe",
					func(c *prop.Case) {
						es := c.Draw(entries, "entries")
						it := openTable(c, writeTable(c, tt.opts, es), sstable.ReaderOptions{}).
							NewIter(sstable.IterOptions{})
						defer it.Close()
						ctx := c.Context()
						for range 10 {
							probe := entry{
								key: c.Draw(prop.SampledFrom(keys...), "key"),
								seq: c.Draw(prop.Integer[uint64](0, 6), "seq"),
							}
							i, _ := slices.BinarySearchFunc(es, probe, func(a, b entry) int {
								return sstable.Compare(a.key, a.seq, b.key, b.seq)
							})
							var got []entry
							for ok := it.SeekGE(ctx, probe.key, probe.seq); ok && len(got) < 3; ok = it.Next(ctx) {
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
			it := openTable(t, writeTable(t, settings[0].opts, goldenTable()), sstable.ReaderOptions{}).
				NewIter(sstable.IterOptions{})
			defer it.Close()
			assert.False(t, it.SeekGE(t.Context(), []byte("zz"), 0), "SeekGE finds no entry")
			assert.Nil(t, it.Key(), "the cursor has no key")
		})

		t.Run("clears the error that exhausted the cursor", func(t *testing.T) {
			t.Parallel()
			it := exhaustedIter(t)
			defer it.Close()
			assert.True(t, it.SeekGE(t.Context(), goldenKeys[0], 2), "SeekGE finds the first entry")
			assert.NoError(t, it.Err(), "SeekGE clears the error")
		})

		t.Run("returns the error of the context of its read", func(t *testing.T) {
			t.Parallel()
			it := openTable(t, writeTable(t, settings[0].opts, goldenTable()), sstable.ReaderOptions{}).
				NewIter(sstable.IterOptions{})
			defer it.Close()
			assert.HonoursCancellation(t, func(ctx context.Context) error {
				assert.False(t, it.SeekGE(ctx, goldenKeys[30], 1), "SeekGE finds no entry")

				return it.Err()
			}, "SeekGE returns the error that the File returns for a context that ended")
		})

		t.Run("returns the error of the context for a block of the cache", func(t *testing.T) {
			t.Parallel()
			it := warmReader(t, writeTable(t, settings[0].opts, goldenTable())).NewIter(sstable.IterOptions{})
			defer it.Close()
			assert.HonoursCancellation(t, func(ctx context.Context) error {
				assert.False(t, it.SeekGE(ctx, goldenKeys[30], 1), "SeekGE finds no entry")

				return it.Err()
			}, "SeekGE returns the error of a context that ended for a block that the cache contains")
		})

		t.Run("moves within the current block under a context that ended", func(t *testing.T) {
			t.Parallel()
			it := openTable(t, writeTable(t, settings[0].opts, goldenTable()), sstable.ReaderOptions{}).
				NewIter(sstable.IterOptions{})
			defer it.Close()
			assert.True(t, it.First(t.Context()), "First finds the first entry")
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			assert.True(t, it.SeekGE(ctx, goldenKeys[0], 1), "SeekGE does not read a block for the second entry")
			expect.Equal(t, it.Key(), goldenKeys[0], "SeekGE moves to the first key")
			expect.Equal(t, it.Seq(), uint64(1), "SeekGE moves to the older version")
		})

		t.Run("remains on the current entry under a context that ended", func(t *testing.T) {
			t.Parallel()
			it := openTable(t, writeTable(t, settings[0].opts, goldenTable()), sstable.ReaderOptions{}).
				NewIter(sstable.IterOptions{})
			defer it.Close()
			assert.True(t, it.First(t.Context()), "First finds the first entry")
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			assert.True(t, it.SeekGE(ctx, goldenKeys[0], 2), "SeekGE does not read a block for the current entry")
			expect.Equal(t, it.Key(), goldenKeys[0], "SeekGE remains on the key")
			expect.Equal(t, it.Seq(), uint64(2), "SeekGE remains on the sequence")
		})

		t.Run("returns ErrCorrupt for a shared prefix at the first restart point of its search", func(t *testing.T) {
			t.Parallel()
			// The second block of a table of everyEntry has four restart points,
			// and the search of a seek compares the third first. A seek of the
			// first entry then decodes the first restart point alone.
			es := goldenTable()
			l := newLayout(t, everyEntry, es)
			first, second := l.blocks[0], l.blocks[1]
			block := l.data[second.off : second.off+second.n]
			offsets := restarts(block)
			assert.Length(t, offsets, 4, "the second block has four restart points")
			block[offsets[2]+sharedAt] = 1
			l.seal()
			probe := es[len(restarts(l.data[first.off:first.off+first.n]))]
			it := openTable(t, l.data, sstable.ReaderOptions{}).NewIter(sstable.IterOptions{})
			defer it.Close()
			expect.False(t, it.SeekGE(t.Context(), probe.key, probe.seq), "SeekGE finds no entry")
			expect.ErrorIs(t, it.Err(), sstable.ErrCorrupt, "SeekGE refuses the restart point that it compares")
		})

		t.Run("moves to the last entry of the current block under a context that ended", func(t *testing.T) {
			t.Parallel()
			es := goldenTable()
			l := newLayout(t, everyEntry, es)
			// Every entry of a table of everyEntry is a restart point, so the
			// first block has as many entries as restart points.
			first := l.blocks[0]
			last := es[len(restarts(l.data[first.off:first.off+first.n]))-1]
			it := openTable(t, l.data, sstable.ReaderOptions{}).NewIter(sstable.IterOptions{})
			defer it.Close()
			assert.True(t, it.First(t.Context()), "First finds the first entry")
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			assert.True(t, it.SeekGE(ctx, last.key, last.seq), "SeekGE does not read a block for the last entry")
			expect.Equal(t, it.Key(), last.key, "SeekGE moves to the key of the last entry")
			expect.Equal(t, it.Seq(), last.seq, "SeekGE moves to the sequence of the last entry")
		})

		t.Run("moves to each entry of a table of long entries", func(t *testing.T) {
			t.Parallel()
			es := longTable()
			it := openTable(t, writeTable(t, longEntries, es), sstable.ReaderOptions{}).NewIter(sstable.IterOptions{})
			defer it.Close()
			got := make([]entry, 0, len(es))
			for _, e := range es {
				if it.SeekGE(t.Context(), e.key, e.seq) {
					got = append(got, entry{key: bytes.Clone(it.Key()), seq: it.Seq(), value: bytes.Clone(it.Value())})
				}
			}
			assert.NoError(t, it.Err(), "the cursor reads the table")
			assert.Equal(t, got, es, "SeekGE moves to each entry")
		})

		for _, tt := range brokenRestarts {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				it, key, seq := brokenRestart(t, tt.giveRestart, tt.giveEntry)
				defer it.Close()
				expect.False(t, it.SeekGE(t.Context(), key, seq), "SeekGE finds no entry")
				expect.ErrorIs(t, it.Err(), sstable.ErrCorrupt, "SeekGE refuses the restart offset that it decodes")
				expect.Nil(t, it.Key(), "the exhausted cursor has no key")
			})
		}
	})

	t.Run("SeekLT", func(t *testing.T) {
		t.Parallel()

		t.Run("moves to the last entry before the probe in a table with versions", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "SeekLT and Prev yield the entries of a model in the order of CompareWithSuffix",
				func(c *prop.Case) {
					es := c.Draw(versionedEntries, "entries")
					opts := c.Draw(prop.SampledFrom(settings[:7]...), "setting").opts
					opts.SuffixBytes = versionBytes
					it := openTable(c, writeTable(c, opts, es), sstable.ReaderOptions{SuffixBytes: versionBytes}).
						NewIter(sstable.IterOptions{})
					defer it.Close()
					ctx := c.Context()
					for range 10 {
						probe := entry{
							key: binary.BigEndian.AppendUint64(bytes.Clone(c.Draw(prop.SampledFrom(keys...), "key")),
								c.Draw(prop.Integer[uint64](0, 4), "version")),
							seq: c.Draw(prop.Integer[uint64](0, 4), "seq"),
						}
						i, _ := slices.BinarySearchFunc(es, probe, func(a, b entry) int {
							return sstable.CompareWithSuffix(a.key, a.seq, b.key, b.seq, versionBytes)
						})
						var got []entry
						for ok := it.SeekLT(ctx, probe.key, probe.seq); ok && len(got) < 3; ok = it.Prev(ctx) {
							got = append(got,
								entry{key: bytes.Clone(it.Key()), seq: it.Seq(), value: bytes.Clone(it.Value())})
						}
						assert.NoError(c, it.Err(), "the cursor reads the table")
						want := slices.Clone(es[max(i-3, 0):i])
						slices.Reverse(want)
						assert.Equal(c, got, want, "the cursor yields the entries before the probe back",
							assert.EquateEmpty())
					}
				})
		})

		for _, tt := range settings {
			t.Run("moves to the last entry before the probe in a table "+tt.name, func(t *testing.T) {
				t.Parallel()
				prop.ForAll(t, "SeekLT and the moves back after it yield the entries of the model before the probe",
					func(c *prop.Case) {
						es := c.Draw(entries, "entries")
						it := openTable(c, writeTable(c, tt.opts, es), sstable.ReaderOptions{}).
							NewIter(sstable.IterOptions{})
						defer it.Close()
						ctx := c.Context()
						for range 10 {
							probe := entry{
								key: c.Draw(prop.SampledFrom(keys...), "key"),
								seq: c.Draw(prop.Integer[uint64](0, 6), "seq"),
							}
							i, _ := slices.BinarySearchFunc(es, probe, func(a, b entry) int {
								return sstable.Compare(a.key, a.seq, b.key, b.seq)
							})
							var got []entry
							for ok := it.SeekLT(ctx, probe.key, probe.seq); ok && len(got) < 3; ok = it.Prev(ctx) {
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
			it := openTable(t, writeTable(t, settings[0].opts, goldenTable()), sstable.ReaderOptions{}).
				NewIter(sstable.IterOptions{})
			defer it.Close()
			assert.False(t, it.SeekLT(t.Context(), goldenKeys[0], 2), "SeekLT finds no entry")
			assert.Nil(t, it.Key(), "the cursor has no key")
		})

		t.Run("moves to the last entry for a probe past the last entry", func(t *testing.T) {
			t.Parallel()
			it := openTable(t, writeTable(t, settings[0].opts, goldenTable()), sstable.ReaderOptions{}).
				NewIter(sstable.IterOptions{})
			defer it.Close()
			assert.True(t, it.SeekLT(t.Context(), []byte("zz"), 0), "SeekLT finds an entry")
			expect.Equal(t, it.Key(), goldenKeys[len(goldenKeys)-1], "SeekLT moves to the largest key")
			expect.Equal(t, it.Seq(), uint64(1), "SeekLT moves to the oldest version of the key")
		})

		t.Run("clears the error that exhausted the cursor", func(t *testing.T) {
			t.Parallel()
			it := exhaustedIter(t)
			defer it.Close()
			assert.True(t, it.SeekLT(t.Context(), []byte("zz"), 0), "SeekLT finds the last entry")
			assert.NoError(t, it.Err(), "SeekLT clears the error")
		})

		t.Run("returns the error of the context of its read", func(t *testing.T) {
			t.Parallel()
			it := openTable(t, writeTable(t, settings[0].opts, goldenTable()), sstable.ReaderOptions{}).
				NewIter(sstable.IterOptions{})
			defer it.Close()
			assert.HonoursCancellation(t, func(ctx context.Context) error {
				assert.False(t, it.SeekLT(ctx, goldenKeys[30], 1), "SeekLT finds no entry")

				return it.Err()
			}, "SeekLT returns the error that the File returns for a context that ended")
		})

		t.Run("returns the error of the context for a block of the cache", func(t *testing.T) {
			t.Parallel()
			it := warmReader(t, writeTable(t, settings[0].opts, goldenTable())).NewIter(sstable.IterOptions{})
			defer it.Close()
			assert.HonoursCancellation(t, func(ctx context.Context) error {
				assert.False(t, it.SeekLT(ctx, goldenKeys[30], 1), "SeekLT finds no entry")

				return it.Err()
			}, "SeekLT returns the error of a context that ended for a block that the cache contains")
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
			it := openTable(t, l.data, sstable.ReaderOptions{}).NewIter(sstable.IterOptions{})
			defer it.Close()
			assert.True(t, it.SeekLT(t.Context(), goldenKeys[0], 1), "SeekLT finds an entry")
			expect.Equal(t, it.Seq(), uint64(3), "SeekLT moves to the entry of the second block")
			expect.Equal(t, it.Value(), []byte("x"), "SeekLT moves to the value of the entry")
		})

		for _, tt := range brokenRestarts {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				it, key, seq := brokenRestart(t, tt.giveRestart, tt.giveEntry)
				defer it.Close()
				expect.False(t, it.SeekLT(t.Context(), key, seq), "SeekLT finds no entry")
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
			it := openTable(t, l.data, sstable.ReaderOptions{}).NewIter(sstable.IterOptions{})
			defer it.Close()
			expect.False(t, it.SeekLT(t.Context(), goldenKeys[30], 1), "SeekLT finds no entry")
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
					it := openTable(c, writeTable(c, tt.opts, es), sstable.ReaderOptions{}).
						NewIter(sstable.IterOptions{})
					defer it.Close()
					got, err := scanAll(c.Context(), it)
					assert.NoError(c, err, "the cursor reads the table")
					assert.Equal(c, got, es, "the cursor yields every entry in order", assert.EquateEmpty())
				})
			})
		}

		t.Run("reports false after the last entry", func(t *testing.T) {
			t.Parallel()
			it := openTable(t, writeTable(t, settings[0].opts, goldenTable()[:1]), sstable.ReaderOptions{}).
				NewIter(sstable.IterOptions{})
			defer it.Close()
			assert.True(t, it.First(t.Context()), "First finds the entry")
			expect.False(t, it.Next(t.Context()), "Next finds no entry after the last")
			expect.False(t, it.Next(t.Context()), "Next of an exhausted cursor finds no entry")
			expect.Nil(t, it.Key(), "the exhausted cursor has no key")
			expect.Equal(t, it.Seq(), uint64(0), "the exhausted cursor has no sequence")
			expect.Nil(t, it.Value(), "the exhausted cursor has no value")
		})

		t.Run("reports false for a cursor that moved before the first entry", func(t *testing.T) {
			t.Parallel()
			it := openTable(t, writeTable(t, settings[0].opts, goldenTable()), sstable.ReaderOptions{}).
				NewIter(sstable.IterOptions{})
			defer it.Close()
			assert.False(t, it.SeekLT(t.Context(), goldenKeys[0], 2), "SeekLT finds no entry before the first")
			assert.False(t, it.Next(t.Context()), "Next of the exhausted cursor finds no entry")
		})

		t.Run("yields every entry of a table from the cache", func(t *testing.T) {
			t.Parallel()
			es := goldenTable()
			data := writeTable(t, settings[0].opts, es)
			counter := &countingFile{data: data}
			r, err := sstable.Open(t.Context(), counter, int64(len(data)),
				sstable.ReaderOptions{Cache: &mapCache{}, CacheID: 1})
			assert.NoError(t, err, "Open reads the table")
			it := r.NewIter(sstable.IterOptions{})
			defer it.Close()
			_, err = scanAll(t.Context(), it)
			assert.NoError(t, err, "the first scan adds every block to the cache")
			reads := counter.reads
			got, err := scanAll(t.Context(), it)
			assert.NoError(t, err, "the second scan takes every block from the cache")
			expect.Equal(t, got, es, "the second scan yields every entry")
			expect.Equal(t, counter.reads, reads, "the second scan reads no block")
		})

		t.Run("reads every block without adding it to the cache under NoFill", func(t *testing.T) {
			t.Parallel()
			es := goldenTable()
			cache := &mapCache{}
			it := openTable(t, writeTable(t, settings[0].opts, es), sstable.ReaderOptions{Cache: cache, CacheID: 1}).
				NewIter(sstable.IterOptions{NoFill: true})
			defer it.Close()
			got, err := scanAll(t.Context(), it)
			assert.NoError(t, err, "the cursor reads the table")
			expect.Equal(t, got, es, "the cursor yields every entry")
			expect.Equal(t, cache.adds, 0, "the cursor does not add a block to the cache")
		})

		t.Run("takes the blocks of the cache under NoFill", func(t *testing.T) {
			t.Parallel()
			es := goldenTable()
			data := writeTable(t, settings[0].opts, es)
			counter := &countingFile{data: data}
			r, err := sstable.Open(t.Context(), counter, int64(len(data)),
				sstable.ReaderOptions{Cache: &mapCache{}, CacheID: 1})
			assert.NoError(t, err, "Open reads the table")
			warm := r.NewIter(sstable.IterOptions{})
			defer warm.Close()
			_, err = scanAll(t.Context(), warm)
			assert.NoError(t, err, "a scan without NoFill adds every block to the cache")
			reads := counter.reads
			it := r.NewIter(sstable.IterOptions{NoFill: true})
			defer it.Close()
			got, err := scanAll(t.Context(), it)
			assert.NoError(t, err, "the scan under NoFill takes every block from the cache")
			expect.Equal(t, got, es, "the scan under NoFill yields every entry")
			expect.Equal(t, counter.reads, reads, "the scan under NoFill does not read a block")
		})

		t.Run("returns the error of the context of the read of the next block", func(t *testing.T) {
			t.Parallel()
			it := openTable(t, writeTable(t, oneEntry, goldenTable()), sstable.ReaderOptions{}).
				NewIter(sstable.IterOptions{})
			defer it.Close()
			assert.True(t, it.First(t.Context()), "First finds the entry of the first block")
			assert.HonoursCancellation(t, func(ctx context.Context) error {
				assert.False(t, it.Next(ctx), "Next finds no entry")

				return it.Err()
			}, "Next returns the error that the File returns for a context that ended")
		})

		t.Run("returns the error of the context for a next block of the cache", func(t *testing.T) {
			t.Parallel()
			it := warmReader(t, writeTable(t, oneEntry, goldenTable())).NewIter(sstable.IterOptions{})
			defer it.Close()
			assert.True(t, it.First(t.Context()), "First finds the entry of the first block")
			assert.HonoursCancellation(t, func(ctx context.Context) error {
				assert.False(t, it.Next(ctx), "Next finds no entry")

				return it.Err()
			}, "Next returns the error of a context that ended for a block that the cache contains")
		})

		t.Run("reads the File once for the blocks of each read ahead", func(t *testing.T) {
			t.Parallel()
			es := goldenTable()
			l := newLayout(t, settings[0].opts, es)
			counter := &countingFile{data: l.data}
			r, err := sstable.Open(t.Context(), counter, int64(len(l.data)), sstable.ReaderOptions{})
			assert.NoError(t, err, "Open reads the table")
			reads := counter.reads
			it := r.NewIter(sstable.IterOptions{ReadAhead: aheadBytes})
			defer it.Close()
			got, err := scanAll(t.Context(), it)
			assert.NoError(t, err, "the cursor reads the table")
			expect.Equal(t, got, es, "the cursor yields every entry")
			expect.Equal(t, counter.reads-reads, aheadReads(l, aheadBytes, false),
				"the scan reads the File once for each read ahead")
		})

		t.Run("adds each block of a read ahead to the cache", func(t *testing.T) {
			t.Parallel()
			es := goldenTable()
			l := newLayout(t, settings[0].opts, es)
			cache := &mapCache{}
			it := openTable(t, l.data, sstable.ReaderOptions{Cache: cache, CacheID: 1}).
				NewIter(sstable.IterOptions{ReadAhead: aheadBytes})
			defer it.Close()
			_, err := scanAll(t.Context(), it)
			assert.NoError(t, err, "the cursor reads the table")
			assert.Equal(t, cache.adds, len(l.blocks), "the cursor adds every block to the cache")
		})

		t.Run("returns the error of the File for a read ahead", func(t *testing.T) {
			t.Parallel()
			data := writeTable(t, settings[0].opts, goldenTable())
			counter := &countingFile{data: data}
			_, err := sstable.Open(t.Context(), counter, int64(len(data)), sstable.ReaderOptions{})
			assert.NoError(t, err, "Open reads the table")
			r, err := sstable.Open(t.Context(), &failingFile{data: data, ok: counter.reads + 1}, int64(len(data)),
				sstable.ReaderOptions{})
			assert.NoError(t, err, "Open reads the table with the reads that it needs")
			it := r.NewIter(sstable.IterOptions{ReadAhead: aheadBytes})
			defer it.Close()
			_, err = scanAll(t.Context(), it)
			assert.ErrorIs(t, err, errRead, "the cursor returns the error of its second read ahead")
		})

		t.Run("reads the blocks again after a short read ahead", func(t *testing.T) {
			t.Parallel()
			es := goldenTable()
			data := writeTable(t, settings[0].opts, es)
			counter := &countingFile{data: data}
			_, err := sstable.Open(t.Context(), counter, int64(len(data)), sstable.ReaderOptions{})
			assert.NoError(t, err, "Open reads the table")
			// The second read of the scan writes the bytes at its own offset into
			// the buffer of the reads ahead before it fails.
			r, err := sstable.Open(t.Context(), &shortOnceFile{data: data, short: counter.reads + 2}, int64(len(data)),
				sstable.ReaderOptions{})
			assert.NoError(t, err, "Open reads the table")
			it := r.NewIter(sstable.IterOptions{ReadAhead: aheadBytes})
			defer it.Close()
			_, err = scanAll(t.Context(), it)
			assert.ErrorIs(t, err, errRead, "the first scan returns the error of the short read")
			got, err := scanAll(t.Context(), it)
			assert.NoError(t, err, "the second scan reads the table")
			assert.Equal(t, got, es, "the second scan yields every entry")
		})

		t.Run("yields every entry of a table of long entries in order", func(t *testing.T) {
			t.Parallel()
			es := longTable()
			it := openTable(t, writeTable(t, longEntries, es), sstable.ReaderOptions{}).NewIter(sstable.IterOptions{})
			defer it.Close()
			got, err := scanAll(t.Context(), it)
			assert.NoError(t, err, "the cursor reads the table")
			assert.Equal(t, got, es, "the cursor yields every entry in order")
		})

		t.Run("moves within a block under a context that ended", func(t *testing.T) {
			t.Parallel()
			it := openTable(t, writeTable(t, settings[0].opts, goldenTable()), sstable.ReaderOptions{}).
				NewIter(sstable.IterOptions{})
			defer it.Close()
			assert.True(t, it.First(t.Context()), "First finds the first entry")
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			assert.True(t, it.Next(ctx), "Next does not read a block for the second entry of the block")
			expect.Equal(t, it.Key(), goldenKeys[0], "Next moves to the second version of the first key")
			expect.Equal(t, it.Seq(), uint64(1), "Next moves to the older version")
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
				it := openTable(t, l.data, sstable.ReaderOptions{Cache: cache, CacheID: 1}).
					NewIter(sstable.IterOptions{})
				defer it.Close()
				_, err := scanAll(t.Context(), it)
				assert.ErrorIs(t, err, sstable.ErrCorrupt, "the cursor refuses the second block")
				_, ok := cache.Get(t.Context(), 1, uint64(l.blocks[1].off))
				expect.False(t, ok, "the cache has no block at the place of the second block")
				expect.Equal(t, cache.adds, 1, "the cursor adds the first block alone")
			})
		}

		t.Run("adds no block to a lending cache under NoFill", func(t *testing.T) {
			t.Parallel()
			lending := lendingCache(t, 4)
			it := openTable(t, writeTable(t, settings[3].opts, goldenTable()),
				sstable.ReaderOptions{Cache: lending, CacheID: 1}).NewIter(sstable.IterOptions{NoFill: true})
			defer it.Close()
			got, err := scanAll(t.Context(), it)
			assert.NoError(t, err, "the cursor reads the table")
			expect.Length(t, got, goldenEntries, "the cursor yields every entry")
			expect.Equal(t, lending.Len(), 0, "a cursor under NoFill adds no block to the cache")
		})

		t.Run("leaves the blocks of a lending cache intact under NoFill", func(t *testing.T) {
			t.Parallel()
			es := goldenTable()
			l := newLayout(t, settings[0].opts, es)
			prop.ForAll(t, "a lending cache returns the entries of every block after a scan under NoFill",
				func(c *prop.Case) {
					// The cache has room for every block, so it evicts none. A block
					// that the scan under NoFill returned to its pool would receive
					// the bytes of a block that the next scan adds.
					r := openTable(c, l.data, sstable.ReaderOptions{Cache: lendingCache(c, len(l.blocks)), CacheID: 1})
					for _, key := range goldenKeys {
						if c.Draw(prop.Boolean(), "warm") {
							_, _, _, err := r.Get(c.Context(), key, 1, nil)
							assert.NoError(c, err, "Get adds the block of its key to the cache")
						}
					}
					cold := r.NewIter(sstable.IterOptions{NoFill: true})
					_, err := scanAll(c.Context(), cold)
					assert.NoError(c, err, "the scan under NoFill reads the table")
					assert.NoError(c, cold.Close(), "the cursor under NoFill closes without an error")
					it := r.NewIter(sstable.IterOptions{})
					defer it.Close()
					_, err = scanAll(c.Context(), it)
					assert.NoError(c, err, "the first scan without NoFill adds every other block to the cache")
					got, err := scanAll(c.Context(), it)
					assert.NoError(c, err, "the second scan takes every block from the cache")
					assert.Equal(c, got, es, "the second scan yields the entries of the table")
				})
		})
	})

	t.Run("Prev", func(t *testing.T) {
		t.Parallel()

		for _, tt := range settings {
			t.Run("yields every entry of a table "+tt.name+" from the last", func(t *testing.T) {
				t.Parallel()
				prop.ForAll(t, "a cursor yields the entries that the Writer added from the last back",
					func(c *prop.Case) {
						es := c.Draw(entries, "entries")
						it := openTable(c, writeTable(c, tt.opts, es), sstable.ReaderOptions{}).
							NewIter(sstable.IterOptions{})
						defer it.Close()
						got, err := scanBack(c.Context(), it)
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
			it := openTable(t, writeTable(t, settings[0].opts, goldenTable()[:1]), sstable.ReaderOptions{}).
				NewIter(sstable.IterOptions{})
			defer it.Close()
			assert.True(t, it.Last(t.Context()), "Last finds the entry")
			expect.False(t, it.Prev(t.Context()), "Prev finds no entry before the first")
			expect.False(t, it.Prev(t.Context()), "Prev of an exhausted cursor finds no entry")
			expect.Nil(t, it.Key(), "the exhausted cursor has no key")
		})

		t.Run("reports false for a cursor that moved past the last entry", func(t *testing.T) {
			t.Parallel()
			it := openTable(t, writeTable(t, settings[0].opts, goldenTable()), sstable.ReaderOptions{}).
				NewIter(sstable.IterOptions{})
			defer it.Close()
			assert.False(t, it.SeekGE(t.Context(), []byte("zz"), 0), "SeekGE finds no entry past the last")
			assert.False(t, it.Prev(t.Context()), "Prev of the exhausted cursor finds no entry")
		})

		t.Run("yields every entry of a table from the cache", func(t *testing.T) {
			t.Parallel()
			es := goldenTable()
			data := writeTable(t, settings[0].opts, es)
			counter := &countingFile{data: data}
			r, err := sstable.Open(t.Context(), counter, int64(len(data)),
				sstable.ReaderOptions{Cache: &mapCache{}, CacheID: 1})
			assert.NoError(t, err, "Open reads the table")
			it := r.NewIter(sstable.IterOptions{})
			defer it.Close()
			_, err = scanBack(t.Context(), it)
			assert.NoError(t, err, "the first scan adds every block to the cache")
			reads := counter.reads
			got, err := scanBack(t.Context(), it)
			assert.NoError(t, err, "the second scan takes every block from the cache")
			want := slices.Clone(es)
			slices.Reverse(want)
			expect.Equal(t, got, want, "the second scan yields every entry")
			expect.Equal(t, counter.reads, reads, "the second scan reads no block")
		})

		t.Run("returns the error of the context of the read of the block before", func(t *testing.T) {
			t.Parallel()
			it := openTable(t, writeTable(t, oneEntry, goldenTable()), sstable.ReaderOptions{}).
				NewIter(sstable.IterOptions{})
			defer it.Close()
			assert.True(t, it.Last(t.Context()), "Last finds the entry of the last block")
			assert.HonoursCancellation(t, func(ctx context.Context) error {
				assert.False(t, it.Prev(ctx), "Prev finds no entry")

				return it.Err()
			}, "Prev returns the error that the File returns for a context that ended")
		})

		t.Run("returns the error of the context for a block before of the cache", func(t *testing.T) {
			t.Parallel()
			it := warmReader(t, writeTable(t, oneEntry, goldenTable())).NewIter(sstable.IterOptions{})
			defer it.Close()
			assert.True(t, it.Last(t.Context()), "Last finds the entry of the last block")
			assert.HonoursCancellation(t, func(ctx context.Context) error {
				assert.False(t, it.Prev(ctx), "Prev finds no entry")

				return it.Err()
			}, "Prev returns the error of a context that ended for a block that the cache contains")
		})

		t.Run("reads the File once for the blocks of each read ahead back", func(t *testing.T) {
			t.Parallel()
			es := goldenTable()
			l := newLayout(t, settings[0].opts, es)
			counter := &countingFile{data: l.data}
			r, err := sstable.Open(t.Context(), counter, int64(len(l.data)), sstable.ReaderOptions{})
			assert.NoError(t, err, "Open reads the table")
			reads := counter.reads
			it := r.NewIter(sstable.IterOptions{ReadAhead: aheadBytes})
			defer it.Close()
			got, err := scanBack(t.Context(), it)
			assert.NoError(t, err, "the cursor reads the table")
			want := slices.Clone(es)
			slices.Reverse(want)
			expect.Equal(t, got, want, "the cursor yields every entry back")
			expect.Equal(t, counter.reads-reads, aheadReads(l, aheadBytes, true),
				"the scan back reads the File once for each read ahead")
		})

		t.Run("yields every entry of a table of long entries from the last", func(t *testing.T) {
			t.Parallel()
			es := longTable()
			it := openTable(t, writeTable(t, longEntries, es), sstable.ReaderOptions{}).NewIter(sstable.IterOptions{})
			defer it.Close()
			got, err := scanBack(t.Context(), it)
			assert.NoError(t, err, "the cursor reads the table")
			want := slices.Clone(es)
			slices.Reverse(want)
			assert.Equal(t, got, want, "the cursor yields every entry in reverse order")
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
			it := openTable(t, l.data, sstable.ReaderOptions{}).NewIter(sstable.IterOptions{})
			defer it.Close()
			assert.True(t, it.First(t.Context()), "First finds the entry of the key a")
			assert.True(t, it.Next(t.Context()), "Next finds the entry of the key b")
			expect.False(t, it.Prev(t.Context()), "Prev finds no entry")
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
			it := openTable(t, l.data, sstable.ReaderOptions{}).NewIter(sstable.IterOptions{})
			defer it.Close()
			got, err := scanBack(t.Context(), it)
			expect.ErrorIs(t, err, sstable.ErrCorrupt, "Prev refuses the second entry of the second block")
			assert.NotEmpty(t, got, "the cursor yields the entries after the second block")
			expect.Equal(t, got[len(got)-1].key, []byte("c"), "the cursor yields the third entry before the error")
		})
	})

	t.Run("Value", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the value of the current entry after a lending cache drops its block", func(t *testing.T) {
			t.Parallel()
			es := goldenTable()
			lending := lendingCache(t, 4)
			r := openTable(t, writeTable(t, settings[2].opts, es), sstable.ReaderOptions{Cache: lending, CacheID: 1})
			warm := r.NewIter(sstable.IterOptions{})
			assert.True(t, warm.First(t.Context()), "First adds the first block to the cache")
			assert.NoError(t, warm.Close(), "Close releases the first block")
			it := r.NewIter(sstable.IterOptions{})
			defer it.Close()
			assert.True(t, it.First(t.Context()), "First lends the first block from the cache")
			// The cache drops its reference to the lent block when Add replaces
			// the block. The scan then copies each block that it reads into a
			// buffer from the pool.
			block, ok := lending.Get(t.Context(), 1, 0)
			assert.True(t, ok, "the cache contains the first block")
			lending.Add(t.Context(), 1, 0, block)
			other := r.NewIter(sstable.IterOptions{})
			defer other.Close()
			_, err := scanAll(t.Context(), other)
			assert.NoError(t, err, "the second cursor reads the table")
			expect.Equal(t, it.Value(), es[0].value, "the value of the current entry keeps its bytes")
		})
	})

	t.Run("Err", func(t *testing.T) {
		t.Parallel()

		t.Run("returns ErrCorrupt for a block that fails its checksum", func(t *testing.T) {
			t.Parallel()
			data := writeTable(t, settings[0].opts, goldenTable())
			data[len(data)/3] ^= 1
			it := openTable(t, data, sstable.ReaderOptions{}).NewIter(sstable.IterOptions{})
			defer it.Close()
			_, err := scanAll(t.Context(), it)
			expect.ErrorIs(t, err, sstable.ErrCorrupt, "the cursor refuses the changed block")
			expect.False(t, it.Next(t.Context()), "the cursor is exhausted after the error")
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
					it := openTable(c, l.data, sstable.ReaderOptions{}).NewIter(sstable.IterOptions{})
					defer it.Close()
					got, err := scanAll(c.Context(), it)
					assert.ErrorIs(c, err, sstable.ErrCorrupt, "the cursor refuses the block at the place")
					assert.Equal(c, got, es[:to], "the cursor yields the entries before the place")
				})
		})

		// A case changes the second data block or its entry in the index. A
		// case of a table in partitions changes the second index partition or
		// its entry in the top-level index instead. A cached case reads the
		// changed table through a cache with every block and partition of the
		// table before the change. A cursor that took a block of a corrupt
		// place from that cache would find the block.
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
				name:     "returns ErrCorrupt for an entry of 2 bytes at the end of the entries",
				giveOpts: oneEntry,
				giveChange: func(l *layout, _ []byte) {
					// The two bytes end the first two varints of a header, and the
					// third varint has no byte. The restart offset 0 and the count
					// 1 follow the entry.
					replaceBlock(l, 1, []byte{0, seqLen, 0, 0, 0, 0, 0, 0, 0, 1})
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
			{
				name:     "returns ErrCorrupt for a CRC32C in the top-level index that differs from the partition",
				giveOpts: oneEntryParts,
				giveChange: func(l *layout, _ []byte) {
					l.indexParts[1].parent[0] ^= 1
				},
			},
			{
				name:     "returns ErrCorrupt for an index partition without room for its restart offsets",
				giveOpts: oneEntryParts,
				giveChange: func(_ *layout, part []byte) {
					binary.BigEndian.PutUint32(part[len(part)-4:], math.MaxUint32)
				},
			},
			{
				name:       "returns ErrCorrupt for a partition one byte before the partitions",
				giveOpts:   oneEntryParts,
				giveCached: true,
				giveChange: func(l *layout, _ []byte) {
					// The data blocks end at the first filter partition. Both
					// offsets have a varint of two bytes.
					binary.PutUvarint(indexValue(l, 1), uint64(l.filterParts[0].off-1))
				},
			},
			{
				name:       "returns ErrCorrupt for a partition that ends one byte past the partitions",
				giveOpts:   oneEntryParts,
				giveCached: true,
				giveChange: func(l *layout, _ []byte) {
					// The partitions end at the top-level index of the filter.
					setBlockLength(l, 1, l.filter.off-l.indexParts[1].off-trailerLen+1)
				},
			},
			{
				name:       "returns ErrCorrupt for a partition length that leaves no room for its trailer",
				giveOpts:   oneEntryParts,
				giveCached: true,
				giveChange: func(l *layout, _ []byte) {
					setBlockLength(l, 1, l.filter.off-trailerLen+1)
				},
			},
			{
				name:       "returns ErrCorrupt for a partition length past the partitions",
				giveOpts:   oneEntryParts,
				giveCached: true,
				giveChange: func(l *layout, _ []byte) {
					// The offset and the length have varints of two bytes.
					v := indexValue(l, 1)
					v[2], v[3] = 0xff, 0x7f
				},
			},
			{
				name:       "returns ErrCorrupt for a partition offset whose varint does not end",
				giveOpts:   oneEntryParts,
				giveCached: true,
				giveChange: func(l *layout, _ []byte) {
					v := indexValue(l, 1)
					for i := range v {
						v[i] = 0x80
					}
				},
			},
			{
				name:       "returns ErrCorrupt for a count of blocks whose varint does not end",
				giveOpts:   oneEntryParts,
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
				name:       "returns ErrCorrupt for a top-level value with bytes after the CRC32C",
				giveOpts:   oneEntryParts,
				giveCached: true,
				giveChange: func(l *layout, _ []byte) {
					// Without the high bit of its first byte, the varint of the
					// offset ends after one byte.
					indexValue(l, 1)[0] &^= 0x80
				},
			},
			{
				name:       "returns ErrCorrupt for a top-level value without room for its CRC32C",
				giveOpts:   oneEntryParts,
				giveCached: true,
				giveChange: func(l *layout, _ []byte) {
					// The count of blocks has a varint of one byte. With the high
					// bit, the varint ends at the first byte of the CRC32C.
					v := indexValue(l, 1)
					v[len(v)-crcLen-1] |= 0x80
					v[len(v)-crcLen] = 0
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				es := goldenTable()
				l := newLayout(t, tt.giveOpts, es)
				// Every entry of the first two blocks or index partitions is a
				// restart point. A block of a table in partitions has one entry,
				// so the restart points count the entries before the second
				// block or partition and after it.
				units := l.blocks
				if tt.giveOpts.PartitionSize != 0 {
					units = l.indexParts
				}
				first, second := units[0], units[1]
				before := len(restarts(l.data[first.off : first.off+first.n]))
				want := es[:before]
				wantBack := slices.Clone(es[before+len(restarts(l.data[second.off:second.off+second.n])):])
				slices.Reverse(wantBack)
				opts := sstable.ReaderOptions{}
				if tt.giveCached {
					opts = sstable.ReaderOptions{Cache: &mapCache{}, CacheID: 1}
					warm := openTable(t, l.data, opts).NewIter(sstable.IterOptions{})
					defer warm.Close()
					_, err := scanAll(t.Context(), warm)
					assert.NoError(t, err, "the cursor adds every block of the table to the cache")
				}
				tt.giveChange(l, l.data[second.off:second.off+second.n])
				l.seal()
				it := openTable(t, l.data, opts).NewIter(sstable.IterOptions{})
				defer it.Close()
				got, err := scanAll(t.Context(), it)
				expect.ErrorIs(t, err, sstable.ErrCorrupt, "the cursor refuses the change")
				expect.Equal(t, got, want, "the cursor yields the entries before the change")
				back, err := scanBack(t.Context(), it)
				expect.ErrorIs(t, err, sstable.ErrCorrupt, "the cursor refuses the change from the last entry")
				expect.Equal(t, back, wantBack, "the cursor yields the entries after the change back")
				probe := es[before]
				expect.False(t, it.SeekGE(t.Context(), probe.key, probe.seq), "SeekGE finds no entry of the change")
				expect.ErrorIs(t, it.Err(), sstable.ErrCorrupt, "SeekGE refuses the change")
			})
		}

		t.Run("returns ErrCorrupt for a restart offset of the top-level index that its search decodes",
			func(t *testing.T) {
				t.Parallel()
				// The search of the top-level index decodes its middle restart
				// point first. Open reads the first and the last entry alone.
				l := newLayout(t, oneEntryParts, goldenTable())
				top := l.data[l.index.off : l.index.off+l.index.n]
				count := len(restarts(top))
				binary.BigEndian.PutUint32(top[len(top)-4-4*count+4*(count/2):], math.MaxUint32)
				l.seal()
				it := openTable(t, l.data, sstable.ReaderOptions{}).NewIter(sstable.IterOptions{})
				defer it.Close()
				expect.False(t, it.SeekGE(t.Context(), goldenKeys[30], 1), "SeekGE finds no entry")
				expect.ErrorIs(t, it.Err(), sstable.ErrCorrupt,
					"SeekGE refuses the restart offset of the top-level index")
			})

		t.Run("returns the error of the File", func(t *testing.T) {
			t.Parallel()
			data := writeTable(t, settings[0].opts, goldenTable())
			counter := &countingFile{data: data}
			_, err := sstable.Open(t.Context(), counter, int64(len(data)), sstable.ReaderOptions{})
			assert.NoError(t, err, "Open reads the table")
			r, err := sstable.Open(t.Context(), &failingFile{data: data, ok: counter.reads + 1}, int64(len(data)),
				sstable.ReaderOptions{})
			assert.NoError(t, err, "Open reads the table with the reads that it needs")
			it := r.NewIter(sstable.IterOptions{})
			defer it.Close()
			_, err = scanAll(t.Context(), it)
			expect.ErrorIs(t, err, errRead, "the cursor returns the error of the read of its second block")
			_, err = scanBack(t.Context(), it)
			expect.ErrorIs(t, err, errRead, "the cursor returns the error of the read of its last block")
		})

		t.Run("returns nil after every entry", func(t *testing.T) {
			t.Parallel()
			it := openTable(t, writeTable(t, settings[3].opts, goldenTable()), sstable.ReaderOptions{}).
				NewIter(sstable.IterOptions{})
			defer it.Close()
			got, err := scanAll(t.Context(), it)
			expect.NoError(t, err, "the cursor reads the table")
			expect.Length(t, got, goldenEntries, "the cursor yields every entry")
		})
	})

	t.Run("Close", func(t *testing.T) {
		t.Parallel()

		t.Run("returns nil for a cursor without an error", func(t *testing.T) {
			t.Parallel()
			it := openTable(t, writeTable(t, settings[0].opts, goldenTable()), sstable.ReaderOptions{}).
				NewIter(sstable.IterOptions{})
			assert.True(t, it.First(t.Context()), "First finds an entry")
			assert.NoError(t, it.Close(), "Close returns the cursor to the pool")
		})

		t.Run("returns the error that exhausted the cursor", func(t *testing.T) {
			t.Parallel()
			data := writeTable(t, settings[0].opts, goldenTable())
			data[len(data)/3] ^= 1
			it := openTable(t, data, sstable.ReaderOptions{}).NewIter(sstable.IterOptions{})
			_, err := scanAll(t.Context(), it)
			assert.ErrorIs(t, err, sstable.ErrCorrupt, "the cursor refuses the changed block")
			assert.ErrorIs(t, it.Close(), sstable.ErrCorrupt, "Close returns the error of the cursor")
		})

		t.Run("returns nil for a second Close", func(t *testing.T) {
			t.Parallel()
			data := writeTable(t, settings[0].opts, goldenTable())
			data[len(data)/3] ^= 1
			it := openTable(t, data, sstable.ReaderOptions{}).NewIter(sstable.IterOptions{})
			_, err := scanAll(t.Context(), it)
			assert.ErrorIs(t, err, sstable.ErrCorrupt, "the cursor refuses the changed block")
			assert.ErrorIs(t, it.Close(), sstable.ErrCorrupt, "the first Close returns the error of the cursor")
			assert.NoError(t, it.Close(), "the second Close finds no cursor to return")
		})

		t.Run("drops the bytes of its reads ahead", func(t *testing.T) {
			t.Parallel()
			// The cursor reads without a cache and without compression, so the
			// value of its entry is a part of the bytes of its read ahead.
			it := openTable(t, writeTable(t, settings[0].opts, goldenTable()), sstable.ReaderOptions{}).
				NewIter(sstable.IterOptions{NoFill: true, ReadAhead: aheadBytes})
			assert.True(t, it.First(t.Context()), "First reads the first bytes ahead")
			value := it.Value()
			assert.NotEmpty(t, value, "the first entry has a value")
			bytesAhead := weak.Make(&value[0])
			assert.NoError(t, it.Close(), "Close returns the cursor to the pool")
			runtime.GC()
			assert.Nil(t, bytesAhead.Value(), "Close drops the bytes of the reads ahead")
		})

		t.Run("returns the cursor to the pool once for a second Close", func(t *testing.T) {
			t.Parallel()
			r := openTable(t, writeTable(t, settings[0].opts, goldenTable()), sstable.ReaderOptions{})
			it := r.NewIter(sstable.IterOptions{})
			assert.NoError(t, it.Close(), "the first Close returns the cursor to the pool")
			assert.NoError(t, it.Close(), "the second Close returns nil")
			first, second := r.NewIter(sstable.IterOptions{}), r.NewIter(sstable.IterOptions{})
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
	it := r.NewIter(sstable.IterOptions{})
	defer it.Close()

	var ok bool
	expect.MaxAllocs(t, func() { ok = it.First(t.Context()) }, 0, "First does not allocate")
	expect.True(t, ok, "the test measures a cursor on an entry")
	expect.MaxAllocs(t, func() { ok = it.Last(t.Context()) }, 0, "Last does not allocate")
	expect.True(t, ok, "the test measures a cursor on the last entry")
	expect.MaxAllocs(t, func() { ok = it.SeekGE(t.Context(), goldenKeys[30], 1) }, 0, "SeekGE does not allocate")
	expect.True(t, ok, "the test measures a seek to an entry")
	expect.MaxAllocs(t, func() { ok = it.SeekLT(t.Context(), goldenKeys[30], 1) }, 0, "SeekLT does not allocate")
	expect.True(t, ok, "the test measures a seek back to an entry")
	expect.MaxAllocs(t, func() {
		if !it.Next(t.Context()) {
			it.First(t.Context())
		}
	}, 0, "Next does not allocate within a block or across blocks")
	expect.MaxAllocs(t, func() {
		if !it.Prev(t.Context()) {
			it.Last(t.Context())
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
	bad := openTable(t, l.data, sstable.ReaderOptions{}).NewIter(sstable.IterOptions{})
	defer bad.Close()
	expect.MaxAllocs(t, func() {
		ok := bad.First(t.Context())
		for ok {
			ok = bad.Next(t.Context())
		}
	}, 0, "a cursor does not allocate the length that the preamble of a corrupt block states")
	expect.ErrorIs(t, bad.Err(), sstable.ErrCorrupt, "the test measures a cursor that refuses the second block")

	// The cache does not keep a block, so a cursor without NoFill would copy
	// every block that it reads for the cache.
	cold := openTable(t, writeTable(t, settings[3].opts, goldenTable()),
		sstable.ReaderOptions{Cache: emptyCache{}, CacheID: 1}).NewIter(sstable.IterOptions{NoFill: true})
	defer cold.Close()
	n := 0
	expect.MaxAllocs(t, func() {
		n = 0
		for ok := cold.First(t.Context()); ok; ok = cold.Next(t.Context()) {
			n++
		}
	}, 0, "a scan under NoFill does not copy a block")
	expect.Equal(t, n, goldenEntries, "the test measures a scan of every entry")

	// The first scan grows the buffer of the reads ahead, and the scans that
	// MaxAllocs counts reuse it.
	ahead := openTable(t, writeTable(t, settings[3].opts, goldenTable()), sstable.ReaderOptions{}).
		NewIter(sstable.IterOptions{NoFill: true, ReadAhead: aheadBytes})
	defer ahead.Close()
	_, err = scanAll(t.Context(), ahead)
	assert.NoError(t, err, "the first scan reads the table")
	expect.MaxAllocs(t, func() {
		n = 0
		for ok := ahead.First(t.Context()); ok; ok = ahead.Next(t.Context()) {
			n++
		}
	}, 0, "a scan with ReadAhead does not allocate once the buffer of its reads has grown")
	expect.Equal(t, n, goldenEntries, "the test measures a scan of every entry with ReadAhead")

	// The cache keeps four blocks, so a scan adds every block that it reads
	// and the cache evicts the earlier ones. Each block of the scan takes a
	// buffer that an evicted block returned to the pool. The cache allocates
	// an entry for each block. indexAllocs bounds the allocations of the new
	// indexes that it builds when the tombs of the evicted blocks fill its 16
	// slots.
	blocks := uint64(len(newLayout(t, settings[3].opts, goldenTable()).blocks))
	filling := openTable(t, writeTable(t, settings[3].opts, goldenTable()),
		sstable.ReaderOptions{Cache: lendingCache(t, 4), CacheID: 1}).NewIter(sstable.IterOptions{})
	defer filling.Close()
	expect.MaxAllocs(t, func() {
		n = 0
		for ok := filling.First(t.Context()); ok; ok = filling.Next(t.Context()) {
			n++
		}
	}, blocks+indexAllocs, "a scan through a lending cache allocates no block")
	expect.Equal(t, n, goldenEntries, "the test measures a scan of every entry through a lending cache")
}

// BenchmarkIter reports the cost of the moves of a cursor, and fails when a
// move allocates.
func BenchmarkIter(b *testing.B) {
	data := writeTable(b, settings[3].opts, goldenTable())
	r := openTable(b, data, sstable.ReaderOptions{})

	b.Run("Next", func(b *testing.B) {
		for _, tt := range []struct {
			name  string
			cache sstable.BlockCache
			opts  sstable.IterOptions
		}{
			{name: "without a cache"},
			{
				name:  "under NoFill through an empty cache",
				cache: emptyCache{}, opts: sstable.IterOptions{NoFill: true},
			},
			{name: "with ReadAhead without a cache", opts: sstable.IterOptions{ReadAhead: aheadBytes}},
			{name: "through a lending cache of four blocks", cache: lendingCache(b, 4)},
		} {
			b.Run(tt.name, func(b *testing.B) {
				it := openTable(b, data, sstable.ReaderOptions{Cache: tt.cache, CacheID: 1}).NewIter(tt.opts)
				defer it.Close()
				assert.True(b, it.First(b.Context()), "First finds an entry")
				n := 0

				c := bench.Start(b).MaxAllocs(0)
				defer c.End()

				for c.Loop() {
					if !it.Next(b.Context()) {
						it.First(b.Context())
					}
					n += len(it.Key())
				}

				assert.NotEqual(b, n, 0, "the benchmark measures moves to entries")
			})
		}
	})

	b.Run("Prev", func(b *testing.B) {
		it := r.NewIter(sstable.IterOptions{})
		defer it.Close()
		assert.True(b, it.Last(b.Context()), "Last finds an entry")
		n := 0

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			if !it.Prev(b.Context()) {
				it.Last(b.Context())
			}
			n += len(it.Key())
		}

		assert.NotEqual(b, n, 0, "the benchmark measures moves to entries")
	})

	b.Run("Last", func(b *testing.B) {
		it := r.NewIter(sstable.IterOptions{})
		defer it.Close()
		var ok bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			ok = it.Last(b.Context())
		}

		assert.True(b, ok, "the benchmark measures moves to the last entry")
	})

	b.Run("SeekGE", func(b *testing.B) {
		it := r.NewIter(sstable.IterOptions{})
		defer it.Close()
		var ok bool
		i := 0

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			ok = it.SeekGE(b.Context(), goldenKeys[i%len(goldenKeys)], 1)
			i++
		}

		assert.True(b, ok, "the benchmark measures seeks to entries")
	})

	b.Run("SeekLT", func(b *testing.B) {
		it := r.NewIter(sstable.IterOptions{})
		defer it.Close()
		var ok bool
		i := 0

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			// The keys after the first have an entry before them.
			ok = it.SeekLT(b.Context(), goldenKeys[1+i%(len(goldenKeys)-1)], 1)
			i++
		}

		assert.True(b, ok, "the benchmark measures seeks back to entries")
	})
}

// iterSteps writes drawn entries into a table of a drawn layout, and moves
// one cursor of the table through a machine of its six moves. The model of
// the cursor is a place in the entries. A move off either end leaves the
// cursor on no entry, and Next and Prev report false there.
//
// The table has partitions when the case draws a PartitionSize. A size of 1
// ends an index partition at every data block. The cursor reads through a
// cache when the case draws one, under NoFill when the case draws it, and
// with the ReadAhead that the case draws. The cache is a mapCache or a
// lending cache with room for four blocks, so the lending cache evicts the
// blocks and the partitions that the cursor keeps. Each move passes a
// context with the name of the move, and a mapCache must receive that
// context. A cursor under NoFill must not add a block to either cache.
func iterSteps(c *prop.Case) {
	es := c.Draw(entries, "entries")
	opts := sstable.WriterOptions{
		BlockSize:       c.Draw(prop.Integer(1, 512), "block size"),
		RestartInterval: c.Draw(prop.Integer(1, 8), "restart interval"),
		Compression:     c.Draw(prop.SampledFrom(sstable.NoCompression, sstable.SnappyCompression), "compression"),
		FilterBits:      c.Draw(prop.SampledFrom(0, 8), "filter bits"),
	}
	// The draws of the partitions and the lending cache follow the draws
	// that the stored cases record.
	cache, lending := &mapCache{}, lendingCache(c, 4)
	readerOpts := sstable.ReaderOptions{CacheID: 1}
	cached := c.Draw(prop.Boolean(), "cache")
	noFill := c.Draw(prop.Boolean(), "no fill")
	readAhead := c.Draw(prop.SampledFrom(0, aheadBytes, 64*aheadBytes), "read ahead")
	opts.PartitionSize = c.Draw(prop.SampledFrom(0, 1, 32, partitionBytes, 1024), "partition size")
	if cached {
		readerOpts.Cache = cache
		if c.Draw(prop.Boolean(), "lending") {
			readerOpts.Cache = lending
		}
	}
	it := openTable(c, writeTable(c, opts, es), readerOpts).
		NewIter(sstable.IterOptions{NoFill: noFill, ReadAhead: readAhead})
	defer it.Close()

	at, on, ok := 0, false, false
	probe := func(c *prop.Case, _ struct{}) any {
		return entry{key: c.Draw(prop.SampledFrom(keys...), "key"), seq: c.Draw(prop.Integer[uint64](0, 6), "seq")}
	}

	// move is the name of the last move, and calls the calls of the cache
	// before it. under returns the context of the move of the name.
	var move string
	calls := 0
	under := func(name string) context.Context {
		move, calls = name, cache.calls

		return context.WithValue(c.Context(), callKey{}, name)
	}

	stateful.Steps(c, stateful.Machine[struct{}]{
		Actions: []stateful.Action[struct{}]{
			{
				Name: moveFirst,
				Run: func(*prop.Case, int, any) {
					ok = it.First(under(moveFirst))
					at, on = 0, len(es) != 0
				},
			},
			{
				Name: moveLast,
				Run: func(*prop.Case, int, any) {
					ok = it.Last(under(moveLast))
					at, on = len(es)-1, len(es) != 0
				},
			},
			{
				Name:  moveSeekGE,
				Input: probe,
				Run: func(_ *prop.Case, _ int, in any) {
					p := in.(entry)
					ok = it.SeekGE(under(moveSeekGE), p.key, p.seq)
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
					ok = it.SeekLT(under(moveSeekLT), p.key, p.seq)
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
					ok = it.Next(under(moveNext))
					at++
					on = on && at < len(es)
				},
			},
			{
				Name:   movePrev,
				Weight: 3,
				Run: func(*prop.Case, int, any) {
					ok = it.Prev(under(movePrev))
					at--
					on = on && at >= 0
				},
			},
		},
		Invariant: func(c *prop.Case, _ struct{}) {
			assert.NoError(c, it.Err(), "the cursor reads the table")
			if cache.calls != calls {
				assert.Equal(c, cache.call, any(move), "the cache receives the context of the move")
			}
			if noFill {
				assert.Equal(c, cache.adds, 0, "a cursor under NoFill does not add a block to the cache")
				assert.Equal(c, lending.Len(), 0, "a cursor under NoFill does not add a block to the lending cache")
			}
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

	return openTable(tb, l.data, sstable.ReaderOptions{}).NewIter(sstable.IterOptions{}), key, seq
}

// exhaustedIter returns a cursor that ErrCorrupt exhausted at the second
// block of a table of oneEntry. The cursor reads the first block of the
// table without an error.
func exhaustedIter(tb assert.TB) *sstable.Iter {
	tb.Helper()

	l := newLayout(tb, oneEntry, goldenTable())
	l.data[l.blocks[1].off] ^= 1
	it := openTable(tb, l.data, sstable.ReaderOptions{}).NewIter(sstable.IterOptions{})
	_, err := scanAll(assert.Context(tb), it)
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

// aheadReads returns the number of reads that a scan of the data blocks of l
// makes with a ReadAhead of n bytes, from the first block forward, or from
// the last block back when back is true. A block outside the bytes of the
// last read takes a read of n bytes from its start, or of n bytes up to its
// end when it is before those bytes, within the data blocks. A block of n
// bytes or more takes a read of its own.
func aheadReads(l *layout, n int, back bool) int {
	dataEnd := l.index.off
	if l.filter.n != 0 {
		dataEnd = l.filter.off
	}
	blocks := slices.Clone(l.blocks)
	if back {
		slices.Reverse(blocks)
	}
	reads, from, to := 0, 0, 0
	for _, b := range blocks {
		start, end := b.off, b.off+b.n+trailerLen
		if start >= from && end <= to {
			continue
		}
		reads++
		switch {
		case n <= end-start:
			// The block takes a read of its own, and the bytes of the last read
			// remain.
		case start < from:
			from, to = max(end-n, 0), end
		default:
			from, to = start, min(start+n, dataEnd)
		}
	}

	return reads
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
