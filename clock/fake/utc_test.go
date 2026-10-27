// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package fake_test

import (
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/clock/fake"
)

func TestUTC(t *testing.T) {
	t.Parallel()

	t.Run("ReadUTC", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a synchronised reading of origin on a new clock", func(t *testing.T) {
			t.Parallel()
			r, err := fake.New(origin).ReadUTC()
			assert.NoError(t, err, "ReadUTC does not fail")
			assert.Equal(t, r, clock.UTCReading{Time: origin, Synced: true},
				"a new clock reports origin, no error bound and a synchronised clock")
		})

		t.Run("returns the virtual time after Advance", func(t *testing.T) {
			t.Parallel()
			c := fake.New(origin)
			c.Advance(time.Minute)
			r, err := c.ReadUTC()
			assert.NoError(t, err, "ReadUTC does not fail")
			assert.Equal(t, r.Time, origin.Add(time.Minute), "the reading follows the virtual time")
		})

		t.Run("leaves Logical unchanged", func(t *testing.T) {
			t.Parallel()
			c := fake.New(origin)
			before := c.Now()
			_, err := c.ReadUTC()
			assert.NoError(t, err, "ReadUTC does not fail")
			assert.Equal(t, c.Now().Logical, before.Logical+1, "the Now after ReadUTC adds 1 to the Logical before it")
		})
	})

	t.Run("SetUTCError", func(t *testing.T) {
		t.Parallel()

		t.Run("sets the MaxError of the readings", func(t *testing.T) {
			t.Parallel()
			c := fake.New(origin)
			c.SetUTCError(3*time.Millisecond, true)
			r, err := c.ReadUTC()
			assert.NoError(t, err, "ReadUTC does not fail")
			assert.Equal(t, r.MaxError, 3*time.Millisecond, "the reading has the MaxError of SetUTCError")
		})

		t.Run("sets the Synced of the readings", func(t *testing.T) {
			t.Parallel()
			c := fake.New(origin)
			c.SetUTCError(3*time.Millisecond, false)
			r, err := c.ReadUTC()
			assert.NoError(t, err, "ReadUTC does not fail")
			assert.False(t, r.Synced, "the reading has the Synced of SetUTCError")
		})

		t.Run("replaces the setting of an earlier call", func(t *testing.T) {
			t.Parallel()
			c := fake.New(origin)
			c.SetUTCError(3*time.Millisecond, false)
			c.SetUTCError(time.Microsecond, true)
			r, err := c.ReadUTC()
			assert.NoError(t, err, "ReadUTC does not fail")
			assert.True(t, r.Within(time.Microsecond), "the reading has the setting of the later call")
		})
	})
}

// TestUTCAllocs checks that ReadUTC does not allocate. MaxAllocs counts the
// allocations of the whole process, so the test does not run in parallel.
func TestUTCAllocs(t *testing.T) {
	c := fake.New(origin)

	t.Run("ReadUTC", func(t *testing.T) {
		var r clock.UTCReading
		expect.MaxAllocs(t, func() { r, _ = c.ReadUTC() }, 0, "ReadUTC does not allocate")
		assert.True(t, r.Synced, "the test measures a reading of a synchronised clock")
	})
}
