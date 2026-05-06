// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package arena_test

import (
	"testing"

	"go.thesmos.sh/core/arena"
)

func TestReset(t *testing.T) {
	t.Parallel()

	t.Run("drops Len to zero, preserves Cap", func(t *testing.T) {
		t.Parallel()
		a := arena.NewWithCapacity(64)
		a.Append([]byte("data"))
		capBefore := a.Cap()
		a.Reset()
		if got := a.Len(); got != 0 {
			t.Fatalf("Len after Reset: got %d, want 0", got)
		}
		if got := a.Cap(); got != capBefore {
			t.Fatalf("Cap changed by Reset: got %d, want %d",
				got, capBefore)
		}
	})

	t.Run("subsequent Append starts from offset 0", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		a.Append([]byte("first"))
		a.Reset()
		got := a.Append([]byte("second"))
		if string(got) != "second" {
			t.Fatalf("Append after Reset: got %q, want second", got)
		}
		if a.Len() != len("second") {
			t.Fatalf("Len: got %d, want 6", a.Len())
		}
	})
}

func TestCapExceeds(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		cap  int
		max  int
		want bool
	}{
		"cap below max":  {cap: 64, max: 1024, want: false},
		"cap equals max": {cap: 1024, max: 1024, want: false},
		"cap above max":  {cap: 2048, max: 1024, want: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			a := arena.NewWithCapacity(tc.cap)
			if got := a.CapExceeds(tc.max); got != tc.want {
				t.Fatalf("CapExceeds(%d, max=%d): got %v, want %v",
					tc.cap, tc.max, got, tc.want)
			}
		})
	}
}

func TestShrink(t *testing.T) {
	t.Parallel()

	t.Run("releases backing buffer", func(t *testing.T) {
		t.Parallel()
		a := arena.NewWithCapacity(1024)
		a.Append([]byte("data"))
		a.Shrink()
		if got := a.Len(); got != 0 {
			t.Fatalf("Len after Shrink: got %d, want 0", got)
		}
		if got := a.Cap(); got != 0 {
			t.Fatalf("Cap after Shrink: got %d, want 0", got)
		}
	})

	t.Run("arena remains usable after Shrink", func(t *testing.T) {
		t.Parallel()
		a := arena.NewWithCapacity(64)
		a.Append([]byte("first"))
		a.Shrink()
		got := a.Append([]byte("second"))
		if string(got) != "second" {
			t.Fatalf("Append after Shrink: got %q, want second", got)
		}
	})
}

// TestLifecycleZeroAlloc cannot run in parallel —
// testing.AllocsPerRun panics if any other test is running.
//
//nolint:paralleltest // see comment above
func TestLifecycleZeroAlloc(t *testing.T) {
	a := arena.NewWithCapacity(1024)

	t.Run("Reset", func(t *testing.T) {
		if got := testing.AllocsPerRun(100, func() {
			a.Reset()
		}); got != 0 {
			t.Fatalf("Reset: %v allocs/op, want 0", got)
		}
	})

	t.Run("CapExceeds", func(t *testing.T) {
		if got := testing.AllocsPerRun(100, func() {
			_ = a.CapExceeds(2048)
		}); got != 0 {
			t.Fatalf("CapExceeds: %v allocs/op, want 0", got)
		}
	})

	t.Run("Shrink", func(t *testing.T) {
		if got := testing.AllocsPerRun(100, func() {
			a.Shrink()
		}); got != 0 {
			t.Fatalf("Shrink: %v allocs/op, want 0", got)
		}
	})
}

func BenchmarkReset(b *testing.B) {
	a := arena.NewWithCapacity(4096)
	a.Append(make([]byte, 1024))
	b.ReportAllocs()
	for b.Loop() {
		a.Reset()
	}
}

func BenchmarkCapExceeds(b *testing.B) {
	a := arena.NewWithCapacity(4096)
	b.ReportAllocs()
	for b.Loop() {
		_ = a.CapExceeds(8192)
	}
}

func BenchmarkShrink(b *testing.B) {
	a := arena.NewWithCapacity(4096)
	b.ReportAllocs()
	for b.Loop() {
		a.Shrink()
	}
}
