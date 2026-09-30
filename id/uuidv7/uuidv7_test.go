// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package uuidv7_test

import (
	"sync"
	"testing"
	"time"

	"go.thesmos.sh/testkit"
	"go.thesmos.sh/testkit/bench"

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

	// goroutines is the number of goroutines that call Generate at once in
	// the concurrent case.
	goroutines = 8

	// idsPerGoroutine is the number of IDs that each goroutine of the
	// concurrent case generates.
	idsPerGoroutine = 1000

	// benchRuns is the number of calls over which a benchmark averages the
	// allocations that it checks.
	benchRuns = 100
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

// newUUIDv7 returns a Generator over a fake clock at exampleTime and a
// seeded source, so that two Generators return the same stream of IDs.
func newUUIDv7() id.Generator {
	return uuidv7.New(fake.New(exampleTime), seeded.New(rand.Seed(1)))
}

// newProductionUUIDv7 returns a Generator over the clock and the source
// that production callers pass.
func newProductionUUIDv7() id.Generator {
	return uuidv7.New(hlc.New(0), crypto.New())
}

// notUUIDs returns IDs that are not 128 bits, each named by its size.
func notUUIDs() map[string]id.ID {
	return map[string]id.ID{
		"the zero ID":  id.Zero,
		"a 160-bit ID": id.New160([id.Size160]byte{1}),
		"a 256-bit ID": id.New256([id.Size256]byte{1}),
	}
}

// increasing reports whether later is greater than earlier in byte order.
func increasing(earlier, later id.ID) bool {
	return earlier.Compare(later) < 0
}

// benchZeroAlloc reports the cost of call, and fails when call allocates.
func benchZeroAlloc(b *testing.B, call func()) {
	b.Helper()

	if allocs := testing.AllocsPerRun(benchRuns, call); allocs != 0 {
		b.Fatalf("allocates %v times per call, want 0", allocs)
	}

	b.ReportAllocs()
	for b.Loop() {
		call()
	}
}

func TestUUIDv7GeneratorContract(t *testing.T) {
	t.Parallel()
	idtest.AssertGeneratorContract(t, newUUIDv7,
		append(idtest.GeneratorContractAssertions(),
			idtest.GeneratorSizeAssertion(id.Size128),
		)...,
	)
}

func TestUUIDv7GeneratorModel(t *testing.T) {
	t.Parallel()
	idtest.GeneratorModelTest(t, newUUIDv7)
}

func FuzzUUIDv7GeneratorModel(f *testing.F) {
	idtest.GeneratorModelFuzz(f, newUUIDv7)
}

// BenchmarkUUIDv7Generator reports the cost of Generate over the clock and
// the source that production callers pass, and fails when Generate
// allocates.
func BenchmarkUUIDv7Generator(b *testing.B) {
	idtest.BenchmarkGeneratorContract(b, newProductionUUIDv7,
		idtest.GeneratorBenchOnGenerate(bench.PureAllocsWithin[id.Generator, id.ID](0)),
	)
}

func TestGenerator(t *testing.T) {
	t.Parallel()

	t.Run("Generate returns the UUIDv7 of the clock time and the random bits", func(t *testing.T) {
		t.Parallel()
		g := uuidv7.New(fake.New(exampleTime), constant.New(randomBits))
		testkit.Equal(t, g.Generate(), exampleID,
			"Generate must write the milliseconds, the fraction and the high 62 random bits")
	})

	t.Run("Generate returns increasing IDs while the clock repeats its time", func(t *testing.T) {
		t.Parallel()
		g := uuidv7.New(fake.New(exampleTime), constant.New(randomBits))
		ids := make([]id.ID, fractions)
		for i := range ids {
			ids[i] = g.Generate()
		}
		testkit.Sequence(t, ids, increasing, "each ID must be greater than the one before")
	})

	t.Run("Generate moves to the next millisecond after 4096 IDs at one clock time", func(t *testing.T) {
		t.Parallel()
		g := uuidv7.New(fake.New(time.UnixMilli(exampleMillis)), constant.New(randomBits))
		var last id.ID
		for range fractions {
			last = g.Generate()
		}
		testkit.Equal(t, uuidv7.TimestampMillis(last), exampleMillis,
			"the first 4096 IDs must have the clock's milliseconds")
		next := g.Generate()
		testkit.Equal(t, uuidv7.TimestampMillis(next), exampleMillis+1,
			"the ID after them must have the next millisecond")
		testkit.Equal(t, next.Bytes()[6:8], []byte{0x70, 0x00},
			"the ID after them must have the fraction 0")
	})

	t.Run("Generate returns a greater ID after the clock moves backwards", func(t *testing.T) {
		t.Parallel()
		clk := fake.New(exampleTime)
		g := uuidv7.New(clk, constant.New(randomBits))
		before := g.Generate()
		clk.Set(exampleTime.Add(-time.Second))
		testkit.True(t, increasing(before, g.Generate()),
			"an ID after the clock moved backwards must be greater than the one before")
	})

	t.Run("Generate returns the clock's milliseconds once the clock passes the last ID", func(t *testing.T) {
		t.Parallel()
		clk := fake.New(exampleTime)
		g := uuidv7.New(clk, constant.New(randomBits))
		_ = g.Generate()
		clk.Set(exampleTime.Add(-time.Second))
		_ = g.Generate()
		clk.Set(exampleTime.Add(time.Millisecond))
		testkit.Equal(t, uuidv7.TimestampMillis(g.Generate()), exampleMillis+1,
			"Generate must use the clock's time once it exceeds the last stamp")
	})

	t.Run("Generate counts a wall time before the Unix epoch as the epoch", func(t *testing.T) {
		t.Parallel()
		g := uuidv7.New(fake.New(time.Unix(-1, 0)), constant.New(randomBits))
		testkit.Equal(t, uuidv7.TimestampMillis(g.Generate()), uint64(0),
			"a wall time before the epoch must give the milliseconds 0")
	})

	t.Run("Generate returns distinct IDs that increase for each concurrent caller", func(t *testing.T) {
		t.Parallel()
		g := uuidv7.New(fake.New(exampleTime), constant.New(randomBits))
		streams := make([][]id.ID, goroutines)
		var wg sync.WaitGroup
		for i := range streams {
			wg.Go(func() {
				stream := make([]id.ID, idsPerGoroutine)
				for j := range stream {
					stream[j] = g.Generate()
				}
				streams[i] = stream
			})
		}
		wg.Wait()

		seen := make(map[id.ID]struct{}, goroutines*idsPerGoroutine)
		for _, stream := range streams {
			testkit.Sequence(t, stream, increasing, "the IDs of one caller must increase")
			for _, u := range stream {
				seen[u] = struct{}{}
			}
		}
		testkit.Equal(t, len(seen), goroutines*idsPerGoroutine, "every ID must be distinct")
	})
}
