// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package hlc_test

import (
	"context"
	"testing"
	"time"

	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/clock/hlc"
	"go.thesmos.sh/core/coretest/clocktest"
)

// TestTimerConformsToTimer applies the checks of the suite of clock.Timer to a
// pending Timer of an hour.
func TestTimerConformsToTimer(t *testing.T) {
	t.Parallel()

	clocktest.RunTimerSuite(t, func() clock.Timer { return hlc.New(0).NewTimer(time.Hour) })
}

func TestTimer(t *testing.T) {
	t.Parallel()

	t.Run("NewTimer", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a Timer that sends its tick after its duration", func(t *testing.T) {
			t.Parallel()
			tm := hlc.New(0).NewTimer(10 * time.Millisecond)
			assert.CompletesWithin(t, time.Second, func(context.Context) error {
				<-tm.C()

				return nil
			}, "the Timer of 10 ms sends its tick within a second")
		})
	})

	t.Run("Reset", func(t *testing.T) {
		t.Parallel()

		t.Run("sends the tick at the duration of the Reset", func(t *testing.T) {
			t.Parallel()
			tm := hlc.New(0).NewTimer(time.Hour)
			assert.True(t, tm.Reset(10*time.Millisecond), "Reset of a pending Timer returns true")
			assert.CompletesWithin(t, time.Second, func(context.Context) error {
				<-tm.C()

				return nil
			}, "the Timer that Reset rescheduled to 10 ms sends its tick within a second")
		})
	})
}
