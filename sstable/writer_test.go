// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sstable_test

import (
	"bytes"
	"cmp"
	"context"
	"encoding/binary"
	"errors"
	"flag"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/golden"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.thesmos.sh/core/compress/snappy"
	"go.thesmos.sh/core/filter"
	"go.thesmos.sh/core/sstable"
)

// warmEntries is the number of entries that the allocation test of Add adds
// before it counts. They grow the index and the hashes so far that a growth
// during a count leaves room for every call of the next count. The
// allocation test of AddRangeDelete adds as many fragments.
const warmEntries = 4096

// benchFragments is the number of fragments of a table of the benchmark of
// AddRangeDelete. The benchmark starts a new table after them, so the block
// of fragments does not grow without end.
const benchFragments = 4096

// childEnv is set in the environment of the child process in which a case
// runs alone.
const childEnv = "CORE_TEST_CHILD"

// The machine of a Writer.
const (
	// writeContract is the contract of writeSteps.
	writeContract = "a Writer must write the bytes of a new Writer of the entries that Add accepted since Reset"

	// The names of the actions of the machine: an Add of the next entry, an
	// Add of an entry at or before the last entry, an AddRangeDelete of the
	// next fragment and of a fragment at or before the last fragment, Close
	// and Reset.
	writeAdd                   = "Add"
	writeOutOfOrder            = "AddOutOfOrder"
	writeRangeDelete           = "AddRangeDelete"
	writeRangeDeleteOutOfOrder = "AddRangeDeleteOutOfOrder"
	writeClose                 = "Close"
	writeReset                 = "Reset"
)

// errWrite is the error of a Sink that fails.
var errWrite = errors.New("write failed")

// alone runs the child processes of runAlone one at a time. Two children that
// exit in the same nanosecond fail to write their coverage into one
// directory.
var alone sync.Mutex

// goldenFiles are the golden tables: the entries of goldenTable under the
// zero options and under each setting of settings. The three in partitions
// have format version 2. The tables with fragments have format version 3.
// The table in partitions with fragments has fragment partitions. The last
// two have the versioned entries of goldenTableOf and the fragments of
// versionedGoldenDeletes.
var goldenFiles = []struct {
	name string
	file string
	opts sstable.WriterOptions
	dels []rangeDelete
}{
	{name: "the zero options", file: "default.sst"},
	{name: settings[0].name, file: "uncompressed.sst", opts: settings[0].opts},
	{name: settings[1].name, file: "uncompressed-filter.sst", opts: settings[1].opts},
	{name: settings[2].name, file: "compressed.sst", opts: settings[2].opts},
	{name: settings[3].name, file: "compressed-filter.sst", opts: settings[3].opts},
	{name: settings[4].name, file: "partitions.sst", opts: settings[4].opts},
	{name: settings[5].name, file: "partitions-filter.sst", opts: settings[5].opts},
	{name: settings[6].name, file: "compressed-partitions-filter.sst", opts: settings[6].opts},
	{
		name: settings[7].name + " and fragments", file: "prefixes-fragments.sst", opts: settings[7].opts,
		dels: goldenDeletes,
	},
	{
		name: settings[8].name + " and fragment partitions", file: "partitions-prefixes-fragment-parts.sst",
		opts: settings[8].opts, dels: goldenDeletes,
	},
	{
		name: settings[7].name + " and versions", file: "prefixes-versions.sst", opts: versionedSettings[0],
		dels: versionedGoldenDeletes,
	},
	{
		name: settings[8].name + " and versions", file: "partitions-prefixes-versions.sst", opts: versionedSettings[1],
		dels: versionedGoldenDeletes,
	},
}

// refusedOptions are options that NewWriter and MaxSize refuse.
var refusedOptions = []struct {
	name string
	give sstable.WriterOptions
}{
	{name: "returns ErrOptions for a negative BlockSize", give: sstable.WriterOptions{BlockSize: -1}},
	{
		name: "returns ErrOptions for a BlockSize above 2^32 - 1",
		give: sstable.WriterOptions{BlockSize: math.MaxUint32 + 1},
	},
	{name: "returns ErrOptions for a negative RestartInterval", give: sstable.WriterOptions{RestartInterval: -1}},
	{name: "returns ErrOptions for an unknown Compression", give: sstable.WriterOptions{Compression: 2}},
	{name: "returns ErrOptions for 3 bits of filter", give: sstable.WriterOptions{FilterBits: 3}},
	{name: "returns ErrOptions for 17 bits of filter", give: sstable.WriterOptions{FilterBits: 17}},
	{name: "returns ErrOptions for negative bits of filter", give: sstable.WriterOptions{FilterBits: -8}},
	{name: "returns ErrOptions for a negative MaxRetained", give: sstable.WriterOptions{MaxRetained: -1}},
	{name: "returns ErrOptions for a negative PartitionSize", give: sstable.WriterOptions{PartitionSize: -1}},
	{
		name: "returns ErrOptions for a PartitionSize above 2^32 - 1",
		give: sstable.WriterOptions{PartitionSize: math.MaxUint32 + 1},
	},
	{name: "returns ErrOptions for a Prefix without FilterBits", give: sstable.WriterOptions{Prefix: goldenPrefix}},
	{
		name: "returns ErrOptions for a Prefix with an empty name",
		give: sstable.WriterOptions{FilterBits: 8, Prefix: fixedPrefix{n: 6}},
	},
	{name: "returns ErrOptions for a negative SuffixBytes", give: sstable.WriterOptions{SuffixBytes: -1}},
}

// pinned are the entries of the tables whose bytes MaxSize counts exactly.
// Their keys of 120 and 192 bytes share no prefix, and their internal keys
// take 128 and 200 bytes. Their values take 128 bytes. Every varint of their
// tables then takes as many bytes as the bound counts for it.
var pinned = []entry{
	{key: bytes.Repeat([]byte{'a'}, 120), seq: 1, value: bytes.Repeat([]byte{'x'}, 128)},
	{key: bytes.Repeat([]byte{'b'}, 120), seq: 1, value: bytes.Repeat([]byte{'y'}, 128)},
	{key: bytes.Repeat([]byte{'c'}, 192), seq: 1, value: bytes.Repeat([]byte{'z'}, 128)},
}

// pinnedLong are the pinned entries of the tables of two blocks and of two
// index partitions. Their keys of 120, 160 and 160 bytes share no prefix, and
// a cut after the second entry ends each block and each partition at a key
// of the longest length.
var pinnedLong = []entry{
	{key: bytes.Repeat([]byte{'a'}, 120), seq: 1, value: bytes.Repeat([]byte{'x'}, 128)},
	{key: bytes.Repeat([]byte{'b'}, 160), seq: 1, value: bytes.Repeat([]byte{'y'}, 128)},
	{key: bytes.Repeat([]byte{'c'}, 160), seq: 1, value: bytes.Repeat([]byte{'z'}, 128)},
}

// pinnedDeletes are the fragments of the tables whose bytes MaxSize counts
// exactly. A start of 120 bytes and its sequence take an internal key of 128
// bytes, whose length takes a varint of two bytes, and an end of one byte
// takes a varint of one byte, as the bound counts them.
var pinnedDeletes = []rangeDelete{
	{start: bytes.Repeat([]byte{'a'}, 120), end: []byte("b"), seq: 9},
	{start: bytes.Repeat([]byte{'a'}, 120), end: []byte("b"), seq: 8},
	{start: bytes.Repeat([]byte{'b'}, 120), end: []byte("c"), seq: 7},
}

// pinnedPartition is the fragment of the tables in partitions whose bytes
// MaxSize counts exactly. Its partition has one fragment, as many as the
// bound counts for one call. Its empty start leaves the end alone in the
// bytes of the calls, and the end is the key of the entry of the top-level
// index of the fragments. The end of 128 bytes takes a varint of two bytes,
// in the partition and with the sequence 0 in the top-level index, as the
// bound counts them.
var pinnedPartition = []rangeDelete{{start: []byte{}, end: bytes.Repeat([]byte{'b'}, 128), seq: 9}}

// pinnedPrefix is the extraction of the pinned tables whose bytes MaxSize
// counts exactly: the first 120 bytes of a key. Each of the first two pinned
// keys is its own prefix.
var pinnedPrefix = fixedPrefix{name: firstBytes, n: 120}

// writerOptions generates the options of the tables of the checks of
// MaxSize: both codecs, filters of 0, 4, 8 and 16 bits, tables without
// partitions and in partitions, and filters of prefixes, with the defaults
// of BlockSize and RestartInterval included.
var writerOptions = prop.Composite(func(c *prop.Case) sstable.WriterOptions {
	opts := sstable.WriterOptions{
		BlockSize:       c.Draw(prop.Integer(0, 512), "block size"),
		RestartInterval: c.Draw(prop.Integer(0, 8), "restart interval"),
		Compression:     c.Draw(prop.SampledFrom(sstable.NoCompression, sstable.SnappyCompression), "compression"),
		FilterBits:      c.Draw(prop.SampledFrom(0, 4, 8, 16), "filter bits"),
		PartitionSize:   c.Draw(prop.SampledFrom(0, 1, 32, partitionBytes, 408, 4096), "partition size"),
	}
	if opts.FilterBits != 0 && c.Draw(prop.Boolean(), "prefix") {
		opts.Prefix = fixedPrefix{name: firstBytes, n: c.Draw(prop.Integer(0, 3), "prefix length")}
	}

	return opts
})

// sizedEntries generates runs of up to 40 entries in the order of Compare.
// Half of their keys come from keys, so a run repeats a key with another
// sequence. The other keys and every value have up to 9,000 random bytes, so
// a long key fills a data block and an index partition alone, and the
// lengths take varints of one and two bytes.
var sizedEntries = prop.Composite(func(c *prop.Case) []entry {
	length := prop.OneOf(prop.Integer(0, 20), prop.Integer(100, 300), prop.Integer(4000, 9000))
	seed := prop.Integer[uint64](0, math.MaxUint64)
	n := c.Draw(prop.Integer(0, 40), "entries")
	es := make([]entry, 0, n)
	for range n {
		key := c.Draw(prop.SampledFrom(keys...), "key")
		if c.Draw(prop.Boolean(), "random key") {
			key = randomBytes(c.Draw(length, "key length"), c.Draw(seed, "key seed"))
		}
		es = append(es, entry{
			key:   key,
			seq:   c.Draw(prop.Integer[uint64](0, 5), "seq"),
			value: randomBytes(c.Draw(length, "value length"), c.Draw(seed, "value seed")),
		})
	}
	slices.SortFunc(es, func(a, b entry) int { return sstable.Compare(a.key, a.seq, b.key, b.seq) })

	return slices.CompactFunc(es, func(a, b entry) bool { return bytes.Equal(a.key, b.key) && a.seq == b.seq })
})

// failingSink is a Sink that accepts the first n bytes of its writes and
// fails every write after them with errWrite.
type failingSink struct {
	n int
}

var _ sstable.Sink = (*failingSink)(nil)

// Write accepts p, or fails once the Sink has accepted n bytes.
func (w *failingSink) Write(_ context.Context, p []byte) (int, error) {
	if len(p) > w.n {
		return 0, errWrite
	}
	w.n -= len(p)

	return len(p), nil
}

// shortSink is a Sink that accepts one byte less than each write, without
// an error.
type shortSink struct{}

var _ sstable.Sink = shortSink{}

// Write reports one byte less than p and no error.
func (shortSink) Write(_ context.Context, p []byte) (int, error) {
	return max(len(p)-1, 0), nil
}

