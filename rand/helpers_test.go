// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package rand_test

import (
	"encoding/binary"
	"math"
	"slices"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/constant"
	"go.thesmos.sh/core/rand/pcg"
)

// Generators of the properties over the helpers.
var (
	// seeds generates the seed of a pcg source.
	seeds = prop.Integer[rand.Seed](math.MinInt64, math.MaxInt64)

	// lengths generates the length of the range that a case shuffles.
	lengths = prop.Integer(0, 64)
)

// scriptedRand returns a pre-set sequence of Uint64 values and panics once
// the sequence ends. It forces the branches of an algorithm that random
// draws reach too rarely, such as the rejection loop of Uint64N.
//
// A case scripts exactly the draws that the algorithm consumes. A draw past
// the script is a defect, and under a mutated redraw condition it would loop
// until the test binary's deadline, so the panic ends it at once.
type scriptedRand struct {
	values []uint64
	pos    int
}

// Uint64 returns the next value of the script, and panics past its end.
func (s *scriptedRand) Uint64() uint64 {
	if s.pos >= len(s.values) {
		panic("scriptedRand: draw past the script") //nolint:forbidigo // see the type's docblock
	}
	v := s.values[s.pos]
	s.pos++

	return v
}

// Read fills p with the little-endian bytes of the next values of the
// script.
func (s *scriptedRand) Read(p []byte) (int, error) {
	var chunk [8]byte
	written := 0
	for written < len(p) {
		binary.LittleEndian.PutUint64(chunk[:], s.Uint64())
		written += copy(p[written:], chunk[:])
	}

	return len(p), nil
}

func TestFloat64(t *testing.T) {
	t.Parallel()

	t.Run("returns the top 53 bits of the draw over 2^53", func(t *testing.T) {
		t.Parallel()
		prop.Equal(t,
			func(u uint64) uint64 { return uint64(math.Ldexp(rand.Float64(constant.New(u)), 53)) },
			func(u uint64) uint64 { return u >> 11 },
			"Float64 must divide the top 53 bits of one draw by 2^53",
			prop.Example(uint64(0)), prop.Example(uint64(1<<63)), prop.Example(uint64(math.MaxUint64)))
	})

	t.Run("returns a value in [0, 1)", func(t *testing.T) {
		t.Parallel()
		prop.InRange(t, func(u uint64) float64 { return rand.Float64(constant.New(u)) }, 0, math.Nextafter(1, 0),
			"Float64 must stay below 1", prop.Example(uint64(math.MaxUint64)))
	})
}

func TestShuffle(t *testing.T) {
	t.Parallel()

	t.Run("calls swap for each i from n-1 down to 1 with j in [0, i]", func(t *testing.T) {
		t.Parallel()
		prop.ForAll(t, "Shuffle must call swap once per index, from the last down to 1", func(c *prop.Case) {
			r := pcg.New(c.Draw(seeds, "seed"))
			n := c.Draw(lengths, "n")

			var is []int
			rand.Shuffle(r, n, func(i, j int) {
				assert.InRange(c, j, 0, float64(i), "swap must receive a j in [0, i]")
				is = append(is, i)
			})

			var want []int
			for i := n - 1; i > 0; i-- {
				want = append(want, i)
			}
			assert.Equal(c, is, want, "Shuffle must call swap for n-1 down to 1")
		})
	})

	t.Run("permutes the range", func(t *testing.T) {
		t.Parallel()
		prop.ForAll(t, "Shuffle must move every element of [0, n) and lose none", func(c *prop.Case) {
			r := pcg.New(c.Draw(seeds, "seed"))
			n := c.Draw(lengths, "n")
			want := make([]int, n)
			for i := range want {
				want[i] = i
			}

			got := slices.Clone(want)
			rand.Shuffle(r, n, func(i, j int) { got[i], got[j] = got[j], got[i] })
			assert.Permutation(c, got, want, "the shuffled range must contain each element once")
		})
	})

	t.Run("returns the same permutation for the same seed", func(t *testing.T) {
		t.Parallel()
		prop.Deterministic(t, func(seed rand.Seed) ([]int, error) {
			out := make([]int, 50)
			for i := range out {
				out[i] = i
			}
			rand.Shuffle(pcg.New(seed), len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })

			return out, nil
		}, "Shuffle must permute the same way for the same stream of draws", prop.Using(seeds))
	})

	t.Run("returns the recorded permutation of seed 1", func(t *testing.T) {
		t.Parallel()
		// The permutation is recorded from pcg.New(rand.Seed(1)). A change
		// of the index arithmetic, such as the i+1 bound or the order of
		// the swaps, changes it.
		out := []int{0, 1, 2, 3, 4, 5, 6, 7}
		rand.Shuffle(pcg.New(rand.Seed(1)), len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
		assert.Equal(t, out, []int{5, 3, 1, 2, 6, 7, 0, 4}, "Shuffle must return the recorded permutation")
	})

	t.Run("calls no swap for n of 1 or less", func(t *testing.T) {
		t.Parallel()
		// The empty script panics at the first draw and the swap at its
		// first call, so a mutant that loops for math.MinInt stops at once.
		prop.NotPanics(t, func(n int) {
			rand.Shuffle(&scriptedRand{}, n, func(int, int) {
				panic("Shuffle: swap called for n of 1 or less") //nolint:forbidigo // see above
			})
		}, "Shuffle must neither draw nor call swap for n of 1 or less",
			prop.Using(prop.Integer(math.MinInt, 1)), prop.Example(math.MinInt), prop.Example(0), prop.Example(1))
	})
}

