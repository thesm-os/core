// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package clock_test

import (
	"context"
	"math"
	"testing"
	"testing/synctest"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/clock/fake"
)

// originUTC is the reference start time for tests that need a concrete
// clock origin. A fixed date keeps Wall values readable in failure output.
var originUTC = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// nonPositive generates the durations for which a timer fires at once.
var nonPositive = prop.Duration(math.MinInt64, 0)

// stoppingTimer is the one Timer of a stoppingClock. Its channel has room
// for one tick, and it counts the calls of Stop.
type stoppingTimer struct {
	tick  chan time.Time
	stops int
}

// C returns the channel of the tick.
func (t *stoppingTimer) C() <-chan time.Time { return t.tick }

// Reset reports false and schedules nothing, because no helper resets a
// timer.
func (*stoppingTimer) Reset(time.Duration) bool { return false }

// Stop counts the call and reports false.
func (t *stoppingTimer) Stop() bool {
	t.stops++

	return false
}

// stoppingClock is a Clock whose NewTimer returns its one timer, so a test
// counts the calls of Stop that a helper makes, and measures the
// allocations of the helper alone. A timer for a duration of zero or less
// has its tick ready, and the timer of any other duration never fires. Now,
// Time and Update return zero values. The zero stoppingClock is ready to
// use. It is not safe for concurrent use.
type stoppingClock struct {
	timer stoppingTimer
}

// Now returns the zero Instant.
func (*stoppingClock) Now() clock.Instant { return clock.Instant{} }

// Time returns the zero time.
func (*stoppingClock) Time() time.Time { return time.Time{} }

// Update returns the zero Instant.
func (*stoppingClock) Update(clock.Instant) clock.Instant { return clock.Instant{} }

// NewTimer returns the clock's timer, with a tick ready for a duration of
// zero or less unless one is ready already. The first call allocates the
// channel of the tick.
func (c *stoppingClock) NewTimer(d time.Duration) clock.Timer {
	if c.timer.tick == nil {
		c.timer.tick = make(chan time.Time, 1)
	}
	if d <= 0 {
		select {
		case c.timer.tick <- time.Time{}:
		default:
		}
	}

	return &c.timer
}

func TestSleep(t *testing.T) {
	t.Parallel()

	t.Run("blocks until virtual time passes the deadline", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			c := fake.New(originUTC)
			done := make(chan struct{}, 1)
			go func() {
				clock.Sleep(c, 5*time.Second)
				done <- struct{}{}
			}()
			synctest.Wait()
			assert.Length(t, done, 0, "Sleep must block before the deadline")
			c.Advance(6 * time.Second)
			synctest.Wait()
			assert.Length(t, done, 1, "Sleep must return once virtual time passes the deadline")
		})
	})

	t.Run("returns at once for a duration of zero or less", func(t *testing.T) {
		t.Parallel()
		c := fake.New(originUTC)
		prop.ForAll(t, "Sleep must not block for a duration of zero or less", func(pc *prop.Case) {
			d := pc.Draw(nonPositive, "d")
			assert.CompletesWithin(pc, time.Second, func(context.Context) error {
				clock.Sleep(c, d)

				return nil
			}, "Sleep must return without an Advance")
		})
	})
}

