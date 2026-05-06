// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package epoch_test

import (
	"math"
	"testing"

	"go.thesmos.sh/core/epoch"
)

func TestEpochZero(t *testing.T) {
	t.Parallel()

	t.Run("Zero is the reserved sentinel", func(t *testing.T) {
		t.Parallel()
		if !epoch.Zero.IsZero() {
			t.Fatal("Zero.IsZero(): got false, want true")
		}
		if epoch.Zero != 0 {
			t.Fatalf("Zero: got %d, want 0", epoch.Zero)
		}
	})

	t.Run("non-zero IsZero returns false", func(t *testing.T) {
		t.Parallel()
		var e epoch.Epoch = 1
		if e.IsZero() {
			t.Fatal("Epoch(1).IsZero(): got true, want false")
		}
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
			if got := tc.a.Compare(tc.b); got != tc.want {
				t.Fatalf("Compare(%d, %d): got %d, want %d",
					tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestEpochSuccessor(t *testing.T) {
	t.Parallel()

	t.Run("Successor advances by one", func(t *testing.T) {
		t.Parallel()
		var e epoch.Epoch = 7
		if got := e.Successor(); got != 8 {
			t.Fatalf("Successor(7): got %d, want 8", got)
		}
	})

	t.Run("Successor of Zero is 1", func(t *testing.T) {
		t.Parallel()
		if got := epoch.Zero.Successor(); got != 1 {
			t.Fatalf("Zero.Successor(): got %d, want 1", got)
		}
	})

	t.Run("Successor wraps at MaxUint64", func(t *testing.T) {
		t.Parallel()
		var maxEpoch epoch.Epoch = math.MaxUint64
		// Documented: monotonicity wraps at MaxUint64; the wrap is
		// unreachable in practice (~584 years at 1 ns/epoch) and
		// therefore not guarded.
		if got := maxEpoch.Successor(); got != epoch.Zero {
			t.Fatalf("Successor(MaxUint64): got %d, want Zero (wrap)", got)
		}
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
			if got := tc.in.String(); got != tc.want {
				t.Fatalf("String(%d): got %q, want %q", tc.in, got, tc.want)
			}
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
			if got := testing.AllocsPerRun(100, tc.fn); got != 0 {
				t.Fatalf("%s: %v allocs/op, want 0", tc.name, got)
			}
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
