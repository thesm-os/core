// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package clocktest

import (
	"testing"
	"time"

	"go.thesmos.sh/core/clock"
)

// TimerContractAssertions returns the standard assertions every
// [clock.Timer] implementation must pass. Use with
// [AssertTimerContract]:
//
//	clocktest.AssertTimerContract(t, factory,
//	    clocktest.TimerContractAssertions()...,
//	)
//
// The factory must return a fresh pending Timer per call. The
// canonical shape:
//
//	func() clock.Timer { return clockImpl.NewTimer(time.Hour) }
//
// Assertions drive Timer through every state transition using
// Reset(0) — both [hlc.Clock] and [fake.Clock] fire-immediately
// on non-positive durations, so the trick reaches a fired state
// without depending on the underlying fire mechanism (real time
// for hlc, [fake.Clock.Advance] for fake).
func TimerContractAssertions() []TimerOption {
	return []TimerOption{
		// --- Stop ---

		TimerCustom("Stop on pending returns true", func(t *testing.T, tm clock.Timer) {
			if !tm.Stop() {
				t.Fatal("Stop on pending timer must return true")
			}
		}),

		TimerCustom("double Stop returns false", func(t *testing.T, tm clock.Timer) {
			tm.Stop()
			if tm.Stop() {
				t.Fatal("second Stop must return false")
			}
		}),

		TimerCustom("Stop after fire returns false", func(t *testing.T, tm clock.Timer) {
			tm.Reset(0)
			select {
			case <-tm.C():
			case <-time.After(time.Second):
				t.Fatal("Reset(0) timer did not fire")
			}
			if tm.Stop() {
				t.Fatal("Stop on already-fired timer must return false")
			}
		}),

		// --- Reset ---

		TimerCustom("Reset on pending returns true", func(t *testing.T, tm clock.Timer) {
			defer tm.Stop()
			if !tm.Reset(time.Hour) {
				t.Fatal("Reset on pending timer must return true")
			}
		}),

		TimerCustom("Reset after Stop returns false", func(t *testing.T, tm clock.Timer) {
			tm.Stop()
			defer tm.Stop()
			if tm.Reset(time.Hour) {
				t.Fatal("Reset on stopped timer must return false")
			}
		}),

		TimerCustom("Reset after fire returns false", func(t *testing.T, tm clock.Timer) {
			tm.Reset(0)
			select {
			case <-tm.C():
			case <-time.After(time.Second):
				t.Fatal("Reset(0) timer did not fire")
			}
			defer tm.Stop()
			if tm.Reset(time.Hour) {
				t.Fatal("Reset on already-fired timer must return false")
			}
		}),

		TimerCustom("Reset reschedules a pending timer to fire", func(t *testing.T, tm clock.Timer) {
			// Reset(0) reschedules a pending timer to fire
			// immediately; verifies the rescheduled deadline is
			// honoured by both real-time (hlc) and virtual-time
			// (fake, fires immediately on non-positive d) impls.
			if !tm.Reset(0) {
				t.Fatal("Reset on pending must return true")
			}
			select {
			case <-tm.C():
			case <-time.After(time.Second):
				t.Fatal("rescheduled Timer did not fire")
			}
		}),
	}
}