// scriptedPrefix is a PrefixExtractor that returns the prefix that it maps
// each key to, and no prefix for a key that it does not map. A case maps a
// key to a prefix that breaks the rules of a PrefixExtractor.
type scriptedPrefix map[string]string

var _ sstable.PrefixExtractor = scriptedPrefix{}

// Name returns the name of the extraction.
func (scriptedPrefix) Name() string {
	return firstBytes
}

// Prefix returns a copy of the prefix that p maps key to, and reports false
// for a key that p does not map.
func (p scriptedPrefix) Prefix(key []byte) ([]byte, bool) {
	prefix, ok := p[string(key)]

	return []byte(prefix), ok
}

// countingPrefix is the extraction of goldenPrefix that counts the calls of
// Prefix.
type countingPrefix struct {
	calls int
}

var _ sstable.PrefixExtractor = (*countingPrefix)(nil)

// Name returns the name of goldenPrefix.
func (*countingPrefix) Name() string {
	return firstSix
}

// Prefix counts the call, and returns the prefix of key that goldenPrefix
// returns.
func (p *countingPrefix) Prefix(key []byte) ([]byte, bool) {
	p.calls++

	return goldenPrefix.Prefix(key)
}

func TestWriter(t *testing.T) {
	t.Parallel()

	t.Run("writes what a new Writer writes under any sequence of calls", func(t *testing.T) {
		t.Parallel()
		prop.ForAll(t, writeContract, writeSteps)
	})

	t.Run("Compression", func(t *testing.T) {
		t.Parallel()

		t.Run("Valid", func(t *testing.T) {
			t.Parallel()

			tests := []struct {
				name string
				give sstable.Compression
				want bool
			}{
				{name: "reports true for NoCompression", give: sstable.NoCompression, want: true},
				{name: "reports true for SnappyCompression", give: sstable.SnappyCompression, want: true},
				{name: "reports false for a value above SnappyCompression", give: 2, want: false},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					assert.Equal(t, tt.give.Valid(), tt.want, "Valid accepts the two codecs alone")
				})
			}
		})

		t.Run("String", func(t *testing.T) {
			t.Parallel()

			tests := []struct {
				name string
				give sstable.Compression
				want string
			}{
				{name: "returns none for NoCompression", give: sstable.NoCompression, want: "none"},
				{name: "returns snappy for SnappyCompression", give: sstable.SnappyCompression, want: "snappy"},
				{name: "returns the number of a value that is not valid", give: 7, want: "Compression(7)"},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					assert.Equal(t, tt.give.String(), tt.want, "String names the codec")
				})
			}
		})
	})

	t.Run("WriterOptions", func(t *testing.T) {
		t.Parallel()

		t.Run("MaxSize", func(t *testing.T) {
			t.Parallel()

			t.Run("returns at least the size of the table of the counts", func(t *testing.T) {
				t.Parallel()
				prop.ForAll(t, "a table takes at most the bytes of MaxSize of its counts", func(c *prop.Case) {
					opts := c.Draw(writerOptions, "options")
					es, dels := c.Draw(sizedEntries, "entries"), c.Draw(fragments, "fragments")
					if c.Draw(prop.Boolean(), "versions") {
						opts.SuffixBytes = versionBytes
						slices.SortFunc(es, func(a, b entry) int {
							return sstable.CompareWithSuffix(a.key, a.seq, b.key, b.seq, versionBytes)
						})
						dels = c.Draw(versionedFragments, "fragments with versions")
					}
					bound, err := opts.MaxSize(countsOf(es, dels...))
					assert.NoError(c, err, "MaxSize accepts the options")
					assert.InRange(c, len(writeTable(c, opts, es, dels...)), 0, float64(bound),
						"the table takes at most the bytes of MaxSize")
				})
			})

			t.Run("exceeds a table of small random entries by at most a quarter", func(t *testing.T) {
				t.Parallel()
				// Entries of at most 64 bytes of key and 64 bytes of value fill blocks
				// and partitions of 2 KiB or more to within one entry of their size.
				prop.ForAll(t, "MaxSize of a table of small random entries is close to its size", func(c *prop.Case) {
					opts := c.Draw(writerOptions, "options")
					opts.BlockSize = c.Draw(prop.SampledFrom(0, 2048, 4096), "large block size")
					opts.PartitionSize = c.Draw(prop.SampledFrom(0, 2048, 4096), "large partition size")
					keyLen := c.Draw(prop.Integer(1, 64), "key length")
					valueLen := c.Draw(prop.Integer(0, 64), "value length")
					seed := c.Draw(prop.Integer[uint64](0, math.MaxUint64), "seed")
					n := c.Draw(prop.Integer(0, 200), "entries")
					es := make([]entry, 0, n)
					for i := range uint64(n) {
						key, value := randomBytes(keyLen, seed+i), randomBytes(valueLen, ^(seed+i))
						es = append(es, entry{key: key, seq: 1, value: value})
					}
					slices.SortFunc(es, func(a, b entry) int { return bytes.Compare(a.key, b.key) })
					es = slices.CompactFunc(es, func(a, b entry) bool { return bytes.Equal(a.key, b.key) })
					size := len(writeTable(c, opts, es))
					bound, err := opts.MaxSize(countsOf(es))
					assert.NoError(c, err, "MaxSize accepts the options")
					assert.InRange(c, bound, float64(size), float64(size+size/4+opts.PartitionSize+4096),
						"MaxSize exceeds the table by at most a quarter of it, a partition and 4 KiB")
				})
			})

			tests := []struct {
				name        string
				giveOpts    sstable.WriterOptions
				giveEntries []entry
				giveDeletes []rangeDelete
			}{
				{
					name:        "returns the size of a table in blocks of one entry",
					giveOpts:    sstable.WriterOptions{BlockSize: 1, RestartInterval: 1},
					giveEntries: pinned,
				},
				{
					name:        "returns the size of a table with a filter",
					giveOpts:    sstable.WriterOptions{BlockSize: 1, RestartInterval: 1, FilterBits: 8},
					giveEntries: pinned,
				},
				{
					name:        "returns the size of a table in one block",
					giveOpts:    sstable.WriterOptions{BlockSize: 4096, RestartInterval: 2},
					giveEntries: pinned,
				},
				{
					name:        "returns the size of a table of two blocks",
					giveOpts:    sstable.WriterOptions{BlockSize: 432, RestartInterval: 1},
					giveEntries: pinnedLong,
				},
				{
					name:        "returns the size of a table of two index partitions",
					giveOpts:    sstable.WriterOptions{BlockSize: 1, RestartInterval: 1, PartitionSize: 300},
					giveEntries: pinnedLong,
				},
				{
					name:        "returns the size of a table in partitions of one entry",
					giveOpts:    sstable.WriterOptions{BlockSize: 1, RestartInterval: 1, PartitionSize: 1},
					giveEntries: pinned,
				},
				{
					name: "returns the size of a table in partitions with a filter",
					giveOpts: sstable.WriterOptions{
						BlockSize: 1, RestartInterval: 1, FilterBits: 8, PartitionSize: 1,
					},
					giveEntries: pinned,
				},
				{
					name: "returns the size of a table in one partition",
					giveOpts: sstable.WriterOptions{
						BlockSize: 4096, RestartInterval: 2, FilterBits: 8, PartitionSize: 4096,
					},
					giveEntries: pinned,
				},
				{name: "returns the size of a table of no entries", giveOpts: sstable.WriterOptions{}},
				{
					name:     "returns the size of a table of no entries with a filter",
					giveOpts: sstable.WriterOptions{FilterBits: 8},
				},
				{
					name:     "returns the size of a table of no entries in partitions",
					giveOpts: sstable.WriterOptions{PartitionSize: 1},
				},
				{
					name:     "returns the size of a table of no entries in partitions with a filter",
					giveOpts: sstable.WriterOptions{FilterBits: 8, PartitionSize: 1},
				},
				{
					name: "returns the size of a table with a filter of prefixes",
					giveOpts: sstable.WriterOptions{
						BlockSize: 1, RestartInterval: 1, FilterBits: 8, Prefix: pinnedPrefix,
					},
					giveEntries: pinned,
				},
				{
					name:        "returns the size of a table with fragments",
					giveOpts:    sstable.WriterOptions{BlockSize: 1, RestartInterval: 1},
					giveEntries: pinned,
					giveDeletes: pinnedDeletes,
				},
				{
					name:        "returns the size of a table with a filter and fragments",
					giveOpts:    sstable.WriterOptions{BlockSize: 1, RestartInterval: 1, FilterBits: 8},
					giveEntries: pinned,
					giveDeletes: pinnedDeletes,
				},
				{
					// The fragment of a start of 102 bytes takes a block of 122
					// bytes, whose length takes a varint of one byte.
					name:        "returns the size of a table with a block of fragments under 128 bytes",
					giveOpts:    sstable.WriterOptions{BlockSize: 1, RestartInterval: 1},
					giveEntries: pinned,
					giveDeletes: []rangeDelete{{start: bytes.Repeat([]byte{'a'}, 102), end: []byte("b"), seq: 1}},
				},
				{
					name: "returns the size of a table in partitions with a filter of prefixes",
					giveOpts: sstable.WriterOptions{
						BlockSize: 1, RestartInterval: 1, FilterBits: 8, PartitionSize: 1, Prefix: pinnedPrefix,
					},
					giveEntries: pinned[:2],
				},
				{
					name:        "returns the size of a table in partitions with a fragment partition",
					giveOpts:    sstable.WriterOptions{BlockSize: 1, RestartInterval: 1, PartitionSize: 1},
					giveEntries: pinned,
					giveDeletes: pinnedPartition,
				},
				{
					name: "returns the size of a table in partitions with a filter of prefixes and a fragment partition",
					giveOpts: sstable.WriterOptions{
						BlockSize: 1, RestartInterval: 1, FilterBits: 8, PartitionSize: 1, Prefix: pinnedPrefix,
					},
					giveEntries: pinned[:2],
					giveDeletes: pinnedPartition,
				},
				{
					name:        "returns the size of a table with versions",
					giveOpts:    sstable.WriterOptions{BlockSize: 1, RestartInterval: 1, SuffixBytes: versionBytes},
					giveEntries: pinned,
				},
				{
					name: "returns the size of a table in partitions with versions and a fragment partition",
					giveOpts: sstable.WriterOptions{
						BlockSize: 1, RestartInterval: 1, PartitionSize: 1, SuffixBytes: versionBytes,
					},
					giveEntries: pinned,
					giveDeletes: pinnedPartition,
				},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					bound, err := tt.giveOpts.MaxSize(countsOf(tt.giveEntries, tt.giveDeletes...))
					assert.NoError(t, err, "MaxSize accepts the options")
					assert.Equal(t, bound, int64(len(writeTable(t, tt.giveOpts, tt.giveEntries, tt.giveDeletes...))),
						"MaxSize returns the size of the table")
				})
			}

			saturated := []struct {
				name       string
				giveCounts sstable.Counts
			}{
				{
					name:       "returns math.MaxInt64 for more entries than 2^48",
					giveCounts: sstable.Counts{Entries: 1<<48 + 1},
				},
				{
					name:       "returns math.MaxInt64 for more key bytes than 2^48",
					giveCounts: sstable.Counts{KeyBytes: 1<<48 + 1},
				},
				{
					name:       "returns math.MaxInt64 for more value bytes than 2^48",
					giveCounts: sstable.Counts{ValueBytes: 1<<48 + 1},
				},
				{
					name:       "returns math.MaxInt64 for a longest key above 2^48",
					giveCounts: sstable.Counts{Longest: math.MaxInt},
				},
				{
					name:       "returns math.MaxInt64 for more fragments than 2^48",
					giveCounts: sstable.Counts{RangeDeletes: 1<<48 + 1},
				},
				{
					name:       "returns math.MaxInt64 for more bytes of fragments than 2^48",
					giveCounts: sstable.Counts{RangeDeleteBytes: 1<<48 + 1},
				},
			}
			for _, tt := range saturated {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					bound, err := settings[5].opts.MaxSize(tt.giveCounts)
					assert.NoError(t, err, "MaxSize accepts the options")
					assert.Equal(t, bound, int64(math.MaxInt64), "MaxSize saturates")
				})
			}

			t.Run("counts a negative count as 0", func(t *testing.T) {
				t.Parallel()
				bound, err := settings[5].opts.MaxSize(sstable.Counts{
					Entries: -1, KeyBytes: -1, ValueBytes: -1, Longest: -1, RangeDeletes: -1, RangeDeleteBytes: -1,
				})
				assert.NoError(t, err, "MaxSize accepts the options")
				assert.Equal(t, bound, int64(len(writeTable(t, settings[5].opts, nil))),
					"MaxSize returns the size of a table of no entries")
			})

			t.Run("returns the size of a table of no entries for any counts of bytes", func(t *testing.T) {
				t.Parallel()
				bound, err := settings[5].opts.MaxSize(sstable.Counts{KeyBytes: 100, ValueBytes: 1000, Longest: 50})
				assert.NoError(t, err, "MaxSize accepts the options")
				assert.Equal(t, bound, int64(len(writeTable(t, settings[5].opts, nil))),
					"MaxSize returns the size of a table of no entries")
			})

			t.Run("counts at most longest bytes for each key", func(t *testing.T) {
				t.Parallel()
				opts := settings[5].opts
				bound, err := opts.MaxSize(sstable.Counts{Entries: 3, KeyBytes: 1000, ValueBytes: 384, Longest: 200})
				assert.NoError(t, err, "MaxSize accepts the counts")
				capped, err := opts.MaxSize(sstable.Counts{Entries: 3, KeyBytes: 600, ValueBytes: 384, Longest: 200})
				assert.NoError(t, err, "MaxSize accepts the capped counts")
				assert.Equal(t, bound, capped, "MaxSize counts at most longest bytes for each of three keys")
			})

			t.Run("returns a bound below math.MaxInt64 for counts of 2^48", func(t *testing.T) {
				t.Parallel()
				bound, err := sstable.WriterOptions{}.MaxSize(sstable.Counts{
					Entries: 1 << 48, KeyBytes: 1 << 48, ValueBytes: 1 << 48, Longest: 1 << 20,
					RangeDeletes: 1 << 48, RangeDeleteBytes: 1 << 48,
				})
				assert.NoError(t, err, "MaxSize accepts the counts")
				assert.NotEqual(t, bound, int64(math.MaxInt64), "MaxSize bounds counts of 2^48")
			})

			for _, tt := range refusedOptions {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					_, err := tt.give.MaxSize(sstable.Counts{Entries: 1, KeyBytes: 1, ValueBytes: 1, Longest: 1})
					assert.ErrorIs(t, err, sstable.ErrOptions, "MaxSize refuses the options")
				})
			}

			t.Run("returns filter.ErrTooLarge for a filter block of more fingerprints than 2^32", func(t *testing.T) {
				t.Parallel()
				_, err := sstable.WriterOptions{FilterBits: 8}.MaxSize(sstable.Counts{Entries: 1 << 40})
				assert.ErrorIs(t, err, filter.ErrTooLarge, "MaxSize refuses a filter that the filter package refuses")
			})
		})
	})

	t.Run("NewWriter", func(t *testing.T) {
		t.Parallel()

		for _, tt := range refusedOptions {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				w, err := sstable.NewWriter(discardSink{}, tt.give)
				assert.ErrorIs(t, err, sstable.ErrOptions, "NewWriter refuses the options")
				assert.Nil(t, w, "NewWriter returns no Writer")
			})
		}

		t.Run("returns a Writer for a BlockSize of 2^32 - 1", func(t *testing.T) {
			t.Parallel()
			data := writeTable(t, sstable.WriterOptions{BlockSize: math.MaxUint32}, goldenTable())
			got, err := scanAll(t.Context(), openTable(t, data, sstable.ReaderOptions{}).NewIter(sstable.IterOptions{}))
			assert.NoError(t, err, "the table of one block reads")
			assert.Equal(t, got, goldenTable(), "the table contains every entry")
		})

		t.Run("returns a Writer for a PartitionSize of 2^32 - 1", func(t *testing.T) {
			t.Parallel()
			l := newLayout(t, sstable.WriterOptions{FilterBits: 8, PartitionSize: math.MaxUint32}, goldenTable())
			expect.Length(t, l.indexParts, 1, "the table has one index partition")
			expect.Length(t, l.filterParts, 1, "the table has one filter partition")
		})

		for _, bits := range []int{4, 8, 10, 12, 16} {
			t.Run("returns a Writer for a filter of "+strconv.Itoa(bits)+" bits", func(t *testing.T) {
				t.Parallel()
				data := writeTable(t, sstable.WriterOptions{FilterBits: bits}, goldenTable())
				r := openTable(t, data, sstable.ReaderOptions{})
				may, err := r.MayContain(t.Context(), []byte("key 000"))
				assert.NoError(t, err, "MayContain reads the filter")
				assert.True(t, may, "the filter contains the keys of the table")
			})
		}
	})

	t.Run("Add", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give entry
		}{
			{name: "returns ErrOrder for a smaller key", give: entry{key: []byte("a"), seq: 1}},
			{name: "returns ErrOrder for the key with a higher sequence", give: entry{key: []byte("b"), seq: 6}},
			{name: "returns ErrOrder for a repeat of the previous entry", give: entry{key: []byte("b"), seq: 5}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				var buf bufferSink
				w, err := sstable.NewWriter(&buf, sstable.WriterOptions{})
				assert.NoError(t, err, "NewWriter accepts the zero options")
				assert.NoError(t, w.Add(t.Context(), []byte("b"), 5, []byte("first")), "Add adds the first entry")
				assert.ErrorIs(t, w.Add(t.Context(), tt.give.key, tt.give.seq, nil), sstable.ErrOrder,
					"Add refuses the entry")
				assert.NoError(t, w.Add(t.Context(), []byte("c"), 1, []byte("next")),
					"Add adds an entry in order after the refusal")
				_, err = w.Close(t.Context())
				assert.NoError(t, err, "Close writes the table")
				r := openTable(t, buf.Bytes(), sstable.ReaderOptions{})
				got, err := scanAll(t.Context(), r.NewIter(sstable.IterOptions{}))
				assert.NoError(t, err, "the table reads")
				assert.Equal(t, got, []entry{
					{key: []byte("b"), seq: 5, value: []byte("first")},
					{key: []byte("c"), seq: 1, value: []byte("next")},
				}, "the table contains the entries that Add accepted")
			})
		}

		t.Run("returns ErrTooLarge for a key that does not fit the index", func(t *testing.T) {
			t.Parallel()
			// The key takes 4 GiB of address space.
			if runAlone(t) {
				return
			}
			key := make([]byte, math.MaxUint32)
			w, err := sstable.NewWriter(discardSink{}, sstable.WriterOptions{})
			assert.NoError(t, err, "NewWriter accepts the zero options")
			assert.ErrorIs(t, w.Add(t.Context(), key, 1, nil), sstable.ErrTooLarge, "Add refuses the key")
			assert.NoError(t, w.Add(t.Context(), []byte("a"), 1, nil), "Add adds an entry after the refusal")
			sum, err := w.Close(t.Context())
			assert.NoError(t, err, "Close writes the table")
			assert.Equal(t, sum.Entries, int64(1), "the table contains the entry that Add accepted")
		})

		// The extraction maps the keys a1, b1 and c1 to the prefixes a, b and
		// c. The refused key a2 follows a1. Its empty prefix is a byte prefix
		// of a2 before the prefix a of a1.
		prefixes := []struct {
			name       string
			giveKey    string
			givePrefix string
		}{
			{
				name:       "returns ErrPrefix for a prefix that is not a byte prefix of its key",
				giveKey:    "a2",
				givePrefix: "b",
			},
			{name: "returns ErrPrefix for a prefix before the prefix of an earlier key", giveKey: "a2"},
		}
		for _, tt := range prefixes {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				prefix := scriptedPrefix{"a1": "a", "b1": "b", "c1": "c", tt.giveKey: tt.givePrefix}
				opts := sstable.WriterOptions{FilterBits: 8, Prefix: prefix}
				var buf bufferSink
				w, err := sstable.NewWriter(&buf, opts)
				assert.NoError(t, err, "NewWriter accepts the options")
				assert.NoError(t, w.Add(t.Context(), []byte("a1"), 1, nil), "Add adds the entry of a1")
				assert.ErrorIs(t, w.Add(t.Context(), []byte(tt.giveKey), 1, nil), sstable.ErrPrefix,
					"Add refuses the prefix")
				for _, key := range []string{"b1", "c1"} {
					assert.NoError(t, w.Add(t.Context(), []byte(key), 1, nil), "Add adds an entry after the refusal")
				}
				_, err = w.Close(t.Context())
				assert.NoError(t, err, "Close writes the table")
				want := writeTable(t, opts, []entry{
					{key: []byte("a1"), seq: 1}, {key: []byte("b1"), seq: 1}, {key: []byte("c1"), seq: 1},
				})
				assert.Equal(t, buf.Bytes(), want, "the refusal leaves the Writer unchanged")
			})
		}

		t.Run("calls Prefix once for each distinct key", func(t *testing.T) {
			t.Parallel()
			// The golden table has two entries of each of its keys.
			prefix := &countingPrefix{}
			writeTable(t, sstable.WriterOptions{FilterBits: 8, Prefix: prefix}, goldenTable())
			assert.Equal(t, prefix.calls, len(goldenKeys), "Add asks the extraction once for each key")
		})

		// Each case adds its entry after the key a at the version 5 and the
		// sequence 2, in a table with versions of versionBytes bytes.
		be := binary.BigEndian
		versionsInOrder := []struct {
			name string
			give entry
		}{
			{
				name: "adds a key that extends the key before it at a higher version",
				give: entry{key: be.AppendUint64([]byte("a\x00"), 9), seq: 1},
			},
			{
				name: "adds the key before it at a lower version",
				give: entry{key: be.AppendUint64([]byte("a"), 4), seq: 3},
			},
			{
				name: "adds the key and the version before it at a lower sequence",
				give: entry{key: be.AppendUint64([]byte("a"), 5), seq: 1},
			},
		}
		for _, tt := range versionsInOrder {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				w, err := sstable.NewWriter(discardSink{}, sstable.WriterOptions{SuffixBytes: versionBytes})
				assert.NoError(t, err, "NewWriter accepts the width")
				assert.NoError(t, w.Add(t.Context(), be.AppendUint64([]byte("a"), 5), 2, nil),
					"Add adds the first entry")
				assert.NoError(t, w.Add(t.Context(), tt.give.key, tt.give.seq, nil),
					"Add takes the keys in the order of their bytes before their versions")
			})
		}

		versionsOutOfOrder := []struct {
			name string
			give entry
		}{
			{
				name: "returns ErrOrder for the key before it at a higher version",
				give: entry{key: be.AppendUint64([]byte("a"), 9), seq: 1},
			},
			{
				name: "returns ErrOrder for the key and the version before it at a higher sequence",
				give: entry{key: be.AppendUint64([]byte("a"), 5), seq: 3},
			},
			{
				name: "returns ErrOrder for a key before the key before it without their versions",
				give: entry{key: be.AppendUint64(nil, 9), seq: 3},
			},
		}
		for _, tt := range versionsOutOfOrder {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				w, err := sstable.NewWriter(discardSink{}, sstable.WriterOptions{SuffixBytes: versionBytes})
				assert.NoError(t, err, "NewWriter accepts the width")
				assert.NoError(t, w.Add(t.Context(), be.AppendUint64([]byte("a"), 5), 2, nil),
					"Add adds the first entry")
				assert.ErrorIs(t, w.Add(t.Context(), tt.give.key, tt.give.seq, nil), sstable.ErrOrder,
					"Add refuses the entry")
			})
		}

		t.Run("calls Prefix once for each distinct key without its version", func(t *testing.T) {
			t.Parallel()
			// The golden table with versions has two versions of each of its
			// keys.
			prefix := &countingPrefix{}
			opts := sstable.WriterOptions{FilterBits: 8, Prefix: prefix, SuffixBytes: versionBytes}
			writeTable(t, opts, goldenTableOf(opts))
			assert.Equal(t, prefix.calls, len(goldenKeys), "Add asks the extraction once for each key")
		})

		t.Run("passes each key without its version to Prefix", func(t *testing.T) {
			t.Parallel()
			// The extraction maps the keys without their versions alone.
			prefix := scriptedPrefix{"a": "a", "b": "b"}
			es := []entry{
				{key: be.AppendUint64([]byte("a"), 5), seq: 1},
				{key: be.AppendUint64([]byte("b"), 5), seq: 1},
			}
			opts := sstable.WriterOptions{FilterBits: 16, Prefix: prefix, SuffixBytes: versionBytes}
			r := openTable(t, writeTable(t, opts, es), sstable.ReaderOptions{Prefix: prefix, SuffixBytes: versionBytes})
			may, err := r.MayContainPrefix(t.Context(), []byte("b"))
			assert.NoError(t, err, "MayContainPrefix asks the filter of prefixes")
			assert.True(t, may, "the filter of prefixes has the prefix of the key without its version")
		})

		t.Run("returns ErrPrefix for a prefix that includes a byte of the version", func(t *testing.T) {
			t.Parallel()
			// The prefix a\x00 is a byte prefix of the key a at the version 5, and
			// not of a without its version.
			prefix := scriptedPrefix{"a": "a\x00"}
			w, err := sstable.NewWriter(discardSink{},
				sstable.WriterOptions{FilterBits: 8, Prefix: prefix, SuffixBytes: versionBytes})
			assert.NoError(t, err, "NewWriter accepts the options")
			assert.ErrorIs(t, w.Add(t.Context(), be.AppendUint64([]byte("a"), 5), 1, nil), sstable.ErrPrefix,
				"Add refuses the prefix")
		})

		t.Run("adds the hash of no prefix for a key without a prefix", func(t *testing.T) {
			t.Parallel()
			// The extraction maps a1 alone, so the table has one prefix.
			prefix := scriptedPrefix{"a1": "a"}
			es := []entry{{key: []byte("a1"), seq: 1}, {key: []byte("b1"), seq: 1}}
			r := openTable(t, writeTable(t, sstable.WriterOptions{FilterBits: 16, Prefix: prefix}, es),
				sstable.ReaderOptions{Prefix: prefix})
			may, err := r.MayContainPrefix(t.Context(), []byte("b1"))
			assert.NoError(t, err, "MayContainPrefix asks the filter of prefixes")
			assert.False(t, may, "the filter of prefixes has no hash of a key without a prefix")
		})

		t.Run("ends a block at the entry that brings it to BlockSize bytes", func(t *testing.T) {
			t.Parallel()
			// An entry of a key of one byte without a value has three varints
			// of one byte and an internal key of 9 bytes.
			const entryLen = 3 + 1 + seqLen
			es := []entry{{key: []byte("a"), seq: 1}, {key: []byte("b"), seq: 1}, {key: []byte("c"), seq: 1}}
			l := newLayout(t, sstable.WriterOptions{BlockSize: entryLen}, es)
			assert.Length(t, l.blocks, len(es), "every entry ends its block")
		})

		t.Run("returns ErrClosed after Close", func(t *testing.T) {
			t.Parallel()
			w, err := sstable.NewWriter(discardSink{}, sstable.WriterOptions{})
			assert.NoError(t, err, "NewWriter accepts the zero options")
			assert.FailsAfterClose(t, func() error {
				_, err := w.Close(t.Context())

				return err
			}, func() error {
				return w.Add(t.Context(), []byte("a"), 1, nil)
			}, sstable.ErrClosed, "Add refuses an entry after Close")
		})

		t.Run("returns the error of the Sink for every later call", func(t *testing.T) {
			t.Parallel()
			w, err := sstable.NewWriter(&failingSink{}, sstable.WriterOptions{BlockSize: 1})
			assert.NoError(t, err, "NewWriter accepts the options")
			expect.ErrorIs(t, w.Add(t.Context(), []byte("a"), 1, nil), errWrite,
				"Add returns the error of the block that it writes")
			expect.ErrorIs(t, w.Add(t.Context(), []byte("b"), 1, nil), errWrite, "Add returns the error again")
			_, err = w.Close(t.Context())
			expect.ErrorIs(t, err, errWrite, "Close returns the error too")
		})

		t.Run("returns the error of the context of the write of its block", func(t *testing.T) {
			t.Parallel()
			w, err := sstable.NewWriter(&bufferSink{}, oneEntry)
			assert.NoError(t, err, "NewWriter accepts the options")
			assert.HonoursCancellation(t, func(ctx context.Context) error {
				return w.Add(ctx, []byte("a"), 1, nil)
			}, "Add returns the error that the Sink returns for a context that ended")
		})
	})

	t.Run("AddRangeDelete", func(t *testing.T) {
		t.Parallel()

		// Each case adds its fragment after the fragment from b up to d at 5,
		// and then the fragment from d up to f at 2.
		first := rangeDelete{start: []byte("b"), end: []byte("d"), seq: 5}
		next := rangeDelete{start: []byte("d"), end: []byte("f"), seq: 2}
		tests := []struct {
			name    string
			give    rangeDelete
			wantErr error
		}{
			{
				name:    "returns ErrRange for a start at its end",
				give:    rangeDelete{start: []byte("e"), end: []byte("e"), seq: 1},
				wantErr: sstable.ErrRange,
			},
			{
				name:    "returns ErrRange for a start after its end",
				give:    rangeDelete{start: []byte("f"), end: []byte("e"), seq: 1},
				wantErr: sstable.ErrRange,
			},
			{
				name:    "returns ErrOrder for the fragment before at the same sequence",
				give:    rangeDelete{start: []byte("b"), end: []byte("d"), seq: 5},
				wantErr: sstable.ErrOrder,
			},
			{
				name:    "returns ErrOrder for the fragment before at a higher sequence",
				give:    rangeDelete{start: []byte("b"), end: []byte("d"), seq: 6},
				wantErr: sstable.ErrOrder,
			},
			{
				name:    "returns ErrOrder for a start before the end of the fragment before",
				give:    rangeDelete{start: []byte("c"), end: []byte("e"), seq: 1},
				wantErr: sstable.ErrOrder,
			},
			{
				name:    "returns ErrOrder for the start of the fragment before with another end",
				give:    rangeDelete{start: []byte("b"), end: []byte("e"), seq: 4},
				wantErr: sstable.ErrOrder,
			},
			{
				name:    "returns ErrOrder for the end of the fragment before with another start",
				give:    rangeDelete{start: []byte("c"), end: []byte("d"), seq: 4},
				wantErr: sstable.ErrOrder,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				var buf bufferSink
				w, err := sstable.NewWriter(&buf, sstable.WriterOptions{})
				assert.NoError(t, err, "NewWriter accepts the zero options")
				assert.NoError(t, w.AddRangeDelete(t.Context(), first.start, first.end, first.seq),
					"AddRangeDelete adds the first fragment")
				assert.ErrorIs(t, w.AddRangeDelete(t.Context(), tt.give.start, tt.give.end, tt.give.seq), tt.wantErr,
					"AddRangeDelete refuses the fragment")
				assert.NoError(t, w.AddRangeDelete(t.Context(), next.start, next.end, next.seq),
					"AddRangeDelete adds a fragment at the end of the fragment before")
				_, err = w.Close(t.Context())
				assert.NoError(t, err, "Close writes the table")
				assert.Equal(t, buf.Bytes(), writeTable(t, sstable.WriterOptions{}, nil, first, next),
					"the refusal leaves the Writer unchanged")
			})
		}

		// Each case adds its fragment after the fragment from b up to d at the
		// version 5 and the sequence 5, in a table with versions.
		be := binary.BigEndian
		versioned := rangeDelete{start: be.AppendUint64([]byte("b"), 5), end: []byte("d"), seq: 5}
		versionsInOrder := []struct {
			name string
			give rangeDelete
		}{
			{
				name: "adds the fragment before at a lower version and a higher sequence",
				give: rangeDelete{start: be.AppendUint64([]byte("b"), 4), end: []byte("d"), seq: 9},
			},
			{
				name: "adds a fragment whose start without its version is before its end",
				give: rangeDelete{start: be.AppendUint64([]byte("d"), 9), end: []byte("d\x00"), seq: 1},
			},
		}
		for _, tt := range versionsInOrder {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				w, err := sstable.NewWriter(discardSink{}, sstable.WriterOptions{SuffixBytes: versionBytes})
				assert.NoError(t, err, "NewWriter accepts the width")
				assert.NoError(t, w.AddRangeDelete(t.Context(), versioned.start, versioned.end, versioned.seq),
					"AddRangeDelete adds the first fragment")
				assert.NoError(t, w.AddRangeDelete(t.Context(), tt.give.start, tt.give.end, tt.give.seq),
					"AddRangeDelete takes the fragment")
			})
		}

		versionsRefused := []struct {
			name    string
			give    rangeDelete
			wantErr error
		}{
			{
				name:    "returns ErrRange for a start without its version at its end",
				give:    rangeDelete{start: be.AppendUint64([]byte("e"), 1), end: []byte("e"), seq: 1},
				wantErr: sstable.ErrRange,
			},
			{
				name:    "returns ErrOrder for the fragment before at a higher version",
				give:    rangeDelete{start: be.AppendUint64([]byte("b"), 6), end: []byte("d"), seq: 1},
				wantErr: sstable.ErrOrder,
			},
			{
				name:    "returns ErrOrder for the fragment before at its version and its sequence",
				give:    rangeDelete{start: be.AppendUint64([]byte("b"), 5), end: []byte("d"), seq: 5},
				wantErr: sstable.ErrOrder,
			},
			{
				name:    "returns ErrOrder for a start without its version before the end of the fragment before",
				give:    rangeDelete{start: be.AppendUint64([]byte("c"), 9), end: []byte("e"), seq: 1},
				wantErr: sstable.ErrOrder,
			},
		}
		for _, tt := range versionsRefused {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				w, err := sstable.NewWriter(discardSink{}, sstable.WriterOptions{SuffixBytes: versionBytes})
				assert.NoError(t, err, "NewWriter accepts the width")
				assert.NoError(t, w.AddRangeDelete(t.Context(), versioned.start, versioned.end, versioned.seq),
					"AddRangeDelete adds the first fragment")
				assert.ErrorIs(t, w.AddRangeDelete(t.Context(), tt.give.start, tt.give.end, tt.give.seq), tt.wantErr,
					"AddRangeDelete refuses the fragment")
			})
		}

		t.Run("ends a fragment partition at the first fragment after PartitionSize bytes", func(t *testing.T) {
			t.Parallel()
			// Each entry of the golden fragments takes 25 bytes. The second entry
			// brings the first partition to 50 bytes, and the third entry is a
			// position of the fragment of the second. The fourth entry starts the
			// next partition.
			l := newLayout(t, sstable.WriterOptions{PartitionSize: 40}, nil, goldenDeletes...)
			counts := make([]int, 0, len(l.fragmentParts))
			for _, part := range l.fragmentParts {
				counts = append(counts, len(restarts(l.data[part.off:part.off+part.n])))
			}
			assert.Equal(t, counts, []int{3, 2}, "the positions of one fragment are in one partition")
		})

		t.Run("returns ErrTooLarge for a fragment that does not fit the block of fragments", func(t *testing.T) {
			t.Parallel()
			// The start takes 4 GiB of address space, and the end of one byte
			// follows it.
			if runAlone(t) {
				return
			}
			start := make([]byte, math.MaxUint32)
			w, err := sstable.NewWriter(discardSink{}, sstable.WriterOptions{})
			assert.NoError(t, err, "NewWriter accepts the zero options")
			assert.ErrorIs(t, w.AddRangeDelete(t.Context(), start, []byte{1}, 1), sstable.ErrTooLarge,
				"AddRangeDelete refuses the fragment")
			assert.NoError(t, w.AddRangeDelete(t.Context(), []byte("a"), []byte("b"), 1),
				"AddRangeDelete adds a fragment after the refusal")
			sum, err := w.Close(t.Context())
			assert.NoError(t, err, "Close writes the table")
			assert.Equal(t, sum.RangeDeletes, int64(1), "the table contains the fragment that AddRangeDelete accepted")
		})

		t.Run("returns ErrClosed after Close", func(t *testing.T) {
			t.Parallel()
			w, err := sstable.NewWriter(discardSink{}, sstable.WriterOptions{})
			assert.NoError(t, err, "NewWriter accepts the zero options")
			assert.FailsAfterClose(t, func() error {
				_, err := w.Close(t.Context())

				return err
			}, func() error {
				return w.AddRangeDelete(t.Context(), []byte("a"), []byte("b"), 1)
			}, sstable.ErrClosed, "AddRangeDelete refuses a fragment after Close")
		})

		t.Run("returns the error of the Sink of an earlier write", func(t *testing.T) {
			t.Parallel()
			w, err := sstable.NewWriter(&failingSink{}, sstable.WriterOptions{BlockSize: 1})
			assert.NoError(t, err, "NewWriter accepts the options")
			assert.ErrorIs(t, w.Add(t.Context(), []byte("a"), 1, nil), errWrite,
				"Add returns the error of the block that it writes")
			assert.ErrorIs(t, w.AddRangeDelete(t.Context(), []byte("a"), []byte("b"), 1), errWrite,
				"AddRangeDelete returns the error of the write before")
		})
	})

	t.Run("Close", func(t *testing.T) {
		t.Parallel()

		// The sequences of the fragments are within the sequences of the
		// entries, so a Writer that restarted its sequences at a fragment
		// would state other bounds.
		summaries := []struct {
			name        string
			giveEntries []entry
			giveDeletes []rangeDelete
			want        sstable.Summary
		}{
			{
				name: "returns the summary of the entries and the fragments",
				giveEntries: []entry{
					{key: []byte("a"), seq: 7}, {key: []byte("a"), seq: 3}, {key: []byte("b"), seq: 9},
				},
				giveDeletes: []rangeDelete{
					{start: []byte("c"), end: []byte("d"), seq: 8},
					{start: []byte("c"), end: []byte("d"), seq: 4},
					{start: []byte("d"), end: []byte("e"), seq: 5},
				},
				want: sstable.Summary{
					Entries: 3, MinSeq: 3, MaxSeq: 9, SmallestKey: []byte("a"), SmallestSeq: 7,
					LargestKey: []byte("b"), LargestSeq: 9,
					RangeDeletes: 3, RangeDeleteStart: []byte("c"), RangeDeleteEnd: []byte("e"),
				},
			},
			{
				name: "returns the summary of a table of fragments alone",
				giveDeletes: []rangeDelete{
					{start: []byte("c"), end: []byte("d"), seq: 6}, {start: []byte("c"), end: []byte("d"), seq: 2},
				},
				want: sstable.Summary{
					MinSeq: 2, MaxSeq: 6, RangeDeletes: 2, RangeDeleteStart: []byte("c"), RangeDeleteEnd: []byte("d"),
				},
			},
		}
		for _, tt := range summaries {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				var buf bufferSink
				w, err := sstable.NewWriter(&buf, sstable.WriterOptions{})
				assert.NoError(t, err, "NewWriter accepts the zero options")
				for _, e := range tt.giveEntries {
					assert.NoError(t, w.Add(t.Context(), e.key, e.seq, e.value), "Add adds the entry")
				}
				for _, d := range tt.giveDeletes {
					assert.NoError(t, w.AddRangeDelete(t.Context(), d.start, d.end, d.seq),
						"AddRangeDelete adds the fragment")
				}
				sum, err := w.Close(t.Context())
				assert.NoError(t, err, "Close writes the table")
				want := tt.want
				want.Size = int64(buf.Len())
				assert.Equal(t, sum, want,
					"the summary describes the entries, the fragments and the bytes of the table")
			})
		}

		t.Run("keeps the sequences of the fragments before the first entry", func(t *testing.T) {
			t.Parallel()
			w, err := sstable.NewWriter(discardSink{}, sstable.WriterOptions{})
			assert.NoError(t, err, "NewWriter accepts the zero options")
			assert.NoError(t, w.AddRangeDelete(t.Context(), []byte("a"), []byte("b"), 1),
				"AddRangeDelete adds the fragment")
			assert.NoError(t, w.Add(t.Context(), []byte("c"), 5, nil), "Add adds the entry after the fragment")
			sum, err := w.Close(t.Context())
			assert.NoError(t, err, "Close writes the table")
			expect.Equal(t, sum.MinSeq, uint64(1), "the lowest sequence is the sequence of the fragment")
			expect.Equal(t, sum.MaxSeq, uint64(5), "the highest sequence is the sequence of the entry")
		})

		t.Run("returns the summary of the table", func(t *testing.T) {
			t.Parallel()
			var buf bufferSink
			w, err := sstable.NewWriter(&buf, sstable.WriterOptions{})
			assert.NoError(t, err, "NewWriter accepts the zero options")
			es := []entry{{key: []byte("a"), seq: 7}, {key: []byte("a"), seq: 3}, {key: []byte("b"), seq: 9}}
			for _, e := range es {
				assert.NoError(t, w.Add(t.Context(), e.key, e.seq, e.value), "Add adds the entry")
			}
			sum, err := w.Close(t.Context())
			assert.NoError(t, err, "Close writes the table")
			assert.Equal(t, sum, sstable.Summary{
				Entries: 3, Size: int64(buf.Len()), MinSeq: 3, MaxSeq: 9,
				SmallestKey: []byte("a"), SmallestSeq: 7, LargestKey: []byte("b"), LargestSeq: 9,
			}, "the summary describes the entries and the bytes of the table")
		})

		t.Run("writes a table of no entries", func(t *testing.T) {
			t.Parallel()
			r := openTable(t, writeTable(t, sstable.WriterOptions{FilterBits: 8}, nil), sstable.ReaderOptions{})
			got, err := scanAll(t.Context(), r.NewIter(sstable.IterOptions{}))
			assert.NoError(t, err, "the empty table reads")
			expect.Empty(t, got, "a cursor yields no entry")
			expect.Equal(t, r.Summary().Entries, int64(0), "the summary counts no entry")
		})

		t.Run("records the width of the versions of a table without fragments and prefixes", func(t *testing.T) {
			t.Parallel()
			l := newLayout(t, sstable.WriterOptions{SuffixBytes: versionBytes}, nil)
			footer := l.data[len(l.data)-footerLen:]
			expect.Equal(t, binary.BigEndian.Uint32(footer[footerVersion:]), uint32(placedVersion),
				"a table with versions has format version 3")
			places := l.data[l.places.off : l.places.off+l.places.n]
			flags, _ := binary.Uvarint(places)
			expect.Equal(t, flags, uint64(suffixFlag), "the block of places has the flag of versions")
			expect.Equal(t, places[len(places)-1], byte(versionBytes), "the width follows the name")
		})

		t.Run("writes the fragments of a table in partitions into fragment partitions", func(t *testing.T) {
			t.Parallel()
			l := newLayout(t, partsOfOne, nil, goldenDeletes...)
			flags, _ := binary.Uvarint(l.data[l.places.off:])
			expect.Equal(t, flags, uint64(partitionedFlag|fragmentPartsFlag),
				"the block of places has the flags of partitions and of fragment partitions")
			expect.Length(t, l.fragmentParts, 4, "each of the four fragments takes a partition")
		})

		t.Run("returns ErrClosed for a second Close", func(t *testing.T) {
			t.Parallel()
			w, err := sstable.NewWriter(discardSink{}, sstable.WriterOptions{})
			assert.NoError(t, err, "NewWriter accepts the zero options")
			closeWriter := func() error {
				_, err := w.Close(t.Context())

				return err
			}
			assert.FailsAfterClose(t, closeWriter, closeWriter, sstable.ErrClosed, "the second Close refuses")
		})

		t.Run("returns the error of the Sink for the blocks after the data", func(t *testing.T) {
			t.Parallel()
			w, err := sstable.NewWriter(&failingSink{}, sstable.WriterOptions{})
			assert.NoError(t, err, "NewWriter accepts the zero options")
			assert.NoError(t, w.Add(t.Context(), []byte("a"), 1, nil), "Add keeps the entry in its block")
			_, err = w.Close(t.Context())
			assert.ErrorIs(t, err, errWrite, "Close returns the error of the writes of the table")
		})

		t.Run("returns the error of the context of its write", func(t *testing.T) {
			t.Parallel()
			w, err := sstable.NewWriter(&bufferSink{}, sstable.WriterOptions{})
			assert.NoError(t, err, "NewWriter accepts the zero options")
			assert.NoError(t, w.Add(t.Context(), []byte("a"), 1, nil), "Add keeps the entry in its block")
			assert.HonoursCancellation(t, func(ctx context.Context) error {
				_, err := w.Close(ctx)

				return err
			}, "Close returns the error that the Sink returns for a context that ended")
		})

		t.Run("returns io.ErrShortWrite for a write of fewer bytes without an error", func(t *testing.T) {
			t.Parallel()
			w, err := sstable.NewWriter(shortSink{}, sstable.WriterOptions{})
			assert.NoError(t, err, "NewWriter accepts the zero options")
			assert.NoError(t, w.Add(t.Context(), []byte("a"), 1, nil), "Add keeps the entry in its block")
			_, err = w.Close(t.Context())
			assert.ErrorIs(t, err, io.ErrShortWrite, "Close refuses the short write")
		})

		t.Run("stores a block that compression shrinks by exactly an eighth as it is", func(t *testing.T) {
			t.Parallel()
			// The block of one entry with a value of zeros zero bytes has 32
			// bytes, and its encoding has 28.
			const zeros = 12
			es := []entry{{key: []byte("k"), seq: 1, value: make([]byte, zeros)}}
			l := newLayout(t, sstable.WriterOptions{}, es)
			raw := l.data[l.blocks[0].off : l.blocks[0].off+l.blocks[0].n]
			enc, err := snappy.AppendEncode(nil, raw)
			assert.NoError(t, err, "the codec encodes the block")
			assert.Length(t, enc, len(raw)-len(raw)/8, "the encoding has seven eighths of the bytes of the block")
			compressed := writeTable(t, sstable.WriterOptions{Compression: sstable.SnappyCompression}, es)
			assert.Equal(t, compressed, l.data, "the block is stored as it is")
		})

		t.Run("stores the blocks that compression shrinks by more than an eighth compressed", func(t *testing.T) {
			t.Parallel()
			uncompressed := writeTable(t, sstable.WriterOptions{}, goldenTable())
			compressed := writeTable(t, sstable.WriterOptions{Compression: sstable.SnappyCompression}, goldenTable())
			assert.InRange(t, len(compressed), 0, float64(len(uncompressed))*7/8, "the repeated values compress")
		})

		for _, tt := range goldenFiles {
			t.Run("writes the bytes of the golden table for "+tt.name, func(t *testing.T) {
				t.Parallel()
				golden.Match(t, tt.file, writeTable(t, tt.opts, goldenTableOf(tt.opts), tt.dels...),
					golden.ShouldUpdate())
			})
		}

		t.Run("writes the data blocks of a table without partitions in partitions", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "a table in partitions has the data blocks of the table without them", func(c *prop.Case) {
				opts := c.Draw(prop.SampledFrom(settings[:4]...), "setting").opts
				es := c.Draw(entries, "entries")
				flat := newLayout(c, opts, es)
				opts.PartitionSize = c.Draw(prop.Integer(1, 1024), "partition size")
				parts := newLayout(c, opts, es)
				assert.Equal(c, parts.blocks, flat.blocks, "the tables have the same places of data blocks")
				end := 0
				if n := len(flat.blocks); n != 0 {
					end = flat.blocks[n-1].off + flat.blocks[n-1].n + trailerLen
				}
				assert.Equal(c, parts.data[:end], flat.data[:end], "the tables have the same bytes of data blocks")
			})
		})

		t.Run("ends an index partition at the entry that brings it to PartitionSize bytes", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "an index partition has PartitionSize bytes of entries only with its last entry",
				func(c *prop.Case) {
					size := c.Draw(prop.Integer(1, 1024), "partition size")
					opts := sstable.WriterOptions{
						BlockSize: c.Draw(prop.Integer(1, 512), "block size"), PartitionSize: size,
					}
					l := newLayout(c, opts, c.Draw(entries, "entries"))
					for i, p := range l.indexParts {
						// Every entry of an index partition is a restart point, so
						// the last restart offset is the size of the entries before
						// the last entry.
						part := l.data[p.off : p.off+p.n]
						offsets := restarts(part)
						assert.InRange(c, offsets[len(offsets)-1], 0, float64(size-1),
							"the entries before the last entry have fewer than PartitionSize bytes")
						if i < len(l.indexParts)-1 {
							assert.InRange(c, len(part)-4-4*len(offsets), float64(size), math.Inf(1),
								"the entries of a partition before the last have PartitionSize bytes or more")
						}
					}
				})
		})

		t.Run("ends a filter partition at the most keys up to which every count fits", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "a full filter partition has the most keys up to which every count fits PartitionSize",
				func(c *prop.Case) {
					bits := c.Draw(prop.SampledFrom(4, 8, 16), "filter bits")
					size := c.Draw(prop.Integer(1, 1024), "partition size")
					es := c.Draw(entries, "entries")
					l := newLayout(c, sstable.WriterOptions{FilterBits: bits, PartitionSize: size}, es)
					// counts are the numbers of distinct keys of the partitions. A
					// partition has the keys after the last key of the partition
					// before it, up to its own last key.
					counts := make([]int, len(l.filterParts))
					i := 0
					for j, e := range es {
						if j > 0 && bytes.Equal(e.key, es[j-1].key) {
							continue
						}
						for i < len(counts) && bytes.Compare(e.key, l.filterParts[i].last) > 0 {
							i++
						}
						assert.InRange(c, i, 0, float64(len(counts)-1), "a filter partition contains each key")
						counts[i]++
					}
					for j, p := range l.filterParts {
						n, err := filter.FuseSize(counts[j], bits)
						assert.NoError(c, err, "FuseSize measures the encoding of the keys of a partition")
						assert.Equal(c, p.n, n, "a filter partition is the encoding of its keys")
						if counts[j] > 1 {
							assert.InRange(c, p.n, 0, float64(size),
								"a filter partition of more keys than one fits PartitionSize")
						}
					}
					if len(counts) < 2 {
						return
					}
					most := counts[0]
					if most > 1 {
						n, err := filter.MaxFuseSize(most, bits)
						assert.NoError(c, err, "MaxFuseSize measures the keys of a full partition")
						assert.InRange(c, n, 0, float64(size),
							"every count up to the keys of a full partition fits PartitionSize")
					}
					n, err := filter.FuseSize(most+1, bits)
					assert.NoError(c, err, "FuseSize measures the encoding of one key more")
					assert.InRange(c, n, float64(size+1), math.Inf(1), "one key more does not fit PartitionSize")
					for _, count := range counts[1 : len(counts)-1] {
						assert.Equal(c, count, most, "every partition before the last has as many keys as the first")
					}
					assert.InRange(c, counts[len(counts)-1], 1, float64(most),
						"the last partition has at most as many keys as the first")
				})
		})

		t.Run("ends a filter partition at a count that takes exactly PartitionSize bytes", func(t *testing.T) {
			t.Parallel()
			size, err := filter.MaxFuseSize(100, 8)
			assert.NoError(t, err, "MaxFuseSize measures 100 keys")
			// most is the largest count up to which every count fits size bytes.
			most := 100
			for {
				n, err := filter.MaxFuseSize(most+1, 8)
				assert.NoError(t, err, "MaxFuseSize measures one key more")
				if n > size {
					break
				}
				most++
			}
			es := make([]entry, 0, 3*most)
			for i := range uint32(3 * most) {
				es = append(es, entry{key: binary.BigEndian.AppendUint32(nil, i), seq: 1})
			}
			l := newLayout(t, sstable.WriterOptions{FilterBits: 8, PartitionSize: size}, es)
			assert.NotEmpty(t, l.filterParts, "the table has filter partitions")
			assert.Equal(t, int(binary.BigEndian.Uint32(l.filterParts[0].last))+1, most,
				"the first partition has the most keys up to which every count fits PartitionSize")
		})

		t.Run("keeps a filter partition of fewer keys than the first within PartitionSize", func(t *testing.T) {
			t.Parallel()
			// The encoding of 242 keys of 8 bits takes 466 bytes, and the
			// encoding of 256 keys takes at most 408. The keys of the table are
			// their numbers in 4 bytes.
			const size, keys = 408, 498
			es := make([]entry, 0, keys)
			for i := range uint32(keys) {
				es = append(es, entry{key: binary.BigEndian.AppendUint32(nil, i), seq: 1})
			}
			l := newLayout(t, sstable.WriterOptions{FilterBits: 8, PartitionSize: size}, es)
			assert.InRange(t, len(l.filterParts), 2, math.Inf(1), "the keys fill more than one partition")
			for _, p := range l.filterParts {
				expect.InRange(t, p.n, 0, size, "every filter partition of more keys than one fits PartitionSize")
			}
		})

		t.Run("releases the buffers of a Writer above MaxRetained", func(t *testing.T) {
			t.Parallel()
			opts := settings[3].opts
			opts.MaxRetained = 1
			w := filledWriter(t, discardSink{}, opts)
			_, err := w.Close(t.Context())
			assert.NoError(t, err, "Close writes the table")
			assert.Equal(t, w.Footprint(), 0, "Close releases every buffer")
		})

		t.Run("keeps the buffers of a Writer within MaxRetained", func(t *testing.T) {
			t.Parallel()
			opts := settings[3].opts
			opts.MaxRetained = 1 << 30
			w := filledWriter(t, discardSink{}, opts)
			_, err := w.Close(t.Context())
			assert.NoError(t, err, "Close writes the table")
			assert.NotEqual(t, w.Footprint(), 0, "Close keeps the buffers")
		})

		t.Run("keeps the buffers of a Writer whose footprint equals MaxRetained", func(t *testing.T) {
			t.Parallel()
			// A Writer without a bound grows its buffers as the Writer with the
			// bound does.
			free := filledWriter(t, discardSink{}, settings[3].opts)
			_, err := free.Close(t.Context())
			assert.NoError(t, err, "Close writes the table of the Writer without a bound")
			opts := settings[3].opts
			opts.MaxRetained = free.Footprint()
			w := filledWriter(t, discardSink{}, opts)
			_, err = w.Close(t.Context())
			assert.NoError(t, err, "Close writes the table")
			assert.Equal(t, w.Footprint(), opts.MaxRetained, "Close keeps the buffers at the bound")
		})

		t.Run("keeps the buffers after an error of the Sink", func(t *testing.T) {
			t.Parallel()
			// The one block of the table ends at Close, so Close makes the first
			// write.
			opts := sstable.WriterOptions{BlockSize: math.MaxUint32, FilterBits: 8, MaxRetained: 1}
			w := filledWriter(t, &failingSink{}, opts)
			_, err := w.Close(t.Context())
			assert.ErrorIs(t, err, errWrite, "Close returns the error of the Sink")
			assert.NotEqual(t, w.Footprint(), 0, "a Close that fails keeps the buffers")
		})
	})

	t.Run("Footprint", func(t *testing.T) {
		t.Parallel()

		t.Run("returns 0 for a new Writer", func(t *testing.T) {
			t.Parallel()
			w, err := sstable.NewWriter(discardSink{}, settings[3].opts)
			assert.NoError(t, err, "NewWriter accepts the options")
			assert.Equal(t, w.Footprint(), 0, "a new Writer has no buffers")
		})

		t.Run("returns the capacity of every buffer in bytes", func(t *testing.T) {
			t.Parallel()
			// The Writer has every buffer: the partitions of both filters and the
			// fragments.
			opts := settings[8].opts
			opts.Compression = sstable.SnappyCompression
			w := filledWriter(t, discardSink{}, opts)
			for _, d := range goldenDeletes {
				assert.NoError(t, w.AddRangeDelete(t.Context(), d.start, d.end, d.seq),
					"AddRangeDelete adds the fragment")
			}
			_, err := w.Close(t.Context())
			assert.NoError(t, err, "Close writes the table")
			// capacities sums the bytes of the slices of a struct and of the
			// structs in it. The smallest key and the start of the first
			// fragment are copies for the Summary.
			var capacities func(v reflect.Value) int
			capacities = func(v reflect.Value) int {
				n := 0
				for field, f := range v.Fields() {
					if f.Kind() == reflect.Struct {
						n += capacities(f)
					} else if f.Kind() == reflect.Slice && field.Name != "smallest" && field.Name != "fragStart" {
						n += f.Cap() * int(f.Type().Elem().Size())
					}
				}

				return n
			}
			assert.Equal(t, w.Footprint(), capacities(reflect.ValueOf(w).Elem()),
				"Footprint returns the bytes of the slices of the Writer and of its filter builder")
		})

		t.Run("grows with the entries of a table", func(t *testing.T) {
			t.Parallel()
			w, err := sstable.NewWriter(discardSink{}, settings[3].opts)
			assert.NoError(t, err, "NewWriter accepts the options")
			es := goldenTable()
			i := 0
			assert.Monotonic(t, w.Footprint, func() error {
				addErr := w.Add(t.Context(), es[i].key, es[i].seq, es[i].value)
				i++

				return addErr
			}, len(es), "Footprint does not fall while a table grows")
			_, err = w.Close(t.Context())
			assert.NoError(t, err, "Close writes the table")
			assert.NotEqual(t, w.Footprint(), 0, "the buffers of the table take memory")
		})
	})

	t.Run("Reset", func(t *testing.T) {
		t.Parallel()

		t.Run("writes the table and the summary of a new Writer after any first table", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "a Writer after Reset writes what a new Writer writes", func(c *prop.Case) {
				opts := c.Draw(prop.SampledFrom(settings...), "setting").opts
				opts.MaxRetained = c.Draw(prop.SampledFrom(0, 1, 1<<30), "max retained")
				first, second := c.Draw(entries, "first entries"), c.Draw(entries, "second entries")
				firstDels, secondDels := c.Draw(fragments, "first fragments"), c.Draw(fragments, "second fragments")
				w, err := sstable.NewWriter(discardSink{}, opts)
				assert.NoError(c, err, "NewWriter accepts the options")
				for _, e := range first[:c.Draw(prop.Integer(0, len(first)), "first added")] {
					assert.NoError(c, w.Add(c.Context(), e.key, e.seq, e.value),
						"Add adds the entry of the first table")
				}
				for _, d := range firstDels[:c.Draw(prop.Integer(0, len(firstDels)), "first fragments added")] {
					assert.NoError(c, w.AddRangeDelete(c.Context(), d.start, d.end, d.seq),
						"AddRangeDelete adds the fragment of the first table")
				}
				if c.Draw(prop.Boolean(), "first closed") {
					_, err = w.Close(c.Context())
					assert.NoError(c, err, "Close writes the first table")
				}

				var buf bufferSink
				w.Reset(&buf)
				for _, e := range second {
					assert.NoError(c, w.Add(c.Context(), e.key, e.seq, e.value),
						"Add adds the entry of the second table")
				}
				for _, d := range secondDels {
					assert.NoError(c, w.AddRangeDelete(c.Context(), d.start, d.end, d.seq),
						"AddRangeDelete adds the fragment of the second table")
				}
				sum, err := w.Close(c.Context())
				assert.NoError(c, err, "Close writes the second table")
				assert.Equal(c, buf.Bytes(), writeTable(c, opts, second, secondDels...),
					"the Writer writes the bytes of a new Writer")
				assert.Equal(c, sum, openTable(c, buf.Bytes(), sstable.ReaderOptions{}).Summary(),
					"Close returns the summary of the second table")
			})
		})

		t.Run("keeps the keys of the summary of the table before", func(t *testing.T) {
			t.Parallel()
			var buf bufferSink
			w, err := sstable.NewWriter(&buf, settings[7].opts)
			assert.NoError(t, err, "NewWriter accepts the options")
			for _, e := range goldenTable() {
				assert.NoError(t, w.Add(t.Context(), e.key, e.seq, e.value), "Add adds the entry of the first table")
			}
			for _, d := range goldenDeletes {
				assert.NoError(t, w.AddRangeDelete(t.Context(), d.start, d.end, d.seq),
					"AddRangeDelete adds the fragment of the first table")
			}
			sum, err := w.Close(t.Context())
			assert.NoError(t, err, "Close writes the first table")
			want := sum
			want.SmallestKey, want.LargestKey = bytes.Clone(sum.SmallestKey), bytes.Clone(sum.LargestKey)
			want.RangeDeleteStart = bytes.Clone(sum.RangeDeleteStart)
			want.RangeDeleteEnd = bytes.Clone(sum.RangeDeleteEnd)
			w.Reset(discardSink{})
			for _, key := range []string{"other a", "other b"} {
				assert.NoError(t, w.Add(t.Context(), []byte(key), 1, nil), "Add adds the entry of the second table")
				assert.NoError(t, w.AddRangeDelete(t.Context(), []byte(key), []byte(key+" end"), 1),
					"AddRangeDelete adds the fragment of the second table")
			}
			_, err = w.Close(t.Context())
			assert.NoError(t, err, "Close writes the second table")
			assert.Equal(t, sum, want, "the second table leaves the summary of the first table unchanged")
		})

		t.Run("clears the error of the Sink of the table before", func(t *testing.T) {
			t.Parallel()
			w, err := sstable.NewWriter(&failingSink{}, sstable.WriterOptions{BlockSize: 1})
			assert.NoError(t, err, "NewWriter accepts the options")
			assert.ErrorIs(t, w.Add(t.Context(), []byte("a"), 1, nil), errWrite, "Add returns the error of the Sink")
			var buf bufferSink
			w.Reset(&buf)
			for _, e := range goldenTable() {
				assert.NoError(t, w.Add(t.Context(), e.key, e.seq, e.value), "Add adds the entry after Reset")
			}
			_, err = w.Close(t.Context())
			assert.NoError(t, err, "Close writes the table after Reset")
			r := openTable(t, buf.Bytes(), sstable.ReaderOptions{})
			got, err := scanAll(t.Context(), r.NewIter(sstable.IterOptions{}))
			assert.NoError(t, err, "the table reads")
			assert.Equal(t, got, goldenTable(), "the table contains the entries after Reset")
		})

		t.Run("clears ErrClosed", func(t *testing.T) {
			t.Parallel()
			w, err := sstable.NewWriter(discardSink{}, sstable.WriterOptions{})
			assert.NoError(t, err, "NewWriter accepts the zero options")
			_, err = w.Close(t.Context())
			assert.NoError(t, err, "Close writes the first table")
			var buf bufferSink
			w.Reset(&buf)
			assert.NoError(t, w.Add(t.Context(), []byte("a"), 1, []byte("v")), "Add adds an entry after Reset")
			sum, err := w.Close(t.Context())
			assert.NoError(t, err, "Close writes the second table")
			assert.Equal(t, sum.Entries, int64(1), "the second table contains the entry")
		})

		t.Run("releases the buffers of a Writer above MaxRetained", func(t *testing.T) {
			t.Parallel()
			opts := settings[3].opts
			opts.MaxRetained = 1
			w := filledWriter(t, discardSink{}, opts)
			w.Reset(discardSink{})
			assert.Equal(t, w.Footprint(), 0, "Reset releases every buffer")
		})

		t.Run("keeps every buffer for a MaxRetained of 0", func(t *testing.T) {
			t.Parallel()
			w := filledWriter(t, discardSink{}, settings[3].opts)
			_, err := w.Close(t.Context())
			assert.NoError(t, err, "Close writes the table")
			assert.NotEqual(t, w.Footprint(), 0, "the table grows the buffers")
			assert.Pure(t, w.Footprint, func() { w.Reset(discardSink{}) }, "Reset keeps every buffer")
		})
	})
}

