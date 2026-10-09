// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package resilience_test

import (
	"context"
	"errors"
	"math"
	"strconv"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/resilience"
)

// authorities are the targets of the failovers of the cases.
var authorities = []string{"tsa-a", "tsa-b", "tsa-c"}

// codeError is an error with a code, which errors.As finds in the error of
// a failover.
type codeError struct{ code int }

// Error returns the code in the text of the error.
func (e *codeError) Error() string { return "resilience_test: code " + strconv.Itoa(e.code) }

// failover is a case of the allocation ceilings of Failover: the calls of
// its targets, the allocations that the contract states, and whether the
// failover fails.
type failover struct {
	fn    func(context.Context, int) (int, error)
	name  string
	want  uint64
	fails bool
}

func TestFailover(t *testing.T) {
	t.Parallel()

	t.Run("Failover", func(t *testing.T) {
		t.Parallel()

		refused := []struct {
			name    string
			targets []string
			start   int
		}{
			{name: "returns ErrConfig for no targets"},
			{name: "returns ErrConfig for a negative start", targets: authorities, start: -1},
			{name: "returns ErrConfig for a target named twice", targets: []string{"tsa-a", "tsa-b", "tsa-a"}},
		}
		for _, tt := range refused {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				b := newBreaker(t, fake.New(originUTC))
				var calls []int

				_, err := resilience.Failover(t.Context(), b, tt.targets, tt.start, targets(nil, &calls))
				expect.ErrorIs(t, err, resilience.ErrConfig, "Failover must refuse the configuration")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "ErrConfig must classify as Invalid")
				expect.Empty(t, calls, "a refused configuration must call no target")
			})
		}

		t.Run("returns the value of the first target that succeeds", func(t *testing.T) {
			t.Parallel()
			b := newBreaker(t, fake.New(originUTC))
			var calls []int

			got, err := resilience.Failover(t.Context(), b, authorities, 0,
				targets([]error{errTripping, nil, nil}, &calls))
			expect.NoError(t, err, "a failover with a target that succeeds must succeed")
			expect.Equal(t, got, 101, "Failover must return the value of the second target")
			expect.Equal(t, calls, []int{0, 1}, "Failover must call no target after a success")
		})

		t.Run("starts at start modulo the number of targets", func(t *testing.T) {
			t.Parallel()
			b := newBreaker(t, fake.New(originUTC))
			var calls []int

			_, err := resilience.Failover(t.Context(), b, authorities, 4,
				targets([]error{errTripping, errTripping, errTripping}, &calls))
			expect.ErrorIs(t, err, errDependency, "a failover whose targets fail must fail")
			expect.Equal(t, calls, []int{1, 2, 0}, "Failover must start at 4 mod 3 and wrap around")
		})

		t.Run("skips a target whose circuit is open", func(t *testing.T) {
			t.Parallel()
			b := newBreaker(t, fake.New(originUTC))
			for range 2 {
				b.Allow("tsa-a")
				b.Record("tsa-a", true)
			}
			var calls []int

			got, err := resilience.Failover(t.Context(), b, authorities, 0, targets([]error{nil, nil, nil}, &calls))
			expect.NoError(t, err, "a failover with a closed circuit must succeed")
			expect.Equal(t, got, 101, "Failover must return the value of the second target")
			expect.Equal(t, calls, []int{1}, "Failover must not call a target whose circuit is open")
		})

		t.Run("records each outcome in the circuit of its target", func(t *testing.T) {
			t.Parallel()
			b := newBreaker(t, fake.New(originUTC))
			var calls []int

			for range 2 {
				_, err := resilience.Failover(t.Context(), b, authorities, 0,
					targets([]error{errTripping, nil, nil}, &calls))
				assert.NoError(t, err, "each failover must succeed through the second target")
			}

			expect.Equal(t, b.State("tsa-a"), resilience.Open, "two failures must open the circuit of the first target")
			expect.Equal(t, b.State("tsa-b"), resilience.Closed,
				"successes must keep the circuit of the second target closed")
		})

		t.Run("returns the cause of a context that ended before a call", func(t *testing.T) {
			t.Parallel()
			b := newBreaker(t, fake.New(originUTC))
			cause := errors.New("resilience_test: shutdown")
			ctx, cancel := context.WithCancelCause(t.Context())
			cancel(cause)
			var calls []int

			_, err := resilience.Failover(ctx, b, authorities, 0, targets([]error{nil, nil, nil}, &calls))
			expect.ErrorIs(t, err, cause, "Failover must return the cause of the context")
			expect.Empty(t, calls, "Failover must call no target after the context ended")
		})

		t.Run("returns the error of a call whose context ended during it", func(t *testing.T) {
			t.Parallel()
			b := newBreaker(t, fake.New(originUTC))
			ctx, cancel := context.WithCancel(t.Context())
			var calls []int

			_, err := resilience.Failover(ctx, b, authorities, 0, func(_ context.Context, i int) (int, error) {
				calls = append(calls, i)
				cancel()

				return 0, errTripping
			})
			expect.ErrorIs(t, err, errTripping, "Failover must return the error of the call")
			expect.Equal(t, calls, []int{0}, "Failover must call no target after the context ended")
		})

		t.Run("records no failure for a call whose context ended during it", func(t *testing.T) {
			t.Parallel()
			b := newBreaker(t, fake.New(originUTC))

			for range 2 {
				ctx, cancel := context.WithCancel(t.Context())
				_, _ = resilience.Failover(ctx, b, authorities, 0, func(context.Context, int) (int, error) {
					cancel()

					return 0, errTripping
				})
			}

			assert.Equal(t, b.State("tsa-a"), resilience.Closed, "a call whose context ended must record no failure")
		})

		// everyTargetFailed returns the error of a failover over authorities
		// in which the first target fails, the circuit of the second refuses,
		// and the third returns coded.
		everyTargetFailed := func(t *testing.T, coded error) error {
			t.Helper()

			b := newBreaker(t, fake.New(originUTC))
			for range 2 {
				b.Allow("tsa-b")
				b.Record("tsa-b", true)
			}
			var calls []int

			_, err := resilience.Failover(t.Context(), b, authorities, 0,
				targets([]error{errTripping, nil, coded}, &calls))
			assert.HasError(t, err, "a failover whose every target fails must fail")

			return err
		}

		t.Run("lists the error of each target in the order of the failover", func(t *testing.T) {
			t.Parallel()
			err := everyTargetFailed(t, &codeError{code: 7})
			assert.Equal(t, err.Error(), "resilience: every target failed: "+
				"tsa-a: resilience_test: dependency failed; "+
				"tsa-b: resilience: circuit open; "+
				"tsa-c: resilience_test: code 7",
				"the message must list each target and its error in order")
		})

		t.Run("lets errors.Is find the error of each target", func(t *testing.T) {
			t.Parallel()
			err := everyTargetFailed(t, &codeError{code: 7})
			expect.That(t, err).
				ErrorIs(errDependency, "errors.Is must find the error of the first target").
				ErrorIs(resilience.ErrOpen, "errors.Is must find the refusal of the open circuit").
				ErrorIsNot(context.Canceled, "errors.Is must not find an error that no target returned")
		})

		t.Run("lets errors.As find the error of a target", func(t *testing.T) {
			t.Parallel()
			coded := &codeError{code: 7}
			found := assert.ErrorAs[*codeError](t, everyTargetFailed(t, coded), "errors.As must find the code")
			assert.Equal(t, found, coded, "errors.As must return the error of the third target", assert.ByIdentity())
		})

		t.Run("lets errors.As find no type that no target returned", func(t *testing.T) {
			t.Parallel()
			var timeout interface{ Timeout() bool }
			assert.False(t, errors.As(everyTargetFailed(t, &codeError{code: 7}), &timeout),
				"errors.As must not find a type that no error of a target has")
		})

		classes := []struct {
			name      string
			results   []error
			wantClass errs.Class
			wantDelay time.Duration
		}{
			{
				name: "classifies as Transient when the error of one target is Transient",
				results: []error{
					errs.WithClass(errDependency, errs.Denied),
					errs.WithClass(errDependency, errs.Transient),
					errs.WithClass(errDependency, errs.Invalid),
				},
				wantClass: errs.Transient,
			},
			{
				name: "classifies as the join when no error is Transient",
				results: []error{
					errs.WithClass(errDependency, errs.Invalid),
					errs.WithClass(errDependency, errs.Denied),
					errs.WithClass(errDependency, errs.NotFound),
				},
				wantClass: errs.Denied,
			},
			{
				name: "reports the shortest delay among the Transient errors",
				results: []error{
					errs.WithRetryAfter(errs.WithClass(errDependency, errs.Transient), 5*time.Second),
					errs.WithRetryAfter(errs.WithClass(errDependency, errs.Denied), time.Hour),
					errs.WithRetryAfter(errs.WithClass(errDependency, errs.Transient), 2*time.Second),
				},
				wantClass: errs.Transient,
				wantDelay: 2 * time.Second,
			},
			{
				name: "reports no delay when a Transient error has none",
				results: []error{
					errs.WithRetryAfter(errs.WithClass(errDependency, errs.Transient), 5*time.Second),
					errs.WithClass(errDependency, errs.Transient),
					errs.WithRetryAfter(errs.WithClass(errDependency, errs.Transient), 2*time.Second),
				},
				wantClass: errs.Transient,
			},
			{
				name: "reports the longest delay when no error is Transient",
				results: []error{
					errs.WithRetryAfter(errs.WithClass(errDependency, errs.Denied), time.Minute),
					errs.WithRetryAfter(errs.WithClass(errDependency, errs.Invalid), time.Hour),
					errs.WithRetryAfter(errs.WithClass(errDependency, errs.Denied), time.Second),
				},
				wantClass: errs.Denied,
				wantDelay: time.Hour,
			},
		}
		for _, tt := range classes {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				b := newBreaker(t, fake.New(originUTC))
				var calls []int

				_, err := resilience.Failover(t.Context(), b, authorities, 0, targets(tt.results, &calls))
				expect.Equal(t, errs.Classify(err), tt.wantClass, "the error must classify by the nearest remedy")
				expect.Equal(t, errs.Retryable(err), tt.wantClass == errs.Transient,
					"the error must be retryable when it is Transient")

				delay, ok := errs.RetryAfter(err)
				expect.Equal(t, delay, tt.wantDelay, "RetryAfter must report the delay of the nearest remedy")
				expect.Equal(t, ok, tt.wantDelay > 0, "RetryAfter must report whether there is a delay")
			})
		}

		t.Run("lets a panic of fn continue", func(t *testing.T) {
			t.Parallel()
			b := newBreaker(t, fake.New(originUTC))

			got := assert.Panics(t, func() {
				_, _ = resilience.Failover(t.Context(), b, authorities[:1], 0, func(context.Context, int) (int, error) {
					panic("fn panicked") //nolint:forbidigo // the case tests the panic of fn
				})
			}, "Failover must let the panic of fn continue")

			assert.Equal(t, got, any("fn panicked"), "the recovered value must be the panic value of fn")
		})

		t.Run("releases the probe of a fn that panics", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			b := newBreaker(t, c)
			for range 2 {
				b.Allow("tsa-a")
				b.Record("tsa-a", true)
			}
			c.Advance(elapsed)

			assert.Panics(t, func() {
				_, _ = resilience.Failover(t.Context(), b, authorities[:1], 0, func(context.Context, int) (int, error) {
					panic("fn panicked") //nolint:forbidigo // the case tests the panic of fn
				})
			}, "Failover must let the panic of fn continue")

			assert.True(t, b.Allow("tsa-a"), "the circuit must admit the next probe")
		})

		t.Run("tries every target on each attempt of a retry around it", func(t *testing.T) {
			t.Parallel()
			b := newBreaker(t, fake.New(originUTC))
			r := newRetrier(t, fake.New(originUTC), noJitter)
			var calls []int
			denied := errs.WithClass(errDependency, errs.Denied)

			got, err := resilience.Do(bounded(t), r, func(ctx context.Context) (int, error) {
				return resilience.Failover(ctx, b, authorities[:2], 0, func(_ context.Context, i int) (int, error) {
					calls = append(calls, i)
					if i == 0 || len(calls) < 4 {
						return 0, []error{denied, errTripping}[i]
					}

					return 7, nil
				})
			})

			expect.NoError(t, err, "the second attempt must succeed through the second target")
			expect.Equal(t, got, 7, "Do must return the value of the second target")
			expect.Equal(t, calls, []int{0, 1, 0, 1}, "each attempt must try every target in order")
		})

		t.Run("stops a retry around it when no error is Transient", func(t *testing.T) {
			t.Parallel()
			b := newBreaker(t, fake.New(originUTC))
			r := newRetrier(t, fake.New(originUTC), noJitter)
			var calls []int
			results := []error{errs.WithClass(errDependency, errs.Denied), errs.WithClass(errDependency, errs.Invalid)}

			_, err := resilience.Do(bounded(t), r, func(ctx context.Context) (int, error) {
				return resilience.Failover(ctx, b, authorities[:2], 0, targets(results, &calls))
			})

			expect.Equal(t, errs.Classify(err), errs.Denied, "Do must return the error of the failover")
			expect.Equal(t, calls, []int{0, 1}, "Do must not retry a failover that no retry can fix")
		})
	})
}

