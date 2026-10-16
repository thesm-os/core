// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package resilience_test

import (
	"cmp"
	"context"
	"math"
	"math/big"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/resilience"
)

// maxBurst is the largest Burst that NewLimiter accepts.
const maxBurst = 4_611_686_018

// nsPerSecond is the number of nanoseconds in a second. A Rate in units per
// second times a time in nanoseconds is nsPerSecond times the units.
const nsPerSecond = int64(time.Second)

// maxWaiting is the most calls that the machine of limiterSteps keeps
// waiting at once.
const maxWaiting = 4

// shareRun is the number of grants in a row to calls of PriorityHigh while
// a call of PriorityNormal waits, after which a Limiter grants the call of
// PriorityNormal.
const shareRun = 9

// mib is the Rate, the Burst and the chunk of the limiter of a disk in the
// case of a flush behind the chunks of compactions.
const mib = 1 << 20

// limiterRates generates the Rate of the machine of limiterSteps. A Rate of
// whole units per nanosecond makes the cost of some sizes a whole number of
// nanoseconds and the cost of others not. Rounded costs of mixed sizes then
// add up to more units than Burst. Any other Rate leaves a remainder on the
// cost of most sizes.
var limiterRates = prop.OneOf(
	prop.SampledFrom[int64](1_000_000_000, 2_000_000_000, 3_000_000_000),
	prop.Integer[int64](1, 3_000_000_000),
)

// limiterContract is the contract of limiterSteps.
const limiterContract = "every call must take its units when an exact model of the bucket, the queues and " +
	"the share grants them"

// timedClock is a fake clock that records every timer that it creates, so a
// case observes how long a head waits, that a call that does not wait
// starts no timer, and that the Limiter stops each timer that it no longer
// reads. Its methods are safe for concurrent use: a mutex guards timers and
// the stops of each timer, and the fake clock guards itself.
type timedClock struct {
	*fake.Clock

	mu sync.Mutex

	// timers contains each timer, in the order of creation.
	timers []*timedTimer
}

// timedTimer is a timer of a timedClock: a timer of the fake clock, with its
// duration and its deadline. stopped reports that a Stop stopped it before
// its deadline, and the mutex of the clock guards it.
type timedTimer struct {
	clock.Timer

	c        *timedClock
	d        time.Duration
	deadline time.Time
	stopped  bool
}

// Stop stops the timer of the fake clock, and records that it stopped the
// timer before its deadline.
func (t *timedTimer) Stop() bool {
	ok := t.Timer.Stop()
	t.c.mu.Lock()
	t.stopped = t.stopped || ok
	t.c.mu.Unlock()

	return ok
}

// NewTimer records a timer of d, and returns it. The timer ends when a case
// advances the clock by d.
func (c *timedClock) NewTimer(d time.Duration) clock.Timer {
	t := &timedTimer{Timer: c.Clock.NewTimer(d), c: c, d: d, deadline: c.Time().Add(d)}
	c.mu.Lock()
	c.timers = append(c.timers, t)
	c.mu.Unlock()

	return t
}

// durations returns the durations of the timers that c created, in the
// order of creation.
func (c *timedClock) durations() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()

	ds := make([]time.Duration, 0, len(c.timers))
	for _, t := range c.timers {
		ds = append(ds, t.d)
	}

	return ds
}

// pending returns the number of the timers of c that no Stop stopped and
// whose deadline is after the time of the clock.
func (c *timedClock) pending() int {
	now := c.Time()
	c.mu.Lock()
	defer c.mu.Unlock()

	n := 0
	for _, t := range c.timers {
		if !t.stopped && now.Before(t.deadline) {
			n++
		}
	}

	return n
}

// lateTimer is a timer of a lateClock. A case delivers its tick with a send
// on c, at a time of its choice.
type lateTimer struct {
	deadline time.Time
	c        chan time.Time
}

// C returns the channel on which a case delivers the tick.
func (t *lateTimer) C() <-chan time.Time {
	return t.c
}

// Reset reports false and changes nothing, because no call of a Limiter
// resets a timer.
func (*lateTimer) Reset(time.Duration) bool {
	return false
}

// Stop reports false and changes nothing. Only a case delivers the tick of a
// lateTimer, so the timer has no schedule to cancel.
func (*lateTimer) Stop() bool {
	return false
}

// lateClock is a fake clock whose timers deliver a tick only when a case
// sends it. A case can then deliver the tick while the time of the clock is
// before the deadline, as a Clock that moved back does. Its methods are safe for
// concurrent use: the fake clock guards itself, and created is a channel.
type lateClock struct {
	*fake.Clock

	// created receives every timer of the clock, in the order of creation.
	created chan *lateTimer
}

// NewTimer returns a timer whose deadline is d after the time of the clock,
// and sends it on created.
func (c *lateClock) NewTimer(d time.Duration) clock.Timer {
	t := &lateTimer{deadline: c.Time().Add(d), c: make(chan time.Time, 1)}
	c.created <- t

	return t
}

// grantLog starts calls of WaitNPriority on goroutines of a synctest bubble,
// and records each call that returns, in the order of the returns.
type grantLog struct {
	l   *resilience.Limiter
	clk clock.Clock

	mu sync.Mutex

	// started is the number of calls that start started.
	started int

	// returns are the calls that returned. mu guards them.
	returns []returned
}

// returned is a call of a grantLog that returned: the number of the call
// in the order of the starts, the time of the clock since originUTC when it
// returned, and its error.
type returned struct {
	call int
	at   time.Duration
	err  error
}

// start calls WaitNPriority(ctx, n, p) on a new goroutine, and returns the
// number of the call. It waits until every other goroutine of the bubble
// blocks, so the call has returned or waits when start returns.
func (g *grantLog) start(ctx context.Context, n int64, p resilience.Priority) int {
	call := g.started
	g.started++
	go func() {
		err := g.l.WaitNPriority(ctx, n, p)

		g.mu.Lock()
		defer g.mu.Unlock()
		g.returns = append(g.returns, returned{call: call, at: g.clk.Time().Sub(originUTC), err: err})
	}()
	synctest.Wait()

	return call
}

// calls returns the calls that returned, ordered by the time of their
// return and then by their number. Calls that return at one time return in
// the order in which their goroutines run.
func (g *grantLog) calls() []returned {
	g.mu.Lock()
	defer g.mu.Unlock()

	return slices.SortedFunc(slices.Values(g.returns), func(a, b returned) int {
		return cmp.Or(cmp.Compare(a.at, b.at), cmp.Compare(a.call, b.call))
	})
}

// limiterCall is one call of a Limiter in the machine of limiterSteps.
// start and end are nanoseconds since originUTC, and end is -1 while the
// call waits. ok reports whether the call returned nil, and returned that
// the machine received the result from done. A call of WaitNPriority has
// the channel of its result and the cancellation of its context.
type limiterCall struct {
	n, start, end int64
	priority      resilience.Priority
	ok, returned  bool

	done   chan error
	cancel context.CancelFunc
}

