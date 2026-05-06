// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package hlc_test

import (
	"testing"
	"time"

	"go.thesmos.sh/core/clock/hlc"
)

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

	t.Run("Reset reschedules an active timer", func(t *testing.T) {
		t.Parallel()
		c := hlc.New(0)
		tm := c.NewTimer(time.Hour)
		if !tm.Reset(10 * time.Millisecond) {
			t.Fatal("Reset on active timer must return true")
		}
		select {
		case <-tm.C():
		case <-time.After(time.Second):
			t.Fatal("reset timer did not fire on new deadline")
		}
	})

	t.Run("Stop returns false on already-fired timer", func(t *testing.T) {
		t.Parallel()
		c := hlc.New(0)
		tm := c.NewTimer(time.Millisecond)
		<-tm.C()
		if tm.Stop() {
			t.Fatal("Stop on fired timer must return false")
		}
	})
}
