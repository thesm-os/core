// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package resilience_test

import (
	"context"
	"runtime"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/constant"
	"go.thesmos.sh/core/resilience"
)

// bounded is the context for every blocking call that is expected to
// succeed.
//
// Do and Acquire block until released or until their context is done,
// and t.Context() is only cancelled once the test COMPLETES — so a
// call that never returns prevents the very cancellation that would
// release it, and the binary hangs until go test gives up, attributed
// to whichever test was running. A real-time deadline turns that
// deadlock into a named failure against the test that caused it.
//
// The bound is real time, not virtual: it measures the process's
// patience with a call that will never return, which no amount of
// fake-clock advancement can affect. Tests that deliberately cancel
// or deliberately block keep their own contexts.
//
// One second, not five. A legitimate Do or Acquire here completes in
// microseconds — every wait is a fake clock or an already-free
// permit — so the margin is still five orders of magnitude. The
// ceiling matters more than the floor: a mutant escapes through this
// bound, and the escape must land well inside gremlins' per-mutant
// budget (coefficient x a ~0.2s baseline) even when two bounded
// subtests trip in sequence. At five seconds that race was real and
// the mutation run flaked; at one it is not close.
func bounded(tb testing.TB) context.Context {
	tb.Helper()

	ctx, cancel := context.WithTimeout(tb.Context(), time.Second)
	tb.Cleanup(cancel)

	return ctx
}

// noJitter draws the bottom of every backoff interval, so a retry
// proceeds without the test having to advance virtual time.
//
// Typed as the interface so the boxing happens once here rather than
// on every call, which would otherwise show up as an allocation
// against Backoff in the benchmark.
var noJitter rand.Rand = constant.FromFloat64(0)

// halfJitter draws the midpoint, so a backoff is a known duration.
var halfJitter rand.Rand = constant.FromFloat64(0.5)

// errTransient is retryable; errPermanent is not.
var (
	errTransient = errs.WithClass(testkit.TestError("transient"), errs.Transient)
	errPermanent = errs.WithClass(testkit.TestError("permanent"), errs.Invalid)
)

func retryConfig(c clock.Clock, r rand.Rand) resilience.RetryConfig {
	return resilience.RetryConfig{
		Clock:    c,
		Rand:     r,
		Attempts: 3,
		Base:     100 * time.Millisecond,
		Max:      time.Second,
		// Two retries per call: enough that the attempt count is what
		// bounds these tests, not the budget. TestDoBudget lowers it.
		Budget:       2,
		MinRetries:   0,
		BudgetWindow: time.Minute,
	}
}

func mustRetrier(tb testing.TB, cfg resilience.RetryConfig) *resilience.Retrier {
	tb.Helper()

	r, err := resilience.NewRetrier(cfg)
	testkit.NoError(tb, err, "NewRetrier must accept a valid config")

	return r
}

// failFor returns a function that fails n times and then succeeds,
// counting its calls.
func failFor(n int, err error) (fn func(context.Context) (int, error), calls *int) {
	calls = new(int)

	return func(context.Context) (int, error) {
		*calls++
		if *calls <= n {
			return 0, err
		}

		return *calls, nil
	}, calls
}

