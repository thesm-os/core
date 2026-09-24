// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package clock_test

import (
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/clock"
)

var at = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func TestUTCReading(t *testing.T) {
	t.Parallel()

	t.Run("Within accepts an error bound equal to the limit", func(t *testing.T) {
		t.Parallel()
		r := clock.UTCReading{Time: at, MaxError: time.Millisecond, Synced: true}
		testkit.True(t, r.Within(time.Millisecond), "a bound equal to the limit must be within it")
	})

	t.Run("Within refuses an error bound above the limit", func(t *testing.T) {
		t.Parallel()
		r := clock.UTCReading{Time: at, MaxError: time.Millisecond + time.Nanosecond, Synced: true}
		testkit.False(t, r.Within(time.Millisecond), "a bound above the limit must not be within it")
	})

	t.Run("Within refuses an unsynchronised reading", func(t *testing.T) {
		t.Parallel()
		r := clock.UTCReading{Time: at}
		testkit.False(t, r.Within(time.Hour), "an unsynchronised reading must not be within any limit")
	})

	t.Run("Earliest and Latest bound the reading by MaxError", func(t *testing.T) {
		t.Parallel()
		r := clock.UTCReading{Time: at, MaxError: 250 * time.Microsecond, Synced: true}
		testkit.Equal(t, r.Earliest(), at.Add(-250*time.Microsecond), "Earliest must be Time minus MaxError")
		testkit.Equal(t, r.Latest(), at.Add(250*time.Microsecond), "Latest must be Time plus MaxError")
	})

	t.Run("Earliest and Latest equal Time for a zero bound", func(t *testing.T) {
		t.Parallel()
		r := clock.UTCReading{Time: at, Synced: true}
		testkit.Equal(t, r.Earliest(), at, "Earliest must be Time for a zero bound")
		testkit.Equal(t, r.Latest(), at, "Latest must be Time for a zero bound")
	})
}
