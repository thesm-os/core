// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package ksuid_test

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

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/coretest/idtest"
	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/ksuid"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/seeded"
)

// The goroutines of a concurrent case, and the KSUIDs that each
// generates.
const (
	goroutines = 8
	rounds     = 20
)

// origin is the wall time of the KSUIDs of the tests, past the KSUID
// epoch of 2014-05-13.
var origin = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// seeds generates the seeds of the sources of the generators.
var seeds = prop.Integer[rand.Seed](math.MinInt64, math.MaxInt64)

// TestKSUIDGeneratorContract runs the contract suite of id.Generator with
// a seeded source, whose entropy advances per call.
func TestKSUIDGeneratorContract(t *testing.T) {
	t.Parallel()
	idtest.AssertGeneratorContract(t,
		func() id.Generator { return ksuid.New(fake.New(origin), seeded.New(rand.Seed(1))) },
		append(idtest.GeneratorContractAssertions(),
			idtest.GeneratorSizeAssertion(id.Size160),
		)...,
	)
}

// TestKSUIDGeneratorModel runs the model test of id.Generator.
func TestKSUIDGeneratorModel(t *testing.T) {
	t.Parallel()
	idtest.GeneratorModelTest(t, func() id.Generator { return ksuid.New(fake.New(origin), seeded.New(rand.Seed(1))) })
}

// FuzzKSUIDGeneratorModel runs the model test of id.Generator on the
// inputs that a fuzzer finds.
func FuzzKSUIDGeneratorModel(f *testing.F) {
	idtest.GeneratorModelFuzz(f, func() id.Generator { return ksuid.New(fake.New(origin), seeded.New(rand.Seed(1))) })
}

func TestKSUID(t *testing.T) {
	t.Parallel()

	t.Run("Generator", func(t *testing.T) {
		t.Parallel()

		t.Run("Generate", func(t *testing.T) {
			t.Parallel()

			t.Run("returns an ID of Size160", func(t *testing.T) {
				t.Parallel()
				u := ksuid.New(fake.New(origin), seeded.New(rand.Seed(1))).Generate()
				assert.Equal(t, u.Size(), id.Size160, "a KSUID must have 160 bits")
			})

			t.Run("returns the seconds of the clock since the KSUID epoch", func(t *testing.T) {
				t.Parallel()
				prop.ForAll(t, "Generate must encode the seconds of the clock", func(c *prop.Case) {
					secs := c.Draw(prop.Integer[int64](ksuid.Epoch, ksuid.Epoch+math.MaxUint32), "seconds")
					nanos := c.Draw(prop.Integer[int64](0, int64(time.Second)-1), "nanoseconds")

					u := ksuid.New(fake.New(time.Unix(secs, nanos)), seeded.New(rand.Seed(1))).Generate()
					assert.Equal(c, ksuid.TimestampSeconds(u), secs, "TimestampSeconds must decode the seconds")
				})
			})

			t.Run("returns two Uint64 values of the source after the timestamp", func(t *testing.T) {
				t.Parallel()
				prop.ForAll(t, "Generate must append two Uint64 values of the source", func(c *prop.Case) {
					seed := c.Draw(seeds, "seed")

					u := ksuid.New(fake.New(origin), seeded.New(seed)).Generate()
					src := seeded.New(seed)
					hi := src.Uint64()
					want := binary.BigEndian.AppendUint64(binary.BigEndian.AppendUint64(nil, hi), src.Uint64())
					assert.Equal(c, u.Bytes()[4:], want, "bytes 4 to 19 must be the two values in big-endian order")
				})
			})

			t.Run("returns an ID that sorts after an ID of an earlier second", func(t *testing.T) {
				t.Parallel()
				clk := fake.New(origin)
				g := ksuid.New(clk, seeded.New(rand.Seed(1)))

				first := g.Generate()
				clk.Advance(2 * time.Second)
				assert.Equal(t, first.Compare(g.Generate()), -1, "an earlier KSUID must sort before a later one")
			})

			t.Run("returns the same IDs for the same clock and seed", func(t *testing.T) {
				t.Parallel()
				assert.Deterministic(t, func(s rand.Seed) (id.ID, error) {
					return ksuid.New(fake.New(origin), seeded.New(s)).Generate(), nil
				}, rand.Seed(42), "one clock and one seed must produce one KSUID")
			})

			// The two reads of one call can interleave with the reads of
			// another, so each value of the source fills one half of one
			// KSUID, in no fixed order.
			t.Run("returns IDs that use each value of the source once to goroutines that generate at once",
				func(t *testing.T) {
					t.Parallel()
					g := ksuid.New(fake.New(origin), seeded.New(rand.Seed(1)))
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
							random := u.Bytes()[4:]
							halves = append(halves, binary.BigEndian.Uint64(random),
								binary.BigEndian.Uint64(random[8:]))
						}
					}

					src := seeded.New(rand.Seed(1))
					want := make([]uint64, 0, 2*goroutines*rounds)
					for range 2 * goroutines * rounds {
						want = append(want, src.Uint64())
					}
					assert.Permutation(t, halves, want, "the KSUIDs must use each value of the source once")
				})
		})
	})

	t.Run("TimestampSeconds", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give id.ID
			want int64
		}{
			{name: "returns 0 for Zero", give: id.Zero, want: 0},
			{
				name: "returns 0 for an ID shorter than 160 bits",
				give: id.New128([id.Size128]byte{0xff, 0xff, 0xff, 0xff}),
				want: 0,
			},
			{name: "returns Epoch for an offset of zero", give: id.New160([id.Size160]byte{}), want: ksuid.Epoch},
			{
				name: "returns the seconds of the first four bytes of a 256-bit ID",
				give: id.New256([id.Size256]byte{0, 0, 0, 5}),
				want: ksuid.Epoch + 5,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, ksuid.TimestampSeconds(tt.give), tt.want, "TimestampSeconds must decode the offset")
			})
		}
	})
}

