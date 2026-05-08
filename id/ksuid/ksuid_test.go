// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package ksuid_test

import (
	"testing"
	"time"

	"go.thesmos.sh/testkit"
	"go.thesmos.sh/testkit/bench"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/coretest/idtest"
	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/ksuid"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/seeded"
)

// origin is the wall-time anchor for KSUIDs in these tests.
// 2026-01-01 UTC sits cleanly past KSUID epoch (2014-05-13).
var origin = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// newKSUID is the SUT factory for the testkit-driven contract
// suite. Uses a deterministic seeded.Rand so the contract suite's
// distinctness assertion still produces fresh IDs (entropy
// advances per call).
func newKSUID() id.Generator {
	return ksuid.New(fake.New(origin), seeded.New(rand.Seed(1)))
}

// --- testkit-driven contract layer ---

func TestKSUIDGeneratorContract(t *testing.T) {
	t.Parallel()
	idtest.AssertGeneratorContract(t, newKSUID,
		append(idtest.GeneratorContractAssertions(),
			idtest.GeneratorSizeAssertion(id.Size160),
		)...,
	)
}

func TestKSUIDGeneratorModel(t *testing.T) {
	t.Parallel()
	idtest.GeneratorModelTest(t, newKSUID)
}

func FuzzKSUIDGeneratorModel(f *testing.F) {
	idtest.GeneratorModelFuzz(f, newKSUID)
}

func BenchmarkKSUIDGenerator(b *testing.B) {
	idtest.BenchmarkGeneratorContract(b, newKSUID,
		idtest.GeneratorBenchOnGenerate(bench.PureAllocsWithin[id.Generator, id.ID](0)),
	)
}

// --- ksuid-specific tests ---

func TestGenerator(t *testing.T) {
	t.Parallel()

	t.Run("encodes timestamp offset from KSUID epoch", func(t *testing.T) {
		t.Parallel()
		clk := fake.New(origin)
		g := ksuid.New(clk, seeded.New(rand.Seed(1)))

		u := g.Generate()
		testkit.Equal(t, ksuid.TimestampSeconds(u), origin.Unix(),
			"TimestampSeconds must decode the configured wall instant")
		testkit.Equal(t, u.Size(), id.Size160,
			"KSUID Generate must produce a 160-bit ID")
	})

	t.Run("two IDs at distinct seconds sort in chronological order", func(t *testing.T) {
		t.Parallel()
		clk := fake.New(origin)
		g := ksuid.New(clk, seeded.New(rand.Seed(1)))

		first := g.Generate()
		clk.Advance(2 * time.Second)
		second := g.Generate()

		testkit.True(t, first.Compare(second) < 0,
			"first KSUID must sort strictly before the later one (chronological order)")
		testkit.True(t, ksuid.TimestampSeconds(first) < ksuid.TimestampSeconds(second),
			"TimestampSeconds must be strictly monotonic")
	})

	t.Run("generator is deterministic given fixed clock and seed", func(t *testing.T) {
		t.Parallel()
		gA := ksuid.New(fake.New(origin), seeded.New(rand.Seed(42)))
		gB := ksuid.New(fake.New(origin), seeded.New(rand.Seed(42)))

		for range 4 {
			testkit.Equal(t, gA.Generate(), gB.Generate(),
				"identical clock+seed must produce identical KSUIDs")
		}
	})
}

func TestTimestampSeconds(t *testing.T) {
	t.Parallel()

	t.Run("Zero ID returns 0", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, ksuid.TimestampSeconds(id.Zero), int64(0),
			"TimestampSeconds(Zero) must return 0 — no embedded timestamp")
	})

	t.Run("128-bit ID is too short, returns 0", func(t *testing.T) {
		t.Parallel()
		// Locks the size-boundary check: any ID smaller than
		// Size160 must return 0 because bytes 0..3 alone are
		// not the KSUID timestamp prefix.
		smaller := id.New128([id.Size128]byte{0xFF, 0xFF, 0xFF, 0xFF})
		testkit.Equal(t, ksuid.TimestampSeconds(smaller), int64(0),
			"TimestampSeconds on 128-bit ID must return 0 — too short for KSUID layout")
	})

	t.Run("decodes big-endian offset from epoch", func(t *testing.T) {
		t.Parallel()
		// Offset 0 from KSUID epoch = ksuid.Epoch absolute seconds.
		var raw [id.Size160]byte
		u := id.New160(raw)
		testkit.Equal(t, ksuid.TimestampSeconds(u), ksuid.Epoch,
			"TimestampSeconds with offset 0 must equal KSUID epoch absolute")
	})
}
