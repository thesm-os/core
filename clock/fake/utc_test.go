// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fake_test

import (
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/clock/fake"
)

// TestReadUTCZeroAlloc enforces the allocation contract of ReadUTC.
// testing.AllocsPerRun reads a process-global malloc counter, so this
// test does not call t.Parallel.
//
//nolint:paralleltest // see comment above
func TestReadUTCZeroAlloc(t *testing.T) {
	c := fake.New(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	t.Run("ReadUTC", func(t *testing.T) {
		testkit.Equal(t, testing.AllocsPerRun(100, func() { _, _ = c.ReadUTC() }), float64(0),
			"ReadUTC must not allocate")
	})
}

func TestReadUTC(t *testing.T) {
	t.Parallel()

	origin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	t.Run("reports a synchronised reading with no error on a new clock", func(t *testing.T) {
		t.Parallel()
		r, err := fake.New(origin).ReadUTC()
		testkit.NoError(t, err, "ReadUTC must not fail")
		testkit.Equal(t, r, clock.UTCReading{Time: origin, Synced: true},
			"a new clock must report its time, no error and synchronised")
	})

	t.Run("reports the virtual time after Advance", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		c.Advance(time.Minute)
		r, err := c.ReadUTC()
		testkit.NoError(t, err, "ReadUTC must not fail")
		testkit.Equal(t, r.Time, origin.Add(time.Minute), "Time must follow the virtual time")
	})

	t.Run("reports what SetUTCError set", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		c.SetUTCError(3*time.Millisecond, false)
		r, err := c.ReadUTC()
		testkit.NoError(t, err, "ReadUTC must not fail")
		testkit.Equal(t, r.MaxError, 3*time.Millisecond, "MaxError must be the value set")
		testkit.False(t, r.Synced, "Synced must be the value set")

		c.SetUTCError(time.Microsecond, true)
		r, err = c.ReadUTC()
		testkit.NoError(t, err, "ReadUTC must not fail")
		testkit.True(t, r.Within(time.Microsecond), "a later SetUTCError must replace the earlier one")
	})

	t.Run("does not advance the logical counter", func(t *testing.T) {
		t.Parallel()
		c := fake.New(origin)
		before := c.Now()
		_, err := c.ReadUTC()
		testkit.NoError(t, err, "ReadUTC must not fail")
		testkit.Equal(t, c.Now().Logical, before.Logical+1, "ReadUTC must leave the logical counter alone")
	})
}
