// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package uuidv4_test

import (
	"encoding/binary"
	"math"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"
	testkitbench "go.thesmos.sh/testkit/bench"

	"go.thesmos.sh/core/coretest/idtest"
	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/uuidv4"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/seeded"
)

// The goroutines of a concurrent case, and the UUIDs that each generates.
const (
	goroutines = 8
	rounds     = 20
)

// seeds generates the seeds of the sources of the generators.
var seeds = prop.Integer[rand.Seed](math.MinInt64, math.MaxInt64)

// TestUUIDv4GeneratorContract runs the contract suite of id.Generator with
// a seeded source.
func TestUUIDv4GeneratorContract(t *testing.T) {
	t.Parallel()
	idtest.AssertGeneratorContract(t, func() id.Generator { return uuidv4.New(seeded.New(rand.Seed(1))) },
		append(idtest.GeneratorContractAssertions(),
			idtest.GeneratorSizeAssertion(id.Size128),
		)...,
	)
}

// TestUUIDv4GeneratorModel runs the model test of id.Generator.
func TestUUIDv4GeneratorModel(t *testing.T) {
	t.Parallel()
	idtest.GeneratorModelTest(t, func() id.Generator { return uuidv4.New(seeded.New(rand.Seed(1))) })
}

// FuzzUUIDv4GeneratorModel runs the model test of id.Generator on the
// inputs that a fuzzer finds.
func FuzzUUIDv4GeneratorModel(f *testing.F) {
	idtest.GeneratorModelFuzz(f, func() id.Generator { return uuidv4.New(seeded.New(rand.Seed(1))) })
}

func TestUUIDv4(t *testing.T) {
	t.Parallel()

	t.Run("Generator", func(t *testing.T) {
		t.Parallel()

		t.Run("Generate", func(t *testing.T) {
			t.Parallel()

			t.Run("returns an ID of Size128", func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, uuidv4.New(seeded.New(rand.Seed(1))).Generate().Size(), id.Size128,
					"a UUIDv4 must have 128 bits")
			})

			t.Run("returns version 4 in the high four bits of byte 6", func(t *testing.T) {
				t.Parallel()
				prop.ForAll(t, "Generate must stamp version 4", func(c *prop.Case) {
					b := uuidv4.New(seeded.New(c.Draw(seeds, "seed"))).Generate().Bytes()
					assert.Equal(c, b[6]&0xF0, byte(0x40), "byte 6 must encode version 4")
				})
			})

			t.Run("returns variant 10 in the high two bits of byte 8", func(t *testing.T) {
				t.Parallel()
				prop.ForAll(t, "Generate must stamp the variant of RFC 9562", func(c *prop.Case) {
					b := uuidv4.New(seeded.New(c.Draw(seeds, "seed"))).Generate().Bytes()
					assert.Equal(c, b[8]&0xC0, byte(0x80), "byte 8 must encode variant 10")
				})
			})

			t.Run("returns the bits of two Uint64 values of the source in the other bits", func(t *testing.T) {
				t.Parallel()
				prop.ForAll(t, "Generate must keep every random bit outside the version and the variant",
					func(c *prop.Case) {
						seed := c.Draw(seeds, "seed")

						u := uuidv4.New(seeded.New(seed)).Generate()
						src := seeded.New(seed)
						hi := src.Uint64()
						want := binary.BigEndian.AppendUint64(binary.BigEndian.AppendUint64(nil, hi), src.Uint64())
						got := u.Bytes()
						want[6], got[6] = want[6]&0x0F, got[6]&0x0F
						want[8], got[8] = want[8]&0x3F, got[8]&0x3F
						assert.Equal(c, got, want, "every other bit must be a bit of the source")
					})
			})

			t.Run("returns the same IDs for the same seed", func(t *testing.T) {
				t.Parallel()
				assert.Deterministic(t, func(s rand.Seed) (id.ID, error) {
					return uuidv4.New(seeded.New(s)).Generate(), nil
				}, rand.Seed(42), "one seed must produce one UUIDv4")
			})

			// The two reads of one call can interleave with the reads of
			// another, so each value of the source fills one half of one
			// UUID, in no fixed order. stamped covers the bits of either
			// half that Generate overwrites: the variant in the high bits
			// of byte 8 and the version in the high bits of byte 6.
			t.Run("returns IDs that use each value of the source once to goroutines that generate at once",
				func(t *testing.T) {
					t.Parallel()
					const stamped uint64 = 0xC000_0000_0000_F000
					g := uuidv4.New(seeded.New(rand.Seed(1)))
					outcomes := history.Concurrently(goroutines, 10*time.Second, func(int) (any, error) {
						ids := make([]id.ID, 0, rounds)
						for range rounds {
							ids = append(ids, g.Generate())
						}

						return ids, nil
					})

					var halves []uint64
					for _, o := range outcomes {
						assert.True(t, o.Finished, "every goroutine must finish")
						ids, _ := o.Output.([]id.ID)
						for _, u := range ids {
							b := u.Bytes()
							halves = append(halves, binary.BigEndian.Uint64(b)&^stamped,
								binary.BigEndian.Uint64(b[8:])&^stamped)
						}
					}

					src := seeded.New(rand.Seed(1))
					want := make([]uint64, 0, 2*goroutines*rounds)
					for range 2 * goroutines * rounds {
						want = append(want, src.Uint64()&^stamped)
					}
					assert.Permutation(t, halves, want, "the UUIDs must use each value of the source once")
				})
		})
	})
}

// TestUUIDv4Allocs checks the allocation contract of Generate over a
// seeded source. MaxAllocs counts the allocations of the whole process,
// so the test does not run in parallel.
func TestUUIDv4Allocs(t *testing.T) {
	g := uuidv4.New(seeded.New(rand.Seed(1)))

	t.Run("Generator", func(t *testing.T) {
		t.Run("Generate", func(t *testing.T) {
			var got id.ID
			expect.MaxAllocs(t, func() { got = g.Generate() }, 0, "Generate over a seeded source must not allocate")
			assert.Equal(t, got.Size(), id.Size128, "the test must measure a UUIDv4")
		})
	})
}

// BenchmarkUUIDv4Generator runs the benchmarks of the contract suite of
// id.Generator.
func BenchmarkUUIDv4Generator(b *testing.B) {
	idtest.BenchmarkGeneratorContract(b, func() id.Generator { return uuidv4.New(seeded.New(rand.Seed(1))) },
		idtest.GeneratorBenchOnGenerate(testkitbench.PureAllocsWithin[id.Generator, id.ID](0)),
	)
}

// BenchmarkUUIDv4 reports the cost of Generate over a seeded source, and
// fails when it allocates.
func BenchmarkUUIDv4(b *testing.B) {
	b.Run("Generator", func(b *testing.B) {
		b.Run("Generate", func(b *testing.B) {
			g := uuidv4.New(seeded.New(rand.Seed(1)))
			var got id.ID

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got = g.Generate()
			}

			assert.Equal(b, got.Size(), id.Size128, "the benchmark must measure a UUIDv4")
		})
	})
}