// TestKSUIDAllocs checks the allocation contracts of Generate over a
// seeded source and of TimestampSeconds. MaxAllocs counts the
// allocations of the whole process, so the test does not run in
// parallel.
func TestKSUIDAllocs(t *testing.T) {
	g := ksuid.New(fake.New(origin), seeded.New(rand.Seed(1)))
	u := g.Generate()

	t.Run("Generator", func(t *testing.T) {
		t.Run("Generate", func(t *testing.T) {
			var got id.ID
			expect.MaxAllocs(t, func() { got = g.Generate() }, 0, "Generate over a seeded source must not allocate")
			assert.Equal(t, got.Size(), id.Size160, "the test must measure a KSUID")
		})
	})

	t.Run("TimestampSeconds", func(t *testing.T) {
		var secs int64
		expect.MaxAllocs(t, func() { secs = ksuid.TimestampSeconds(u) }, 0, "TimestampSeconds must not allocate")
		assert.Equal(t, secs, origin.Unix(), "the test must measure the seconds of the KSUID")
	})
}

// BenchmarkKSUIDGenerator runs the benchmarks of the contract suite of
// id.Generator.
func BenchmarkKSUIDGenerator(b *testing.B) {
	idtest.BenchmarkGeneratorContract(b,
		func() id.Generator { return ksuid.New(fake.New(origin), seeded.New(rand.Seed(1))) },
		idtest.GeneratorBenchOnGenerate(testkitbench.PureAllocsWithin[id.Generator, id.ID](0)),
	)
}

// BenchmarkKSUID reports the cost of Generate over a seeded source and of
// TimestampSeconds, and fails when one allocates.
func BenchmarkKSUID(b *testing.B) {
	g := ksuid.New(fake.New(origin), seeded.New(rand.Seed(1)))

	b.Run("Generator", func(b *testing.B) {
		b.Run("Generate", func(b *testing.B) {
			var got id.ID

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got = g.Generate()
			}

			assert.Equal(b, got.Size(), id.Size160, "the benchmark must measure a KSUID")
		})
	})

	b.Run("TimestampSeconds", func(b *testing.B) {
		u := g.Generate()
		var secs int64

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			secs = ksuid.TimestampSeconds(u)
		}

		assert.Equal(b, secs, origin.Unix(), "the benchmark must measure the seconds of the KSUID")
	})
}
