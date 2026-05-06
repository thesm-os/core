// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package arena_test

import (
	"bytes"
	"testing"

	"go.thesmos.sh/core/arena"
)

func TestCopyOut(t *testing.T) {
	t.Parallel()

	t.Run("returns nil for empty arena", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		if got := a.CopyOut(); got != nil {
			t.Fatalf("CopyOut on empty: got %v, want nil", got)
		}
	})

	t.Run("returns a caller-owned copy of the appended bytes", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		a.Append([]byte("hello "))
		a.Append([]byte("world"))

		got := a.CopyOut()
		if !bytes.Equal(got, []byte("hello world")) {
			t.Fatalf("CopyOut: got %q, want hello world", got)
		}

		// The returned slice must be caller-owned: resetting
		// the arena and overwriting must not corrupt got.
		a.Reset()
		a.Append([]byte("OVERWRITE!!"))
		if !bytes.Equal(got, []byte("hello world")) {
			t.Fatalf("CopyOut return aliased arena buffer: got %q after Reset+Append",
				got)
		}
	})
}

func TestCopyOutTo(t *testing.T) {
	t.Parallel()

	t.Run("appends arena bytes to destination", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		a.Append([]byte("world"))
		dst := []byte("hello ")
		got := a.CopyOutTo(dst)
		if !bytes.Equal(got, []byte("hello world")) {
			t.Fatalf("CopyOutTo: got %q, want hello world", got)
		}
	})

	t.Run("empty arena returns dst unchanged", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		dst := []byte("hello")
		got := a.CopyOutTo(dst)
		if !bytes.Equal(got, []byte("hello")) {
			t.Fatalf("CopyOutTo on empty: got %q, want hello", got)
		}
	})
}

func TestRebaseSlices(t *testing.T) {
	t.Parallel()

	t.Run("returns nil for empty input", func(t *testing.T) {
		t.Parallel()
		if got := arena.RebaseSlices(nil); got != nil {
			t.Fatalf("RebaseSlices(nil): got %v, want nil", got)
		}
		if got := arena.RebaseSlices([][]byte{}); got != nil {
			t.Fatalf("RebaseSlices(empty): got %v, want nil", got)
		}
	})

	t.Run("returns nil when every entry is empty", func(t *testing.T) {
		t.Parallel()
		got := arena.RebaseSlices([][]byte{nil, {}, nil})
		if got != nil {
			t.Fatalf("RebaseSlices(all empty): got %v, want nil", got)
		}
	})

	t.Run("rebases entries into a single allocation", func(t *testing.T) {
		t.Parallel()
		entries := [][]byte{
			[]byte("alpha"),
			[]byte("beta"),
			[]byte("gamma"),
		}
		out := arena.RebaseSlices(entries)
		if !bytes.Equal(out, []byte("alphabetagamma")) {
			t.Fatalf("RebaseSlices output: got %q, want alphabetagamma", out)
		}
		// Each entry must point into out and contain its
		// original bytes.
		if !bytes.Equal(entries[0], []byte("alpha")) {
			t.Fatalf("entries[0]: got %q", entries[0])
		}
		if !bytes.Equal(entries[1], []byte("beta")) {
			t.Fatalf("entries[1]: got %q", entries[1])
		}
		if !bytes.Equal(entries[2], []byte("gamma")) {
			t.Fatalf("entries[2]: got %q", entries[2])
		}
	})

	t.Run("each rebased entry has capacity capped at length", func(t *testing.T) {
		t.Parallel()
		entries := [][]byte{
			[]byte("ab"),
			[]byte("cd"),
		}
		_ = arena.RebaseSlices(entries)
		for i, e := range entries {
			if cap(e) != len(e) {
				t.Fatalf("entries[%d]: cap=%d, want %d (three-index cap)",
					i, cap(e), len(e))
			}
		}
	})

	t.Run("rebased entries don't alias each other", func(t *testing.T) {
		t.Parallel()
		entries := [][]byte{
			[]byte("ab"),
			[]byte("cd"),
		}
		_ = arena.RebaseSlices(entries)
		// Append onto entries[0]; three-index capping must
		// prevent it from extending into entries[1].
		entries[0] = append(entries[0], 'X', 'Y')
		if !bytes.Equal(entries[1], []byte("cd")) {
			t.Fatalf("entries[1] corrupted by append on entries[0]: got %q",
				entries[1])
		}
	})
}

