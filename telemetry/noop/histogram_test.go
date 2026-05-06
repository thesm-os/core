// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package noop_test

import (
	"context"
	"testing"

	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/telemetry/noop"
)

func TestHistogram(t *testing.T) {
	t.Parallel()
	h := noop.New().Histogram(telemetry.InstrumentSpec{
		Name:   "n",
		Bounds: []float64{1, 10, 100},
	})

	t.Run("Record discards every value without panic", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		h.Record(ctx, 5.0)
		h.Record(ctx, -1.0)
		h.Record(ctx, 0.0)
	})

	t.Run("With returns a non-nil bound Histogram", func(t *testing.T) {
		t.Parallel()
		bound := h.With([]telemetry.Attr{telemetry.AttrFloat("k", 1.5)})
		if bound == nil {
			t.Fatal("With returned nil")
		}
		bound.Record(t.Context(), 1.0)
	})
}

// TestHistogramZeroAlloc cannot run in parallel —
// testing.AllocsPerRun panics if any other test is running.
// See [TestCounterZeroAlloc] for the context.Background() rationale.
//
//nolint:paralleltest,usetesting // see comment above
func TestHistogramZeroAlloc(t *testing.T) {
	bound := noop.New().Histogram(telemetry.InstrumentSpec{Name: "n"}).
		With([]telemetry.Attr{telemetry.AttrFloat("k", 1.5)})
	ctx := context.Background()

	if got := testing.AllocsPerRun(100, func() { bound.Record(ctx, 1.0) }); got != 0 {
		t.Fatalf("Histogram.Record: %v allocs/op, want 0", got)
	}
}
