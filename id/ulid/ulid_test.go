// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package ulid_test

import (
	"encoding/binary"
	"math"
	"slices"
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
	"go.thesmos.sh/core/id/ulid"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/seeded"
)

// callers is the number of callers that call Generate at once in the
// concurrent case, and idsPerCaller the number of IDs of each.
const (
	callers      = 8
	idsPerCaller = 1000
)

// origin is the wall time of the ULIDs of the tests.
var origin = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// TestULIDGeneratorContract runs the contract suite of id.Generator with
// a seeded source, whose entropy advances per call.
func TestULIDGeneratorContract(t *testing.T) {
	t.Parallel()
	idtest.AssertGeneratorContract(t,
		func() id.Generator { return ulid.New(fake.New(origin), seeded.New(rand.Seed(1))) },
		append(idtest.GeneratorContractAssertions(),
			idtest.GeneratorSizeAssertion(id.Size128),
		)...,
	)
}

// TestULIDGeneratorModel runs the model test of id.Generator.
func TestULIDGeneratorModel(t *testing.T) {
	t.Parallel()
	idtest.GeneratorModelTest(t, func() id.Generator { return ulid.New(fake.New(origin), seeded.New(rand.Seed(1))) })
}

// FuzzULIDGeneratorModel runs the model test of id.Generator on the
// inputs that a fuzzer finds.
func FuzzULIDGeneratorModel(f *testing.F) {
	idtest.GeneratorModelFuzz(f, func() id.Generator { return ulid.New(fake.New(origin), seeded.New(rand.Seed(1))) })
}

func TestULID(t *testing.T) {
	t.Parallel()

	t.Run("Generator", func(t *testing.T) {
		t.Parallel()

		t.Run("Generate", func(t *testing.T) {
			t.Parallel()

			t.Run("returns an ID of Size128", func(t *testing.T) {
				t.Parallel()
				u := ulid.New(fake.New(origin), seeded.New(rand.Seed(1))).Generate()
				assert.Equal(t, u.Size(), id.Size128, "a ULID must have 128 bits")
			})

			// A clock reads its wall time in int64 nanoseconds, which end
			// in 2262, before the 48 bits of milliseconds do.
			t.Run("returns the milliseconds of the clock in its first six bytes", func(t *testing.T) {
				t.Parallel()
				prop.ForAll(t, "Generate must encode the milliseconds of the clock", func(c *prop.Case) {
					ms := c.Draw(prop.Integer[int64](0, math.MaxInt64/int64(time.Millisecond)-1), "milliseconds")
					nanos := c.Draw(prop.Integer[int64](0, int64(time.Millisecond)-1), "nanoseconds")

					clk := fake.New(time.UnixMilli(ms).Add(time.Duration(nanos)))
					u := ulid.New(clk, seeded.New(rand.Seed(1))).Generate()
					assert.Equal(c, ulid.TimestampMillis(u), uint64(ms), "TimestampMillis must decode the milliseconds")
				})
			})

			t.Run("returns 80 bits of two Uint64 values of the source after the timestamp", func(t *testing.T) {
				t.Parallel()
				prop.ForAll(t, "Generate must append the first Uint64 and 16 bits of the second", func(c *prop.Case) {
					seed := c.Draw(prop.Integer[rand.Seed](math.MinInt64, math.MaxInt64), "seed")

					u := ulid.New(fake.New(origin), seeded.New(seed)).Generate()
					src := seeded.New(seed)
					hi, lo := src.Uint64(), uint16(src.Uint64()>>48)
					want := binary.BigEndian.AppendUint16(binary.BigEndian.AppendUint64(nil, hi), lo)
					assert.Equal(c, u.Bytes()[6:], want, "bytes 6 to 15 must be the bits in big-endian order")
				})
			})

			t.Run("returns an ID that sorts after an ID of an earlier millisecond", func(t *testing.T) {
				t.Parallel()
				clk := fake.New(origin)
				g := ulid.New(clk, seeded.New(rand.Seed(1)))

				first := g.Generate()
				clk.Advance(time.Millisecond)
				assert.Equal(t, first.Compare(g.Generate()), -1, "an earlier ULID must sort before a later one")
			})

			t.Run("returns the same IDs for the same clock and seed", func(t *testing.T) {
				t.Parallel()
				assert.Deterministic(t, func(s rand.Seed) (id.ID, error) {
					return ulid.New(fake.New(origin), seeded.New(s)).Generate(), nil
				}, rand.Seed(42), "one clock and one seed must produce one ULID")
			})

			// The clock repeats one millisecond, so the random bits alone
			// separate the IDs, and the seeded source serialises its draws.
			t.Run("returns distinct IDs to concurrent callers", func(t *testing.T) {
				t.Parallel()
				g := ulid.New(fake.New(origin), seeded.New(rand.Seed(1)))
				outcomes := history.Concurrently(callers, 10*time.Second, func(int) (any, error) {
					ids := make([]id.ID, idsPerCaller)
					for i := range ids {
						ids[i] = g.Generate()
					}

					return ids, nil
				})

				all := make([]id.ID, 0, callers*idsPerCaller)
				for _, o := range outcomes {
					assert.True(t, o.Finished, "every caller must finish")
					ids, _ := o.Output.([]id.ID)
					all = append(all, ids...)
				}
				assert.Length(t, all, callers*idsPerCaller, "every caller must return its IDs")
				slices.SortFunc(all, id.ID.Compare)
				assert.Pairwise(t, all, func(earlier, later id.ID) bool { return earlier.Compare(later) < 0 },
					"no two calls may return one ID")
			})
		})
	})

	t.Run("TimestampMillis", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give id.ID
			want uint64
		}{
			{name: "returns 0 for Zero", give: id.Zero, want: 0},
			{
				name: "returns the big-endian 48-bit prefix of a 128-bit ID",
				give: id.New128([id.Size128]byte{0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC}),
				want: 0x123456789ABC,
			},
			{
				name: "returns the big-endian 48-bit prefix of a 256-bit ID",
				give: id.New256([id.Size256]byte{0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC}),
				want: 0x123456789ABC,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, ulid.TimestampMillis(tt.give), tt.want, "TimestampMillis must decode the prefix")
			})
		}
	})
}