func TestRebaseSlicesTo(t *testing.T) {
	t.Parallel()

	t.Run("appends entries into dst and rebases", func(t *testing.T) {
		t.Parallel()
		entries := [][]byte{
			[]byte("alpha"),
			[]byte("beta"),
		}
		dst := []byte("prefix:")
		out := arena.RebaseSlicesTo(dst, entries)
		if !bytes.Equal(out, []byte("prefix:alphabeta")) {
			t.Fatalf("RebaseSlicesTo: got %q, want prefix:alphabeta", out)
		}
		// Entries point into the new dst, with their original
		// bytes preserved.
		if !bytes.Equal(entries[0], []byte("alpha")) {
			t.Fatalf("entries[0]: got %q", entries[0])
		}
		if !bytes.Equal(entries[1], []byte("beta")) {
			t.Fatalf("entries[1]: got %q", entries[1])
		}
	})

	t.Run("empty entries return dst unchanged", func(t *testing.T) {
		t.Parallel()
		dst := []byte("hello")
		out := arena.RebaseSlicesTo(dst, nil)
		if !bytes.Equal(out, dst) {
			t.Fatalf("RebaseSlicesTo with nil entries: got %q, want %q",
				out, dst)
		}
	})

	t.Run("each rebased entry has capacity capped at length", func(t *testing.T) {
		t.Parallel()
		entries := [][]byte{
			[]byte("xy"),
			[]byte("z"),
		}
		_ = arena.RebaseSlicesTo(nil, entries)
		for i, e := range entries {
			if cap(e) != len(e) {
				t.Fatalf("entries[%d]: cap=%d, want %d",
					i, cap(e), len(e))
			}
		}
	})
}

// TestCopyZeroAlloc cannot run in parallel —
// testing.AllocsPerRun panics if any other test is running.
//
//nolint:paralleltest // see comment above
func TestCopyZeroAlloc(t *testing.T) {
	t.Run("CopyOutTo with sufficient dst capacity", func(t *testing.T) {
		a := arena.NewWithCapacity(1024)
		a.Append([]byte("hello world"))
		dst := make([]byte, 0, 64)
		if got := testing.AllocsPerRun(100, func() {
			_ = a.CopyOutTo(dst)
		}); got != 0 {
			t.Fatalf("CopyOutTo: %v allocs/op, want 0", got)
		}
	})

	t.Run("RebaseSlicesTo with sufficient dst capacity", func(t *testing.T) {
		entries := [][]byte{
			[]byte("ab"), []byte("cd"), []byte("ef"),
		}
		dst := make([]byte, 0, 64)
		if got := testing.AllocsPerRun(100, func() {
			// RebaseSlicesTo mutates entries; we don't care
			// about that for the alloc test.
			_ = arena.RebaseSlicesTo(dst, entries)
		}); got != 0 {
			t.Fatalf("RebaseSlicesTo: %v allocs/op, want 0", got)
		}
	})
}

func BenchmarkCopyOut(b *testing.B) {
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
			a.Append(make([]byte, sz.n))
			b.ReportAllocs()
			b.SetBytes(int64(sz.n))
			for b.Loop() {
				_ = a.CopyOut()
			}
		})
	}
}

func BenchmarkCopyOutTo(b *testing.B) {
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
			a.Append(make([]byte, sz.n))
			dst := make([]byte, 0, sz.n)
			b.ReportAllocs()
			b.SetBytes(int64(sz.n))
			for b.Loop() {
				dst = a.CopyOutTo(dst[:0])
			}
		})
	}
}

func BenchmarkRebaseSlices(b *testing.B) {
	a := arena.NewWithCapacity(64 * 1024)
	const items = 32
	const itemSize = 256
	for range items {
		a.Append(make([]byte, itemSize))
	}
	b.ReportAllocs()
	b.SetBytes(int64(items * itemSize))
	for b.Loop() {
		entries := make([][]byte, 0, items)
		off := 0
		all := a.Bytes()
		for range items {
			entries = append(entries, all[off:off+itemSize])
			off += itemSize
		}
		_ = arena.RebaseSlices(entries)
	}
}

func BenchmarkRebaseSlicesTo(b *testing.B) {
	a := arena.NewWithCapacity(64 * 1024)
	const items = 32
	const itemSize = 256
	for range items {
		a.Append(make([]byte, itemSize))
	}
	dst := make([]byte, 0, items*itemSize)
	b.ReportAllocs()
	b.SetBytes(int64(items * itemSize))
	for b.Loop() {
		entries := make([][]byte, 0, items)
		off := 0
		all := a.Bytes()
		for range items {
			entries = append(entries, all[off:off+itemSize])
			off += itemSize
		}
		dst = arena.RebaseSlicesTo(dst[:0], entries)
	}
}
