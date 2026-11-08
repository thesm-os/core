// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package hlc_test

import (
	"math"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/clock/hlc"
	"go.thesmos.sh/core/coretest/clocktest"
	"go.thesmos.sh/core/errs"
)

// maxOffset is the max offset of the clocks of the cases of Update.
const maxOffset = time.Second

// origin is the time of the fake clocks under the clocks of NewOver.
var origin = time.Date(2026, time.November, 8, 0, 0, 0, 0, time.UTC)

// newClock returns a Clock of node 0 on the system clock. The conformance test
// of the package runs the suite of clock.Clock on it.
func newClock() *hlc.Clock {
	return hlc.New(0, clock.DefaultMaxOffset)
}

func TestClock(t *testing.T) {
	t.Parallel()

	t.Run("Now", func(t *testing.T) {
		t.Parallel()

		t.Run("returns instants of the node of New", func(t *testing.T) {
			t.Parallel()
			const (
				node  = clock.NodeID(42)
				calls = 100
			)
			c := hlc.New(node, clock.DefaultMaxOffset)
			nodes := make([]clock.NodeID, 0, calls)
			for range calls {
				nodes = append(nodes, c.Now().Node)
			}
			assert.Equal(t, nodes, slices.Repeat([]clock.NodeID{node}, calls),
				"every Instant of Now has the node of New")
		})

		t.Run("returns Logical 1 after the source advances", func(t *testing.T) {
			t.Parallel()
			now := int64(100)
			c := hlc.NewWithSource(clock.NodeID(1), maxOffset, func() int64 { return now })
			for range 5 {
				c.Now()
			}
			now = 200
			got := c.Now()
			expect.Equal(t, got.Wall, int64(200), "Now takes the new wall of the source")
			expect.Equal(t, got.Logical, uint32(1), "Now starts Logical at 1 on a new wall")
		})

		t.Run("returns the next Logical while the source repeats its wall", func(t *testing.T) {
			t.Parallel()
			c := hlc.NewWithSource(clock.NodeID(1), maxOffset, func() int64 { return 100 })
			first := c.Now()
			second := c.Now()
			expect.Equal(t, second.Wall, first.Wall, "Now keeps the wall that the source repeats")
			expect.Equal(t, second.Logical, first.Logical+1, "Now adds 1 to Logical on a repeated wall")
		})

		t.Run("moves the wall forward by a nanosecond after the last Logical", func(t *testing.T) {
			t.Parallel()
			c := hlc.NewWithSource(clock.NodeID(1), maxOffset, func() int64 { return 100 })
			_, err := c.Update(clock.Instant{Wall: 100, Logical: math.MaxUint32 - 1, Node: 99})
			assert.NoError(t, err, "Update must merge a peer at the wall of the clock")
			got := c.Now()
			expect.Equal(t, got.Wall, int64(101), "Now moves the wall forward by 1 ns after the last Logical")
			expect.Equal(t, got.Logical, uint32(1), "Now starts Logical at 1 on the next wall")
		})

		t.Run("returns the wall of the physical clock of NewOver", func(t *testing.T) {
			t.Parallel()
			f := fake.New(origin)
			c := hlc.NewOver(clock.NodeID(1), maxOffset, f)
			f.Advance(time.Second)
			assert.Equal(t, c.Now().Wall, origin.Add(time.Second).UnixNano(),
				"Now takes the wall of the physical clock")
		})
	})

	t.Run("Elapsed", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a duration that is not negative after New", func(t *testing.T) {
			t.Parallel()
			assert.InRange(t, hlc.New(0, clock.DefaultMaxOffset).Elapsed(), 0, math.MaxInt64,
				"Elapsed must not be negative")
		})

		t.Run("advances by at least the duration of a Timer", func(t *testing.T) {
			t.Parallel()
			c := hlc.New(0, clock.DefaultMaxOffset)
			before := c.Elapsed()
			<-c.NewTimer(time.Millisecond).C()
			assert.InRange(t, c.Elapsed()-before, float64(time.Millisecond), math.MaxInt64,
				"Elapsed must advance by at least the duration of a Timer that fired")
		})

		t.Run("does not move back when the source steps back", func(t *testing.T) {
			t.Parallel()
			var wall atomic.Int64
			wall.Store(int64(time.Hour))
			c := hlc.NewWithSource(clock.NodeID(1), maxOffset, wall.Load)
			before := c.Elapsed()
			wall.Add(-int64(time.Hour))
			assert.InRange(t, c.Elapsed(), float64(before), math.MaxInt64,
				"Elapsed must not follow a step back of the source")
		})

		t.Run("returns the Elapsed of the physical clock of NewOver", func(t *testing.T) {
			t.Parallel()
			f := fake.New(origin)
			c := hlc.NewOver(clock.NodeID(1), maxOffset, f)
			f.Advance(3 * time.Second)
			assert.Equal(t, c.Elapsed(), 3*time.Second, "Elapsed returns the Elapsed of the physical clock")
		})
	})

	t.Run("Update", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name                string
			giveStartWall       int64
			giveStartNows       int
			givePhysicalWall    int64
			giveObservedWall    int64
			giveObservedLogical uint32
			wantWall            int64
			wantLogical         uint32
		}{
			{
				name:                "returns the Logical of a peer ahead plus 1",
				giveStartWall:       100,
				giveStartNows:       5,
				givePhysicalWall:    100,
				giveObservedWall:    200,
				giveObservedLogical: 2,
				wantWall:            200,
				wantLogical:         3,
			},
			{
				name:                "returns the local Logical plus 1 for a peer behind the clock",
				giveStartWall:       200,
				giveStartNows:       2,
				givePhysicalWall:    200,
				giveObservedWall:    100,
				giveObservedLogical: 5,
				wantWall:            200,
				wantLogical:         3,
			},
			{
				name:                "returns the larger Logical plus 1 for a peer at the wall of the clock",
				giveStartWall:       100,
				giveStartNows:       2,
				givePhysicalWall:    100,
				giveObservedWall:    100,
				giveObservedLogical: 5,
				wantWall:            100,
				wantLogical:         6,
			},
			{
				name:                "returns Logical 1 for a source ahead of both walls",
				giveStartWall:       50,
				giveStartNows:       3,
				givePhysicalWall:    300,
				giveObservedWall:    100,
				giveObservedLogical: 5,
				wantWall:            300,
				wantLogical:         1,
			},
			{
				name:                "moves the wall forward by a nanosecond for a peer at the last Logical",
				giveStartWall:       100,
				giveStartNows:       2,
				givePhysicalWall:    100,
				giveObservedWall:    100,
				giveObservedLogical: math.MaxUint32,
				wantWall:            101,
				wantLogical:         1,
			},
			{
				name:                "merges a peer at the max offset ahead of the source",
				giveStartWall:       100,
				giveStartNows:       1,
				givePhysicalWall:    100,
				giveObservedWall:    100 + int64(maxOffset),
				giveObservedLogical: 2,
				wantWall:            100 + int64(maxOffset),
				wantLogical:         3,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				now := tt.giveStartWall
				c := hlc.NewWithSource(clock.NodeID(1), maxOffset, func() int64 { return now })
				for range tt.giveStartNows {
					c.Now()
				}
				now = tt.givePhysicalWall
				peer := clock.Instant{Wall: tt.giveObservedWall, Logical: tt.giveObservedLogical, Node: 99}
				got, err := c.Update(peer)
				assert.NoError(t, err, "Update must merge a peer within the max offset")
				expect.Equal(t, got.Wall, tt.wantWall, "Update returns the wall of the merge")
				expect.Equal(t, got.Logical, tt.wantLogical, "Update returns the Logical of the merge")
				expect.Equal(t, got.Node, clock.NodeID(1), "Update returns the node of the clock")
			})
		}

		t.Run("returns ErrOffset for a peer beyond the max offset", func(t *testing.T) {
			t.Parallel()
			c := hlc.NewWithSource(clock.NodeID(1), maxOffset, func() int64 { return 100 })
			got, err := c.Update(clock.Instant{Wall: 101 + int64(maxOffset), Logical: 1, Node: 99})
			assert.ErrorIs(t, err, clock.ErrOffset, "Update must refuse a peer beyond the max offset")
			expect.Equal(t, errs.Classify(err), errs.Integrity, "ErrOffset must classify as Integrity")
			expect.Equal(t, got, clock.Instant{}, "Update must return the zero Instant with ErrOffset")
		})

		t.Run("keeps the clock for a refused peer", func(t *testing.T) {
			t.Parallel()
			c := hlc.NewWithSource(clock.NodeID(1), maxOffset, func() int64 { return 100 })
			c.Now()
			_, err := c.Update(clock.Instant{Wall: 101 + int64(maxOffset), Logical: 7, Node: 99})
			assert.ErrorIs(t, err, clock.ErrOffset, "Update must refuse a peer beyond the max offset")
			got := c.Now()
			expect.Equal(t, got.Wall, int64(100), "a refused peer must not move the wall")
			expect.Equal(t, got.Logical, uint32(2), "a refused peer must not move the Logical")
		})

		t.Run("returns ErrOffset for a peer ahead of the source for a negative max offset", func(t *testing.T) {
			t.Parallel()
			c := hlc.NewWithSource(clock.NodeID(1), -time.Second, func() int64 { return 100 })
			_, err := c.Update(clock.Instant{Wall: 101, Logical: 1, Node: 99})
			assert.ErrorIs(t, err, clock.ErrOffset, "a negative max offset must refuse every peer ahead")
		})

		t.Run("merges a peer at the largest wall for a source within the max offset of it", func(t *testing.T) {
			t.Parallel()
			c := hlc.NewWithSource(clock.NodeID(1), maxOffset, func() int64 { return math.MaxInt64 - 1 })
			got, err := c.Update(clock.Instant{Wall: math.MaxInt64, Logical: 1, Node: 99})
			assert.NoError(t, err, "Update must merge a peer within the max offset of the largest wall")
			assert.Equal(t, got.Wall, int64(math.MaxInt64), "Update takes the largest wall of the peer")
		})

		t.Run("merges a peer at the source for a negative max offset", func(t *testing.T) {
			t.Parallel()
			c := hlc.NewWithSource(clock.NodeID(1), -time.Second, func() int64 { return 100 })
			got, err := c.Update(clock.Instant{Wall: 100, Logical: 4, Node: 99})
			assert.NoError(t, err, "a negative max offset must merge a peer at the source")
			assert.Equal(t, got.Logical, uint32(5), "Update returns the Logical of the peer plus 1")
		})

		t.Run("returns ErrOffset for a peer beyond the max offset of a physical clock", func(t *testing.T) {
			t.Parallel()
			c := hlc.NewOver(clock.NodeID(1), maxOffset, fake.New(origin))
			_, err := c.Update(clock.Instant{Wall: origin.Add(maxOffset).UnixNano() + 1, Logical: 1, Node: 99})
			assert.ErrorIs(t, err, clock.ErrOffset, "Update compares the peer with the wall of the physical clock")
		})
	})

	t.Run("NewOver", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a Clock that passes the suite of clock.Clock over a fake Clock", func(t *testing.T) {
			t.Parallel()
			clocktest.RunClockSuite(t, func() *hlc.Clock {
				return hlc.NewOver(clock.NodeID(1), clock.DefaultMaxOffset, fake.New(origin))
			})
		})
	})
}

// TestClockAllocs checks the allocation contract of an Update that refuses its
// peer. MaxAllocs counts the allocations of the whole process, so the test
// does not run in parallel.
func TestClockAllocs(t *testing.T) {
	c := hlc.NewWithSource(clock.NodeID(1), maxOffset, func() int64 { return 100 })
	peer := clock.Instant{Wall: 101 + int64(maxOffset), Logical: 1, Node: 99}

	t.Run("Now", func(t *testing.T) {
		over := hlc.NewOver(clock.NodeID(1), maxOffset, fake.New(origin))
		var got clock.Instant
		expect.MaxAllocs(t, func() { got = over.Now() }, 0, "a Now over a fake physical clock must not allocate")
		assert.Equal(t, got.Wall, origin.UnixNano(), "the test must measure a Now over the fake clock")
	})

	t.Run("Update", func(t *testing.T) {
		var err error
		expect.MaxAllocs(t, func() { _, err = c.Update(peer) }, 0, "an Update that refuses its peer must not allocate")
		assert.ErrorIs(t, err, clock.ErrOffset, "the test must measure an Update that refuses its peer")
	})
}
