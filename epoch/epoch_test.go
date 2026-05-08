// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package epoch_test

import (
	"math"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/epoch"
)

func TestEpochZero(t *testing.T) {
	t.Parallel()

	t.Run("Zero is the reserved sentinel", func(t *testing.T) {
		t.Parallel()
		testkit.True(t, epoch.Zero.IsZero(), "Zero.IsZero must return true")
		testkit.Equal(t, epoch.Zero, epoch.Epoch(0), "Zero must equal 0")
	})

	t.Run("non-zero IsZero returns false", func(t *testing.T) {
		t.Parallel()
		var e epoch.Epoch = 1
		testkit.False(t, e.IsZero(), "Epoch(1).IsZero must return false")
	})
}

func TestEpochCompare(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		a, b epoch.Epoch
		want int
	}{
		"a < b":           {1, 2, -1},
		"a > b":           {5, 3, 1},
		"a == b":          {7, 7, 0},
		"zero < non-zero": {epoch.Zero, 1, -1},
		"non-zero > zero": {1, epoch.Zero, 1},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			testkit.Equal(t, tc.a.Compare(tc.b), tc.want,
				"Compare must reflect ordering")
		})
	}
}

func TestEpochSuccessor(t *testing.T) {
	t.Parallel()

	t.Run("Successor advances by one", func(t *testing.T) {
		t.Parallel()
		var e epoch.Epoch = 7
		testkit.Equal(t, e.Successor(), epoch.Epoch(8),
			"Successor(7) must equal 8")
	})

	t.Run("Successor of Zero is 1", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, epoch.Zero.Successor(), epoch.Epoch(1),
			"Successor(Zero) must equal 1")
	})

	t.Run("Successor wraps at MaxUint64", func(t *testing.T) {
		t.Parallel()
		var maxEpoch epoch.Epoch = math.MaxUint64
		// Documented: monotonicity wraps at MaxUint64; the wrap is
		// unreachable in practice (~584 years at 1 ns/epoch) and
		// therefore not guarded.
		testkit.Equal(t, maxEpoch.Successor(), epoch.Zero,
			"Successor(MaxUint64) must wrap to Zero")
	})
}

func TestEpochString(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		want string
		in   epoch.Epoch
	}{
		"zero":  {"0", epoch.Zero},
		"one":   {"1", 1},
		"large": {"1234567890", 1234567890},
		"max":   {"18446744073709551615", math.MaxUint64},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			testkit.Equal(t, tc.in.String(), tc.want, "String must match expected")
		})
	}
}

// TestEpochZeroAlloc cannot run in parallel —
// testing.AllocsPerRun panics if any other test is running.
//
//nolint:paralleltest // see comment above
func TestEpochZeroAlloc(t *testing.T) {
	var e epoch.Epoch = 42
	other := epoch.Epoch(43)

	cases := []struct {
		fn   func()
		name string
	}{
		{func() { _ = e.IsZero() }, "IsZero"},
		{func() { _ = e.Compare(other) }, "Compare"},
		{func() { _ = e.Successor() }, "Successor"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testkit.Equal(t, testing.AllocsPerRun(100, tc.fn),
				float64(0), tc.name+" must be zero-alloc")
		})
	}
}

func BenchmarkCompare(b *testing.B) {
	e := epoch.Epoch(42)
	other := epoch.Epoch(43)
	b.ReportAllocs()
	for b.Loop() {
		_ = e.Compare(other)
	}
}

func BenchmarkSuccessor(b *testing.B) {
	e := epoch.Epoch(42)
	b.ReportAllocs()
	for b.Loop() {
		_ = e.Successor()
	}
}

func BenchmarkString(b *testing.B) {
	e := epoch.Epoch(123456789)
	b.ReportAllocs()
	for b.Loop() {
		_ = e.String()
	}
}
