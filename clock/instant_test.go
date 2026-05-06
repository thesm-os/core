// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package clock_test

import (
	"testing"
	"time"

	"go.thesmos.sh/core/clock"
)

func TestInstantIsZero(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		in   clock.Instant
		want bool
	}{
		"zero value":          {clock.Instant{}, true},
		"non-zero Wall":       {clock.Instant{Wall: 1}, false},
		"non-zero Logical":    {clock.Instant{Logical: 1}, false},
		"non-zero Node":       {clock.Instant{Node: 1}, false},
		"all fields non-zero": {clock.Instant{Wall: 1, Logical: 1, Node: 1}, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := tc.in.IsZero(); got != tc.want {
				t.Fatalf("IsZero: got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestInstantOrdering(t *testing.T) {
	t.Parallel()

	// Lexicographic (Wall, Logical, Node) ordering. Each row is a
	// (a, b) pair where a is causally before b.
	pairs := map[string]struct {
		a, b clock.Instant
	}{
		"earlier Wall":              {clock.Instant{Wall: 1}, clock.Instant{Wall: 2}},
		"same Wall earlier Logical": {clock.Instant{Wall: 1, Logical: 1}, clock.Instant{Wall: 1, Logical: 2}},
		"same Wall+Logical, lower Node": {
			clock.Instant{Wall: 1, Logical: 1, Node: 1},
			clock.Instant{Wall: 1, Logical: 1, Node: 2},
		},
	}
	for name, p := range pairs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := p.a.Compare(p.b); got != -1 {
				t.Fatalf("a.Compare(b): got %d, want -1", got)
			}
			if got := p.b.Compare(p.a); got != 1 {
				t.Fatalf("b.Compare(a): got %d, want 1", got)
			}
			if !p.a.HappensBefore(p.b) {
				t.Fatal("a must happen before b")
			}
			if p.b.HappensBefore(p.a) {
				t.Fatal("b must not happen before a")
			}
		})
	}

	t.Run("identical instants compare equal", func(t *testing.T) {
		t.Parallel()
		// Construct two equal instants from independent literals
		// so the assertion does not look like a self-comparison
		// to static analysis.
		x := clock.Instant{Wall: 1, Logical: 1, Node: 1}
		y := clock.Instant{Wall: 1, Logical: 1, Node: 1}
		if got := x.Compare(y); got != 0 {
			t.Fatalf("Compare on equal instants: got %d, want 0", got)
		}
		if x.HappensBefore(y) {
			t.Fatal("equal instants must not be ordered")
		}
	})
}

func TestInstantArithmetic(t *testing.T) {
	t.Parallel()

	t.Run("Sub returns wall-clock duration", func(t *testing.T) {
		t.Parallel()
		a := clock.Instant{Wall: 2_000_000_000}
		b := clock.Instant{Wall: 1_000_000_000}
		if got := a.Sub(b); got != time.Second {
			t.Fatalf("a.Sub(b): got %v, want %v", got, time.Second)
		}
		if got := b.Sub(a); got != -time.Second {
			t.Fatalf("b.Sub(a): got %v, want %v", got, -time.Second)
		}
	})

	t.Run("Add advances Wall, preserves Logical and Node", func(t *testing.T) {
		t.Parallel()
		in := clock.Instant{Wall: 1_000_000_000, Logical: 5, Node: 3}
		got := in.Add(time.Second)
		if got.Wall != 2_000_000_000 {
			t.Fatalf("Wall: got %d, want 2_000_000_000", got.Wall)
		}
		if got.Logical != 5 {
			t.Fatalf("Logical: got %d, want 5", got.Logical)
		}
		if got.Node != 3 {
			t.Fatalf("Node: got %d, want 3", got.Node)
		}
	})

	t.Run("Time returns Wall as UTC time.Time", func(t *testing.T) {
		t.Parallel()
		want := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
		got := clock.Instant{Wall: want.UnixNano()}.Time()
		if !got.Equal(want) {
			t.Fatalf("Time: got %v, want %v", got, want)
		}
		if got.Location() != time.UTC {
			t.Fatalf("Time location: got %v, want UTC", got.Location())
		}
	})
}

