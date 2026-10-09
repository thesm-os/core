// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package uuidv7_test

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
	"go.thesmos.sh/core/clock/hlc"
	"go.thesmos.sh/core/coretest/idtest"
	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/uuidv7"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/constant"
	"go.thesmos.sh/core/rand/crypto"
	"go.thesmos.sh/core/rand/seeded"
)

// Fixtures and parameters of the tests.
const (
	// exampleText is the text form of exampleID.
	exampleText = "01856d53-f1fb-774e-8048-d159e26af37b"

	// exampleMillis is the Unix milliseconds of exampleTime, and the
	// unix_ts_ms field of exampleID.
	exampleMillis = 0x01856d53f1fb

	// randomBits is the value that the constant source of the Generate
	// cases returns.
	randomBits = 0x0123456789abcdef

	// fractions is the number of fractions of one millisecond, and so the
	// number of IDs that a Generator issues in one millisecond of clock
	// time before it moves ahead of the clock.
	fractions = 4096

	// callers is the number of callers that call Generate at once in the
	// concurrent case, and idsPerCaller the number of IDs of each.
	callers      = 8
	idsPerCaller = 1000

	// opGenerate is the operation that the history of the concurrent case
	// records for a call of Generate.
	opGenerate = "generate"
)

// exampleTime is the clock time of the example of RFC 9562 for its
// method 3. It is 0.4567 ms into its millisecond, which is the fraction
// 1870, or 0x74e.
var exampleTime = time.Date(2023, 1, 1, 12, 34, 56, 123456700, time.UTC)

// exampleBytes are the bytes of the UUIDv7 that a Generator returns at
// exampleTime from a source whose Uint64 returns randomBits: the
// milliseconds 0x01856d53f1fb, version 7 and the fraction 0x74e, then
// variant 10 and the high 62 bits of randomBits.
var exampleBytes = [id.Size128]byte{
	0x01, 0x85, 0x6d, 0x53, 0xf1, 0xfb, 0x77, 0x4e,
	0x80, 0x48, 0xd1, 0x59, 0xe2, 0x6a, 0xf3, 0x7b,
}

// exampleID is the UUIDv7 whose bytes are exampleBytes.
var exampleID = id.New128(exampleBytes)

// notUUIDs are IDs that are not 128 bits.
var notUUIDs = []struct {
	name string
	id   id.ID
}{
	{name: "the zero ID", id: id.Zero},
	{name: "a 160-bit ID", id: id.New160([id.Size160]byte{1})},
	{name: "a 256-bit ID", id: id.New256([id.Size256]byte{1})},
}

// TestUUIDv7GeneratorContract runs the contract suite of id.Generator over
// a fake clock and a seeded source.
func TestUUIDv7GeneratorContract(t *testing.T) {
	t.Parallel()
	idtest.AssertGeneratorContract(t,
		func() id.Generator { return uuidv7.New(fake.New(exampleTime), seeded.New(rand.Seed(1))) },
		append(idtest.GeneratorContractAssertions(),
			idtest.GeneratorSizeAssertion(id.Size128),
		)...,
	)
}

// TestUUIDv7GeneratorModel runs the model test of id.Generator.
func TestUUIDv7GeneratorModel(t *testing.T) {
	t.Parallel()
	idtest.GeneratorModelTest(t,
		func() id.Generator { return uuidv7.New(fake.New(exampleTime), seeded.New(rand.Seed(1))) })
}

// FuzzUUIDv7GeneratorModel runs the model test of id.Generator on the
// inputs that a fuzzer finds.
func FuzzUUIDv7GeneratorModel(f *testing.F) {
	idtest.GeneratorModelFuzz(f,
		func() id.Generator { return uuidv7.New(fake.New(exampleTime), seeded.New(rand.Seed(1))) })
}

