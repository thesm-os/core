// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package filter

import (
	"encoding/binary"
	"math"
	"strconv"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
)

// The geometry and the seed of the peels of crafted mixed hashes: one
// segment of 8 fingerprints and two more segments after it. A mixed hash x
// has the first position x>>61, the second position 8 ^ (x>>18 & 7) plus the
// first, and the third position 16 ^ (x & 7) plus the first.
var (
	crafted     = geometry{exponent: 3, segments: 1}
	craftedSeed = uint64(7)
)

// murmurInverses are the inverses modulo 2^64 of the multipliers of murmur64,
// in the order in which unmix applies them.
var murmurInverses = func() [2]uint64 {
	var inv [2]uint64
	for i, c := range [2]uint64{0xc4ceb9fe1a85ec53, 0xff51afd7ed558ccd} {
		// An odd c is its own inverse modulo 8, and each step of Newton's
		// method doubles the number of correct bits.
		inv[i] = c
		for range 5 {
			inv[i] *= 2 - c*inv[i]
		}
	}

	return inv
}()

// TestFuseBuilderInternals is in package filter because the size tables, the
// seeds and the positions of a FuseBuilder are unexported. It checks the
// tables against the formulas of the authors of binary fuse filters, peels
// crafted mixed hashes, and builds sets that the seeds peel late or never.
func TestFuseBuilderInternals(t *testing.T) {
	t.Parallel()

	t.Run("newGeometry", func(t *testing.T) {
		t.Parallel()

		t.Run("takes the segment exponent of the formula of the authors", func(t *testing.T) {
			t.Parallel()

			// The authors give n hashes the exponent ⌊ln n / ln 3.33 + 2.25⌋,
			// which grows with n. The search starts below the threshold of k
			// and stops at the first n of exponent k.
			for k := minExponent + 1; k <= maxExponent; k++ {
				n := max(int(math.Pow(3.33, float64(k)-2.25))-2, 1)
				for math.Floor(math.Log(float64(n))/math.Log(3.33)+2.25) < float64(k) {
					n++
				}
				expect.Equal(t, exponentThresholds[k-minExponent], n,
					"the threshold of exponent "+strconv.Itoa(k)+" is the smallest n of that exponent")
				below, err := newGeometry(n-1, 8)
				assert.NoError(t, err, "newGeometry accepts the size")
				expect.Equal(t, int(below.exponent), k-1, "a set below the threshold takes the exponent before it")
				at, err := newGeometry(n, 8)
				assert.NoError(t, err, "newGeometry accepts the size")
				expect.Equal(t, int(at.exponent), k, "a set at the threshold takes its exponent")
			}
		})

		t.Run("takes the capacity of the formula of the authors", func(t *testing.T) {
			t.Parallel()

			// The authors give m hashes the size factor
			// max(1.125, 0.875 + 0.25 * ln 10^6 / ln m), and the table contains
			// 1,024 times it for the smallest m of each bit length, rounded up.
			for b := 2; b <= 2+len(capacityFactors); b++ {
				got := minCapacityFactor
				if b-2 < len(capacityFactors) {
					got = capacityFactors[b-2]
				}
				m := math.Ldexp(1, b-1)
				want := int(math.Ceil(1024 * math.Max(1.125, 0.875+0.25*math.Log(1e6)/math.Log(m))))
				expect.Equal(t, got, want, "the factor of bit length "+strconv.Itoa(b)+" is the factor of 2^"+
					strconv.Itoa(b-1))
			}
		})

		// A set of n hashes has the capacity ⌈n * q / 1024⌉ for the factor q of
		// its bit length, and ⌈capacity / 2^e⌉ segments of 2^e fingerprints,
		// at least 3, of which the segment count leaves out 2.
		sizes := []struct {
			name         string
			giveN        int
			wantExponent uint8
			wantSegments uint32
		}{
			{
				// ⌈2 * 5999 / 1024⌉ = 12 fingerprints take 3 segments of 4.
				name:  "takes 3 segments of 4 fingerprints for 2 hashes",
				giveN: 2, wantExponent: 2, wantSegments: 1,
			},
			{
				// ⌈23 * 2172 / 1024⌉ = 49 fingerprints take 4 segments of 16.
				name:  "takes 4 segments of 16 fingerprints for 23 hashes",
				giveN: 23, wantExponent: 4, wantSegments: 2,
			},
			{
				// ⌈100000 * 1215 / 1024⌉ = 118653 fingerprints take 58 segments
				// of 2^11.
				name:  "takes 58 segments of 2^11 fingerprints for 100000 hashes",
				giveN: 100000, wantExponent: 11, wantSegments: 56,
			},
			{
				// ⌈3817748707 * 1152 / 1024⌉ = 2^32 fingerprints fill 2^14
				// segments of 2^18.
				name:  "takes 2^32 fingerprints for 3817748707 hashes",
				giveN: 3817748707, wantExponent: 18, wantSegments: 1<<14 - 2,
			},
		}
		for _, tt := range sizes {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				g, err := newGeometry(tt.giveN, 8)
				assert.NoError(t, err, "newGeometry accepts the size")
				expect.Equal(t, g.exponent, tt.wantExponent, "the geometry has the exponent of the size")
				expect.Equal(t, g.segments, tt.wantSegments, "the geometry has the segments of the capacity")
			})
		}

		t.Run("returns ErrTooLarge for more hashes than fit 2^32 fingerprints", func(t *testing.T) {
			t.Parallel()

			// The capacity of 2^57 hashes is a multiple of 2^64, which the check
			// of the number of hashes refuses before the multiplication.
			for _, n := range []int{3817748708, 3_900_000_000, maxFingerprints + 1, 1 << 57, math.MaxInt} {
				_, err := newGeometry(n, 8)
				expect.ErrorIs(t, err, ErrTooLarge, "newGeometry refuses "+strconv.Itoa(n)+" hashes")
			}
			_, err := newGeometry(3_700_000_000, 8)
			assert.NoError(t, err, "newGeometry accepts a set whose fingerprints fit 2^32")
		})
	})

	t.Run("forAttempt", func(t *testing.T) {
		t.Parallel()

		// A halving attempt has the exponent 4 and 2 * 10 + 2 segments.
		g := geometry{exponent: 5, segments: 10}
		halved := geometry{exponent: 4, segments: 22}
		tests := []struct {
			name        string
			giveAttempt int
			giveN       int
			want        geometry
		}{
			{name: "keeps the geometry of the first attempt", giveAttempt: 1, giveN: 1000, want: g},
			{name: "halves the segments of the second attempt", giveAttempt: 2, giveN: 1000, want: halved},
			{name: "keeps the geometry of the third attempt", giveAttempt: 3, giveN: 1000, want: g},
			{name: "halves the segments of the sixth attempt", giveAttempt: 6, giveN: 1000, want: halved},
			{name: "keeps the geometry for 4 hashes", giveAttempt: 2, giveN: minHalving - 1, want: g},
			{name: "halves the segments for 5 hashes", giveAttempt: 2, giveN: minHalving, want: halved},
			{name: "halves the segments for 999999 hashes", giveAttempt: 2, giveN: maxHalving, want: halved},
			{name: "keeps the geometry for 1000000 hashes", giveAttempt: 2, giveN: maxHalving + 1, want: g},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				assert.Equal(t, g.forAttempt(tt.giveAttempt, tt.giveN), tt.want, "forAttempt returns the geometry")
			})
		}
	})

	t.Run("peel", func(t *testing.T) {
		t.Parallel()

		// In each case the repeated mixed hash d has the positions 0, 8 and 16.
		// The other hashes share two of them, so one position alone has the
		// two copies of d.
		const d = 1 << 30
		repeats := []struct {
			name string
			give []uint64
		}{
			{
				name: "leaves out a repeated hash that only its first position shows",
				give: []uint64{1<<61 | 1<<18 | 1<<31, 2<<61 | 2 | 1<<32, d, d},
			},
			{
				name: "leaves out a repeated hash that only its second position shows",
				give: []uint64{1<<18 | 1 | 1<<31, 2<<61 | 2 | 1<<32, d, d},
			},
			{
				name: "leaves out a repeated hash that only its third position shows",
				give: []uint64{1<<18 | 1 | 1<<31, 1<<61 | 1<<18 | 1<<32, d, d},
			},
		}
		for _, tt := range repeats {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				var b FuseBuilder
				for _, x := range tt.give {
					b.keys = append(b.keys, unmix(x, craftedSeed))
				}
				_, removed := b.peel(crafted, craftedSeed)
				assert.Equal(t, removed, 1, "peel leaves out the second copy of the hash")
			})
		}

		t.Run("keeps three hashes whose exclusive or is 0 at their positions", func(t *testing.T) {
			t.Parallel()

			// The three hashes share their three positions, where their
			// exclusive or is 0 and their count is 3.
			var b FuseBuilder
			for _, x := range []uint64{1 << 30, 1 << 31, 3 << 30} {
				b.keys = append(b.keys, unmix(x, craftedSeed))
			}
			_, removed := b.peel(crafted, craftedSeed)
			assert.Equal(t, removed, 0, "peel leaves out no hash")
		})

		// Each case puts 65 mixed hashes at one position and spreads them over
		// the other two, so the count of 6 bits of that position overflows at
		// the 64th hash.
		crowds := []struct {
			name  string
			giveX func(i uint64) uint64
		}{
			{
				name:  "ends the peel at the 64th hash at one first position",
				giveX: func(i uint64) uint64 { return i<<24 | (i&7)<<18 | i>>3&7 },
			},
			{
				name:  "ends the peel at the 64th hash at one second position",
				giveX: func(i uint64) uint64 { return (i&7)<<61 | i<<24 | (i&7)<<18 | i>>3&7 },
			},
			{
				name:  "ends the peel at the 64th hash at one third position",
				giveX: func(i uint64) uint64 { return (i&7)<<61 | i<<24 | (i>>3&7)<<18 | i&7 },
			},
		}
		for _, tt := range crowds {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				var b FuseBuilder
				for i := range uint64(65) {
					b.keys = append(b.keys, unmix(tt.giveX(i+1), craftedSeed))
				}
				stack, _ := b.peel(crafted, craftedSeed)
				assert.Equal(t, stack, 0, "peel removes no hash")
			})
		}
	})

	t.Run("Append", func(t *testing.T) {
		t.Parallel()

		t.Run("returns ErrBuild for a set that no seed peels", func(t *testing.T) {
			t.Parallel()

			var b FuseBuilder
			dst := []byte("prefix")
			got, err := b.Append(dst, unpeelable(t, maxSeeds), 8)
			assert.ErrorIs(t, err, ErrBuild, "Append returns ErrBuild when no seed peels the set")
			assert.Equal(t, got, dst, "Append returns dst unchanged", assert.ByIdentity())
		})

		t.Run("builds a set that only the last seed peels", func(t *testing.T) {
			t.Parallel()

			var b FuseBuilder
			enc, err := b.Append(nil, unpeelable(t, maxSeeds-1), 8)
			assert.NoError(t, err, "the last seed builds the filter")
			state := uint64(1)
			var last uint64
			for range maxSeeds {
				last = splitmix64(&state)
			}
			assert.Equal(t, binary.BigEndian.Uint64(enc[7:]), last, "the filter has the last seed")
		})

		t.Run("builds a filter of a set that puts 64 hashes at one position under the first seed", func(t *testing.T) {
			t.Parallel()

			// The count of the shared position exceeds 63, the largest count of
			// its 6 bits. The first attempt ends there, and a later seed builds
			// the filter.
			set, seed := crowded(t, 64)
			var b FuseBuilder
			enc, err := b.Append(nil, set, 8)
			assert.NoError(t, err, "a later seed builds the filter")
			assert.NotEqual(t, binary.BigEndian.Uint64(enc[7:]), seed, "the first seed does not build the filter")
			f, err := ParseFuse(enc)
			assert.NoError(t, err, "ParseFuse accepts the encoding")
			for _, h := range set {
				expect.True(t, f.MayContain(h), "MayContain reports true for a hash of the set")
			}
		})
	})
}

