// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package arena_test

import (
	"bytes"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/arena"
)

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("New returns an empty arena", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		testkit.Equal(t, a.Len(), 0, "Len on fresh arena must be 0")
		testkit.Equal(t, a.Cap(), 0, "Cap on fresh arena must be 0")
	})

	t.Run("NewWithCapacity pre-allocates the backing buffer", func(t *testing.T) {
		t.Parallel()
		a := arena.NewWithCapacity(1024)
		testkit.Equal(t, a.Len(), 0, "Len on freshly-allocated arena must be 0")
		testkit.Equal(t, a.Cap(), 1024, "Cap must equal the requested capacity")
	})
}

func TestAppend(t *testing.T) {
	t.Parallel()

	t.Run("returns a slice equal to the input", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		input := []byte("hello")
		testkit.Equal(t, a.Append(input), input,
			"Append must return a slice equal to the supplied input")
	})

	t.Run("returned slice has capacity equal to length", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		got := a.Append([]byte("hello"))
		// Three-index slicing caps capacity. append on the
		// returned slice must NOT extend into the arena.
		testkit.Equal(t, cap(got), len(got),
			"three-index cap must keep cap == len so callers can't extend into the arena")
	})

	t.Run("multiple appends extend Len cumulatively", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		a.Append([]byte("ab"))
		a.Append([]byte("cd"))
		a.Append([]byte("ef"))
		testkit.Equal(t, a.Len(), 6, "Len must equal the cumulative bytes appended")
	})

	t.Run("returned slice aliases the backing buffer until Reset", func(t *testing.T) {
		t.Parallel()
		a := arena.NewWithCapacity(64)
		first := a.Append([]byte("hello"))
		// A second append within capacity must not invalidate
		// first — arena guarantees stability across Appends as
		// long as no realloc occurs.
		a.Append([]byte("world"))
		testkit.Equal(t, first, []byte("hello"),
			"first slice must remain stable across subsequent in-capacity appends")
	})
}

func TestAlloc(t *testing.T) {
	t.Parallel()

	t.Run("returns a zeroed slice of length n", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		got := a.Alloc(8)
		testkit.Equal(t, len(got), 8, "Alloc(n) must return a slice of length n")
		testkit.Equal(t, got, make([]byte, 8), "Alloc must zero the returned region")
	})

	t.Run("returned slice has capacity equal to length", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		got := a.Alloc(8)
		testkit.Equal(t, cap(got), len(got), "Alloc must cap capacity at length")
	})

	t.Run("zeroes new region after Reset reuse", func(t *testing.T) {
		t.Parallel()
		a := arena.NewWithCapacity(64)
		// Fill the buffer with non-zero bytes.
		filled := a.Alloc(32)
		for i := range filled {
			filled[i] = 0xFF
		}
		a.Reset()
		// Alloc must return zeroed bytes, not the prior 0xFFs.
		testkit.Equal(t, a.Alloc(32), make([]byte, 32),
			"Alloc after Reset must zero the reused region")
	})

	t.Run("grows via reallocation when capacity exceeded", func(t *testing.T) {
		t.Parallel()
		a := arena.NewWithCapacity(8)
		a.Alloc(4)
		// Force a grow.
		a.Alloc(100)
		testkit.True(t, a.Cap() >= 104, "Cap must be ≥ requested after grow")
	})

	t.Run("uses in-place expansion at cap == needed boundary", func(t *testing.T) {
		t.Parallel()
		// Lock the >= boundary in the cap-fits check: cap
		// exactly equals needed triggers in-place expansion,
		// not the grow branch.
		a := arena.NewWithCapacity(32)
		_ = a.Alloc(32)
		testkit.Equal(t, a.Cap(), 32,
			"Cap at boundary must not reallocate — in-place expansion")
	})

	t.Run("doubling growth dominates when have*2 > want", func(t *testing.T) {
		t.Parallel()
		// Lock the doubling-dominant grow path: requested
		// capacity is less than 2x current, so grown capacity
		// is have*2 (50*2=100), not want (75).
		a := arena.NewWithCapacity(50)
		_ = a.Alloc(75)
		testkit.Equal(t, a.Cap(), 100,
			"doubling must dominate when have*2 > want")
	})

	t.Run("doubling boundary: have*2 == want returns have*2", func(t *testing.T) {
		t.Parallel()
		// Lock the equality boundary in growCap's doubling
		// check: have*2 == want, growCap returns have*2.
		a := arena.NewWithCapacity(50)
		_ = a.Alloc(100)
		testkit.Equal(t, a.Cap(), 100,
			"have*2 == want must return have*2")
	})

	t.Run("requested capacity dominates when have*2 < want", func(t *testing.T) {
		t.Parallel()
		// Lock the want-dominant grow path: requested capacity
		// is more than 2x current, so grown capacity is
		// exactly want (1000), not have*2 (8).
		a := arena.NewWithCapacity(4)
		_ = a.Alloc(1000)
		testkit.Equal(t, a.Cap(), 1000,
			"want must dominate when have*2 < want")
	})

	t.Run("caller may write into the returned slice", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		buf := a.Alloc(5)
		copy(buf, "hello")
		// The arena's Bytes view must reflect the write.
		testkit.Equal(t, a.Bytes(), []byte("hello"),
			"writes into Alloc'd slice must reflect in arena Bytes")
	})

	t.Run("zero n returns an empty slice", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		testkit.Equal(t, len(a.Alloc(0)), 0, "Alloc(0) must return an empty slice")
	})
}