func TestLimiter(t *testing.T) {
	t.Parallel()

	t.Run("NewLimiter", func(t *testing.T) {
		t.Parallel()

		t.Run("returns ErrConfig for a nil Clock", func(t *testing.T) {
			t.Parallel()
			_, err := resilience.NewLimiter(resilience.LimiterConfig{Rate: 1, Burst: 1})
			expect.ErrorIs(t, err, resilience.ErrConfig, "NewLimiter must refuse the config")
			expect.Equal(t, errs.Classify(err), errs.Invalid, "ErrConfig must classify as Invalid")
		})

		t.Run("returns ErrConfig for a negative Rate", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(rate int64) error {
				cfg := resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: rate, Burst: 1}
				_, err := resilience.NewLimiter(cfg)

				return err
			}, resilience.ErrConfig, "NewLimiter must refuse a negative Rate",
				prop.Using(prop.Integer[int64](math.MinInt64, -1)), prop.Example(int64(-1)))
		})

		t.Run("returns ErrConfig for a Burst below 1 at a positive Rate", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(burst int64) error {
				cfg := resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: 1, Burst: burst}
				_, err := resilience.NewLimiter(cfg)

				return err
			}, resilience.ErrConfig, "NewLimiter must refuse a Burst below 1",
				prop.Using(prop.Integer[int64](math.MinInt64, 0)), prop.Example(int64(0)))
		})

		t.Run("returns ErrConfig for a Burst above 4,611,686,018", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(burst int64) error {
				cfg := resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: 1, Burst: burst}
				_, err := resilience.NewLimiter(cfg)

				return err
			}, resilience.ErrConfig, "NewLimiter must refuse a Burst above the largest",
				prop.Using(prop.Integer[int64](maxBurst+1, math.MaxInt64)), prop.Example(int64(maxBurst+1)))
		})

		t.Run("returns a Limiter without a limit for a Rate of zero", func(t *testing.T) {
			t.Parallel()
			l := newLimiter(t, resilience.LimiterConfig{Clock: fake.New(originUTC)})
			assert.True(t, l.AllowN(math.MaxInt64), "a Rate of zero must admit any number of units")
		})

		t.Run("returns a Limiter whose bucket has Burst units", func(t *testing.T) {
			t.Parallel()
			l := newLimiter(t, resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: 1, Burst: 3})
			assert.True(t, l.AllowN(3), "a new bucket must have Burst units")
			assert.False(t, l.AllowN(1), "a new bucket must have no more than Burst units")
		})

		bursts := []struct {
			name  string
			burst int64
		}{
			{name: "returns a Limiter for a Burst of 1", burst: 1},
			{name: "returns a Limiter for a Burst of 4,611,686,018", burst: maxBurst},
		}
		for _, tt := range bursts {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				l := newLimiter(t, resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: 1, Burst: tt.burst})
				assert.True(t, l.AllowN(tt.burst), "the bucket must have Burst units")
			})
		}
	})

	t.Run("Priority", func(t *testing.T) {
		t.Parallel()

		t.Run("Valid", func(t *testing.T) {
			t.Parallel()

			tests := []struct {
				name string
				give resilience.Priority
				want bool
			}{
				{name: "reports true for PriorityNormal", give: resilience.PriorityNormal, want: true},
				{name: "reports true for PriorityHigh", give: resilience.PriorityHigh, want: true},
				{name: "reports false for a value above PriorityHigh", give: 2, want: false},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					assert.Equal(t, tt.give.Valid(), tt.want, "Valid must accept the two priorities alone")
				})
			}
		})
	})

	t.Run("AllowN", func(t *testing.T) {
		t.Parallel()

		t.Run("reports false for units that the bucket lacks", func(t *testing.T) {
			t.Parallel()
			l := drained(t, fake.New(originUTC))
			assert.False(t, l.AllowN(1), "an empty bucket must not admit a unit")
		})

		t.Run("takes nothing when it reports false", func(t *testing.T) {
			t.Parallel()
			l := newLimiter(t, resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: 10, Burst: 10})
			assert.True(t, l.AllowN(8), "the bucket must admit 8 of its 10 units")
			assert.False(t, l.AllowN(3), "the bucket must not admit 3 of its 2 units")
			assert.True(t, l.AllowN(2), "a refused call must leave the 2 units in the bucket")
		})

		t.Run("adds Rate units per second", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			l := drained(t, c)

			c.Advance(100 * time.Millisecond)
			assert.True(t, l.AllowN(1), "a tenth of a second must add one unit at Rate 10")
			assert.False(t, l.AllowN(1), "a tenth of a second must add one unit alone at Rate 10")

			c.Advance(time.Second)
			assert.True(t, l.AllowN(10), "a second must add Rate units")
		})

		t.Run("admits a unit at the first nanosecond after its exact cost", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			l := newLimiter(t, resilience.LimiterConfig{Clock: c, Rate: 3, Burst: 3})
			assert.True(t, l.AllowN(3), "a full bucket must have Burst units")

			c.Advance(333_333_333 * time.Nanosecond)
			assert.False(t, l.AllowN(1), "333,333,333 ns must add less than a unit at Rate 3")

			c.Advance(time.Nanosecond)
			assert.True(t, l.AllowN(1), "333,333,334 ns must add a unit at Rate 3")
		})

		t.Run("admits Burst units of one unit each after the bucket refills at Rate 3", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			l := newLimiter(t, resilience.LimiterConfig{Clock: c, Rate: 3, Burst: 3})
			assert.True(t, l.AllowN(3), "a full bucket must have Burst units")

			c.Advance(time.Second)
			assert.True(t, l.AllowN(1), "a refilled bucket must admit its first unit")
			assert.True(t, l.AllowN(1), "a refilled bucket must admit its second unit")
			assert.True(t, l.AllowN(1), "a refilled bucket must admit its third unit")
			assert.False(t, l.AllowN(1), "a refilled bucket must have no more than Burst units")
		})

		t.Run("admits no more than Burst units at once at 2 units per nanosecond", func(t *testing.T) {
			t.Parallel()
			l := newLimiter(t, resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: 2_000_000_000, Burst: 3})
			assert.True(t, l.AllowN(2), "a full bucket of 3 units must admit 2")
			assert.False(t, l.AllowN(2), "a bucket with 1 unit left must refuse 2")
			assert.True(t, l.AllowN(1), "a bucket with 1 unit left must admit 1")
		})

		t.Run("adds no more than Burst units", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			l := drained(t, c)

			c.Advance(time.Hour)
			assert.True(t, l.AllowN(10), "a refilled bucket must have Burst units")
			assert.False(t, l.AllowN(1), "a refilled bucket must have no more than Burst units")
		})

		t.Run("adds no units while the clock is behind the last refill", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			l := drained(t, c)

			c.Set(originUTC.Add(-time.Hour))
			assert.False(t, l.AllowN(1), "a clock that moved back must add no unit")

			c.Set(originUTC.Add(time.Second))
			assert.True(t, l.AllowN(10), "a clock past the last refill must add units again")
		})

		t.Run("reports true for an n of zero", func(t *testing.T) {
			t.Parallel()
			l := drained(t, fake.New(originUTC))
			assert.True(t, l.AllowN(0), "an empty bucket must admit zero units")
		})

		t.Run("reports true for an n of zero while a call waits", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			l := drained(t, c)

			got := startWait(bounded(t), l, 5)
			c.AwaitWaiters(1)
			assert.True(t, l.AllowN(0), "a bucket for which a call waits must admit zero units")

			c.Advance(500 * time.Millisecond)
			assert.NoError(t, await(t, got, "WaitN must return"), "the waiting call must take its units")
		})

		t.Run("reports false for a negative n", func(t *testing.T) {
			t.Parallel()
			l := newLimiter(t, resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: 10, Burst: 10})
			prop.False(t, l.AllowN, "a negative number of units must be refused",
				prop.Using(prop.Integer[int64](math.MinInt64, -1)), prop.Example(int64(-1)))
			assert.True(t, l.AllowN(10), "a refused negative call must take nothing")
		})

		t.Run("reports false for an n above Burst", func(t *testing.T) {
			t.Parallel()
			l := newLimiter(t, resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: 10, Burst: 10})
			assert.False(t, l.AllowN(11), "more units than the burst must be refused")
			assert.True(t, l.AllowN(10), "a refused call above the burst must take nothing")
		})

		t.Run("reports false for an n above Burst whose cost wraps an int64", func(t *testing.T) {
			t.Parallel()
			// 18,446,744,074 units of 10^9 nanounits each wrap an int64 to
			// 290,448,384 nanounits, the cost of 29 ms at Rate 10.
			l := newLimiter(t, resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: 10, Burst: 10})
			assert.False(t, l.AllowN(18_446_744_074), "more units than the burst must be refused whatever they cost")
			assert.True(t, l.AllowN(10), "a refused call above the burst must take nothing")
		})

		t.Run("reports false for an n above Burst at a rate above a unit per nanosecond", func(t *testing.T) {
			t.Parallel()
			l := newLimiter(t, resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: 2_000_000_000, Burst: 1})
			assert.False(t, l.AllowN(2), "more units than the burst must be refused whatever they cost")
		})

		t.Run("reports false for a negative n with a Rate of zero", func(t *testing.T) {
			t.Parallel()
			l := newLimiter(t, resilience.LimiterConfig{Clock: fake.New(originUTC)})
			assert.False(t, l.AllowN(-1), "a negative number of units must be refused without a limit")
		})

		t.Run("reports true for any n of the zero Limiter", func(t *testing.T) {
			t.Parallel()
			var l resilience.Limiter
			assert.True(t, l.AllowN(math.MaxInt64), "the zero Limiter must admit any number of units")
		})

		t.Run("reports false while a call waits", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			l := drained(t, c)

			got := startWait(bounded(t), l, 5)
			c.AwaitWaiters(1)
			c.Advance(100 * time.Millisecond)
			assert.False(t, l.AllowN(1), "the unit that the bucket gained must go to the waiting call")

			c.Advance(400 * time.Millisecond)
			assert.NoError(t, await(t, got, "WaitN must return"), "the waiting call must take its units")
			assert.False(t, l.AllowN(1), "the units must go to the waiting call")

			c.Advance(100 * time.Millisecond)
			assert.True(t, l.AllowN(1), "the bucket must add units after the waiting call took its units")
		})

		t.Run("reports false for units whose time passes the largest Duration", func(t *testing.T) {
			t.Parallel()
			// Two costs of the largest Burst at Rate 1 fall 0.86 s short of the
			// largest Duration, so the time of the second call passes it 1 s
			// after the start.
			c := fake.New(originUTC)
			l := newLimiter(t, resilience.LimiterConfig{Clock: c, Rate: 1, Burst: maxBurst})
			c.Advance(time.Second)
			assert.True(t, l.AllowN(maxBurst), "the largest bucket must have Burst units")
			assert.False(t, l.AllowN(maxBurst), "units beyond the largest Duration must be refused")
		})

		t.Run("admits at most the units of the bucket across concurrent callers", func(t *testing.T) {
			t.Parallel()
			l := newLimiter(t, resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: 10, Burst: 10})

			outcomes := history.Concurrently(64, patience, func(int) (any, error) { return l.AllowN(1), nil })

			admitted := 0
			for _, o := range outcomes {
				assert.True(t, o.Finished, "every caller must return")
				if o.Output == true {
					admitted++
				}
			}

			assert.Equal(t, admitted, 10, "concurrent calls must share the 10 units of the bucket")
		})
	})

	t.Run("WaitN", func(t *testing.T) {
		t.Parallel()

		t.Run("returns nil at once for units that the bucket has", func(t *testing.T) {
			t.Parallel()
			c := &timedClock{Clock: fake.New(originUTC)}
			l := newLimiter(t, resilience.LimiterConfig{Clock: c, Rate: 10, Burst: 10})

			assert.NoError(t, l.WaitN(bounded(t), 10), "a full bucket must admit Burst units")
			expect.Empty(t, c.durations(), "a call that does not wait must start no timer")
			expect.False(t, l.AllowN(1), "WaitN must take the units")
		})

		t.Run("starts no timer for the exact units that the bucket has", func(t *testing.T) {
			t.Parallel()
			c := &timedClock{Clock: fake.New(originUTC)}
			l := newLimiter(t, resilience.LimiterConfig{Clock: c, Rate: 10, Burst: 10})
			assert.True(t, l.AllowN(5), "the bucket must admit 5 of its 10 units")

			assert.NoError(t, l.WaitN(bounded(t), 5), "the bucket must admit its last 5 units")
			assert.Empty(t, c.durations(), "a call for the units left must start no timer")
		})

		t.Run("waits for the time in which the bucket adds the units", func(t *testing.T) {
			t.Parallel()
			c := &timedClock{Clock: fake.New(originUTC)}
			l := drained(t, c)

			got := startWait(bounded(t), l, 5)
			c.AwaitWaiters(1)
			assert.Equal(t, c.durations(), []time.Duration{500 * time.Millisecond},
				"5 units at Rate 10 must take half a second")

			c.Advance(500 * time.Millisecond)
			assert.NoError(t, await(t, got, "WaitN must return"),
				"the call must take its units once the bucket added them")
		})

		t.Run("waits for the units that the bucket lacks alone", func(t *testing.T) {
			t.Parallel()
			c := &timedClock{Clock: fake.New(originUTC)}
			l := drained(t, c)

			c.Advance(200 * time.Millisecond)
			got := startWait(bounded(t), l, 5)
			c.AwaitWaiters(1)
			assert.Equal(t, c.durations(), []time.Duration{300 * time.Millisecond},
				"a bucket with 2 units must wait 300 ms for 5 units at Rate 10")

			c.Advance(300 * time.Millisecond)
			assert.NoError(t, await(t, got, "WaitN must return"),
				"the call must take its units once the bucket added them")
		})

		t.Run("grants waiting calls in the order in which they started", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				c := &timedClock{Clock: fake.New(originUTC)}
				l := drained(t, c)
				first := startWait(t.Context(), l, 5)
				synctest.Wait()
				second := startWait(t.Context(), l, 5)
				synctest.Wait()
				assert.Equal(t, c.durations(), []time.Duration{500 * time.Millisecond},
					"only the head must wait on a timer")

				c.Advance(500 * time.Millisecond)
				synctest.Wait()
				assert.Length(t, first, 1, "the first call must take its units first")
				assert.Empty(t, second, "the second call must wait behind the first")
				assert.Equal(t, c.durations(), []time.Duration{500 * time.Millisecond, 500 * time.Millisecond},
					"the second call must wait on a timer once it is the head")

				c.Advance(500 * time.Millisecond)
				synctest.Wait()
				assert.NoError(t, <-first, "the first call must take its units")
				assert.NoError(t, <-second, "the second call must take its units after the first")
			})
		})

		t.Run("waits behind a call of PriorityHigh that starts after it", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				c := fake.New(originUTC)
				l := drained(t, c)
				g := &grantLog{l: l, clk: c}
				normal := startWait(t.Context(), l, 5)
				synctest.Wait()
				high := g.start(t.Context(), 5, resilience.PriorityHigh)

				c.Advance(500 * time.Millisecond)
				synctest.Wait()
				assert.Equal(t, g.calls(), []returned{{call: high, at: 500 * time.Millisecond}},
					"the call of PriorityHigh must take its units first")
				assert.Empty(t, normal, "WaitN must wait at PriorityNormal")

				c.Advance(500 * time.Millisecond)
				synctest.Wait()
				assert.NoError(t, <-normal, "WaitN must take its units after the call of PriorityHigh")
			})
		})

		t.Run("returns the error of a context that ends during the wait", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			l := drained(t, c)

			ctx, cancel := context.WithCancel(bounded(t))
			got := startWait(ctx, l, 5)
			c.AwaitWaiters(1)
			cancel()

			assert.ErrorIs(t, await(t, got, "WaitN must return"), context.Canceled,
				"a cancelled wait must return the error of the context")
		})

		t.Run("takes no units for a call whose context ends during the wait", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			l := drained(t, c)

			ctx, cancel := context.WithCancel(bounded(t))
			got := startWait(ctx, l, 5)
			c.AwaitWaiters(1)
			cancel()
			assert.ErrorIs(t, await(t, got, "WaitN must return"), context.Canceled,
				"a cancelled wait must return the error of the context")

			c.Advance(time.Second)
			assert.True(t, l.AllowN(10), "a cancelled wait must leave the bucket its 10 units")
		})

		ended := []struct {
			name string
			give resilience.LimiterConfig
		}{
			{
				name: "returns the error of a context that ended before the call",
				give: resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: 10, Burst: 10},
			},
			{
				name: "returns the error of a context that ended before the call with a Rate of zero",
				give: resilience.LimiterConfig{Clock: fake.New(originUTC)},
			},
		}
		for _, tt := range ended {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				l := newLimiter(t, tt.give)
				assert.HonoursCancellation(t, func(ctx context.Context) error { return l.WaitN(ctx, 1) },
					"an ended context must stop the call")
			})
		}

		t.Run("takes nothing for a context that ended before the call", func(t *testing.T) {
			t.Parallel()
			l := newLimiter(t, resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: 10, Burst: 10})
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			_ = l.WaitN(ctx, 1)
			assert.True(t, l.AllowN(10), "a call with an ended context must take nothing")
		})

		t.Run("returns ErrUnits for a negative n", func(t *testing.T) {
			t.Parallel()
			l := newLimiter(t, resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: 10, Burst: 10})

			err := l.WaitN(bounded(t), -1)
			expect.ErrorIs(t, err, resilience.ErrUnits, "a negative number of units must be refused")
			expect.Equal(t, errs.Classify(err), errs.Invalid, "ErrUnits must classify as Invalid")
		})

		t.Run("returns ErrUnits for an n above Burst", func(t *testing.T) {
			t.Parallel()
			l := newLimiter(t, resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: 10, Burst: 10})

			assert.ErrorIs(t, l.WaitN(bounded(t), 11), resilience.ErrUnits, "more units than the burst must be refused")
			assert.True(t, l.AllowN(10), "a refused call must take nothing")
		})

		t.Run("returns ErrUnits for a negative n with a Rate of zero", func(t *testing.T) {
			t.Parallel()
			l := newLimiter(t, resilience.LimiterConfig{Clock: fake.New(originUTC)})
			assert.ErrorIs(t, l.WaitN(bounded(t), -1), resilience.ErrUnits,
				"a negative number of units must be refused without a limit")
		})

		t.Run("returns nil at once with a Rate of zero", func(t *testing.T) {
			t.Parallel()
			c := &timedClock{Clock: fake.New(originUTC)}
			l := newLimiter(t, resilience.LimiterConfig{Clock: c})

			assert.NoError(t, l.WaitN(bounded(t), math.MaxInt64), "a Rate of zero must admit any number of units")
			assert.Empty(t, c.durations(), "a call without a limit must start no timer")
		})

		t.Run("returns nil at once for the zero Limiter", func(t *testing.T) {
			t.Parallel()
			var l resilience.Limiter
			assert.NoError(t, l.WaitN(bounded(t), math.MaxInt64), "the zero Limiter must admit any number of units")
		})

		t.Run("returns nil at once for an n of zero", func(t *testing.T) {
			t.Parallel()
			c := &timedClock{Clock: fake.New(originUTC)}
			l := drained(t, c)

			assert.NoError(t, l.WaitN(bounded(t), 0), "an empty bucket must admit zero units")
			assert.Empty(t, c.durations(), "a call for zero units must start no timer")
		})

		t.Run("returns nil at once for an n of zero while a call waits", func(t *testing.T) {
			t.Parallel()
			c := &timedClock{Clock: fake.New(originUTC)}
			l := drained(t, c)

			got := startWait(bounded(t), l, 5)
			c.AwaitWaiters(1)

			assert.NoError(t, l.WaitN(bounded(t), 0), "a call for zero units must not wait")
			assert.Length(t, c.durations(), 1, "a call for zero units must start no timer")

			c.Advance(500 * time.Millisecond)
			assert.NoError(t, await(t, got, "WaitN must return"), "the waiting call must take its units")
		})

		t.Run("waits while the time that the bucket owes exceeds a Duration", func(t *testing.T) {
			t.Parallel()
			// Two costs of the largest Burst at Rate 1 fall 0.86 s short of the
			// largest Duration, so the time of the call passes it 1 s after the
			// start, and saturates at it.
			c := &timedClock{Clock: fake.New(originUTC)}
			l := newLimiter(t, resilience.LimiterConfig{Clock: c, Rate: 1, Burst: maxBurst})
			c.Advance(time.Second)
			assert.True(t, l.AllowN(maxBurst), "the largest bucket must have Burst units")

			ctx, cancel := context.WithCancel(bounded(t))
			got := startWait(ctx, l, maxBurst)
			c.AwaitWaiters(1)
			assert.Equal(t, c.durations(),
				[]time.Duration{time.Duration(math.MaxInt64) - maxBurst*time.Second - time.Second},
				"the head must wait until the largest Duration less the time that fills the bucket")

			cancel()
			assert.ErrorIs(t, await(t, got, "WaitN must return"), context.Canceled,
				"a call past the largest Duration must wait until its context ends")
		})

		t.Run("waits again when its timer ends before the bucket has the units", func(t *testing.T) {
			t.Parallel()
			clk := &lateClock{Clock: fake.New(originUTC), created: make(chan *lateTimer, 1)}
			l := drained(t, clk)

			got := startWait(bounded(t), l, 5)
			early := await(t, clk.created, "the head must start a timer")
			early.c <- clk.Time()
			timer := await(t, clk.created, "a head whose timer ended early must start another timer")
			assert.Equal(t, timer.deadline.Sub(clk.Time()), 500*time.Millisecond,
				"the head must wait again for the units that the bucket lacks")

			clk.Advance(500 * time.Millisecond)
			timer.c <- clk.Time()
			assert.NoError(t, await(t, got, "WaitN must return"), "the head must take its units at the deadline")
		})
	})

	t.Run("WaitNPriority", func(t *testing.T) {
		t.Parallel()

		t.Run("returns ErrPriority for a priority above PriorityHigh", func(t *testing.T) {
			t.Parallel()
			l := newLimiter(t, resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: 10, Burst: 10})
			prop.ErrorIs(t, func(p resilience.Priority) error {
				return l.WaitNPriority(t.Context(), 1, p)
			}, resilience.ErrPriority, "a priority other than the two must be refused",
				prop.Using(prop.Integer[resilience.Priority](2, math.MaxUint8)), prop.Example(resilience.Priority(2)))
			expect.Equal(t, errs.Classify(l.WaitNPriority(t.Context(), 1, 2)), errs.Invalid,
				"ErrPriority must classify as Invalid")
			expect.True(t, l.AllowN(10), "a refused call must take nothing")
		})

		t.Run("returns ErrPriority for a priority above PriorityHigh with a Rate of zero", func(t *testing.T) {
			t.Parallel()
			var l resilience.Limiter
			assert.ErrorIs(t, l.WaitNPriority(t.Context(), 1, 2), resilience.ErrPriority,
				"a priority other than the two must be refused without a limit")
		})

		t.Run("returns the error of a context that ended before the call for a priority that is not valid",
			func(t *testing.T) {
				t.Parallel()
				l := newLimiter(t, resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: 10, Burst: 10})
				assert.HonoursCancellation(t, func(ctx context.Context) error { return l.WaitNPriority(ctx, 1, 2) },
					"an ended context must stop the call before its priority")
			})

		t.Run("returns a flush behind the chunks of four compactions after 1 second", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				c := fake.New(originUTC)
				l := newLimiter(t, resilience.LimiterConfig{Clock: c, Rate: mib, Burst: mib})
				g := &grantLog{l: l, clk: c}
				compactions := make([]int, 0, 4)
				for range 4 {
					compactions = append(compactions, g.start(t.Context(), mib, resilience.PriorityNormal))
				}
				flush := g.start(t.Context(), mib, resilience.PriorityHigh)
				for range 4 {
					c.Advance(time.Second)
					synctest.Wait()
				}
				assert.Equal(t, g.calls(), []returned{
					{call: compactions[0]},
					{call: flush, at: time.Second},
					{call: compactions[1], at: 2 * time.Second},
					{call: compactions[2], at: 3 * time.Second},
					{call: compactions[3], at: 4 * time.Second},
				}, "the flush must take its units after the first chunk, which took the full bucket at once")
			})
		})

		t.Run("grants a call of PriorityHigh ahead of the call of PriorityNormal that is the head", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				c := &timedClock{Clock: fake.New(originUTC)}
				l := drained(t, c)
				g := &grantLog{l: l, clk: c}
				normal := g.start(t.Context(), 5, resilience.PriorityNormal)
				c.Advance(200 * time.Millisecond)
				synctest.Wait()

				// The bucket has 2 units, so the call of PriorityHigh takes them
				// at once, and the call of PriorityNormal waits again for its 5
				// units.
				high := g.start(t.Context(), 2, resilience.PriorityHigh)
				assert.Equal(t, c.durations(), []time.Duration{500 * time.Millisecond, 500 * time.Millisecond},
					"the head must wait on a new timer after a grant to another call")
				assert.Equal(t, c.pending(), 1, "the head must stop the timer that it no longer reads")
				c.Advance(500 * time.Millisecond)
				synctest.Wait()
				assert.Equal(t, g.calls(), []returned{
					{call: high, at: 200 * time.Millisecond},
					{call: normal, at: 700 * time.Millisecond},
				}, "the call of PriorityHigh must take its units before the head of PriorityNormal")
			})
		})

		t.Run("grants every tenth unit to a call of PriorityNormal while calls of PriorityHigh wait",
			func(t *testing.T) {
				t.Parallel()
				synctest.Test(t, func(t *testing.T) {
					c := fake.New(originUTC)
					l := oneUnit(t, c)
					g := &grantLog{l: l, clk: c}
					first, second := g.start(t.Context(), 1, resilience.PriorityNormal),
						g.start(t.Context(), 1, resilience.PriorityNormal)
					highs := make([]int, 0, 2*shareRun+2)
					for range 2*shareRun + 2 {
						highs = append(highs, g.start(t.Context(), 1, resilience.PriorityHigh))
					}
					order := slices.Concat(highs[:shareRun], []int{first}, highs[shareRun:2*shareRun], []int{second},
						highs[2*shareRun:])
					want := make([]returned, 0, len(order))
					for i, call := range order {
						c.Advance(100 * time.Millisecond)
						synctest.Wait()
						want = append(want, returned{call: call, at: time.Duration(i+1) * 100 * time.Millisecond})
					}
					assert.Equal(t, g.calls(), want, "one grant in ten must go to the oldest call of PriorityNormal")
				})
			})

		t.Run("keeps the tenth grant for the call of PriorityNormal when a call of PriorityHigh starts",
			func(t *testing.T) {
				t.Parallel()
				synctest.Test(t, func(t *testing.T) {
					c := fake.New(originUTC)
					l := oneUnit(t, c)
					g := &grantLog{l: l, clk: c}
					normal := g.start(t.Context(), 1, resilience.PriorityNormal)
					want := make([]returned, 0, shareRun+2)
					for i := range shareRun {
						high := g.start(t.Context(), 1, resilience.PriorityHigh)
						c.Advance(100 * time.Millisecond)
						synctest.Wait()
						want = append(want, returned{call: high, at: time.Duration(i+1) * 100 * time.Millisecond})
					}
					late := g.start(t.Context(), 1, resilience.PriorityHigh)
					for range 2 {
						c.Advance(100 * time.Millisecond)
						synctest.Wait()
					}
					want = append(want, returned{call: normal, at: time.Second},
						returned{call: late, at: 1100 * time.Millisecond})
					assert.Equal(t, g.calls(), want, "a call of PriorityHigh must wait behind the tenth grant")
				})
			})

		t.Run("starts the share again when no call of PriorityNormal waits", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				c := fake.New(originUTC)
				l := oneUnit(t, c)
				g := &grantLog{l: l, clk: c}
				ctx, cancel := context.WithCancel(t.Context())
				gone := g.start(ctx, 1, resilience.PriorityNormal)
				highs := make([]int, 0, 15)
				for range 15 {
					highs = append(highs, g.start(t.Context(), 1, resilience.PriorityHigh))
				}
				for range 5 {
					c.Advance(100 * time.Millisecond)
					synctest.Wait()
				}
				cancel()
				synctest.Wait()
				want := make([]returned, 0, 17)
				for i := range 4 {
					want = append(want, returned{call: highs[i], at: time.Duration(i+1) * 100 * time.Millisecond})
				}
				// The cancelled call returns at the time of the fifth grant, and
				// it started before the call of that grant.
				want = append(want, returned{call: gone, at: 500 * time.Millisecond, err: context.Canceled},
					returned{call: highs[4], at: 500 * time.Millisecond})

				// The call of PriorityNormal that starts now waits for nine
				// grants to calls of PriorityHigh, not for the four that the
				// count of the cancelled call had left.
				normal := g.start(t.Context(), 1, resilience.PriorityNormal)
				order := slices.Concat(highs[5:5+shareRun], []int{normal}, highs[5+shareRun:])
				for i, call := range order {
					c.Advance(100 * time.Millisecond)
					synctest.Wait()
					want = append(want, returned{call: call, at: time.Duration(6+i) * 100 * time.Millisecond})
				}
				assert.Equal(t, g.calls(), want, "the count of the grants must start again without a waiting call")
			})
		})

		t.Run("lets the next call take its units at once when the head leaves", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				c := &timedClock{Clock: fake.New(originUTC)}
				l := newLimiter(t, resilience.LimiterConfig{Clock: c, Rate: 10, Burst: 10})
				assert.True(t, l.AllowN(8), "the bucket must admit 8 of its 10 units")
				g := &grantLog{l: l, clk: c}
				ctx, cancel := context.WithCancel(t.Context())
				head := g.start(ctx, 5, resilience.PriorityNormal)
				next := g.start(t.Context(), 2, resilience.PriorityNormal)
				cancel()
				synctest.Wait()
				assert.Equal(t, g.calls(), []returned{
					{call: head, err: context.Canceled},
					{call: next},
				}, "the call behind a head that leaves must take the 2 units of the bucket at once")
				assert.Equal(t, c.durations(), []time.Duration{300 * time.Millisecond},
					"the call behind the head must start no timer")
				assert.Equal(t, c.pending(), 0, "the head that leaves must stop its timer")
			})
		})
	})

	t.Run("Burst", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the Burst of the configuration", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Burst must return the Burst of the configuration", func(pc *prop.Case) {
				rate := pc.Draw(prop.Integer[int64](1, math.MaxInt64), "rate")
				burst := pc.Draw(prop.Integer[int64](1, maxBurst), "burst")
				cfg := resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: rate, Burst: burst}
				l, err := resilience.NewLimiter(cfg)
				assert.NoError(pc, err, "NewLimiter must accept the config")
				assert.Equal(pc, l.Burst(), burst, "Burst must return the Burst of the configuration")
			})
		})

		t.Run("returns the most units that one WaitN takes", func(t *testing.T) {
			t.Parallel()
			l := newLimiter(t, resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: 10, Burst: 7})
			expect.NoError(t, l.WaitN(bounded(t), l.Burst()), "WaitN must take Burst units in one call")
			expect.ErrorIs(t, l.WaitN(bounded(t), l.Burst()+1), resilience.ErrUnits,
				"WaitN must refuse one unit more than Burst")
		})

		t.Run("returns math.MaxInt64 for a Rate of zero", func(t *testing.T) {
			t.Parallel()
			l := newLimiter(t, resilience.LimiterConfig{Clock: fake.New(originUTC), Burst: 7})
			assert.Equal(t, l.Burst(), int64(math.MaxInt64),
				"a Limiter without a limit must take any number of units in one call")
		})

		t.Run("returns math.MaxInt64 for the zero Limiter", func(t *testing.T) {
			t.Parallel()
			var l resilience.Limiter
			assert.Equal(t, l.Burst(), int64(math.MaxInt64),
				"the zero Limiter must take any number of units in one call")
		})
	})

	t.Run("grants units by an exact model of the bucket, the queues and the share under any sequence of calls",
		func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				prop.ForAll(t, limiterContract, limiterSteps)
			})
		})
}

