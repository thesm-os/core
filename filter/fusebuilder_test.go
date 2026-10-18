// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package filter_test

import (
	"encoding/binary"
	"math"
	"math/bits"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/golden"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/filter"
)

// The counts of the check of MaxFuseSize. The size factor of a set falls
// where its count reaches a power of two, up to 2^20 hashes, so the check
// takes every count up to denseCounts and the counts at each power of two up
// to maxFuseCount.
const (
	denseCounts  = 1 << 12
	maxFuseCount = 1<<21 + 1
)

func TestFuseBuilder(t *testing.T) {
	t.Parallel()

	t.Run("Append", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name      string
			giveWidth int
		}{
			{name: "returns ErrFingerprintBits for fingerprints of 0 bits", giveWidth: 0},
			{name: "returns ErrFingerprintBits for fingerprints of 3 bits", giveWidth: 3},
			{name: "returns ErrFingerprintBits for fingerprints of 5 bits", giveWidth: 5},
			{name: "returns ErrFingerprintBits for fingerprints of 9 bits", giveWidth: 9},
			{name: "returns ErrFingerprintBits for fingerprints of 32 bits", giveWidth: 32},
			{name: "returns ErrFingerprintBits for a negative width", giveWidth: -8},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				var b filter.FuseBuilder
				dst := []byte("prefix")
				got, err := b.Append(dst, hashes(10, 1), tt.giveWidth)
				assert.ErrorIs(t, err, filter.ErrFingerprintBits, "Append refuses the width")
				assert.Equal(t, got, dst, "Append returns dst unchanged", assert.ByIdentity())
			})
		}

		t.Run("appends the encoding to dst", func(t *testing.T) {
			t.Parallel()

			hs := hashes(100, 2)
			var b filter.FuseBuilder
			got, err := b.Append([]byte("prefix"), hs, 8)
			assert.NoError(t, err, "Append builds a filter of the set")
			assert.Equal(t, got, append([]byte("prefix"), build(t, hs, 8)...), "Append appends the encoding to dst")
		})

		t.Run("writes the header of the format", func(t *testing.T) {
			t.Parallel()

			// 1000 hashes have the segment exponent 7. Their capacity is
			// ⌈1000 * 1463 / 1024⌉ = 1429 fingerprints, which takes 12 segments
			// of 128, so the segment count is 10.
			enc := build(t, hashes(1000, 5), 10)
			expect.Equal(t, enc[:3], []byte{1, 10, 7}, "the header starts with the version, the width and the exponent")
			expect.Equal(t, binary.BigEndian.Uint32(enc[3:]), uint32(10), "the segment count follows the exponent")
			expect.Equal(t, binary.BigEndian.Uint64(enc[7:]), hashes(1, 1)[0],
				"the seed of the first attempt follows the segment count")
			expect.Length(t, enc, 15+12*128*10/8+3, "the fingerprints and three zero bytes follow the seed")
		})

		t.Run("writes fingerprints that combine to the fingerprint of each hash", func(t *testing.T) {
			t.Parallel()

			for _, w := range widths {
				hs := hashes(3000, uint64(w))
				enc := build(t, hs, w)
				e, seed := uint(enc[2]), binary.BigEndian.Uint64(enc[7:])
				span, segment := uint64(binary.BigEndian.Uint32(enc[3:]))<<e, uint64(1)<<e

				// fingerprint reads fingerprint i bit by bit, from the most
				// significant bit of the first byte.
				fingerprint := func(i uint64) uint64 {
					var v uint64
					for j := range uint64(w) {
						bit := i*uint64(w) + j
						v = v<<1 | uint64(enc[15+bit/8]>>(7-bit%8)&1)
					}

					return v
				}
				for _, h := range hs {
					x := h + seed
					x = (x ^ x>>33) * 0xff51afd7ed558ccd
					x = (x ^ x>>33) * 0xc4ceb9fe1a85ec53
					x ^= x >> 33
					p0, _ := bits.Mul64(x, span)
					p1 := (p0 + segment) ^ (x >> 18 & (segment - 1))
					p2 := (p0 + 2*segment) ^ (x & (segment - 1))
					expect.Equal(t, fingerprint(p0)^fingerprint(p1)^fingerprint(p2), (x^x>>32)&(1<<w-1),
						"the fingerprints at the three positions of a hash combine to its fingerprint")
				}
			}
		})

		t.Run("builds the empty filter for no hashes", func(t *testing.T) {
			t.Parallel()

			want := []byte{1, 12, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
			assert.Equal(t, build(t, nil, 12), want, "the empty filter has no segments, no seed and no fingerprints")
		})

		t.Run("builds the bytes of the golden file for each width", func(t *testing.T) {
			t.Parallel()

			for _, w := range widths {
				path := filepath.Join("testdata", "golden", "fuse-"+strconv.Itoa(w)+".bin")
				golden.MatchAt(t, path, build(t, hashes(1000, 7), w), golden.ShouldUpdate())
			}
		})

		t.Run("builds the bytes of the golden file for a set with repeated hashes", func(t *testing.T) {
			t.Parallel()

			hs := append(hashes(500, 9), hashes(250, 9)...)
			golden.MatchAt(t, filepath.Join("testdata", "golden", "fuse-repeated.bin"), build(t, hs, 8),
				golden.ShouldUpdate())
		})

		t.Run("builds a set of one repeated hash under the first seed", func(t *testing.T) {
			t.Parallel()

			// The two copies of the hash are alone at their positions, so the
			// first attempt leaves out the second copy and peels the first.
			h := hashes(1, 41)[0]
			enc := build(t, []uint64{h, h}, 8)
			assert.Equal(t, binary.BigEndian.Uint64(enc[7:]), hashes(1, 1)[0], "the filter has the first seed")
		})

		t.Run("builds the same bytes with a reused builder", func(t *testing.T) {
			t.Parallel()

			var b filter.FuseBuilder
			_, err := b.Append(nil, hashes(5000, 11), 16)
			assert.NoError(t, err, "Append builds the first filter")
			got, err := b.Append(nil, hashes(1000, 7), 8)
			assert.NoError(t, err, "Append builds the second filter")
			assert.Equal(t, got, build(t, hashes(1000, 7), 8), "a reused builder builds the bytes of a new builder")
		})

		t.Run("does not modify hashes", func(t *testing.T) {
			t.Parallel()

			hs := append(hashes(300, 13), hashes(300, 13)...)
			var b filter.FuseBuilder
			assert.Pure(t, func() []uint64 { return slices.Clone(hs) }, func() { _, _ = b.Append(nil, hs, 8) },
				"Append leaves hashes as it found them")
		})

		t.Run("builds a filter of a set with every hash repeated", func(t *testing.T) {
			t.Parallel()

			distinct := hashes(50, 17)
			hs := make([]uint64, 0, 100*len(distinct))
			for range 100 {
				hs = append(hs, distinct...)
			}
			f := parse(t, build(t, hs, 8))
			for _, h := range distinct {
				expect.True(t, f.MayContain(h), "MayContain reports true for a repeated hash")
			}
		})
	})

	t.Run("FuseSize", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the length of the encoding that Append builds", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "FuseSize must return the length of the encoding of n hashes", func(c *prop.Case) {
				n := c.Draw(prop.Integer(0, 12_000), "n")
				w := c.Draw(prop.SampledFrom(widths...), "width")
				size, err := filter.FuseSize(n, w)
				assert.NoError(c, err, "FuseSize accepts the width")
				assert.Length(c, build(c, hashes(n, uint64(w)), w), size, "the encoding has the length of FuseSize")
			})
		})

		t.Run("returns the length of the encoding at the bounds of the segment exponents", func(t *testing.T) {
			t.Parallel()
			// The examples are the last count of an exponent and the first count
			// of the next one, for the exponents 8 to 11.
			var b filter.FuseBuilder
			prop.Equal(t, func(n int) int {
				size, _ := filter.FuseSize(n, 8)

				return size
			}, func(n int) int {
				enc, _ := b.Append(nil, hashes(n, 31), 8)

				return len(enc)
			}, "FuseSize must return the length of the encoding at each bound",
				prop.Using(prop.Integer(1, 40_000)),
				prop.Examples(1009, 1010, 3361, 3362, 11192, 11193, 37272, 37273))
		})

		t.Run("returns the length of the encoding of a set of a million hashes", func(t *testing.T) {
			t.Parallel()
			size, err := filter.FuseSize(1_000_000, 8)
			assert.NoError(t, err, "FuseSize accepts the width")
			assert.Length(t, build(t, hashes(1_000_000, 29), 8), size, "the encoding has the length of FuseSize")
		})

		t.Run("counts a negative n as 0", func(t *testing.T) {
			t.Parallel()
			size, err := filter.FuseSize(-1, 8)
			assert.NoError(t, err, "FuseSize accepts the width")
			assert.Length(t, build(t, nil, 8), size, "the length is the length of the empty filter")
		})

		t.Run("returns ErrFingerprintBits for fingerprints of 9 bits", func(t *testing.T) {
			t.Parallel()
			_, err := filter.FuseSize(10, 9)
			assert.ErrorIs(t, err, filter.ErrFingerprintBits, "FuseSize refuses the width")
		})

		t.Run("returns ErrTooLarge for more hashes than fit 2^32 fingerprints", func(t *testing.T) {
			t.Parallel()
			_, err := filter.FuseSize(math.MaxInt, 8)
			assert.ErrorIs(t, err, filter.ErrTooLarge, "FuseSize refuses the number of hashes")
		})
	})

	t.Run("MaxFuseSize", func(t *testing.T) {
		t.Parallel()

		for _, w := range widths {
			t.Run("returns the largest FuseSize of at most n hashes of "+strconv.Itoa(w)+" bits", func(t *testing.T) {
				t.Parallel()
				var got, want []int
				most := 0
				for n := range maxFuseCount + 1 {
					size, err := filter.FuseSize(n, w)
					assert.NoError(t, err, "FuseSize accepts the count")
					most = max(most, size)
					if n > denseCounts && n&(n-1) != 0 && n&(n+1) != 0 && (n-1)&(n-2) != 0 {
						continue
					}
					largest, err := filter.MaxFuseSize(n, w)
					assert.NoError(t, err, "MaxFuseSize accepts the count")
					got, want = append(got, largest), append(want, most)
				}
				assert.Equal(t, got, want, "MaxFuseSize returns the largest FuseSize of the counts up to n")
			})
		}

		t.Run("counts a negative n as 0", func(t *testing.T) {
			t.Parallel()
			size, err := filter.MaxFuseSize(-1, 8)
			assert.NoError(t, err, "MaxFuseSize accepts the width")
			assert.Length(t, build(t, nil, 8), size, "the length is the length of the empty filter")
		})

		t.Run("returns ErrFingerprintBits for fingerprints of 9 bits", func(t *testing.T) {
			t.Parallel()
			_, err := filter.MaxFuseSize(10, 9)
			assert.ErrorIs(t, err, filter.ErrFingerprintBits, "MaxFuseSize refuses the width")
		})

		t.Run("returns ErrTooLarge for more hashes than fit 2^32 fingerprints", func(t *testing.T) {
			t.Parallel()
			_, err := filter.MaxFuseSize(math.MaxInt, 8)
			assert.ErrorIs(t, err, filter.ErrTooLarge, "MaxFuseSize refuses the number of hashes")
		})
	})

	t.Run("Footprint", func(t *testing.T) {
		t.Parallel()

		t.Run("returns 0 for a new builder", func(t *testing.T) {
			t.Parallel()

			var b filter.FuseBuilder
			assert.Equal(t, b.Footprint(), 0, "a new builder has no scratch")
		})

		t.Run("returns about 34 bytes for each hash of the largest build", func(t *testing.T) {
			t.Parallel()

			var b filter.FuseBuilder
			_, err := b.Append(nil, hashes(10_000, 19), 8)
			assert.NoError(t, err, "Append builds the filter")
			assert.InRange(t, b.Footprint(), 30*10_000, 40*10_000, "the scratch takes about 34 bytes for each hash")
		})

		t.Run("returns the capacity of every array of its scratch in bytes", func(t *testing.T) {
			t.Parallel()

			var b filter.FuseBuilder
			_, err := b.Append(nil, hashes(10_000, 19), 8)
			assert.NoError(t, err, "Append builds the filter")
			want := 0
			for _, f := range reflect.ValueOf(&b).Elem().Fields() {
				if f.Kind() == reflect.Slice {
					want += f.Cap() * int(f.Type().Elem().Size())
				}
			}
			assert.Equal(t, b.Footprint(), want, "Footprint returns the bytes of the slices of the builder")
		})

		t.Run("returns the same bytes after a build of fewer hashes", func(t *testing.T) {
			t.Parallel()

			var b filter.FuseBuilder
			_, err := b.Append(nil, hashes(10_000, 19), 8)
			assert.NoError(t, err, "Append builds the first filter")
			assert.Pure(t, b.Footprint, func() { _, err = b.Append(nil, hashes(5000, 19), 8) },
				"the second build reuses the scratch of the first")
			assert.NoError(t, err, "Append builds the second filter")
		})
	})
}

