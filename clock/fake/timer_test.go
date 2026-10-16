// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package fake_test

import (
	"testing"
	"time"

	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/coretest/clocktest"
)

// newPendingTimer returns a fresh pending [clock.Timer] from a
// fake.Clock — the factory shape required by
// [clocktest.AssertTimerContract].
func newPendingTimer() clock.Timer { return fake.New(origin).NewTimer(time.Hour) }

// --- testkit-driven contract layer ---

func TestFakeTimer(t *testing.T) {
	t.Parallel()
	clocktest.AssertTimerContract(t, newPendingTimer, clocktest.TimerContractAssertions()...)
}

// --- fake-specific tests ---

// TestNewTimer covers the virtual-time fire mechanism specific
// to fake.Clock — Timer state transitions are covered by
// [TestFakeTimer].
func TestNewTimer(t *testing.T) {
	t.Parallel()

	t.Run("fires when virtual time advances past deadline", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		tm := c.NewTimer(5 * time.Second)
		c.Advance(6 * time.Second)
		//dokimi:lint-skip completes-within: the tests of clock/fake use testkit until they move to assert
		select {
		case <-tm.C():
		case <-time.After(time.Second):
			t.Fatal("timer did not fire after Advance past deadline")
		}
	})

	t.Run("does not fire before deadline", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		tm := c.NewTimer(time.Hour)
		c.Advance(time.Second)
		select {
		case <-tm.C():
			t.Fatal("timer fired before deadline")
		default:
		}
	})

	t.Run("Reset to past deadline fires immediately", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		tm := c.NewTimer(time.Hour)
		tm.Reset(0)
		//dokimi:lint-skip completes-within: the tests of clock/fake use testkit until they move to assert
		select {
		case <-tm.C():
		case <-time.After(time.Second):
			t.Fatal("Reset(0) timer did not fire immediately")
		}
	})

	t.Run("Reset drains undrained prior fire", func(t *testing.T) {
		t.Parallel()
		// When a timer has fired but its channel has not been
		// drained, Reset must clear the buffered tick before
		// scheduling the new deadline. Otherwise the next fire
		// would block on the full channel while holding the
		// clock mutex.
		c := fake.New(origin)
		tm := c.NewTimer(time.Second)
		c.Advance(2 * time.Second) // fires; channel has 1 buffered tick

		tm.Reset(time.Hour) // drains the prior tick

		// The reset must have removed the stale tick.
		select {
		case <-tm.C():
			t.Fatal("Reset did not drain the prior fire — got stale tick")
		default:
		}

		c.Advance(2 * time.Hour)
		//dokimi:lint-skip completes-within: the tests of clock/fake use testkit until they move to assert
		select {
		case <-tm.C():
		case <-time.After(time.Second):
			t.Fatal("Reset+Advance timer did not fire on new deadline")
		}
	})

	t.Run("Reset reports true for a tick that nobody received", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		tm := c.NewTimer(time.Second)
		c.Advance(2 * time.Second)
		assert.True(t, tm.Reset(time.Hour),
			"Reset of a timer whose tick nobody received must report true, as on a time.Timer")
	})

	t.Run("Stop drops a tick that nobody received", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		tm := c.NewTimer(time.Second)
		c.Advance(2 * time.Second)
		assert.True(t, tm.Stop(), "Stop of a timer whose tick nobody received must report true, as on a time.Timer")
		assert.Empty(t, tm.C(), "a receive after Stop must find no tick from before Stop")
	})

	t.Run("Stop reports false for a tick that was received", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		tm := c.NewTimer(time.Second)
		c.Advance(2 * time.Second)
		<-tm.C()
		assert.False(t, tm.Stop(), "Stop of a timer whose tick was received must report false")
	})

	t.Run("Stop reports false for a second call", func(t *testing.T) {
		t.Parallel()
		tm := fake.New(origin).NewTimer(time.Hour)
		assert.True(t, tm.Stop(), "the first Stop of a pending timer must report true")
		assert.False(t, tm.Stop(), "the second Stop must report false")
	})

	t.Run("Stop cancels the tick at the deadline of a Reset", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		tm := c.NewTimer(time.Hour)
		tm.Reset(2 * time.Hour)
		assert.True(t, tm.Stop(), "Stop of a timer that Reset rescheduled must report true")
		c.Advance(3 * time.Hour)
		assert.Empty(t, tm.C(), "the stopped timer must not send a tick at the deadline of the Reset")
	})
}

// TestFireWaiters verifies the partial-firing branch of
// [fake.Clock.Advance]: only waiters whose deadlines fall within
// the new time fire; later waiters stay pending. Internal-path
// coverage; not part of the Timer contract.
func TestFireWaiters(t *testing.T) {
	t.Parallel()

	c := fake.New(origin)
	due := c.NewTimer(time.Second)
	later := c.NewTimer(time.Hour)
	c.Advance(2 * time.Second)

	//dokimi:lint-skip completes-within: the tests of clock/fake use testkit until they move to assert
	select {
	case <-due.C():
	case <-time.After(time.Second):
		t.Fatal("due timer did not fire")
	}
	select {
	case <-later.C():
		t.Fatal("later timer fired prematurely")
	default:
	}
}