// TestLimiterAllocs checks the allocation contract of AllowN, of the calls
// of WaitN and WaitNPriority that do not wait and of Burst. MaxAllocs counts
// the allocations of the whole process, so the test does not run in
// parallel.
func TestLimiterAllocs(t *testing.T) {
	ctx := t.Context()

	t.Run("AllowN", func(t *testing.T) {
		cfg := resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: math.MaxInt32, Burst: math.MaxInt32}
		l := newLimiter(t, cfg)

		var got bool
		expect.MaxAllocs(t, func() { got = l.AllowN(1) }, 0, "AllowN must not allocate")
		assert.True(t, got, "the test must measure an admitted call")
	})

	t.Run("WaitN", func(t *testing.T) {
		t.Run("of a call that does not wait", func(t *testing.T) {
			c := fake.New(originUTC)
			l := newLimiter(t, resilience.LimiterConfig{Clock: c, Rate: math.MaxInt32, Burst: math.MaxInt32})

			var err error
			expect.MaxAllocs(t, func() {
				c.Advance(time.Nanosecond)
				err = l.WaitN(ctx, 1)
			}, 0, "a WaitN that does not wait must not allocate")
			assert.NoError(t, err, "the test must measure an admitted call")
		})
	})

	t.Run("WaitNPriority", func(t *testing.T) {
		t.Run("of a call that does not wait", func(t *testing.T) {
			c := fake.New(originUTC)
			l := newLimiter(t, resilience.LimiterConfig{Clock: c, Rate: math.MaxInt32, Burst: math.MaxInt32})

			var err error
			expect.MaxAllocs(t, func() {
				c.Advance(time.Nanosecond)
				err = l.WaitNPriority(ctx, 1, resilience.PriorityHigh)
			}, 0, "a WaitNPriority that does not wait must not allocate")
			assert.NoError(t, err, "the test must measure an admitted call")
		})
	})

	t.Run("Burst", func(t *testing.T) {
		l := newLimiter(t, resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: 10, Burst: 7})

		var got int64
		expect.MaxAllocs(t, func() { got = l.Burst() }, 0, "Burst must not allocate")
		assert.Equal(t, got, int64(7), "the test must measure the Burst of the configuration")
	})
}

