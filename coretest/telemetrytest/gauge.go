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

// The values that the checks of a Gauge pass to Set and to Add.
var (
	// finiteValues are values of both signs and of a large magnitude.
	finiteValues = []float64{0, 1, -1, 1e9, -1e9}

	// nonFiniteValues are NaN and both infinities.
	nonFiniteValues = []float64{math.NaN(), math.Inf(1), math.Inf(-1)}
)

// GaugeSetReturnsForAFiniteValue checks that Set of 0, 1, -1, 1e9 and -1e9
// returns without a panic.
//
//dokimi:check Gauge.Set defect=gaugeSetPanicsForMinusOne
func GaugeSetReturnsForAFiniteValue(h *GaugeHandle) {
	g := h.Subject()
	assert.Total(h, func(value float64) error {
		g.Set(h.Context(), value)

		return nil
	}, finiteValues, h.Statement())
}

// GaugeAddReturnsForAFiniteDelta checks that Add of 0, 1, -1, 1e9 and -1e9
// returns without a panic.
//
//dokimi:check Gauge.Add defect=gaugeAddPanicsForMinusOne
func GaugeAddReturnsForAFiniteDelta(h *GaugeHandle) {
	g := h.Subject()
	assert.Total(h, func(delta float64) error {
		g.Add(h.Context(), delta)

		return nil
	}, finiteValues, h.Statement())
}

// GaugeSetReturnsForANonFiniteValue checks that Set of NaN and of both
// infinities returns without a panic.
//
//dokimi:check Gauge.Set defect=gaugeSetPanicsForNaN
func GaugeSetReturnsForANonFiniteValue(h *GaugeHandle) {
	g := h.Subject()
	assert.Total(h, func(value float64) error {
		g.Set(h.Context(), value)

		return nil
	}, nonFiniteValues, h.Statement())
}

// GaugeAddReturnsForANonFiniteDelta checks that Add of NaN and of both
// infinities returns without a panic.
//
//dokimi:check Gauge.Add defect=gaugeAddPanicsForNaN
func GaugeAddReturnsForANonFiniteDelta(h *GaugeHandle) {
	g := h.Subject()
	assert.Total(h, func(delta float64) error {
		g.Add(h.Context(), delta)

		return nil
	}, nonFiniteValues, h.Statement())
}

// GaugeWithReturnsAGaugeForNilAttributes checks that With(nil) returns a
// Gauge whose Set returns without a panic.
//
//dokimi:check Gauge.With defect=gaugeWithReturnsNilForNil
func GaugeWithReturnsAGaugeForNilAttributes(h *GaugeHandle) {
	derived := h.Subject().With(nil)
	assert.NotNil(h, derived, h.Statement())
	assert.NotPanics(h, func() { derived.Set(h.Context(), 1) }, h.Statement())
}

// GaugeWithReturnsAGaugeForEmptyAttributes checks that With of an empty slice
// returns a Gauge whose Set returns without a panic.
//
//dokimi:check Gauge.With defect=gaugeWithReturnsNilForEmpty
func GaugeWithReturnsAGaugeForEmptyAttributes(h *GaugeHandle) {
	derived := h.Subject().With([]telemetry.Attr{})
	assert.NotNil(h, derived, h.Statement())
	assert.NotPanics(h, func() { derived.Set(h.Context(), 1) }, h.Statement())
}

// GaugeWithOfABoundGaugeReturnsAGauge checks that With of a Gauge that With
// returned returns a Gauge whose Set and Add return without a panic.
//
//dokimi:check Gauge.With defect=gaugeWithOfABoundGaugeReturnsNil
func GaugeWithOfABoundGaugeReturnsAGauge(h *GaugeHandle) {
	derived := h.Subject().With(firstAttrs).With(secondAttrs)
	assert.NotNil(h, derived, h.Statement())
	assert.NotPanics(h, func() {
		derived.Set(h.Context(), 42)
		derived.Add(h.Context(), 1)
	}, h.Statement())
}

