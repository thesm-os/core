// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package fake_test

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/clock/fake"
)

// origin is the virtual time at which the tests start their Clocks,
// 2026-01-01T00:00:00Z.
var origin = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// newClock returns a Clock at origin. The conformance test of the package runs
// the suite of clock.Clock on it.
func newClock() *fake.Clock {
	return fake.New(origin)
}

func TestClock(t *testing.T) {
	t.Parallel()

	t.Run("Now", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the wall of origin on the first call", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, fake.New(origin).Now().Wall, origin.UnixNano(),
				"the first Instant of Now has the wall of origin")
		})

		t.Run("adds 1 to Logical on each call between advances", func(t *testing.T) {
			t.Parallel()
			c := fake.New(origin)
			first := c.Now()
			assert.Equal(t, c.Now().Logical, first.Logical+1, "the second Now adds 1 to the Logical of the first")
		})

		t.Run("returns instants of the node of NewWithNode", func(t *testing.T) {
			t.Parallel()
			const node = clock.NodeID(7)
			assert.Equal(t, fake.NewWithNode(origin, node).Now().Node, node,
				"Now returns instants of the node of NewWithNode")
		})

		t.Run("moves the virtual time forward by a nanosecond after the last Logical", func(t *testing.T) {
			t.Parallel()
			c := fake.New(origin)
			_, err := c.Update(clock.Instant{Wall: origin.UnixNano(), Logical: math.MaxUint32 - 1, Node: 9})
			assert.NoError(t, err, "Update must merge a peer at the wall of the clock")
			got := c.Now()
			expect.Equal(t, got.Wall, origin.UnixNano()+1, "Now moves the wall forward by 1 ns after the last Logical")
			expect.Equal(t, got.Logical, uint32(1), "Now starts Logical at 1 on the next wall")
		})
	})

	t.Run("Elapsed", func(t *testing.T) {
		t.Parallel()

		t.Run("returns 0 after New", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, fake.New(origin).Elapsed(), time.Duration(0), "Elapsed starts at 0")
		})

		t.Run("adds the duration of each Advance", func(t *testing.T) {
			t.Parallel()
			c := fake.New(origin)
			c.Advance(3 * time.Second)
			c.Advance(2 * time.Second)
			assert.Equal(t, c.Elapsed(), 5*time.Second, "Elapsed returns the sum of the durations of Advance")
		})

		t.Run("returns 0 after an Advance of a negative duration", func(t *testing.T) {
			t.Parallel()
			c := fake.New(origin)
			c.Advance(-time.Second)
			assert.Equal(t, c.Elapsed(), time.Duration(0), "an Advance of a negative duration must not move Elapsed")
		})

		t.Run("does not change on a Set", func(t *testing.T) {
			t.Parallel()
			c := fake.New(origin)
			c.Advance(time.Second)
			c.Set(origin.Add(-time.Hour))
			assert.Equal(t, c.Elapsed(), time.Second, "Set must change the wall time alone")
		})

		t.Run("does not change on an Update of a peer ahead", func(t *testing.T) {
			t.Parallel()
			c := fake.New(origin)
			_, err := c.Update(clock.Instant{Wall: origin.Add(time.Millisecond).UnixNano(), Logical: 1, Node: 9})
			assert.NoError(t, err, "Update must merge a peer a millisecond ahead")
			assert.Equal(t, c.Elapsed(), time.Duration(0), "Update must change the wall time alone")
		})
	})

	t.Run("Time", func(t *testing.T) {
		t.Parallel()

		t.Run("returns origin before an Advance", func(t *testing.T) {
			t.Parallel()
			assert.True(t, fake.New(origin).Time().Equal(origin), "Time returns origin before an Advance")
		})

		t.Run("adds 1 to Logical on each call", func(t *testing.T) {
			t.Parallel()
			c := fake.New(origin)
			c.Time()
			c.Time()
			assert.Equal(t, c.Now().Logical, uint32(3), "the Now after two calls of Time returns Logical 3")
		})
	})

	t.Run("Advance", func(t *testing.T) {
		t.Parallel()

		t.Run("moves the virtual time forward by d", func(t *testing.T) {
			t.Parallel()
			c := fake.New(origin)
			c.Advance(5 * time.Second)
			assert.True(t, c.Time().Equal(origin.Add(5*time.Second)), "Time returns origin plus 5 s")
		})

		t.Run("adds the durations of two calls", func(t *testing.T) {
			t.Parallel()
			c := fake.New(origin)
			c.Advance(3 * time.Second)
			c.Advance(2 * time.Second)
			assert.True(t, c.Time().Equal(origin.Add(5*time.Second)), "Time returns origin plus 3 s plus 2 s")
		})

		tests := []struct {
			name string
			give time.Duration
		}{
			{name: "leaves the clock unchanged for a duration of 0", give: 0},
			{name: "leaves the clock unchanged for a negative duration", give: -time.Second},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				c := fake.New(origin)
				c.Now()
				c.Now()
				c.Advance(tt.give)
				got := c.Now()
				expect.Equal(t, got.Wall, origin.UnixNano(), "Now keeps the wall of origin")
				expect.Equal(t, got.Logical, uint32(3), "Now continues the Logical of the calls before Advance")
			})
		}

		t.Run("restarts Logical when the wall moves", func(t *testing.T) {
			t.Parallel()
			c := fake.New(origin)
			c.Now()
			c.Now()
			c.Advance(time.Second)
			assert.Equal(t, c.Now().Logical, uint32(1), "the first Now after Advance returns Logical 1")
		})

		t.Run("sends the tick of a Timer whose deadline it passes", func(t *testing.T) {
			t.Parallel()
			c := fake.New(origin)
			due := c.NewTimer(time.Second)
			c.Advance(2 * time.Second)
			assert.Length(t, due.C(), 1, "Advance sends the tick of the Timer of a second")
		})

		t.Run("sends no tick of a Timer whose deadline it does not pass", func(t *testing.T) {
			t.Parallel()
			c := fake.New(origin)
			later := c.NewTimer(time.Hour)
			c.Advance(2 * time.Second)
			assert.Empty(t, later.C(), "Advance of 2 s sends no tick of the Timer of an hour")
		})
	})

	t.Run("Set", func(t *testing.T) {
		t.Parallel()

		target := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)

		t.Run("moves the virtual time to t", func(t *testing.T) {
			t.Parallel()
			c := fake.New(origin)
			c.Set(target)
			assert.True(t, c.Time().Equal(target), "Time returns the time of Set")
		})

		t.Run("restarts Logical", func(t *testing.T) {
			t.Parallel()
			c := fake.New(origin)
			c.Now()
			c.Now()
			c.Set(target)
			assert.Equal(t, c.Now().Logical, uint32(1), "the first Now after Set returns Logical 1")
		})

		t.Run("sends the tick of a Timer whose deadline t passes", func(t *testing.T) {
			t.Parallel()
			c := fake.New(origin)
			tm := c.NewTimer(time.Hour)
			c.Set(origin.Add(2 * time.Hour))
			assert.Length(t, tm.C(), 1, "Set sends the tick of the Timer whose deadline it passes")
		})
	})

	t.Run("Update", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the next local Logical for a peer behind the clock", func(t *testing.T) {
			t.Parallel()
			got, err := fake.New(origin).Update(clock.Instant{Wall: 1, Logical: 99, Node: 7})
			assert.NoError(t, err, "Update must merge a peer behind the clock")
			expect.Equal(t, got.Wall, origin.UnixNano(), "Update keeps the wall of a clock ahead of the peer")
			expect.Equal(t, got.Logical, uint32(1), "Update returns the next local Logical, whatever the peer has")
		})

		t.Run("returns the Logical of a peer ahead plus 1", func(t *testing.T) {
			t.Parallel()
			ahead := origin.Add(time.Millisecond).UnixNano()
			got, err := fake.New(origin).Update(clock.Instant{Wall: ahead, Logical: 7, Node: 9})
			assert.NoError(t, err, "Update must merge a peer a millisecond ahead")
			expect.Equal(t, got.Wall, ahead, "Update takes the wall of a peer ahead")
			expect.Equal(t, got.Logical, uint32(8), "Update returns the Logical of the peer plus 1")
		})

		t.Run("returns the larger Logical of a peer at the same wall plus 1", func(t *testing.T) {
			t.Parallel()
			c := fake.New(origin)
			c.Now()
			got, err := c.Update(clock.Instant{Wall: origin.UnixNano(), Logical: 5, Node: 9})
			assert.NoError(t, err, "Update must merge a peer at the wall of the clock")
			assert.Equal(t, got.Logical, uint32(6), "Update returns the Logical 5 of the peer plus 1")
		})

		t.Run("returns the larger local Logical plus 1 for a peer at the same wall", func(t *testing.T) {
			t.Parallel()
			c := fake.New(origin)
			for range 10 {
				c.Now()
			}
			got, err := c.Update(clock.Instant{Wall: origin.UnixNano(), Logical: 3, Node: 9})
			assert.NoError(t, err, "Update must merge a peer at the wall of the clock")
			assert.Equal(t, got.Logical, uint32(11), "Update returns the local Logical 10 plus 1")
		})

		t.Run("moves the virtual time forward by a nanosecond for a peer at the last Logical", func(t *testing.T) {
			t.Parallel()
			peer := clock.Instant{Wall: origin.UnixNano(), Logical: math.MaxUint32, Node: 9}
			got, err := fake.New(origin).Update(peer)
			assert.NoError(t, err, "Update must merge a peer at the wall of the clock")
			expect.Equal(t, got.Wall, origin.UnixNano()+1, "Update moves the wall forward by 1 ns")
			expect.Equal(t, got.Logical, uint32(1), "Update starts Logical at 1 on the next wall")
		})

		t.Run("merges a peer at the max offset ahead", func(t *testing.T) {
			t.Parallel()
			ahead := origin.Add(clock.DefaultMaxOffset).UnixNano()
			got, err := fake.New(origin).Update(clock.Instant{Wall: ahead, Logical: 1, Node: 9})
			assert.NoError(t, err, "Update must merge a peer at the max offset")
			assert.Equal(t, got.Wall, ahead, "Update takes the wall of a peer at the max offset")
		})

		t.Run("merges a peer at the largest wall for a virtual time within the max offset of it", func(t *testing.T) {
			t.Parallel()
			c := fake.New(time.Unix(0, math.MaxInt64-1).UTC())
			got, err := c.Update(clock.Instant{Wall: math.MaxInt64, Logical: 1, Node: 9})
			assert.NoError(t, err, "Update must merge a peer within the max offset of the largest wall")
			assert.Equal(t, got.Wall, int64(math.MaxInt64), "Update takes the largest wall of the peer")
		})

		t.Run("returns ErrOffset for a peer beyond the max offset", func(t *testing.T) {
			t.Parallel()
			beyond := origin.Add(clock.DefaultMaxOffset + time.Nanosecond).UnixNano()
			got, err := fake.New(origin).Update(clock.Instant{Wall: beyond, Logical: 1, Node: 9})
			assert.ErrorIs(t, err, clock.ErrOffset, "Update must refuse a peer beyond the max offset")
			expect.Equal(t, got, clock.Instant{}, "Update must return the zero Instant with ErrOffset")
		})

		t.Run("keeps the clock for a refused peer", func(t *testing.T) {
			t.Parallel()
			c := fake.New(origin)
			c.Now()
			beyond := origin.Add(time.Hour).UnixNano()
			_, err := c.Update(clock.Instant{Wall: beyond, Logical: 7, Node: 9})
			assert.ErrorIs(t, err, clock.ErrOffset, "Update must refuse a peer an hour ahead")
			got := c.Now()
			expect.Equal(t, got.Wall, origin.UnixNano(), "a refused peer must not move the wall")
			expect.Equal(t, got.Logical, uint32(2), "a refused peer must not move the Logical")
		})

		t.Run("releases the clock for the call after it", func(t *testing.T) {
			t.Parallel()
			c := fake.New(origin)
			_, err := c.Update(clock.Instant{Wall: 1, Logical: 1, Node: 7})
			assert.NoError(t, err, "Update must merge a peer behind the clock")
			assert.CompletesWithin(t, time.Second, func(context.Context) error {
				c.Now()

				return nil
			}, "Now after Update does not wait for the mutex of the clock")
		})
	})

	t.Run("AwaitWaiters", func(t *testing.T) {
		t.Parallel()

		t.Run("returns once n goroutines wait on the clock", func(t *testing.T) {
			t.Parallel()
			const n = 3
			c := fake.New(origin)
			var wg sync.WaitGroup
			for range n {
				wg.Go(func() { clock.Sleep(c, time.Hour) })
			}
			// Advance wakes every due waiter before it returns, so the bound
			// of the wait only ends a test in which a sleeper never returns.
			t.Cleanup(func() {
				c.Advance(2 * time.Hour)
				assert.CompletesWithin(t, 5*time.Second, func(context.Context) error {
					wg.Wait()

					return nil
				}, "the sleepers return once Advance passes their deadlines")
			})
			c.AwaitWaiters(n)
		})
	})

	t.Run("Waiters", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give func(c *fake.Clock)
			want int
		}{
			{name: "returns 0 for a clock without timers", give: func(*fake.Clock) {}, want: 0},
			{
				name: "counts each pending timer",
				give: func(c *fake.Clock) {
					c.NewTimer(time.Hour)
					c.NewTimer(time.Minute)
				},
				want: 2,
			},
			{
				name: "does not count a timer of a duration that is not positive",
				give: func(c *fake.Clock) { c.NewTimer(0) },
				want: 0,
			},
			{
				name: "does not count a timer whose deadline Advance passed",
				give: func(c *fake.Clock) {
					c.NewTimer(time.Second)
					c.Advance(2 * time.Second)
				},
				want: 0,
			},
			{
				name: "does not count a timer whose deadline Set passed",
				give: func(c *fake.Clock) {
					c.NewTimer(time.Hour)
					c.Set(origin.Add(2 * time.Hour))
				},
				want: 0,
			},
			{
				name: "does not count a stopped timer",
				give: func(c *fake.Clock) { c.NewTimer(time.Hour).Stop() },
				want: 0,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				c := fake.New(origin)
				tt.give(c)
				assert.Equal(t, c.Waiters(), tt.want, "Waiters must count the pending timers of the clock")
			})
		}

		t.Run("does not count the timer of a Wait whose context ended", func(t *testing.T) {
			t.Parallel()
			c := fake.New(origin)
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			go func() { done <- clock.Wait(ctx, c, time.Hour) }()
			c.AwaitWaiters(1)
			cancel()
			assert.CompletesWithin(t, 5*time.Second, func(context.Context) error {
				return <-done
			}, "Wait must return once its context ends")
			assert.Equal(t, c.Waiters(), 0, "Waiters must not count the timer of a Wait that returned")
		})
	})
}

// TestClockAllocs checks the allocation contract of an Update that refuses its
// peer. MaxAllocs counts the allocations of the whole process, so the test
// does not run in parallel.
func TestClockAllocs(t *testing.T) {
	c := fake.New(origin)
	peer := clock.Instant{Wall: origin.Add(time.Hour).UnixNano(), Logical: 1, Node: 9}

	t.Run("Update", func(t *testing.T) {
		var err error
		expect.MaxAllocs(t, func() { _, err = c.Update(peer) }, 0, "an Update that refuses its peer must not allocate")
		assert.ErrorIs(t, err, clock.ErrOffset, "the test must measure an Update that refuses its peer")
	})
}

// BenchmarkFakeAdvance reports the cost of an Advance of a nanosecond on a
// Clock without waiters.
func BenchmarkFakeAdvance(b *testing.B) {
	c := fake.New(origin)
	b.ReportAllocs()
	for b.Loop() {
		c.Advance(time.Nanosecond)
	}
}
