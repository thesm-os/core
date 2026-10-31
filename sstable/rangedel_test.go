// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sstable_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"runtime"
	"slices"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/sstable"
)

// seekKeys generates the keys of the seeks of a cursor of fragments: the keys
// that the generators of fragments draw most often, and random bytes.
var seekKeys = prop.OneOf(prop.SampledFrom(keys...), prop.Bytes(prop.MaxSize(20)))

// fragmentLayouts are the layouts of the fragments of the cases of a cursor:
// a block of fragments in memory, partitions of one fragment each, and
// partitions of a few fragments. A fragment partition ends at the first
// fragment after PartitionSize bytes.
var fragmentLayouts = []struct {
	name string
	opts sstable.WriterOptions
}{
	{name: "in memory", opts: settings[0].opts},
	{
		name: "in partitions of one fragment",
		opts: sstable.WriterOptions{BlockSize: smallBlocks, RestartInterval: everyFourth, PartitionSize: 1},
	},
	{
		name: "in partitions of a few fragments",
		opts: sstable.WriterOptions{BlockSize: smallBlocks, RestartInterval: everyFourth, PartitionSize: 64},
	},
}

// partsOfOne are the options of a table whose fragment partitions have one
// fragment each.
var partsOfOne = fragmentLayouts[1].opts