func TestNewRetrier(t *testing.T) {
	t.Parallel()

	invalid := []struct {
		name  string
		spoil func(*resilience.RetryConfig)
	}{
		{"nil clock", func(c *resilience.RetryConfig) { c.Clock = nil }},
		{"nil rand", func(c *resilience.RetryConfig) { c.Rand = nil }},
		{"zero attempts", func(c *resilience.RetryConfig) { c.Attempts = 0 }},
		{"negative attempts", func(c *resilience.RetryConfig) { c.Attempts = -1 }},
		{"zero base", func(c *resilience.RetryConfig) { c.Base = 0 }},
		{"zero max", func(c *resilience.RetryConfig) { c.Max = 0 }},
		{"max below base", func(c *resilience.RetryConfig) { c.Max = c.Base - 1 }},
		{"negative budget", func(c *resilience.RetryConfig) { c.Budget = -1 }},
		{"negative floor", func(c *resilience.RetryConfig) { c.MinRetries = -1 }},
		{"budget without a window", func(c *resilience.RetryConfig) { c.BudgetWindow = 0 }},
		{"a floor without a window", func(c *resilience.RetryConfig) {
			c.Budget, c.MinRetries, c.BudgetWindow = 0, 1, 0
		}},
	}
	for _, tc := range invalid {
		t.Run("rejects a "+tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := retryConfig(fake.New(originUTC), noJitter)
			tc.spoil(&cfg)

			_, err := resilience.NewRetrier(cfg)
			testkit.ErrorIs(t, err, resilience.ErrConfig, "an invalid config must be rejected")
		})
	}

	t.Run("accepts a cap equal to the base", func(t *testing.T) {
		t.Parallel()
		// Max must be >= Base, not > Base: a caller wanting a constant
		// interval rather than an exponential one sets them equal.
		cfg := retryConfig(fake.New(originUTC), noJitter)
		cfg.Max = cfg.Base

		_, err := resilience.NewRetrier(cfg)
		testkit.NoError(t, err, "an equal cap and base is a constant interval")
	})

	t.Run("accepts no allowance at all without a window", func(t *testing.T) {
		t.Parallel()
		// A zero budget and a zero floor disable retrying, so there is
		// no window to measure them over.
		cfg := retryConfig(fake.New(originUTC), noJitter)
		cfg.Budget, cfg.MinRetries, cfg.BudgetWindow = 0, 0, 0

		_, err := resilience.NewRetrier(cfg)
		testkit.NoError(t, err, "no allowance needs no window")
	})
}

func TestDo(t *testing.T) {
	t.Parallel()

	t.Run("a success is not retried", func(t *testing.T) {
		t.Parallel()
		r := mustRetrier(t, retryConfig(fake.New(originUTC), noJitter))
		fn, calls := failFor(0, errTransient)

		got, err := resilience.Do(bounded(t), r, fn)
		testkit.NoError(t, err, "a successful call must return its value")
		testkit.Equal(t, got, 1, "the value must be the one fn returned")
		testkit.Equal(t, *calls, 1, "a success must not be retried")
	})

	t.Run("a transient failure is retried", func(t *testing.T) {
		t.Parallel()
		r := mustRetrier(t, retryConfig(fake.New(originUTC), noJitter))
		fn, calls := failFor(1, errTransient)

		got, err := resilience.Do(bounded(t), r, fn)
		testkit.NoError(t, err, "a retried call that succeeds must return its value")
		testkit.Equal(t, got, 2, "the value must come from the successful attempt")
		testkit.Equal(t, *calls, 2, "one retry must have been spent")
	})

	t.Run("a non-retryable failure stops at once", func(t *testing.T) {
		t.Parallel()
		// Retrying an error the producer has classified as the
		// caller's own fault burns the budget on a call that cannot
		// succeed.
		r := mustRetrier(t, retryConfig(fake.New(originUTC), noJitter))
		fn, calls := failFor(3, errPermanent)

		_, err := resilience.Do(bounded(t), r, fn)
		testkit.ErrorIs(t, err, errPermanent, "the error must reach the caller unchanged")
		testkit.Equal(t, *calls, 1, "a non-retryable error must not be retried")
	})

	t.Run("an unclassified failure stops at once", func(t *testing.T) {
		t.Parallel()
		// Unspecified is not retryable: retrying an error nobody has
		// reasoned about is a guess.
		r := mustRetrier(t, retryConfig(fake.New(originUTC), noJitter))
		fn, calls := failFor(3, testkit.TestError("unclassified"))

		_, err := resilience.Do(bounded(t), r, fn)
		testkit.Error(t, err, "the failure must reach the caller")
		testkit.Equal(t, *calls, 1, "an unclassified error must not be retried")
	})

	t.Run("attempts are exhausted", func(t *testing.T) {
		t.Parallel()
		r := mustRetrier(t, retryConfig(fake.New(originUTC), noJitter))
		fn, calls := failFor(99, errTransient)

		_, err := resilience.Do(bounded(t), r, fn)
		testkit.ErrorIs(t, err, errTransient, "the last error must reach the caller")
		testkit.Equal(t, *calls, 3, "Attempts counts calls, not retries")
	})
}

