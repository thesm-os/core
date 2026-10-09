// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package seeded_test

import (
	"encoding/binary"
	"encoding/hex"
	"math"
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
	"go.thesmos.sh/core/rand/seeded"
)

// opUint64 is the operation that the history of the concurrent draws
// records for a call of Uint64.
const opUint64 = "uint64"

// seeds generates every Seed.
var seeds = prop.Integer[rand.Seed](math.MinInt64, math.MaxInt64)

// TestSeededRandContract runs the contract suite of rand.Rand with the
// assertions of distinct draws and of determinism under one seed.
func TestSeededRandContract(t *testing.T) {
	t.Parallel()
	randtest.AssertRandContract(t, func() rand.Rand { return seeded.New(rand.Seed(1)) },
		append(randtest.RandContractAssertions(),
			randtest.RandUint64DistinctnessAssertion(),
			randtest.RandSeedDeterminismAssertion(
				func() rand.Rand { return seeded.New(rand.Seed(42)) },
				func() rand.Rand { return seeded.New(rand.Seed(42)) },
			),
		)...,
	)
}

// TestSeededRandModel runs the model test of the contract suite.
func TestSeededRandModel(t *testing.T) {
	t.Parallel()
	randtest.RandModelTest(t, func() rand.Rand { return seeded.New(rand.Seed(1)) })
}

