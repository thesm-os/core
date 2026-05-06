// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package ksuid_test

import (
	"testing"
	"time"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/ksuid"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/seeded"
)

func TestGenerator(t *testing.T) {
	t.Parallel()

	t.Run("encodes timestamp offset from KSUID epoch", func(t *testing.T) {
		t.Parallel()
		// 2026-01-01T00:00:00Z
		origin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		clk := fake.New(origin)
		g := ksuid.New(clk, seeded.New(rand.Seed(1)))

		u := g.Generate()
		gotSec := ksuid.TimestampSeconds(u)
		wantSec := origin.Unix()
		if gotSec != wantSec {
			t.Fatalf("TimestampSeconds: got %d, want %d", gotSec, wantSec)
		}
		if u.Size() != id.Size160 {
			t.Fatalf("Size: got %d, want %d", u.Size(), id.Size160)
		}
	})

	t.Run("two IDs at distinct seconds sort in chronological order", func(t *testing.T) {
		t.Parallel()
		origin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		clk := fake.New(origin)
		g := ksuid.New(clk, seeded.New(rand.Seed(1)))

		first := g.Generate()
		clk.Advance(2 * time.Second)
		second := g.Generate()

		if first.Compare(second) >= 0 {
			t.Fatalf("expected first < second; got first=%s second=%s",
				ksuid.Format(first), ksuid.Format(second))
		}
		if ksuid.TimestampSeconds(first) >= ksuid.TimestampSeconds(second) {
			t.Fatal("timestamp not monotonic")
		}
	})

	t.Run("generator is deterministic given fixed clock and seed", func(t *testing.T) {
		t.Parallel()
		origin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		gA := ksuid.New(fake.New(origin), seeded.New(rand.Seed(42)))
		gB := ksuid.New(fake.New(origin), seeded.New(rand.Seed(42)))

		for range 4 {
			if got, want := gA.Generate(), gB.Generate(); got != want {
				t.Fatalf("identical inputs produced different IDs: %v vs %v",
					got, want)
			}
		}
	})

	t.Run("does not produce Zero", func(t *testing.T) {
		t.Parallel()
		origin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		g := ksuid.New(fake.New(origin), seeded.New(rand.Seed(1)))
		if u := g.Generate(); u.IsZero() {
			t.Fatal("Generator.Generate returned Zero")
		}
	})
}

func TestTimestampSeconds(t *testing.T) {
	t.Parallel()

	t.Run("Zero ID returns 0", func(t *testing.T) {
		t.Parallel()
		if got := ksuid.TimestampSeconds(id.Zero); got != 0 {
			t.Fatalf("TimestampSeconds(Zero): got %d, want 0", got)
		}
	})

	t.Run("128-bit ID is too short, returns 0", func(t *testing.T) {
		t.Parallel()
		// Locks the size-boundary check: any ID smaller than
		// Size160 must return 0 because bytes 0..3 alone are
		// not the KSUID timestamp prefix.
		smaller := id.New128([id.Size128]byte{0xFF, 0xFF, 0xFF, 0xFF})
		if got := ksuid.TimestampSeconds(smaller); got != 0 {
			t.Fatalf("TimestampSeconds(128-bit): got %d, want 0", got)
		}
	})

	t.Run("decodes big-endian offset from epoch", func(t *testing.T) {
		t.Parallel()
		// Offset 0 from KSUID epoch = ksuid.Epoch absolute seconds.
		var raw [id.Size160]byte
		u := id.New160(raw)
		if got, want := ksuid.TimestampSeconds(u), ksuid.Epoch; got != want {
			t.Fatalf("zero offset: got %d, want %d", got, want)
		}
	})
}

// TestGeneratorZeroAlloc cannot run in parallel —
// testing.AllocsPerRun panics if any other test is running.
//
//nolint:paralleltest // see comment above
func TestGeneratorZeroAlloc(t *testing.T) {
	origin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	g := ksuid.New(fake.New(origin), seeded.New(rand.Seed(1)))
	if got := testing.AllocsPerRun(100, func() { _ = g.Generate() }); got != 0 {
		t.Fatalf("Generator.Generate: %v allocs/op, want 0", got)
	}

	var raw [id.Size160]byte
	for i := range raw {
		raw[i] = byte(i)
	}
	u := id.New160(raw)
	if got := testing.AllocsPerRun(100, func() { _ = ksuid.TimestampSeconds(u) }); got != 0 {
		t.Fatalf("TimestampSeconds: %v allocs/op, want 0", got)
	}
}