// BenchmarkLimiter reports the cost of AllowN, of the calls of WaitN and
// WaitNPriority that do not wait and of Burst, and fails when one
// allocates.
func BenchmarkLimiter(b *testing.B) {
	ctx := b.Context()

	b.Run("AllowN", func(b *testing.B) {
		cfg := resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: math.MaxInt32, Burst: math.MaxInt32}
		l := newLimiter(b, cfg)

		var got bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = l.AllowN(1)
		}

		assert.True(b, got, "the benchmark must measure an admitted call")
	})

	b.Run("WaitN", func(b *testing.B) {
		b.Run("of a call that does not wait", func(b *testing.B) {
			clk := fake.New(originUTC)
			l := newLimiter(b, resilience.LimiterConfig{Clock: clk, Rate: math.MaxInt32, Burst: math.MaxInt32})

			var err error

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				clk.Advance(time.Nanosecond)
				err = l.WaitN(ctx, 1)
			}

			assert.NoError(b, err, "the benchmark must measure an admitted call")
		})
	})

	b.Run("WaitNPriority", func(b *testing.B) {
		b.Run("of a call that does not wait", func(b *testing.B) {
			clk := fake.New(originUTC)
			l := newLimiter(b, resilience.LimiterConfig{Clock: clk, Rate: math.MaxInt32, Burst: math.MaxInt32})

			var err error

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				clk.Advance(time.Nanosecond)
				err = l.WaitNPriority(ctx, 1, resilience.PriorityHigh)
			}

			assert.NoError(b, err, "the benchmark must measure an admitted call")
		})
	})

	b.Run("Burst", func(b *testing.B) {
		l := newLimiter(b, resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: 10, Burst: 7})

		var got int64

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = l.Burst()
		}

		assert.Equal(b, got, int64(7), "the benchmark must measure the Burst of the configuration")
	})
}