func TestAfter(t *testing.T) {
	t.Parallel()

	t.Run("returns a channel that delivers once virtual time passes the deadline", func(t *testing.T) {
		t.Parallel()
		c := fake.New(originUTC)
		ch := clock.After(c, 5*time.Second)
		assert.Length(t, ch, 0, "the channel must not deliver before the deadline")
		c.Advance(6 * time.Second)
		assert.Length(t, ch, 1, "the channel must deliver once virtual time passes the deadline")
	})

	t.Run("returns a channel that delivers at once for a duration of zero or less", func(t *testing.T) {
		t.Parallel()
		c := fake.New(originUTC)
		prop.ForAll(t, "After must deliver without an Advance for a duration of zero or less", func(pc *prop.Case) {
			ch := clock.After(c, pc.Draw(nonPositive, "d"))
			assert.CompletesWithin(pc, time.Second, func(ctx context.Context) error {
				select {
				case <-ch:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}, "the channel must deliver without an Advance")
		})
	})
}

func TestWait(t *testing.T) {
	t.Parallel()

	t.Run("returns nil once virtual time passes the deadline", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			c := fake.New(originUTC)
			errc := make(chan error, 1)
			go func() { errc <- clock.Wait(t.Context(), c, 5*time.Second) }()
			synctest.Wait()
			assert.Length(t, errc, 0, "Wait must block before the deadline")
			c.Advance(6 * time.Second)
			assert.NoError(t, <-errc, "Wait must return nil when the deadline passes")
		})
	})

	t.Run("returns nil for a duration of zero or less", func(t *testing.T) {
		t.Parallel()
		c := fake.New(originUTC)
		prop.NoError(t, func(d time.Duration) error { return clock.Wait(t.Context(), c, d) },
			"Wait must return nil without an Advance for a duration of zero or less",
			prop.Using(nonPositive), prop.Example(time.Duration(0)), prop.Example(-time.Second))
	})

	t.Run("returns the context's error for a cancelled context", func(t *testing.T) {
		t.Parallel()
		c := fake.New(originUTC)
		assert.HonoursCancellation(t, func(ctx context.Context) error { return clock.Wait(ctx, c, time.Hour) },
			"Wait must return the error of a cancelled context")
	})

	t.Run("returns the context's error for a passed deadline", func(t *testing.T) {
		t.Parallel()
		c := fake.New(originUTC)
		assert.HonoursDeadline(t, func(ctx context.Context) error { return clock.Wait(ctx, c, time.Hour) },
			"Wait must return the error of a passed deadline")
	})

	t.Run("returns the context's error when the context ends first", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			c := fake.New(originUTC)
			ctx, cancel := context.WithCancel(t.Context())
			errc := make(chan error, 1)
			go func() { errc <- clock.Wait(ctx, c, time.Hour) }()
			synctest.Wait()
			cancel()
			assert.ErrorIs(t, <-errc, context.Canceled, "Wait must return the error of the context that ended")
		})
	})

	t.Run("stops its timer when the deadline passes", func(t *testing.T) {
		t.Parallel()
		c := &stoppingClock{}
		assert.NoError(t, clock.Wait(t.Context(), c, 0), "Wait must return nil for a ready tick")
		assert.Equal(t, c.timer.stops, 1, "Wait must stop the timer that it created")
	})

	t.Run("stops its timer when the context ends first", func(t *testing.T) {
		t.Parallel()
		c := &stoppingClock{}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		assert.ErrorIs(t, clock.Wait(ctx, c, time.Hour), context.Canceled,
			"Wait must return the error of a cancelled context")
		assert.Equal(t, c.timer.stops, 1, "Wait must stop the timer that it created")
	})
}

// TestHelpersAllocs checks that Sleep, After and Wait allocate nothing
// besides what the NewTimer of their Clock allocates, through a
// stoppingClock, whose NewTimer allocates nothing. MaxAllocs counts the
// allocations of the whole process, so the test does not run in parallel.
//
//nolint:paralleltest // see above
func TestHelpersAllocs(t *testing.T) {
	t.Run("Sleep", func(t *testing.T) {
		c := &stoppingClock{}
		expect.MaxAllocs(t, func() { clock.Sleep(c, 0) }, 0, "Sleep must allocate nothing of its own")
		assert.Length(t, c.timer.tick, 0, "the test must measure a Sleep that took the tick")
	})

	t.Run("After", func(t *testing.T) {
		c := &stoppingClock{}

		var got <-chan time.Time
		expect.MaxAllocs(t, func() { got = clock.After(c, 0) }, 0, "After must allocate nothing of its own")
		assert.Length(t, got, 1, "the test must measure the channel of a ready tick")
	})

	t.Run("Wait", func(t *testing.T) {
		c := &stoppingClock{}
		ctx := t.Context()

		var err error
		expect.MaxAllocs(t, func() { err = clock.Wait(ctx, c, 0) }, 0, "Wait must allocate nothing of its own")
		assert.NoError(t, err, "the test must measure a wait that completes")
	})
}

// BenchmarkHelpers reports the cost of Sleep, After and Wait on a
// stoppingClock, and fails when a helper allocates anything of its own.
func BenchmarkHelpers(b *testing.B) {
	b.Run("Sleep", func(b *testing.B) {
		c := &stoppingClock{}

		bc := bench.Start(b).MaxAllocs(0)
		defer bc.End()

		for bc.Loop() {
			clock.Sleep(c, 0)
		}

		assert.Length(b, c.timer.tick, 0, "the benchmark must measure a Sleep that took the tick")
	})

	b.Run("After", func(b *testing.B) {
		c := &stoppingClock{}

		var got <-chan time.Time

		bc := bench.Start(b).MaxAllocs(0)
		defer bc.End()

		for bc.Loop() {
			got = clock.After(c, 0)
		}

		assert.Length(b, got, 1, "the benchmark must measure the channel of a ready tick")
	})

	b.Run("Wait", func(b *testing.B) {
		c := &stoppingClock{}
		ctx := b.Context()

		var err error

		bc := bench.Start(b).MaxAllocs(0)
		defer bc.End()

		for bc.Loop() {
			err = clock.Wait(ctx, c, 0)
		}

		assert.NoError(b, err, "the benchmark must measure a wait that completes")
	})
}