// TestWriterAllocs checks the allocation contract of Add. MaxAllocs counts
// the allocations of the whole process, so the test does not run in
// parallel.
func TestWriterAllocs(t *testing.T) {
	value := make([]byte, 100)

	t.Run("Add", func(t *testing.T) {
		for _, tt := range settings {
			t.Run("does not allocate once its buffers have room in a table "+tt.name, func(t *testing.T) {
				w, err := sstable.NewWriter(discardSink{}, tt.opts)
				assert.NoError(t, err, "NewWriter accepts the options")
				key := make([]byte, 16)
				i := uint64(0)
				add := func() {
					binary.BigEndian.PutUint64(key[8:], i)
					err = w.Add(t.Context(), key, 1, value)
					i++
				}
				for range warmEntries {
					add()
				}
				expect.MaxAllocs(t, add, 0, "Add does not allocate once its buffers have room")
				assert.NoError(t, err, "the test measures Adds that succeed")
			})
		}

		// The first Add copies its key for the summary and grows the empty
		// buffers of the entry and the block. With a filter it also grows
		// the hashes.
		tests := []struct {
			name     string
			giveOpts sstable.WriterOptions
			want     uint64
		}{
			{name: "allocates six times for the first entry without a filter", want: 6},
			{
				name:     "allocates seven times for the first entry with a filter",
				giveOpts: sstable.WriterOptions{FilterBits: 8},
				want:     7,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				key, firstValue := []byte("a"), []byte("v")
				var err error
				expect.MaxAllocsWithSetup(t, func() *sstable.Writer {
					w, newErr := sstable.NewWriter(discardSink{}, tt.giveOpts)
					assert.NoError(t, newErr, "NewWriter accepts the options")

					return w
				}, func(w *sstable.Writer) { err = w.Add(t.Context(), key, 1, firstValue) }, tt.want,
					"the first Add allocates the copy of its key and the first growth of its buffers")
				assert.NoError(t, err, "the test measures Adds that succeed")
			})
		}
	})

	t.Run("AddRangeDelete", func(t *testing.T) {
		w, err := sstable.NewWriter(discardSink{}, sstable.WriterOptions{})
		assert.NoError(t, err, "NewWriter accepts the zero options")
		// Each fragment ends before the start of the next one.
		start, end := make([]byte, 16), make([]byte, 16)
		i := uint64(0)
		add := func() {
			binary.BigEndian.PutUint64(start[8:], 2*i)
			binary.BigEndian.PutUint64(end[8:], 2*i+1)
			err = w.AddRangeDelete(t.Context(), start, end, 1)
			i++
		}
		for range warmEntries {
			add()
		}
		expect.MaxAllocs(t, add, 0, "AddRangeDelete does not allocate once its buffers have room")
		assert.NoError(t, err, "the test measures calls that succeed")
	})

	t.Run("Footprint", func(t *testing.T) {
		w := filledWriter(t, discardSink{}, settings[3].opts)
		n := 0
		expect.MaxAllocs(t, func() { n = w.Footprint() }, 0, "Footprint does not allocate")
		assert.NotEqual(t, n, 0, "the test measures the footprint of a table")
	})

	t.Run("Reset", func(t *testing.T) {
		t.Run("does not allocate", func(t *testing.T) {
			w, err := sstable.NewWriter(discardSink{}, settings[3].opts)
			assert.NoError(t, err, "NewWriter accepts the options")
			expect.MaxAllocs(t, func() { w.Reset(discardSink{}) }, 0, "Reset does not allocate")
		})

		for _, tt := range settings {
			t.Run("reuses the buffers for a table "+tt.name, func(t *testing.T) {
				var buf bufferSink
				w, err := sstable.NewWriter(&buf, tt.opts)
				assert.NoError(t, err, "NewWriter accepts the options")
				es := goldenTable()
				var sum sstable.Summary
				expect.MaxAllocs(t, func() {
					buf.Reset()
					w.Reset(&buf)
					for _, e := range es {
						err = cmp.Or(err, w.Add(t.Context(), e.key, e.seq, e.value))
					}
					var closeErr error
					sum, closeErr = w.Close(t.Context())
					err = cmp.Or(err, closeErr)
				}, 2, "a table after Reset allocates the copies of its smallest key and its largest key")
				assert.NoError(t, err, "the test measures tables that the Writer writes")
				assert.Equal(t, sum.Entries, int64(goldenEntries), "the test measures tables of every entry")
			})
		}

		t.Run("reuses the buffers for a table with fragments", func(t *testing.T) {
			var buf bufferSink
			w, err := sstable.NewWriter(&buf, settings[8].opts)
			assert.NoError(t, err, "NewWriter accepts the options")
			es := goldenTable()
			var sum sstable.Summary
			expect.MaxAllocs(t, func() {
				buf.Reset()
				w.Reset(&buf)
				for _, e := range es {
					err = cmp.Or(err, w.Add(t.Context(), e.key, e.seq, e.value))
				}
				for _, d := range goldenDeletes {
					err = cmp.Or(err, w.AddRangeDelete(t.Context(), d.start, d.end, d.seq))
				}
				var closeErr error
				sum, closeErr = w.Close(t.Context())
				err = cmp.Or(err, closeErr)
			}, 4, "a table after Reset allocates the copies of the bounds of its entries and its fragments")
			assert.NoError(t, err, "the test measures tables that the Writer writes")
			assert.Equal(t, sum.RangeDeletes, int64(len(goldenDeletes)), "the test measures tables of every fragment")
		})
	})
}