func TestUUIDv7(t *testing.T) {
	t.Parallel()

	ascending := func(earlier, later id.ID) bool { return earlier.Compare(later) < 0 }

	t.Run("Generator", func(t *testing.T) {
		t.Parallel()

		t.Run("Generate", func(t *testing.T) {
			t.Parallel()

			t.Run("returns the UUIDv7 of the example of RFC 9562", func(t *testing.T) {
				t.Parallel()
				g := uuidv7.New(fake.New(exampleTime), constant.New(randomBits))
				assert.Equal(t, g.Generate(), exampleID,
					"Generate must write the milliseconds, the fraction and the high 62 random bits")
			})

			// A clock in the first millisecond of the epoch can have the
			// stamp 0, which a new Generator counts as its last.
			t.Run("returns the stamp of a clock after the first millisecond in its first call", func(t *testing.T) {
				t.Parallel()
				nanos := prop.Integer[int64](int64(time.Millisecond), math.MaxInt64)
				prop.ForAll(t, "Generate must encode the stamp of the clock", func(c *prop.Case) {
					ns := c.Draw(nanos, "nanoseconds")

					u := uuidv7.New(fake.New(time.Unix(0, ns)), constant.New(randomBits)).Generate()
					fraction := binary.BigEndian.Uint16(u.Bytes()[6:8]) & 0x0FFF
					assert.Equal(c, uuidv7.TimestampMillis(u), uint64(ns/1_000_000),
						"the milliseconds must be the clock's")
					assert.Equal(c, uint64(fraction), uint64(ns%1_000_000)*fractions/1_000_000,
						"the fraction must be the clock's in 4096 steps")
				})
			})

			t.Run("returns the stamp 1 for a clock at the Unix epoch", func(t *testing.T) {
				t.Parallel()
				u := uuidv7.New(fake.New(time.Unix(0, 0)), constant.New(randomBits)).Generate()
				assert.Equal(t, u.Bytes()[6:8], []byte{0x70, 0x01}, "the first ID must have the fraction 1")
			})

			t.Run("returns the high 62 bits of the Uint64 of the source after the variant", func(t *testing.T) {
				t.Parallel()
				prop.ForAll(t, "Generate must write the high 62 bits of the source", func(c *prop.Case) {
					v := c.Draw(prop.Integer[uint64](0, math.MaxUint64), "random")

					u := uuidv7.New(fake.New(exampleTime), constant.New(v)).Generate()
					assert.Equal(c, binary.BigEndian.Uint64(u.Bytes()[8:16]), 1<<63|v>>2,
						"bytes 8 to 15 must be variant 10 and the high 62 bits")
				})
			})

			t.Run("returns increasing IDs while the clock repeats its time", func(t *testing.T) {
				t.Parallel()
				g := uuidv7.New(fake.New(exampleTime), constant.New(randomBits))
				ids := make([]id.ID, fractions)
				for i := range ids {
					ids[i] = g.Generate()
				}
				assert.Pairwise(t, ids, ascending, "each ID must be greater than the one before")
			})

			t.Run("returns the next millisecond after 4096 IDs at one clock time", func(t *testing.T) {
				t.Parallel()
				g := uuidv7.New(fake.New(time.UnixMilli(exampleMillis)), constant.New(randomBits))
				var last id.ID
				for range fractions {
					last = g.Generate()
				}
				assert.Equal(t, uuidv7.TimestampMillis(last), uint64(exampleMillis),
					"the first 4096 IDs must have the milliseconds of the clock")
				next := g.Generate()
				expect.Equal(t, uuidv7.TimestampMillis(next), uint64(exampleMillis+1),
					"the ID after them must have the next millisecond")
				expect.Equal(t, next.Bytes()[6:8], []byte{0x70, 0x00}, "the ID after them must have the fraction 0")
			})

			t.Run("returns a greater ID after the clock moves backwards", func(t *testing.T) {
				t.Parallel()
				clk := fake.New(exampleTime)
				g := uuidv7.New(clk, constant.New(randomBits))
				before := g.Generate()
				clk.Set(exampleTime.Add(-time.Second))
				assert.Equal(t, before.Compare(g.Generate()), -1,
					"an ID after the clock moved backwards must be greater than the one before")
			})

			t.Run("returns the milliseconds of the clock once the clock passes the last ID", func(t *testing.T) {
				t.Parallel()
				clk := fake.New(exampleTime)
				g := uuidv7.New(clk, constant.New(randomBits))
				_ = g.Generate()
				clk.Set(exampleTime.Add(-time.Second))
				_ = g.Generate()
				clk.Set(exampleTime.Add(time.Millisecond))
				assert.Equal(t, uuidv7.TimestampMillis(g.Generate()), uint64(exampleMillis+1),
					"Generate must use the time of the clock once it exceeds the last stamp")
			})

			t.Run("returns the milliseconds 0 for a wall time before the Unix epoch", func(t *testing.T) {
				t.Parallel()
				g := uuidv7.New(fake.New(time.Unix(-1, 0)), constant.New(randomBits))
				assert.Equal(t, uuidv7.TimestampMillis(g.Generate()), uint64(0),
					"a wall time before the epoch must give the milliseconds 0")
			})

			// The history of the calls must linearize against a spec whose
			// state is the last ID, so an ID exceeds the ID of every call that
			// returned before its call started, on any caller.
			t.Run("returns increasing IDs to concurrent callers", func(t *testing.T) {
				t.Parallel()
				g := uuidv7.New(fake.New(exampleTime), constant.New(randomBits))
				h := history.New()
				outcomes := history.Concurrently(callers, 10*time.Second, func(client int) (any, error) {
					for range idsPerCaller {
						call := h.Invoke(client, opGenerate, nil)
						call.OK(g.Generate())
					}

					return client, nil
				})
				for _, o := range outcomes {
					assert.True(t, o.Finished, "every caller must finish")
				}

				history.Linearizable(t, h, history.Spec[id.ID]{
					Initial: func() id.ID { return id.ID{} },
					Next: func(last id.ID, op history.Operation) []id.ID {
						got, _ := op.Output.(id.ID)
						if !op.Known || got.Compare(last) <= 0 {
							return nil
						}

						return []id.ID{got}
					},
				}, "each ID must exceed the ID of every call that returned before its call started")
			})
		})
	})
}