// TestULIDAllocs checks the allocation contracts of Generate over a
// seeded source and of TimestampMillis. MaxAllocs counts the allocations
// of the whole process, so the test does not run in parallel.
func TestULIDAllocs(t *testing.T) {
	g := ulid.New(fake.New(origin), seeded.New(rand.Seed(1)))
	u := g.Generate()

	t.Run("Generator", func(t *testing.T) {
		t.Run("Generate", func(t *testing.T) {
			var got id.ID
			expect.MaxAllocs(t, func() { got = g.Generate() }, 0, "Generate over a seeded source must not allocate")
			assert.Equal(t, got.Size(), id.Size128, "the test must measure a ULID")
		})
	})

	t.Run("TimestampMillis", func(t *testing.T) {
		var ms uint64
		expect.MaxAllocs(t, func() { ms = ulid.TimestampMillis(u) }, 0, "TimestampMillis must not allocate")
		assert.Equal(t, ms, uint64(origin.UnixMilli()), "the test must measure the milliseconds of the ULID")
	})
}

// BenchmarkULIDGenerator runs the benchmarks of the contract suite of
// id.Generator.
func BenchmarkULIDGenerator(b *testing.B) {
	idtest.BenchmarkGeneratorContract(b,
		func() id.Generator { return ulid.New(fake.New(origin), seeded.New(rand.Seed(1))) },
		idtest.GeneratorBenchOnGenerate(testkitbench.PureAllocsWithin[id.Generator, id.ID](0)),
	)
}

// BenchmarkULID reports the cost of Generate over a seeded source and of
// TimestampMillis, and fails when one allocates.
func BenchmarkULID(b *testing.B) {
	g := ulid.New(fake.New(origin), seeded.New(rand.Seed(1)))

	b.Run("Generator", func(b *testing.B) {
		b.Run("Generate", func(b *testing.B) {
			var got id.ID

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got = g.Generate()
			}

			assert.Equal(b, got.Size(), id.Size128, "the benchmark must measure a ULID")
		})
	})

	b.Run("TimestampMillis", func(b *testing.B) {
		u := g.Generate()
		var ms uint64

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			ms = ulid.TimestampMillis(u)
		}

		assert.Equal(b, ms, uint64(origin.UnixMilli()), "the benchmark must measure the milliseconds of the ULID")
	})
}
