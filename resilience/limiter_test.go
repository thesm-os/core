// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package resilience_test

import (
	"context"
	"math"
	"math/big"
	"slices"
	"sync"
	"testing"
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
const maxWaiting = 3

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
const limiterContract = "the units of the calls that return nil in a window must stay within " +
	"the Rate times its length, Burst and the units of the calls that waited before it"

// timedClock is a fake clock that records the duration of every timer that
// it creates, so a case observes how long a WaitN waits, and that a call
// that does not wait starts no timer. Its methods are safe for concurrent
// use: a mutex guards timers, and the fake clock guards itself.
type timedClock struct {
	*fake.Clock

	mu sync.Mutex

	// timers contains the duration of each timer, in the order of creation.
	timers []time.Duration
}

// NewTimer records d, and returns the timer of the fake clock, which
// expires when a case advances the clock by d.
func (c *timedClock) NewTimer(d time.Duration) clock.Timer {
	c.mu.Lock()
	c.timers = append(c.timers, d)
	c.mu.Unlock()

	return c.Clock.NewTimer(d)
}

// durations returns a copy of the durations of the timers that c created,
// in the order of creation, and nil when it created none.
func (c *timedClock) durations() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()

	return slices.Clone(c.timers)
}

// lateTimer is a timer of a lateClock. A case delivers its tick with a send
// on c at or after its deadline.
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
// sends it. A case can then deliver the tick after the deadline, as a Clock
// may. Its methods are safe for concurrent use: the fake clock guards
// itself, and created is a channel.
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

// limiterCall is one call of a Limiter that took or reserved units. start
// and end are nanoseconds since originUTC, and end is -1 while the call
// waits. ok reports whether the call returned nil. A call that waits has
// its timer, the channel of its result and the cancellation of its context.
type limiterCall struct {
	n, start, end int64
	ok            bool

	timer  *lateTimer
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

		t.Run("reports true for an n of zero while a waiting call owes the units", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			l := drained(t, c)

			got := startWait(bounded(t), l, 5)
			c.AwaitWaiters(1)
			assert.True(t, l.AllowN(0), "a bucket that owes units to a waiting call must admit zero units")

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

		t.Run("reports false while a waiting call owes the units", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			l := drained(t, c)

			got := startWait(bounded(t), l, 5)
			c.AwaitWaiters(1)
			assert.False(t, l.AllowN(1), "units that a waiting call reserved must not go to another")

			c.Advance(500 * time.Millisecond)
			assert.NoError(t, await(t, got, "WaitN must return"), "the waiting call must take its units")
			assert.False(t, l.AllowN(1), "the units must go to the waiting call")

			c.Advance(100 * time.Millisecond)
			assert.True(t, l.AllowN(1), "the bucket must add units after the waiting call took its own")
		})

		t.Run("reports false while the reservations exceed a Duration", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			l := newLimiter(t, resilience.LimiterConfig{Clock: c, Rate: 1, Burst: maxBurst})
			assert.True(t, l.AllowN(maxBurst), "the largest bucket must have Burst units")

			ctx, cancel := context.WithCancel(bounded(t))
			defer cancel()

			got := startWait(ctx, l, maxBurst)
			c.AwaitWaiters(1)
			assert.False(t, l.AllowN(1), "a unit beyond the largest Duration must be refused")

			cancel()
			assert.ErrorIs(t, await(t, got, "WaitN must return"), context.Canceled,
				"the waiting call must end with its context")
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

		t.Run("admits waiting calls in the order in which they started", func(t *testing.T) {
			t.Parallel()
			c := &timedClock{Clock: fake.New(originUTC)}
			l := drained(t, c)

			first := startWait(bounded(t), l, 5)
			c.AwaitWaiters(1)
			second := startWait(bounded(t), l, 5)
			c.AwaitWaiters(2)
			assert.Equal(t, c.durations(), []time.Duration{500 * time.Millisecond, time.Second},
				"the second call must wait behind the first")

			c.Advance(500 * time.Millisecond)
			assert.NoError(t, await(t, first, "the first WaitN must return"),
				"the first call must take its units first")

			c.Advance(500 * time.Millisecond)
			assert.NoError(t, await(t, second, "the second WaitN must return"),
				"the second call must take its units after the first")
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

		t.Run("gives the reservation back when the context ends during the wait", func(t *testing.T) {
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
			assert.True(t, l.AllowN(10), "a cancelled wait must give its 5 units back")
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
			c := fake.New(originUTC)
			l := newLimiter(t, resilience.LimiterConfig{Clock: c, Rate: 1, Burst: maxBurst})
			assert.True(t, l.AllowN(maxBurst), "the largest bucket must have Burst units")

			ctx, cancel := context.WithCancel(bounded(t))
			first := startWait(ctx, l, maxBurst)
			c.AwaitWaiters(1)
			second := startWait(ctx, l, maxBurst)
			c.AwaitWaiters(2)

			cancel()
			expect.ErrorIs(t, await(t, first, "the first WaitN must return"), context.Canceled,
				"a call that owes about 146 years must wait until its context ends")
			expect.ErrorIs(t, await(t, second, "the second WaitN must return"), context.Canceled,
				"a call past the largest Duration must wait until its context ends")
		})

		tests := []struct {
			name      string
			giveRate  int64
			giveChunk int64
		}{
			{
				name:      "refunds the exact units of a cancelled call at Burst 3 and 2 units per nanosecond",
				giveRate:  2_000_000_000,
				giveChunk: 3,
			},
			{
				name:      "refunds the exact units of a cancelled call at Burst 4,096 and 3 units per nanosecond",
				giveRate:  3_000_000_000,
				giveChunk: 4096,
			},
			{
				name:      "refunds the exact units of a cancelled call at Burst 1,048,575 and 2 units per nanosecond",
				giveRate:  2_000_000_000,
				giveChunk: 1<<20 - 1,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				returned, bound := refundTrace(t, tt.giveRate, tt.giveChunk)
				assert.InRange(t, returned, 0, float64(bound),
					"the window must admit at most the Rate times 1 ns, Burst and the open reservations")
			})
		}
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

	t.Run("admits no more units in a window than the Rate, Burst and the open reservations", func(t *testing.T) {
		t.Parallel()
		prop.ForAll(t, limiterContract, limiterSteps)
	})
}

// TestLimiterAllocs checks the allocation contract of AllowN, of a WaitN
// that does not wait and of Burst. MaxAllocs counts the allocations of the
// whole process, so the test does not run in parallel.
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

	t.Run("Burst", func(t *testing.T) {
		l := newLimiter(t, resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: 10, Burst: 7})

		var got int64
		expect.MaxAllocs(t, func() { got = l.Burst() }, 0, "Burst must not allocate")
		assert.Equal(t, got, int64(7), "the test must measure the Burst of the configuration")
	})
}