func TestRangeDeleteIter(t *testing.T) {
	t.Parallel()

	t.Run("First", func(t *testing.T) {
		t.Parallel()

		for _, layout := range fragmentLayouts {
			t.Run("moves to the first position of fragments "+layout.name, func(t *testing.T) {
				t.Parallel()
				it := fragmentCursor(t, layout.opts, goldenDeletes)
				defer it.Close()
				assert.True(t, it.First(t.Context()), "First finds a fragment")
				assert.Equal(t, rangeDelete{start: it.Start(), end: it.End(), seq: it.Seq()}, goldenDeletes[0],
					"First moves to the first fragment at its highest sequence")
			})
		}

		t.Run("reads a fragment partition through the cache under FragmentBlock", func(t *testing.T) {
			t.Parallel()
			cache := &roleCache{Cache: lendingCache(t, 16)}
			r := openTable(t, writeTable(t, partsOfOne, nil, goldenDeletes...),
				sstable.ReaderOptions{Cache: cache, CacheID: 1})
			for range 2 {
				it := r.RangeDeletes()
				assert.True(t, it.First(t.Context()), "First reads the first partition")
				it.Close()
			}
			assert.Length(t, cache.adds, 1, "the first First adds the partition that it read")
			expect.Equal(t, cache.adds[0].role, sstable.FragmentBlock,
				"the cache receives the partition under FragmentBlock")
			part := lookup{offset: cache.adds[0].offset, role: sstable.FragmentBlock}
			expect.Equal(t, cache.lends, []lookup{part, part}, "each First looks the partition up under FragmentBlock")
		})

		t.Run("adds a fragment partition that it reads to a BlockCache", func(t *testing.T) {
			t.Parallel()
			cache := &mapCache{}
			r := openTable(t, writeTable(t, partsOfOne, nil, goldenDeletes...),
				sstable.ReaderOptions{Cache: cache, CacheID: 1})
			it := r.RangeDeletes()
			defer it.Close()
			assert.True(t, it.First(t.Context()), "First reads the first partition")
			assert.Equal(t, cache.adds, 1, "First adds the partition that it read")
		})

		t.Run("takes a fragment partition from a BlockCache", func(t *testing.T) {
			t.Parallel()
			// The partition in the bytes of the File fails its checksum after the
			// first First, so the second First reads the partition of the cache.
			l := newLayout(t, partsOfOne, nil, goldenDeletes...)
			r := openTable(t, l.data, sstable.ReaderOptions{Cache: &mapCache{}, CacheID: 1})
			first := r.RangeDeletes()
			assert.True(t, first.First(t.Context()), "First reads the first partition")
			first.Close()
			l.data[l.fragmentParts[0].off] ^= 0xff
			it := r.RangeDeletes()
			defer it.Close()
			assert.True(t, it.First(t.Context()), "First takes the partition from the cache")
			assert.Equal(t, rangeDelete{start: it.Start(), end: it.End(), seq: it.Seq()}, goldenDeletes[0],
				"First moves to the first fragment of the partition of the cache")
		})

		t.Run("adds a copy of a fragment partition to a BlockCache", func(t *testing.T) {
			t.Parallel()
			// The read of the last partition takes the buffers of the cursor that
			// read the first partition before.
			r := openTable(t, writeTable(t, partsOfOne, nil, goldenDeletes...),
				sstable.ReaderOptions{Cache: &mapCache{}, CacheID: 1})
			reader := r.RangeDeletes()
			assert.True(t, reader.First(t.Context()), "First adds the first partition to the cache")
			assert.True(t, reader.Last(t.Context()), "Last reads the last partition")
			reader.Close()
			it := r.RangeDeletes()
			defer it.Close()
			assert.True(t, it.First(t.Context()), "First takes the first partition from the cache")
			assert.Equal(t, rangeDelete{start: it.Start(), end: it.End(), seq: it.Seq()}, goldenDeletes[0],
				"the cache keeps the bytes of the first partition")
		})
	})

	t.Run("Last", func(t *testing.T) {
		t.Parallel()

		for _, layout := range fragmentLayouts {
			t.Run("moves to the last position of fragments "+layout.name, func(t *testing.T) {
				t.Parallel()
				it := fragmentCursor(t, layout.opts, goldenDeletes)
				defer it.Close()
				assert.True(t, it.Last(t.Context()), "Last finds a fragment")
				assert.Equal(t, rangeDelete{start: it.Start(), end: it.End(), seq: it.Seq()},
					goldenDeletes[len(goldenDeletes)-1], "Last moves to the last fragment at its lowest sequence")
			})
		}

		t.Run("returns the error of a context that ended before a partition of the cache", func(t *testing.T) {
			t.Parallel()
			r := openTable(t, writeTable(t, partsOfOne, nil, goldenDeletes...),
				sstable.ReaderOptions{Cache: &mapCache{}, CacheID: 1})
			warmFragments(t, r)
			it := r.RangeDeletes()
			defer it.Close()
			assert.True(t, it.First(t.Context()), "First takes the first partition from the cache")
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			assert.False(t, it.Last(ctx), "Last stops at the context before the last partition")
			assert.ErrorIs(t, it.Err(), context.Canceled, "Err returns the error of the context")
		})
	})

	t.Run("SeekGE", func(t *testing.T) {
		t.Parallel()

		t.Run("moves to the first position of the first fragment whose end is after key", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "SeekGE finds what a sorted model of the fragments finds", func(c *prop.Case) {
				dels := c.Draw(fragments, "fragments")
				it := fragmentCursor(c, c.Draw(prop.SampledFrom(fragmentLayouts...), "layout").opts, dels)
				defer it.Close()
				for range 10 {
					key := c.Draw(seekKeys, "key")
					i := slices.IndexFunc(dels, func(d rangeDelete) bool { return bytes.Compare(d.end, key) > 0 })
					if i < 0 {
						assert.False(c, it.SeekGE(c.Context(), key), "SeekGE finds no fragment that ends after key")

						continue
					}
					assert.True(c, it.SeekGE(c.Context(), key), "SeekGE finds a fragment that ends after key")
					assert.Equal(c, rangeDelete{start: it.Start(), end: it.End(), seq: it.Seq()}, dels[i],
						"SeekGE moves to the first position of the fragment")
					if i+1 == len(dels) {
						assert.False(c, it.Next(c.Context()),
							"Next of the last position after SeekGE moves off the end")

						continue
					}
					assert.True(c, it.Next(c.Context()), "Next after SeekGE moves to the position after it")
					assert.Equal(c, rangeDelete{start: it.Start(), end: it.End(), seq: it.Seq()}, dels[i+1],
						"Next after SeekGE moves to the next position")
				}
				assert.NoError(c, it.Err(), "the seeks read every partition that they need")
			})
		})

		t.Run("compares the ends with the key without its version", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "SeekGE finds what a sorted model of the fragments with versions finds", func(c *prop.Case) {
				dels := c.Draw(versionedFragments, "fragments")
				opts := c.Draw(prop.SampledFrom(fragmentLayouts...), "layout").opts
				opts.SuffixBytes = versionBytes
				it := openTable(c, writeTable(c, opts, nil, dels...),
					sstable.ReaderOptions{SuffixBytes: versionBytes}).RangeDeletes()
				defer it.Close()
				for range 10 {
					user := c.Draw(seekKeys, "key")
					version := c.Draw(prop.Integer[uint64](0, 9), "version")
					key := binary.BigEndian.AppendUint64(bytes.Clone(user), version)
					i := slices.IndexFunc(dels, func(d rangeDelete) bool { return bytes.Compare(d.end, user) > 0 })
					if i < 0 {
						assert.False(c, it.SeekGE(c.Context(), key), "SeekGE finds no fragment that ends after key")

						continue
					}
					assert.True(c, it.SeekGE(c.Context(), key), "SeekGE finds a fragment that ends after key")
					assert.Equal(c, rangeDelete{start: it.Start(), end: it.End(), seq: it.Seq()}, dels[i],
						"SeekGE moves to the first position of the fragment at its highest version")
				}
			})
		})

		t.Run("returns the error of a context that ended before the read of a partition", func(t *testing.T) {
			t.Parallel()
			it := fragmentCursor(t, partsOfOne, goldenDeletes)
			defer it.Close()
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			assert.False(t, it.SeekGE(ctx, []byte("key 017")), "SeekGE needs a partition and stops")
			assert.ErrorIs(t, it.Err(), context.Canceled, "Err returns the error of the context")
		})

		t.Run("reads the partition of the current position once", func(t *testing.T) {
			t.Parallel()
			data := writeTable(t, partsOfOne, nil, goldenDeletes...)
			f := &countingFile{data: data}
			r, err := sstable.Open(t.Context(), f, int64(len(data)), sstable.ReaderOptions{})
			assert.NoError(t, err, "Open reads the table")
			it := r.RangeDeletes()
			defer it.Close()
			assert.True(t, it.SeekGE(t.Context(), []byte("key 017")), "SeekGE reads the partition of the key")
			reads := f.reads
			assert.True(t, it.SeekGE(t.Context(), []byte("key 016")), "SeekGE finds a fragment of the same partition")
			assert.Equal(t, f.reads, reads, "the second SeekGE reads no partition")
		})

		t.Run("ignores its context for fragments in memory", func(t *testing.T) {
			t.Parallel()
			it := fragmentCursor(t, fragmentLayouts[0].opts, goldenDeletes)
			defer it.Close()
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			assert.True(t, it.SeekGE(ctx, []byte("key 017")), "SeekGE reads no partition")
			assert.NoError(t, it.Err(), "Err has no error")
		})
	})

	t.Run("SeekLT", func(t *testing.T) {
		t.Parallel()

		t.Run("moves to the last position of the last fragment whose start is before key", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "SeekLT finds what a sorted model of the fragments finds", func(c *prop.Case) {
				dels := c.Draw(fragments, "fragments")
				it := fragmentCursor(c, c.Draw(prop.SampledFrom(fragmentLayouts...), "layout").opts, dels)
				defer it.Close()
				for range 10 {
					key := c.Draw(seekKeys, "key")
					i := len(dels) - 1
					for i >= 0 && bytes.Compare(dels[i].start, key) >= 0 {
						i--
					}
					if i < 0 {
						assert.False(c, it.SeekLT(c.Context(), key), "SeekLT finds no fragment that starts before key")

						continue
					}
					assert.True(c, it.SeekLT(c.Context(), key), "SeekLT finds a fragment that starts before key")
					assert.Equal(c, rangeDelete{start: it.Start(), end: it.End(), seq: it.Seq()}, dels[i],
						"SeekLT moves to the last position of the fragment")
					if i == 0 {
						assert.False(c, it.Prev(c.Context()),
							"Prev of the first position after SeekLT moves off the start")

						continue
					}
					assert.True(c, it.Prev(c.Context()), "Prev after SeekLT moves to the position before it")
					assert.Equal(c, rangeDelete{start: it.Start(), end: it.End(), seq: it.Seq()}, dels[i-1],
						"Prev after SeekLT moves to the position before")
				}
				assert.NoError(c, it.Err(), "the seeks read every partition that they need")
			})
		})

		t.Run("compares the starts with the key without its version", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "SeekLT finds what a sorted model of the fragments with versions finds", func(c *prop.Case) {
				dels := c.Draw(versionedFragments, "fragments")
				opts := c.Draw(prop.SampledFrom(fragmentLayouts...), "layout").opts
				opts.SuffixBytes = versionBytes
				it := openTable(c, writeTable(c, opts, nil, dels...),
					sstable.ReaderOptions{SuffixBytes: versionBytes}).RangeDeletes()
				defer it.Close()
				for range 10 {
					user := c.Draw(seekKeys, "key")
					version := c.Draw(prop.Integer[uint64](0, 9), "version")
					key := binary.BigEndian.AppendUint64(bytes.Clone(user), version)
					i := len(dels) - 1
					for i >= 0 && bytes.Compare(dels[i].start[:len(dels[i].start)-versionBytes], user) >= 0 {
						i--
					}
					if i < 0 {
						assert.False(c, it.SeekLT(c.Context(), key), "SeekLT finds no fragment that starts before key")

						continue
					}
					assert.True(c, it.SeekLT(c.Context(), key), "SeekLT finds a fragment that starts before key")
					assert.Equal(c, rangeDelete{start: it.Start(), end: it.End(), seq: it.Seq()}, dels[i],
						"SeekLT moves to the last position of the fragment at its lowest version")
				}
			})
		})

		t.Run("returns the error of a context that ended before the read of a partition", func(t *testing.T) {
			t.Parallel()
			it := fragmentCursor(t, partsOfOne, goldenDeletes)
			defer it.Close()
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			assert.False(t, it.SeekLT(ctx, []byte("key 017")), "SeekLT needs a partition and stops")
			assert.ErrorIs(t, it.Err(), context.Canceled, "Err returns the error of the context")
		})

		t.Run("returns the error of a context that ended before the read of the partition before", func(t *testing.T) {
			t.Parallel()
			// The fragment from key 020 up to key 030 is alone in its partition.
			// A seek to key 020 reads that partition, and then the partition of
			// the fragment before it.
			it := fragmentCursor(t, partsOfOne, goldenDeletes)
			defer it.Close()
			key := []byte("key 020")
			assert.True(t, it.SeekGE(t.Context(), key), "SeekGE reads the partition of key")
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			assert.False(t, it.SeekLT(ctx, key), "SeekLT needs the partition before and stops")
			assert.ErrorIs(t, it.Err(), context.Canceled, "Err returns the error of the context")
		})
	})

	t.Run("Next", func(t *testing.T) {
		t.Parallel()

		t.Run("moves to each position in the order of the fragments", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Next yields the fragments in their order", func(c *prop.Case) {
				dels := c.Draw(fragments, "fragments")
				it := fragmentCursor(c, c.Draw(prop.SampledFrom(fragmentLayouts...), "layout").opts, dels)
				defer it.Close()
				var got []rangeDelete
				for ok := it.First(c.Context()); ok && len(got) <= len(dels); ok = it.Next(c.Context()) {
					got = append(got,
						rangeDelete{start: bytes.Clone(it.Start()), end: bytes.Clone(it.End()), seq: it.Seq()})
				}
				assert.NoError(c, it.Err(), "Next reads every partition")
				assert.Equal(c, got, dels, "the cursor yields each fragment once", assert.EquateEmpty())
			})
		})

		t.Run("reports false for a cursor on no position", func(t *testing.T) {
			t.Parallel()
			for _, layout := range fragmentLayouts {
				it := fragmentCursor(t, layout.opts, goldenDeletes)
				expect.False(t, it.Next(t.Context()), "a new cursor has no next position "+layout.name)
				assert.True(t, it.Last(t.Context()), "Last finds a fragment")
				expect.False(t, it.Next(t.Context()), "Next of the last position moves off the end "+layout.name)
				expect.False(t, it.Next(t.Context()), "Next of a cursor off the end reports false "+layout.name)
				expect.False(t, it.Prev(t.Context()), "Prev of a cursor off the end reports false "+layout.name)
				it.Close()
			}
		})

		t.Run("returns the error of a context that ended before the read of the next partition", func(t *testing.T) {
			t.Parallel()
			it := fragmentCursor(t, partsOfOne, goldenDeletes)
			defer it.Close()
			assert.True(t, it.First(t.Context()), "First reads the first partition")
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			assert.False(t, it.Next(ctx), "Next needs the next partition and stops")
			assert.ErrorIs(t, it.Err(), context.Canceled, "Err returns the error of the context")
			assert.Nil(t, it.Start(), "the cursor is on no position")
		})
	})

	t.Run("Prev", func(t *testing.T) {
		t.Parallel()

		t.Run("moves to each position in the reverse order of the fragments", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Prev yields the fragments in their reverse order", func(c *prop.Case) {
				dels := c.Draw(fragments, "fragments")
				it := fragmentCursor(c, c.Draw(prop.SampledFrom(fragmentLayouts...), "layout").opts, dels)
				defer it.Close()
				var got []rangeDelete
				for ok := it.Last(c.Context()); ok && len(got) <= len(dels); ok = it.Prev(c.Context()) {
					got = append(got,
						rangeDelete{start: bytes.Clone(it.Start()), end: bytes.Clone(it.End()), seq: it.Seq()})
				}
				assert.NoError(c, it.Err(), "Prev reads every partition")
				slices.Reverse(got)
				assert.Equal(c, got, dels, "the cursor yields each fragment once", assert.EquateEmpty())
			})
		})

		t.Run("reports false for a cursor on no position", func(t *testing.T) {
			t.Parallel()
			for _, layout := range fragmentLayouts {
				it := fragmentCursor(t, layout.opts, goldenDeletes)
				expect.False(t, it.Prev(t.Context()), "a new cursor has no position before "+layout.name)
				assert.True(t, it.First(t.Context()), "First finds a fragment")
				expect.False(t, it.Prev(t.Context()), "Prev of the first position moves off the start "+layout.name)
				expect.False(t, it.Prev(t.Context()), "Prev of a cursor off the start reports false "+layout.name)
				expect.False(t, it.Next(t.Context()), "Next of a cursor off the start reports false "+layout.name)
				it.Close()
			}
		})

		t.Run("returns the error of a context that ended before the read of the partition before", func(t *testing.T) {
			t.Parallel()
			it := fragmentCursor(t, partsOfOne, goldenDeletes)
			defer it.Close()
			assert.True(t, it.Last(t.Context()), "Last reads the last partition")
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			assert.False(t, it.Prev(ctx), "Prev needs the partition before and stops")
			assert.ErrorIs(t, it.Err(), context.Canceled, "Err returns the error of the context")
		})
	})

	t.Run("Start", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a start with a capacity of its length", func(t *testing.T) {
			t.Parallel()
			it := fragmentCursor(t, settings[0].opts, goldenDeletes)
			defer it.Close()
			assert.True(t, it.First(t.Context()), "First finds a fragment")
			assert.Equal(t, cap(it.Start()), len(it.Start()), "an append to the start does not write into the block")
		})

		t.Run("returns nil for a cursor on no position", func(t *testing.T) {
			t.Parallel()
			it := fragmentCursor(t, settings[0].opts, goldenDeletes)
			defer it.Close()
			assert.Nil(t, it.Start(), "a new cursor has no start")
		})

		t.Run("returns nil after a move that stops at its context", func(t *testing.T) {
			t.Parallel()
			it := fragmentCursor(t, partsOfOne, goldenDeletes)
			defer it.Close()
			assert.True(t, it.Last(t.Context()), "Last moves to the last partition")
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			assert.False(t, it.First(ctx), "First stops at the context before the first partition")
			assert.Nil(t, it.Start(), "the cursor is on no position")
		})
	})

	t.Run("End", func(t *testing.T) {
		t.Parallel()

		t.Run("returns an end with a capacity of its length", func(t *testing.T) {
			t.Parallel()
			it := fragmentCursor(t, settings[0].opts, goldenDeletes)
			defer it.Close()
			assert.True(t, it.First(t.Context()), "First finds a fragment")
			assert.Equal(t, cap(it.End()), len(it.End()), "an append to the end does not write into the block")
		})

		t.Run("returns nil for a cursor on no position", func(t *testing.T) {
			t.Parallel()
			it := fragmentCursor(t, settings[0].opts, goldenDeletes)
			defer it.Close()
			assert.True(t, it.Last(t.Context()), "Last finds a fragment")
			assert.False(t, it.Next(t.Context()), "Next of the last position moves off the end")
			assert.Nil(t, it.End(), "a cursor off the end has no end")
		})
	})

	t.Run("Seq", func(t *testing.T) {
		t.Parallel()

		t.Run("returns 0 for a cursor on no position", func(t *testing.T) {
			t.Parallel()
			it := fragmentCursor(t, settings[0].opts, []rangeDelete{{start: []byte("a"), end: []byte("b"), seq: 7}})
			defer it.Close()
			assert.Equal(t, it.Seq(), uint64(0), "a new cursor has no sequence")
		})
	})

	t.Run("Err", func(t *testing.T) {
		t.Parallel()

		t.Run("returns ErrCorrupt for a fragment partition that fails its checksum", func(t *testing.T) {
			t.Parallel()
			l := newLayout(t, partsOfOne, nil, goldenDeletes...)
			last := l.fragmentParts[len(l.fragmentParts)-1]
			l.data[last.off] ^= 1
			it := openTable(t, l.data, sstable.ReaderOptions{}).RangeDeletes()
			defer it.Close()
			assert.False(t, it.Last(t.Context()), "Last reads the damaged partition")
			assert.ErrorIs(t, it.Err(), sstable.ErrCorrupt, "Err returns ErrCorrupt")
		})

		t.Run("returns ErrCorrupt for a partition that starts before the end before it", func(t *testing.T) {
			t.Parallel()
			// The third partition has the fragment from key 020 up to key 030
			// alone. Its start moves from key 020, the end of the partition before
			// it, to a key before it.
			l := newLayout(t, partsOfOne, nil, goldenDeletes...)
			part := l.fragmentParts[2]
			l.data[part.off+3+len("key 02")]--
			l.seal()
			it := openTable(t, l.data, sstable.ReaderOptions{}).RangeDeletes()
			defer it.Close()
			assert.False(t, it.SeekGE(t.Context(), []byte("key 025")), "SeekGE reads the changed partition")
			assert.ErrorIs(t, it.Err(), sstable.ErrCorrupt, "Err returns ErrCorrupt")
		})

		t.Run("returns ErrCorrupt for a partition after a top-level entry that has no place", func(t *testing.T) {
			t.Parallel()
			// The second entry of the top-level index has no place. The third
			// entry states the count 1 of its partition alone, the count that a
			// cursor would take for a partition without a partition before it.
			l := newLayout(t, partsOfOne, nil, goldenDeletes...)
			top := l.data[l.fragments.off : l.fragments.off+l.fragments.n]
			offsets := restarts(top)
			_, before := restartEntry(top[offsets[1]:])
			for i := range before {
				before[i] = 0xff
			}
			_, value := restartEntry(top[offsets[2]:])
			_, n1 := binary.Uvarint(value)
			_, n2 := binary.Uvarint(value[n1:])
			value[n1+n2] = 1
			l.seal()
			it := openTable(t, l.data, sstable.ReaderOptions{}).RangeDeletes()
			defer it.Close()
			assert.False(t, it.SeekGE(t.Context(), []byte("key 025")), "SeekGE reads the third partition")
			assert.ErrorIs(t, it.Err(), sstable.ErrCorrupt, "Err returns ErrCorrupt")
		})

		// Each case changes the second fragment partition, the fragment from key
		// 015 up to key 020 at the sequences 9 and 7, and seals the table.
		tests := []struct {
			name       string
			giveChange func(l *layout, part place)
		}{
			{
				name: "returns ErrCorrupt for a partition whose last end differs from its top-level entry",
				giveChange: func(l *layout, part place) {
					// Both entries end at key 021 in place of key 020, so they are
					// still one fragment.
					for _, off := range restarts(l.data[part.off : part.off+part.n]) {
						_, end := restartEntry(l.data[part.off+off:])
						end[len(end)-1]++
					}
				},
			},
			{
				name: "returns ErrCorrupt for a partition with fewer entries than its top-level entry states",
				giveChange: func(l *layout, part place) {
					// The count of the restart points of the partition is 2.
					binary.BigEndian.PutUint32(l.data[part.off+part.n-4:], 1)
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				l := newLayout(t, partsOfOne, nil, goldenDeletes...)
				assert.Length(t, l.fragmentParts, 4, "each fragment of the golden fragments has a partition")
				tt.giveChange(l, l.fragmentParts[1])
				l.seal()
				it := openTable(t, l.data, sstable.ReaderOptions{}).RangeDeletes()
				defer it.Close()
				assert.False(t, it.SeekGE(t.Context(), []byte("key 017")), "SeekGE reads the changed partition")
				assert.ErrorIs(t, it.Err(), sstable.ErrCorrupt, "Err returns ErrCorrupt")
			})
		}

		// Each case changes the second entry of the top-level index of the
		// fragments, the entry of the fragment from key 015 up to key 020, and
		// seals the table. Open reads the first and the last entry alone. A key
		// does not decode with a shared length of 1, because an entry at a
		// restart point does not share bytes with the entry before it.
		topChanges := []struct {
			name       string
			giveChange func(entry []byte)
			giveMove   func(ctx context.Context, it *sstable.RangeDeleteIter) bool
		}{
			{
				name:       "returns ErrCorrupt for a SeekGE whose search reads a top-level entry that does not decode",
				giveChange: func(entry []byte) { entry[0] = 1 },
				giveMove: func(ctx context.Context, it *sstable.RangeDeleteIter) bool {
					return it.SeekGE(ctx, []byte("key 017"))
				},
			},
			{
				name:       "returns ErrCorrupt for a SeekLT whose search reads a top-level entry that does not decode",
				giveChange: func(entry []byte) { entry[0] = 1 },
				giveMove: func(ctx context.Context, it *sstable.RangeDeleteIter) bool {
					return it.SeekLT(ctx, []byte("key 017"))
				},
			},
			{
				name:       "returns ErrCorrupt for a Next into a partition whose top-level entry does not decode",
				giveChange: func(entry []byte) { entry[0] = 1 },
				giveMove: func(ctx context.Context, it *sstable.RangeDeleteIter) bool {
					return it.First(ctx) && it.Next(ctx)
				},
			},
			{
				name:       "returns ErrCorrupt for a Prev into a partition after a top-level entry that does not decode",
				giveChange: func(entry []byte) { entry[0] = 1 },
				giveMove: func(ctx context.Context, it *sstable.RangeDeleteIter) bool {
					return it.Last(ctx) && it.Prev(ctx)
				},
			},
			{
				name: "returns ErrCorrupt for a Next into a partition whose top-level entry has no place",
				giveChange: func(entry []byte) {
					_, value := restartEntry(entry)
					for i := range value {
						value[i] = 0xff
					}
				},
				giveMove: func(ctx context.Context, it *sstable.RangeDeleteIter) bool {
					return it.First(ctx) && it.Next(ctx)
				},
			},
		}
		for _, tt := range topChanges {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				l := newLayout(t, partsOfOne, nil, goldenDeletes...)
				top := l.data[l.fragments.off : l.fragments.off+l.fragments.n]
				tt.giveChange(top[restarts(top)[1]:])
				l.seal()
				it := openTable(t, l.data, sstable.ReaderOptions{}).RangeDeletes()
				defer it.Close()
				assert.False(t, tt.giveMove(t.Context(), it), "the move reads the changed entry")
				assert.ErrorIs(t, it.Err(), sstable.ErrCorrupt, "Err returns ErrCorrupt")
			})
		}

		// A cursor does not check a partition that the cache returns. The cache
		// of each case contains every partition of the intact table under the
		// identity of the changed table, so the search of the top-level index
		// alone reads the changed entry.
		cachedSearches := []struct {
			name     string
			giveSeek func(ctx context.Context, it *sstable.RangeDeleteIter) bool
		}{
			{
				name: "returns ErrCorrupt for a SeekGE whose search reads a top-level entry that does not decode " +
					"before a partition of the cache",
				giveSeek: func(ctx context.Context, it *sstable.RangeDeleteIter) bool {
					return it.SeekGE(ctx, []byte("key 017"))
				},
			},
			{
				name: "returns ErrCorrupt for a SeekLT whose search reads a top-level entry that does not decode " +
					"before a partition of the cache",
				giveSeek: func(ctx context.Context, it *sstable.RangeDeleteIter) bool {
					return it.SeekLT(ctx, []byte("key 017"))
				},
			},
		}
		for _, tt := range cachedSearches {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				cache := &mapCache{}
				data := writeTable(t, partsOfOne, nil, goldenDeletes...)
				warmFragments(t, openTable(t, data, sstable.ReaderOptions{Cache: cache, CacheID: 1}))
				l := parseLayout(bytes.Clone(data))
				top := l.data[l.fragments.off : l.fragments.off+l.fragments.n]
				top[restarts(top)[1]] = 1
				l.seal()
				it := openTable(t, l.data, sstable.ReaderOptions{Cache: cache, CacheID: 1}).RangeDeletes()
				defer it.Close()
				assert.False(t, tt.giveSeek(t.Context(), it), "the seek reads the changed entry")
				assert.ErrorIs(t, it.Err(), sstable.ErrCorrupt, "Err returns ErrCorrupt")
			})
		}

		t.Run("returns ErrCorrupt for a Next into a partition whose top-level entry has no place before a block "+
			"of the cache at the offset 0", func(t *testing.T) {
			t.Parallel()
			// The cache contains the first data block of the table at the offset
			// 0, which the place of an entry that does not decode would give.
			l := newLayout(t, partsOfOne, goldenTable(), goldenDeletes...)
			top := l.data[l.fragments.off : l.fragments.off+l.fragments.n]
			_, value := restartEntry(top[restarts(top)[1]:])
			for i := range value {
				value[i] = 0xff
			}
			l.seal()
			r := openTable(t, l.data, sstable.ReaderOptions{Cache: &mapCache{}, CacheID: 1})
			entries := r.NewIter(sstable.IterOptions{})
			assert.True(t, entries.First(t.Context()), "First adds the first data block to the cache")
			assert.NoError(t, entries.Close(), "the cursor of the entries closes")
			it := r.RangeDeletes()
			defer it.Close()
			assert.True(t, it.First(t.Context()), "First reads the first partition")
			assert.False(t, it.Next(t.Context()), "Next reads the changed entry")
			assert.ErrorIs(t, it.Err(), sstable.ErrCorrupt, "Err returns ErrCorrupt")
		})

		t.Run("returns an error that wraps the error of the File", func(t *testing.T) {
			t.Parallel()
			data := writeTable(t, partsOfOne, nil, goldenDeletes...)
			f := &failingFile{data: data, ok: 1 << 30}
			r, err := sstable.Open(t.Context(), f, int64(len(data)), sstable.ReaderOptions{})
			assert.NoError(t, err, "Open reads the table")
			f.ok = f.reads
			it := r.RangeDeletes()
			defer it.Close()
			assert.False(t, it.Last(t.Context()), "Last reads the last partition")
			assert.ErrorIs(t, it.Err(), errRead, "Err returns the error of the File")
		})

		// Each case moves the cursor with a seek after a First that stopped at
		// its context.
		seeks := []struct {
			name     string
			giveSeek func(ctx context.Context, it *sstable.RangeDeleteIter) bool
		}{
			{
				name:     "clears the error at First",
				giveSeek: func(ctx context.Context, it *sstable.RangeDeleteIter) bool { return it.First(ctx) },
			},
			{
				name:     "clears the error at Last",
				giveSeek: func(ctx context.Context, it *sstable.RangeDeleteIter) bool { return it.Last(ctx) },
			},
			{
				name: "clears the error at SeekGE",
				giveSeek: func(ctx context.Context, it *sstable.RangeDeleteIter) bool {
					return it.SeekGE(ctx, []byte("key 017"))
				},
			},
			{
				name: "clears the error at SeekLT",
				giveSeek: func(ctx context.Context, it *sstable.RangeDeleteIter) bool {
					return it.SeekLT(ctx, []byte("key 017"))
				},
			},
		}
		for _, tt := range seeks {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				// The cache contains every partition, so no read of the File sets
				// the error of the cursor.
				r := openTable(t, writeTable(t, partsOfOne, nil, goldenDeletes...),
					sstable.ReaderOptions{Cache: &mapCache{}, CacheID: 1})
				warmFragments(t, r)
				it := r.RangeDeletes()
				defer it.Close()
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				assert.False(t, it.First(ctx), "First stops at the context")
				assert.True(t, tt.giveSeek(t.Context(), it), "the seek moves under a live context")
				assert.NoError(t, it.Err(), "the seek clears the error")
			})
		}
	})

	t.Run("Close", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the cursor to the pool once for a second Close", func(t *testing.T) {
			t.Parallel()
			r := openTable(t, writeTable(t, settings[0].opts, nil, goldenDeletes...), sstable.ReaderOptions{})
			it := r.RangeDeletes()
			it.Close()
			it.Close()
			first, second := r.RangeDeletes(), r.RangeDeletes()
			defer first.Close()
			defer second.Close()
			assert.NotEqual(t, first, second, "the pool hands out the closed cursor once", assert.ByIdentity())
		})

		t.Run("releases a lent partition once after a move that stops at its context", func(t *testing.T) {
			t.Parallel()
			// A second release of the first partition would return its Block to
			// the pool while the cache contains it. The read of the last
			// partition would then take that Block, and the cache would lend the
			// bytes of the last partition for the first.
			r := openTable(t, writeTable(t, partsOfOne, nil, goldenDeletes...),
				sstable.ReaderOptions{Cache: lendingCache(t, 16), CacheID: 1})
			stopped := r.RangeDeletes()
			assert.True(t, stopped.First(t.Context()), "First adds the first partition to the cache")
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			assert.False(t, stopped.Last(ctx), "Last stops at the context")
			stopped.Close()
			last := r.RangeDeletes()
			assert.True(t, last.Last(t.Context()), "Last reads the last partition")
			last.Close()
			it := r.RangeDeletes()
			defer it.Close()
			assert.True(t, it.First(t.Context()), "First takes the first partition from the cache")
			assert.Equal(t, rangeDelete{start: it.Start(), end: it.End(), seq: it.Seq()}, goldenDeletes[0],
				"the cache lends the bytes of the first partition")
		})
	})
}