// GaugeSecondReleaseReturns checks that a second Release of a bound Gauge
// returns without a panic.
//
//dokimi:check Gauge.Release defect=gaugeSecondReleasePanics
func GaugeSecondReleaseReturns(h *GaugeHandle) {
	bound := h.Subject().With(boundAttrs)
	bound.Release()
	assert.NotPanics(h, bound.Release, h.Statement())
}

// GaugeReleaseLeavesAnUnboundGaugeUsable checks that Release of a Gauge that
// the Reporter returned returns without a panic, and that its Set then
// returns without a panic.
//
//dokimi:check Gauge.Release defect=gaugeSetPanicsAfterRelease
func GaugeReleaseLeavesAnUnboundGaugeUsable(h *GaugeHandle) {
	g := h.Subject()
	assert.NotPanics(h, g.Release, h.Statement())
	assert.NotPanics(h, func() { g.Set(h.Context(), 1) }, h.Statement())
}

// GaugeSetReturnsAfterRelease checks that Set of a released Gauge returns
// without a panic.
//
//dokimi:check Gauge.Set defect=gaugeBoundSetPanicsAfterRelease
func GaugeSetReturnsAfterRelease(h *GaugeHandle) {
	bound := h.Subject().With(boundAttrs)
	bound.Release()
	assert.NotPanics(h, func() { bound.Set(h.Context(), 1) }, h.Statement())
}

// GaugeAddReturnsAfterRelease checks that Add of a released Gauge returns
// without a panic.
//
//dokimi:check Gauge.Add defect=gaugeBoundAddPanicsAfterRelease
func GaugeAddReturnsAfterRelease(h *GaugeHandle) {
	bound := h.Subject().With(boundAttrs)
	bound.Release()
	assert.NotPanics(h, func() { bound.Add(h.Context(), 1) }, h.Statement())
}

// GaugeReleaseLeavesTheGaugeOfWithUsable checks that Set of a Gauge that the
// With of a bound Gauge returned returns without a panic after the Release of
// the bound Gauge.
//
//dokimi:check Gauge.Release defect=gaugeWithOfABoundGaugeReturnsNil
func GaugeReleaseLeavesTheGaugeOfWithUsable(h *GaugeHandle) {
	parent := h.Subject().With(boundAttrs)
	child := parent.With(secondAttrs)
	parent.Release()
	assert.NotPanics(h, func() { child.Set(h.Context(), 1) }, h.Statement())
}

// GaugeReleaseReturnsWhileSetRuns checks that Release of a bound Gauge
// returns while 4 goroutines call its Set 100 times each. Under the race
// detector the check also fails on a data race between Release and Set.
//
//dokimi:check Gauge.Release defect=gaugeReleaseWaitsAfterSet
func GaugeReleaseReturnsWhileSetRuns(h *GaugeHandle) {
	bound := h.Subject().With(boundAttrs)
	ctx := h.Context()
	outcomes := releaseWhileEmitting(bound.Release, func() { bound.Set(ctx, 2) })
	for _, o := range outcomes {
		assert.True(h, o.Finished, h.Statement())
	}
}

// GaugeReleaseReturnsWhileAddRuns checks that Release of a bound Gauge
// returns while 4 goroutines call its Add 100 times each. Under the race
// detector the check also fails on a data race between Release and Add.
//
//dokimi:check Gauge.Release defect=gaugeReleaseWaitsAfterAdd
func GaugeReleaseReturnsWhileAddRuns(h *GaugeHandle) {
	bound := h.Subject().With(boundAttrs)
	ctx := h.Context()
	outcomes := releaseWhileEmitting(bound.Release, func() { bound.Add(ctx, 2) })
	for _, o := range outcomes {
		assert.True(h, o.Finished, h.Statement())
	}
}

// gaugeSetPanicsForMinusOne returns the planted defect of
// GaugeSetReturnsForAFiniteValue: a Gauge whose Set panics for -1.
func gaugeSetPanicsForMinusOne(h *GaugeHandle) telemetry.Gauge {
	return NewGaugeStub(h, GaugeStubDelegateTo(noop.New().Gauge(soundSpec)),
		WithGaugeSet(func(_ context.Context, value float64) {
			if value == -1 {
				panic("telemetrytest: Set panics for -1")
			}
		}))
}

