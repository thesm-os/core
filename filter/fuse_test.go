// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package filter_test

import (
	"encoding/binary"
	"math"
	"slices"
	"strconv"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/filter"
)

// parseContract is the contract of parseChanged.
const parseContract = "ParseFuse refuses a changed encoding with ErrCorrupt, or returns a filter whose MayContain " +
	"does not panic"

func TestFuse(t *testing.T) {
	t.Parallel()

	t.Run("ParseFuse", func(t *testing.T) {
		t.Parallel()

		t.Run("accepts every encoding that Append builds", func(t *testing.T) {
			t.Parallel()

			prop.ForAll(t, "ParseFuse accepts every encoding that Append builds", func(c *prop.Case) {
				enc := build(c, c.Draw(sets, "hashes"), c.Draw(prop.SampledFrom(widths...), "width"))
				_, err := filter.ParseFuse(enc)
				assert.NoError(c, err, "ParseFuse accepts the encoding")
			})
		})

		t.Run("returns ErrCorrupt for every truncation of an encoding", func(t *testing.T) {
			t.Parallel()

			for _, w := range widths {
				enc := build(t, hashes(100, uint64(w)), w)
				for n := range len(enc) {
					_, err := filter.ParseFuse(enc[:n])
					expect.ErrorIs(t, err, filter.ErrCorrupt, "ParseFuse refuses an encoding of "+strconv.Itoa(n)+
						" bytes of "+strconv.Itoa(len(enc)))
				}
			}
		})

		t.Run("returns ErrCorrupt for an encoding with a byte past its end", func(t *testing.T) {
			t.Parallel()

			for _, enc := range [][]byte{build(t, nil, 8), build(t, hashes(100, 3), 8)} {
				_, err := filter.ParseFuse(append(enc, 0))
				expect.ErrorIs(t, err, filter.ErrCorrupt, "ParseFuse refuses a byte past the end of the encoding")
			}
		})

		// enc has the segment exponent 6 and the segment count 1.
		enc := build(t, hashes(100, 59), 8)
		tests := []struct {
			name      string
			giveIndex int
			giveByte  byte
		}{
			{name: "returns ErrCorrupt for version 0", giveIndex: 0, giveByte: 0},
			{name: "returns ErrCorrupt for version 2", giveIndex: 0, giveByte: 2},
			{name: "returns ErrCorrupt for fingerprints of 9 bits", giveIndex: 1, giveByte: 9},
			{name: "returns ErrCorrupt for a width that does not match the length", giveIndex: 1, giveByte: 16},
			{name: "returns ErrCorrupt for a segment exponent of 1", giveIndex: 2, giveByte: 1},
			{name: "returns ErrCorrupt for a segment exponent of 19", giveIndex: 2, giveByte: 19},
			{
				name:      "returns ErrCorrupt for a segment exponent that does not match the length",
				giveIndex: 2, giveByte: enc[2] + 1,
			},
			{
				name:      "returns ErrCorrupt for a segment count that does not match the length",
				giveIndex: 6, giveByte: enc[6] + 1,
			},
			{name: "returns ErrCorrupt for a first padding byte of 1", giveIndex: len(enc) - 3, giveByte: 1},
			{name: "returns ErrCorrupt for a second padding byte of 1", giveIndex: len(enc) - 2, giveByte: 1},
			{name: "returns ErrCorrupt for a third padding byte of 1", giveIndex: len(enc) - 1, giveByte: 1},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				changed := slices.Clone(enc)
				changed[tt.giveIndex] = tt.giveByte
				_, err := filter.ParseFuse(changed)
				assert.ErrorIs(t, err, filter.ErrCorrupt, "ParseFuse refuses the changed encoding")
			})
		}

		// Each encoding has the length that its header states, so the check of
		// its width or of its exponent decides alone.
		headers := []struct {
			name         string
			giveWidth    byte
			giveExponent byte
			giveSegments uint32
			wantErr      error
		}{
			{
				name:      "returns ErrCorrupt for fingerprints of 7 bits in an encoding of their length",
				giveWidth: 7, giveExponent: 2, giveSegments: 1, wantErr: filter.ErrCorrupt,
			},
			{
				name:      "returns ErrCorrupt for a segment exponent of 1 in an encoding of its length",
				giveWidth: 8, giveExponent: 1, giveSegments: 1, wantErr: filter.ErrCorrupt,
			},
			{
				name:      "returns ErrCorrupt for a segment exponent of 19 without segments",
				giveWidth: 8, giveExponent: 19, wantErr: filter.ErrCorrupt,
			},
			{name: "returns a filter for a segment exponent of 18 without segments", giveWidth: 8, giveExponent: 18},
		}
		for _, tt := range headers {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				count := 0
				if tt.giveSegments > 0 {
					count = (int(tt.giveSegments) + 2) << tt.giveExponent
				}
				enc := binary.BigEndian.AppendUint32([]byte{1, tt.giveWidth, tt.giveExponent}, tt.giveSegments)
				enc = binary.BigEndian.AppendUint64(enc, 0)
				enc = append(enc, make([]byte, count*int(tt.giveWidth)/8+3)...)
				_, err := filter.ParseFuse(enc)
				if tt.wantErr != nil {
					assert.ErrorIs(t, err, tt.wantErr, "ParseFuse refuses the header")

					return
				}
				assert.NoError(t, err, "ParseFuse accepts the header")
			})
		}

		t.Run("refuses a changed encoding or returns a filter that does not panic", func(t *testing.T) {
			t.Parallel()

			prop.ForAll(t, parseContract, parseChanged)
		})
	})

	t.Run("MayContain", func(t *testing.T) {
		t.Parallel()

		t.Run("reports true for every hash of the set that built the filter", func(t *testing.T) {
			t.Parallel()

			prop.ForAll(t, "MayContain reports true for every hash of the set", func(c *prop.Case) {
				hs := c.Draw(sets, "hashes")
				f := parse(c, build(c, hs, c.Draw(prop.SampledFrom(widths...), "width")))
				for _, h := range hs {
					assert.True(c, f.MayContain(h), "MayContain reports true for a hash of the set")
				}
			})
		})

		t.Run("reports false for every hash of the empty filter", func(t *testing.T) {
			t.Parallel()

			f := parse(t, build(t, nil, 8))
			prop.ForAll(t, "the empty filter reports false for every hash", func(c *prop.Case) {
				assert.False(c, f.MayContain(c.Draw(anyHash, "hash")), "MayContain reports false for the empty filter")
			})
		})

		t.Run("reports false for every hash of the zero Fuse", func(t *testing.T) {
			t.Parallel()

			var f filter.Fuse
			prop.ForAll(t, "the zero Fuse reports false for every hash", func(c *prop.Case) {
				assert.False(c, f.MayContain(c.Draw(anyHash, "hash")), "MayContain reports false for the zero Fuse")
			})
		})

		tests := []struct {
			name      string
			giveWidth int
		}{
			{name: "reports true for one absent hash in 2^4 for fingerprints of 4 bits", giveWidth: 4},
			{name: "reports true for one absent hash in 2^8 for fingerprints of 8 bits", giveWidth: 8},
			{name: "reports true for one absent hash in 2^10 for fingerprints of 10 bits", giveWidth: 10},
			{name: "reports true for one absent hash in 2^12 for fingerprints of 12 bits", giveWidth: 12},
			{name: "reports true for one absent hash in 2^16 for fingerprints of 16 bits", giveWidth: 16},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				const keys, queries = 1 << 14, 1 << 20
				f := parse(t, build(t, hashes(keys, 61), tt.giveWidth))
				positives := 0
				for _, h := range hashes(queries, 67) {
					if f.MayContain(h) {
						positives++
					}
				}

				// An absent hash matches the exclusive or of its three
				// fingerprints with probability 2^-width, so the positives are
				// binomial.
				p := math.Ldexp(1, -tt.giveWidth)
				mean, sigma := queries*p, math.Sqrt(queries*p*(1-p))
				assert.InRange(t, positives, mean-6*sigma, mean+6*sigma,
					"the false positives are within six standard deviations of the binomial mean")
			})
		}
	})

	t.Run("Bits", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name      string
			giveWidth int
		}{
			{name: "returns 4 for fingerprints of 4 bits", giveWidth: 4},
			{name: "returns 8 for fingerprints of 8 bits", giveWidth: 8},
			{name: "returns 10 for fingerprints of 10 bits", giveWidth: 10},
			{name: "returns 12 for fingerprints of 12 bits", giveWidth: 12},
			{name: "returns 16 for fingerprints of 16 bits", giveWidth: 16},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				f := parse(t, build(t, hashes(100, 71), tt.giveWidth))
				assert.Equal(t, f.Bits(), tt.giveWidth, "Bits returns the width of the encoding")
			})
		}

		t.Run("returns 0 for the zero Fuse", func(t *testing.T) {
			t.Parallel()

			var f filter.Fuse
			assert.Equal(t, f.Bits(), 0, "the zero Fuse has no fingerprints")
		})
	})
}

