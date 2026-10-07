// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package resilience_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/resilience"
)

// target is the dependency of the circuits of the cases.
const target = "inventory"

// The interval after which an open circuit of circuits admits a probe, and
// the advance of a case that passes it.
const (
	openFor = 30 * time.Second
	elapsed = openFor + time.Second
)

// originUTC is the time of the fake clocks of the cases.
var originUTC = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// The failures of the dependencies of the cases.
var (
	errDependency = errors.New("resilience_test: dependency failed")

	// errTripping is a failure whose class trips the circuits of circuits.
	errTripping = errs.WithClass(errDependency, errs.Transient)
)

// circuits configures the Breakers of the cases without a clock: a circuit
// opens after 2 consecutive failures, refuses calls for 30 s, and closes
// after 2 consecutive probe successes.
var circuits = resilience.BreakerConfig{
	FailureThreshold: 2,
	SuccessThreshold: 2,
	OpenFor:          openFor,
	TripOn:           []errs.Class{errs.Transient},
}

func TestState(t *testing.T) {
	t.Parallel()

	t.Run("String", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give resilience.State
			want string
		}{
			{name: "returns Closed for Closed", give: resilience.Closed, want: "Closed"},
			{name: "returns Open for Open", give: resilience.Open, want: "Open"},
			{name: "returns HalfOpen for HalfOpen", give: resilience.HalfOpen, want: "HalfOpen"},
			{name: "returns the number of a value outside the states", give: resilience.State(99), want: "State(99)"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, tt.give.String(), tt.want, "String must name the state")
			})
		}
	})

	t.Run("has Closed as its zero value", func(t *testing.T) {
		t.Parallel()
		var zero resilience.State
		assert.Equal(t, zero, resilience.Closed, "the zero State must be Closed")
	})
}

