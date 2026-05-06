// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package arena_test

import (
	"bytes"
	"testing"

	"go.thesmos.sh/core/arena"
)

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("New returns an empty arena", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		if got := a.Len(); got != 0 {
			t.Fatalf("Len: got %d, want 0", got)
		}
		if got := a.Cap(); got != 0 {
			t.Fatalf("Cap: got %d, want 0", got)
		}
	})

	t.Run("NewWithCapacity pre-allocates the backing buffer", func(t *testing.T) {
		t.Parallel()
		a := arena.NewWithCapacity(1024)
		if got := a.Len(); got != 0 {
			t.Fatalf("Len: got %d, want 0", got)
		}
		if got := a.Cap(); got != 1024 {
			t.Fatalf("Cap: got %d, want 1024", got)
		}
	})
}

func TestAppend(t *testing.T) {
	t.Parallel()

	t.Run("returns a slice equal to the input", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		input := []byte("hello")
		got := a.Append(input)
		if !bytes.Equal(got, input) {
			t.Fatalf("Append: got %q, want %q", got, input)
		}
	})

	t.Run("returned slice has capacity equal to length", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		got := a.Append([]byte("hello"))
		// Three-index slicing caps capacity. append on the
		// returned slice must NOT extend into the arena.
		if cap(got) != len(got) {
			t.Fatalf("cap=%d, want %d (three-index cap)",
				cap(got), len(got))
		}
	})

	t.Run("multiple appends extend Len cumulatively", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		a.Append([]byte("ab"))
		a.Append([]byte("cd"))
		a.Append([]byte("ef"))
		if got := a.Len(); got != 6 {
			t.Fatalf("Len after three Appends: got %d, want 6", got)
		}
	})

	t.Run("returned slice aliases the backing buffer until Reset", func(t *testing.T) {
		t.Parallel()
		a := arena.NewWithCapacity(64)
		first := a.Append([]byte("hello"))
		// A second append within capacity must not invalidate
		// first — arena guarantees stability across Appends as
		// long as no realloc occurs.
		a.Append([]byte("world"))
		if !bytes.Equal(first, []byte("hello")) {
			t.Fatalf("first slice corrupted by subsequent Append: got %q",
				first)
		}
	})
}

