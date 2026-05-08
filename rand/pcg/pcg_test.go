// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package pcg_test

import (
	"encoding/hex"
	"testing"

	"go.thesmos.sh/testkit"
	"go.thesmos.sh/testkit/bench"

	"go.thesmos.sh/core/coretest/randtest"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/pcg"
)

// newPCG is the SUT factory for the testkit-driven contract
// suite. The fixed seed is deliberate — testkit's auto-laws call
// the factory for each subtest and fresh state matters when
// asserting determinism.
func newPCG() rand.Rand { return pcg.New(rand.Seed(1)) }

// --- testkit-driven contract layer ---

func TestPCGRandContract(t *testing.T) {
	t.Parallel()
	randtest.AssertRandContract(t, newPCG,
		append(randtest.RandContractAssertions(),
			randtest.RandUint64DistinctnessAssertion(),
			randtest.RandSeedDeterminismAssertion(
				func() rand.Rand { return pcg.New(rand.Seed(42)) },
				func() rand.Rand { return pcg.New(rand.Seed(42)) },
			),
		)...,
	)
}

func TestPCGRandModel(t *testing.T) {
	t.Parallel()
	randtest.RandModelTest(t, newPCG)
}

func FuzzPCGRandModel(f *testing.F) {
	randtest.RandModelFuzz(f, newPCG)
}

func BenchmarkPCGRand(b *testing.B) {
	randtest.BenchmarkRandContract(b, newPCG,
		randtest.RandBenchOnUint64(bench.PureAllocsWithin[rand.Rand, uint64](0)),
	)
}

// --- pcg-specific tests ---

func TestSeed(t *testing.T) {
	t.Parallel()

	t.Run("returns the construction-time seed", func(t *testing.T) {
		t.Parallel()
		const seed = rand.Seed(123)
		testkit.Equal(t, pcg.New(seed).Seed(), seed,
			"Seed must return the construction-time value")
	})

	t.Run("is invariant as the generator advances", func(t *testing.T) {
		t.Parallel()
		const seed = rand.Seed(456)
		r := pcg.New(seed)
		for range 100 {
			r.Uint64()
		}
		testkit.Equal(t, r.Seed(), seed,
			"Seed must remain unchanged as the generator advances")
	})
}

// TestKnownAnswerVectors locks the impl against frozen byte
// streams produced by [pcg.Rand.Read]. The contract suite covers
// "fills the buffer" and "same seed identical streams"; these
// vectors lock the *specific* PCG-from-math/rand/v2 byte sequence
// against silent stdlib drift.
func TestKnownAnswerVectors(t *testing.T) {
	t.Parallel()

	t.Run("seed 42 produces a known 32-byte fixture (aligned)", func(t *testing.T) {
		t.Parallel()
		// Golden bytes recorded from pcg.New(42).Read(make([]byte, 32)).
		// Pinned to detect drift; covers the aligned (8-byte
		// multiple) path of Read.
		want := mustDecodeHex(t,
			"9fa987db721b88dbe49fb0762d6df1f5c73270890a25e4227161b5e80282a816")
		got := make([]byte, len(want))
		_, _ = pcg.New(rand.Seed(42)).Read(got)
		testkit.Equal(t, got, want, "PCG Seed(42) Read(32) must byte-match the golden vector")
	})

	t.Run("seed 1 produces a known 17-byte fixture (non-aligned tail)", func(t *testing.T) {
		t.Parallel()
		// Golden bytes recorded from pcg.New(1).Read(make([]byte, 17)).
		// 17 = 2*8 + 1; exercises the tail-byte branch where the
		// final Uint64 only contributes one byte to the output.
		want := mustDecodeHex(t, "0329edab29a127995653604a8c07d016f2")
		got := make([]byte, len(want))
		_, _ = pcg.New(rand.Seed(1)).Read(got)
		testkit.Equal(t, got, want, "PCG Seed(1) Read(17) must byte-match the golden vector")
	})

	t.Run("split reads equal a single read of the same length", func(t *testing.T) {
		t.Parallel()
		// Behavioural contract: callers can substitute one Read(n)
		// with multiple Read calls totalling n bytes — the byte
		// stream is the same. Kills mutations that draw extra
		// Uint64s on the aligned-tail branch.
		single := make([]byte, 32)
		_, _ = pcg.New(rand.Seed(2)).Read(single)

		split := make([]byte, 32)
		r := pcg.New(rand.Seed(2))
		_, _ = r.Read(split[:8])
		_, _ = r.Read(split[8:24])
		_, _ = r.Read(split[24:])
		testkit.Equal(t, split, single,
			"split Reads must reproduce the same byte stream as a single Read")
	})
}

// --- helpers ---

func mustDecodeHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	testkit.NoError(t, err, "decode hex fixture")
	return b
}