// gaugeAddPanicsForMinusOne returns the planted defect of
// GaugeAddReturnsForAFiniteDelta: a Gauge whose Add panics for -1.
func gaugeAddPanicsForMinusOne(h *GaugeHandle) telemetry.Gauge {
	return NewGaugeStub(h, GaugeStubDelegateTo(noop.New().Gauge(soundSpec)),
		WithGaugeAdd(func(_ context.Context, delta float64) {
			if delta == -1 {
				panic("telemetrytest: Add panics for -1")
			}
		}))
}

// gaugeSetPanicsForNaN returns the planted defect of
// GaugeSetReturnsForANonFiniteValue: a Gauge whose Set panics for NaN.
func gaugeSetPanicsForNaN(h *GaugeHandle) telemetry.Gauge {
	return NewGaugeStub(h, GaugeStubDelegateTo(noop.New().Gauge(soundSpec)),
		WithGaugeSet(func(_ context.Context, value float64) {
			if math.IsNaN(value) {
				panic("telemetrytest: Set panics for NaN")
			}
		}))
}

// gaugeAddPanicsForNaN returns the planted defect of
// GaugeAddReturnsForANonFiniteDelta: a Gauge whose Add panics for NaN.
func gaugeAddPanicsForNaN(h *GaugeHandle) telemetry.Gauge {
	return NewGaugeStub(h, GaugeStubDelegateTo(noop.New().Gauge(soundSpec)),
		WithGaugeAdd(func(_ context.Context, delta float64) {
			if math.IsNaN(delta) {
				panic("telemetrytest: Add panics for NaN")
			}
		}))
}

// gaugeWithReturnsNilForNil returns the planted defect of
// GaugeWithReturnsAGaugeForNilAttributes: a Gauge whose With(nil) returns nil.
func gaugeWithReturnsNilForNil(h *GaugeHandle) telemetry.Gauge {
	sound := noop.New().Gauge(soundSpec)

	return NewGaugeStub(h, GaugeStubDelegateTo(sound), WithGaugeWith(func(attrs []telemetry.Attr) telemetry.Gauge {
		if attrs == nil {
			return nil
		}

		return sound
	}))
}

// gaugeWithReturnsNilForEmpty returns the planted defect of
// GaugeWithReturnsAGaugeForEmptyAttributes: a Gauge whose With of an empty
// slice returns nil.
func gaugeWithReturnsNilForEmpty(h *GaugeHandle) telemetry.Gauge {
	sound := noop.New().Gauge(soundSpec)

	return NewGaugeStub(h, GaugeStubDelegateTo(sound), WithGaugeWith(func(attrs []telemetry.Attr) telemetry.Gauge {
		if attrs != nil && len(attrs) == 0 {
			return nil
		}

		return sound
	}))
}

// gaugeWithOfABoundGaugeReturnsNil returns the planted defect of
// GaugeWithOfABoundGaugeReturnsAGauge and GaugeReleaseLeavesTheGaugeOfWithUsable:
// a Gauge whose With returns a Gauge whose own With returns nil.
func gaugeWithOfABoundGaugeReturnsNil(h *GaugeHandle) telemetry.Gauge {
	sound := noop.New().Gauge(soundSpec)
	bound := NewGaugeStub(h, GaugeStubDelegateTo(sound),
		WithGaugeWith(func([]telemetry.Attr) telemetry.Gauge { return nil }))

	return NewGaugeStub(h, GaugeStubDelegateTo(sound),
		WithGaugeWith(func([]telemetry.Attr) telemetry.Gauge { return bound }))
}

// gaugeSecondReleasePanics returns the planted defect of
// GaugeSecondReleaseReturns: a Gauge whose With returns a Gauge whose second
// Release panics.
func gaugeSecondReleasePanics(h *GaugeHandle) telemetry.Gauge {
	sound := noop.New().Gauge(soundSpec)

	return NewGaugeStub(h, GaugeStubDelegateTo(sound), WithGaugeWith(func([]telemetry.Attr) telemetry.Gauge {
		var released bool

		return NewGaugeStub(h, GaugeStubDelegateTo(sound), WithGaugeRelease(func() {
			if released {
				panic("telemetrytest: a second Release panics")
			}
			released = true
		}))
	}))
}