func TestBreaker(t *testing.T) {
	t.Parallel()

	t.Run("NewBreaker", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			edit func(*resilience.BreakerConfig)
			name string
		}{
			{name: "returns ErrConfig for a nil Clock", edit: func(c *resilience.BreakerConfig) { c.Clock = nil }},
			{name: "returns ErrConfig for an empty TripOn", edit: func(c *resilience.BreakerConfig) { c.TripOn = nil }},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				cfg := circuits
				cfg.Clock = fake.New(originUTC)
				tt.edit(&cfg)

				_, err := resilience.NewBreaker(cfg)
				expect.ErrorIs(t, err, resilience.ErrConfig, "NewBreaker must refuse the config")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "ErrConfig must classify as Invalid")
			})
		}

		t.Run("returns ErrConfig for a FailureThreshold that is not positive", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(n int) error {
				cfg := circuits
				cfg.Clock, cfg.FailureThreshold = fake.New(originUTC), n
				_, err := resilience.NewBreaker(cfg)

				return err
			}, resilience.ErrConfig, "NewBreaker must refuse a FailureThreshold that is not positive",
				prop.Using(prop.Integer(math.MinInt, 0)), prop.Example(0))
		})

		t.Run("returns ErrConfig for a SuccessThreshold that is not positive", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(n int) error {
				cfg := circuits
				cfg.Clock, cfg.SuccessThreshold = fake.New(originUTC), n
				_, err := resilience.NewBreaker(cfg)

				return err
			}, resilience.ErrConfig, "NewBreaker must refuse a SuccessThreshold that is not positive",
				prop.Using(prop.Integer(math.MinInt, 0)), prop.Example(0))
		})

		t.Run("returns ErrConfig for an OpenFor that is not positive", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(d time.Duration) error {
				cfg := circuits
				cfg.Clock, cfg.OpenFor = fake.New(originUTC), d
				_, err := resilience.NewBreaker(cfg)

				return err
			}, resilience.ErrConfig, "NewBreaker must refuse an OpenFor that is not positive",
				prop.Using(prop.Duration(math.MinInt64, 0)), prop.Example(time.Duration(0)))
		})

		t.Run("returns a Breaker for a complete config", func(t *testing.T) {
			t.Parallel()
			cfg := circuits
			cfg.Clock = fake.New(originUTC)

			_, err := resilience.NewBreaker(cfg)
			assert.NoError(t, err, "NewBreaker must accept a complete config")
		})
	})

	t.Run("Allow", func(t *testing.T) {
		t.Parallel()

		t.Run("admits a call below the failure threshold", func(t *testing.T) {
			t.Parallel()
			b := newBreaker(t, fake.New(originUTC))
			assert.True(t, b.Allow(target), "a new circuit must admit a call")
			b.Record(target, true)

			assert.True(t, b.Allow(target), "one failure must not open the circuit")
		})

		t.Run("refuses a call at the failure threshold", func(t *testing.T) {
			t.Parallel()
			b := newBreaker(t, fake.New(originUTC))
			trip(t, b)

			assert.False(t, b.Allow(target), "an open circuit must refuse a call")
		})

		t.Run("keeps a circuit for each target", func(t *testing.T) {
			t.Parallel()
			b := newBreaker(t, fake.New(originUTC))
			trip(t, b)

			assert.True(t, b.Allow("healthy"), "the circuit of another target must admit a call")
		})

		t.Run("refuses a call until the open interval elapses", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			b := newBreaker(t, c)
			trip(t, b)

			c.Advance(openFor - time.Second)
			assert.False(t, b.Allow(target), "the circuit must refuse a call before the interval ends")
		})

		t.Run("admits a probe after the open interval", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			b := newBreaker(t, c)
			trip(t, b)

			c.Advance(elapsed)
			assert.True(t, b.Allow(target), "the circuit must admit a probe after the interval")
		})

		t.Run("refuses every call beside the probe", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			b := newBreaker(t, c)
			trip(t, b)

			c.Advance(elapsed)
			assert.True(t, b.Allow(target), "the first caller must take the probe")
			expect.False(t, b.Allow(target), "a second caller must be refused")
			expect.False(t, b.Allow(target), "a third caller must be refused")
		})

		t.Run("admits one probe across concurrent callers", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			b := newBreaker(t, c)
			trip(t, b)
			c.Advance(elapsed)

			outcomes := history.Concurrently(64, patience, func(int) (any, error) { return b.Allow(target), nil })

			admitted := 0
			for _, o := range outcomes {
				assert.True(t, o.Finished, "every caller must return")
				if o.Output == true {
					admitted++
				}
			}

			assert.Equal(t, admitted, 1, "exactly one caller must take the probe")
		})
	})

	t.Run("Record", func(t *testing.T) {
		t.Parallel()

		t.Run("resets the failure count for a success", func(t *testing.T) {
			t.Parallel()
			b := newBreaker(t, fake.New(originUTC))

			b.Allow(target)
			b.Record(target, true)
			b.Allow(target)
			b.Record(target, false)
			b.Allow(target)
			b.Record(target, true)

			assert.True(t, b.Allow(target), "a success between two failures must reset the count")
		})

		t.Run("reopens the circuit for the full interval after a failed probe", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			b := newBreaker(t, c)
			trip(t, b)
			c.Advance(elapsed)

			assert.True(t, b.Allow(target), "the circuit must admit the probe")
			b.Record(target, true)

			c.Advance(openFor - time.Second)
			assert.False(t, b.Allow(target), "the full interval must elapse again")
		})

		t.Run("closes the circuit after consecutive probe successes", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			b := newBreaker(t, c)
			trip(t, b)

			for range 2 {
				c.Advance(elapsed)
				assert.True(t, b.Allow(target), "each probe must be admitted")
				b.Record(target, false)
			}

			assert.Equal(t, b.State(target), resilience.Closed, "two probe successes must close the circuit")
		})

		t.Run("resets the failure count when the circuit closes", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			b := newBreaker(t, c)
			trip(t, b)
			c.Advance(elapsed)
			for range 2 {
				b.Allow(target)
				b.Record(target, false)
			}

			b.Allow(target)
			b.Record(target, true)
			assert.Equal(t, b.State(target), resilience.Closed, "one failure of a closed circuit must not open it")
		})

		t.Run("reopens the circuit for a failed second probe", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			b := newBreaker(t, c)
			trip(t, b)

			c.Advance(elapsed)
			b.Allow(target)
			b.Record(target, false)

			c.Advance(elapsed)
			b.Allow(target)
			b.Record(target, true)

			assert.False(t, b.Allow(target), "the circuit must reopen, not close")
		})

		t.Run("reopens the circuit for a failed probe below the failure threshold", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			b := newBreaker(t, c)
			trip(t, b)
			b.Record(target, false)

			c.Advance(elapsed)
			assert.True(t, b.Allow(target), "the circuit must admit the probe")
			b.Record(target, true)

			assert.Equal(t, b.State(target), resilience.Open, "a failed probe must reopen the circuit")
		})

		t.Run("keeps a half-open circuit for a late failure below the failure threshold", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			b := newBreaker(t, c)
			trip(t, b)
			b.Record(target, false)

			c.Advance(elapsed)
			b.Allow(target)
			b.Record(target, false)
			b.Record(target, true)

			assert.Equal(t, b.State(target), resilience.HalfOpen,
				"a late failure below the threshold must not reopen the circuit")
		})

		t.Run("resets the probe successes for a late failure", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			b := newBreaker(t, c)
			trip(t, b)
			b.Record(target, false)

			c.Advance(elapsed)
			b.Allow(target)
			b.Record(target, false)
			b.Record(target, true)

			assert.True(t, b.Allow(target), "the circuit must admit the next probe")
			b.Record(target, false)
			assert.Equal(t, b.State(target), resilience.HalfOpen,
				"a probe success after a late failure must not close the circuit")
		})

		t.Run("reopens a half-open circuit for a late failure at the failure threshold", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			b := newBreaker(t, c)
			trip(t, b)

			c.Advance(elapsed)
			b.Allow(target)
			b.Record(target, false)
			b.Record(target, true)

			assert.Equal(t, b.State(target), resilience.Open, "a late failure at the threshold must reopen the circuit")
		})

		t.Run("keeps a half-open circuit for a late success", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			b := newBreaker(t, c)
			trip(t, b)

			c.Advance(elapsed)
			b.Allow(target)
			b.Record(target, false)
			b.Record(target, false)

			assert.Equal(t, b.State(target), resilience.HalfOpen, "a late success must not close the circuit")
		})

		t.Run("ignores a target that Allow never saw", func(t *testing.T) {
			t.Parallel()
			b := newBreaker(t, fake.New(originUTC))
			b.Record("never-allowed", true)
			b.Record("never-allowed", true)

			assert.True(t, b.Allow("never-allowed"), "Record must not open a circuit that Allow never made")
		})

		t.Run("restarts the open interval for a late failure", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			b := newBreaker(t, c)
			for range 3 {
				assert.True(t, b.Allow(target), "a closed circuit must admit the calls")
			}
			b.Record(target, true)
			b.Record(target, true)

			c.Advance(20 * time.Second)
			b.Record(target, true)

			c.Advance(15 * time.Second)
			assert.False(t, b.Allow(target), "the late failure must restart the interval")

			c.Advance(15 * time.Second)
			assert.True(t, b.Allow(target), "the restarted interval must end with a probe")
		})

		t.Run("resets the failure count for a late success while open", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			b := newBreaker(t, c)
			for range 4 {
				assert.True(t, b.Allow(target), "a closed circuit must admit the calls")
			}
			b.Record(target, true)
			b.Record(target, true)

			c.Advance(10 * time.Second)
			b.Record(target, false)
			b.Record(target, true)

			c.Advance(25 * time.Second)
			assert.True(t, b.Allow(target), "the first interval must end with a probe")
		})

		t.Run("restarts the open interval for a second late failure after a late success", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			b := newBreaker(t, c)
			for range 5 {
				assert.True(t, b.Allow(target), "a closed circuit must admit the calls")
			}
			b.Record(target, true)
			b.Record(target, true)

			c.Advance(10 * time.Second)
			b.Record(target, false)
			b.Record(target, true)
			b.Record(target, true)

			c.Advance(25 * time.Second)
			assert.False(t, b.Allow(target), "the second late failure must restart the interval")
		})
	})

	t.Run("State", func(t *testing.T) {
		t.Parallel()

		t.Run("returns Closed for a target without a circuit", func(t *testing.T) {
			t.Parallel()
			b := newBreaker(t, fake.New(originUTC))
			assert.Equal(t, b.State("unknown"), resilience.Closed, "a target never called must be Closed")
		})

		t.Run("returns Open for an open circuit", func(t *testing.T) {
			t.Parallel()
			b := newBreaker(t, fake.New(originUTC))
			trip(t, b)

			assert.Equal(t, b.State(target), resilience.Open, "two failures must open the circuit")
		})

		t.Run("returns HalfOpen once the open interval elapses", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			b := newBreaker(t, c)
			trip(t, b)

			c.Advance(elapsed)
			assert.Equal(t, b.State(target), resilience.HalfOpen,
				"an elapsed interval must report HalfOpen before a caller claims the probe")
		})

		t.Run("returns HalfOpen while a probe is outstanding", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			b := newBreaker(t, c)
			trip(t, b)

			c.Advance(elapsed)
			b.Allow(target)
			assert.Equal(t, b.State(target), resilience.HalfOpen, "a circuit with a probe must be HalfOpen")
		})
	})

	t.Run("Call", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the value of fn", func(t *testing.T) {
			t.Parallel()
			b := newBreaker(t, fake.New(originUTC))

			got, err := resilience.Call(t.Context(), b, target, func(context.Context) (int, error) { return 42, nil })
			expect.NoError(t, err, "Call must return the success of fn")
			expect.Equal(t, got, 42, "Call must return the value of fn")
		})

		t.Run("opens the circuit for an error of a class in TripOn", func(t *testing.T) {
			t.Parallel()
			b := newBreaker(t, fake.New(originUTC))

			for range 2 {
				_, err := resilience.Call(t.Context(), b, target,
					func(context.Context) (int, error) { return 0, errTripping })
				assert.ErrorIs(t, err, errDependency, "Call must return the error of fn")
			}

			assert.Equal(t, b.State(target), resilience.Open, "two errors of a class in TripOn must open the circuit")
		})

		t.Run("keeps the circuit closed for an error of a class outside TripOn", func(t *testing.T) {
			t.Parallel()
			b := newBreaker(t, fake.New(originUTC))
			invalid := errs.WithClass(errDependency, errs.Invalid)

			for range 5 {
				_, _ = resilience.Call(t.Context(), b, target, func(context.Context) (int, error) { return 0, invalid })
			}

			assert.Equal(t, b.State(target), resilience.Closed, "a class outside TripOn must not open the circuit")
		})

		t.Run("counts a success for a TripOn that contains Unspecified", func(t *testing.T) {
			t.Parallel()
			cfg := circuits
			cfg.Clock, cfg.TripOn = fake.New(originUTC), []errs.Class{errs.Unspecified}
			b, err := resilience.NewBreaker(cfg)
			assert.NoError(t, err, "NewBreaker must accept the config")

			for range 2 {
				_, _ = resilience.Call(t.Context(), b, target, func(context.Context) (int, error) { return 42, nil })
			}

			assert.Equal(t, b.State(target), resilience.Closed, "a call that succeeds must not count as a failure")
		})

		t.Run("returns ErrOpen without a call of fn for an open circuit", func(t *testing.T) {
			t.Parallel()
			b := newBreaker(t, fake.New(originUTC))
			trip(t, b)

			called := false
			_, err := resilience.Call(t.Context(), b, target, func(context.Context) (int, error) {
				called = true

				return 0, nil
			})

			expect.ErrorIs(t, err, resilience.ErrOpen, "an open circuit must refuse the call")
			expect.Equal(t, errs.Classify(err), errs.Transient, "ErrOpen must classify as Transient")
			expect.False(t, called, "an open circuit must not run fn")
		})

		t.Run("keeps the circuit closed for calls whose context ended", func(t *testing.T) {
			t.Parallel()
			b := newBreaker(t, fake.New(originUTC))
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			for range 5 {
				_, _ = resilience.Call(ctx, b, target, func(context.Context) (int, error) { return 0, errTripping })
			}

			assert.Equal(t, b.State(target), resilience.Closed, "a cancellation must not open the circuit")
		})

		t.Run("keeps the failure count for a call whose context ended", func(t *testing.T) {
			t.Parallel()
			b := newBreaker(t, fake.New(originUTC))
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			failing := func(context.Context) (int, error) { return 0, errTripping }

			_, _ = resilience.Call(t.Context(), b, target, failing)
			_, _ = resilience.Call(ctx, b, target, failing)
			_, _ = resilience.Call(t.Context(), b, target, failing)

			assert.Equal(t, b.State(target), resilience.Open,
				"a cancelled call between two failures must not reset the failure count")
		})

		t.Run("counts a success after the context ended as a success", func(t *testing.T) {
			t.Parallel()
			b := newBreaker(t, fake.New(originUTC))
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			failing := func(context.Context) (int, error) { return 0, errTripping }

			_, _ = resilience.Call(t.Context(), b, target, failing)
			_, err := resilience.Call(ctx, b, target, func(context.Context) (int, error) { return 42, nil })
			assert.NoError(t, err, "Call must return the success of fn")
			_, _ = resilience.Call(t.Context(), b, target, failing)

			assert.Equal(t, b.State(target), resilience.Closed,
				"the success between two failures must reset the failure count")
		})

		t.Run("releases the probe of a call whose context ended", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			b := newBreaker(t, c)
			trip(t, b)
			c.Advance(elapsed)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			_, err := resilience.Call(ctx, b, target, func(context.Context) (int, error) { return 0, errTripping })
			assert.ErrorIs(t, err, errDependency, "Call must return the error of fn")
			assert.True(t, b.Allow(target), "the circuit must admit the next probe")
		})

		t.Run("counts no success for the probe of a call whose context ended", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			b := newBreaker(t, c)
			trip(t, b)
			c.Advance(elapsed)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			_, _ = resilience.Call(ctx, b, target, func(context.Context) (int, error) { return 0, errTripping })
			b.Allow(target)
			b.Record(target, false)

			assert.Equal(t, b.State(target), resilience.HalfOpen,
				"one probe success must not close a circuit that needs two")
		})

		t.Run("lets a panic of fn continue", func(t *testing.T) {
			t.Parallel()
			b := newBreaker(t, fake.New(originUTC))

			got := assert.Panics(t, func() {
				_, _ = resilience.Call(t.Context(), b, target, func(context.Context) (int, error) {
					panic("fn panicked") //nolint:forbidigo // the case tests the panic of fn
				})
			}, "Call must let the panic of fn continue")

			assert.Equal(t, got, any("fn panicked"), "the recovered value must be the panic value of fn")
		})

		t.Run("releases the probe of a fn that panics", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			b := newBreaker(t, c)
			trip(t, b)
			c.Advance(elapsed)

			assert.Panics(t, func() {
				_, _ = resilience.Call(t.Context(), b, target, func(context.Context) (int, error) {
					panic("fn panicked") //nolint:forbidigo // the case tests the panic of fn
				})
			}, "Call must let the panic of fn continue")

			assert.True(t, b.Allow(target), "the circuit must admit the next probe")
		})

		t.Run("counts nothing for a panic in a closed circuit", func(t *testing.T) {
			t.Parallel()
			b := newBreaker(t, fake.New(originUTC))
			failing := func(context.Context) (int, error) { return 0, errTripping }

			_, _ = resilience.Call(t.Context(), b, target, failing)
			assert.Panics(t, func() {
				_, _ = resilience.Call(t.Context(), b, target, func(context.Context) (int, error) {
					panic("fn panicked") //nolint:forbidigo // the case tests the panic of fn
				})
			}, "Call must let the panic of fn continue")
			_, _ = resilience.Call(t.Context(), b, target, failing)

			assert.Equal(t, b.State(target), resilience.Open,
				"a panic between two failures must not reset the failure count")
		})
	})
}

