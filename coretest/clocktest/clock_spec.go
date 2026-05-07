// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package clocktest

import (
	"testing"
	"time"

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
			prev := c.Now()
			for range 100 {
				next := c.Now()
				if !prev.HappensBefore(next) {
					t.Fatalf("non-monotone: %+v then %+v", prev, next)
				}
				prev = next
			}
		}),

		ClockCustom("Now wall is non-zero", func(t *testing.T, c clock.Clock) {
			if got := c.Now().Wall; got == 0 {
				t.Fatal("Now().Wall must be non-zero")
			}
		}),

		// --- Time ---

		ClockCustom("Time returns UTC", func(t *testing.T, c clock.Clock) {
			if got := c.Time().Location(); got != time.UTC {
				t.Fatalf("Location: got %v, want UTC", got)
			}
		}),

		ClockCustom("Time is non-zero", func(t *testing.T, c clock.Clock) {
			if c.Time().IsZero() {
				t.Fatal("Time() must be non-zero")
			}
		}),

		// --- Update ---

		ClockCustom("Update is causal", func(t *testing.T, c clock.Clock) {
			observed := NewInstantPeer().WithLogical(99).Build()
			got := c.Update(observed)
			if !observed.HappensBefore(got) {
				t.Fatalf("Update must return instant causally after observed: got=%+v obs=%+v", got, observed)
			}
		}),

		ClockCustom("Update preserves local node", func(t *testing.T, c clock.Clock) {
			localNode := c.Now().Node
			got := c.Update(NewInstantPeer().WithLogical(1).Build())
			if got.Node != localNode {
				t.Fatalf("Update must preserve local node: got %d, want %d", got.Node, localNode)
			}
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
			if got.Wall < future.Wall {
				t.Fatalf("Update must adopt future-Wall observation: got Wall=%d, observed Wall=%d",
					got.Wall, future.Wall)
			}
			if !future.HappensBefore(got) {
				t.Fatalf("Update result must be causally after a future-Wall observation: got=%+v obs=%+v", got, future)
			}
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
			if now.Wall < t1 || now.Wall > t2 {
				t.Fatalf("Now().Wall %d not bracketed by Time() reads [%d, %d]", now.Wall, t1, t2)
			}
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