func TestFuseBuilderAllocs(t *testing.T) {
	t.Run("Append", func(t *testing.T) {
		var b filter.FuseBuilder
		hs := hashes(10_000, 19)
		dst, err := b.Append(nil, hs, 8)
		assert.NoError(t, err, "Append builds the filter that sizes the scratch")

		var got []byte
		expect.MaxAllocs(t, func() { got, err = b.Append(dst[:0], hs[:5000], 8) }, 0,
			"Append does not allocate for a set no larger than the last")
		assert.NoError(t, err, "the test measures a build that succeeds")
		assert.Equal(t, got, build(t, hs[:5000], 8), "the test measures the encoding of the smaller set")

		expect.MaxAllocs(t, func() { got, err = b.Append(nil, hs[:5000], 8) }, 1,
			"Append grows a nil dst once")
		assert.NoError(t, err, "the test measures a build that succeeds")
	})

	t.Run("Footprint", func(t *testing.T) {
		var b filter.FuseBuilder
		_, err := b.Append(nil, hashes(1000, 19), 8)
		assert.NoError(t, err, "Append builds the filter that sizes the scratch")
		n := 0
		expect.MaxAllocs(t, func() { n = b.Footprint() }, 0, "Footprint does not allocate")
		assert.NotEqual(t, n, 0, "the test measures the footprint of a scratch")
	})
}

func BenchmarkFuseBuilder(b *testing.B) {
	b.Run("Append", func(b *testing.B) {
		for _, n := range []int{1000, 1 << 20} {
			b.Run(strconv.Itoa(n), func(b *testing.B) {
				var fb filter.FuseBuilder
				hs := hashes(n, 23)
				dst, err := fb.Append(nil, hs, 8)
				assert.NoError(b, err, "Append builds the filter that sizes the scratch")

				c := bench.Start(b).MaxAllocs(0)
				defer c.End()

				for c.Loop() {
					dst, err = fb.Append(dst[:0], hs, 8)
				}

				assert.NoError(b, err, "the benchmark measures builds that succeed")
			})
		}
	})
}