// TestBreakerAllocs checks the allocation contract of Allow, Record, State
// and Call for a target that has a circuit. MaxAllocs counts the
// allocations of the whole process, so the test does not run in parallel.
//
//nolint:paralleltest // see above
func TestBreakerAllocs(t *testing.T) {
	ctx := t.Context()
	c := fake.New(originUTC)
	closed := newBreaker(t, c)
	closed.Allow(target)

	opened := newBreaker(t, c)
	trip(t, opened)

	t.Run("Allow", func(t *testing.T) {
		t.Run("of a closed circuit", func(t *testing.T) {
			var got bool
			expect.MaxAllocs(t, func() { got = closed.Allow(target) }, 0, "Allow must not allocate")
			assert.True(t, got, "the test must measure an admitted call")
		})

		t.Run("of an open circuit", func(t *testing.T) {
			var got bool
			expect.MaxAllocs(t, func() { got = opened.Allow(target) }, 0, "Allow must not allocate")
			assert.False(t, got, "the test must measure a refused call")
		})
	})

	t.Run("Record", func(t *testing.T) {
		expect.MaxAllocs(t, func() { closed.Record(target, false) }, 0, "Record must not allocate")
		assert.Equal(t, closed.State(target), resilience.Closed, "the test must measure a closed circuit")
	})

	t.Run("State", func(t *testing.T) {
		var got resilience.State
		expect.MaxAllocs(t, func() { got = opened.State(target) }, 0, "State must not allocate")
		assert.Equal(t, got, resilience.Open, "the test must measure an open circuit")
	})

	t.Run("Call", func(t *testing.T) {
		fn := func(context.Context) (int, error) { return 42, nil }

		var (
			got int
			err error
		)
		expect.MaxAllocs(t, func() { got, err = resilience.Call(ctx, closed, target, fn) }, 0,
			"Call must not allocate")
		assert.NoError(t, err, "the test must measure a call that succeeds")
		assert.Equal(t, got, 42, "the test must measure the value of fn")
	})
}

