// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package resilience_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/resilience"
)

var originUTC = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

const target = "inventory"

var errDependency = errors.New("resilience_test: dependency failed")

// breakerConfig is the shared shape: open after 2 consecutive
// failures, stay open 30s, close after 2 consecutive probe successes.
func breakerConfig(c *fake.Clock) resilience.BreakerConfig {
	return resilience.BreakerConfig{
		Clock:            c,
		FailureThreshold: 2,
		OpenFor:          30 * time.Second,
		SuccessThreshold: 2,
		TripOn:           []errs.Class{errs.Transient},
	}
}

func mustBreaker(tb testing.TB, c *fake.Clock) *resilience.Breaker {
	tb.Helper()

	b, err := resilience.NewBreaker(breakerConfig(c))
	testkit.NoError(tb, err, "NewBreaker must accept a complete config")

	return b
}

func TestNewBreaker(t *testing.T) {
	t.Parallel()

	valid := breakerConfig(fake.New(originUTC))

	invalid := []struct {
		name   string
		mutate func(*resilience.BreakerConfig)
	}{
		{"nil clock", func(c *resilience.BreakerConfig) { c.Clock = nil }},
		{"zero failure threshold", func(c *resilience.BreakerConfig) { c.FailureThreshold = 0 }},
		{"negative failure threshold", func(c *resilience.BreakerConfig) { c.FailureThreshold = -1 }},
		{"zero open interval", func(c *resilience.BreakerConfig) { c.OpenFor = 0 }},
		{"zero success threshold", func(c *resilience.BreakerConfig) { c.SuccessThreshold = 0 }},
		{"empty TripOn", func(c *resilience.BreakerConfig) { c.TripOn = nil }},
	}
	for _, tc := range invalid {
		t.Run("rejects a "+tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := valid
			tc.mutate(&cfg)
			_, err := resilience.NewBreaker(cfg)
			testkit.ErrorIs(t, err, resilience.ErrConfig, "an incomplete config must be rejected")
		})
	}

	t.Run("accepts a complete config", func(t *testing.T) {
		t.Parallel()
		_, err := resilience.NewBreaker(breakerConfig(fake.New(originUTC)))
		testkit.NoError(t, err, "a complete config must be accepted")
	})
}

func TestBreakerOpens(t *testing.T) {
	t.Parallel()

	t.Run("stays closed below the threshold", func(t *testing.T) {
		t.Parallel()
		b := mustBreaker(t, fake.New(originUTC))

		testkit.True(t, b.Allow(target), "a fresh circuit must allow")
		b.Record(target, true)

		testkit.True(t, b.Allow(target), "one failure must not open the circuit")
		testkit.Equal(t, b.State(target), resilience.Closed, "the circuit must still be closed")
	})

	t.Run("opens at the threshold", func(t *testing.T) {
		t.Parallel()
		b := mustBreaker(t, fake.New(originUTC))

		for range 2 {
			testkit.True(t, b.Allow(target), "the circuit must allow until it opens")
			b.Record(target, true)
		}

		testkit.False(t, b.Allow(target), "the circuit must refuse once open")
		testkit.Equal(t, b.State(target), resilience.Open, "State must report Open")
	})

	t.Run("a success resets the failure count", func(t *testing.T) {
		t.Parallel()
		// Consecutive failures, not cumulative: a dependency that
		// fails, succeeds, fails is not down.
		b := mustBreaker(t, fake.New(originUTC))

		b.Allow(target)
		b.Record(target, true)
		b.Allow(target)
		b.Record(target, false)
		b.Allow(target)
		b.Record(target, true)

		testkit.True(t, b.Allow(target), "an interleaved success must reset the count")
	})

	t.Run("circuits are independent per target", func(t *testing.T) {
		t.Parallel()
		// One dependency being down says nothing about another.
		b := mustBreaker(t, fake.New(originUTC))

		for range 2 {
			b.Allow("failing")
			b.Record("failing", true)
		}

		testkit.False(t, b.Allow("failing"), "the failing circuit must be open")
		testkit.True(t, b.Allow("healthy"), "an unrelated circuit must be unaffected")
		testkit.Equal(t, b.State("healthy"), resilience.Closed, "the unrelated circuit is closed")
	})
}