func TestDoContext(t *testing.T) {
	t.Parallel()

	t.Run("a cancelled context stops the retry", func(t *testing.T) {
		t.Parallel()
		// The dependency did not refuse; the caller stopped asking.
		r := mustRetrier(t, retryConfig(fake.New(originUTC), noJitter))

		ctx, cancel := context.WithCancel(t.Context())
		calls := 0
		_, err := resilience.Do(ctx, r, func(context.Context) (int, error) {
			calls++
			cancel()

			return 0, errTransient
		})

		testkit.ErrorIs(t, err, errTransient, "the attempt's own error must reach the caller")
		testkit.Equal(t, calls, 1, "a call whose context ended must not be retried")
	})

	t.Run("a context ending during the backoff stops the retry", func(t *testing.T) {
		t.Parallel()
		c := fake.New(originUTC)
		r := mustRetrier(t, retryConfig(c, halfJitter))
		fn, calls := failFor(99, errTransient)

		ctx, cancel := context.WithCancel(t.Context())
		got := make(chan error, 1)
		go func() {
			_, err := resilience.Do(ctx, r, fn)
			got <- err
		}()

		c.AwaitWaiters(1)
		cancel()

		select {
		case err := <-got:
			testkit.ErrorIs(t, err, context.Canceled,
				"a wait cut short by the context must report the context")
			testkit.Equal(t, *calls, 1, "the next attempt must not run")
		case <-time.After(time.Second):
			t.Fatal("Do never returned")
		}
	})

	t.Run("the backoff elapses before the next attempt", func(t *testing.T) {
		t.Parallel()
		c := fake.New(originUTC)
		r := mustRetrier(t, retryConfig(c, halfJitter))
		fn, calls := failFor(1, errTransient)

		got := make(chan error, 1)
		go func() {
			_, err := resilience.Do(bounded(t), r, fn)
			got <- err
		}()

		c.AwaitWaiters(1)
		testkit.Equal(t, *calls, 1, "the retry must wait rather than fire at once")
		c.Advance(50 * time.Millisecond)

		select {
		case err := <-got:
			testkit.NoError(t, err, "the retry must run once its backoff elapsed")
			testkit.Equal(t, *calls, 2, "exactly one retry must have run")
		case <-time.After(time.Second):
			t.Fatal("Do never returned")
		}
	})
}

