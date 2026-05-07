// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package hlc_test

import (
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/clock/hlc"
	"go.thesmos.sh/core/coretest/clocktest"
)

// newPendingTimer returns a fresh pending [clock.Timer] from
// an [hlc.Clock] — the factory shape required by
// [clocktest.AssertTimerContract]. The hour-deadline keeps the
// timer pending until assertions deliberately fire it
// (typically via Reset(0)).
func newPendingTimer() clock.Timer { return hlc.New(0).NewTimer(time.Hour) }

// --- testkit-driven contract layer ---

func TestHLCTimer(t *testing.T) {
	t.Parallel()
	clocktest.AssertTimerContract(t, newPendingTimer, clocktest.TimerContractAssertions()...)
}

// --- HLC-specific tests ---

// TestNewTimer covers the real-time fire mechanism specific to
// hlc.Clock — Timer state transitions are covered by
// [TestHLCTimer].
func TestNewTimer(t *testing.T) {
	t.Parallel()

	t.Run("fires after duration", func(t *testing.T) {
		t.Parallel()
		c := hlc.New(0)
		tm := c.NewTimer(10 * time.Millisecond)
		select {
		case <-tm.C():
		case <-time.After(time.Second):
			t.Fatal("timer did not fire within 1s")
		}
	})

	t.Run("Reset reschedules to a real-time deadline", func(t *testing.T) {
		t.Parallel()
		c := hlc.New(0)
		tm := c.NewTimer(time.Hour)
		testkit.True(t, tm.Reset(10*time.Millisecond), "Reset on active timer must return true")
		select {
		case <-tm.C():
		case <-time.After(time.Second):
			t.Fatal("reset timer did not fire on new deadline")
		}
	})
}
