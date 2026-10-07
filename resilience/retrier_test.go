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
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/constant"
	"go.thesmos.sh/core/resilience"
)

// patience bounds the context of every blocking call that a case expects to
// return, and every wait of a case for a call on another goroutine. Every
// wait of the code under test is a fake clock or a free permit, so a call
// that returns does so in microseconds.
const patience = time.Second

// The base and the cap of the backoffs of the cases.
const (
	base = 100 * time.Millisecond
	peak = time.Second
)

// windowBuckets pins the number of buckets of a budget window, which leave
// the window one at a time.
const windowBuckets = 12

// The labels under which the machine of the budget counts a case that
// refuses a retry and a case that affords one.
const (
	refusedRetry  = "a refused retry"
	affordedRetry = "an afforded retry"
)

// The jitter sources of the cases. Each is a rand.Rand already, so a call
// of Backoff converts no value to the interface.
var (
	// noJitter returns 0 for every draw, so a retry runs without an advance
	// of the clock.
	noJitter rand.Rand = constant.FromFloat64(0)

	// halfJitter returns the midpoint for every draw, so a backoff is half
	// its ceiling.
	halfJitter rand.Rand = constant.FromFloat64(0.5)
)

// The failures of the cases: errTransient is retryable, and errPermanent
// and errUnclassified are not.
var (
	errTransient    = errs.WithClass(errors.New("resilience_test: transient"), errs.Transient)
	errPermanent    = errs.WithClass(errors.New("resilience_test: permanent"), errs.Invalid)
	errUnclassified = errors.New("resilience_test: unclassified")
)

// policy configures the Retriers of the cases without a clock and a jitter
// source: 3 attempts, a first backoff of at most 100 ms that doubles up to
// 1 s, delays of up to a minute, and a budget of two retries per call over
// a minute. A budget that large leaves the attempt count to bound the
// retries of the cases that do not test the budget.
var policy = resilience.RetryConfig{
	Attempts:      3,
	Base:          base,
	Max:           peak,
	MaxRetryAfter: time.Minute,
	Budget:        2,
	BudgetWindow:  time.Minute,
}