// gaugeSetPanicsAfterRelease returns the planted defect of
// GaugeReleaseLeavesAnUnboundGaugeUsable: a Gauge whose Set panics after its
// own Release.
func gaugeSetPanicsAfterRelease(h *GaugeHandle) telemetry.Gauge {
	var released bool

	return NewGaugeStub(h, GaugeStubDelegateTo(noop.New().Gauge(soundSpec)),
		WithGaugeRelease(func() { released = true }),
		WithGaugeSet(func(context.Context, float64) {
			if released {
				panic("telemetrytest: Set panics after Release")
			}
		}))
}

// gaugeBoundSetPanicsAfterRelease returns the planted defect of
// GaugeSetReturnsAfterRelease: a Gauge whose With returns a Gauge whose Set of
// 1 panics after its Release.
func gaugeBoundSetPanicsAfterRelease(h *GaugeHandle) telemetry.Gauge {
	sound := noop.New().Gauge(soundSpec)

	return NewGaugeStub(h, GaugeStubDelegateTo(sound), WithGaugeWith(func([]telemetry.Attr) telemetry.Gauge {
		var released atomic.Bool

		return NewGaugeStub(h, GaugeStubDelegateTo(sound),
			WithGaugeRelease(func() { released.Store(true) }),
			WithGaugeSet(func(_ context.Context, value float64) {
				if released.Load() && value == 1 {
					panic("telemetrytest: Set of 1 on a released Gauge panics")
				}
			}))
	}))
}

// gaugeBoundAddPanicsAfterRelease returns the planted defect of
// GaugeAddReturnsAfterRelease: a Gauge whose With returns a Gauge whose Add of
// 1 panics after its Release.
func gaugeBoundAddPanicsAfterRelease(h *GaugeHandle) telemetry.Gauge {
	sound := noop.New().Gauge(soundSpec)

	return NewGaugeStub(h, GaugeStubDelegateTo(sound), WithGaugeWith(func([]telemetry.Attr) telemetry.Gauge {
		var released atomic.Bool

		return NewGaugeStub(h, GaugeStubDelegateTo(sound),
			WithGaugeRelease(func() { released.Store(true) }),
			WithGaugeAdd(func(_ context.Context, delta float64) {
				if released.Load() && delta == 1 {
					panic("telemetrytest: Add of 1 to a released Gauge panics")
				}
			}))
	}))
}

// gaugeReleaseWaitsAfterSet returns the planted defect of
// GaugeReleaseReturnsWhileSetRuns: a Gauge whose With returns a Gauge whose
// Release never returns once its Set has run.
func gaugeReleaseWaitsAfterSet(h *GaugeHandle) telemetry.Gauge {
	sound := noop.New().Gauge(soundSpec)

	return NewGaugeStub(h, GaugeStubDelegateTo(sound), WithGaugeWith(func([]telemetry.Attr) telemetry.Gauge {
		var set atomic.Bool

		return NewGaugeStub(h, GaugeStubDelegateTo(sound),
			WithGaugeSet(func(context.Context, float64) { set.Store(true) }),
			WithGaugeRelease(func() {
				if set.Load() {
					select {}
				}
			}))
	}))
}

// gaugeReleaseWaitsAfterAdd returns the planted defect of
// GaugeReleaseReturnsWhileAddRuns: a Gauge whose With returns a Gauge whose
// Release never returns once its Add has run.
func gaugeReleaseWaitsAfterAdd(h *GaugeHandle) telemetry.Gauge {
	sound := noop.New().Gauge(soundSpec)

	return NewGaugeStub(h, GaugeStubDelegateTo(sound), WithGaugeWith(func([]telemetry.Attr) telemetry.Gauge {
		var added atomic.Bool

		return NewGaugeStub(h, GaugeStubDelegateTo(sound),
			WithGaugeAdd(func(context.Context, float64) { added.Store(true) }),
			WithGaugeRelease(func() {
				if added.Load() {
					select {}
				}
			}))
	}))
}
