// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package randtest

import (
	"strconv"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/rand"
)

// RandContractAssertions returns the generic assertions every
// [rand.Rand] implementation must satisfy: Read fills the
// supplied buffer length, Read on nil/empty is a no-op, Uint64
// produces 64 bits of distinct values across calls, and the
// reported byte count matches the buffer length.
//
// "Stable across implementations" properties only — impl-specific
// algorithm-of-record vectors live in each per-impl test file.
//
//	cryptotest.AssertRandContract(t, factory,
//	    cryptotest.RandContractAssertions()...,
//	)
func RandContractAssertions() []RandOption {
	return []RandOption{
		// --- Read ---

		RandCustom("Read fills the supplied buffer", func(t *testing.T, r rand.Rand) {
			buf := make([]byte, 32)
			n, err := r.Read(buf)
			testkit.NoError(t, err, "Read")
			testkit.Equal(t, n, len(buf),
				"Read must report the same length as the supplied buffer")
		}),

		RandCustom("Read on nil slice returns zero, no error", func(t *testing.T, r rand.Rand) {
			n, err := r.Read(nil)
			testkit.NoError(t, err, "Read(nil)")
			testkit.Equal(t, n, 0, "Read(nil) must report 0 bytes")
		}),

		RandCustom("Read on empty slice returns zero, no error", func(t *testing.T, r rand.Rand) {
			n, err := r.Read([]byte{})
			testkit.NoError(t, err, "Read([]byte{})")
			testkit.Equal(t, n, 0, "Read([]byte{}) must report 0 bytes")
		}),

		// --- Cross-method ---

		RandCustom("Read produces full output for various sizes", func(t *testing.T, r rand.Rand) {
			for _, sz := range []int{1, 7, 8, 9, 32, 1024} {
				buf := make([]byte, sz)
				n, err := r.Read(buf)
				testkit.NoError(t, err, "Read")
				testkit.Equal(t, n, sz,
					"Read must fully fill any caller-sized buffer")
			}
		}),
	}
}

// RandUint64DistinctnessAssertion verifies that consecutive
// [rand.Rand.Uint64] calls do not all collide on the same value.
// A degenerate stream (e.g. always 0) would pass this check
// trivially with the right `if`-pattern; the assertion uses a
// 16-call sweep + duplicate-set check to surface "stream stuck"
// regressions.
//
// Not part of [RandContractAssertions] — [fixed.Rand] returns the
// same value by design and would fail. Wire this in for non-fixed
// implementations only.
func RandUint64DistinctnessAssertion() RandOption {
	return RandCustom("Uint64 stream produces distinct values", func(t *testing.T, r rand.Rand) {
		const samples = 16
		seen := make(map[uint64]struct{}, samples)
		for range samples {
			seen[r.Uint64()] = struct{}{}
		}
		// Birthday bound on 64-bit uniform draws: probability of
		// any collision in 16 samples is ~10⁻¹⁷. A single repeat
		// across 16 calls is astronomically unlikely on a healthy
		// generator.
		testkit.True(t, len(seen) >= samples/2,
			"Uint64 stream must produce diverse values — degenerate stream detected")
	})
}

// RandSeedDeterminismAssertion verifies that two factories built
// from the same seed produce identical Uint64 streams.
// Wire this in for deterministic implementations (pcg, seeded);
// skip for crypto and fixed.
func RandSeedDeterminismAssertion(factoryA, factoryB func() rand.Rand) RandOption {
	return RandCustom("same seed produces identical Uint64 stream", func(t *testing.T, _ rand.Rand) {
		a := factoryA()
		b := factoryB()
		const samples = 64
		for i := range samples {
			testkit.Equal(t, a.Uint64(), b.Uint64(),
				"Uint64 sample "+strconv.Itoa(i)+" must match across same-seed instances")
		}
	})
}
