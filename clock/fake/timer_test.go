// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fake_test

import (
	"testing"
	"time"

	"go.thesmos.sh/core/clock/fake"
)

func TestNewTimer(t *testing.T) {
	t.Parallel()

	t.Run("fires when virtual time advances past deadline", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		tm := c.NewTimer(5 * time.Second)
		c.Advance(6 * time.Second)
		select {
		case <-tm.C():
		case <-time.After(time.Second):
			t.Fatal("timer did not fire after Advance past deadline")
		}
	})

	t.Run("non-positive duration fires immediately", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		for _, d := range []time.Duration{0, -time.Second} {
			tm := c.NewTimer(d)
			select {
			case <-tm.C():
			case <-time.After(time.Second):
				t.Fatalf("NewTimer(%v) did not fire immediately", d)
			}
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

	t.Run("Stop returns true once on a pending timer", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		tm := c.NewTimer(time.Hour)
		if !tm.Stop() {
			t.Fatal("first Stop on pending timer must return true")
		}
		if tm.Stop() {
			t.Fatal("second Stop must return false")
		}
	})

	t.Run("Stop returns false on a fired timer", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		tm := c.NewTimer(time.Second)
		c.Advance(2 * time.Second)
		<-tm.C()
		if tm.Stop() {
			t.Fatal("Stop on fired timer must return false")
		}
	})

	t.Run("Reset to past deadline fires immediately", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		tm := c.NewTimer(time.Hour)
		tm.Reset(0)
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
		select {
		case <-tm.C():
		case <-time.After(time.Second):
			t.Fatal("Reset+Advance timer did not fire on new deadline")
		}
	})
}

func TestFireWaiters(t *testing.T) {
	t.Parallel()

	// Verify that an Advance fires only waiters whose deadlines
	// fall within the new time, leaving later waiters pending.
	// This is the partial-firing branch of the internal
	// fireWaiters path.
	c := fake.New(origin)
	due := c.NewTimer(time.Second)
	later := c.NewTimer(time.Hour)
	c.Advance(2 * time.Second)

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
