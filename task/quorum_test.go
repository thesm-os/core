// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package task_test

import (
	"context"
	"errors"
	"math"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"weak"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/task"
)

// quorumAllocs is the number of allocations of one call of Quorum whose
// calls all succeed. For any number of elements, the call allocates the
// derived context, its cancel function, the quorum, the channel of its
// places and the closure that its goroutines share.
const quorumAllocs = 5

// runnerAllocs is the number of allocations of one call of QuorumRunner.Run
// whose calls all succeed, after the first call: the derived context and
// its cancel function.
const runnerAllocs = 2

// payloadKey is the key of the value of the context of a case.
type payloadKey struct{}

// payloadError is a failure of 64 bytes, so a case can watch it with a
// weak pointer.
type payloadError [64]byte

// Error returns the text of the failure.
func (*payloadError) Error() string {
	return "task_test: a failure with a payload"
}

// quorumRun is one call of Quorum that runs on a goroutine of its own.
type quorumRun struct {
	// done is a buffered channel of the error that Quorum returns.
	done chan error

	// calls contains the number of calls of each element.
	calls []atomic.Int32
}

// startQuorum calls Quorum over n elements on a goroutine of its own. It
// counts each call of an element before fn runs.
func startQuorum(ctx context.Context, limit, k, n int, fn func(ctx context.Context, i int) error) *quorumRun {
	r := &quorumRun{done: make(chan error, 1), calls: make([]atomic.Int32, n)}
	go func() {
		r.done <- task.Quorum(ctx, limit, k, indices(n), func(ctx context.Context, i, _ int) error {
			r.calls[i].Add(1)

			return fn(ctx, i)
		})
	}()

	return r
}