// BenchmarkBreaker reports the cost of Allow, Record, State and Call for a
// target that has a circuit, and fails when one allocates.
func BenchmarkBreaker(b *testing.B) {
	ctx := b.Context()
	c := fake.New(originUTC)
	closed := newBreaker(b, c)
	closed.Allow(target)

	opened := newBreaker(b, c)
	trip(b, opened)

	b.Run("Allow", func(b *testing.B) {
		b.Run("of a closed circuit", func(b *testing.B) {
			var got bool

			bc := bench.Start(b).MaxAllocs(0)
			defer bc.End()

			for bc.Loop() {
				got = closed.Allow(target)
			}

			assert.True(b, got, "the benchmark must measure an admitted call")
		})

		b.Run("of an open circuit", func(b *testing.B) {
			var got bool

			bc := bench.Start(b).MaxAllocs(0)
			defer bc.End()

			for bc.Loop() {
				got = opened.Allow(target)
			}

			assert.False(b, got, "the benchmark must measure a refused call")
		})
	})

	b.Run("Record", func(b *testing.B) {
		bc := bench.Start(b).MaxAllocs(0)
		defer bc.End()

		for bc.Loop() {
			closed.Record(target, false)
		}

		assert.Equal(b, closed.State(target), resilience.Closed, "the benchmark must measure a closed circuit")
	})

	b.Run("State", func(b *testing.B) {
		var got resilience.State

		bc := bench.Start(b).MaxAllocs(0)
		defer bc.End()

		for bc.Loop() {
			got = opened.State(target)
		}

		assert.Equal(b, got, resilience.Open, "the benchmark must measure an open circuit")
	})

	b.Run("Call", func(b *testing.B) {
		fn := func(context.Context) (int, error) { return 42, nil }

		var (
			got int
			err error
		)

		bc := bench.Start(b).MaxAllocs(0)
		defer bc.End()

		for bc.Loop() {
			got, err = resilience.Call(ctx, closed, target, fn)
		}

		assert.NoError(b, err, "the benchmark must measure a call that succeeds")
		assert.Equal(b, got, 42, "the benchmark must measure the value of fn")
	})
}

// newBreaker returns a Breaker of circuits on c, and fails tb when
// NewBreaker refuses the config.
func newBreaker(tb testing.TB, c *fake.Clock) *resilience.Breaker {
	tb.Helper()

	cfg := circuits
	cfg.Clock = c
	b, err := resilience.NewBreaker(cfg)
	assert.NoError(tb, err, "NewBreaker must accept a complete config")

	return b
}

// trip opens the circuit of target of b with the two failures that
// circuits takes, and fails tb when the circuit stays closed.
func trip(tb testing.TB, b *resilience.Breaker) {
	tb.Helper()

	for range 2 {
		b.Allow(target)
		b.Record(target, true)
	}

	assert.Equal(tb, b.State(target), resilience.Open, "two failures must open the circuit")
}
