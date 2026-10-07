// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package resilience_test

import (
	"context"
	"math"
	"slices"
	"sync"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/resilience"
)

// maxBurst is the largest Burst that NewLimiter accepts.
const maxBurst = 4_611_686_018

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

		t.Run("returns a Limiter whose bucket holds Burst units", func(t *testing.T) {
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

		t.Run("charges a unit up to the next whole nanosecond", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			l := newLimiter(t, resilience.LimiterConfig{Clock: c, Rate: 3, Burst: 3})
			assert.True(t, l.AllowN(3), "a full bucket must have Burst units")

			c.Advance(333_333_333 * time.Nanosecond)
			assert.False(t, l.AllowN(1), "a unit at Rate 3 must cost 333,333,334 ns")

			c.Advance(time.Nanosecond)
			assert.True(t, l.AllowN(1), "a unit at Rate 3 must cost no more than 333,333,334 ns")
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
	})
}

// TestLimiterAllocs checks the allocation contract of AllowN and of a WaitN
// that does not wait. MaxAllocs counts the allocations of the whole
// process, so the test does not run in parallel.
//
//nolint:paralleltest // see above
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
}

// BenchmarkLimiter reports the cost of AllowN and of a WaitN that does not
// wait, and fails when one allocates.
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
