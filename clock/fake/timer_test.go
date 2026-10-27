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

// TestTimerConformsToTimer applies the checks of the suite of clock.Timer to a
// pending Timer of an hour on a Clock at origin.
func TestTimerConformsToTimer(t *testing.T) {
	t.Parallel()

	clocktest.RunTimerSuite(t, func() clock.Timer { return fake.New(origin).NewTimer(time.Hour) })
}

func TestTimer(t *testing.T) {
	t.Parallel()

	t.Run("NewTimer", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a Timer that sends its tick once the virtual time passes its deadline", func(t *testing.T) {
			t.Parallel()
			c := fake.New(origin)
			tm := c.NewTimer(5 * time.Second)
			c.Advance(6 * time.Second)
			assert.Length(t, tm.C(), 1, "the Timer of 5 s sends its tick once Advance passes 6 s")
		})

		t.Run("returns a Timer that sends no tick before its deadline", func(t *testing.T) {
			t.Parallel()
			c := fake.New(origin)
			tm := c.NewTimer(time.Hour)
			c.Advance(time.Second)
			assert.Empty(t, tm.C(), "the Timer of an hour sends no tick after an Advance of a second")
		})
	})

	t.Run("Reset", func(t *testing.T) {
		t.Parallel()

		t.Run("sends the tick at once for a duration of 0", func(t *testing.T) {
			t.Parallel()
			tm := fake.New(origin).NewTimer(time.Hour)
			tm.Reset(0)
			assert.Length(t, tm.C(), 1, "Reset(0) sends the tick at once")
		})

		t.Run("drops a tick that nobody received", func(t *testing.T) {
			t.Parallel()
			c := fake.New(origin)
			tm := c.NewTimer(time.Second)
			c.Advance(2 * time.Second)
			tm.Reset(time.Hour)
			assert.Empty(t, tm.C(), "Reset drops the tick of the earlier deadline")
		})

		t.Run("sends the tick at the deadline of the Reset", func(t *testing.T) {
			t.Parallel()
			c := fake.New(origin)
			tm := c.NewTimer(time.Second)
			c.Advance(2 * time.Second)
			tm.Reset(time.Hour)
			c.Advance(2 * time.Hour)
			assert.Length(t, tm.C(), 1, "the Timer sends the tick of the Reset once Advance passes its deadline")
		})

		t.Run("returns true for a tick that nobody received", func(t *testing.T) {
			t.Parallel()
			c := fake.New(origin)
			tm := c.NewTimer(time.Second)
			c.Advance(2 * time.Second)
			assert.True(t, tm.Reset(time.Hour),
				"Reset of a Timer whose tick nobody received returns true, as on a time.Timer")
		})
	})

	t.Run("Stop", func(t *testing.T) {
		t.Parallel()

		t.Run("returns true for a tick that nobody received", func(t *testing.T) {
			t.Parallel()
			c := fake.New(origin)
			tm := c.NewTimer(time.Second)
			c.Advance(2 * time.Second)
			assert.True(t, tm.Stop(), "Stop of a Timer whose tick nobody received returns true, as on a time.Timer")
		})

		t.Run("drops a tick that nobody received", func(t *testing.T) {
			t.Parallel()
			c := fake.New(origin)
			tm := c.NewTimer(time.Second)
			c.Advance(2 * time.Second)
			tm.Stop()
			assert.Empty(t, tm.C(), "a receive after Stop finds no tick from before Stop")
		})

		t.Run("returns false for a tick that was received", func(t *testing.T) {
			t.Parallel()
			c := fake.New(origin)
			tm := c.NewTimer(time.Second)
			c.Advance(2 * time.Second)
			<-tm.C()
			assert.False(t, tm.Stop(), "Stop of a Timer whose tick was received returns false")
		})

		t.Run("returns false for a second call", func(t *testing.T) {
			t.Parallel()
			tm := fake.New(origin).NewTimer(time.Hour)
			assert.True(t, tm.Stop(), "the first Stop of a pending Timer returns true")
			assert.False(t, tm.Stop(), "the second Stop returns false")
		})

		t.Run("cancels the tick at the deadline of a Reset", func(t *testing.T) {
			t.Parallel()
			c := fake.New(origin)
			tm := c.NewTimer(time.Hour)
			tm.Reset(2 * time.Hour)
			assert.True(t, tm.Stop(), "Stop of a Timer that Reset rescheduled returns true")
			c.Advance(3 * time.Hour)
			assert.Empty(t, tm.C(), "the stopped Timer sends no tick at the deadline of the Reset")
		})
	})
}
