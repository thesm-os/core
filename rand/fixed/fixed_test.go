// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fixed_test

import (
	"encoding/binary"
	"testing"

	"go.thesmos.sh/testkit"
	"go.thesmos.sh/testkit/bench"

	"go.thesmos.sh/core/coretest/randtest"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/fixed"
)

// newFixed is the SUT factory for the testkit-driven contract
// suite. fixed.Rand returns the same value forever, so the
// distinctness assertion does not apply (deliberately omitted
// from the wiring).
func newFixed() rand.Rand { return fixed.New(0xDEADBEEFCAFEBABE) }

// --- testkit-driven contract layer ---

func TestFixedRandContract(t *testing.T) {
	t.Parallel()
	randtest.AssertRandContract(t, newFixed, randtest.RandContractAssertions()...)
}

func BenchmarkFixedRand(b *testing.B) {
	randtest.BenchmarkRandContract(b, newFixed,
		randtest.RandBenchOnUint64(bench.PureAllocsWithin[rand.Rand, uint64](0)),
	)
}

// --- fixed-specific tests ---

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("Uint64 returns the configured value on every call", func(t *testing.T) {
		t.Parallel()
		const v uint64 = 0xDEADBEEFCAFEBABE
		r := fixed.New(v)
		for range 5 {
			testkit.Equal(t, r.Uint64(), v, "Uint64 must return the configured value on every call")
		}
	})

	t.Run("zero value returns zero", func(t *testing.T) {
		t.Parallel()
		var r fixed.Rand
		testkit.Equal(t, r.Uint64(), uint64(0), "zero-value Rand must return 0")
	})
}

func TestRead(t *testing.T) {
	t.Parallel()

	t.Run("fills with little-endian repetition of the configured value", func(t *testing.T) {
		t.Parallel()
		const v uint64 = 0x0102030405060708
		buf := make([]byte, 16)
		n, err := fixed.New(v).Read(buf)
		testkit.NoError(t, err, "Read")
		testkit.Equal(t, n, 16, "Read must fill 16 bytes")
		testkit.Equal(t, binary.LittleEndian.Uint64(buf[0:8]), v,
			"first 8 bytes must encode the configured value")
		testkit.Equal(t, binary.LittleEndian.Uint64(buf[8:16]), v,
			"second 8 bytes must encode the configured value")
	})

	t.Run("non-aligned length fills the prefix correctly", func(t *testing.T) {
		t.Parallel()
		buf := make([]byte, 11)
		n, _ := fixed.New(0x42).Read(buf)
		testkit.Equal(t, n, 11, "Read must fill 11 bytes")
		testkit.Equal(t, buf[0], byte(0x42), "buf[0] must be 0x42")
	})
}

func TestFromFloat64(t *testing.T) {
	t.Parallel()

	t.Run("Float64 round-trips for in-range values", func(t *testing.T) {
		t.Parallel()
		// FromFloat64 inverts the (uint64 >> 11) / 2^53
		// construction; values exactly representable on the
		// 53-bit mantissa round-trip exactly.
		for _, v := range []float64{0.0, 0.25, 0.5, 0.75} {
			testkit.Equal(t, rand.Float64(fixed.FromFloat64(v)), v,
				"Float64(FromFloat64(v)) must round-trip exactly")
		}
	})

	t.Run("clamps negative inputs to 0.0", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, rand.Float64(fixed.FromFloat64(-1.0)), 0.0,
			"FromFloat64(-1.0) must clamp to 0.0")
	})

	t.Run("clamps inputs >= 1.0 to 1 - 2^-53", func(t *testing.T) {
		t.Parallel()
		const want = 1.0 - 1.0/(1<<53)
		for _, v := range []float64{1.0, 1.5, 2.0, 1e10} {
			got := rand.Float64(fixed.FromFloat64(v))
			testkit.Equal(t, got, want,
				"FromFloat64(>=1.0) must clamp to just-below-1.0")
			testkit.True(t, got < 1.0,
				"clamped value must remain strictly < 1.0")
		}
	})
}
