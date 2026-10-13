// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package constant_test

import (
	"encoding/binary"
	"slices"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"
	testkitbench "go.thesmos.sh/testkit/bench"

	"go.thesmos.sh/core/coretest/randtest"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/constant"
)

// exactDraws is the number of the first draws of a Counter whose Float64
// returns the Float64 of FromFloat64 of v.
const exactDraws = 2048

// TestCounterRandContract runs the contract suite of rand.Rand with the
// assertions of distinct draws and of a deterministic stream.
func TestCounterRandContract(t *testing.T) {
	t.Parallel()
	randtest.AssertRandContract(t, func() rand.Rand { return constant.NewCounter(0.5) },
		append(randtest.RandContractAssertions(),
			randtest.RandUint64DistinctnessAssertion(),
			randtest.RandSeedDeterminismAssertion(
				func() rand.Rand { return constant.NewCounter(0.25) },
				func() rand.Rand { return constant.NewCounter(0.25) },
			),
		)...)
}

func TestCounter(t *testing.T) {
	t.Parallel()

	t.Run("NewCounter", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a Counter whose first 2,048 draws return v for a multiple of 2^-53", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Float64 of each of the first 2,048 draws must return v", func(pc *prop.Case) {
				v := float64(pc.Draw(prop.Integer[uint64](0, 1<<53-1), "k")) / (1 << 53)
				c := constant.NewCounter(v)
				got := make([]float64, 0, exactDraws)
				for range exactDraws {
					got = append(got, rand.Float64(c))
				}
				assert.Equal(pc, got, slices.Repeat([]float64{v}, exactDraws), "Float64 must return v on each draw")
			})
		})

		t.Run("returns a Counter whose first draw is the value of FromFloat64 of v", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(v float64) uint64 { return constant.NewCounter(v).Uint64() },
				func(v float64) uint64 { return constant.FromFloat64(v).Uint64() },
				"NewCounter must clamp v as FromFloat64 clamps it", prop.Using(prop.Float(-2.0, 2.0)),
				prop.Example(-1.0), prop.Example(1.5))
		})
	})

	t.Run("Uint64", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the value of FromFloat64 of v plus the number of earlier draws", func(t *testing.T) {
			t.Parallel()
			c := constant.NewCounter(0.5)
			base := constant.FromFloat64(0.5).Uint64()
			got, want := make([]uint64, 0, 3*exactDraws), make([]uint64, 0, 3*exactDraws)
			for i := range uint64(3 * exactDraws) {
				got, want = append(got, c.Uint64()), append(want, base+i)
			}
			assert.Equal(t, got, want, "draw n must return the value of FromFloat64 plus n")
		})

		t.Run("returns a Float64 of the next float above v at draw 2,048", func(t *testing.T) {
			t.Parallel()
			c := constant.NewCounter(0.5)
			for range exactDraws {
				c.Uint64()
			}
			assert.Equal(t, rand.Float64(c), 0.5+1.0/(1<<53), "draw 2,048 must return the next float above v")
		})

		t.Run("wraps to 0 at draw 2,048 for the largest v below 1", func(t *testing.T) {
			t.Parallel()
			c := constant.NewCounter(justBelowOne)
			for range exactDraws {
				c.Uint64()
			}
			assert.Equal(t, c.Uint64(), uint64(0), "the sum must wrap modulo 2^64")
		})

		t.Run("returns each draw once to concurrent callers", func(t *testing.T) {
			t.Parallel()
			const clients, draws = 4, 100
			c := constant.NewCounter(0.5)
			outcomes := history.Concurrently(clients, time.Minute, func(int) (any, error) {
				got := make([]uint64, 0, draws)
				for range draws {
					got = append(got, c.Uint64())
				}

				return got, nil
			})
			base := constant.FromFloat64(0.5).Uint64()
			got, want := make([]uint64, 0, clients*draws), make([]uint64, 0, clients*draws)
			for _, o := range outcomes {
				assert.True(t, o.Finished, "every client must finish")
				drawn, ok := o.Output.([]uint64)
				assert.True(t, ok, "every client must return its draws")
				got = append(got, drawn...)
			}
			for i := range uint64(clients * draws) {
				want = append(want, base+i)
			}
			assert.Permutation(t, got, want, "the clients must receive each draw once")
		})
	})

	t.Run("Read", func(t *testing.T) {
		t.Parallel()

		t.Run("fills each started 8 bytes of p with the next draw in little-endian order", func(t *testing.T) {
			t.Parallel()
			base := constant.FromFloat64(0.5).Uint64()
			prop.Equal(t, func(n int) []byte {
				p := make([]byte, n)
				_, _ = constant.NewCounter(0.5).Read(p)

				return p
			}, func(n int) []byte {
				want := make([]byte, 0, n+8)
				for i := range uint64((n + 7) / 8) {
					want = binary.LittleEndian.AppendUint64(want, base+i)
				}

				return want[:n]
			}, "Read must fill p with successive draws", prop.Using(prop.Integer(0, 100)),
				prop.Example(0), prop.Example(1), prop.Example(8), prop.Example(9), prop.Example(16))
		})

		t.Run("takes one draw for each started 8 bytes of p", func(t *testing.T) {
			t.Parallel()
			base := constant.FromFloat64(0.5).Uint64()
			prop.Equal(t, func(n int) uint64 {
				c := constant.NewCounter(0.5)
				_, _ = c.Read(make([]byte, n))

				return c.Uint64()
			}, func(n int) uint64 { return base + uint64((n+7)/8) }, "Read must take one draw for each started 8 bytes",
				prop.Using(prop.Integer(0, 100)), prop.Example(0), prop.Example(8), prop.Example(9))
		})

		t.Run("returns the length of p", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(n int) int {
				got, _ := constant.NewCounter(0.5).Read(make([]byte, n))

				return got
			}, func(n int) int { return n }, "Read must report every byte of p", prop.Using(prop.Integer(0, 100)))
		})

		t.Run("returns no error", func(t *testing.T) {
			t.Parallel()
			prop.NoError(t, func(n int) error {
				_, err := constant.NewCounter(0.5).Read(make([]byte, n))

				return err
			}, "Read must not fail", prop.Using(prop.Integer(0, 100)))
		})
	})
}