// TestFailoverAllocs checks the allocation contract of Failover. MaxAllocs
// counts the allocations of the whole process, so the test does not run in
// parallel.
func TestFailoverAllocs(t *testing.T) {
	ctx := t.Context()

	t.Run("Failover", func(t *testing.T) {
		for _, tt := range failovers() {
			t.Run(tt.name, func(t *testing.T) {
				b := newFailoverBreaker(t)

				var err error
				expect.MaxAllocs(t, func() { _, err = resilience.Failover(ctx, b, authorities, 0, tt.fn) }, tt.want,
					"Failover must allocate as its contract states")
				assert.Equal(t, err != nil, tt.fails, "the test must measure the outcome of the case")
			})
		}
	})
}

// BenchmarkFailover reports the cost of Failover, and fails above the
// allocations that its contract states.
func BenchmarkFailover(b *testing.B) {
	ctx := b.Context()

	b.Run("Failover", func(b *testing.B) {
		for _, tt := range failovers() {
			b.Run(tt.name, func(b *testing.B) {
				br := newFailoverBreaker(b)

				var err error

				c := bench.Start(b).MaxAllocs(tt.want)
				defer c.End()

				for c.Loop() {
					_, err = resilience.Failover(ctx, br, authorities, 0, tt.fn)
				}

				assert.Equal(b, err != nil, tt.fails, "the benchmark must measure the outcome of the case")
			})
		}
	})
}