// TestFuseBuilderInternalsAllocs checks the allocation contract of Append on
// a set that the second attempt builds with half the segment length. The
// test does not run in parallel, because MaxAllocs counts the allocations of
// the whole process.
func TestFuseBuilderInternalsAllocs(t *testing.T) {
	t.Run("Append", func(t *testing.T) {
		set, _ := crowded(t, 64)
		state := uint64(1 << 40)
		warm := make([]uint64, 64)
		for i := range warm {
			warm[i] = splitmix64(&state)
		}
		dst := make([]byte, 0, 1024)
		var err error

		// Each count takes a builder that built 64 hashes under the first
		// seed, so the halving attempt of the set needs the room that Append
		// reserves for it.
		expect.MaxAllocsWithSetup(t, func() *FuseBuilder {
			var b FuseBuilder
			_, warmErr := b.Append(dst[:0], warm, 8)
			assert.NoError(t, warmErr, "Append builds the set of 64 hashes")

			return &b
		}, func(b *FuseBuilder) { dst, err = b.Append(dst[:0], set, 8) }, 0,
			"Append does not allocate for a set that a halving attempt builds")
		assert.NoError(t, err, "the test measures a build that succeeds")
	})
}

// unmix returns the hash h whose mixed hash murmur64(h + seed) is x. It undoes
// the steps of murmur64 in the reverse order. A shift of 33 bits and an
// exclusive or undo themselves, and a multiplication undoes with its inverse.
func unmix(x, seed uint64) uint64 {
	x ^= x >> 33
	x *= murmurInverses[0]
	x ^= x >> 33
	x *= murmurInverses[1]
	x ^= x >> 33

	return x - seed
}