// TestQuorum covers the decision of Quorum and the start of its calls. A
// case that depends on the order of the results orders the calls with a
// barrier or with synctest.Wait. The rules that Quorum shares with the
// other functions of the package are in TestScope.
func TestQuorum(t *testing.T) {
	t.Parallel()

	t.Run("cancels the calls still running once k succeed", func(t *testing.T) {
		t.Parallel()
		start := barrier(5)

		var (
			cancelled atomic.Int32
			err       error
		)
		assert.CompletesWithin(t, returnBound, func(ctx context.Context) error {
			err = task.Quorum(ctx, 5, 2, indices(5), func(ctx context.Context, i, _ int) error {
				start()
				if i < 2 {
					return nil
				}

				if errors.Is(awaitCancel(ctx), context.Canceled) {
					cancelled.Add(1)
				}

				return errBoom
			})

			return err
		}, "Quorum must return once its calls return")

		assert.NoError(t, err, "two successes must meet a quorum of two")
		assert.Equal(t, cancelled.Load(), 3, "the three calls still running must be cancelled")
	})

	t.Run("returns ErrNoQuorum joined with the failures that decided it", func(t *testing.T) {
		t.Parallel()
		start := barrier(5)
		failures := []error{errors.New("task_test: a"), errors.New("task_test: b"), errors.New("task_test: c")}

		var (
			causes [5]error
			err    error
		)
		assert.CompletesWithin(t, returnBound, func(ctx context.Context) error {
			err = task.Quorum(ctx, 5, 3, indices(5), func(ctx context.Context, i, _ int) error {
				start()
				if i < len(failures) {
					return failures[i]
				}

				causes[i] = awaitCancel(ctx)

				return causes[i]
			})

			return err
		}, "Quorum must return once its calls return")

		assert.ErrorIs(t, err, task.ErrNoQuorum, "three failures of five must make a quorum of three impossible")
		for i, f := range failures {
			assert.ErrorIs(t, err, f, "failure "+strconv.Itoa(i)+" must be joined with ErrNoQuorum")
			assert.Contains(t, err.Error(), "item "+strconv.Itoa(i)+": "+f.Error(),
				"failure "+strconv.Itoa(i)+" must be wrapped with its index")
		}
		assert.ErrorIsNot(t, err, context.Canceled, "the cancelled calls must not be joined")
		assert.ErrorIs(t, causes[3], task.ErrNoQuorum, "the calls still running must be cancelled with the result")
	})

	t.Run("does not call fn after the decision", func(t *testing.T) {
		t.Parallel()

		var runs atomic.Int32
		err := task.Quorum(t.Context(), 1, 1, indices(5), func(context.Context, int, int) error {
			runs.Add(1)

			return nil
		})

		assert.NoError(t, err, "one success must meet a quorum of one")
		assert.Equal(t, runs.Load(), 1, "fn must not run after the quorum is met")
	})

	t.Run("does not start a call beyond k while the calls in flight can meet the quorum", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			release := make(chan struct{})
			r := startQuorum(t.Context(), 2, 2, 3, func(ctx context.Context, i int) error {
				if i == 1 {
					return awaitRelease(ctx, release)
				}

				return nil
			})
			synctest.Wait()
			calls := r.calls[2].Load()
			close(release)

			assert.NoError(t, <-r.done, "two successes must meet a quorum of two")
			assert.Equal(t, calls, 0, "a success must not start a call while the call in flight can meet the quorum")
		})
	})

	t.Run("starts the next element in place of a failed call", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			r := startQuorum(t.Context(), 2, 2, 3, func(_ context.Context, i int) error {
				if i == 1 {
					return errBoom
				}

				return nil
			})

			assert.NoError(t, <-r.done, "the element after the failure must meet the quorum")
			assert.Equal(t, r.calls[2].Load(), 1, "a failure must start the next element")
		})
	})

	t.Run("runs at most limit minus k calls beyond the quorum", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			release := make(chan struct{})
			r := startQuorum(t.Context(), 3, 2, 5, func(ctx context.Context, i int) error {
				if i == 0 {
					return nil
				}

				return awaitRelease(ctx, release)
			})
			synctest.Wait()
			spare, beyond := r.calls[2].Load(), r.calls[3].Load()+r.calls[4].Load()
			close(release)

			assert.NoError(t, <-r.done, "the calls after the release must meet the quorum")
			expect.Equal(t, spare, 1, "a limit of three must run one call beyond a quorum of two")
			expect.Equal(t, beyond, 0, "a limit of three must not run a second call beyond a quorum of two")
		})
	})

	t.Run("does not start a call beyond k when limit is below k", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			release := make(chan struct{})
			r := startQuorum(t.Context(), 2, 3, 5, func(ctx context.Context, i int) error {
				if i == 1 {
					return awaitRelease(ctx, release)
				}

				return nil
			})
			synctest.Wait()
			beyond := r.calls[3].Load() + r.calls[4].Load()
			close(release)

			assert.NoError(t, <-r.done, "three successes must meet a quorum of three")
			assert.Equal(t, beyond, 0, "a quorum of three must not start a fourth call while a call is in flight")
		})
	})

	t.Run("starts the next element in place of a call that ends by runtime.Goexit", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			r := startQuorum(t.Context(), 1, 1, 2, func(_ context.Context, i int) error {
				if i == 0 {
					runtime.Goexit()
				}

				return nil
			})

			assert.NoError(t, <-r.done, "the element after the exit must meet the quorum")
			assert.Equal(t, r.calls[1].Load(), 1, "an exit must start the next element")
		})
	})

	t.Run("returns ErrNoQuorum when every call ends by runtime.Goexit", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			r := startQuorum(t.Context(), 1, 1, 2, func(context.Context, int) error {
				runtime.Goexit()

				return nil
			})

			assert.That(t, <-r.done).
				ErrorIs(task.ErrNoQuorum, "two exits must make a quorum of one impossible").
				ErrorIs(task.ErrExited, "each exit must be joined as ErrExited")
		})
	})

	t.Run("starts a call only in a free place of the quorum", func(t *testing.T) {
		t.Parallel()
		prop.ForAll(t, "Quorum must start a call only in a free place of its quorum", func(c *prop.Case) {
			n := c.Draw(prop.Integer(1, 8), "n")
			k := c.Draw(prop.Integer(1, n), "k")
			limit := c.Draw(prop.Integer(1, 10), "limit")
			ok := c.Draw(prop.List(prop.Boolean(), prop.MinSize(n), prop.MaxSize(n)), "ok")

			var (
				mu                          sync.Mutex
				calls, failed, peak         int
				inFlight, succeeded, places int
			)
			err := task.Quorum(c.Context(), limit, k, indices(n), func(_ context.Context, i, _ int) error {
				mu.Lock()
				calls++
				inFlight++
				peak = max(peak, inFlight)
				places = max(places, inFlight+succeeded)
				mu.Unlock()
				runtime.Gosched()

				mu.Lock()
				defer mu.Unlock()
				inFlight--
				if ok[i] {
					succeeded++

					return nil
				}
				failed++

				return errBoom
			})

			free := min(max(k, limit), n)
			expect.InRange(c, peak, 1, float64(limit), "at most limit calls may run at once")
			expect.InRange(c, places, 1, float64(free), "the calls in flight and the successes must fit the places")
			expect.InRange(c, calls, 1, float64(min(n, free+failed)),
				"each call beyond the places must replace a failure")
			want := 0
			for _, s := range ok {
				if s {
					want++
				}
			}
			if want >= k {
				expect.NoError(c, err, "a quorum with k elements that succeed must be met")
			} else {
				expect.ErrorIs(c, err, task.ErrNoQuorum, "a quorum with fewer than k elements that succeed must fail")
			}
		})
	})

	t.Run("returns the cause of ctx when the quorum becomes impossible after ctx ended", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancelCause(t.Context())
		cause := errors.New("task_test: shutting down")
		start := barrier(3)

		var err error
		assert.CompletesWithin(t, returnBound, func(context.Context) error {
			err = task.Quorum(ctx, 3, 3, indices(3), func(ctx context.Context, i, _ int) error {
				start()
				if i == 0 {
					cancel(cause)

					return nil
				}

				return awaitCancel(ctx)
			})

			return err
		}, "Quorum must return once its calls return")

		assert.That(t, err).
			ErrorIs(cause, "the end of ctx must be the result").
			ErrorIsNot(task.ErrNoQuorum, "a quorum cut short by ctx must not report ErrNoQuorum")
	})

	t.Run("returns the cause of ctx when ctx ends with elements unclaimed", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancelCause(t.Context())
		cause := errors.New("task_test: shutting down")

		err := task.Quorum(ctx, 1, 3, indices(3), func(context.Context, int, int) error {
			cancel(cause)

			return nil
		})

		assert.ErrorIs(t, err, cause, "the end of ctx must be the result")
	})

	t.Run("classifies ErrNoQuorum as the class of its only failure", func(t *testing.T) {
		t.Parallel()
		err := task.Quorum(t.Context(), 1, 1, indices(1), func(context.Context, int, int) error {
			return errs.WithClass(errBoom, errs.Transient)
		})

		assert.ErrorIs(t, err, task.ErrNoQuorum, "the only call failing must make the quorum impossible")
		assert.Equal(t, errs.Classify(err), errs.Transient, "the failure's class must be the result's class")
	})

	t.Run("classifies ErrNoQuorum as the class of highest rank among its failures", func(t *testing.T) {
		t.Parallel()
		failures := []error{errs.WithClass(errBoom, errs.Transient), errs.WithClass(errLate, errs.Integrity)}
		err := task.Quorum(t.Context(), 1, 1, failures, func(_ context.Context, _ int, f error) error { return f })
		assert.Equal(t, errs.Classify(err), errs.Integrity, "Integrity must outrank Transient")
	})

	t.Run("classifies ErrNoQuorum whatever the order of its failures", func(t *testing.T) {
		t.Parallel()
		// One worker claims the items in order, so the failures are
		// recorded in the order of the slice.
		fail := func(_ context.Context, _ int, f error) error { return f }
		prop.Commutative(t, func(a, b errs.Class) errs.Class {
			failures := []error{errs.WithClass(errBoom, a), errs.WithClass(errLate, b)}

			return errs.Classify(task.Quorum(t.Context(), 1, 1, failures, fail))
		}, "the class must not depend on the order of the failures",
			prop.Using(prop.Integer[errs.Class](errs.Unspecified, errs.Integrity)))
	})

	t.Run("returns ErrQuorumSize for a quorum outside one to the number of items", func(t *testing.T) {
		t.Parallel()

		var runs atomic.Int32
		prop.ErrorIs(t, func(k int) error {
			return task.Quorum(t.Context(), 1, k, indices(3), func(context.Context, int, int) error {
				runs.Add(1)

				return nil
			})
		}, task.ErrQuorumSize, "a quorum outside one to three of three must be refused",
			prop.Using(prop.OneOf(prop.Integer(math.MinInt, 0), prop.Integer(4, math.MaxInt))),
			prop.Example(-1), prop.Example(0), prop.Example(4))
		assert.Equal(t, runs.Load(), 0, "a refused call must not run fn")
		assert.Equal(t, errs.Classify(task.ErrQuorumSize), errs.Invalid, "ErrQuorumSize must classify as Invalid")
	})

	t.Run("QuorumRunner", func(t *testing.T) {
		t.Parallel()

		t.Run("Run", func(t *testing.T) {
			t.Parallel()

			t.Run("returns the result of Quorum for each quorum of a sequence", func(t *testing.T) {
				t.Parallel()
				prop.ForAll(t, "a QuorumRunner must return the result of Quorum for each quorum", func(c *prop.Case) {
					// One worker claims the elements in order, so the results of
					// a quorum do not depend on the order of the goroutines.
					ctx, cancel := context.WithTimeout(c.Context(), returnBound)
					defer cancel()

					var r task.QuorumRunner[int]
					for range c.Draw(prop.Integer(1, 5), "quorums") {
						n := c.Draw(prop.Integer(1, 6), "n")
						k := c.Draw(prop.Integer(1, n), "k")
						ok := c.Draw(prop.List(prop.Boolean(), prop.MinSize(n), prop.MaxSize(n)), "ok")
						fn := func(_ context.Context, i, _ int) error {
							if ok[i] {
								return nil
							}

							return errBoom
						}

						want := task.Quorum(ctx, 1, k, indices(n), fn)
						assert.Equal(c, r.Run(ctx, 1, k, indices(n), fn), want,
							"a reused QuorumRunner must return the result of a new one")
					}
				})
			})

			t.Run("gives every place back to the next call", func(t *testing.T) {
				t.Parallel()

				var r task.QuorumRunner[int]
				succeed := func(context.Context, int, int) error { return nil }
				err := r.Run(t.Context(), 3, 3, indices(3), succeed)
				assert.NoError(t, err, "three successes must meet a quorum of three")
				assert.CompletesWithin(t, returnBound, func(ctx context.Context) error {
					return r.Run(ctx, 3, 3, indices(3), succeed)
				}, "the next call must find every place free")
			})

			t.Run("runs concurrent calls one after the other", func(t *testing.T) {
				t.Parallel()

				var (
					r        task.QuorumRunner[int]
					active   atomic.Int32
					overlaps atomic.Int32
					failed   atomic.Int32
					wg       sync.WaitGroup
				)
				solo := func(context.Context, int, int) error {
					if active.Add(1) > 1 {
						overlaps.Add(1)
					}
					runtime.Gosched()
					active.Add(-1)

					return nil
				}
				for range 4 {
					wg.Go(func() {
						for range 20 {
							if r.Run(t.Context(), 1, 1, indices(1), solo) != nil {
								failed.Add(1)
							}
						}
					})
				}
				wg.Wait()
				expect.Equal(t, overlaps.Load(), 0, "the calls of two runs must not overlap")
				expect.Equal(t, failed.Load(), 0, "every run must meet its quorum")
			})

			t.Run("returns ErrLimit for a limit below one", func(t *testing.T) {
				t.Parallel()

				var r task.QuorumRunner[int]
				err := r.Run(t.Context(), 0, 1, indices(1), func(context.Context, int, int) error { return nil })
				assert.ErrorIs(t, err, task.ErrLimit, "a limit of zero must be refused")
			})

			t.Run("does not keep the items of a call", func(t *testing.T) {
				t.Parallel()

				var r task.QuorumRunner[*[64]byte]
				items := []*[64]byte{new([64]byte)}
				w := weak.Make(items[0])
				err := r.Run(t.Context(), 1, 1, items, func(context.Context, int, *[64]byte) error { return nil })
				assert.NoError(t, err, "one success must meet a quorum of one")

				runtime.GC()
				assert.Nil(t, w.Value(), "the QuorumRunner must not refer to the items of a call that returned")
				runtime.KeepAlive(&r)
			})

			t.Run("does not keep the function of a call", func(t *testing.T) {
				t.Parallel()

				var w weak.Pointer[[64]byte]
				fn := func() func(context.Context, int, int) error {
					value := new([64]byte)
					w = weak.Make(value)

					return func(context.Context, int, int) error {
						value[0]++

						return nil
					}
				}()

				var r task.QuorumRunner[int]
				assert.NoError(t, r.Run(t.Context(), 1, 1, indices(1), fn), "one success must meet a quorum of one")

				runtime.GC()
				assert.Nil(t, w.Value(), "the QuorumRunner must not refer to the function of a call that returned")
				runtime.KeepAlive(&r)
			})

			t.Run("does not keep the context of a call", func(t *testing.T) {
				t.Parallel()

				value := new([64]byte)
				w := weak.Make(value)
				ctx := context.WithValue(t.Context(), payloadKey{}, value)

				var r task.QuorumRunner[int]
				assert.NoError(t, r.Run(ctx, 1, 1, indices(1), func(context.Context, int, int) error { return nil }),
					"one success must meet a quorum of one")

				runtime.GC()
				assert.Nil(t, w.Value(), "the QuorumRunner must not refer to the context of a call that returned")
				runtime.KeepAlive(&r)
			})

			t.Run("does not keep the failures of a call", func(t *testing.T) {
				t.Parallel()

				failure := new(payloadError)
				w := weak.Make(failure)

				var r task.QuorumRunner[int]
				err := r.Run(t.Context(), 1, 1, indices(1), func(context.Context, int, int) error { return failure })
				assert.ErrorIs(t, err, task.ErrNoQuorum, "the only call failing must make the quorum impossible")

				runtime.GC()
				assert.Nil(t, w.Value(), "the QuorumRunner must not refer to the failures of a call that returned")
				runtime.KeepAlive(&r)
			})
		})
	})
}

