// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package resilience_test

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/resilience"
)

const (
	// maxBurst is the largest Burst that NewLimiter accepts.
	maxBurst = 4_611_686_018

	// benchRuns is the number of calls over which a benchmark averages
	// the allocations that it checks.
	benchRuns = 100
)

// timedClock is a fake clock that records the duration of every timer it
// creates, so a case observes how long a WaitN waits, and that a call
// which does not wait starts no timer. Its methods are safe for concurrent
// use: a mutex guards timers, and the fake clock guards itself.
type timedClock struct {
	*fake.Clock

	mu sync.Mutex

	// timers contains the duration of each timer, in the order of
	// creation.
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

	return append([]time.Duration(nil), c.timers...)
}

// newTimedClock returns a timedClock at originUTC that has created no
// timer.
func newTimedClock() *timedClock {
	return &timedClock{Clock: fake.New(originUTC)}
}

// mustLimiter returns the Limiter of cfg, and fails tb at once when
// NewLimiter refuses cfg.
func mustLimiter(tb testing.TB, cfg resilience.LimiterConfig) *resilience.Limiter {
	tb.Helper()

	l, err := resilience.NewLimiter(cfg)
	testkit.NoError(tb, err, "NewLimiter must accept a valid config")

	return l
}

// startWait calls l.WaitN(ctx, n) on a new goroutine, and returns a
// channel with room for its result, so the goroutine never blocks on the
// send and ends once WaitN returns.
func startWait(ctx context.Context, l *resilience.Limiter, n int64) <-chan error {
	got := make(chan error, 1)
	go func() { got <- l.WaitN(ctx, n) }()

	return got
}

// waitResult returns the result that startWait delivers on got, and fails
// tb when WaitN does not return within one second of real time. The bound
// is real time, so a WaitN that never returns fails its case and does not
// hang the test binary.
func waitResult(tb testing.TB, got <-chan error) error {
	tb.Helper()

	select {
	case err := <-got:
		return err
	case <-time.After(time.Second):
		tb.Fatal("WaitN never returned")

		return nil
	}
}

// drained returns a Limiter over c with Rate 10 and Burst 10 whose bucket
// is empty at the current time of c: it takes the 10 units of the new
// bucket, and fails tb when AllowN refuses them. At Rate 10 the bucket
// then gains a unit every 100 ms.
func drained(tb testing.TB, c clock.Clock) *resilience.Limiter {
	tb.Helper()

	l := mustLimiter(tb, resilience.LimiterConfig{Clock: c, Rate: 10, Burst: 10})
	testkit.True(tb, l.AllowN(10), "a full bucket must hold Burst units")

	return l
}