func TestInstantRange(t *testing.T) {
	t.Parallel()

	a := clock.Instant{Wall: 100}
	b := clock.Instant{Wall: 200}
	c := clock.Instant{Wall: 300}

	t.Run("zero range contains every Instant", func(t *testing.T) {
		t.Parallel()
		var r clock.InstantRange
		if !r.IsZero() {
			t.Fatal("zero range must report IsZero")
		}
		for _, i := range []clock.Instant{{}, a, b} {
			if !r.Contains(i) {
				t.Fatalf("zero range must contain %+v", i)
			}
		}
	})

	t.Run("bounded range respects half-open semantics", func(t *testing.T) {
		t.Parallel()
		r := clock.InstantRange{Since: a, Until: c}
		if r.IsZero() {
			t.Fatal("bounded range must not report IsZero")
		}
		// Below Since.
		if r.Contains(clock.Instant{Wall: 50}) {
			t.Fatal("must not contain instant below Since")
		}
		// At Since (inclusive).
		if !r.Contains(a) {
			t.Fatal("must contain Since (inclusive)")
		}
		// Strictly inside.
		if !r.Contains(b) {
			t.Fatal("must contain instant strictly inside")
		}
		// At Until (exclusive).
		if r.Contains(c) {
			t.Fatal("must not contain Until (exclusive)")
		}
		// Above Until.
		if r.Contains(clock.Instant{Wall: 400}) {
			t.Fatal("must not contain instant above Until")
		}
	})

	t.Run("zero Since means no lower bound", func(t *testing.T) {
		t.Parallel()
		r := clock.InstantRange{Until: b}
		if r.IsZero() {
			t.Fatal("range with Until set must not report IsZero")
		}
		if !r.Contains(clock.Instant{Wall: -1}) {
			t.Fatal("must accept arbitrarily-low instants when Since is zero")
		}
		if !r.Contains(a) {
			t.Fatal("must contain a (below Until)")
		}
		if r.Contains(b) {
			t.Fatal("must not contain Until itself")
		}
	})

	t.Run("zero Until means no upper bound", func(t *testing.T) {
		t.Parallel()
		r := clock.InstantRange{Since: b}
		if r.IsZero() {
			t.Fatal("range with Since set must not report IsZero")
		}
		if r.Contains(a) {
			t.Fatal("must not contain instant below Since")
		}
		if !r.Contains(b) {
			t.Fatal("must contain Since")
		}
		if !r.Contains(c) {
			t.Fatal("must accept arbitrarily-high instants when Until is zero")
		}
	})
}

// TestZeroAlloc enforces the documented "Zero alloc" contracts on
// Instant and InstantRange. testing.AllocsPerRun uses a
// process-global malloc counter, so this test does not call
// t.Parallel.
func TestZeroAlloc(t *testing.T) {
	a := clock.Instant{Wall: 1_000_000_000, Logical: 1, Node: 1}
	b := clock.Instant{Wall: 2_000_000_000, Logical: 2, Node: 2}
	r := clock.InstantRange{Since: a, Until: b}

	cases := []struct {
		name string
		fn   func()
	}{
		{"Instant.Compare", func() { _ = a.Compare(b) }},
		{"Instant.HappensBefore", func() { _ = a.HappensBefore(b) }},
		{"Instant.Sub", func() { _ = b.Sub(a) }},
		{"Instant.Add", func() { _ = a.Add(time.Second) }},
		{"Instant.Time", func() { _ = a.Time() }},
		{"Instant.IsZero", func() { _ = a.IsZero() }},
		{"InstantRange.Contains", func() { _ = r.Contains(a) }},
		{"InstantRange.IsZero", func() { _ = r.IsZero() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := testing.AllocsPerRun(100, tc.fn); got != 0 {
				t.Fatalf("%s: %v allocs/op, want 0", tc.name, got)
			}
		})
	}
}

func BenchmarkInstantCompare(b *testing.B) {
	a := clock.Instant{Wall: 1_000_000_000, Logical: 1, Node: 1}
	c := clock.Instant{Wall: 1_000_000_000, Logical: 2, Node: 1}
	b.ReportAllocs()
	for b.Loop() {
		_ = a.Compare(c)
	}
}

func BenchmarkInstantHappensBefore(b *testing.B) {
	a := clock.Instant{Wall: 1_000_000_000, Logical: 1}
	c := clock.Instant{Wall: 2_000_000_000}
	b.ReportAllocs()
	for b.Loop() {
		_ = a.HappensBefore(c)
	}
}

func BenchmarkInstantSub(b *testing.B) {
	now := clock.Instant{Wall: time.Now().UnixNano()}
	earlier := clock.Instant{Wall: now.Wall - int64(time.Second)}
	b.ReportAllocs()
	for b.Loop() {
		_ = now.Sub(earlier)
	}
}

func BenchmarkInstantAdd(b *testing.B) {
	i := clock.Instant{Wall: time.Now().UnixNano()}
	b.ReportAllocs()
	for b.Loop() {
		_ = i.Add(time.Second)
	}
}

func BenchmarkInstantTime(b *testing.B) {
	i := clock.Instant{Wall: time.Now().UnixNano()}
	b.ReportAllocs()
	for b.Loop() {
		_ = i.Time()
	}
}

func BenchmarkInstantRangeContains(b *testing.B) {
	r := clock.InstantRange{
		Since: clock.Instant{Wall: 1_000_000_000},
		Until: clock.Instant{Wall: 2_000_000_000},
	}
	i := clock.Instant{Wall: 1_500_000_000}
	b.ReportAllocs()
	for b.Loop() {
		_ = r.Contains(i)
	}
}
