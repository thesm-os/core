// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fixed_test

import (
	"testing"

	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/fixed"
)

func TestGenerator(t *testing.T) {
	t.Parallel()

	t.Run("zero-value Generator returns Zero", func(t *testing.T) {
		t.Parallel()
		var g fixed.Generator
		if got := g.Generate(); !got.IsZero() {
			t.Fatalf("zero Generate(): got %v, want Zero", got)
		}
	})

	t.Run("Generate returns the configured value", func(t *testing.T) {
		t.Parallel()
		want := id.New128([id.Size128]byte{
			1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16,
		})
		g := fixed.New(want)
		if got := g.Generate(); got != want {
			t.Fatalf("Generate: got %v, want %v", got, want)
		}
	})

	t.Run("repeated calls return the same value", func(t *testing.T) {
		t.Parallel()
		want := id.New128([id.Size128]byte{42})
		g := fixed.New(want)
		for range 5 {
			if got := g.Generate(); got != want {
				t.Fatalf("repeated Generate: got %v, want %v", got, want)
			}
		}
	})

	t.Run("works with all three sizes", func(t *testing.T) {
		t.Parallel()
		w128 := id.New128([id.Size128]byte{1})
		w160 := id.New160([id.Size160]byte{2})
		w256 := id.New256([id.Size256]byte{3})

		if got := fixed.New(w128).Generate(); got.Size() != id.Size128 {
			t.Fatalf("128 size: got %d", got.Size())
		}
		if got := fixed.New(w160).Generate(); got.Size() != id.Size160 {
			t.Fatalf("160 size: got %d", got.Size())
		}
		if got := fixed.New(w256).Generate(); got.Size() != id.Size256 {
			t.Fatalf("256 size: got %d", got.Size())
		}
	})
}

// TestGeneratorZeroAlloc cannot run in parallel —
// testing.AllocsPerRun panics if any other test is running.
//
//nolint:paralleltest // see comment above
func TestGeneratorZeroAlloc(t *testing.T) {
	g := fixed.New(id.New128([id.Size128]byte{1, 2, 3}))
	if got := testing.AllocsPerRun(100, func() { _ = g.Generate() }); got != 0 {
		t.Fatalf("Generator.Generate: %v allocs/op, want 0", got)
	}
}