// BenchmarkWriter reports the cost of Add for each setting, and fails when
// Add allocates.
func BenchmarkWriter(b *testing.B) {
	value := make([]byte, 100)

	b.Run("Add", func(b *testing.B) {
		for _, tt := range settings {
			b.Run(tt.name, func(b *testing.B) {
				w, err := sstable.NewWriter(discardSink{}, tt.opts)
				assert.NoError(b, err, "NewWriter accepts the options")
				key := make([]byte, 16)
				i := uint64(0)

				c := bench.Start(b).MaxAllocs(0)
				defer c.End()

				for c.Loop() {
					binary.BigEndian.PutUint64(key[8:], i)
					err = w.Add(b.Context(), key, 1, value)
					i++
				}

				assert.NoError(b, err, "the benchmark measures Adds that succeed")
			})
		}
	})

	b.Run("AddRangeDelete", func(b *testing.B) {
		w, err := sstable.NewWriter(discardSink{}, sstable.WriterOptions{})
		assert.NoError(b, err, "NewWriter accepts the zero options")
		// Each fragment ends before the start of the next one. The first
		// fragment of a table allocates the copy of its start.
		start, end := make([]byte, 16), make([]byte, 16)
		i := uint64(0)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			if i == benchFragments {
				w.Reset(discardSink{})
				i = 0
			}
			binary.BigEndian.PutUint64(start[8:], 2*i)
			binary.BigEndian.PutUint64(end[8:], 2*i+1)
			err = w.AddRangeDelete(b.Context(), start, end, 1)
			i++
		}

		assert.NoError(b, err, "the benchmark measures calls that succeed")
	})

	// A Writer that Reset reuses allocates the copies of the smallest key and
	// the largest key of each table.
	b.Run("Reset", func(b *testing.B) {
		for _, tt := range settings {
			b.Run(tt.name, func(b *testing.B) {
				var buf bufferSink
				w, err := sstable.NewWriter(&buf, tt.opts)
				assert.NoError(b, err, "NewWriter accepts the options")
				es := goldenTable()
				var sum sstable.Summary

				c := bench.Start(b).MaxAllocs(2)
				defer c.End()

				for c.Loop() {
					buf.Reset()
					w.Reset(&buf)
					for _, e := range es {
						err = cmp.Or(err, w.Add(b.Context(), e.key, e.seq, e.value))
					}
					var closeErr error
					sum, closeErr = w.Close(b.Context())
					err = cmp.Or(err, closeErr)
				}

				assert.NoError(b, err, "the benchmark measures tables that the Writer writes")
				assert.Equal(b, sum.Entries, int64(goldenEntries), "the benchmark measures tables of every entry")
			})
		}
	})
}

