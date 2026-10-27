// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package telemetrytest

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/history"

	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/telemetry/noop"
)

// The emits of a check that releases an instrument while other goroutines
// emit through it.
const (
	// emitters is the number of goroutines that emit through the instrument.
	emitters = 4

	// emits is the number of emits of each of those goroutines.
	emits = 100

	// releaseWithin is the time within which the Release and every emit
	// return.
	releaseWithin = time.Second
)

// The attribute sets that the checks of the instruments bind. The checks only
// read them, so the checks that run in parallel share them.
var (
	// boundAttrs is the attribute set of a bound instrument that a check
	// releases.
	boundAttrs = []telemetry.Attr{telemetry.AttrString("release", "bound")}

	// firstAttrs and secondAttrs are the attribute sets of two calls of With,
	// the second on the instrument that the first returned.
	firstAttrs  = []telemetry.Attr{telemetry.AttrString("k1", "v1")}
	secondAttrs = []telemetry.Attr{telemetry.AttrString("k2", "v2")}
)

// soundSpec is the spec of the no-op instruments that keep the calls of a
// planted defect that do not break its law.
var soundSpec = telemetry.InstrumentSpec{Name: "test.sound"}

// CounterAddReturnsForAPositiveValue checks that Add of 1 returns without a
// panic.
//
//dokimi:check Counter.Add defect=counterAddPanicsForOne
func CounterAddReturnsForAPositiveValue(h *CounterHandle) {
	c := h.Subject()
	assert.NotPanics(h, func() { c.Add(h.Context(), 1) }, h.Statement())
}

// CounterAddReturnsForZero checks that Add of 0 returns without a panic.
//
//dokimi:check Counter.Add defect=counterAddPanicsForZero
func CounterAddReturnsForZero(h *CounterHandle) {
	c := h.Subject()
	assert.NotPanics(h, func() { c.Add(h.Context(), 0) }, h.Statement())
}

// CounterAddReturnsForANegativeValue checks that Add of -1 and Add of
// math.MinInt64 return without a panic.
//
//dokimi:check Counter.Add defect=counterAddPanicsForANegativeValue
func CounterAddReturnsForANegativeValue(h *CounterHandle) {
	c := h.Subject()
	assert.NotPanics(h, func() { c.Add(h.Context(), -1) }, h.Statement())
	assert.NotPanics(h, func() { c.Add(h.Context(), math.MinInt64) }, h.Statement())
}

// CounterWithReturnsACounterForNilAttributes checks that With(nil) returns a
// Counter whose Add returns without a panic.
//
//dokimi:check Counter.With defect=counterWithReturnsNilForNil
func CounterWithReturnsACounterForNilAttributes(h *CounterHandle) {
	derived := h.Subject().With(nil)
	assert.NotNil(h, derived, h.Statement())
	assert.NotPanics(h, func() { derived.Add(h.Context(), 1) }, h.Statement())
}

// CounterWithReturnsACounterForEmptyAttributes checks that With of an empty
// slice returns a Counter whose Add returns without a panic.
//
//dokimi:check Counter.With defect=counterWithReturnsNilForEmpty
func CounterWithReturnsACounterForEmptyAttributes(h *CounterHandle) {
	derived := h.Subject().With([]telemetry.Attr{})
	assert.NotNil(h, derived, h.Statement())
	assert.NotPanics(h, func() { derived.Add(h.Context(), 1) }, h.Statement())
}

// CounterWithOfABoundCounterReturnsACounter checks that With of a Counter that
// With returned returns a Counter whose Add returns without a panic.
//
//dokimi:check Counter.With defect=counterWithOfABoundCounterReturnsNil
func CounterWithOfABoundCounterReturnsACounter(h *CounterHandle) {
	derived := h.Subject().With(firstAttrs).With(secondAttrs)
	assert.NotNil(h, derived, h.Statement())
	assert.NotPanics(h, func() { derived.Add(h.Context(), 1) }, h.Statement())
}

// CounterSecondReleaseReturns checks that a second Release of a bound Counter
// returns without a panic.
//
//dokimi:check Counter.Release defect=counterSecondReleasePanics
func CounterSecondReleaseReturns(h *CounterHandle) {
	bound := h.Subject().With(boundAttrs)
	bound.Release()
	assert.NotPanics(h, bound.Release, h.Statement())
}