func TestBreakerHalfOpen(t *testing.T) {
	t.Parallel()

	// open drives a circuit to Open and returns the clock driving it.
	open := func(t *testing.T) (*resilience.Breaker, *fake.Clock) {
		t.Helper()
		c := fake.New(originUTC)
		b := mustBreaker(t, c)
		for range 2 {
			b.Allow(target)
			b.Record(target, true)
		}
		testkit.False(t, b.Allow(target), "the circuit must be open")

		return b, c
	}

	t.Run("stays open until the interval elapses", func(t *testing.T) {
		t.Parallel()
		b, c := open(t)

		c.Advance(29 * time.Second)
		testkit.False(t, b.Allow(target), "the circuit must stay open before the interval")

		c.Advance(2 * time.Second)
		testkit.True(t, b.Allow(target), "the circuit must admit a probe after the interval")
	})

	t.Run("admits exactly one probe", func(t *testing.T) {
		t.Parallel()
		// Without the claim, "the interval elapsed" would release the
		// full load at once — indistinguishable from never having
		// opened, for a dependency still down.
		b, c := open(t)
		c.Advance(31 * time.Second)

		testkit.True(t, b.Allow(target), "the first caller must take the probe slot")
		testkit.False(t, b.Allow(target), "a second caller must be refused")
		testkit.False(t, b.Allow(target), "and a third")
		testkit.Equal(t, b.State(target), resilience.HalfOpen, "State must report HalfOpen")
	})

	t.Run("a failed probe re-opens for the full interval", func(t *testing.T) {
		t.Parallel()
		b, c := open(t)
		c.Advance(31 * time.Second)

		testkit.True(t, b.Allow(target), "the probe must be admitted")
		b.Record(target, true)

		testkit.False(t, b.Allow(target), "a failed probe must re-open the circuit")
		c.Advance(29 * time.Second)
		testkit.False(t, b.Allow(target), "the full interval must elapse again")
		c.Advance(2 * time.Second)
		testkit.True(t, b.Allow(target), "and then admit another probe")
	})

	t.Run("closes after consecutive probe successes", func(t *testing.T) {
		t.Parallel()
		b, c := open(t)

		for range 2 {
			c.Advance(31 * time.Second)
			testkit.True(t, b.Allow(target), "each probe must be admitted")
			b.Record(target, false)
		}

		testkit.Equal(t, b.State(target), resilience.Closed, "the circuit must close")
		testkit.True(t, b.Allow(target), "and admit freely")
		testkit.True(t, b.Allow(target), "without claiming a probe slot")
	})

	t.Run("a failure resets the probe successes", func(t *testing.T) {
		t.Parallel()
		// A dependency answering one request and falling over must
		// not get the whole traffic back.
		b, c := open(t)

		c.Advance(31 * time.Second)
		b.Allow(target)
		b.Record(target, false)

		c.Advance(31 * time.Second)
		b.Allow(target)
		b.Record(target, true)

		testkit.False(t, b.Allow(target), "the circuit must re-open, not close")
	})
}

func TestBreakerRecord(t *testing.T) {
	t.Parallel()

	t.Run("recording an unknown target is a no-op", func(t *testing.T) {
		t.Parallel()
		b := mustBreaker(t, fake.New(originUTC))
		b.Record("never-allowed", true)
		testkit.Equal(t, b.State("never-allowed"), resilience.Closed,
			"a target with no circuit must stay closed")
	})

	t.Run("State of an unknown target is Closed", func(t *testing.T) {
		t.Parallel()
		b := mustBreaker(t, fake.New(originUTC))
		testkit.Equal(t, b.State("unknown"), resilience.Closed,
			"a target never called must report Closed")
	})

	t.Run("State reports HalfOpen once the interval elapses", func(t *testing.T) {
		t.Parallel()
		// Before any caller claims the probe slot. State describes
		// what the next caller will find, not what has happened.
		c := fake.New(originUTC)
		b := mustBreaker(t, c)
		for range 2 {
			b.Allow(target)
			b.Record(target, true)
		}
		testkit.Equal(t, b.State(target), resilience.Open, "the circuit must be open")

		c.Advance(31 * time.Second)
		testkit.Equal(t, b.State(target), resilience.HalfOpen,
			"an elapsed interval must report HalfOpen before the probe is claimed")
	})
}

func TestStateString(t *testing.T) {
	t.Parallel()

	cases := []struct {
		state resilience.State
		want  string
	}{
		{resilience.Closed, "Closed"},
		{resilience.Open, "Open"},
		{resilience.HalfOpen, "HalfOpen"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			testkit.Equal(t, tc.state.String(), tc.want, "String must name the state")
		})
	}

	t.Run("an out-of-range value renders numerically", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, resilience.State(99).String(), "State(99)",
			"an unrecognised state must stay distinguishable in a log line")
	})

	t.Run("Closed is the zero value", func(t *testing.T) {
		t.Parallel()
		// A circuit with no history must read as passing traffic.
		var zero resilience.State
		testkit.Equal(t, zero, resilience.Closed, "the zero State must be Closed")
	})
}