func TestRetrier(t *testing.T) {
	t.Parallel()

	t.Run("NewRetrier", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			edit func(*resilience.RetryConfig)
			name string
		}{
			{name: "returns ErrConfig for a nil Clock", edit: func(c *resilience.RetryConfig) { c.Clock = nil }},
			{name: "returns ErrConfig for a nil Rand", edit: func(c *resilience.RetryConfig) { c.Rand = nil }},
			{name: "returns ErrConfig for a negative Budget", edit: func(c *resilience.RetryConfig) { c.Budget = -1 }},
			{
				name: "returns ErrConfig for a Budget without a window",
				edit: func(c *resilience.RetryConfig) { c.BudgetWindow = 0 },
			},
			{
				name: "returns ErrConfig for a floor without a window",
				edit: func(c *resilience.RetryConfig) { c.Budget, c.MinRetries, c.BudgetWindow = 0, 1, 0 },
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				cfg := policy
				cfg.Clock, cfg.Rand = fake.New(originUTC), noJitter
				tt.edit(&cfg)

				_, err := resilience.NewRetrier(cfg)
				expect.ErrorIs(t, err, resilience.ErrConfig, "NewRetrier must refuse the config")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "ErrConfig must classify as Invalid")
			})
		}

		t.Run("returns ErrConfig for Attempts that are not positive", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(attempts int) error {
				cfg := policy
				cfg.Clock, cfg.Rand, cfg.Attempts = fake.New(originUTC), noJitter, attempts
				_, err := resilience.NewRetrier(cfg)

				return err
			}, resilience.ErrConfig, "NewRetrier must refuse Attempts that are not positive",
				prop.Using(prop.Integer(math.MinInt, 0)), prop.Example(0))
		})

		t.Run("returns ErrConfig for a Base that is not positive", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(b time.Duration) error {
				cfg := policy
				cfg.Clock, cfg.Rand, cfg.Base = fake.New(originUTC), noJitter, b
				_, err := resilience.NewRetrier(cfg)

				return err
			}, resilience.ErrConfig, "NewRetrier must refuse a Base that is not positive",
				prop.Using(prop.Duration(math.MinInt64, 0)), prop.Example(time.Duration(0)))
		})

		t.Run("returns ErrConfig for a Max below the Base", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(limit time.Duration) error {
				cfg := policy
				cfg.Clock, cfg.Rand, cfg.Max = fake.New(originUTC), noJitter, limit
				_, err := resilience.NewRetrier(cfg)

				return err
			}, resilience.ErrConfig, "NewRetrier must refuse a Max below the Base",
				prop.Using(prop.Duration(math.MinInt64, base-1)), prop.Example(time.Duration(0)), prop.Example(base-1))
		})

		t.Run("returns ErrConfig for a negative MaxRetryAfter", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(d time.Duration) error {
				cfg := policy
				cfg.Clock, cfg.Rand, cfg.MaxRetryAfter = fake.New(originUTC), noJitter, d
				_, err := resilience.NewRetrier(cfg)

				return err
			}, resilience.ErrConfig, "NewRetrier must refuse a negative MaxRetryAfter",
				prop.Using(prop.Duration(math.MinInt64, -1)))
		})

		t.Run("returns ErrConfig for a negative MinRetries", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(n int) error {
				cfg := policy
				cfg.Clock, cfg.Rand, cfg.MinRetries = fake.New(originUTC), noJitter, n
				_, err := resilience.NewRetrier(cfg)

				return err
			}, resilience.ErrConfig, "NewRetrier must refuse a negative MinRetries",
				prop.Using(prop.Integer(math.MinInt, -1)))
		})

		valid := []struct {
			edit func(*resilience.RetryConfig)
			name string
		}{
			{
				name: "returns a Retrier for a Max equal to the Base",
				edit: func(c *resilience.RetryConfig) { c.Max = c.Base },
			},
			{
				name: "returns a Retrier without a budget or a window",
				edit: func(c *resilience.RetryConfig) { c.Budget, c.MinRetries, c.BudgetWindow = 0, 0, 0 },
			},
			{
				name: "returns a Retrier for a MaxRetryAfter of zero",
				edit: func(c *resilience.RetryConfig) { c.MaxRetryAfter = 0 },
			},
		}
		for _, tt := range valid {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				cfg := policy
				cfg.Clock, cfg.Rand = fake.New(originUTC), noJitter
				tt.edit(&cfg)

				_, err := resilience.NewRetrier(cfg)
				assert.NoError(t, err, "NewRetrier must accept the config")
			})
		}
	})

	t.Run("Do", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a success without a retry", func(t *testing.T) {
			t.Parallel()
			r := newRetrier(t, fake.New(originUTC), noJitter)
			fn, calls := failFor(0, errTransient)

			got, err := resilience.Do(bounded(t), r, fn)
			expect.NoError(t, err, "a call that succeeds must return no error")
			expect.Equal(t, got, 1, "Do must return the value of fn")
			expect.Equal(t, *calls, 1, "a success must not be retried")
		})

		t.Run("retries a Transient failure", func(t *testing.T) {
			t.Parallel()
			r := newRetrier(t, fake.New(originUTC), noJitter)
			fn, calls := failFor(1, errTransient)

			got, err := resilience.Do(bounded(t), r, fn)
			expect.NoError(t, err, "a retried call that succeeds must return no error")
			expect.Equal(t, got, 2, "Do must return the value of the attempt that succeeded")
			expect.Equal(t, *calls, 2, "Do must retry once")
		})

		failures := []struct {
			err  error
			name string
		}{
			{name: "returns a failure that is not retryable at once", err: errPermanent},
			{name: "returns an unclassified failure at once", err: errUnclassified},
			{
				name: "returns an unclassified failure with a delay at once",
				err:  errs.WithRetryAfter(errUnclassified, time.Second),
			},
		}
		for _, tt := range failures {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				r := newRetrier(t, fake.New(originUTC), noJitter)
				fn, calls := failFor(3, tt.err)

				_, err := resilience.Do(bounded(t), r, fn)
				expect.ErrorIs(t, err, tt.err, "the failure must reach the caller unchanged")
				expect.Equal(t, *calls, 1, "Do must not retry the failure")
			})
		}

		t.Run("returns the last failure once the attempts run out", func(t *testing.T) {
			t.Parallel()
			r := newRetrier(t, fake.New(originUTC), noJitter)
			fn, calls := failFor(99, errTransient)

			_, err := resilience.Do(bounded(t), r, fn)
			expect.ErrorIs(t, err, errTransient, "the last failure must reach the caller")
			expect.Equal(t, *calls, 3, "Attempts must count the calls, not the retries")
		})

		t.Run("does not retry a call whose context ended", func(t *testing.T) {
			t.Parallel()
			r := newRetrier(t, fake.New(originUTC), noJitter)

			ctx, cancel := context.WithCancel(t.Context())
			calls := 0
			_, err := resilience.Do(ctx, r, func(context.Context) (int, error) {
				calls++
				cancel()

				return 0, errTransient
			})

			expect.ErrorIs(t, err, errTransient, "the error of the attempt must reach the caller")
			expect.Equal(t, calls, 1, "a call whose context ended must not be retried")
		})

		t.Run("returns the error of a context that ends during the backoff", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			r := newRetrier(t, c, halfJitter)
			fn, calls := failFor(99, errTransient)

			ctx, cancel := context.WithCancel(t.Context())
			got := make(chan error, 1)
			go func() {
				_, err := resilience.Do(ctx, r, fn)
				got <- err
			}()

			c.AwaitWaiters(1)
			cancel()

			expect.ErrorIs(t, await(t, got, "Do must return"), context.Canceled,
				"a wait that the context ends must report the context")
			expect.Equal(t, *calls, 1, "the next attempt must not run")
		})

		t.Run("waits the backoff before the next attempt", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			r := newRetrier(t, c, halfJitter)
			fn, calls := failFor(1, errTransient)

			got := make(chan error, 1)
			go func() {
				_, err := resilience.Do(bounded(t), r, fn)
				got <- err
			}()

			c.AwaitWaiters(1)
			assert.Equal(t, *calls, 1, "the retry must wait for its backoff")

			c.Advance(50 * time.Millisecond)
			expect.NoError(t, await(t, got, "Do must return"), "the retry must run once its backoff elapsed")
			expect.Equal(t, *calls, 2, "one retry must run")
		})

		t.Run("returns ErrBudget without the traffic to afford a retry", func(t *testing.T) {
			t.Parallel()
			r := newRetrier(t, fake.New(originUTC), noJitter, func(c *resilience.RetryConfig) { c.Budget = 0.5 })
			fn, calls := failFor(99, errTransient)

			_, err := resilience.Do(bounded(t), r, fn)
			expect.That(t, err).
				ErrorIs(resilience.ErrBudget, "the refusal must name the budget").
				ErrorIs(errTransient, "the error must wrap the failure before the refusal")
			expect.Equal(t, *calls, 1, "the retry must not run")
		})

		t.Run("affords a retry with the calls of the window", func(t *testing.T) {
			t.Parallel()
			r := newRetrier(t, fake.New(originUTC), noJitter, func(c *resilience.RetryConfig) { c.Budget = 0.5 })
			prime(t, r, 2)

			fn, calls := failFor(1, errTransient)
			_, err := resilience.Do(bounded(t), r, fn)
			expect.NoError(t, err, "three calls of the window must afford one retry at 0.5")
			expect.Equal(t, *calls, 2, "the retry must run")
		})

		forgotten := []struct {
			name    string
			advance time.Duration
			steps   int
		}{
			{name: "forgets the calls before the window", advance: 2 * time.Minute, steps: 1},
			{name: "forgets old calls one bucket at a time", advance: 5 * time.Second, steps: 12},
		}
		for _, tt := range forgotten {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				c := fake.New(originUTC)
				r := newRetrier(t, c, noJitter, func(c *resilience.RetryConfig) { c.Budget = 0.5 })
				prime(t, r, 2)

				for range tt.steps {
					c.Advance(tt.advance)
				}

				fn, calls := failFor(1, errTransient)
				_, err := resilience.Do(bounded(t), r, fn)
				expect.ErrorIs(t, err, resilience.ErrBudget, "the calls before the window must not afford the retry")
				expect.Equal(t, *calls, 1, "the retry must not run")
			})
		}

		kept := []struct {
			name    string
			advance time.Duration
			steps   int
		}{
			{name: "keeps the calls of the window after a partial roll", advance: 10 * time.Second, steps: 1},
			{name: "keeps the calls of the window across repeated rolls", advance: 5 * time.Second, steps: 6},
		}
		for _, tt := range kept {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				c := fake.New(originUTC)
				r := newRetrier(t, c, noJitter, func(c *resilience.RetryConfig) { c.Budget = 0.5 })
				prime(t, r, 2)

				for range tt.steps {
					c.Advance(tt.advance)
				}

				fn, calls := failFor(1, errTransient)
				_, err := resilience.Do(bounded(t), r, fn)
				expect.NoError(t, err, "the calls of the window must afford the retry")
				expect.Equal(t, *calls, 2, "the retry must run")
			})
		}

		t.Run("counts a call after the window in the next window", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			r := newRetrier(t, c, noJitter)
			prime(t, r, 1)

			c.Advance(2 * time.Minute)
			fn, calls := failFor(1, errTransient)
			_, err := resilience.Do(bounded(t), r, fn)
			expect.NoError(t, err, "the call of the next window must afford its retry")
			expect.Equal(t, *calls, 2, "the retry must run")
		})

		t.Run("returns ErrBudget for a retry after its call left the window", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			r := newRetrier(t, c, noJitter, func(c *resilience.RetryConfig) { c.MaxRetryAfter = time.Hour })
			fn, calls := failFor(99, errs.WithRetryAfter(errTransient, 61*time.Second))

			got := make(chan error, 1)
			go func() {
				_, err := resilience.Do(bounded(t), r, fn)
				got <- err
			}()

			c.AwaitWaiters(1)
			c.Advance(61 * time.Second)

			expect.ErrorIs(t, await(t, got, "Do must return"), resilience.ErrBudget,
				"a window without the call must not afford the second retry")
			expect.Equal(t, *calls, 2, "the second retry must not run")
		})

		t.Run("keeps the calls of the window while the clock moves back", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			r := newRetrier(t, c, noJitter, func(c *resilience.RetryConfig) { c.Budget = 0.5 })
			prime(t, r, 2)

			c.Set(originUTC.Add(-time.Minute))
			prime(t, r, 1)

			c.Set(originUTC)
			fn, calls := failFor(1, errTransient)
			_, err := resilience.Do(bounded(t), r, fn)
			expect.NoError(t, err, "the four calls of the window must afford the retry")
			expect.Equal(t, *calls, 2, "the retry must run")
		})

		t.Run("forgets the calls of a bucket one bucket after the window passed it", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			r := newRetrier(t, c, noJitter, func(c *resilience.RetryConfig) { c.Budget = 0.4 })
			prime(t, r, 2)

			c.Advance(55 * time.Second)
			prime(t, r, 1)

			c.Advance(5 * time.Second)
			fn, calls := failFor(1, errTransient)
			_, err := resilience.Do(bounded(t), r, fn)
			expect.ErrorIs(t, err, resilience.ErrBudget, "the two calls of the window must not afford a retry at 0.4")
			expect.Equal(t, *calls, 1, "the retry must not run")
		})

		t.Run("returns ErrBudget for an allowance of zero", func(t *testing.T) {
			t.Parallel()
			r := newRetrier(t, fake.New(originUTC), noJitter, func(c *resilience.RetryConfig) {
				c.Budget, c.MinRetries, c.BudgetWindow = 0, 0, 0
			})
			fn, calls := failFor(99, errTransient)

			_, err := resilience.Do(bounded(t), r, fn)
			expect.ErrorIs(t, err, resilience.ErrBudget, "no allowance must mean no retry")
			expect.Equal(t, *calls, 1, "the retry must not run")
		})

		// floored sets a floor of one retry beneath a fraction that cannot
		// afford one, and four attempts.
		floored := func(c *resilience.RetryConfig) { c.Budget, c.MinRetries, c.Attempts = 0.5, 1, 4 }

		t.Run("affords a retry from the floor that the fraction cannot afford", func(t *testing.T) {
			t.Parallel()
			r := newRetrier(t, fake.New(originUTC), noJitter, floored)
			fn, calls := failFor(1, errTransient)

			_, err := resilience.Do(bounded(t), r, fn)
			expect.NoError(t, err, "the floor must afford the first retry")
			expect.Equal(t, *calls, 2, "the retry must run")
		})

		t.Run("returns ErrBudget for a retry beyond the floor", func(t *testing.T) {
			t.Parallel()
			r := newRetrier(t, fake.New(originUTC), noJitter, floored)
			fn, calls := failFor(99, errTransient)

			_, err := resilience.Do(bounded(t), r, fn)
			expect.ErrorIs(t, err, resilience.ErrBudget, "the second retry must exceed the floor")
			expect.Equal(t, *calls, 2, "one retry must run")
		})

		t.Run("affords retries from the fraction above the floor", func(t *testing.T) {
			t.Parallel()
			r := newRetrier(t, fake.New(originUTC), noJitter, floored)
			prime(t, r, 10)

			fn, calls := failFor(2, errTransient)
			_, err := resilience.Do(bounded(t), r, fn)
			expect.NoError(t, err, "eleven calls at 0.5 must afford two retries")
			expect.Equal(t, *calls, 3, "both retries must run")
		})

		t.Run("restores the floor in a new window", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			r := newRetrier(t, c, noJitter, floored)

			spend, _ := failFor(1, errTransient)
			_, err := resilience.Do(bounded(t), r, spend)
			assert.NoError(t, err, "the floor must afford the first retry")

			c.Advance(2 * time.Minute)
			fn, calls := failFor(1, errTransient)
			_, err = resilience.Do(bounded(t), r, fn)
			expect.NoError(t, err, "a new window must restore the floor")
			expect.Equal(t, *calls, 2, "the retry must run")
		})

		// The machine makes calls that fail up to twice, and moves the clock
		// forward and back. Its model counts the calls and the retries of
		// each bucket since the origin, up to the latest bucket that a call
		// reached, which a clock that moves back leaves in place.
		t.Run("retries a failure exactly when the window affords it", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Do must retry a failure exactly when the calls of the window afford it",
				func(c *prop.Case) {
					clk := fake.New(originUTC)
					cfg := policy
					cfg.Clock, cfg.Rand = clk, noJitter
					cfg.Budget = c.Draw(prop.SampledFrom(0, 0.4, 0.5, 2), "budget")
					cfg.MinRetries = c.Draw(prop.Integer(0, 2), "floor")
					r, err := resilience.NewRetrier(cfg)
					assert.NoError(c, err, "NewRetrier must accept the config")

					bucket := cfg.BudgetWindow / windowBuckets
					calls, retries := map[int]int{}, map[int]int{}
					cur := 0
					inWindow := func(counts map[int]int) int {
						n := 0
						for b := cur - windowBuckets + 1; b <= cur; b++ {
							n += counts[b]
						}

						return n
					}
					shift := func(c *prop.Case, _ struct{}) any {
						return c.Draw(prop.Duration(0, 2*cfg.BudgetWindow), "shift")
					}

					stateful.Steps(c, stateful.Machine[struct{}]{
						Actions: []stateful.Action[struct{}]{
							{
								Name: "call",
								Input: func(c *prop.Case, _ struct{}) any {
									return c.Draw(prop.Integer(0, cfg.Attempts-1), "failures")
								},
								Run: func(c *prop.Case, _ int, in any) {
									failures := in.(int)
									cur = max(cur, int(clk.Time().Sub(originUTC)/bucket))
									calls[cur]++
									want := 1
									for range failures {
										allowance := max(float64(cfg.MinRetries), cfg.Budget*float64(inWindow(calls)))
										if float64(inWindow(retries)+1) > allowance {
											break
										}
										retries[cur]++
										want++
									}

									fn, ran := failFor(failures, errTransient)
									_, doErr := resilience.Do(c.Context(), r, fn)
									assert.Equal(c, *ran, want, "Do must run the attempts that the window affords")
									if want <= failures {
										c.Classify(refusedRetry)
										assert.ErrorIs(c, doErr, resilience.ErrBudget,
											"a refused retry must return ErrBudget")

										return
									}
									if failures > 0 {
										c.Classify(affordedRetry)
									}
									assert.NoError(c, doErr, "a call whose retries the window affords must succeed")
								},
							},
							{
								Name:  "advance",
								Input: shift,
								Run:   func(_ *prop.Case, _ int, in any) { clk.Advance(in.(time.Duration)) },
							},
							{
								Name:  "rewind",
								Input: shift,
								Run:   func(_ *prop.Case, _ int, in any) { clk.Set(clk.Time().Add(-in.(time.Duration))) },
							},
						},
					})
				}, prop.Require(refusedRetry, 0.1), prop.Require(affordedRetry, 0.1))
		})

		t.Run("waits the delay of a failure that exceeds the backoff", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			r := newRetrier(t, c, noJitter)
			fn, calls := failFor(1, errs.WithRetryAfter(errTransient, 30*time.Second))

			got := make(chan error, 1)
			go func() {
				_, err := resilience.Do(bounded(t), r, fn)
				got <- err
			}()

			c.AwaitWaiters(1)
			c.Advance(30*time.Second - time.Nanosecond)
			c.AwaitWaiters(1)
			assert.Equal(t, *calls, 1, "the retry must wait for the delay")

			c.Advance(time.Nanosecond)
			expect.NoError(t, await(t, got, "Do must return"), "the retry must run once the delay elapsed")
			expect.Equal(t, *calls, 2, "one retry must run")
		})

		t.Run("waits its backoff when the backoff exceeds the delay", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			r := newRetrier(t, c, halfJitter)
			fn, calls := failFor(1, errs.WithRetryAfter(errTransient, 10*time.Millisecond))

			got := make(chan error, 1)
			go func() {
				_, err := resilience.Do(bounded(t), r, fn)
				got <- err
			}()

			c.AwaitWaiters(1)
			c.Advance(10 * time.Millisecond)
			c.AwaitWaiters(1)
			assert.Equal(t, *calls, 1, "the retry must wait for the backoff of 50 ms")

			c.Advance(40 * time.Millisecond)
			expect.NoError(t, await(t, got, "Do must return"), "the retry must run once the backoff elapsed")
			expect.Equal(t, *calls, 2, "one retry must run")
		})

		t.Run("applies a delay to the attempt after its failure alone", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			r := newRetrier(t, c, halfJitter)
			fn, calls := failWith(errs.WithRetryAfter(errTransient, 30*time.Second), errTransient)

			got := make(chan error, 1)
			go func() {
				_, err := resilience.Do(bounded(t), r, fn)
				got <- err
			}()

			c.AwaitWaiters(1)
			c.Advance(30 * time.Second)
			c.AwaitWaiters(1)
			c.Advance(100 * time.Millisecond)
			expect.NoError(t, await(t, got, "Do must return"), "the second retry must wait for its backoff alone")
			expect.Equal(t, *calls, 3, "both retries must run")
		})

		t.Run("returns a failure whose delay exceeds MaxRetryAfter without a wait", func(t *testing.T) {
			t.Parallel()
			r := newRetrier(t, fake.New(originUTC), noJitter, func(c *resilience.RetryConfig) {
				c.MaxRetryAfter = 29 * time.Second
			})
			fn, calls := failFor(1, errs.WithRetryAfter(errTransient, 30*time.Second))

			_, err := resilience.Do(bounded(t), r, fn)
			assert.ErrorIs(t, err, errTransient, "Do must return the failure")

			delay, _ := errs.RetryAfter(err)
			expect.Equal(t, delay, 30*time.Second, "the caller must read the delay of the failure")
			expect.Equal(t, *calls, 1, "the retry must not run")
		})

		t.Run("waits a delay equal to MaxRetryAfter", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			r := newRetrier(t, c, noJitter, func(c *resilience.RetryConfig) { c.MaxRetryAfter = 30 * time.Second })
			fn, calls := failFor(1, errs.WithRetryAfter(errTransient, 30*time.Second))

			got := make(chan error, 1)
			go func() {
				_, err := resilience.Do(bounded(t), r, fn)
				got <- err
			}()

			c.AwaitWaiters(1)
			c.Advance(30 * time.Second)
			expect.NoError(t, await(t, got, "Do must return"), "Do must wait for a delay at the ceiling")
			expect.Equal(t, *calls, 2, "the retry must run")
		})

		t.Run("returns a failure with a delay when MaxRetryAfter is zero", func(t *testing.T) {
			t.Parallel()
			r := newRetrier(t, fake.New(originUTC), noJitter, func(c *resilience.RetryConfig) { c.MaxRetryAfter = 0 })
			fn, calls := failFor(1, errs.WithRetryAfter(errTransient, time.Nanosecond))

			_, err := resilience.Do(bounded(t), r, fn)
			expect.ErrorIs(t, err, errTransient, "Do must return the failure")
			expect.Equal(t, *calls, 1, "the retry must not run")
		})

		t.Run("retries a failure without a delay when MaxRetryAfter is zero", func(t *testing.T) {
			t.Parallel()
			r := newRetrier(t, fake.New(originUTC), noJitter, func(c *resilience.RetryConfig) { c.MaxRetryAfter = 0 })
			fn, calls := failFor(1, errTransient)

			_, err := resilience.Do(bounded(t), r, fn)
			expect.NoError(t, err, "Do must retry a failure without a delay")
			expect.Equal(t, *calls, 2, "the retry must run")
		})
	})

	t.Run("Backoff", func(t *testing.T) {
		t.Parallel()

		t.Run("returns 0 before the first retry", func(t *testing.T) {
			t.Parallel()
			backoff := func(attempt int) time.Duration { return resilience.Backoff(halfJitter, attempt, base, peak) }
			prop.Equal(t, backoff, func(int) time.Duration { return 0 }, "Backoff must return 0 before anything failed",
				prop.Using(prop.Integer(math.MinInt, 0)), prop.Example(0), prop.Example(-1))
		})

		tests := []struct {
			jitter      rand.Rand
			name        string
			attempt     int
			first, last time.Duration
			want        time.Duration
		}{
			{
				name:   "returns half the base for the first retry",
				jitter: halfJitter, attempt: 1, first: base, last: peak,
				want: 50 * time.Millisecond,
			},
			{
				name:   "doubles the ceiling for the second retry",
				jitter: halfJitter, attempt: 2, first: base, last: peak,
				want: 100 * time.Millisecond,
			},
			{
				name:   "doubles the ceiling for the third retry",
				jitter: halfJitter, attempt: 3, first: base, last: peak,
				want: 200 * time.Millisecond,
			},
			{
				name:   "doubles the ceiling for the fourth retry",
				jitter: halfJitter, attempt: 4, first: base, last: peak,
				want: 400 * time.Millisecond,
			},
			{
				name:   "caps a base above the maximum",
				jitter: halfJitter, attempt: 1, first: 10 * peak, last: peak,
				want: peak / 2,
			},
			{
				name:   "doubles a ceiling up to an odd maximum",
				jitter: constant.FromFloat64(0.99), attempt: 2, first: 1, last: 3,
				want: 1,
			},
			{
				name:   "returns 0 for a draw of 0",
				jitter: noJitter, attempt: 1, first: base, last: peak,
			},
			{
				name:   "returns nearly the ceiling for a draw near 1",
				jitter: constant.FromFloat64(0.99), attempt: 1, first: base, last: peak,
				want: 99 * time.Millisecond,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, resilience.Backoff(tt.jitter, tt.attempt, tt.first, tt.last), tt.want,
					"Backoff must scale the ceiling of the retry by the draw")
			})
		}

		t.Run("caps the ceiling at the maximum", func(t *testing.T) {
			t.Parallel()
			backoff := func(attempt int) time.Duration { return resilience.Backoff(halfJitter, attempt, base, peak) }
			prop.Equal(t, backoff, func(int) time.Duration { return peak / 2 },
				"Backoff must cap the ceiling, which would overflow without a cap",
				prop.Using(prop.Integer(5, math.MaxInt)), prop.Example(5), prop.Example(40), prop.Example(1<<20))
		})

		t.Run("returns a delay within the ceiling of the retry", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Backoff must return a delay from 0 to the ceiling of the retry", func(c *prop.Case) {
				fraction := c.Draw(prop.Float(0.0, 0.999), "draw")
				attempt := c.Draw(prop.Integer(1, 100), "attempt")
				first := c.Draw(prop.Duration(time.Nanosecond, time.Hour), "base")
				limit := c.Draw(prop.Duration(first, 2*time.Hour), "limit")

				ceiling := math.Min(float64(first)*math.Pow(2, float64(attempt-1)), float64(limit))
				got := resilience.Backoff(constant.FromFloat64(fraction), attempt, first, limit)
				assert.InRange(c, got, 0, ceiling, "Backoff must not exceed the ceiling of the retry")
			})
		})
	})
}

