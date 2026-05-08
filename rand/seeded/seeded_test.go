// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package seeded_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	"go.thesmos.sh/testkit"
	"go.thesmos.sh/testkit/bench"

	"go.thesmos.sh/core/coretest/randtest"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/seeded"
)

// newSeeded is the SUT factory for the testkit-driven contract
// suite.
func newSeeded() rand.Rand { return seeded.New(rand.Seed(1)) }

// --- testkit-driven contract layer ---

func TestSeededRandContract(t *testing.T) {
	t.Parallel()
	randtest.AssertRandContract(t, newSeeded,
		append(randtest.RandContractAssertions(),
			randtest.RandUint64DistinctnessAssertion(),
			randtest.RandSeedDeterminismAssertion(
				func() rand.Rand { return seeded.New(rand.Seed(42)) },
				func() rand.Rand { return seeded.New(rand.Seed(42)) },
			),
		)...,
	)
}

func TestSeededRandModel(t *testing.T) {
	t.Parallel()
	randtest.RandModelTest(t, newSeeded)
}

func FuzzSeededRandModel(f *testing.F) {
	randtest.RandModelFuzz(f, newSeeded)
}

func BenchmarkSeededRand(b *testing.B) {
	randtest.BenchmarkRandContract(b, newSeeded,
		randtest.RandBenchOnUint64(bench.PureAllocsWithin[rand.Rand, uint64](0)),
	)
}

// --- seeded-specific tests ---

func TestSeed(t *testing.T) {
	t.Parallel()

	t.Run("Seed reports construction-time seed for int-seeded", func(t *testing.T) {
		t.Parallel()
		const s = rand.Seed(99)
		testkit.Equal(t, seeded.New(s).Seed(), s,
			"Seed must return the construction-time value")
	})

	t.Run("Seed reports SeedUnspecified for byte-seeded", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, seeded.NewFromBytes([]byte("anything")).Seed(),
			rand.SeedUnspecified,
			"NewFromBytes must report SeedUnspecified — no int recovers a byte seed")
	})

	t.Run("byte-seeded instances with equal input produce identical streams", func(t *testing.T) {
		t.Parallel()
		seed := []byte("test-seed-bytes-2026")
		a, b := seeded.NewFromBytes(seed), seeded.NewFromBytes(seed)
		ba, bb := make([]byte, 128), make([]byte, 128)
		_, _ = a.Read(ba)
		_, _ = b.Read(bb)
		testkit.Equal(t, ba, bb,
			"NewFromBytes with equal seed bytes must produce identical streams")
	})
}

func TestRead(t *testing.T) {
	t.Parallel()

	t.Run("split reads equal a single read of the same length", func(t *testing.T) {
		t.Parallel()
		// Behavioural contract: a 100-byte Read produces the
		// same bytes as ten 10-byte Reads. Exercises the
		// buffered-block carry-over between calls.
		single := make([]byte, 100)
		_, _ = seeded.New(rand.Seed(2)).Read(single)

		split := make([]byte, 100)
		r := seeded.New(rand.Seed(2))
		for i := 0; i < 100; i += 10 {
			_, _ = r.Read(split[i : i+10])
		}
		testkit.Equal(t, split, single,
			"split Reads must reproduce the same byte stream as a single Read")
	})

	t.Run("each HMAC-SHA-256 block is distinct", func(t *testing.T) {
		t.Parallel()
		// The block size is 32 bytes. Three consecutive blocks
		// must differ — a stalled counter would yield
		// b1 == b2 == b3.
		buf := make([]byte, 96)
		_, _ = seeded.New(rand.Seed(123)).Read(buf)
		b1, b2, b3 := buf[0:32], buf[32:64], buf[64:96]
		testkit.False(t, bytes.Equal(b1, b2),
			"block 1 must differ from block 2 — counter must advance")
		testkit.False(t, bytes.Equal(b2, b3),
			"block 2 must differ from block 3 — counter must advance")
	})

	t.Run("seed 0xabcd produces a known 64-byte fixture", func(t *testing.T) {
		t.Parallel()
		// Golden bytes recorded from seeded.New(0xabcd).Read(64).
		// Provides a cross-version stability check; pinned because
		// HMAC-SHA-256 is deterministic and the construction
		// (key = big-endian seed, counter encoded big-endian) is
		// part of the public contract — see package godoc.
		want := mustDecodeHex(t,
			"c1dfde25ff46bc350cbd30fd529c74c4e4e820f2e2ea56dff62b4e1b60e75c56"+
				"df4a9b70aa92cd69a9a3d37de3ff9a5baa97ee15640101987fd736296aa2cfcc")
		got := make([]byte, len(want))
		_, _ = seeded.New(rand.Seed(0xabcd)).Read(got)
		testkit.Equal(t, got, want, "seeded.New(0xabcd) Read(64) must byte-match the golden vector")
	})
}

// --- helpers ---

func mustDecodeHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	testkit.NoError(t, err, "decode hex fixture")
	return b
}