// TestUUIDv7Allocs checks the allocation contract of Generate over a fake
// clock and a constant source. MaxAllocs counts the allocations of the
// whole process, so the test does not run in parallel.
func TestUUIDv7Allocs(t *testing.T) {
	g := uuidv7.New(fake.New(exampleTime), constant.New(randomBits))

	t.Run("Generator", func(t *testing.T) {
		t.Run("Generate", func(t *testing.T) {
			var got id.ID
			expect.MaxAllocs(t, func() { got = g.Generate() }, 0, "Generate must not allocate")
			assert.True(t, uuidv7.Valid(got), "the test must measure a UUIDv7")
		})
	})
}

// BenchmarkUUIDv7Generator runs the benchmarks of the contract suite of
// id.Generator over the clock and the source that production callers
// pass.
func BenchmarkUUIDv7Generator(b *testing.B) {
	idtest.BenchmarkGeneratorContract(b, func() id.Generator { return uuidv7.New(hlc.New(0), crypto.New()) },
		idtest.GeneratorBenchOnGenerate(testkitbench.PureAllocsWithin[id.Generator, id.ID](0)),
	)
}

// BenchmarkUUIDv7 reports the cost of Generate over the clock and the
// source that production callers pass, and fails when it allocates.
func BenchmarkUUIDv7(b *testing.B) {
	b.Run("Generator", func(b *testing.B) {
		b.Run("Generate", func(b *testing.B) {
			g := uuidv7.New(hlc.New(0), crypto.New())
			var got id.ID

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got = g.Generate()
			}

			assert.True(b, uuidv7.Valid(got), "the benchmark must measure a UUIDv7")
		})
	})
}