// unpeelable returns a set of one pair of hashes for each of the first
// attempts of a build. The two hashes of a pair share their three positions
// under the seed and the geometry of their attempt, so neither is ever alone
// at a position and the attempt cannot remove them.
func unpeelable(tb testing.TB, attempts int) []uint64 {
	tb.Helper()

	n := 2 * attempts
	g, err := newGeometry(n, 8)
	assert.NoError(tb, err, "newGeometry accepts the size")
	set := make([]uint64, 0, n)
	state, candidate := uint64(1), uint64(0)
	for attempt := 1; attempt <= attempts; attempt++ {
		seed := splitmix64(&state)
		a := g.forAttempt(attempt, n)
		span, segment := uint64(a.segments)<<a.exponent, uint64(1)<<a.exponent
		seen := make(map[[3]uint64]uint64)
		for {
			candidate++
			var ps [3]uint64
			ps[0], ps[1], ps[2] = positions(murmur64(candidate+seed), segment, span)
			if other, ok := seen[ps]; ok {
				set = append(set, other, candidate)

				break
			}
			seen[ps] = candidate
		}
	}

	return set
}

// crowded returns n hashes that share their first position under the first
// seed and the geometry of n hashes, and that seed.
func crowded(tb testing.TB, n int) ([]uint64, uint64) {
	tb.Helper()

	g, err := newGeometry(n, 8)
	assert.NoError(tb, err, "newGeometry accepts the size")
	state := uint64(1)
	seed := splitmix64(&state)
	span, segment := uint64(g.segments)<<g.exponent, uint64(1)<<g.exponent
	set := make([]uint64, 0, n)
	for candidate := uint64(1); len(set) < n; candidate++ {
		if p0, _, _ := positions(murmur64(candidate+seed), segment, span); p0 == 0 {
			set = append(set, candidate)
		}
	}

	return set, seed
}