// writeSteps draws a sorted run of entries, a run of fragments in the order
// of a table and the options of a Writer, and calls the Writer through a
// machine of Add, AddRangeDelete, Close and Reset. An Add takes the next
// entry of the run, and an Add out of order takes an entry at or before the
// last entry that Add accepted. AddRangeDelete takes the fragments the same
// way. The model is the part of each run that the Writer accepted since the
// last Reset, and whether Close closed the Writer. Close must write the bytes
// that a new Writer writes for those parts, and return the summary of the
// table. The buffers that Close and Reset keep must fit MaxRetained. Settle
// closes the Writer, so every case checks a table.
func writeSteps(c *prop.Case) {
	es, dels := c.Draw(entries, "entries"), c.Draw(fragments, "fragments")
	opts := sstable.WriterOptions{
		BlockSize:       c.Draw(prop.Integer(1, 512), "block size"),
		RestartInterval: c.Draw(prop.Integer(1, 8), "restart interval"),
		Compression:     c.Draw(prop.SampledFrom(sstable.NoCompression, sstable.SnappyCompression), "compression"),
		FilterBits:      c.Draw(prop.SampledFrom(0, 8), "filter bits"),
		MaxRetained:     c.Draw(prop.SampledFrom(0, 1, 1<<30), "max retained"),
		PartitionSize:   c.Draw(prop.SampledFrom(0, 1, 32, partitionBytes, 1024), "partition size"),
	}
	if opts.FilterBits != 0 && c.Draw(prop.Boolean(), "prefix") {
		opts.Prefix = fixedPrefix{name: firstBytes, n: c.Draw(prop.Integer(0, 2), "prefix length")}
	}
	var sink bufferSink
	w, err := sstable.NewWriter(&sink, opts)
	assert.NoError(c, err, "NewWriter accepts the options")

	// The table of the Writer has the entries es[start:next] and the
	// fragments dels[from:to]. closed reports whether Close closed the
	// Writer.
	start, next, from, to, closed := 0, 0, 0, 0, false

	// closeTable closes the Writer, and checks the table and the buffers
	// that Close keeps.
	closeTable := func(c *prop.Case) {
		sum, err := w.Close(c.Context())
		if closed {
			assert.ErrorIs(c, err, sstable.ErrClosed, "a second Close returns ErrClosed")

			return
		}
		assert.NoError(c, err, "Close writes the table")
		closed = true
		assert.Equal(c, sink.Bytes(), writeTable(c, opts, es[start:next], dels[from:to]...),
			"Close writes the bytes of a new Writer of the entries and the fragments that the Writer accepted")
		assert.Equal(c, sum, openTable(c, sink.Bytes(), sstable.ReaderOptions{}).Summary(),
			"Close returns the summary of the table")
		if opts.MaxRetained != 0 {
			assert.InRange(c, w.Footprint(), 0, float64(opts.MaxRetained), "Close keeps buffers within MaxRetained")
		}
	}

	stateful.Steps(c, stateful.Machine[struct{}]{
		Actions: []stateful.Action[struct{}]{
			{
				Name:    writeAdd,
				Weight:  8,
				Enabled: func(struct{}) bool { return next < len(es) },
				Run: func(c *prop.Case, _ int, _ any) {
					e := es[next]
					err := w.Add(c.Context(), e.key, e.seq, e.value)
					if closed {
						assert.ErrorIs(c, err, sstable.ErrClosed, "Add returns ErrClosed after Close")

						return
					}
					assert.NoError(c, err, "Add accepts the entry after the last entry")
					next++
				},
			},
			{
				Name:    writeOutOfOrder,
				Enabled: func(struct{}) bool { return next > start },
				Input: func(c *prop.Case, _ struct{}) any {
					return c.Draw(prop.Integer(start, next-1), "entry")
				},
				Run: func(c *prop.Case, _ int, in any) {
					e := es[in.(int)]
					err := w.Add(c.Context(), e.key, e.seq, e.value)
					if closed {
						assert.ErrorIs(c, err, sstable.ErrClosed, "Add returns ErrClosed after Close")

						return
					}
					assert.ErrorIs(c, err, sstable.ErrOrder, "Add returns ErrOrder for an entry at or before the last")
				},
			},
			{
				Name:    writeRangeDelete,
				Weight:  4,
				Enabled: func(struct{}) bool { return to < len(dels) },
				Run: func(c *prop.Case, _ int, _ any) {
					d := dels[to]
					err := w.AddRangeDelete(c.Context(), d.start, d.end, d.seq)
					if closed {
						assert.ErrorIs(c, err, sstable.ErrClosed, "AddRangeDelete returns ErrClosed after Close")

						return
					}
					assert.NoError(c, err, "AddRangeDelete accepts the fragment after the last fragment")
					to++
				},
			},
			{
				Name:    writeRangeDeleteOutOfOrder,
				Enabled: func(struct{}) bool { return to > from },
				Input: func(c *prop.Case, _ struct{}) any {
					return c.Draw(prop.Integer(from, to-1), "fragment")
				},
				Run: func(c *prop.Case, _ int, in any) {
					d := dels[in.(int)]
					err := w.AddRangeDelete(c.Context(), d.start, d.end, d.seq)
					if closed {
						assert.ErrorIs(c, err, sstable.ErrClosed, "AddRangeDelete returns ErrClosed after Close")

						return
					}
					assert.ErrorIs(c, err, sstable.ErrOrder,
						"AddRangeDelete returns ErrOrder for a fragment at or before the last")
				},
			},
			{
				Name: writeClose,
				Run:  func(c *prop.Case, _ int, _ any) { closeTable(c) },
			},
			{
				Name: writeReset,
				Run: func(c *prop.Case, _ int, _ any) {
					sink.Reset()
					w.Reset(&sink)
					start, from, closed = next, to, false
					if opts.MaxRetained != 0 {
						assert.InRange(c, w.Footprint(), 0, float64(opts.MaxRetained),
							"Reset keeps buffers within MaxRetained")
					}
				},
			},
		},
		Settle: func(c *prop.Case, _ struct{}) { closeTable(c) },
	})
}