// CounterReleaseLeavesAnUnboundCounterUsable checks that Release of a Counter
// that the Reporter returned returns without a panic, and that its Add then
// returns without a panic.
//
//dokimi:check Counter.Release defect=counterAddPanicsAfterRelease
func CounterReleaseLeavesAnUnboundCounterUsable(h *CounterHandle) {
	c := h.Subject()
	assert.NotPanics(h, c.Release, h.Statement())
	assert.NotPanics(h, func() { c.Add(h.Context(), 2) }, h.Statement())
}

// CounterAddReturnsAfterRelease checks that Add of a released Counter returns
// without a panic.
//
//dokimi:check Counter.Add defect=counterBoundAddPanicsAfterRelease
func CounterAddReturnsAfterRelease(h *CounterHandle) {
	bound := h.Subject().With(boundAttrs)
	bound.Release()
	assert.NotPanics(h, func() { bound.Add(h.Context(), 1) }, h.Statement())
}

// CounterReleaseLeavesTheCounterOfWithUsable checks that Add of a Counter that
// the With of a bound Counter returned returns without a panic after the
// Release of the bound Counter.
//
//dokimi:check Counter.Release defect=counterWithOfABoundCounterReturnsNil
func CounterReleaseLeavesTheCounterOfWithUsable(h *CounterHandle) {
	parent := h.Subject().With(boundAttrs)
	child := parent.With(secondAttrs)
	parent.Release()
	assert.NotPanics(h, func() { child.Add(h.Context(), 1) }, h.Statement())
}

// CounterReleaseReturnsWhileAddRuns checks that Release of a bound Counter
// returns while 4 goroutines call its Add 100 times each. Under the race
// detector the check also fails on a data race between Release and Add.
//
//dokimi:check Counter.Release defect=counterReleaseWaitsAfterAdd
func CounterReleaseReturnsWhileAddRuns(h *CounterHandle) {
	bound := h.Subject().With(boundAttrs)
	ctx := h.Context()
	outcomes := releaseWhileEmitting(bound.Release, func() { bound.Add(ctx, 2) })
	for _, o := range outcomes {
		assert.True(h, o.Finished, h.Statement())
	}
}

// releaseWhileEmitting calls release on one goroutine while emitters other
// goroutines each call emit emits times. The release starts after the first
// emit. It returns the outcome of each goroutine once each has returned or
// releaseWithin has passed. An emit passes the value 2, which no planted
// defect of an emit panics for.
func releaseWhileEmitting(release, emit func()) []history.Outcome {
	emitted := make(chan struct{})
	var first sync.Once

	return history.Concurrently(emitters+1, releaseWithin, func(client int) (any, error) {
		if client == emitters {
			<-emitted
			release()

			return client, nil
		}
		for range emits {
			emit()
			first.Do(func() { close(emitted) })
		}

		return client, nil
	})
}

// counterAddPanicsForOne returns the planted defect of
// CounterAddReturnsForAPositiveValue: a Counter whose Add panics for 1.
func counterAddPanicsForOne(h *CounterHandle) telemetry.Counter {
	return NewCounterStub(h, CounterStubDelegateTo(noop.New().Counter(soundSpec)),
		WithCounterAdd(func(_ context.Context, value int64) {
			if value == 1 {
				panic("telemetrytest: Add panics for 1")
			}
		}))
}

// counterAddPanicsForZero returns the planted defect of
// CounterAddReturnsForZero: a Counter whose Add panics for 0.
func counterAddPanicsForZero(h *CounterHandle) telemetry.Counter {
	return NewCounterStub(h, CounterStubDelegateTo(noop.New().Counter(soundSpec)),
		WithCounterAdd(func(_ context.Context, value int64) {
			if value == 0 {
				panic("telemetrytest: Add panics for 0")
			}
		}))
}

// counterAddPanicsForANegativeValue returns the planted defect of
// CounterAddReturnsForANegativeValue: a Counter whose Add panics for a
// negative value.
func counterAddPanicsForANegativeValue(h *CounterHandle) telemetry.Counter {
	return NewCounterStub(h, CounterStubDelegateTo(noop.New().Counter(soundSpec)),
		WithCounterAdd(func(_ context.Context, value int64) {
			if value < 0 {
				panic("telemetrytest: Add panics for a negative value")
			}
		}))
}

