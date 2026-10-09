// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package pcg_test

import (
	"encoding/binary"
	"encoding/hex"
	"math"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"
	testkitbench "go.thesmos.sh/testkit/bench"

	"go.thesmos.sh/core/coretest/randtest"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/pcg"
)

// seeds generates every Seed.
var seeds = prop.Integer[rand.Seed](math.MinInt64, math.MaxInt64)

// TestPCGRandContract runs the contract suite of rand.Rand with the
// assertions of distinct draws and of determinism under one seed.
func TestPCGRandContract(t *testing.T) {
	t.Parallel()
	randtest.AssertRandContract(t, func() rand.Rand { return pcg.New(rand.Seed(1)) },
		append(randtest.RandContractAssertions(),
			randtest.RandUint64DistinctnessAssertion(),
			randtest.RandSeedDeterminismAssertion(
				func() rand.Rand { return pcg.New(rand.Seed(42)) },
				func() rand.Rand { return pcg.New(rand.Seed(42)) },
			),
		)...,
	)
}

// TestPCGRandModel runs the model test of the contract suite.
func TestPCGRandModel(t *testing.T) {
	t.Parallel()
	randtest.RandModelTest(t, func() rand.Rand { return pcg.New(rand.Seed(1)) })
}

func TestRand(t *testing.T) {
	t.Parallel()

	t.Run("New", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a Rand whose stream depends only on the seed", func(t *testing.T) {
			t.Parallel()
			assert.Deterministic(t, func(s rand.Seed) ([]byte, error) {
				p := make([]byte, 64)
				_, _ = pcg.New(s).Read(p)

				return p, nil
			}, rand.Seed(42), "two Rands of one seed must return one stream")
		})
	})

	t.Run("Seed", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the seed of New", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(s rand.Seed) rand.Seed { return pcg.New(s).Seed() },
				func(s rand.Seed) rand.Seed { return s }, "Seed must return the seed that New received",
				prop.Using(seeds), prop.Example(rand.Seed(123)))
		})

		t.Run("returns the seed of New after draws", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Seed must not change as the generator advances", func(c *prop.Case) {
				seed := c.Draw(seeds, "seed")
				r := pcg.New(seed)
				for range c.Draw(prop.Integer(0, 100), "draws") {
					r.Uint64()
				}
				assert.Equal(c, r.Seed(), seed, "Seed must return the seed of New")
			})
		})
	})

	t.Run("Read", func(t *testing.T) {
		t.Parallel()

		// A read of a size that is not a multiple of 8 drops the rest of
		// its last draw, so only reads of whole draws compose.
		t.Run("returns the stream of one read across reads of multiples of 8 bytes", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Read must return the stream of one read across reads of whole draws", func(c *prop.Case) {
				seed := c.Draw(seeds, "seed")
				sizes := c.Draw(prop.List(prop.Integer(0, 24).Map(func(n int) int { return 8 * n }),
					prop.MaxSize(8)), "sizes")
				total := 0
				for _, n := range sizes {
					total += n
				}
				single := make([]byte, total)
				_, _ = pcg.New(seed).Read(single)

				split := make([]byte, total)
				r := pcg.New(seed)
				at := 0
				for _, n := range sizes {
					_, _ = r.Read(split[at : at+n])
					at += n
				}
				assert.Equal(c, split, single, "reads of the drawn sizes must return the bytes of one read")
			})
		})

		// The vectors pin the byte stream of the PCG of math/rand/v2,
		// so a change of the standard library fails here.
		vectors := []struct {
			name string
			seed rand.Seed
			want string
		}{
			{
				name: "returns the recorded 32 bytes for seed 42",
				seed: 42,
				want: "9fa987db721b88dbe49fb0762d6df1f5c73270890a25e4227161b5e80282a816",
			},
			{
				name: "returns the recorded 17 bytes for seed 1",
				seed: 1,
				want: "0329edab29a127995653604a8c07d016f2",
			},
		}
		for _, tt := range vectors {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				want, err := hex.DecodeString(tt.want)
				assert.NoError(t, err, "the record must be hexadecimal")
				got := make([]byte, len(want))
				_, _ = pcg.New(tt.seed).Read(got)
				assert.Equal(t, got, want, "the stream must match its record")
			})
		}

		t.Run("returns the length of p", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(n int) int {
				got, _ := pcg.New(rand.Seed(1)).Read(make([]byte, n))

				return got
			}, func(n int) int { return n }, "Read must report every byte of p", prop.Using(prop.Integer(0, 200)))
		})

		t.Run("returns no error", func(t *testing.T) {
			t.Parallel()
			prop.NoError(t, func(n int) error {
				_, err := pcg.New(rand.Seed(1)).Read(make([]byte, n))

				return err
			}, "Read must not fail", prop.Using(prop.Integer(0, 200)))
		})
	})

	t.Run("Uint64", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the next 8 bytes of the stream in little-endian order", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(s rand.Seed) uint64 { return pcg.New(s).Uint64() }, func(s rand.Seed) uint64 {
				p := make([]byte, 8)
				_, _ = pcg.New(s).Read(p)

				return binary.LittleEndian.Uint64(p)
			}, "Uint64 must decode the bytes that Read returns", prop.Using(seeds))
		})
	})
}

// TestRandAllocs checks that Uint64 and Read allocate nothing.
// MaxAllocs counts the allocations of the whole process, so the test
// does not run in parallel.
func TestRandAllocs(t *testing.T) {
	r := pcg.New(rand.Seed(1))
	p := make([]byte, 100)

	t.Run("Uint64", func(t *testing.T) {
		var got uint64
		expect.MaxAllocs(t, func() { got = r.Uint64() }, 0, "Uint64 must not allocate")
		assert.NotEqual(t, got, 0, "the test must measure a draw")
	})

	t.Run("Read", func(t *testing.T) {
		var n int
		expect.MaxAllocs(t, func() { n, _ = r.Read(p) }, 0, "Read must not allocate")
		assert.Equal(t, n, len(p), "the test must measure a read of p")
	})
}

// BenchmarkPCGRand runs the benchmarks of the contract suite of
// rand.Rand.
func BenchmarkPCGRand(b *testing.B) {
	randtest.BenchmarkRandContract(b, func() rand.Rand { return pcg.New(rand.Seed(1)) },
		randtest.RandBenchOnUint64(testkitbench.PureAllocsWithin[rand.Rand, uint64](0)),
	)
}

// BenchmarkRand reports the cost of Uint64 and of a Read of 100 bytes,
// and fails when either allocates.
func BenchmarkRand(b *testing.B) {
	r := pcg.New(rand.Seed(1))

	b.Run("Uint64", func(b *testing.B) {
		var got uint64

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = r.Uint64()
		}

		assert.NotEqual(b, got, 0, "the benchmark must measure a draw")
	})

	b.Run("Read", func(b *testing.B) {
		p := make([]byte, 100)
		var n int

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			n, _ = r.Read(p)
		}

		assert.Equal(b, n, len(p), "the benchmark must measure a read of p")
	})
}

// FuzzPCGRandModel runs the model of the contract suite on the inputs
// that a fuzzer finds.
func FuzzPCGRandModel(f *testing.F) {
	randtest.RandModelFuzz(f, func() rand.Rand { return pcg.New(rand.Seed(1)) })
}