// TestRetrierAllocs checks the allocation contract of Backoff and of a Do
// whose first attempt succeeds. MaxAllocs counts the allocations of the
// whole process, so the test does not run in parallel.
//
//nolint:paralleltest // see above
func TestRetrierAllocs(t *testing.T) {
	t.Run("Do", func(t *testing.T) {
		t.Run("of a call that succeeds", func(t *testing.T) {
			ctx := t.Context()
			r := newRetrier(t, fake.New(originUTC), noJitter)
			fn := func(context.Context) (int, error) { return 1, nil }

			var (
				got int
				err error
			)
			expect.MaxAllocs(t, func() { got, err = resilience.Do(ctx, r, fn) }, 0, "Do must not allocate")
			assert.NoError(t, err, "the test must measure a call that succeeds")
			assert.Equal(t, got, 1, "the test must measure the value of fn")
		})
	})

	t.Run("Backoff", func(t *testing.T) {
		var got time.Duration
		expect.MaxAllocs(t, func() { got = resilience.Backoff(halfJitter, 4, base, peak) }, 0,
			"Backoff must not allocate")
		assert.Equal(t, got, 400*time.Millisecond, "the test must measure the fourth retry")
	})
}

// BenchmarkRetrier reports the cost of Backoff and of a Do whose first
// attempt succeeds, and fails when one allocates.
func BenchmarkRetrier(b *testing.B) {
	b.Run("Do", func(b *testing.B) {
		b.Run("of a call that succeeds", func(b *testing.B) {
			ctx := b.Context()
			r := newRetrier(b, fake.New(originUTC), noJitter)
			fn := func(context.Context) (int, error) { return 1, nil }

			var (
				got int
				err error
			)

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got, err = resilience.Do(ctx, r, fn)
			}

			assert.NoError(b, err, "the benchmark must measure a call that succeeds")
			assert.Equal(b, got, 1, "the benchmark must measure the value of fn")
		})
	})

	b.Run("Backoff", func(b *testing.B) {
		var got time.Duration

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = resilience.Backoff(halfJitter, 4, base, peak)
		}

		assert.Equal(b, got, 400*time.Millisecond, "the benchmark must measure the fourth retry")
	})
}