// TestRangeDeleteIterAllocs checks the allocation contract of the moves of a
// cursor of fragments. MaxAllocs counts the allocations of the whole process,
// so the test does not run in parallel.
func TestRangeDeleteIterAllocs(t *testing.T) {
	key := []byte("key 017")
	moves := []struct {
		name string
		move func(ctx context.Context, it *sstable.RangeDeleteIter) bool
	}{
		{
			name: "SeekGE",
			move: func(ctx context.Context, it *sstable.RangeDeleteIter) bool { return it.SeekGE(ctx, key) },
		},
		{
			name: "SeekLT",
			move: func(ctx context.Context, it *sstable.RangeDeleteIter) bool { return it.SeekLT(ctx, key) },
		},
		{
			name: "Next",
			move: func(ctx context.Context, it *sstable.RangeDeleteIter) bool { return it.First(ctx) && it.Next(ctx) },
		},
		{
			name: "Prev",
			move: func(ctx context.Context, it *sstable.RangeDeleteIter) bool { return it.Last(ctx) && it.Prev(ctx) },
		},
	}
	for _, m := range moves {
		t.Run(m.name, func(t *testing.T) {
			for _, layout := range fragmentLayouts {
				t.Run(layout.name, func(t *testing.T) {
					it := fragmentCursor(t, layout.opts, goldenDeletes)
					defer it.Close()
					ok := false
					expect.MaxAllocs(t, func() { ok = m.move(t.Context(), it) }, 0,
						m.name+" does not allocate once the cursor has grown")
					expect.True(t, ok, "the test measures moves to fragments")
				})
			}
		})
	}

	t.Run("First", func(t *testing.T) {
		// A lending cache of one block makes each First of the first and of
		// the last partition read and lend one partition, whose Block the pool
		// lends again.
		r := openTable(t, writeTable(t, partsOfOne, nil, goldenDeletes...),
			sstable.ReaderOptions{Cache: lendingCache(t, 1), CacheID: 1})
		runtime.GC()
		runtime.GC()
		var ok bool
		expect.MaxAllocs(t, func() {
			it := r.RangeDeletes()
			ok = it.First(t.Context()) && it.Last(t.Context())
			it.Close()
		}, 2, "a miss of a lending cache allocates the entry of the cache")
		expect.True(t, ok, "the test measures moves to fragments")
	})
}

