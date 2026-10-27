// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package telemetrytest

import (
	"context"
	"math"
	"sync/atomic"

	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/telemetry/noop"
)

// The values that the checks of a Histogram pass to Record.
var (
	// nonNegativeValues are 0 and positive values of a small and a large
	// magnitude.
	nonNegativeValues = []float64{0, 0.5, 1, 1e3, 1e9}

	// negativeValues are negative values of a small and a large magnitude.
	negativeValues = []float64{-0.5, -1, -1e9}
)

// HistogramRecordReturnsForANonNegativeValue checks that Record of 0, 0.5, 1,
// 1e3 and 1e9 returns without a panic.
//
//dokimi:check Histogram.Record defect=histogramRecordPanicsForAHalf
func HistogramRecordReturnsForANonNegativeValue(h *HistogramHandle) {
	hist := h.Subject()
	assert.Total(h, func(value float64) error {
		hist.Record(h.Context(), value)

		return nil
	}, nonNegativeValues, h.Statement())
}

// HistogramRecordReturnsForANegativeValue checks that Record of -0.5, -1 and
// -1e9 returns without a panic.
//
//dokimi:check Histogram.Record defect=histogramRecordPanicsForMinusOne
func HistogramRecordReturnsForANegativeValue(h *HistogramHandle) {
	hist := h.Subject()
	assert.Total(h, func(value float64) error {
		hist.Record(h.Context(), value)

		return nil
	}, negativeValues, h.Statement())
}

// HistogramRecordReturnsForANonFiniteValue checks that Record of NaN and of
// both infinities returns without a panic.
//
//dokimi:check Histogram.Record defect=histogramRecordPanicsForNaN
func HistogramRecordReturnsForANonFiniteValue(h *HistogramHandle) {
	hist := h.Subject()
	assert.Total(h, func(value float64) error {
		hist.Record(h.Context(), value)

		return nil
	}, nonFiniteValues, h.Statement())
}

// HistogramWithReturnsAHistogramForNilAttributes checks that With(nil)
// returns a Histogram whose Record returns without a panic.
//
//dokimi:check Histogram.With defect=histogramWithReturnsNilForNil
func HistogramWithReturnsAHistogramForNilAttributes(h *HistogramHandle) {
	derived := h.Subject().With(nil)
	assert.NotNil(h, derived, h.Statement())
	assert.NotPanics(h, func() { derived.Record(h.Context(), 1) }, h.Statement())
}

// HistogramWithReturnsAHistogramForEmptyAttributes checks that With of an
// empty slice returns a Histogram whose Record returns without a panic.
//
//dokimi:check Histogram.With defect=histogramWithReturnsNilForEmpty
func HistogramWithReturnsAHistogramForEmptyAttributes(h *HistogramHandle) {
	derived := h.Subject().With([]telemetry.Attr{})
	assert.NotNil(h, derived, h.Statement())
	assert.NotPanics(h, func() { derived.Record(h.Context(), 1) }, h.Statement())
}

// HistogramWithOfABoundHistogramReturnsAHistogram checks that With of a
// Histogram that With returned returns a Histogram whose Record returns
// without a panic.
//
//dokimi:check Histogram.With defect=histogramWithOfABoundHistogramReturnsNil
func HistogramWithOfABoundHistogramReturnsAHistogram(h *HistogramHandle) {
	derived := h.Subject().With(firstAttrs).With(secondAttrs)
	assert.NotNil(h, derived, h.Statement())
	assert.NotPanics(h, func() { derived.Record(h.Context(), 42) }, h.Statement())
}

// HistogramSecondReleaseReturns checks that a second Release of a bound
// Histogram returns without a panic.
//
//dokimi:check Histogram.Release defect=histogramSecondReleasePanics
func HistogramSecondReleaseReturns(h *HistogramHandle) {
	bound := h.Subject().With(boundAttrs)
	bound.Release()
	assert.NotPanics(h, bound.Release, h.Statement())
}

// HistogramReleaseLeavesAnUnboundHistogramUsable checks that Release of a
// Histogram that the Reporter returned returns without a panic, and that its
// Record then returns without a panic.
//
//dokimi:check Histogram.Release defect=histogramRecordPanicsAfterRelease
func HistogramReleaseLeavesAnUnboundHistogramUsable(h *HistogramHandle) {
	hist := h.Subject()
	assert.NotPanics(h, hist.Release, h.Statement())
	assert.NotPanics(h, func() { hist.Record(h.Context(), 1) }, h.Statement())
}