func TestRand(t *testing.T) {
	t.Parallel()

	t.Run("New", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a Rand whose stream depends only on the seed", func(t *testing.T) {
			t.Parallel()
			assert.Deterministic(t, func(s rand.Seed) ([]byte, error) {
				p := make([]byte, 64)
				_, _ = seeded.New(s).Read(p)

				return p, nil
			}, rand.Seed(42), "two Rands of one seed must return one stream")
		})

		// HMAC-SHA-256 is deterministic and the key derivation, the
		// big-endian seed, is part of the contract, so the stream of a
		// seed must not change across versions.
		t.Run("returns a Rand whose stream for seed 0xabcd matches its record", func(t *testing.T) {
			t.Parallel()
			want, err := hex.DecodeString("c1dfde25ff46bc350cbd30fd529c74c4e4e820f2e2ea56dff62b4e1b60e75c56" +
				"df4a9b70aa92cd69a9a3d37de3ff9a5baa97ee15640101987fd736296aa2cfcc")
			assert.NoError(t, err, "the record must be hexadecimal")
			got := make([]byte, len(want))
			_, _ = seeded.New(rand.Seed(0xabcd)).Read(got)
			assert.Equal(t, got, want, "the stream of seed 0xabcd must match its record")
		})
	})

	t.Run("NewFromBytes", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a Rand whose stream depends only on the seed bytes", func(t *testing.T) {
			t.Parallel()
			assert.Deterministic(t, func(seed string) ([]byte, error) {
				p := make([]byte, 128)
				_, _ = seeded.NewFromBytes([]byte(seed)).Read(p)

				return p, nil
			}, "test-seed-bytes-2026", "two Rands of one byte seed must return one stream")
		})

		t.Run("returns a Rand whose Seed reports SeedUnspecified", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, seeded.NewFromBytes([]byte("anything")).Seed(), rand.SeedUnspecified,
				"no int64 recovers a byte seed")
		})
	})

	t.Run("Seed", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the seed of New", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(s rand.Seed) rand.Seed { return seeded.New(s).Seed() },
				func(s rand.Seed) rand.Seed { return s }, "Seed must return the seed that New received",
				prop.Using(seeds), prop.Example(rand.Seed(99)))
		})
	})

	t.Run("Read", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the stream of one read across reads of any sizes", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Read must return the stream of one read across reads of any sizes", func(c *prop.Case) {
				seed := c.Draw(seeds, "seed")
				sizes := c.Draw(prop.List(prop.Integer(0, 80), prop.MaxSize(8)), "sizes")
				total := 0
				for _, n := range sizes {
					total += n
				}
				single := make([]byte, total)
				_, _ = seeded.New(seed).Read(single)

				split := make([]byte, total)
				r := seeded.New(seed)
				at := 0
				for _, n := range sizes {
					_, _ = r.Read(split[at : at+n])
					at += n
				}
				assert.Equal(c, split, single, "reads of the drawn sizes must return the bytes of one read")
			})
		})

		// A counter that stalls repeats its block.
		t.Run("returns another block for each counter", func(t *testing.T) {
			t.Parallel()
			buf := make([]byte, 96)
			_, _ = seeded.New(rand.Seed(123)).Read(buf)
			expect.NotEqual(t, buf[0:32], buf[32:64], "the second block must differ from the first")
			expect.NotEqual(t, buf[32:64], buf[64:96], "the third block must differ from the second")
		})

		t.Run("returns the length of p", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(n int) int {
				got, _ := seeded.New(rand.Seed(1)).Read(make([]byte, n))

				return got
			}, func(n int) int { return n }, "Read must report every byte of p", prop.Using(prop.Integer(0, 200)))
		})

		t.Run("returns no error", func(t *testing.T) {
			t.Parallel()
			prop.NoError(t, func(n int) error {
				_, err := seeded.New(rand.Seed(1)).Read(make([]byte, n))

				return err
			}, "Read must not fail", prop.Using(prop.Integer(0, 200)))
		})
	})

	t.Run("Uint64", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the next 8 bytes of the stream in little-endian order", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(s rand.Seed) uint64 { return seeded.New(s).Uint64() }, func(s rand.Seed) uint64 {
				p := make([]byte, 8)
				_, _ = seeded.New(s).Read(p)

				return binary.LittleEndian.Uint64(p)
			}, "Uint64 must decode the bytes that Read returns", prop.Using(seeds))
		})

		// The history of the calls must linearize against the stream of a
		// Rand of the same seed, so the mutex gives each value of the
		// stream to one call.
		t.Run("returns the values of one stream to concurrent callers", func(t *testing.T) {
			t.Parallel()
			const (
				clients = 8
				draws   = 64
			)

			stream := make([]uint64, clients*draws)
			reference := seeded.New(rand.Seed(7))
			for i := range stream {
				stream[i] = reference.Uint64()
			}

			r := seeded.New(rand.Seed(7))
			h := history.New()
			outcomes := history.Concurrently(clients, 10*time.Second, func(client int) (any, error) {
				for range draws {
					call := h.Invoke(client, opUint64, nil)
					call.OK(r.Uint64())
				}

				return client, nil
			})
			for _, o := range outcomes {
				assert.True(t, o.Finished, "every client must finish")
			}

			history.Linearizable(t, h, history.Spec[int]{
				Initial: func() int { return 0 },
				Next: func(drawn int, op history.Operation) []int {
					if drawn == len(stream) || !op.Returned(stream[drawn]) {
						return nil
					}

					return []int{drawn + 1}
				},
			}, "concurrent calls must return the values of the stream in an order of their calls")
		})
	})
}

// TestRandAllocs checks that Uint64 and Read allocate nothing.
// MaxAllocs counts the allocations of the whole process, so the test
// does not run in parallel.
func TestRandAllocs(t *testing.T) {
	r := seeded.New(rand.Seed(1))
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

// BenchmarkSeededRand runs the benchmarks of the contract suite of
// rand.Rand.
func BenchmarkSeededRand(b *testing.B) {
	randtest.BenchmarkRandContract(b, func() rand.Rand { return seeded.New(rand.Seed(1)) },
		randtest.RandBenchOnUint64(testkitbench.PureAllocsWithin[rand.Rand, uint64](0)),
	)
}

// BenchmarkRand reports the cost of Uint64 and of a Read of 100 bytes,
// and fails when either allocates.
func BenchmarkRand(b *testing.B) {
	r := seeded.New(rand.Seed(1))

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

// FuzzSeededRandModel runs the model of the contract suite on the inputs
// that a fuzzer finds.
func FuzzSeededRandModel(f *testing.F) {
	randtest.RandModelFuzz(f, func() rand.Rand { return seeded.New(rand.Seed(1)) })
}