// FuzzAppendBytesEquivalence asserts that bytes appended to
// the arena read back identically via Bytes — for arbitrary
// chunked input. Catches any divergence between the append
// and read paths under unusual chunk-size patterns.
func FuzzAppendBytesEquivalence(f *testing.F) {
	f.Add([]byte(""), []byte(""))
	f.Add([]byte("hello"), []byte(" world"))
	f.Add(bytes.Repeat([]byte{0xAA}, 1024), bytes.Repeat([]byte{0xBB}, 1024))

	f.Fuzz(func(t *testing.T, first, second []byte) {
		a := arena.New()
		a.Append(first)
		a.Append(second)

		want := append([]byte{}, first...)
		want = append(want, second...)

		// bytes.Equal treats nil and empty slice as equal; go-cmp
		// (which testkit.Equal uses) does not. The arena may return
		// nil for an empty result while want is the empty slice —
		// stick with bytes.Equal here for the right semantics.
		testkit.True(t, bytes.Equal(a.Bytes(), want),
			"Bytes must equal the concatenation of the two Appends")
		testkit.True(t, bytes.Equal(a.CopyOut(), want),
			"CopyOut must equal the concatenation of the two Appends")
	})
}

func TestMarkSliceSince(t *testing.T) {
	t.Parallel()

	t.Run("captures a region built across multiple appends", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		a.Append([]byte("prefix:"))
		mark := a.Mark()
		a.Append([]byte("part1"))
		a.Append([]byte("part2"))
		testkit.Equal(t, a.SliceSince(mark), []byte("part1part2"),
			"SliceSince must return bytes appended after the mark")
	})

	t.Run("returns nil when mark is at current end", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		a.Append([]byte("data"))
		mark := a.Mark()
		testkit.True(t, a.SliceSince(mark) == nil,
			"SliceSince at end must return nil — no bytes after mark")
	})

	t.Run("Marker from previous lifecycle is rejected after Reset", func(t *testing.T) {
		t.Parallel()
		// Lock the epoch invalidation: a Marker captured
		// before Reset must produce nil from SliceSince after
		// Reset, even if subsequent appends extend past the
		// captured position. Prevents silent corruption when a
		// stale Marker survives across a lifecycle boundary.
		a := arena.New()
		a.Append([]byte("first lifecycle"))
		stale := a.Mark()

		a.Reset()
		a.Append(make([]byte, 200)) // len now 200; stale.pos was 15

		testkit.True(t, a.SliceSince(stale) == nil,
			"stale Marker after Reset must return nil")
	})

	t.Run("Marker from previous lifecycle is rejected after Shrink", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		a.Append([]byte("first lifecycle"))
		stale := a.Mark()

		a.Shrink()
		a.Append(make([]byte, 200))

		testkit.True(t, a.SliceSince(stale) == nil,
			"stale Marker after Shrink must return nil")
	})

	t.Run("returned slice has capped capacity", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		mark := a.Mark()
		a.Append([]byte("hello"))
		got := a.SliceSince(mark)
		testkit.Equal(t, cap(got), len(got),
			"SliceSince must cap capacity at length (three-index slicing)")
	})
}