// TestCounterAllocs checks that Uint64 and Read allocate nothing. MaxAllocs
// counts the allocations of the whole process, so the test does not run in
// parallel.
func TestCounterAllocs(t *testing.T) {
	c := constant.NewCounter(0.5)
	p := make([]byte, 100)

	t.Run("Uint64", func(t *testing.T) {
		var got uint64
		expect.MaxAllocs(t, func() { got = c.Uint64() }, 0, "Uint64 must not allocate")
		assert.NotEqual(t, got, constant.FromFloat64(0.5).Uint64(), "the test must measure a later draw")
	})

	t.Run("Read", func(t *testing.T) {
		var n int
		expect.MaxAllocs(t, func() { n, _ = c.Read(p) }, 0, "Read must not allocate")
		assert.Equal(t, n, len(p), "the test must measure a read of p")
	})
}

// BenchmarkCounterRand runs the benchmarks of the contract suite of
// rand.Rand.
func BenchmarkCounterRand(b *testing.B) {
	randtest.BenchmarkRandContract(b, func() rand.Rand { return constant.NewCounter(0.5) },
		randtest.RandBenchOnUint64(testkitbench.PureAllocsWithin[rand.Rand, uint64](0)),
	)
}

// BenchmarkCounter reports the cost of Uint64 and of a Read of 100 bytes,
// and fails when either allocates.
func BenchmarkCounter(b *testing.B) {
	c := constant.NewCounter(0.5)

	b.Run("Uint64", func(b *testing.B) {
		var got uint64

		bc := bench.Start(b).MaxAllocs(0)
		defer bc.End()

		for bc.Loop() {
			got = c.Uint64()
		}

		assert.NotEqual(b, got, constant.FromFloat64(0.5).Uint64(), "the benchmark must measure later draws")
	})

	b.Run("Read", func(b *testing.B) {
		p := make([]byte, 100)
		var n int

		bc := bench.Start(b).MaxAllocs(0)
		defer bc.End()

		for bc.Loop() {
			n, _ = c.Read(p)
		}

		assert.Equal(b, n, len(p), "the benchmark must measure a read of p")
	})
}
