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
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/golden"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/compress/snappy"
	"go.thesmos.sh/core/sstable"
)

// warmEntries is the number of entries that the allocation test of Add adds
// before it counts. They grow the index and the hashes so far that a growth
// during a count leaves room for every call of the next count.
const warmEntries = 4096

// childEnv is set in the environment of the child process in which a case
// runs alone.
const childEnv = "CORE_TEST_CHILD"

// errWrite is the error of a Sink that fails.
var errWrite = errors.New("write failed")

// goldenFiles are the golden tables: the entries of goldenTable under the
// zero options and under each setting of settings.
var goldenFiles = []struct {
	name string
	file string
	opts sstable.WriterOptions
}{
	{name: "the zero options", file: "default.sst"},
	{name: settings[0].name, file: "uncompressed.sst", opts: settings[0].opts},
	{name: settings[1].name, file: "uncompressed-filter.sst", opts: settings[1].opts},
	{name: settings[2].name, file: "compressed.sst", opts: settings[2].opts},
	{name: settings[3].name, file: "compressed-filter.sst", opts: settings[3].opts},
}

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

func TestWriter(t *testing.T) {
	t.Parallel()

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

	t.Run("NewWriter", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give sstable.WriterOptions
		}{
			{name: "returns ErrOptions for a negative BlockSize", give: sstable.WriterOptions{BlockSize: -1}},
			{
				name: "returns ErrOptions for a BlockSize above 2^32 - 1",
				give: sstable.WriterOptions{BlockSize: math.MaxUint32 + 1},
			},
			{
				name: "returns ErrOptions for a negative RestartInterval",
				give: sstable.WriterOptions{RestartInterval: -1},
			},
			{name: "returns ErrOptions for an unknown Compression", give: sstable.WriterOptions{Compression: 2}},
			{name: "returns ErrOptions for 3 bits of filter", give: sstable.WriterOptions{FilterBits: 3}},
			{name: "returns ErrOptions for 17 bits of filter", give: sstable.WriterOptions{FilterBits: 17}},
			{name: "returns ErrOptions for negative bits of filter", give: sstable.WriterOptions{FilterBits: -8}},
		}
		for _, tt := range tests {
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

		for _, bits := range []int{4, 8, 10, 12, 16} {
			t.Run("returns a Writer for a filter of "+strconv.Itoa(bits)+" bits", func(t *testing.T) {
				t.Parallel()
				data := writeTable(t, sstable.WriterOptions{FilterBits: bits}, goldenTable())
				r := openTable(t, data, sstable.ReaderOptions{})
				assert.True(t, r.MayContain([]byte("key 000")), "the filter contains the keys of the table")
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
			// The key takes 4 GiB of address space. The runtime zeroes a large
			// allocation that reuses pages that a collection freed, and so
			// touches every page. The case therefore runs alone in a child
			// process of the test binary, whose heap has freed no pages yet.
			if os.Getenv(childEnv) == "" {
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
				out, err := cmd.CombinedOutput()
				assert.NoError(t, err, "the case passes in a child process, whose output is:\n"+string(out))

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

	t.Run("Close", func(t *testing.T) {
		t.Parallel()

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
				golden.Match(t, tt.file, writeTable(t, tt.opts, goldenTable()), golden.ShouldUpdate())
			})
		}
	})

	t.Run("Reset", func(t *testing.T) {
		t.Parallel()

		t.Run("writes the table and the summary of a new Writer after any first table", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "a Writer after Reset writes what a new Writer writes", func(c *prop.Case) {
				opts := c.Draw(prop.SampledFrom(settings...), "setting").opts
				first, second := c.Draw(entries, "first entries"), c.Draw(entries, "second entries")
				w, err := sstable.NewWriter(discardSink{}, opts)
				assert.NoError(c, err, "NewWriter accepts the options")
				for _, e := range first[:c.Draw(prop.Integer(0, len(first)), "first added")] {
					assert.NoError(c, w.Add(c.Context(), e.key, e.seq, e.value),
						"Add adds the entry of the first table")
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
				sum, err := w.Close(c.Context())
				assert.NoError(c, err, "Close writes the second table")
				assert.Equal(c, buf.Bytes(), writeTable(c, opts, second), "the Writer writes the bytes of a new Writer")
				assert.Equal(c, sum, openTable(c, buf.Bytes(), sstable.ReaderOptions{}).Summary(),
					"Close returns the summary of the second table")
			})
		})

		t.Run("keeps the keys of the summary of the table before", func(t *testing.T) {
			t.Parallel()
			var buf bufferSink
			w, err := sstable.NewWriter(&buf, settings[1].opts)
			assert.NoError(t, err, "NewWriter accepts the options")
			for _, e := range goldenTable() {
				assert.NoError(t, w.Add(t.Context(), e.key, e.seq, e.value), "Add adds the entry of the first table")
			}
			sum, err := w.Close(t.Context())
			assert.NoError(t, err, "Close writes the first table")
			want := sum
			want.SmallestKey, want.LargestKey = bytes.Clone(sum.SmallestKey), bytes.Clone(sum.LargestKey)
			w.Reset(discardSink{})
			for _, key := range []string{"other a", "other b"} {
				assert.NoError(t, w.Add(t.Context(), []byte(key), 1, nil), "Add adds the entry of the second table")
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