// failovers returns the cases of the allocation ceilings of Failover: none
// for a success after at most one failed target, two for a failover whose
// every target fails with a Transient error, and five without one.
func failovers() []failover {
	invalid := errs.WithClass(errDependency, errs.Invalid)

	return []failover{
		{
			name: "of a first target that succeeds",
			fn:   func(context.Context, int) (int, error) { return 1, nil },
		},
		{
			name: "of a second target that succeeds after the first fails",
			fn: func(_ context.Context, i int) (int, error) {
				if i == 0 {
					return 0, invalid
				}

				return 1, nil
			},
		},
		{
			name:  "of targets that fail with a Transient error",
			fn:    func(context.Context, int) (int, error) { return 0, errTripping },
			want:  2,
			fails: true,
		},
		{
			name:  "of targets that fail without a Transient error",
			fn:    func(context.Context, int) (int, error) { return 0, invalid },
			want:  5,
			fails: true,
		},
	}
}

// newFailoverBreaker returns a Breaker of circuits whose failure threshold
// exceeds any count of calls, so every circuit stays closed and each
// failover calls every target, and fails tb when NewBreaker refuses it.
func newFailoverBreaker(tb testing.TB) *resilience.Breaker {
	tb.Helper()

	cfg := circuits
	cfg.Clock, cfg.FailureThreshold = fake.New(originUTC), math.MaxInt
	b, err := resilience.NewBreaker(cfg)
	assert.NoError(tb, err, "NewBreaker must accept a complete config")

	return b
}

// targets returns a function for Failover that returns the result of
// results for the index of its target, and records each index that it
// receives in *calls.
func targets(results []error, calls *[]int) func(context.Context, int) (int, error) {
	return func(_ context.Context, i int) (int, error) {
		*calls = append(*calls, i)
		if err := results[i]; err != nil {
			return 0, err
		}

		return i + 100, nil
	}
}