func TestFuseAllocs(t *testing.T) {
	hs := hashes(1000, 73)
	enc := build(t, hs, 8)
	f := parse(t, enc)

	t.Run("ParseFuse", func(t *testing.T) {
		var err error
		expect.MaxAllocs(t, func() { _, err = filter.ParseFuse(enc) }, 0, "ParseFuse does not allocate")
		assert.NoError(t, err, "the test measures an encoding that ParseFuse accepts")
	})

	t.Run("MayContain", func(t *testing.T) {
		var got bool
		expect.MaxAllocs(t, func() { got = f.MayContain(hs[0]) }, 0, "MayContain does not allocate")
		assert.True(t, got, "the test measures a hash of the set")
	})

	t.Run("Bits", func(t *testing.T) {
		var got int
		expect.MaxAllocs(t, func() { got = f.Bits() }, 0, "Bits does not allocate")
		assert.Equal(t, got, 8, "the test measures a filter of 8-bit fingerprints")
	})
}

func BenchmarkFuse(b *testing.B) {
	hs := hashes(1<<20, 79)
	enc := build(b, hs, 8)
	f := parse(b, enc)

	b.Run("ParseFuse", func(b *testing.B) {
		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		var err error
		for c.Loop() {
			_, err = filter.ParseFuse(enc)
		}

		assert.NoError(b, err, "the benchmark measures an encoding that ParseFuse accepts")
	})

	b.Run("MayContain", func(b *testing.B) {
		for _, w := range widths {
			b.Run(strconv.Itoa(w), func(b *testing.B) {
				fw := parse(b, build(b, hs, w))

				c := bench.Start(b).MaxAllocs(0)
				defer c.End()

				var got bool
				i := 0
				for c.Loop() {
					got = fw.MayContain(hs[i&(len(hs)-1)])
					i++
				}

				assert.True(b, got, "the benchmark measures hashes of the set")
			})
		}
	})

	b.Run("Bits", func(b *testing.B) {
		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		var got int
		for c.Loop() {
			got = f.Bits()
		}

		assert.Equal(b, got, 8, "the benchmark measures a filter of 8-bit fingerprints")
	})
}

