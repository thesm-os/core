// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package arena_test

import (
	"runtime"
	"testing"

	"go.thesmos.sh/testkit"

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
		testkit.Equal(t, a.Len(), 0, "Len after Reset must be 0")
		testkit.Equal(t, a.Cap(), capBefore, "Reset must preserve Cap")
	})

	t.Run("subsequent Append starts from offset 0", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		a.Append([]byte("first"))
		a.Reset()
		got := a.Append([]byte("second"))
		testkit.Equal(t, string(got), "second", "Append after Reset must return the new value")
		testkit.Equal(t, a.Len(), len("second"), "Len after Reset+Append must equal new value length")
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
			testkit.Equal(t, a.CapExceeds(tc.max), tc.want,
				"CapExceeds must reflect cap vs max comparison")
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
		testkit.Equal(t, a.Len(), 0, "Len after Shrink must be 0")
		testkit.Equal(t, a.Cap(), 0, "Cap after Shrink must be 0 — backing buffer released")
	})

	t.Run("arena remains usable after Shrink", func(t *testing.T) {
		t.Parallel()
		a := arena.NewWithCapacity(64)
		a.Append([]byte("first"))
		a.Shrink()
		testkit.Equal(t, string(a.Append([]byte("second"))), "second",
			"Append after Shrink must return the new value")
	})
}

// TestLifecycleZeroAlloc cannot run in parallel —
// testing.AllocsPerRun panics if any other test is running.
//
//nolint:paralleltest // see comment above
func TestLifecycleZeroAlloc(t *testing.T) {
	a := arena.NewWithCapacity(1024)

	t.Run("Reset", func(t *testing.T) {
		testkit.Equal(t, testing.AllocsPerRun(100, func() {
			a.Reset()
		}), float64(0), "Reset must be zero-alloc")
	})

	t.Run("CapExceeds", func(t *testing.T) {
		testkit.Equal(t, testing.AllocsPerRun(100, func() {
			_ = a.CapExceeds(2048)
		}), float64(0), "CapExceeds must be zero-alloc")
	})

	t.Run("Shrink", func(t *testing.T) {
		testkit.Equal(t, testing.AllocsPerRun(100, func() {
			a.Shrink()
		}), float64(0), "Shrink must be zero-alloc")
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
	var sink bool
	for b.Loop() {
		sink = a.CapExceeds(8192)
	}
	runtime.KeepAlive(sink)
}

func BenchmarkShrink(b *testing.B) {
	a := arena.NewWithCapacity(4096)
	b.ReportAllocs()
	for b.Loop() {
		a.Shrink()
	}
}