func TestDoBudget(t *testing.T) {
	t.Parallel()

	// Budget 0.5 allows one retry for every two calls in the window,
	// with no floor beneath it.
	halfBudget := func(c clock.Clock) resilience.RetryConfig {
		cfg := retryConfig(c, noJitter)
		cfg.Budget, cfg.MinRetries = 0.5, 0

		return cfg
	}

	t.Run("a retry is refused without the traffic to pay for it", func(t *testing.T) {
		t.Parallel()
		r := mustRetrier(t, halfBudget(fake.New(originUTC)))
		fn, calls := failFor(99, errTransient)

		_, err := resilience.Do(bounded(t), r, fn)
		testkit.ErrorIs(t, err, resilience.ErrBudget, "the refusal must name the budget")
		testkit.ErrorIs(t, err, errTransient, "the failure that prompted it must survive")
		testkit.Equal(t, *calls, 1, "the retry must not have run")
	})

	t.Run("accumulated calls pay for a retry", func(t *testing.T) {
		t.Parallel()
		r := mustRetrier(t, halfBudget(fake.New(originUTC)))

		succeed, _ := failFor(0, errTransient)
		for range 2 {
			_, err := resilience.Do(bounded(t), r, succeed)
			testkit.NoError(t, err, "the priming calls must succeed")
		}

		fn, calls := failFor(1, errTransient)
		_, err := resilience.Do(bounded(t), r, fn)
		testkit.NoError(t, err, "three calls in the window pay for one retry")
		testkit.Equal(t, *calls, 2, "the retry must have run")
	})

	t.Run("the window forgets", func(t *testing.T) {
		t.Parallel()
		// The budget measures the recent call rate. Traffic that has
		// aged out of the window cannot pay for a retry now.
		c := fake.New(originUTC)
		r := mustRetrier(t, halfBudget(c))

		succeed, _ := failFor(0, errTransient)
		for range 2 {
			_, err := resilience.Do(bounded(t), r, succeed)
			testkit.NoError(t, err, "the priming calls must succeed")
		}

		c.Advance(2 * time.Minute)

		fn, calls := failFor(1, errTransient)
		_, err := resilience.Do(bounded(t), r, fn)
		testkit.ErrorIs(t, err, resilience.ErrBudget, "aged-out traffic must not pay")
		testkit.Equal(t, *calls, 1, "the retry must not have run")
	})

	t.Run("a partial roll keeps the traffic still in the window", func(t *testing.T) {
		t.Parallel()
		c := fake.New(originUTC)
		r := mustRetrier(t, halfBudget(c))

		succeed, _ := failFor(0, errTransient)
		for range 2 {
			_, err := resilience.Do(bounded(t), r, succeed)
			testkit.NoError(t, err, "the priming calls must succeed")
		}

		// Well inside the window: the buckets roll, the counts stay.
		c.Advance(10 * time.Second)

		fn, calls := failFor(1, errTransient)
		_, err := resilience.Do(bounded(t), r, fn)
		testkit.NoError(t, err, "traffic still inside the window must pay")
		testkit.Equal(t, *calls, 2, "the retry must have run")
	})

	t.Run("the window forgets one bucket at a time", func(t *testing.T) {
		t.Parallel()
		// A live system's time arrives in small increments, not in one
		// jump past the whole window. Traffic must age out either way.
		c := fake.New(originUTC)
		r := mustRetrier(t, halfBudget(c))

		succeed, _ := failFor(0, errTransient)
		for range 2 {
			_, err := resilience.Do(bounded(t), r, succeed)
			testkit.NoError(t, err, "the priming calls must succeed")
		}

		// One bucket at a time, all the way round the ring.
		for range 12 {
			c.Advance(5 * time.Second)
		}

		fn, calls := failFor(1, errTransient)
		_, err := resilience.Do(bounded(t), r, fn)
		testkit.ErrorIs(t, err, resilience.ErrBudget, "a full lap must clear the traffic")
		testkit.Equal(t, *calls, 1, "the retry must not have run")
	})

	t.Run("repeated rolls age the window only once", func(t *testing.T) {
		t.Parallel()
		// The ring advances from where it left off. A roll that lost
		// its place would re-age the same traffic on every call and
		// empty the window long before its time.
		c := fake.New(originUTC)
		r := mustRetrier(t, halfBudget(c))

		succeed, _ := failFor(0, errTransient)
		for range 2 {
			_, err := resilience.Do(bounded(t), r, succeed)
			testkit.NoError(t, err, "the priming calls must succeed")
		}

		// Six advances of one bucket each: half the window, so the
		// priming traffic is still well inside it.
		for range 6 {
			c.Advance(5 * time.Second)
		}

		fn, calls := failFor(1, errTransient)
		_, err := resilience.Do(bounded(t), r, fn)
		testkit.NoError(t, err, "traffic still inside the window must pay")
		testkit.Equal(t, *calls, 2, "the retry must have run")
	})

	t.Run("no budget and no floor disables retrying", func(t *testing.T) {
		t.Parallel()
		cfg := retryConfig(fake.New(originUTC), noJitter)
		cfg.Budget, cfg.MinRetries, cfg.BudgetWindow = 0, 0, 0
		r := mustRetrier(t, cfg)

		fn, calls := failFor(99, errTransient)
		_, err := resilience.Do(bounded(t), r, fn)
		testkit.ErrorIs(t, err, resilience.ErrBudget, "no allowance means no retry")
		testkit.Equal(t, *calls, 1, "the retry must not have run")
	})
}