// counterWithReturnsNilForNil returns the planted defect of
// CounterWithReturnsACounterForNilAttributes: a Counter whose With(nil)
// returns nil.
func counterWithReturnsNilForNil(h *CounterHandle) telemetry.Counter {
	sound := noop.New().Counter(soundSpec)

	return NewCounterStub(h, CounterStubDelegateTo(sound),
		WithCounterWith(func(attrs []telemetry.Attr) telemetry.Counter {
			if attrs == nil {
				return nil
			}

			return sound
		}))
}

// counterWithReturnsNilForEmpty returns the planted defect of
// CounterWithReturnsACounterForEmptyAttributes: a Counter whose With of an
// empty slice returns nil.
func counterWithReturnsNilForEmpty(h *CounterHandle) telemetry.Counter {
	sound := noop.New().Counter(soundSpec)

	return NewCounterStub(h, CounterStubDelegateTo(sound),
		WithCounterWith(func(attrs []telemetry.Attr) telemetry.Counter {
			if attrs != nil && len(attrs) == 0 {
				return nil
			}

			return sound
		}))
}

// counterWithOfABoundCounterReturnsNil returns the planted defect of
// CounterWithOfABoundCounterReturnsACounter and
// CounterReleaseLeavesTheCounterOfWithUsable: a Counter whose With returns a
// Counter whose own With returns nil.
func counterWithOfABoundCounterReturnsNil(h *CounterHandle) telemetry.Counter {
	sound := noop.New().Counter(soundSpec)
	bound := NewCounterStub(h, CounterStubDelegateTo(sound),
		WithCounterWith(func([]telemetry.Attr) telemetry.Counter { return nil }))

	return NewCounterStub(h, CounterStubDelegateTo(sound),
		WithCounterWith(func([]telemetry.Attr) telemetry.Counter { return bound }))
}

// counterSecondReleasePanics returns the planted defect of
// CounterSecondReleaseReturns: a Counter whose With returns a Counter whose
// second Release panics.
func counterSecondReleasePanics(h *CounterHandle) telemetry.Counter {
	sound := noop.New().Counter(soundSpec)

	return NewCounterStub(h, CounterStubDelegateTo(sound), WithCounterWith(func([]telemetry.Attr) telemetry.Counter {
		var released bool

		return NewCounterStub(h, CounterStubDelegateTo(sound), WithCounterRelease(func() {
			if released {
				panic("telemetrytest: a second Release panics")
			}
			released = true
		}))
	}))
}

// counterAddPanicsAfterRelease returns the planted defect of
// CounterReleaseLeavesAnUnboundCounterUsable: a Counter whose Add panics after
// its own Release.
func counterAddPanicsAfterRelease(h *CounterHandle) telemetry.Counter {
	var released bool

	return NewCounterStub(h, CounterStubDelegateTo(noop.New().Counter(soundSpec)),
		WithCounterRelease(func() { released = true }),
		WithCounterAdd(func(context.Context, int64) {
			if released {
				panic("telemetrytest: Add panics after Release")
			}
		}))
}

// counterBoundAddPanicsAfterRelease returns the planted defect of
// CounterAddReturnsAfterRelease: a Counter whose With returns a Counter whose
// Add of 1 panics after its Release.
func counterBoundAddPanicsAfterRelease(h *CounterHandle) telemetry.Counter {
	sound := noop.New().Counter(soundSpec)

	return NewCounterStub(h, CounterStubDelegateTo(sound), WithCounterWith(func([]telemetry.Attr) telemetry.Counter {
		var released atomic.Bool

		return NewCounterStub(h, CounterStubDelegateTo(sound),
			WithCounterRelease(func() { released.Store(true) }),
			WithCounterAdd(func(_ context.Context, value int64) {
				if released.Load() && value == 1 {
					panic("telemetrytest: Add of 1 to a released Counter panics")
				}
			}))
	}))
}

// counterReleaseWaitsAfterAdd returns the planted defect of
// CounterReleaseReturnsWhileAddRuns: a Counter whose With returns a Counter
// whose Release never returns once its Add has run.
func counterReleaseWaitsAfterAdd(h *CounterHandle) telemetry.Counter {
	sound := noop.New().Counter(soundSpec)

	return NewCounterStub(h, CounterStubDelegateTo(sound), WithCounterWith(func([]telemetry.Attr) telemetry.Counter {
		var added atomic.Bool

		return NewCounterStub(h, CounterStubDelegateTo(sound),
			WithCounterAdd(func(context.Context, int64) { added.Store(true) }),
			WithCounterRelease(func() {
				if added.Load() {
					select {}
				}
			}))
	}))
}
