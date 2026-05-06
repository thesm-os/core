// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package noop_test

import (
	"context"
	"testing"

	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/telemetry/noop"
)

func TestCounter(t *testing.T) {
	t.Parallel()
	c := noop.New().Counter(telemetry.InstrumentSpec{Name: "n"})

	t.Run("Add discards every value without panic", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		c.Add(ctx, 1)
		c.Add(ctx, 100)
		c.Add(ctx, 0)
	})

	t.Run("Add discards negative values rather than panicking", func(t *testing.T) {
		// Locks the documented noop contract: noop discards
		// monotonic-precondition violations because it produces
		// no observable signal regardless. Production-grade
		// implementations panic; noop deliberately does not.
		t.Parallel()
		c.Add(t.Context(), -1)
		c.Add(t.Context(), -1_000_000)
	})

	t.Run("With returns a non-nil bound Counter", func(t *testing.T) {
		t.Parallel()
		bound := c.With([]telemetry.Attr{telemetry.AttrString("k", "v")})
		if bound == nil {
			t.Fatal("With returned nil")
		}
		bound.Add(t.Context(), 1)
	})

	t.Run("With(nil) returns a usable Counter", func(t *testing.T) {
		t.Parallel()
		bound := c.With(nil)
		if bound == nil {
			t.Fatal("With(nil) returned nil")
		}
		bound.Add(t.Context(), 1)
	})
}

// TestCounterZeroAlloc cannot run in parallel —
// testing.AllocsPerRun panics if any other test is running.
// Uses context.Background() rather than t.Context() to keep the
// captured context lifetime independent of test framework
// machinery during the AllocsPerRun loop.
//
//nolint:paralleltest,usetesting // see comment above
func TestCounterZeroAlloc(t *testing.T) {
	c := noop.New().Counter(telemetry.InstrumentSpec{Name: "n"}).
		With([]telemetry.Attr{telemetry.AttrString("k", "v")})
	ctx := context.Background()

	if got := testing.AllocsPerRun(100, func() { c.Add(ctx, 1) }); got != 0 {
		t.Fatalf("Counter.Add: %v allocs/op, want 0", got)
	}
}

func BenchmarkCounterAdd(b *testing.B) {
	c := noop.New().Counter(telemetry.InstrumentSpec{Name: "n"}).
		With([]telemetry.Attr{telemetry.AttrString("k", "v")})
	ctx := b.Context()
	b.ReportAllocs()
	for b.Loop() {
		c.Add(ctx, 1)
	}
}
