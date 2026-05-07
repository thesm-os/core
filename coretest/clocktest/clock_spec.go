// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package clocktest

import (
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/clock"
)

// ClockContractAssertions returns the standard assertions every
// [clock.Clock] implementation must pass. Use with
// [AssertClockContract]:
//
//	coretest.AssertClockContract(t, factory,
//	    coretest.ClockContractAssertions()...,
//	)
func ClockContractAssertions() []ClockOption {
	return []ClockOption{
		// --- Now ---

		ClockCustom("Now is HLC-monotone", func(t *testing.T, c clock.Clock) {
			samples := make([]clock.Instant, 0, 100)
			for range 100 {
				samples = append(samples, c.Now())
			}
			testkit.Sequence(t, samples,
				func(earlier, later clock.Instant) bool { return earlier.HappensBefore(later) },
				"Now must be HLC-monotone — every adjacent pair must satisfy HappensBefore")
		}),

		ClockCustom("Now wall is non-zero", func(t *testing.T, c clock.Clock) {
			testkit.NotEqual(t, c.Now().Wall, int64(0), "Now().Wall must be non-zero")
		}),

		// --- Time ---

		ClockCustom("Time returns UTC", func(t *testing.T, c clock.Clock) {
			// time.Location has unexported fields go-cmp can't
			// traverse; pointer equality is sufficient since
			// time.UTC is a package-level singleton.
			testkit.True(t, c.Time().Location() == time.UTC, "Time() must be UTC-located")
		}),

		ClockCustom("Time is non-zero", func(t *testing.T, c clock.Clock) {
			testkit.False(t, c.Time().IsZero(), "Time() must be non-zero")
		}),

		// --- Update ---

		ClockCustom("Update is causal", func(t *testing.T, c clock.Clock) {
			observed := NewInstantPeer().WithLogical(99).Build()
			got := c.Update(observed)
			testkit.True(t, observed.HappensBefore(got),
				"Update result must be causally after observed instant")
		}),

		ClockCustom("Update preserves local node", func(t *testing.T, c clock.Clock) {
			localNode := c.Now().Node
			got := c.Update(NewInstantPeer().WithLogical(1).Build())
			testkit.Equal(t, got.Node, localNode, "Update must preserve local node ID")
		}),

		ClockCustom("Update advances past future-Wall observation", func(t *testing.T, c clock.Clock) {
			// Complementary branch to "Update is causal":
			// when the observed Wall is in the future, the
			// returned instant must adopt at least that Wall.
			// Without this branch HLC fails its core property
			// of merging clocks across nodes.
			now := c.Now()
			future := NewInstantPeer().
				WithWall(now.Wall + int64(time.Hour)).
				Build()
			got := c.Update(future)
			testkit.True(t, got.Wall >= future.Wall,
				"Update must adopt future-Wall observation")
			testkit.True(t, future.HappensBefore(got),
				"Update result must be causally after a future-Wall observation")
		}),

		// --- Cross-method ---

		ClockCustom("Now and Time bracket consistently", func(t *testing.T, c clock.Clock) {
			// Now().Wall and Time().UnixNano() reflect the
			// same underlying time source. The calls are non-
			// atomic, so allow Now to fall anywhere within a
			// pair of bracketing Time() reads.
			t1 := c.Time().UnixNano()
			now := c.Now()
			t2 := c.Time().UnixNano()
			testkit.True(t, now.Wall >= t1 && now.Wall <= t2,
				"Now().Wall must fall within bracketing Time() reads")
		}),

		// --- NewTimer ---

		ClockCustom("NewTimer zero fires immediately", func(t *testing.T, c clock.Clock) {
			tm := c.NewTimer(0)
			select {
			case <-tm.C():
			case <-time.After(time.Second):
				t.Fatal("zero-duration timer did not fire")
			}
		}),

		ClockCustom("NewTimer negative fires immediately", func(t *testing.T, c clock.Clock) {
			tm := c.NewTimer(-time.Second)
			select {
			case <-tm.C():
			case <-time.After(time.Second):
				t.Fatal("negative-duration timer did not fire")
			}
		}),
	}
}