// BenchmarkLimiter reports the cost of AllowN, of a WaitN that does not wait
// and of Burst, and fails when one allocates.
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

// refundTrace runs a trace in which a refund rounded to whole nanoseconds
// funds more units than it took. It drains a Limiter of Rate rate and
// Burst chunk, starts three waiting calls of chunk units, and moves the
// clock past their deadlines. In a window of 1 ns it delivers the tick of
// the first call, and takes two calls with AllowN after it and after the
// cancellation of each of the other two calls. The two calls are of sizes
// that fit the rounding of the cost of chunk units. refundTrace returns the
// units of the calls that succeeded in the window and the bound of the
// window: the Rate times 1 ns, Burst and the chunks of the three calls.
// rate is a whole number of units per nanosecond.
func refundTrace(tb testing.TB, rate, chunk int64) (returned, bound int64) {
	tb.Helper()

	perNS := rate / nsPerSecond
	ceilNS := (chunk + perNS - 1) / perNS
	pair := [2]int64{(ceilNS - 1) * perNS, perNS}

	clk := &lateClock{Clock: fake.New(originUTC), created: make(chan *lateTimer, 1)}
	l := newLimiter(tb, resilience.LimiterConfig{Clock: clk, Rate: rate, Burst: chunk})
	assert.True(tb, l.AllowN(chunk), "a full bucket must have Burst units")

	calls := make([]*limiterCall, 3)
	for i := range calls {
		ctx, cancel := context.WithCancel(bounded(tb))
		tb.Cleanup(cancel)
		call := &limiterCall{n: chunk, done: make(chan error, 1), cancel: cancel}
		go func() { call.done <- l.WaitN(ctx, call.n) }()
		call.timer = await(tb, clk.created, "a WaitN on an empty bucket must start a timer")
		calls[i] = call
	}

	clk.Advance(time.Duration(4*ceilNS) + time.Nanosecond)
	assert.False(tb, clk.Time().Before(calls[0].timer.deadline), "the timer of the first call must be due")
	calls[0].timer.c <- clk.Time()
	assert.NoError(tb, await(tb, calls[0].done, "the first WaitN must return"), "the first call must take its units")

	returned = chunk
	for i, call := range calls {
		if i > 0 {
			call.cancel()
			assert.ErrorIs(tb, await(tb, call.done, "a cancelled WaitN must return"), context.Canceled,
				"a cancelled call must return the error of its context")
		}

		for _, n := range pair {
			if l.AllowN(n) {
				returned += n
			}
		}
	}

	return returned, perNS + chunk + 3*chunk
}

