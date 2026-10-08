// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tlog_test

import (
	"bytes"
	"math"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/tlog"
)

// bundled are the entries of the bundle of the cases of BundleEntries: a
// short entry, an empty one, and one longer than 255 bytes.
var bundled = [][]byte{[]byte("one"), {}, bytes.Repeat([]byte{7}, 300)}

func TestBundle(t *testing.T) {
	t.Parallel()

	var bundle []byte
	for _, e := range bundled {
		var err error
		bundle, err = tlog.AppendBundleEntry(bundle, e)
		assert.NoError(t, err, "AppendBundleEntry must succeed")
	}

	t.Run("AppendBundleEntry", func(t *testing.T) {
		t.Parallel()

		t.Run("prefixes the entry with its big-endian length", func(t *testing.T) {
			t.Parallel()
			b, err := tlog.AppendBundleEntry([]byte{0xAA}, []byte("abc"))
			assert.NoError(t, err, "AppendBundleEntry must succeed")
			assert.Equal(t, b, []byte{0xAA, 0x00, 0x03, 'a', 'b', 'c'}, "the entry must follow its length")
		})

		t.Run("appends an entry of 65535 bytes", func(t *testing.T) {
			t.Parallel()
			b, err := tlog.AppendBundleEntry(nil, make([]byte, math.MaxUint16))
			assert.NoError(t, err, "the largest entry must fit")
			assert.Length(t, b, 2+math.MaxUint16, "the entry must follow its length")
		})

		t.Run("returns ErrEntrySize with dst unchanged for an entry of 65536 bytes", func(t *testing.T) {
			t.Parallel()
			b, err := tlog.AppendBundleEntry([]byte{0xAA}, make([]byte, math.MaxUint16+1))
			expect.ErrorIs(t, err, tlog.ErrEntrySize, "a longer entry must be ErrEntrySize")
			expect.Equal(t, b, []byte{0xAA}, "dst must be returned unchanged")
		})
	})

	t.Run("BundleEntries", func(t *testing.T) {
		t.Parallel()

		t.Run("yields every entry in order", func(t *testing.T) {
			t.Parallel()
			var got [][]byte
			for e, err := range tlog.BundleEntries(bundle) {
				assert.NoError(t, err, "a well-formed bundle must iterate")
				got = append(got, e)
			}
			assert.Equal(t, got, bundled, "the entries must round-trip")
		})

		t.Run("yields entries without spare capacity", func(t *testing.T) {
			t.Parallel()
			for e, err := range tlog.BundleEntries(bundle) {
				assert.NoError(t, err, "a well-formed bundle must iterate")
				expect.Equal(t, cap(e), len(e), "an entry must not expose the next one")
			}
		})

		t.Run("yields every entry that AppendBundleEntry appends", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "BundleEntries must yield every entry that AppendBundleEntry appends", func(c *prop.Case) {
				entries := c.Draw(prop.List(prop.Bytes(prop.MaxSize(300)), prop.MaxSize(8)), "entries")

				var data []byte
				for _, e := range entries {
					var err error
					data, err = tlog.AppendBundleEntry(data, e)
					assert.NoError(c, err, "AppendBundleEntry must accept an entry of at most 300 bytes")
				}

				got := [][]byte{}
				for e, err := range tlog.BundleEntries(data) {
					assert.NoError(c, err, "a bundle of AppendBundleEntry must iterate")
					got = append(got, e)
				}
				assert.Equal(c, got, entries, "BundleEntries must yield the entries in order", assert.EquateEmpty())
			})
		})

		t.Run("yields an empty entry that ends the data", func(t *testing.T) {
			t.Parallel()
			data, err := tlog.AppendBundleEntry([]byte{0x00, 0x01, 'a'}, nil)
			assert.NoError(t, err, "AppendBundleEntry must succeed")

			var got [][]byte
			for e, err := range tlog.BundleEntries(data) {
				assert.NoError(t, err, "a bundle that ends with an empty entry must iterate")
				got = append(got, e)
			}
			assert.Equal(t, got, [][]byte{[]byte("a"), {}}, "the empty entry must follow the first")
		})

		tests := []struct {
			name string
			give []byte
		}{
			{name: "yields ErrBundle for a cut length", give: bundle[:len(bundle)-301]},
			{name: "yields ErrBundle for a cut entry", give: bundle[:len(bundle)-1]},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				var last error
				for _, err := range tlog.BundleEntries(tt.give) {
					last = err
				}
				assert.ErrorIs(t, last, tlog.ErrBundle, "the bundle must end with ErrBundle")
			})
		}

		t.Run("stops when the caller stops", func(t *testing.T) {
			t.Parallel()
			n := 0
			for range tlog.BundleEntries(bundle) {
				n++

				break
			}
			assert.Equal(t, n, 1, "the iteration must end at the break of the caller")
		})
	})

	t.Run("BundlePath", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the path of a full bundle", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tlog.BundlePath(1234067, fullWidth), "tile/entries/x001/x234/067",
				"BundlePath must write the C2SP path")
		})

		t.Run("returns the path of a partial bundle", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tlog.BundlePath(0, 5), "tile/entries/000.p/5", "BundlePath must write the C2SP path")
		})
	})
}