// newLimiter returns the Limiter of cfg, and fails tb when NewLimiter
// refuses cfg.
func newLimiter(tb testing.TB, cfg resilience.LimiterConfig) *resilience.Limiter {
	tb.Helper()

	l, err := resilience.NewLimiter(cfg)
	assert.NoError(tb, err, "NewLimiter must accept the config")

	return l
}

// startWait calls l.WaitN(ctx, n) on a new goroutine, and returns a channel
// with room for its result, so the goroutine never blocks on the send and
// ends once WaitN returns.
func startWait(ctx context.Context, l *resilience.Limiter, n int64) <-chan error {
	got := make(chan error, 1)
	go func() { got <- l.WaitN(ctx, n) }()

	return got
}

// drained returns a Limiter on c with Rate 10 and Burst 10 whose bucket is
// empty at the current time of c, and fails tb when AllowN refuses the 10
// units of the new bucket. At Rate 10 the bucket then adds a unit every
// 100 ms.
func drained(tb testing.TB, c clock.Clock) *resilience.Limiter {
	tb.Helper()

	l := newLimiter(tb, resilience.LimiterConfig{Clock: c, Rate: 10, Burst: 10})
	assert.True(tb, l.AllowN(10), "a full bucket must have Burst units")

	return l
}

// oneUnit returns a Limiter on c with Rate 10 and Burst 1 whose bucket is
// empty at the current time of c, and fails tb when AllowN refuses the unit
// of the new bucket. A waiting call of one unit then takes its unit every
// 100 ms.
func oneUnit(tb testing.TB, c clock.Clock) *resilience.Limiter {
	tb.Helper()

	l := newLimiter(tb, resilience.LimiterConfig{Clock: c, Rate: 10, Burst: 1})
	assert.True(tb, l.AllowN(1), "a full bucket must have its unit")

	return l
}