// countsOf returns the counts of es and dels that WriterOptions.MaxSize
// takes: the number of entries, the bytes of their keys and of their values,
// the length of the longest key, the number of fragments and the bytes of
// their starts and their ends.
func countsOf(es []entry, dels ...rangeDelete) sstable.Counts {
	c := sstable.Counts{Entries: int64(len(es)), RangeDeletes: int64(len(dels))}
	for _, e := range es {
		c.KeyBytes += int64(len(e.key))
		c.ValueBytes += int64(len(e.value))
		c.Longest = max(c.Longest, len(e.key))
	}
	for _, d := range dels {
		c.RangeDeleteBytes += int64(len(d.start) + len(d.end))
	}

	return c
}

// runAlone runs the case of t alone in a child process of the test binary,
// whose heap has freed no pages yet, and reports true. In the child it
// reports false, so the case runs its body there. A case of an allocation of
// gigabytes runs alone, because the runtime zeroes a large allocation that
// reuses pages that a collection freed, and so touches every page. The child
// writes its coverage into the directory of the parent.
func runAlone(t *testing.T) bool {
	t.Helper()

	if os.Getenv(childEnv) != "" {
		return false
	}
	exe, err := os.Executable()
	assert.NoError(t, err, "the test binary has a path")
	levels := strings.Split(t.Name(), "/")
	for i, level := range levels {
		levels[i] = "^" + regexp.QuoteMeta(level) + "$"
	}
	args := []string{"-test.run=" + strings.Join(levels, "/"), "-test.count=1"}
	if dir := flag.Lookup("test.gocoverdir"); dir != nil && dir.Value.String() != "" {
		args = append(args, "-test.gocoverdir="+dir.Value.String())
	}
	if deadline, ok := t.Deadline(); ok {
		args = append(args, "-test.timeout="+time.Until(deadline).String())
	}
	cmd := exec.CommandContext(t.Context(), exe, args...)
	cmd.Env = append(os.Environ(), childEnv+"=1")
	alone.Lock()
	out, err := cmd.CombinedOutput()
	alone.Unlock()
	assert.NoError(t, err, "the case passes in a child process, whose output is:\n"+string(out))

	return true
}

// randomBytes returns n bytes of a PCG of seed, which Snappy does not shrink.
func randomBytes(n int, seed uint64) []byte {
	r := rand.New(rand.NewPCG(seed, seed))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.Uint32())
	}

	return b
}

// filledWriter returns a Writer onto sink under opts, after an Add of every
// entry of the golden table.
func filledWriter(tb testing.TB, sink sstable.Sink, opts sstable.WriterOptions) *sstable.Writer {
	tb.Helper()

	w, err := sstable.NewWriter(sink, opts)
	assert.NoError(tb, err, "NewWriter accepts the options")
	for _, e := range goldenTable() {
		assert.NoError(tb, w.Add(tb.Context(), e.key, e.seq, e.value), "Add adds the entry")
	}

	return w
}
