// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package arena_test

import (
	"strconv"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/arena"
)

func TestCopyOut(t *testing.T) {
	t.Parallel()

	t.Run("returns nil for empty arena", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		testkit.True(t, a.CopyOut() == nil, "CopyOut on empty arena must return nil")
	})

	t.Run("returns a caller-owned copy of the appended bytes", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		a.Append([]byte("hello "))
		a.Append([]byte("world"))

		got := a.CopyOut()
		testkit.Equal(t, got, []byte("hello world"),
			"CopyOut must return the appended bytes")

		// The returned slice must be caller-owned: resetting
		// the arena and overwriting must not corrupt got.
		a.Reset()
		a.Append([]byte("OVERWRITE!!"))
		testkit.Equal(t, got, []byte("hello world"),
			"CopyOut must be caller-owned — Reset+Append must not affect prior return")
	})
}

func TestCopyOutTo(t *testing.T) {
	t.Parallel()

	t.Run("appends arena bytes to destination", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		a.Append([]byte("world"))
		dst := []byte("hello ")
		testkit.Equal(t, a.CopyOutTo(dst), []byte("hello world"),
			"CopyOutTo must append arena bytes to dst")
	})

	t.Run("empty arena returns dst unchanged", func(t *testing.T) {
		t.Parallel()
		a := arena.New()
		dst := []byte("hello")
		testkit.Equal(t, a.CopyOutTo(dst), []byte("hello"),
			"CopyOutTo on empty arena must return dst unchanged")
	})
}

func TestRebaseSlices(t *testing.T) {
	t.Parallel()

	t.Run("returns nil for empty input", func(t *testing.T) {
		t.Parallel()
		testkit.True(t, arena.RebaseSlices(nil) == nil,
			"RebaseSlices(nil) must return nil")
		testkit.True(t, arena.RebaseSlices([][]byte{}) == nil,
			"RebaseSlices([]) must return nil")
	})

	t.Run("returns nil when every entry is empty", func(t *testing.T) {
		t.Parallel()
		testkit.True(t, arena.RebaseSlices([][]byte{nil, {}, nil}) == nil,
			"RebaseSlices with all-empty entries must return nil")
	})

	t.Run("rebases entries into a single allocation", func(t *testing.T) {
		t.Parallel()
		entries := [][]byte{
			[]byte("alpha"),
			[]byte("beta"),
			[]byte("gamma"),
		}
		out := arena.RebaseSlices(entries)
		testkit.Equal(t, out, []byte("alphabetagamma"),
			"RebaseSlices output must be the concatenation")
		// Each entry must point into out and contain its
		// original bytes.
		testkit.Equal(t, entries[0], []byte("alpha"), "entries[0] must remain alpha")
		testkit.Equal(t, entries[1], []byte("beta"), "entries[1] must remain beta")
		testkit.Equal(t, entries[2], []byte("gamma"), "entries[2] must remain gamma")
	})

	t.Run("each rebased entry has capacity capped at length", func(t *testing.T) {
		t.Parallel()
		entries := [][]byte{
			[]byte("ab"),
			[]byte("cd"),
		}
		_ = arena.RebaseSlices(entries)
		for i, e := range entries {
			testkit.Equal(t, cap(e), len(e),
				"entries["+strconv.Itoa(i)+"] cap must be capped at length")
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
		testkit.Equal(t, entries[1], []byte("cd"),
			"entries[1] must remain cd — three-index cap must prevent aliasing")
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
		testkit.Equal(t, out, []byte("prefix:alphabeta"),
			"RebaseSlicesTo must append entries to dst")
		// Entries point into the new dst, with their original
		// bytes preserved.
		testkit.Equal(t, entries[0], []byte("alpha"), "entries[0] must remain alpha")
		testkit.Equal(t, entries[1], []byte("beta"), "entries[1] must remain beta")
	})

	t.Run("empty entries return dst unchanged", func(t *testing.T) {
		t.Parallel()
		dst := []byte("hello")
		testkit.Equal(t, arena.RebaseSlicesTo(dst, nil), dst,
			"RebaseSlicesTo with nil entries must return dst unchanged")
	})

	t.Run("each rebased entry has capacity capped at length", func(t *testing.T) {
		t.Parallel()
		entries := [][]byte{
			[]byte("xy"),
			[]byte("z"),
		}
		_ = arena.RebaseSlicesTo(nil, entries)
		for i, e := range entries {
			testkit.Equal(t, cap(e), len(e),
				"entries["+strconv.Itoa(i)+"] cap must be capped at length")
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
		testkit.Equal(t, testing.AllocsPerRun(100, func() {
			_ = a.CopyOutTo(dst)
		}), float64(0), "CopyOutTo with sufficient dst capacity must be zero-alloc")
	})

	t.Run("RebaseSlicesTo with sufficient dst capacity", func(t *testing.T) {
		entries := [][]byte{
			[]byte("ab"), []byte("cd"), []byte("ef"),
		}
		dst := make([]byte, 0, 64)
		testkit.Equal(t, testing.AllocsPerRun(100, func() {
			// RebaseSlicesTo mutates entries; we don't care
			// about that for the alloc test.
			_ = arena.RebaseSlicesTo(dst, entries)
		}), float64(0), "RebaseSlicesTo with sufficient dst capacity must be zero-alloc")
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
			// sink read past the loop forces CopyOut's slice
			// to escape uniformly across sizes. Without it,
			// escape analysis stack-promotes small returns
			// and the bench under-reports the "always
			// allocates" contract on small input.
			var sink []byte
			a := arena.NewWithCapacity(sz.n * 2)
			a.Append(make([]byte, sz.n))
			b.ReportAllocs()
			b.SetBytes(int64(sz.n))
			for b.Loop() {
				sink = a.CopyOut()
			}
			if len(sink) == 0 {
				b.Fatal("sink unexpectedly empty after loop")
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