func TestBytes(t *testing.T) {
	t.Parallel()

	t.Run("empty arena returns empty slice", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		testkit.Equal(t, len(a.Bytes()), 0, "Bytes on empty arena must be empty")
	})

	t.Run("returns the full appended region", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		a.Append([]byte("hello "))
		a.Append([]byte("world"))
		testkit.Equal(t, a.Bytes(), []byte("hello world"),
			"Bytes must return the full appended region")
	})

	t.Run("returned slice has capped capacity", func(t *testing.T) {
		t.Parallel()
		a := arena.NewWithCapacity(64)
		a.Append([]byte("data"))
		got := a.Bytes()
		testkit.Equal(t, cap(got), len(got),
			"Bytes must cap capacity at length (three-index slicing)")
	})
}

// TestZeroAlloc cannot run in parallel — testing.AllocsPerRun
// panics if any other test is running.
//
//nolint:paralleltest // see comment above
func TestZeroAlloc(t *testing.T) {
	a := arena.NewWithCapacity(1024)
	data := []byte("hello world")

	t.Run("Append into pre-sized buffer", func(t *testing.T) {
		// Reset between iterations so capacity stays in scope.
		testkit.Equal(t, testing.AllocsPerRun(100, func() {
			a.Reset()
			_ = a.Append(data)
		}), float64(0), "Append into pre-sized arena must be zero-alloc")
	})

	t.Run("Alloc within capacity", func(t *testing.T) {
		testkit.Equal(t, testing.AllocsPerRun(100, func() {
			a.Reset()
			_ = a.Alloc(64)
		}), float64(0), "Alloc within capacity must be zero-alloc")
	})

	t.Run("Mark + SliceSince", func(t *testing.T) {
		a.Reset()
		a.Append(data)
		mark := a.Mark()
		a.Append(data)
		testkit.Equal(t, testing.AllocsPerRun(100, func() {
			_ = a.SliceSince(mark)
		}), float64(0), "SliceSince must be zero-alloc")
	})

	t.Run("Bytes / Len / Cap", func(t *testing.T) {
		a.Reset()
		a.Append(data)
		testkit.Equal(t, testing.AllocsPerRun(100, func() {
			_ = a.Bytes()
			_ = a.Len()
			_ = a.Cap()
		}), float64(0), "Bytes/Len/Cap must be zero-alloc")
	})
}

func BenchmarkAppend(b *testing.B) {
	for _, sz := range []struct {
		name string
		n    int
	}{
		{"16B", 16},
		{"64B", 64},
		{"256B", 256},
		{"4K", 4096},
		{"64K", 65536},
	} {
		b.Run(sz.name, func(b *testing.B) {
			a := arena.NewWithCapacity(sz.n * 2)
			data := make([]byte, sz.n)
			b.ReportAllocs()
			b.SetBytes(int64(sz.n))
			for b.Loop() {
				a.Reset()
				_ = a.Append(data)
			}
		})
	}
}

func BenchmarkAlloc(b *testing.B) {
	for _, sz := range []struct {
		name string
		n    int
	}{
		{"16B", 16},
		{"64B", 64},
		{"256B", 256},
		{"4K", 4096},
		{"64K", 65536},
	} {
		b.Run(sz.name, func(b *testing.B) {
			a := arena.NewWithCapacity(sz.n * 2)
			b.ReportAllocs()
			b.SetBytes(int64(sz.n))
			for b.Loop() {
				a.Reset()
				_ = a.Alloc(sz.n)
			}
		})
	}
}

func BenchmarkMark(b *testing.B) {
	a := arena.NewWithCapacity(4096)
	a.Append(make([]byte, 256))
	b.ReportAllocs()
	for b.Loop() {
		_ = a.Mark()
	}
}

func BenchmarkSliceSince(b *testing.B) {
	a := arena.NewWithCapacity(4096)
	m := a.Mark()
	a.Append(make([]byte, 128))
	b.ReportAllocs()
	for b.Loop() {
		_ = a.SliceSince(m)
	}
}

func BenchmarkBytes(b *testing.B) {
	a := arena.NewWithCapacity(4096)
	a.Append(make([]byte, 1024))
	b.ReportAllocs()
	for b.Loop() {
		_ = a.Bytes()
	}
}
