// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package ulid_test

import (
	"testing"
	"time"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/ulid"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/seeded"
)

// idFromBytes wraps a 16-byte literal as a 128-bit [id.ID].
// Lives in this file so both ulid_test.go and parse_test.go can
// use it (same _test package).
func idFromBytes(b ...byte) id.ID {
	var raw [id.Size128]byte
	copy(raw[:], b)
	return id.New128(raw)
}

func TestGeneratorGenerate(t *testing.T) {
	t.Parallel()

	t.Run("encodes timestamp into bytes 0..5 big-endian", func(t *testing.T) {
		t.Parallel()
		origin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		clk := fake.New(origin)
		rng := seeded.New(rand.Seed(1))
		g := ulid.New(clk, rng)

		u := g.Generate()
		gotMs := ulid.TimestampMillis(u)
		wantMs := uint64(origin.UnixMilli())
		if gotMs != wantMs {
			t.Fatalf("TimestampMillis: got %d, want %d", gotMs, wantMs)
		}
		if u.Size() != id.Size128 {
			t.Fatalf("Size: got %d, want %d", u.Size(), id.Size128)
		}
	})

	t.Run("two generators with identical seed produce different streams when clock advances", func(t *testing.T) {
		t.Parallel()
		origin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		clk := fake.New(origin)
		g := ulid.New(clk, seeded.New(rand.Seed(1)))

		first := g.Generate()
		clk.Advance(1 * time.Millisecond)
		second := g.Generate()
		if first == second {
			t.Fatal("two ULIDs minted at distinct times should differ")
		}
		if ulid.TimestampMillis(first) >= ulid.TimestampMillis(second) {
			t.Fatalf("timestamp not monotonic: first=%d second=%d",
				ulid.TimestampMillis(first), ulid.TimestampMillis(second))
		}
	})

	t.Run("generator is deterministic given fixed clock and seed", func(t *testing.T) {
		t.Parallel()
		origin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		clkA := fake.New(origin)
		clkB := fake.New(origin)
		gA := ulid.New(clkA, seeded.New(rand.Seed(42)))
		gB := ulid.New(clkB, seeded.New(rand.Seed(42)))

		for range 4 {
			if got, want := gA.Generate(), gB.Generate(); got != want {
				t.Fatalf("identical inputs produced different ULIDs: %v vs %v",
					got, want)
			}
		}
	})

	t.Run("does not produce Zero", func(t *testing.T) {
		t.Parallel()
		g := ulid.New(fake.New(time.Now()), seeded.New(rand.Seed(1)))
		if u := g.Generate(); u.IsZero() {
			t.Fatal("Generator.Generate returned Zero")
		}
	})
}

func TestTimestampMillis(t *testing.T) {
	t.Parallel()

	t.Run("Zero ID has zero timestamp", func(t *testing.T) {
		t.Parallel()
		if got := ulid.TimestampMillis(id.Zero); got != 0 {
			t.Fatalf("TimestampMillis(Zero): got %d, want 0", got)
		}
	})

	t.Run("decodes big-endian 48-bit prefix", func(t *testing.T) {
		t.Parallel()
		u := idFromBytes(0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC)
		const want uint64 = 0x123456789ABC
		if got := ulid.TimestampMillis(u); got != want {
			t.Fatalf("TimestampMillis: got %d, want %d", got, want)
		}
	})
}

// TestGeneratorZeroAlloc cannot run in parallel —
// testing.AllocsPerRun panics if any other test is running.
//
//nolint:paralleltest // see comment above
func TestGeneratorZeroAlloc(t *testing.T) {
	g := ulid.New(fake.New(time.Now()), seeded.New(rand.Seed(1)))
	if got := testing.AllocsPerRun(100, func() { _ = g.Generate() }); got != 0 {
		t.Fatalf("Generator.Generate: %v allocs/op, want 0", got)
	}

	u := idFromBytes(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16)
	if got := testing.AllocsPerRun(100, func() { _ = ulid.TimestampMillis(u) }); got != 0 {
		t.Fatalf("TimestampMillis: %v allocs/op, want 0", got)
	}
}