// bounded returns a context that ends patience after the call, for every
// blocking call of a case that the case expects to return. t.Context ends
// only once the test completes, so a call that never returns would keep the
// test from the cancellation that releases it. The bound is real time: it
// measures the patience of the process with a call that never returns.
func bounded(tb testing.TB) context.Context {
	tb.Helper()

	ctx, cancel := context.WithTimeout(tb.Context(), patience)
	tb.Cleanup(cancel)

	return ctx
}

// await returns the next value of ch, and fails tb when no value arrives
// within patience of real time, so a call that never returns fails its
// case and does not hang the test binary.
func await[T any](tb testing.TB, ch <-chan T, what string) T {
	tb.Helper()

	timer := time.NewTimer(patience)
	defer timer.Stop()

	select {
	case v := <-ch:
		return v
	case <-timer.C:
		tb.Fatalf("%s: nothing arrived within %s", what, patience)

		var zero T

		return zero
	}
}

// newRetrier returns a Retrier of policy on c with r, after edits, and
// fails tb when NewRetrier refuses the config.
func newRetrier(tb testing.TB, c clock.Clock, r rand.Rand, edits ...func(*resilience.RetryConfig)) *resilience.Retrier {
	tb.Helper()

	cfg := policy
	cfg.Clock, cfg.Rand = c, r
	for _, edit := range edits {
		edit(&cfg)
	}

	retrier, err := resilience.NewRetrier(cfg)
	assert.NoError(tb, err, "NewRetrier must accept the config")

	return retrier
}

// prime makes n calls through r that succeed, which count in its budget
// window, and fails tb when one fails.
func prime(tb testing.TB, r *resilience.Retrier, n int) {
	tb.Helper()

	succeed, _ := failFor(0, errTransient)
	for range n {
		_, err := resilience.Do(bounded(tb), r, succeed)
		assert.NoError(tb, err, "a call that succeeds must return no error")
	}
}

// failFor returns a function that fails with err n times and then
// succeeds, and the count of its calls.
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

// failWith returns a function that fails with each of failures in turn and
// then succeeds, and the count of its calls.
func failWith(failures ...error) (fn func(context.Context) (int, error), calls *int) {
	calls = new(int)

	return func(context.Context) (int, error) {
		*calls++
		if *calls <= len(failures) {
			return 0, failures[*calls-1]
		}

		return *calls, nil
	}, calls
}
