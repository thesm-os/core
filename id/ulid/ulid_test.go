// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package ulid_test

import (
	"testing"
	"time"

	"go.thesmos.sh/testkit"
	"go.thesmos.sh/testkit/bench"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/coretest/idtest"
	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/ulid"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/seeded"
)

// origin is the wall-time anchor for ULIDs in these tests.
var origin = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// idFromBytes wraps a 16-byte literal as a 128-bit [id.ID].
// Lives in this file so both ulid_test.go and parse_test.go can
// use it (same _test package).
func idFromBytes(b ...byte) id.ID {
	var raw [id.Size128]byte
	copy(raw[:], b)
	return id.New128(raw)
}

// newULID is the SUT factory for the testkit-driven contract
// suite. seeded.Rand advances per call so the distinctness
// assertion produces fresh entropy.
func newULID() id.Generator {
	return ulid.New(fake.New(origin), seeded.New(rand.Seed(1)))
}

// --- testkit-driven contract layer ---

func TestULIDGeneratorContract(t *testing.T) {
	t.Parallel()
	idtest.AssertGeneratorContract(t, newULID,
		append(idtest.GeneratorContractAssertions(),
			idtest.GeneratorSizeAssertion(id.Size128),
		)...,
	)
}

func TestULIDGeneratorModel(t *testing.T) {
	t.Parallel()
	idtest.GeneratorModelTest(t, newULID)
}

func FuzzULIDGeneratorModel(f *testing.F) {
	idtest.GeneratorModelFuzz(f, newULID)
}

func BenchmarkULIDGenerator(b *testing.B) {
	idtest.BenchmarkGeneratorContract(b, newULID,
		idtest.GeneratorBenchOnGenerate(bench.PureAllocsWithin[id.Generator, id.ID](0)),
	)
}

// --- ulid-specific tests ---

func TestGeneratorGenerate(t *testing.T) {
	t.Parallel()

	t.Run("encodes timestamp into bytes 0..5 big-endian", func(t *testing.T) {
		t.Parallel()
		clk := fake.New(origin)
		g := ulid.New(clk, seeded.New(rand.Seed(1)))

		u := g.Generate()
		testkit.Equal(t, ulid.TimestampMillis(u), uint64(origin.UnixMilli()),
			"TimestampMillis must decode the configured wall instant")
		testkit.Equal(t, u.Size(), id.Size128,
			"ULID Generate must produce a 128-bit ID")
	})

	t.Run("two ULIDs at distinct wall times differ and remain monotonic", func(t *testing.T) {
		t.Parallel()
		clk := fake.New(origin)
		g := ulid.New(clk, seeded.New(rand.Seed(1)))

		first := g.Generate()
		clk.Advance(1 * time.Millisecond)
		second := g.Generate()
		testkit.NotEqual(t, first, second,
			"two ULIDs minted at distinct times must differ")
		testkit.True(t, ulid.TimestampMillis(first) < ulid.TimestampMillis(second),
			"TimestampMillis must be strictly monotonic across an Advance")
	})

	t.Run("generator is deterministic given fixed clock and seed", func(t *testing.T) {
		t.Parallel()
		gA := ulid.New(fake.New(origin), seeded.New(rand.Seed(42)))
		gB := ulid.New(fake.New(origin), seeded.New(rand.Seed(42)))

		for range 4 {
			testkit.Equal(t, gA.Generate(), gB.Generate(),
				"identical clock+seed must produce identical ULIDs")
		}
	})
}

func TestTimestampMillis(t *testing.T) {
	t.Parallel()

	t.Run("Zero ID has zero timestamp", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, ulid.TimestampMillis(id.Zero), uint64(0),
			"TimestampMillis(Zero) must return 0 — no embedded timestamp")
	})

	t.Run("decodes big-endian 48-bit prefix", func(t *testing.T) {
		t.Parallel()
		u := idFromBytes(0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC)
		testkit.Equal(t, ulid.TimestampMillis(u), uint64(0x123456789ABC),
			"TimestampMillis must decode the leading 48 bits big-endian")
	})
}