// limiterSteps runs the steps of a machine over a Limiter on a fake clock,
// inside the synctest bubble of its test. The actions are an AllowN, a
// WaitNPriority at a drawn priority, the cancellation of a waiting call, and
// an advance of the clock by a few nanoseconds, or to the end of the timer
// of the head and a few nanoseconds past it. At most maxWaiting calls wait
// at once. Every action ends with synctest.Wait, so each call that the
// Limiter grants has returned.
//
// A model keeps the time of the generic cell rate algorithm as an exact
// big.Rat of nanoseconds, the queue of each priority, the head, the end of
// the timer of the head and the count of the grants to calls of
// PriorityHigh. After every step the invariant checks that the calls that
// returned are the calls that the model granted or cancelled, that the
// timers of the Limiter are the timers of the heads of the model, and that
// the head has the only timer that no Stop stopped before its deadline.
//
// The invariant also takes each window from a time s to the time of the
// clock, both included. It checks that the units of the calls that returned
// nil in the window do not exceed the Rate times the length of the window
// plus Burst. A window of length 0 contains the calls of one instant, so the
// check also bounds the units that a full bucket admits at once.
func limiterSteps(c *prop.Case) {
	rate := c.Draw(limiterRates, "rate")
	burst := c.Draw(prop.Integer[int64](1, 16), "burst")
	clk := &timedClock{Clock: fake.New(originUTC)}
	l, err := resilience.NewLimiter(resilience.LimiterConfig{Clock: clk, Rate: rate, Burst: burst})
	assert.NoError(c, err, "NewLimiter must accept the config")

	ctx, cancel := context.WithCancel(c.Context())
	c.Cleanup(cancel)

	var (
		calls  []*limiterCall
		queues [2][]*limiterCall
		head   *limiterCall
		// deadline is the time of the end of the timer of the head, in
		// nanoseconds since originUTC.
		deadline int64
		// highs is the number of grants in a row to calls of PriorityHigh
		// while a call of PriorityNormal waited.
		highs  int
		timers []time.Duration
	)

	// model is the time of the bucket in nanoseconds since originUTC, and
	// fill is the time in which an empty bucket gains Burst units.
	model := new(big.Rat)
	fill := big.NewRat(burst*nsPerSecond, rate)
	now := func() int64 { return int64(clk.Time().Sub(originUTC)) }

	// lack returns the model's time once it covered n more units at the
	// time of the clock, and the time in which the bucket gains the units of
	// n that it lacks: zero or less when the bucket has them.
	lack := func(n int64) (after, wait *big.Rat) {
		at := new(big.Rat).SetInt64(now())
		after = new(big.Rat).Set(model)
		if after.Cmp(at) < 0 {
			after.Set(at)
		}
		after.Add(after, big.NewRat(n*nsPerSecond, rate))
		wait = new(big.Rat).Sub(after, at)

		return after, wait.Sub(wait, fill)
	}

	// leave removes call from the queue of its priority. With no call of
	// PriorityNormal left, the count of the grants starts again.
	leave := func(call *limiterCall) {
		queues[call.priority] = slices.DeleteFunc(queues[call.priority],
			func(w *limiterCall) bool { return w == call })
		if len(queues[resilience.PriorityNormal]) == 0 {
			highs = 0
		}
	}

	// settle grants each head that the bucket covers at the time of the
	// clock, and starts the timer of the head that it does not cover. The
	// head is the oldest call of PriorityHigh, or the oldest call of
	// PriorityNormal when no call of PriorityHigh waits or after shareRun
	// grants in a row to calls of PriorityHigh while it waited. A head keeps
	// its timer until the timer ends, and a new head starts a timer.
	settle := func() {
		for {
			high, normal := queues[resilience.PriorityHigh], queues[resilience.PriorityNormal]
			var next *limiterCall
			if len(high) != 0 && (len(normal) == 0 || highs < shareRun) {
				next = high[0]
			} else if len(normal) != 0 {
				next = normal[0]
			}
			if next == nil {
				head = nil

				return
			}
			if next == head && now() < deadline {
				return
			}

			head = next
			after, wait := lack(next.n)
			if wait.Sign() > 0 {
				// QuoRem rounds the positive wait down, and a remainder
				// rounds it up to the next nanosecond.
				whole, rest := new(big.Int).QuoRem(wait.Num(), wait.Denom(), new(big.Int))
				if rest.Sign() > 0 {
					whole.Add(whole, big.NewInt(1))
				}
				timers = append(timers, time.Duration(whole.Int64()))
				deadline = now() + whole.Int64()

				return
			}

			model.Set(after)
			if next.priority == resilience.PriorityNormal {
				highs = 0
			} else if len(normal) != 0 {
				highs++
			}
			leave(next)
			next.end, next.ok = now(), true
		}
	}

	stateful.Steps(c, stateful.Machine[struct{}]{
		Actions: []stateful.Action[struct{}]{
			{
				Name:  "AllowN",
				Input: func(c *prop.Case, _ struct{}) any { return c.Draw(prop.Integer[int64](0, burst+1), "n") },
				Run: func(c *prop.Case, _ int, in any) {
					n := in.(int64)
					after, wait := lack(n)
					fits := n == 0 || n <= burst && head == nil && wait.Sign() <= 0
					assert.Equal(c, l.AllowN(n), fits,
						"AllowN must admit the calls that the exact bucket covers while no call waits")
					if fits && n > 0 {
						model.Set(after)
						at := now()
						calls = append(calls, &limiterCall{n: n, start: at, end: at, ok: true, returned: true})
					}
				},
			},
			{
				Name:    "WaitNPriority",
				Enabled: func(struct{}) bool { return len(queues[0])+len(queues[1]) < maxWaiting },
				Input: func(c *prop.Case, _ struct{}) any {
					return limiterCall{
						n: c.Draw(prop.Integer[int64](0, burst), "n"),
						priority: c.Draw(prop.SampledFrom(resilience.PriorityNormal, resilience.PriorityHigh),
							"priority"),
					}
				},
				Run: func(_ *prop.Case, _ int, in any) {
					give := in.(limiterCall)
					callCtx, callCancel := context.WithCancel(ctx)
					call := &limiterCall{
						n: give.n, priority: give.priority, start: now(), end: -1,
						done: make(chan error, 1), cancel: callCancel,
					}
					calls = append(calls, call)
					go func() { call.done <- l.WaitNPriority(callCtx, call.n, call.priority) }()
					synctest.Wait()

					after, wait := lack(call.n)
					if call.n == 0 || head == nil && wait.Sign() <= 0 {
						if call.n > 0 {
							model.Set(after)
						}
						call.end, call.ok = call.start, true

						return
					}
					queues[call.priority] = append(queues[call.priority], call)
					settle()
				},
			},
			{
				Name:    "cancel",
				Enabled: func(struct{}) bool { return head != nil },
				Input: func(c *prop.Case, _ struct{}) any {
					return c.Draw(prop.Integer(0, len(queues[0])+len(queues[1])-1), "call")
				},
				Run: func(_ *prop.Case, _ int, in any) {
					call := slices.Concat(queues[resilience.PriorityNormal], queues[resilience.PriorityHigh])[in.(int)]
					call.cancel()
					synctest.Wait()
					leave(call)
					call.end = now()
					settle()
				},
			},
			{
				Name: "advance",
				Input: func(c *prop.Case, _ struct{}) any {
					d := c.Draw(prop.Integer[int64](0, 3), "ns")
					if head != nil && c.Draw(prop.Boolean(), "to the timer") {
						d += deadline - now()
					}

					return d
				},
				Run: func(_ *prop.Case, _ int, in any) {
					clk.Advance(time.Duration(in.(int64)))
					synctest.Wait()
					settle()
				},
			},
		},
		Invariant: func(c *prop.Case, _ struct{}) {
			assert.Equal(c, clk.durations(), timers, "the heads must wait on the exact timers of the model",
				assert.EquateEmpty())
			wantPending := 0
			if head != nil {
				wantPending = 1
			}
			assert.Equal(c, clk.pending(), wantPending, "the Limiter must keep one timer, the timer of its head")
			for _, call := range calls {
				if call.returned {
					continue
				}
				if call.end < 0 {
					assert.Empty(c, call.done, "a call that the model does not grant must wait")

					continue
				}
				assert.Length(c, call.done, 1, "a call must return when the model grants it or it leaves")
				call.returned = true
				if err := <-call.done; call.ok {
					assert.NoError(c, err, "a call that the model grants must return nil")
				} else {
					assert.ErrorIs(c, err, context.Canceled, "a cancelled call must return the error of its context")
				}
			}

			at := now()
			starts := make([]int64, 0, 2+len(calls))
			starts = append(starts, 0, at)
			for _, call := range calls {
				starts = append(starts, call.end)
			}
			for _, s := range starts {
				if s < 0 {
					continue
				}

				var units int64
				for _, call := range calls {
					if call.ok && call.end >= s {
						units += call.n
					}
				}

				// The values stay below 2^53, so InRange compares them exactly.
				bound := rate*(at-s) + burst*nsPerSecond
				assert.InRange(c, units*nsPerSecond, 0, float64(bound),
					"the units of a window must not exceed the Rate times its length plus Burst")
			}
		},
	})
}