func TestLimiter(t *testing.T) {
	t.Parallel()

	t.Run("NewLimiter", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give resilience.LimiterConfig
		}{
			{name: "returns ErrConfig for a nil Clock", give: resilience.LimiterConfig{Rate: 1, Burst: 1}},
			{
				name: "returns ErrConfig for a negative Rate",
				give: resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: -1, Burst: 1},
			},
			{
				name: "returns ErrConfig for a positive Rate without a Burst",
				give: resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: 1},
			},
			{
				name: "returns ErrConfig for a negative Burst",
				give: resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: 1, Burst: -1},
			},
			{
				name: "returns ErrConfig for a Burst above 4,611,686,018",
				give: resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: 1, Burst: maxBurst + 1},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, err := resilience.NewLimiter(tt.give)
				testkit.ErrorIs(t, err, resilience.ErrConfig, "NewLimiter must refuse the config")
				testkit.Equal(t, errs.Classify(err), errs.Invalid, "ErrConfig must classify as Invalid")
			})
		}

		t.Run("returns a Limiter without a limit for a Rate of zero", func(t *testing.T) {
			t.Parallel()
			l := mustLimiter(t, resilience.LimiterConfig{Clock: fake.New(originUTC)})
			testkit.True(t, l.AllowN(math.MaxInt64), "a Rate of zero must admit any number of units")
		})

		t.Run("returns a Limiter whose bucket holds Burst units", func(t *testing.T) {
			t.Parallel()
			l := mustLimiter(t, resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: 1, Burst: 3})
			testkit.True(t, l.AllowN(3), "a new bucket must hold Burst units")
			testkit.False(t, l.AllowN(1), "a new bucket must hold no more than Burst units")
		})

		t.Run("returns a Limiter for a Burst of 1", func(t *testing.T) {
			t.Parallel()
			l := mustLimiter(t, resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: 1, Burst: 1})
			testkit.True(t, l.AllowN(1), "a bucket of Burst 1 must hold one unit")
		})

		t.Run("returns a Limiter for a Burst of 4,611,686,018", func(t *testing.T) {
			t.Parallel()
			l := mustLimiter(t, resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: 1, Burst: maxBurst})
			testkit.True(t, l.AllowN(maxBurst), "the largest bucket must hold Burst units")
		})
	})

	t.Run("AllowN", func(t *testing.T) {
		t.Parallel()

		t.Run("reports false for units that the bucket does not hold", func(t *testing.T) {
			t.Parallel()
			l := drained(t, fake.New(originUTC))
			testkit.False(t, l.AllowN(1), "an empty bucket must not admit a unit")
		})

		t.Run("takes nothing when it reports false", func(t *testing.T) {
			t.Parallel()
			l := mustLimiter(t, resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: 10, Burst: 10})
			testkit.True(t, l.AllowN(8), "the bucket must admit 8 of its 10 units")
			testkit.False(t, l.AllowN(3), "the bucket must not admit 3 of its 2 units")
			testkit.True(t, l.AllowN(2), "a refused call must leave the 2 units in the bucket")
		})

		t.Run("adds Rate units per second", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			l := drained(t, c)

			c.Advance(100 * time.Millisecond)
			testkit.True(t, l.AllowN(1), "a tenth of a second must add one unit at Rate 10")
			testkit.False(t, l.AllowN(1), "a tenth of a second must add only one unit at Rate 10")

			c.Advance(time.Second)
			testkit.True(t, l.AllowN(10), "a second must add Rate units")
		})

		t.Run("charges a unit up to the next whole nanosecond", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			l := mustLimiter(t, resilience.LimiterConfig{Clock: c, Rate: 3, Burst: 3})
			testkit.True(t, l.AllowN(3), "a full bucket must hold Burst units")

			c.Advance(333_333_333 * time.Nanosecond)
			testkit.False(t, l.AllowN(1), "a unit at Rate 3 must cost 333,333,334 ns")

			c.Advance(time.Nanosecond)
			testkit.True(t, l.AllowN(1), "a unit at Rate 3 must cost no more than 333,333,334 ns")
		})

		t.Run("adds no more than Burst units", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			l := drained(t, c)

			c.Advance(time.Hour)
			testkit.True(t, l.AllowN(10), "a refilled bucket must hold Burst units")
			testkit.False(t, l.AllowN(1), "a refilled bucket must hold no more than Burst units")
		})

		t.Run("adds no units while the clock is behind the last refill", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			l := drained(t, c)

			c.Set(originUTC.Add(-time.Hour))
			testkit.False(t, l.AllowN(1), "a clock that moved back must add no unit")

			c.Set(originUTC.Add(time.Second))
			testkit.True(t, l.AllowN(10), "a clock past the last refill must add units again")
		})

		t.Run("reports true for an n of zero", func(t *testing.T) {
			t.Parallel()
			l := drained(t, fake.New(originUTC))
			testkit.True(t, l.AllowN(0), "an empty bucket must admit zero units")
		})

		t.Run("reports false for a negative n", func(t *testing.T) {
			t.Parallel()
			l := mustLimiter(t, resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: 10, Burst: 10})
			testkit.False(t, l.AllowN(-1), "a negative number of units must be refused")
			testkit.True(t, l.AllowN(10), "a refused negative call must take nothing")
		})

		t.Run("reports false for an n above Burst", func(t *testing.T) {
			t.Parallel()
			l := mustLimiter(t, resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: 10, Burst: 10})
			testkit.False(t, l.AllowN(11), "more units than the burst must be refused")
			testkit.True(t, l.AllowN(10), "a refused call above the burst must take nothing")
		})

		t.Run("reports false for a negative n with a Rate of zero", func(t *testing.T) {
			t.Parallel()
			l := mustLimiter(t, resilience.LimiterConfig{Clock: fake.New(originUTC)})
			testkit.False(t, l.AllowN(-1), "a negative number of units must be refused without a limit")
		})

		t.Run("reports true for any n of the zero Limiter", func(t *testing.T) {
			t.Parallel()
			var l resilience.Limiter
			testkit.True(t, l.AllowN(math.MaxInt64), "the zero Limiter must admit any number of units")
		})

		t.Run("reports false while a waiting call owes the units", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			l := drained(t, c)

			got := startWait(bounded(t), l, 5)
			c.AwaitWaiters(1)
			testkit.False(t, l.AllowN(1), "units reserved by a waiting call must not be admitted to another")

			c.Advance(500 * time.Millisecond)
			testkit.NoError(t, waitResult(t, got), "the waiting call must take its units")
			testkit.False(t, l.AllowN(1), "the units must go to the waiting call")

			c.Advance(100 * time.Millisecond)
			testkit.True(t, l.AllowN(1), "the bucket must add units after the waiting call took its own")
		})

		t.Run("admits at most the bucket's units across goroutines", func(t *testing.T) {
			t.Parallel()
			l := mustLimiter(t, resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: 10, Burst: 10})

			var (
				admitted atomic.Int64
				wg       sync.WaitGroup
			)
			for range 64 {
				wg.Go(func() {
					if l.AllowN(1) {
						admitted.Add(1)
					}
				})
			}
			wg.Wait()

			testkit.Equal(t, admitted.Load(), int64(10), "concurrent calls must share the 10 units of the bucket")
		})
	})

	t.Run("WaitN", func(t *testing.T) {
		t.Parallel()

		t.Run("returns nil at once for units that the bucket holds", func(t *testing.T) {
			t.Parallel()
			c := newTimedClock()
			l := mustLimiter(t, resilience.LimiterConfig{Clock: c, Rate: 10, Burst: 10})

			testkit.NoError(t, l.WaitN(bounded(t), 10), "a full bucket must admit Burst units")
			testkit.Len(t, c.durations(), 0, "a call that does not wait must start no timer")
			testkit.False(t, l.AllowN(1), "WaitN must take the units")
		})

		t.Run("starts no timer for units that the bucket holds exactly", func(t *testing.T) {
			t.Parallel()
			c := newTimedClock()
			l := mustLimiter(t, resilience.LimiterConfig{Clock: c, Rate: 10, Burst: 10})
			testkit.True(t, l.AllowN(5), "the bucket must admit 5 of its 10 units")

			testkit.NoError(t, l.WaitN(bounded(t), 5), "the bucket must admit its last 5 units")
			testkit.Len(t, c.durations(), 0, "a call for the units left must start no timer")
		})

		t.Run("waits for the time in which the bucket adds the units", func(t *testing.T) {
			t.Parallel()
			c := newTimedClock()
			l := drained(t, c)

			got := startWait(bounded(t), l, 5)
			c.AwaitWaiters(1)
			testkit.Equal(t, c.durations(), []time.Duration{500 * time.Millisecond},
				"5 units at Rate 10 must take half a second")

			c.Advance(500 * time.Millisecond)
			testkit.NoError(t, waitResult(t, got), "the call must take its units once the bucket added them")
		})

		t.Run("waits only for the units that the bucket lacks", func(t *testing.T) {
			t.Parallel()
			c := newTimedClock()
			l := drained(t, c)

			c.Advance(200 * time.Millisecond)
			got := startWait(bounded(t), l, 5)
			c.AwaitWaiters(1)
			testkit.Equal(t, c.durations(), []time.Duration{300 * time.Millisecond},
				"a bucket with 2 units must wait 300 ms for 5 units at Rate 10")

			c.Advance(300 * time.Millisecond)
			testkit.NoError(t, waitResult(t, got), "the call must take its units once the bucket added them")
		})

		t.Run("admits waiting calls in the order in which they started", func(t *testing.T) {
			t.Parallel()
			c := newTimedClock()
			l := drained(t, c)

			first := startWait(bounded(t), l, 5)
			c.AwaitWaiters(1)
			second := startWait(bounded(t), l, 5)
			c.AwaitWaiters(2)
			testkit.Equal(t, c.durations(), []time.Duration{500 * time.Millisecond, time.Second},
				"the second call must wait behind the first")

			c.Advance(500 * time.Millisecond)
			testkit.NoError(t, waitResult(t, first), "the first call must take its units first")

			c.Advance(500 * time.Millisecond)
			testkit.NoError(t, waitResult(t, second), "the second call must take its units after the first")
		})

		t.Run("returns the context's error when ctx ends first", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			l := drained(t, c)

			ctx, cancel := context.WithCancel(bounded(t))
			got := startWait(ctx, l, 5)
			c.AwaitWaiters(1)
			cancel()

			testkit.ErrorIs(t, waitResult(t, got), context.Canceled, "a cancelled wait must return the context's error")
		})

		t.Run("gives the reservation back when ctx ends first", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			l := drained(t, c)

			ctx, cancel := context.WithCancel(bounded(t))
			got := startWait(ctx, l, 5)
			c.AwaitWaiters(1)
			cancel()
			testkit.ErrorIs(t, waitResult(t, got), context.Canceled, "a cancelled wait must return the context's error")

			c.Advance(time.Second)
			testkit.True(t, l.AllowN(10), "a cancelled wait must give its 5 units back")
		})

		t.Run("returns the context's error for a ctx that has ended", func(t *testing.T) {
			t.Parallel()
			c := newTimedClock()
			l := mustLimiter(t, resilience.LimiterConfig{Clock: c, Rate: 10, Burst: 10})

			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			testkit.ErrorIs(t, l.WaitN(ctx, 1), context.Canceled, "an ended context must stop the call")
			testkit.True(t, l.AllowN(10), "a call with an ended context must take nothing")
		})

		t.Run("returns the context's error for a ctx that has ended with a Rate of zero", func(t *testing.T) {
			t.Parallel()
			l := mustLimiter(t, resilience.LimiterConfig{Clock: fake.New(originUTC)})

			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			testkit.ErrorIs(t, l.WaitN(ctx, 1), context.Canceled, "an ended context must stop the call")
		})

		t.Run("returns ErrUnits for a negative n", func(t *testing.T) {
			t.Parallel()
			l := mustLimiter(t, resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: 10, Burst: 10})

			err := l.WaitN(bounded(t), -1)
			testkit.ErrorIs(t, err, resilience.ErrUnits, "a negative number of units must be refused")
			testkit.Equal(t, errs.Classify(err), errs.Invalid, "ErrUnits must classify as Invalid")
		})

		t.Run("returns ErrUnits for an n above Burst", func(t *testing.T) {
			t.Parallel()
			l := mustLimiter(t, resilience.LimiterConfig{Clock: fake.New(originUTC), Rate: 10, Burst: 10})

			testkit.ErrorIs(t, l.WaitN(bounded(t), 11), resilience.ErrUnits,
				"more units than the burst must be refused")
			testkit.True(t, l.AllowN(10), "a refused call must take nothing")
		})

		t.Run("returns ErrUnits for a negative n with a Rate of zero", func(t *testing.T) {
			t.Parallel()
			l := mustLimiter(t, resilience.LimiterConfig{Clock: fake.New(originUTC)})
			testkit.ErrorIs(t, l.WaitN(bounded(t), -1), resilience.ErrUnits,
				"a negative number of units must be refused without a limit")
		})

		t.Run("returns nil at once with a Rate of zero", func(t *testing.T) {
			t.Parallel()
			c := newTimedClock()
			l := mustLimiter(t, resilience.LimiterConfig{Clock: c})

			testkit.NoError(t, l.WaitN(bounded(t), math.MaxInt64), "a Rate of zero must admit any number of units")
			testkit.Len(t, c.durations(), 0, "a call without a limit must start no timer")
		})

		t.Run("returns nil at once for the zero Limiter", func(t *testing.T) {
			t.Parallel()
			var l resilience.Limiter
			testkit.NoError(t, l.WaitN(bounded(t), math.MaxInt64), "the zero Limiter must admit any number of units")
		})

		t.Run("returns nil at once for an n of zero", func(t *testing.T) {
			t.Parallel()
			c := newTimedClock()
			l := drained(t, c)

			testkit.NoError(t, l.WaitN(bounded(t), 0), "an empty bucket must admit zero units")
			testkit.Len(t, c.durations(), 0, "a call for zero units must start no timer")
		})

		t.Run("waits while the time that the bucket owes exceeds a Duration", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			l := mustLimiter(t, resilience.LimiterConfig{Clock: c, Rate: 1, Burst: maxBurst})
			testkit.True(t, l.AllowN(maxBurst), "the largest bucket must hold Burst units")

			ctx, cancel := context.WithCancel(bounded(t))
			first := startWait(ctx, l, maxBurst)
			c.AwaitWaiters(1)
			second := startWait(ctx, l, maxBurst)
			c.AwaitWaiters(2)

			cancel()
			testkit.ErrorIs(t, waitResult(t, first), context.Canceled,
				"a call that owes about 146 years must wait until its context ends")
			testkit.ErrorIs(t, waitResult(t, second), context.Canceled,
				"a call past the largest Duration must wait until its context ends")
		})
	})
}