// limiterSteps runs the steps of a machine over a Limiter on a lateClock.
// The actions are an AllowN, a WaitN, the late tick of the timer of a
// waiting call, the cancellation of a waiting call, and an advance of the
// clock by up to 3 ns. At most maxWaiting calls wait at once.
//
// A model keeps the time of the generic cell rate algorithm as an exact
// big.Rat of nanoseconds. Each AllowN must report what the model admits,
// and each WaitN must return at once or start the timer of the model's
// wait, rounded up to a whole nanosecond.
//
// After every step the invariant takes each window from a time s to the
// time of the clock, both included. It checks that the units of the calls
// that returned nil in the window stay within the Rate times the length of
// the window, Burst and the units of the calls that waited before s. A
// window of length 0 contains the calls of one instant, so the check also
// bounds the units that a full bucket admits at once.
func limiterSteps(c *prop.Case) {
	rate := c.Draw(limiterRates, "rate")
	burst := c.Draw(prop.Integer[int64](1, 16), "burst")
	clk := &lateClock{Clock: fake.New(originUTC), created: make(chan *lateTimer, 1)}
	l, err := resilience.NewLimiter(resilience.LimiterConfig{Clock: clk, Rate: rate, Burst: burst})
	assert.NoError(c, err, "NewLimiter must accept the config")

	ctx, cancel := context.WithCancel(c.Context())
	c.Cleanup(cancel)

	var calls, waiting []*limiterCall

	// model is the time of the bucket in nanoseconds since originUTC, and
	// fill is the time in which an empty bucket gains Burst units.
	model := new(big.Rat)
	fill := big.NewRat(burst*nsPerSecond, rate)

	// gap returns the model's time once it covered n more units at the time
	// of the clock, and that time minus the time of the clock.
	gap := func(n int64) (after, ahead *big.Rat) {
		now := new(big.Rat).SetInt64(int64(clk.Time().Sub(originUTC)))
		after = new(big.Rat).Set(model)
		if after.Cmp(now) < 0 {
			after.Set(now)
		}
		after.Add(after, big.NewRat(n*nsPerSecond, rate))

		return after, new(big.Rat).Sub(after, now)
	}

	// due returns the waiting calls whose ticks a case may deliver.
	due := func() []*limiterCall {
		var ready []*limiterCall
		for _, call := range waiting {
			if !clk.Time().Before(call.timer.deadline) {
				ready = append(ready, call)
			}
		}

		return ready
	}

	// end records that call returned at the time of the clock, and whether
	// it returned nil.
	end := func(call *limiterCall, ok bool) {
		call.end, call.ok = int64(clk.Time().Sub(originUTC)), ok
		waiting = slices.DeleteFunc(waiting, func(w *limiterCall) bool { return w == call })
	}

	stateful.Steps(c, stateful.Machine[struct{}]{
		Actions: []stateful.Action[struct{}]{
			{
				Name:  "AllowN",
				Input: func(c *prop.Case, _ struct{}) any { return c.Draw(prop.Integer[int64](0, burst+1), "n") },
				Run: func(c *prop.Case, _ int, in any) {
					n := in.(int64)
					if n == 0 {
						assert.True(c, l.AllowN(0), "AllowN must admit zero units")

						return
					}

					after, ahead := gap(n)
					fits := n <= burst && ahead.Cmp(fill) <= 0
					assert.Equal(c, l.AllowN(n), fits, "AllowN must admit the calls that the exact bucket covers")
					if fits {
						model.Set(after)
						at := int64(clk.Time().Sub(originUTC))
						calls = append(calls, &limiterCall{n: n, start: at, end: at, ok: true})
					}
				},
			},
			{
				Name:    "WaitN",
				Enabled: func(struct{}) bool { return len(waiting) < maxWaiting },
				Input:   func(c *prop.Case, _ struct{}) any { return c.Draw(prop.Integer[int64](0, burst), "n") },
				Run: func(c *prop.Case, _ int, in any) {
					n := in.(int64)
					after, ahead := gap(n)
					wait := new(big.Rat).Sub(ahead, fill)

					callCtx, callCancel := context.WithCancel(ctx)
					call := &limiterCall{
						n: n, start: int64(clk.Time().Sub(originUTC)), end: -1,
						done: make(chan error, 1), cancel: callCancel,
					}
					calls = append(calls, call)
					go func() { call.done <- l.WaitN(callCtx, call.n) }()

					patient := time.NewTimer(patience)
					defer patient.Stop()

					select {
					case err := <-call.done:
						assert.NoError(c, err, "a WaitN that the bucket covers must return nil")
						assert.True(c, n == 0 || wait.Sign() <= 0,
							"WaitN must return at once only when the exact bucket covers the units")
						call.end, call.ok = call.start, true
						callCancel()
					case call.timer = <-clk.created:
						// The exact wait is positive here, so QuoRem rounds it down
						// and a remainder rounds it up to the next nanosecond.
						whole, rest := new(big.Int).QuoRem(wait.Num(), wait.Denom(), new(big.Int))
						if rest.Sign() > 0 {
							whole.Add(whole, big.NewInt(1))
						}
						assert.Equal(c, call.timer.deadline.Sub(clk.Time()), time.Duration(whole.Int64()),
							"WaitN must wait the exact time of the bucket rounded up to a whole nanosecond")
						waiting = append(waiting, call)
					case <-patient.C:
						c.Fatalf("WaitN neither returned nor started a timer within %s", patience)
					}

					if n > 0 {
						model.Set(after)
					}
				},
			},
			{
				Name:    "tick",
				Enabled: func(struct{}) bool { return len(due()) > 0 },
				Input: func(c *prop.Case, _ struct{}) any {
					return c.Draw(prop.Integer(0, len(due())-1), "call")
				},
				Run: func(c *prop.Case, _ int, in any) {
					call := due()[in.(int)]
					call.timer.c <- clk.Time()
					err := await(c, call.done, "a WaitN must return once its tick arrives")
					assert.NoError(c, err, "a call must take its units once its tick arrives")
					end(call, true)
				},
			},
			{
				Name:    "cancel",
				Enabled: func(struct{}) bool { return len(waiting) > 0 },
				Input: func(c *prop.Case, _ struct{}) any {
					return c.Draw(prop.Integer(0, len(waiting)-1), "call")
				},
				Run: func(c *prop.Case, _ int, in any) {
					call := waiting[in.(int)]
					call.cancel()
					assert.ErrorIs(c, await(c, call.done, "a cancelled WaitN must return"), context.Canceled,
						"a cancelled call must return the error of its context")
					model.Sub(model, big.NewRat(call.n*nsPerSecond, rate))
					end(call, false)
				},
			},
			{
				Name:  "advance",
				Input: func(c *prop.Case, _ struct{}) any { return c.Draw(prop.Integer(0, 3), "ns") },
				Run: func(_ *prop.Case, _ int, in any) {
					clk.Advance(time.Duration(in.(int)))
				},
			},
		},
		Invariant: func(c *prop.Case, _ struct{}) {
			now := int64(clk.Time().Sub(originUTC))
			starts := make([]int64, 0, 2+2*len(calls))
			starts = append(starts, 0, now)
			for _, call := range calls {
				starts = append(starts, call.start+1, call.end)
			}

			for _, s := range starts {
				if s < 0 || s > now {
					continue
				}

				var units, open int64
				for _, call := range calls {
					if call.ok && call.end >= s {
						units += call.n
					}
					if call.start < s && (call.end < 0 || call.end >= s) {
						open += call.n
					}
				}

				// The values stay below 2^53, so InRange compares them exactly.
				bound := rate*(now-s) + (burst+open)*nsPerSecond
				assert.InRange(c, units*nsPerSecond, 0, float64(bound), limiterContract)
			}
		},
	})
}