// TestQuorumAllocs checks the allocation contract of Quorum over 64 and
// 512 elements whose calls succeed. MaxAllocs counts the allocations of
// the whole process, so the test does not run in parallel.
func TestQuorumAllocs(t *testing.T) {
	ctx := t.Context()
	succeed := func(context.Context, int, int) error { return nil }

	for _, n := range []int{64, 512} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			items := indices(n)

			var err error
			expect.MaxAllocs(t, func() { err = task.Quorum(ctx, 1, n, items, succeed) }, quorumAllocs,
				"the allocations of Quorum must not depend on the number of elements")
			assert.NoError(t, err, "the test must measure a quorum that is met")
		})
	}

	t.Run("with every place taken when a call succeeds", func(t *testing.T) {
		// Each call returns once the three calls have started, so the worker
		// of the first success finds every place taken and no element left.
		items := indices(3)

		var started atomic.Int32
		await := func(context.Context, int, int) error {
			started.Add(1)
			for started.Load() < 3 {
				runtime.Gosched()
			}

			return nil
		}

		var err error
		expect.MaxAllocs(t, func() {
			started.Store(0)
			err = task.Quorum(ctx, 3, 3, items, await)
		}, quorumAllocs, "a worker without an element left must not wait for a place")
		assert.NoError(t, err, "the test must measure a quorum that is met")
	})

	t.Run("QuorumRunner", func(t *testing.T) {
		t.Run("Run", func(t *testing.T) {
			items := indices(64)

			var (
				r   task.QuorumRunner[int]
				err error
			)
			expect.MaxAllocs(t, func() { err = r.Run(ctx, 1, 64, items, succeed) }, runnerAllocs,
				"Run must allocate only the derived context and its cancel function")
			assert.NoError(t, err, "the test must measure a quorum that is met")
		})
	})
}