func BenchmarkLimiter(b *testing.B) {
	b.Run("AllowN", func(b *testing.B) {
		c := fake.New(originUTC)
		l := mustLimiter(b, resilience.LimiterConfig{Clock: c, Rate: math.MaxInt32, Burst: math.MaxInt32})

		var sink bool
		allocs(b, 0, func() { sink = l.AllowN(1) })
		testkit.True(b, sink, "the benchmark must measure an admitted call")
	})

	b.Run("WaitN without a wait", func(b *testing.B) {
		c := fake.New(originUTC)
		l := mustLimiter(b, resilience.LimiterConfig{Clock: c, Rate: math.MaxInt32, Burst: math.MaxInt32})
		ctx := b.Context()

		var sink error
		allocs(b, 0, func() {
			c.Advance(time.Nanosecond)
			sink = l.WaitN(ctx, 1)
		})
		testkit.NoError(b, sink, "the benchmark must measure an admitted call")
	})
}

// allocs fails b when call does not allocate want times per call,
// averaged over benchRuns calls, and then reports the time and the
// allocations of call per iteration. The check runs in the benchmark, so
// it applies to the build that a benchmark measures.
func allocs(b *testing.B, want float64, call func()) {
	b.Helper()

	if got := testing.AllocsPerRun(benchRuns, call); got != want {
		b.Fatalf("allocates %v times per call, want %v", got, want)
	}

	b.ReportAllocs()
	for b.Loop() {
		call()
	}
}
