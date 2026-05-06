// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package noop_test

import (
	"context"
	"testing"

	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/telemetry/noop"
)

func TestGauge(t *testing.T) {
	t.Parallel()
	g := noop.New().Gauge(telemetry.InstrumentSpec{Name: "n"})

	t.Run("Set discards every value without panic", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		g.Set(ctx, 1.0)
		g.Set(ctx, -1.0)
		g.Set(ctx, 0.0)
	})

	t.Run("Add discards every delta without panic", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		g.Add(ctx, 0.5)
		g.Add(ctx, -0.5)
	})

	t.Run("With returns a non-nil bound Gauge", func(t *testing.T) {
		t.Parallel()
		bound := g.With([]telemetry.Attr{telemetry.AttrInt("k", 1)})
		if bound == nil {
			t.Fatal("With returned nil")
		}
		ctx := t.Context()
		bound.Set(ctx, 1.0)
		bound.Add(ctx, 0.1)
	})
}

// TestGaugeZeroAlloc cannot run in parallel —
// testing.AllocsPerRun panics if any other test is running.
// See [TestCounterZeroAlloc] for the context.Background() rationale.
//
//nolint:paralleltest,usetesting // see comment above
func TestGaugeZeroAlloc(t *testing.T) {
	bound := noop.New().Gauge(telemetry.InstrumentSpec{Name: "n"}).
		With([]telemetry.Attr{telemetry.AttrInt("k", 1)})
	ctx := context.Background()

	t.Run("Set", func(t *testing.T) {
		if got := testing.AllocsPerRun(100, func() { bound.Set(ctx, 1.0) }); got != 0 {
			t.Fatalf("Gauge.Set: %v allocs/op, want 0", got)
		}
	})

	t.Run("Add", func(t *testing.T) {
		if got := testing.AllocsPerRun(100, func() { bound.Add(ctx, 0.1) }); got != 0 {
			t.Fatalf("Gauge.Add: %v allocs/op, want 0", got)
		}
	})
}