func TestBreakerCall(t *testing.T) {
	t.Parallel()

	t.Run("returns the function's value", func(t *testing.T) {
		t.Parallel()
		b := mustBreaker(t, fake.New(originUTC))

		got, err := resilience.Call(t.Context(), b, target,
			func(context.Context) (int, error) { return 42, nil })

		testkit.NoError(t, err, "Call must succeed")
		testkit.Equal(t, got, 42, "Call must return the function's value")
	})

	t.Run("a tripping class opens the circuit", func(t *testing.T) {
		t.Parallel()
		b := mustBreaker(t, fake.New(originUTC))
		transient := errs.WithClass(errDependency, errs.Transient)

		for range 2 {
			_, err := resilience.Call(t.Context(), b, target,
				func(context.Context) (int, error) { return 0, transient })
			testkit.ErrorIs(t, err, errDependency, "the dependency's error must reach the caller")
		}

		_, err := resilience.Call(t.Context(), b, target,
			func(context.Context) (int, error) { return 42, nil })
		testkit.ErrorIs(t, err, resilience.ErrOpen, "the circuit must be open")
	})

	t.Run("a non-tripping class does not open the circuit", func(t *testing.T) {
		t.Parallel()
		// A dependency correctly rejecting a bad request is not a
		// failing dependency.
		b := mustBreaker(t, fake.New(originUTC))
		invalid := errs.WithClass(errDependency, errs.Invalid)

		for range 5 {
			_, _ = resilience.Call(t.Context(), b, target,
				func(context.Context) (int, error) { return 0, invalid })
		}

		testkit.Equal(t, b.State(target), resilience.Closed,
			"a class outside TripOn must not open the circuit")
	})

	t.Run("ErrOpen classifies as Transient", func(t *testing.T) {
		t.Parallel()
		// So a retry wrapped around a breaker backs off rather than
		// giving up.
		testkit.Equal(t, errs.Classify(resilience.ErrOpen), errs.Transient,
			"ErrOpen must be retryable")
	})

	t.Run("a cancelled caller does not count as a failure", func(t *testing.T) {
		t.Parallel()
		// The dependency did not fail; the caller stopped waiting.
		// Counting it would open a circuit against a healthy
		// dependency during a cancellation storm.
		b := mustBreaker(t, fake.New(originUTC))
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		for range 5 {
			_, _ = resilience.Call(ctx, b, target,
				func(context.Context) (int, error) {
					return 0, errs.WithClass(errDependency, errs.Transient)
				})
		}

		testkit.Equal(t, b.State(target), resilience.Closed,
			"cancellation must not open the circuit")
	})

	t.Run("does not call fn when the circuit is open", func(t *testing.T) {
		t.Parallel()
		b := mustBreaker(t, fake.New(originUTC))
		transient := errs.WithClass(errDependency, errs.Transient)

		for range 2 {
			_, _ = resilience.Call(t.Context(), b, target,
				func(context.Context) (int, error) { return 0, transient })
		}

		called := false
		_, err := resilience.Call(t.Context(), b, target,
			func(context.Context) (int, error) { called = true; return 0, nil })

		testkit.ErrorIs(t, err, resilience.ErrOpen, "the call must be refused")
		testkit.False(t, called, "an open circuit must not reach the dependency")
	})
}

func TestBreakerConcurrent(t *testing.T) {
	t.Parallel()

	// Exactly one probe may be admitted no matter how many callers
	// arrive at the moment the interval elapses.
	c := fake.New(originUTC)
	b := mustBreaker(t, c)

	for range 2 {
		b.Allow(target)
		b.Record(target, true)
	}
	c.Advance(31 * time.Second)

	var (
		mu      sync.Mutex
		allowed int
		wg      sync.WaitGroup
	)
	for range 64 {
		wg.Go(func() {
			if b.Allow(target) {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	testkit.Equal(t, allowed, 1, "exactly one caller may take the probe slot")
}

func BenchmarkBreakerAllow(b *testing.B) {
	br, err := resilience.NewBreaker(breakerConfig(fake.New(originUTC)))
	testkit.NoError(b, err, "NewBreaker must succeed")
	b.ReportAllocs()

	for b.Loop() {
		br.Allow(target)
		br.Record(target, false)
	}
}