// TestBundleAllocs checks the allocation contract of each function.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
//
//nolint:paralleltest // see above
func TestBundleAllocs(t *testing.T) {
	entry := []byte("an entry")

	t.Run("AppendBundleEntry", func(t *testing.T) {
		dst := make([]byte, 0, 2+len(entry))

		var got []byte
		expect.MaxAllocs(t, func() { got, _ = tlog.AppendBundleEntry(dst[:0], entry) }, 0,
			"AppendBundleEntry must not allocate into a dst with room")
		assert.Length(t, got, 2+len(entry), "the test must measure an entry that AppendBundleEntry accepts")
	})

	t.Run("BundleEntries", func(t *testing.T) {
		bundle, err := tlog.AppendBundleEntry(nil, entry)
		assert.NoError(t, err, "AppendBundleEntry must succeed")

		n := 0
		expect.MaxAllocs(t, func() {
			for range tlog.BundleEntries(bundle) {
				n++
			}
		}, 0, "a range over BundleEntries must not allocate")
		assert.NotEqual(t, n, 0, "the test must measure entries")
	})

	t.Run("BundlePath", func(t *testing.T) {
		var got string
		expect.MaxAllocs(t, func() { got = tlog.BundlePath(1234067, fullWidth) }, 1,
			"BundlePath must allocate the path alone")
		assert.Equal(t, got, "tile/entries/x001/x234/067", "the test must measure the path of a full bundle")
	})
}

// BenchmarkBundle reports the cost of each function, and fails above the
// allocations that their contracts state.
func BenchmarkBundle(b *testing.B) {
	entry := []byte("an entry")

	b.Run("AppendBundleEntry", func(b *testing.B) {
		dst := make([]byte, 0, 2+len(entry))

		var got []byte

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, _ = tlog.AppendBundleEntry(dst[:0], entry)
		}

		assert.Length(b, got, 2+len(entry), "the benchmark must measure an entry that AppendBundleEntry accepts")
	})

	b.Run("BundleEntries", func(b *testing.B) {
		bundle, err := tlog.AppendBundleEntry(nil, entry)
		assert.NoError(b, err, "AppendBundleEntry must succeed")

		n := 0

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			for range tlog.BundleEntries(bundle) {
				n++
			}
		}

		assert.NotEqual(b, n, 0, "the benchmark must measure entries")
	})

	b.Run("BundlePath", func(b *testing.B) {
		var got string

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			got = tlog.BundlePath(1234067, fullWidth)
		}

		assert.Equal(b, got, "tile/entries/x001/x234/067", "the benchmark must measure the path of a full bundle")
	})
}