// HistogramRecordReturnsAfterRelease checks that Record of a released
// Histogram returns without a panic.
//
//dokimi:check Histogram.Record defect=histogramBoundRecordPanicsAfterRelease
func HistogramRecordReturnsAfterRelease(h *HistogramHandle) {
	bound := h.Subject().With(boundAttrs)
	bound.Release()
	assert.NotPanics(h, func() { bound.Record(h.Context(), 1) }, h.Statement())
}

// HistogramReleaseLeavesTheHistogramOfWithUsable checks that Record of a
// Histogram that the With of a bound Histogram returned returns without a
// panic after the Release of the bound Histogram.
//
//dokimi:check Histogram.Release defect=histogramWithOfABoundHistogramReturnsNil
func HistogramReleaseLeavesTheHistogramOfWithUsable(h *HistogramHandle) {
	parent := h.Subject().With(boundAttrs)
	child := parent.With(secondAttrs)
	parent.Release()
	assert.NotPanics(h, func() { child.Record(h.Context(), 1) }, h.Statement())
}

// HistogramReleaseReturnsWhileRecordRuns checks that Release of a bound
// Histogram returns while 4 goroutines call its Record 100 times each. Under
// the race detector the check also fails on a data race between Release and
// Record.
//
//dokimi:check Histogram.Release defect=histogramReleaseWaitsAfterRecord
func HistogramReleaseReturnsWhileRecordRuns(h *HistogramHandle) {
	bound := h.Subject().With(boundAttrs)
	ctx := h.Context()
	outcomes := releaseWhileEmitting(bound.Release, func() { bound.Record(ctx, 2) })
	for _, o := range outcomes {
		assert.True(h, o.Finished, h.Statement())
	}
}

// histogramRecordPanicsForAHalf returns the planted defect of
// HistogramRecordReturnsForANonNegativeValue: a Histogram whose Record panics
// for 0.5.
func histogramRecordPanicsForAHalf(h *HistogramHandle) telemetry.Histogram {
	return NewHistogramStub(h, HistogramStubDelegateTo(noop.New().Histogram(soundSpec)),
		WithHistogramRecord(func(_ context.Context, value float64) {
			if value == 0.5 {
				panic("telemetrytest: Record panics for 0.5")
			}
		}))
}

// histogramRecordPanicsForMinusOne returns the planted defect of
// HistogramRecordReturnsForANegativeValue: a Histogram whose Record panics for
// -1.
func histogramRecordPanicsForMinusOne(h *HistogramHandle) telemetry.Histogram {
	return NewHistogramStub(h, HistogramStubDelegateTo(noop.New().Histogram(soundSpec)),
		WithHistogramRecord(func(_ context.Context, value float64) {
			if value == -1 {
				panic("telemetrytest: Record panics for -1")
			}
		}))
}

// histogramRecordPanicsForNaN returns the planted defect of
// HistogramRecordReturnsForANonFiniteValue: a Histogram whose Record panics
// for NaN.
func histogramRecordPanicsForNaN(h *HistogramHandle) telemetry.Histogram {
	return NewHistogramStub(h, HistogramStubDelegateTo(noop.New().Histogram(soundSpec)),
		WithHistogramRecord(func(_ context.Context, value float64) {
			if math.IsNaN(value) {
				panic("telemetrytest: Record panics for NaN")
			}
		}))
}

// histogramWithReturnsNilForNil returns the planted defect of
// HistogramWithReturnsAHistogramForNilAttributes: a Histogram whose With(nil)
// returns nil.
func histogramWithReturnsNilForNil(h *HistogramHandle) telemetry.Histogram {
	sound := noop.New().Histogram(soundSpec)

	return NewHistogramStub(h, HistogramStubDelegateTo(sound),
		WithHistogramWith(func(attrs []telemetry.Attr) telemetry.Histogram {
			if attrs == nil {
				return nil
			}

			return sound
		}))
}