func TestDoMinRetries(t *testing.T) {
	t.Parallel()

	// A floor of one retry beneath a ratio that cannot pay for one.
	floored := func(c clock.Clock) resilience.RetryConfig {
		cfg := retryConfig(c, noJitter)
		cfg.Budget, cfg.MinRetries, cfg.Attempts = 0.5, 1, 4

		return cfg
	}

	t.Run("the floor pays where the ratio cannot", func(t *testing.T) {
		t.Parallel()
		// Without MinRetries this same call is refused outright: one
		// call cannot pay for one retry at any fraction under 1.0.
		r := mustRetrier(t, floored(fake.New(originUTC)))
		fn, calls := failFor(1, errTransient)

		_, err := resilience.Do(bounded(t), r, fn)
		testkit.NoError(t, err, "the floor must cover the first retry")
		testkit.Equal(t, *calls, 2, "the retry must have run")
	})

	t.Run("the floor is a floor, not an allowance per call", func(t *testing.T) {
		t.Parallel()
		// Attempts would allow three retries; the window affords one.
		r := mustRetrier(t, floored(fake.New(originUTC)))
		fn, calls := failFor(99, errTransient)

		_, err := resilience.Do(bounded(t), r, fn)
		testkit.ErrorIs(t, err, resilience.ErrBudget, "the second retry must exceed the floor")
		testkit.Equal(t, *calls, 2, "exactly one retry must have run")
	})

	t.Run("the ratio takes over once the traffic is there", func(t *testing.T) {
		t.Parallel()
		// Eleven calls at 0.5 afford five retries, well above the floor.
		r := mustRetrier(t, floored(fake.New(originUTC)))

		succeed, _ := failFor(0, errTransient)
		for range 10 {
			_, err := resilience.Do(bounded(t), r, succeed)
			testkit.NoError(t, err, "the priming calls must succeed")
		}

		fn, calls := failFor(2, errTransient)
		_, err := resilience.Do(bounded(t), r, fn)
		testkit.NoError(t, err, "the ratio must pay for more than the floor")
		testkit.Equal(t, *calls, 3, "both retries must have run")
	})

	t.Run("the floor ages out with the window", func(t *testing.T) {
		t.Parallel()
		// A floor that never reset would be an unbounded retry
		// allowance spread thinly over time.
		c := fake.New(originUTC)
		r := mustRetrier(t, floored(c))

		spend, _ := failFor(1, errTransient)
		_, err := resilience.Do(bounded(t), r, spend)
		testkit.NoError(t, err, "the floor must cover the first retry")

		c.Advance(2 * time.Minute)

		fn, calls := failFor(1, errTransient)
		_, err = resilience.Do(bounded(t), r, fn)
		testkit.NoError(t, err, "a fresh window must restore the floor")
		testkit.Equal(t, *calls, 2, "the retry must have run")
	})
}

func TestBackoff(t *testing.T) {
	t.Parallel()

	const (
		base = 100 * time.Millisecond
		peak = time.Second
	)

	t.Run("there is no delay before the first attempt", func(t *testing.T) {
		t.Parallel()
		for _, attempt := range []int{-1, 0} {
			got := resilience.Backoff(halfJitter, attempt, base, peak)
			testkit.Equal(t, got, time.Duration(0), "nothing has failed yet")
		}
	})

	t.Run("the ceiling doubles per retry", func(t *testing.T) {
		t.Parallel()
		// halfJitter draws the midpoint, so the result is half the
		// ceiling and the growth is visible.
		want := []time.Duration{
			50 * time.Millisecond,
			100 * time.Millisecond,
			200 * time.Millisecond,
			400 * time.Millisecond,
		}
		for i, w := range want {
			got := resilience.Backoff(halfJitter, i+1, base, peak)
			testkit.Equal(t, got, w, "the ceiling must double with each retry")
		}
	})

	t.Run("the ceiling is capped", func(t *testing.T) {
		t.Parallel()
		for _, attempt := range []int{5, 6, 40, 1 << 20} {
			got := resilience.Backoff(halfJitter, attempt, base, peak)
			testkit.Equal(t, got, peak/2, "an unbounded ceiling would overflow")
		}
	})

	t.Run("a base above the cap is capped", func(t *testing.T) {
		t.Parallel()
		got := resilience.Backoff(halfJitter, 1, 10*peak, peak)
		testkit.Equal(t, got, peak/2, "the cap wins over the base")
	})

	t.Run("jitter spans the whole interval", func(t *testing.T) {
		t.Parallel()
		// Full jitter, not a fixed fraction: an unjittered backoff
		// keeps a fleet's retries synchronised, which is the failure
		// backoff exists to prevent.
		testkit.Equal(t, resilience.Backoff(noJitter, 1, base, peak), time.Duration(0),
			"the bottom of the interval must be reachable")
		testkit.True(t, resilience.Backoff(constant.FromFloat64(0.99), 1, base, peak) > 98*time.Millisecond,
			"the top of the interval must be reachable")
	})
}

func BenchmarkBackoff(b *testing.B) {
	var sink time.Duration

	b.ReportAllocs()
	for b.Loop() {
		sink = resilience.Backoff(halfJitter, 4, 100*time.Millisecond, time.Second)
	}

	runtime.KeepAlive(sink)
}
