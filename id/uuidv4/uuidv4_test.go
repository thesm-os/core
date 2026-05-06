// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package uuidv4_test

import (
	"testing"

	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/uuidv4"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/seeded"
)

// idFromBytes wraps a 16-byte literal as a 128-bit [id.ID].
// Lives in this file so both uuidv4_test.go and parse_test.go
// can use it (same _test package).
func idFromBytes(b ...byte) id.ID {
	var raw [id.Size128]byte
	copy(raw[:], b)
	return id.New128(raw)
}

func TestGeneratorGenerate(t *testing.T) {
	t.Parallel()

	rng := seeded.New(rand.Seed(1))
	g := uuidv4.New(rng)

	t.Run("stamps version-4 in byte 6 high nibble", func(t *testing.T) {
		t.Parallel()
		u := g.Generate()
		b := u.Bytes()
		if got := b[6] & 0xF0; got != 0x40 {
			t.Fatalf("byte 6 high nibble: got 0x%02X, want 0x40", got)
		}
	})

	t.Run("stamps variant-10 in byte 8 high bits", func(t *testing.T) {
		t.Parallel()
		u := g.Generate()
		b := u.Bytes()
		if got := b[8] & 0xC0; got != 0x80 {
			t.Fatalf("byte 8 high bits: got 0x%02X, want 0x80", got)
		}
	})

	t.Run("does not produce Zero", func(t *testing.T) {
		t.Parallel()
		u := g.Generate()
		if u.IsZero() {
			t.Fatal("Generator.Generate returned Zero")
		}
		if u.Size() != id.Size128 {
			t.Fatalf("Size: got %d, want %d", u.Size(), id.Size128)
		}
	})
}

func TestGeneratorDeterministic(t *testing.T) {
	t.Parallel()

	a := uuidv4.New(seeded.New(rand.Seed(42)))
	b := uuidv4.New(seeded.New(rand.Seed(42)))

	for range 8 {
		if got, want := a.Generate(), b.Generate(); got != want {
			t.Fatalf("identical seeds produced different IDs: %v vs %v",
				got, want)
		}
	}
}

// TestGeneratorZeroAlloc cannot run in parallel —
// testing.AllocsPerRun panics if any other test is running.
//
//nolint:paralleltest // see comment above
func TestGeneratorZeroAlloc(t *testing.T) {
	g := uuidv4.New(seeded.New(rand.Seed(1)))
	if got := testing.AllocsPerRun(100, func() { _ = g.Generate() }); got != 0 {
		t.Fatalf("Generator.Generate: %v allocs/op, want 0", got)
	}
}

func BenchmarkGenerate(b *testing.B) {
	g := uuidv4.New(seeded.New(rand.Seed(1)))
	b.ReportAllocs()
	for b.Loop() {
		_ = g.Generate()
	}
}