func TestUint64N(t *testing.T) {
	t.Parallel()

	t.Run("returns 0 for n of 0 or 1", func(t *testing.T) {
		t.Parallel()
		prop.ForAll(t, "Uint64N must return 0 for an interval of at most one value", func(c *prop.Case) {
			r := constant.New(c.Draw(prop.Of[uint64](), "draw"))
			n := c.Draw(prop.SampledFrom[uint64](0, 1), "n")
			assert.Equal(c, rand.Uint64N(r, n), 0, "Uint64N must return 0")
		})
	})

	t.Run("returns a value below n", func(t *testing.T) {
		t.Parallel()
		prop.ForAll(t, "Uint64N must return a value in [0, n)", func(c *prop.Case) {
			r := pcg.New(c.Draw(seeds, "seed"))
			n := c.Draw(prop.Integer[uint64](1, math.MaxUint64), "n")
			assert.True(c, rand.Uint64N(r, n) < n, "Uint64N must return a value below n")
		})
	})

	t.Run("redraws while the draw falls in the band of bias", func(t *testing.T) {
		t.Parallel()
		// For n = 7 the band is lo < -7 % 7 = 2. A first draw of 0 gives
		// lo = 0, inside the band. The second, 1<<60, gives lo = 7<<60,
		// outside it, and hi = 0.
		r := &scriptedRand{values: []uint64{0, 1 << 60}}
		assert.Equal(t, rand.Uint64N(r, 7), 0, "Uint64N must return the hi of the second draw")
		assert.Equal(t, r.pos, 2, "Uint64N must take exactly two draws")
	})

	t.Run("keeps a draw at the edge of the band of bias", func(t *testing.T) {
		t.Parallel()
		// For n = 3 the band is lo < -3 % 3 = 1. The modular inverse of 3
		// gives 3x = 2*2^64 + 1, so lo = 1, at the edge, and hi = 2.
		r := &scriptedRand{values: []uint64{12297829382473034411, 4}}
		assert.Equal(t, rand.Uint64N(r, 3), 2, "Uint64N must return the hi of the first draw")
		assert.Equal(t, r.pos, 1, "Uint64N must not redraw for a lo equal to the band")
	})
}

// TestHelpersAllocs checks the allocation contracts of Float64, Shuffle
// and Uint64N. MaxAllocs counts the allocations of the whole process, so
// the test does not run in parallel.
//
//nolint:paralleltest // see above
func TestHelpersAllocs(t *testing.T) {
	r := pcg.New(rand.Seed(1))

	t.Run("Float64", func(t *testing.T) {
		var got float64
		expect.MaxAllocs(t, func() { got = rand.Float64(r) }, 0, "Float64 must not allocate")
		assert.InRange(t, got, 0, math.Nextafter(1, 0), "the test must measure a value in [0, 1)")
	})

	t.Run("Shuffle", func(t *testing.T) {
		calls := 0
		expect.MaxAllocs(t, func() { rand.Shuffle(r, 16, func(int, int) { calls++ }) }, 0,
			"Shuffle must not allocate")
		assert.InRange(t, calls, 1, 1<<63, "the test must measure calls of swap")
	})

	t.Run("Uint64N", func(t *testing.T) {
		var got uint64
		expect.MaxAllocs(t, func() { got = rand.Uint64N(r, 100) }, 0, "Uint64N must not allocate")
		assert.InRange(t, got, 0, 99, "the test must measure a value below n")
	})
}

// BenchmarkHelpers reports the cost of Float64, Shuffle and Uint64N on a
// pcg source, and fails when one of them allocates.
func BenchmarkHelpers(b *testing.B) {
	b.Run("Float64", func(b *testing.B) {
		r := pcg.New(rand.Seed(1))

		var got float64

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = rand.Float64(r)
		}

		assert.InRange(b, got, 0, math.Nextafter(1, 0), "the benchmark must measure a value in [0, 1)")
	})

	b.Run("Shuffle", func(b *testing.B) {
		for _, size := range []struct {
			name string
			n    int
		}{
			{"16", 16},
			{"256", 256},
			{"4K", 4096},
		} {
			b.Run(size.name, func(b *testing.B) {
				r := pcg.New(rand.Seed(1))
				calls := 0
				swap := func(int, int) { calls++ }

				c := bench.Start(b).MaxAllocs(0)
				defer c.End()

				for c.Loop() {
					rand.Shuffle(r, size.n, swap)
				}

				assert.InRange(b, calls, 1, 1<<63, "the benchmark must measure calls of swap")
			})
		}
	})

	b.Run("Uint64N", func(b *testing.B) {
		r := pcg.New(rand.Seed(1))

		var got uint64

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = rand.Uint64N(r, 1024)
		}

		assert.InRange(b, got, 0, 1023, "the benchmark must measure a value below n")
	})
}