func TestAlloc(t *testing.T) {
	t.Parallel()

	t.Run("returns a zeroed slice of length n", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		got := a.Alloc(8)
		if len(got) != 8 {
			t.Fatalf("len: got %d, want 8", len(got))
		}
		for i, b := range got {
			if b != 0 {
				t.Fatalf("byte %d: got %#x, want 0", i, b)
			}
		}
	})

	t.Run("returned slice has capacity equal to length", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		got := a.Alloc(8)
		if cap(got) != len(got) {
			t.Fatalf("cap=%d, want %d", cap(got), len(got))
		}
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
		fresh := a.Alloc(32)
		for i, b := range fresh {
			if b != 0 {
				t.Fatalf("byte %d after Reset+Alloc: got %#x, want 0",
					i, b)
			}
		}
	})

	t.Run("grows via reallocation when capacity exceeded", func(t *testing.T) {
		t.Parallel()
		a := arena.NewWithCapacity(8)
		a.Alloc(4)
		// Force a grow.
		a.Alloc(100)
		if a.Cap() < 104 {
			t.Fatalf("Cap after grow: got %d, want ≥ 104", a.Cap())
		}
	})

	t.Run("uses in-place expansion at cap == needed boundary", func(t *testing.T) {
		t.Parallel()
		// Lock the >= boundary in the cap-fits check: cap
		// exactly equals needed triggers in-place expansion,
		// not the grow branch.
		a := arena.NewWithCapacity(32)
		_ = a.Alloc(32)
		if got := a.Cap(); got != 32 {
			t.Fatalf("Cap at boundary: got %d, want 32 (no reallocation)",
				got)
		}
	})

	t.Run("doubling growth dominates when have*2 > want", func(t *testing.T) {
		t.Parallel()
		// Lock the doubling-dominant grow path: requested
		// capacity is less than 2x current, so grown capacity
		// is have*2 (50*2=100), not want (75).
		a := arena.NewWithCapacity(50)
		_ = a.Alloc(75)
		if got := a.Cap(); got != 100 {
			t.Fatalf("Cap after doubling-dominant grow: got %d, want 100",
				got)
		}
	})

	t.Run("doubling boundary: have*2 == want returns have*2", func(t *testing.T) {
		t.Parallel()
		// Lock the equality boundary in growCap's doubling
		// check: have*2 == want, growCap returns have*2.
		a := arena.NewWithCapacity(50)
		_ = a.Alloc(100)
		if got := a.Cap(); got != 100 {
			t.Fatalf("Cap at doubling boundary: got %d, want 100", got)
		}
	})

	t.Run("requested capacity dominates when have*2 < want", func(t *testing.T) {
		t.Parallel()
		// Lock the want-dominant grow path: requested capacity
		// is more than 2x current, so grown capacity is
		// exactly want (1000), not have*2 (8).
		a := arena.NewWithCapacity(4)
		_ = a.Alloc(1000)
		if got := a.Cap(); got != 1000 {
			t.Fatalf("Cap after want-dominant grow: got %d, want 1000",
				got)
		}
	})

	t.Run("caller may write into the returned slice", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		buf := a.Alloc(5)
		copy(buf, "hello")
		// The arena's Bytes view must reflect the write.
		if !bytes.Equal(a.Bytes(), []byte("hello")) {
			t.Fatalf("arena Bytes: got %q, want hello", a.Bytes())
		}
	})

	t.Run("zero n returns an empty slice", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		got := a.Alloc(0)
		if len(got) != 0 {
			t.Fatalf("Alloc(0) len: got %d, want 0", len(got))
		}
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

		if got := a.Bytes(); !bytes.Equal(got, want) {
			t.Fatalf("Bytes != concat(first, second):\n  got=%x\n  want=%x",
				got, want)
		}
		if got := a.CopyOut(); !bytes.Equal(got, want) {
			t.Fatalf("CopyOut != concat(first, second):\n  got=%x\n  want=%x",
				got, want)
		}
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
		got := a.SliceSince(mark)
		if !bytes.Equal(got, []byte("part1part2")) {
			t.Fatalf("SliceSince: got %q, want part1part2", got)
		}
	})

	t.Run("returns nil when mark is at current end", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		a.Append([]byte("data"))
		mark := a.Mark()
		if got := a.SliceSince(mark); got != nil {
			t.Fatalf("SliceSince at end: got %v, want nil", got)
		}
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

		if got := a.SliceSince(stale); got != nil {
			t.Fatalf("stale Marker after Reset: got %d bytes, want nil",
				len(got))
		}
	})

	t.Run("Marker from previous lifecycle is rejected after Shrink", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		a.Append([]byte("first lifecycle"))
		stale := a.Mark()

		a.Shrink()
		a.Append(make([]byte, 200))

		if got := a.SliceSince(stale); got != nil {
			t.Fatalf("stale Marker after Shrink: got %d bytes, want nil",
				len(got))
		}
	})

	t.Run("returned slice has capped capacity", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		mark := a.Mark()
		a.Append([]byte("hello"))
		got := a.SliceSince(mark)
		if cap(got) != len(got) {
			t.Fatalf("cap=%d, want %d", cap(got), len(got))
		}
	})
}

func TestBytes(t *testing.T) {
	t.Parallel()

	t.Run("empty arena returns empty slice", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		if got := a.Bytes(); len(got) != 0 {
			t.Fatalf("Bytes on empty: got %q, want empty", got)
		}
	})

	t.Run("returns the full appended region", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		a.Append([]byte("hello "))
		a.Append([]byte("world"))
		if got := a.Bytes(); !bytes.Equal(got, []byte("hello world")) {
			t.Fatalf("Bytes: got %q, want hello world", got)
		}
	})

	t.Run("returned slice has capped capacity", func(t *testing.T) {
		t.Parallel()
		a := arena.NewWithCapacity(64)
		a.Append([]byte("data"))
		got := a.Bytes()
		if cap(got) != len(got) {
			t.Fatalf("cap=%d, want %d (three-index cap)",
				cap(got), len(got))
		}
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
		if got := testing.AllocsPerRun(100, func() {
			a.Reset()
			_ = a.Append(data)
		}); got != 0 {
			t.Fatalf("Append: %v allocs/op, want 0", got)
		}
	})

	t.Run("Alloc within capacity", func(t *testing.T) {
		if got := testing.AllocsPerRun(100, func() {
			a.Reset()
			_ = a.Alloc(64)
		}); got != 0 {
			t.Fatalf("Alloc: %v allocs/op, want 0", got)
		}
	})

	t.Run("Mark + SliceSince", func(t *testing.T) {
		a.Reset()
		a.Append(data)
		mark := a.Mark()
		a.Append(data)
		if got := testing.AllocsPerRun(100, func() {
			_ = a.SliceSince(mark)
		}); got != 0 {
			t.Fatalf("SliceSince: %v allocs/op, want 0", got)
		}
	})

	t.Run("Bytes / Len / Cap", func(t *testing.T) {
		a.Reset()
		a.Append(data)
		if got := testing.AllocsPerRun(100, func() {
			_ = a.Bytes()
			_ = a.Len()
			_ = a.Cap()
		}); got != 0 {
			t.Fatalf("Bytes/Len/Cap: %v allocs/op, want 0", got)
		}
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