// FuzzParseFuse checks that ParseFuse refuses a changed encoding with
// ErrCorrupt, or returns a filter whose MayContain does not panic.
func FuzzParseFuse(f *testing.F) {
	prop.Fuzz(f, parseContract, parseChanged)
}

// parseChanged builds the encoding of a drawn set, changes one byte of it,
// cuts it to a drawn length and parses it. ParseFuse refuses the result with
// ErrCorrupt, or returns a filter whose MayContain does not panic.
func parseChanged(c *prop.Case) {
	hs := c.Draw(prop.List(anyHash, prop.MaxSize(200)), "hashes")
	enc := build(c, hs, c.Draw(prop.SampledFrom(widths...), "width"))
	enc[c.Draw(prop.Integer(0, len(enc)-1), "index")] = c.Draw(prop.Integer[byte](0, 255), "byte")
	enc = enc[:c.Draw(prop.Integer(0, len(enc)), "length")]

	f, err := filter.ParseFuse(enc)
	if err != nil {
		assert.ErrorIs(c, err, filter.ErrCorrupt, "ParseFuse refuses the changed encoding with ErrCorrupt")

		return
	}
	queries := append(slices.Clone(hs), c.Draw(anyHash, "hash"))
	assert.NotPanics(c, func() {
		for _, h := range queries {
			f.MayContain(h)
		}
	}, "MayContain does not panic on a changed encoding")
}
