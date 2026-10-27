// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package clocktest

import (
	"context"
	"time"

	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/clock/fake"
)

// tickWithin is the time within which a Timer whose deadline has passed sends
// its tick.
const tickWithin = time.Second

// StopOfAPendingTimerReturnsTrue checks that Stop of a pending Timer returns
// true.
//
//dokimi:check Timer.Stop defect=stopReturnsFalse
func StopOfAPendingTimerReturnsTrue(h *TimerHandle) {
	assert.True(h, h.Subject().Stop(), h.Statement())
}

// SecondStopReturnsFalse checks that the second call of Stop returns false.
//
//dokimi:check Timer.Stop defect=stopKeepsAStoppedTimerActive
func SecondStopReturnsFalse(h *TimerHandle) {
	tm := h.Subject()
	tm.Stop()
	assert.False(h, tm.Stop(), h.Statement())
}

// StopAfterTheTickReturnsFalse checks that Stop returns false after the
// receive of the tick of a Reset(0) of the stopped Timer.
//
//dokimi:check Timer.Stop defect=stopKeepsAResetTimerActive
func StopAfterTheTickReturnsFalse(h *TimerHandle) {
	tm := h.Subject()
	tm.Stop()
	tm.Reset(0)
	assert.CompletesWithin(h, tickWithin, receiveTick(tm.C()), "the Timer sends the tick of Reset(0)")
	assert.False(h, tm.Stop(), h.Statement())
}

// ResetOfAPendingTimerReturnsTrue checks that Reset of a pending Timer returns
// true.
//
//dokimi:check Timer.Reset defect=resetReturnsFalse
func ResetOfAPendingTimerReturnsTrue(h *TimerHandle) {
	tm := h.Subject()
	defer tm.Stop()
	assert.True(h, tm.Reset(time.Hour), h.Statement())
}

// ResetAfterStopReturnsFalse checks that Reset of a stopped Timer returns
// false.
//
//dokimi:check Timer.Reset defect=resetKeepsAStoppedTimerActive
func ResetAfterStopReturnsFalse(h *TimerHandle) {
	tm := h.Subject()
	tm.Stop()
	defer tm.Stop()
	assert.False(h, tm.Reset(time.Hour), h.Statement())
}

// ResetAfterTheTickReturnsFalse checks that Reset returns false after the
// receive of the tick of a Reset(0) of the stopped Timer.
//
//dokimi:check Timer.Reset defect=resetKeepsAResetTimerActive
func ResetAfterTheTickReturnsFalse(h *TimerHandle) {
	tm := h.Subject()
	tm.Stop()
	tm.Reset(0)
	assert.CompletesWithin(h, tickWithin, receiveTick(tm.C()), "the Timer sends the tick of Reset(0)")
	defer tm.Stop()
	assert.False(h, tm.Reset(time.Hour), h.Statement())
}

// ResetToZeroSendsATick checks that a pending Timer sends its tick within a
// second of a Reset(0).
//
//dokimi:check Timer.Reset defect=resetKeepsTheDeadline
func ResetToZeroSendsATick(h *TimerHandle) {
	tm := h.Subject()
	tm.Reset(0)
	assert.CompletesWithin(h, tickWithin, receiveTick(tm.C()), h.Statement())
}

// receiveTick returns a function that waits for a tick from c and ignores its
// context. CompletesWithin fails the function when it still waits at the
// deadline. A function that returned when its context ended would pass,
// because CompletesWithin measures its time on the clock of the handle, and no
// check advances that clock. On a Timer that never sends a tick, the function
// waits until the test binary ends.
func receiveTick(c <-chan time.Time) func(context.Context) error {
	return func(context.Context) error {
		<-c

		return nil
	}
}

// stopReturnsFalse returns the planted defect of
// StopOfAPendingTimerReturnsTrue: a Timer whose Stop returns false on every
// call.
func stopReturnsFalse(h *TimerHandle) clock.Timer {
	tm := fake.New(reference).NewTimer(time.Hour)

	return NewTimerStub(h, TimerStubDelegateTo(tm), WithTimerStop(func() bool {
		tm.Stop()

		return false
	}))
}

// stopKeepsAStoppedTimerActive returns the planted defect of
// SecondStopReturnsFalse: a Timer whose Stop returns true for a Timer that a
// Stop stopped.
func stopKeepsAStoppedTimerActive(h *TimerHandle) clock.Timer {
	tm := fake.New(reference).NewTimer(time.Hour)
	var stopped bool

	return NewTimerStub(h, TimerStubDelegateTo(tm),
		WithTimerStop(func() bool {
			active := tm.Stop() || stopped
			stopped = true

			return active
		}),
		WithTimerReset(func(d time.Duration) bool {
			stopped = false

			return tm.Reset(d)
		}))
}

// stopKeepsAResetTimerActive returns the planted defect of
// StopAfterTheTickReturnsFalse: a Timer whose Stop returns true after a
// Reset.
func stopKeepsAResetTimerActive(h *TimerHandle) clock.Timer {
	tm := fake.New(reference).NewTimer(time.Hour)
	var reset bool

	return NewTimerStub(h, TimerStubDelegateTo(tm),
		WithTimerStop(func() bool { return tm.Stop() || reset }),
		WithTimerReset(func(d time.Duration) bool {
			reset = true

			return tm.Reset(d)
		}))
}

// resetReturnsFalse returns the planted defect of
// ResetOfAPendingTimerReturnsTrue: a Timer whose Reset returns false on every
// call.
func resetReturnsFalse(h *TimerHandle) clock.Timer {
	tm := fake.New(reference).NewTimer(time.Hour)

	return NewTimerStub(h, TimerStubDelegateTo(tm), WithTimerReset(func(d time.Duration) bool {
		tm.Reset(d)

		return false
	}))
}

// resetKeepsAStoppedTimerActive returns the planted defect of
// ResetAfterStopReturnsFalse: a Timer whose Reset right after a Stop returns
// true.
func resetKeepsAStoppedTimerActive(h *TimerHandle) clock.Timer {
	tm := fake.New(reference).NewTimer(time.Hour)
	var stopped bool

	return NewTimerStub(h, TimerStubDelegateTo(tm),
		WithTimerStop(func() bool {
			stopped = true

			return tm.Stop()
		}),
		WithTimerReset(func(d time.Duration) bool {
			active := tm.Reset(d) || stopped
			stopped = false

			return active
		}))
}

// resetKeepsAResetTimerActive returns the planted defect of
// ResetAfterTheTickReturnsFalse: a Timer whose second and later calls of Reset
// return true.
func resetKeepsAResetTimerActive(h *TimerHandle) clock.Timer {
	tm := fake.New(reference).NewTimer(time.Hour)
	var reset bool

	return NewTimerStub(h, TimerStubDelegateTo(tm), WithTimerReset(func(d time.Duration) bool {
		active := tm.Reset(d) || reset
		reset = true

		return active
	}))
}

// resetKeepsTheDeadline returns the planted defect of ResetToZeroSendsATick: a
// Timer whose Reset keeps the deadline of an hour until the first Stop.
func resetKeepsTheDeadline(h *TimerHandle) clock.Timer {
	tm := fake.New(reference).NewTimer(time.Hour)
	var stopped bool

	return NewTimerStub(h, TimerStubDelegateTo(tm),
		WithTimerStop(func() bool {
			stopped = true

			return tm.Stop()
		}),
		WithTimerReset(func(d time.Duration) bool {
			if !stopped {
				return tm.Reset(time.Hour)
			}

			return tm.Reset(d)
		}))
}