// BenchmarkQuorum measures a quorum of two of five witnesses whose calls
// succeed at once, through Quorum and through a QuorumRunner that runs one
// quorum after the other.
func BenchmarkQuorum(b *testing.B) {
	witnesses := indices(5)

	var sum atomic.Int64
	cosign := func(_ context.Context, i, _ int) error {
		sum.Add(int64(i))

		return nil
	}

	b.Run("Quorum", func(b *testing.B) {
		var err error

		c := bench.Start(b).Warmup(benchWarmup).MaxAllocs(quorumAllocs)
		defer c.End()

		for c.Loop() {
			err = task.Quorum(b.Context(), len(witnesses), 2, witnesses, cosign)
		}

		assert.NoError(b, err, "the benchmark must measure a quorum that is met")
	})

	b.Run("QuorumRunner", func(b *testing.B) {
		b.Run("Run", func(b *testing.B) {
			var (
				r   task.QuorumRunner[int]
				err error
			)

			c := bench.Start(b).Warmup(benchWarmup).MaxAllocs(runnerAllocs)
			defer c.End()

			for c.Loop() {
				err = r.Run(b.Context(), len(witnesses), 2, witnesses, cosign)
			}

			assert.NoError(b, err, "the benchmark must measure a quorum that is met")
		})
	})
}

// barrier returns a function that each of n calls runs first. It returns
// once all n calls have started. A call that never starts leaves the
// others waiting, and the bound of CompletesWithin fails the case.
func barrier(n int) func() {
	var wg sync.WaitGroup
	wg.Add(n)

	return func() {
		wg.Done()
		wg.Wait()
	}
}

// awaitRelease waits until release is closed and returns nil. It returns
// the cause of ctx when ctx ends first.
func awaitRelease(ctx context.Context, release <-chan struct{}) error {
	select {
	case <-release:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