// histogramWithReturnsNilForEmpty returns the planted defect of
// HistogramWithReturnsAHistogramForEmptyAttributes: a Histogram whose With of
// an empty slice returns nil.
func histogramWithReturnsNilForEmpty(h *HistogramHandle) telemetry.Histogram {
	sound := noop.New().Histogram(soundSpec)

	return NewHistogramStub(h, HistogramStubDelegateTo(sound),
		WithHistogramWith(func(attrs []telemetry.Attr) telemetry.Histogram {
			if attrs != nil && len(attrs) == 0 {
				return nil
			}

			return sound
		}))
}

// histogramWithOfABoundHistogramReturnsNil returns the planted defect of
// HistogramWithOfABoundHistogramReturnsAHistogram and
// HistogramReleaseLeavesTheHistogramOfWithUsable: a Histogram whose With
// returns a Histogram whose own With returns nil.
func histogramWithOfABoundHistogramReturnsNil(h *HistogramHandle) telemetry.Histogram {
	sound := noop.New().Histogram(soundSpec)
	bound := NewHistogramStub(h, HistogramStubDelegateTo(sound),
		WithHistogramWith(func([]telemetry.Attr) telemetry.Histogram { return nil }))

	return NewHistogramStub(h, HistogramStubDelegateTo(sound),
		WithHistogramWith(func([]telemetry.Attr) telemetry.Histogram { return bound }))
}

// histogramSecondReleasePanics returns the planted defect of
// HistogramSecondReleaseReturns: a Histogram whose With returns a Histogram
// whose second Release panics.
func histogramSecondReleasePanics(h *HistogramHandle) telemetry.Histogram {
	sound := noop.New().Histogram(soundSpec)

	return NewHistogramStub(h, HistogramStubDelegateTo(sound),
		WithHistogramWith(func([]telemetry.Attr) telemetry.Histogram {
			var released bool

			return NewHistogramStub(h, HistogramStubDelegateTo(sound), WithHistogramRelease(func() {
				if released {
					panic("telemetrytest: a second Release panics")
				}
				released = true
			}))
		}))
}

// histogramRecordPanicsAfterRelease returns the planted defect of
// HistogramReleaseLeavesAnUnboundHistogramUsable: a Histogram whose Record
// panics after its own Release.
func histogramRecordPanicsAfterRelease(h *HistogramHandle) telemetry.Histogram {
	var released bool

	return NewHistogramStub(h, HistogramStubDelegateTo(noop.New().Histogram(soundSpec)),
		WithHistogramRelease(func() { released = true }),
		WithHistogramRecord(func(context.Context, float64) {
			if released {
				panic("telemetrytest: Record panics after Release")
			}
		}))
}

// histogramBoundRecordPanicsAfterRelease returns the planted defect of
// HistogramRecordReturnsAfterRelease: a Histogram whose With returns a
// Histogram whose Record of 1 panics after its Release.
func histogramBoundRecordPanicsAfterRelease(h *HistogramHandle) telemetry.Histogram {
	sound := noop.New().Histogram(soundSpec)

	return NewHistogramStub(h, HistogramStubDelegateTo(sound),
		WithHistogramWith(func([]telemetry.Attr) telemetry.Histogram {
			var released atomic.Bool

			return NewHistogramStub(h, HistogramStubDelegateTo(sound),
				WithHistogramRelease(func() { released.Store(true) }),
				WithHistogramRecord(func(_ context.Context, value float64) {
					if released.Load() && value == 1 {
						panic("telemetrytest: Record of 1 on a released Histogram panics")
					}
				}))
		}))
}

// histogramReleaseWaitsAfterRecord returns the planted defect of
// HistogramReleaseReturnsWhileRecordRuns: a Histogram whose With returns a
// Histogram whose Release never returns once its Record has run.
func histogramReleaseWaitsAfterRecord(h *HistogramHandle) telemetry.Histogram {
	sound := noop.New().Histogram(soundSpec)

	return NewHistogramStub(h, HistogramStubDelegateTo(sound),
		WithHistogramWith(func([]telemetry.Attr) telemetry.Histogram {
			var recorded atomic.Bool

			return NewHistogramStub(h, HistogramStubDelegateTo(sound),
				WithHistogramRecord(func(context.Context, float64) { recorded.Store(true) }),
				WithHistogramRelease(func() {
					if recorded.Load() {
						select {}
					}
				}))
		}))
}