// BenchmarkRangeDeleteIter reports the cost of the seeks of a cursor of
// fragments, and fails when they allocate.
func BenchmarkRangeDeleteIter(b *testing.B) {
	key := []byte("key 017")
	seeks := []struct {
		name string
		seek func(ctx context.Context, it *sstable.RangeDeleteIter) bool
	}{
		{
			name: "SeekGE",
			seek: func(ctx context.Context, it *sstable.RangeDeleteIter) bool { return it.SeekGE(ctx, key) },
		},
		{
			name: "SeekLT",
			seek: func(ctx context.Context, it *sstable.RangeDeleteIter) bool { return it.SeekLT(ctx, key) },
		},
	}
	for _, s := range seeks {
		b.Run(s.name, func(b *testing.B) {
			for _, layout := range fragmentLayouts {
				b.Run(layout.name, func(b *testing.B) {
					it := fragmentCursor(b, layout.opts, goldenDeletes)
					defer it.Close()
					ok := false

					c := bench.Start(b).MaxAllocs(0)
					defer c.End()

					for c.Loop() {
						ok = s.seek(b.Context(), it)
					}

					assert.True(b, ok, "the benchmark measures a seek to a fragment")
				})
			}
		})
	}
}

// warmFragments reads every fragment partition of r through a cursor under
// the context of tb, so the cache of r contains each of them.
func warmFragments(tb assert.TB, r *sstable.Reader) {
	tb.Helper()

	ctx := assert.Context(tb)
	it := r.RangeDeletes()
	defer it.Close()
	ok := it.First(ctx)
	for ok {
		ok = it.Next(ctx)
	}
	assert.NoError(tb, it.Err(), "the cursor reads every fragment partition")
}

// fragmentCursor writes a table of the fragments dels without entries under
// opts, opens it and returns a cursor over its fragments, which the caller
// closes.
func fragmentCursor(tb assert.TB, opts sstable.WriterOptions, dels []rangeDelete) *sstable.RangeDeleteIter {
	tb.Helper()

	data := writeTable(tb, opts, nil, dels...)

	return openTable(tb, data, sstable.ReaderOptions{}).RangeDeletes()
}
