// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package clock_test

import (
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/clock"
)

// at is the time of the readings of the explicit cases.
var at = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

// Generators of the properties over readings.
var (
	// readTimes generates the time of a reading, far enough from the
	// limits of int64 nanoseconds that a bound of errorBounds cannot
	// overflow it.
	readTimes = prop.Integer[int64](-1<<62, 1<<62)

	// errorBounds generates the error bound of a reading.
	errorBounds = prop.Duration(0, time.Hour)
)

func TestUTCReading(t *testing.T) {
	t.Parallel()

	t.Run("Within", func(t *testing.T) {
		t.Parallel()

		t.Run("reports true for a synchronised reading whose bound is at most the limit", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Within must report true for a bound up to the limit", func(c *prop.Case) {
				limit := c.Draw(errorBounds, "limit")
				r := clock.UTCReading{Time: at, MaxError: c.Draw(prop.Duration(0, limit), "maxError"), Synced: true}
				assert.True(c, r.Within(limit), "a bound up to the limit must be within it")
			})
		})

		t.Run("reports true for a bound equal to the limit", func(t *testing.T) {
			t.Parallel()
			r := clock.UTCReading{Time: at, MaxError: time.Millisecond, Synced: true}
			assert.True(t, r.Within(time.Millisecond), "a bound equal to the limit must be within it")
		})

		t.Run("reports false for a bound above the limit", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Within must report false for a bound above the limit", func(c *prop.Case) {
				limit := c.Draw(errorBounds, "limit")
				r := clock.UTCReading{
					Time:     at,
					MaxError: c.Draw(prop.Duration(limit+1, limit+time.Hour), "maxError"),
					Synced:   true,
				}
				assert.False(c, r.Within(limit), "a bound above the limit must not be within it")
			})
		})

		t.Run("reports false for an unsynchronised reading", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Within must report false for every reading that is not synchronised", func(c *prop.Case) {
				r := clock.UTCReading{Time: at, MaxError: c.Draw(errorBounds, "maxError")}
				limit := c.Draw(errorBounds, "limit")
				assert.False(c, r.Within(limit), "an unsynchronised reading must be within no limit")
			})
		})
	})

	t.Run("Earliest", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the time MaxError before Time", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Earliest must be MaxError before Time", func(c *prop.Case) {
				r := clock.UTCReading{
					Time:     time.Unix(0, c.Draw(readTimes, "time")).UTC(),
					MaxError: c.Draw(errorBounds, "maxError"),
					Synced:   true,
				}
				assert.Equal(c, r.Earliest().Add(r.MaxError), r.Time, "Earliest must lie MaxError before Time")
			})
		})
	})

	t.Run("Latest", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the time MaxError after Time", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Latest must be MaxError after Time", func(c *prop.Case) {
				r := clock.UTCReading{
					Time:     time.Unix(0, c.Draw(readTimes, "time")).UTC(),
					MaxError: c.Draw(errorBounds, "maxError"),
					Synced:   true,
				}
				assert.Equal(c, r.Latest().Add(-r.MaxError), r.Time, "Latest must lie MaxError after Time")
			})
		})
	})
}

// TestUTCReadingAllocs checks the allocation contract of each method of
// UTCReading. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
//
//nolint:paralleltest // see above
func TestUTCReadingAllocs(t *testing.T) {
	r := clock.UTCReading{Time: at, MaxError: time.Millisecond, Synced: true}

	t.Run("Within", func(t *testing.T) {
		var got bool
		expect.MaxAllocs(t, func() { got = r.Within(time.Second) }, 0, "Within must not allocate")
		assert.True(t, got, "the test must measure a reading within the limit")
	})

	t.Run("Earliest", func(t *testing.T) {
		var got time.Time
		expect.MaxAllocs(t, func() { got = r.Earliest() }, 0, "Earliest must not allocate")
		assert.Equal(t, got, at.Add(-time.Millisecond), "the test must measure the earliest time")
	})

	t.Run("Latest", func(t *testing.T) {
		var got time.Time
		expect.MaxAllocs(t, func() { got = r.Latest() }, 0, "Latest must not allocate")
		assert.Equal(t, got, at.Add(time.Millisecond), "the test must measure the latest time")
	})
}

// BenchmarkUTCReading reports the cost of each method of UTCReading, and
// fails when a method allocates.
func BenchmarkUTCReading(b *testing.B) {
	r := clock.UTCReading{Time: at, MaxError: time.Millisecond, Synced: true}

	b.Run("Within", func(b *testing.B) {
		var got bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = r.Within(time.Second)
		}

		assert.True(b, got, "the benchmark must measure a reading within the limit")
	})

	b.Run("Earliest", func(b *testing.B) {
		var got time.Time

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = r.Earliest()
		}

		assert.Equal(b, got, at.Add(-time.Millisecond), "the benchmark must measure the earliest time")
	})

	b.Run("Latest", func(b *testing.B) {
		var got time.Time

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = r.Latest()
		}

		assert.Equal(b, got, at.Add(time.Millisecond), "the benchmark must measure the latest time")
	})
}
