// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package batch_test

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/history"

	"go.thesmos.sh/core/batch"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/errs"
)

// window is the accumulation window of every Loader of the tests.
const window = 5 * time.Millisecond

// joinBound is how long a case waits for its callers to join a batch, and
// for a Load to return. A correct Loader takes microseconds.
const joinBound = time.Second

// originUTC is the virtual time at which the fake clock of every case
// starts.
var originUTC = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// errDown is the error of a dependency that refused a batch.
var errDown = errors.New("batch_test: dependency down")

// resolve maps every key to twice its value and records the batches it
// receives.
type resolve struct {
	batches [][]int
	mu      sync.Mutex
}

// fn is the batch function: it records keys and returns twice each key.
func (r *resolve) fn(_ context.Context, keys []int) (map[int]int, error) {
	r.mu.Lock()
	r.batches = append(r.batches, slices.Clone(keys))
	r.mu.Unlock()

	out := make(map[int]int, len(keys))
	for _, k := range keys {
		out[k] = k * 2
	}

	return out, nil
}

// calls reports how many batches were dispatched.
func (r *resolve) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.batches)
}

// sizes reports each dispatched batch's size, largest first, so an
// assertion does not depend on goroutine arrival order.
func (r *resolve) sizes() []int {
	r.mu.Lock()
	defer r.mu.Unlock()

	got := make([]int, 0, len(r.batches))
	for _, b := range r.batches {
		got = append(got, len(b))
	}
	slices.Sort(got)
	slices.Reverse(got)

	return got
}

// result is the outcome of one Load, sent back from its goroutine.
type result struct {
	err error
	v   int
}

func TestLoader(t *testing.T) {
	t.Parallel()

	t.Run("NewLoader", func(t *testing.T) {
		t.Parallel()

		var r resolve
		tests := []struct {
			fn   func(context.Context, []int) (map[int]int, error)
			name string
			cfg  batch.LoaderConfig
		}{
			{r.fn, "returns ErrConfig for a nil clock", batch.LoaderConfig{Wait: window, MaxBatch: 1}},
			{r.fn, "returns ErrConfig for a zero wait", batch.LoaderConfig{Clock: fake.New(originUTC), MaxBatch: 1}},
			{r.fn, "returns ErrConfig for a negative wait", batch.LoaderConfig{
				Clock: fake.New(originUTC), Wait: -window, MaxBatch: 1,
			}},
			{r.fn, "returns ErrConfig for a zero batch size", batch.LoaderConfig{
				Clock: fake.New(originUTC), Wait: window,
			}},
			{r.fn, "returns ErrConfig for a negative batch size", batch.LoaderConfig{
				Clock: fake.New(originUTC), Wait: window, MaxBatch: -1,
			}},
			{nil, "returns ErrConfig for a nil function", batch.LoaderConfig{
				Clock: fake.New(originUTC), Wait: window, MaxBatch: 1,
			}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				l, err := batch.NewLoader(tt.cfg, tt.fn)
				assert.ErrorIs(t, err, batch.ErrConfig, "an invalid config must be refused")
				assert.Nil(t, l, "no Loader may be returned with an error")
				assert.Equal(t, errs.Classify(err), errs.Invalid, "ErrConfig must classify as Invalid")
			})
		}
	})

	t.Run("Load", func(t *testing.T) {
		t.Parallel()

		t.Run("dispatches one key after the window", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			var r resolve
			l := mustLoader(t, c, 10, r.fn)

			out := loadAsync(t.Context(), l, 21)

			c.AwaitWaiters(1)
			assert.Equal(t, r.calls(), 0, "the window must not have elapsed yet")
			c.Advance(window)

			got := await(t, out)
			assert.NoError(t, got.err, "the load must succeed")
			assert.Equal(t, got.v, 42, "the value must be the one fn returned")
			assert.Equal(t, r.calls(), 1, "one key must cost one call")
		})

		t.Run("coalesces concurrent distinct keys into one call", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			var r resolve
			l := mustLoader(t, c, 10, r.fn)

			outs := make([]<-chan result, 0, 4)
			for key := range 4 {
				outs = append(outs, loadAsync(t.Context(), l, key+1))
			}

			assert.EventuallyTrue(t, joinBound, func() bool { return l.Pending() == 4 }, "four callers must join")
			c.Advance(window)

			for i, out := range outs {
				got := await(t, out)
				assert.NoError(t, got.err, "every caller must be served")
				assert.Equal(t, got.v, (i+1)*2, "each caller must get its own key's value")
			}
			assert.Equal(t, r.sizes(), []int{4}, "four keys must travel as one batch")
		})

		t.Run("returns one result to concurrent loads of one key", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			var r resolve
			l := mustLoader(t, c, 10, r.fn)

			outs := make([]<-chan result, 0, 3)
			for range 3 {
				outs = append(outs, loadAsync(t.Context(), l, 7))
			}

			assert.EventuallyTrue(t, joinBound, func() bool { return l.Pending() == 3 }, "three callers must join")
			c.Advance(window)

			for _, out := range outs {
				got := await(t, out)
				assert.NoError(t, got.err, "every caller must be served")
				assert.Equal(t, got.v, 14, "every caller must get the same value")
			}
			assert.Equal(t, r.sizes(), []int{1}, "the key must travel once")
		})

		t.Run("dispatches a full batch without waiting for the window", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			var r resolve
			l := mustLoader(t, c, 2, r.fn)

			first := loadAsync(t.Context(), l, 1)
			assert.EventuallyTrue(t, joinBound, func() bool { return l.Pending() == 1 }, "the first caller must join")
			second := loadAsync(t.Context(), l, 2)

			// No Advance: reaching MaxBatch is what fires this batch.
			assert.NoError(t, await(t, first).err, "the first caller must be served")
			assert.NoError(t, await(t, second).err, "the second caller must be served")
			assert.Equal(t, r.sizes(), []int{2}, "the batch must fire at its limit")
		})

		t.Run("starts a fresh batch for a later load", func(t *testing.T) {
			t.Parallel()
			// Not a cache: a key loaded once is fetched again.
			c := fake.New(originUTC)
			var r resolve
			l := mustLoader(t, c, 10, r.fn)

			for range 2 {
				out := loadAsync(t.Context(), l, 3)
				c.AwaitWaiters(1)
				c.Advance(window)
				assert.NoError(t, await(t, out).err, "the load must succeed")
			}

			assert.Equal(t, r.calls(), 2, "the second load must reach fn")
		})

		t.Run("returns each caller its own key's value under concurrent loads", func(t *testing.T) {
			t.Parallel()
			const (
				clients = 64
				keys    = 8
			)

			c := fake.New(originUTC)
			var r resolve
			l := mustLoader(t, c, 4, r.fn)

			// Batches fire on MaxBatch mostly, but a partial one at the end
			// needs its window, so a goroutine keeps virtual time moving.
			stop := make(chan struct{})
			ticking := make(chan struct{})
			go func() {
				defer close(ticking)
				for {
					select {
					case <-stop:
						return
					default:
						c.Advance(window)
						runtime.Gosched()
					}
				}
			}()

			h := history.New()
			outcomes := history.Concurrently(clients, 2*joinBound, func(client int) (any, error) {
				key := client%keys + 1
				call := h.Invoke(client, "load", []any{key})
				v, err := l.Load(t.Context(), key)
				call.OK(result{err: err, v: v})

				return v, nil
			})
			close(stop)
			<-ticking

			for _, o := range outcomes {
				assert.True(t, o.Finished, "every caller must return")
			}
			history.Linearizable(t, h, history.Spec[struct{}]{
				Initial: func() struct{} { return struct{}{} },
				Next: func(s struct{}, op history.Operation) []struct{} {
					if !op.Returned(result{v: op.Args[0].(int) * 2}) {
						return nil
					}

					return []struct{}{s}
				},
			}, "every Load must return twice its own key, however the batches divide")
			assert.Equal(t, l.Pending(), 0, "every caller must have been released")
		})

		t.Run("returns ErrNotFound for a key missing from the result", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			l := mustLoader(t, c, 10, func(context.Context, []int) (map[int]int, error) {
				return map[int]int{}, nil
			})

			out := loadAsync(t.Context(), l, 9)
			c.AwaitWaiters(1)
			c.Advance(window)

			got := await(t, out)
			assert.ErrorIs(t, got.err, batch.ErrNotFound, "an unresolved key must be an error")
			assert.Equal(t, errs.Classify(got.err), errs.NotFound, "absence must classify as NotFound")
			assert.Equal(t, got.v, 0, "an unresolved key must yield the zero value")
		})

		t.Run("returns the error of a failed batch to every waiter", func(t *testing.T) {
			t.Parallel()
			// The error describes the call rather than any one key, so it
			// is not wrapped per key.
			c := fake.New(originUTC)
			l := mustLoader(t, c, 10, failOn(1, errDown))

			first := loadAsync(t.Context(), l, 1)
			second := loadAsync(t.Context(), l, 2)
			assert.EventuallyTrue(t, joinBound, func() bool { return l.Pending() == 2 }, "two callers must join")
			c.Advance(window)

			assert.ErrorIs(t, await(t, first).err, errDown, "the first caller must see the failure")
			assert.ErrorIs(t, await(t, second).err, errDown, "the second caller must see the failure")
		})

		t.Run("returns the context error to a caller that gives up", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			var r resolve
			l := mustLoader(t, c, 10, r.fn)

			ctx, cancel := context.WithCancel(t.Context())
			out := loadAsync(ctx, l, 1)
			assert.EventuallyTrue(t, joinBound, func() bool { return l.Pending() == 1 }, "the caller must join")
			cancel()

			assert.ErrorIs(t, await(t, out).err, context.Canceled, "the caller's own context must be reported")
		})

		t.Run("serves the callers that remain after one leaves", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			var r resolve
			l := mustLoader(t, c, 10, r.fn)

			ctx, cancel := context.WithCancel(t.Context())
			leaving := loadAsync(ctx, l, 1)
			staying := loadAsync(t.Context(), l, 2)
			assert.EventuallyTrue(t, joinBound, func() bool { return l.Pending() == 2 }, "two callers must join")

			cancel()
			assert.ErrorIs(t, await(t, leaving).err, context.Canceled, "the departing caller must be released")

			c.Advance(window)
			got := await(t, staying)
			assert.NoError(t, got.err, "the remaining caller must still be served")
			assert.Equal(t, got.v, 4, "the batch must have run")
		})

		t.Run("keeps the batch context live while a caller remains", func(t *testing.T) {
			t.Parallel()
			// The batch is abandoned only when the last caller goes. One
			// leaving early must not cancel work the others still want.
			c := fake.New(originUTC)
			proceed := make(chan struct{})
			live := make(chan bool, 1)
			l := mustLoader(t, c, 10, func(ctx context.Context, keys []int) (map[int]int, error) {
				<-proceed
				live <- ctx.Err() == nil

				return map[int]int{keys[0]: 7}, nil
			})

			ctx, cancel := context.WithCancel(t.Context())
			leaving := loadAsync(ctx, l, 1)
			staying := loadAsync(t.Context(), l, 1)
			assert.EventuallyTrue(t, joinBound, func() bool { return l.Pending() == 2 }, "two callers must join")
			c.Advance(window)

			cancel()
			assert.ErrorIs(t, await(t, leaving).err, context.Canceled, "the departing caller must be released")

			close(proceed)
			got := await(t, staying)
			assert.NoError(t, got.err, "the remaining caller must still be served")
			assert.Equal(t, got.v, 7, "the batch must have run")
			assert.True(t, <-live, "the batch must stay live while a caller remains")
		})

		t.Run("cancels the batch context once every caller is gone", func(t *testing.T) {
			t.Parallel()
			// Otherwise a cancellation storm leaves the dependency serving
			// work nobody is waiting for.
			c := fake.New(originUTC)
			gone := make(chan struct{})
			l := mustLoader(t, c, 10, func(ctx context.Context, _ []int) (map[int]int, error) {
				<-ctx.Done()
				close(gone)

				return nil, ctx.Err()
			})

			// Two callers wait on one key, so the case depends on the count
			// a joiner adds. A join that did not register would leave the
			// batch running for callers that had all gone.
			ctx, cancel := context.WithCancel(t.Context())
			first := loadAsync(ctx, l, 1)
			second := loadAsync(ctx, l, 1)
			assert.EventuallyTrue(t, joinBound, func() bool { return l.Pending() == 2 }, "two callers must join")
			c.Advance(window)

			cancel()
			assert.ErrorIs(t, await(t, first).err, context.Canceled, "the caller must be released")
			assert.ErrorIs(t, await(t, second).err, context.Canceled, "the caller must be released")

			assert.CompletesWithin(t, joinBound, func(ctx context.Context) error {
				select {
				case <-gone:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}, "the batch context must be cancelled")
		})

		t.Run("passes no caller's values to the batch function", func(t *testing.T) {
			t.Parallel()
			// A batch serves several callers, so taking values from one of
			// them would attribute its span to whichever caller happened to
			// arrive first.
			type ctxKey struct{}

			c := fake.New(originUTC)
			seen := make(chan any, 1)
			l := mustLoader(t, c, 10, func(ctx context.Context, keys []int) (map[int]int, error) {
				seen <- ctx.Value(ctxKey{})

				return map[int]int{keys[0]: 1}, nil
			})

			out := loadAsync(context.WithValue(t.Context(), ctxKey{}, "span"), l, 1)
			c.AwaitWaiters(1)
			c.Advance(window)
			assert.NoError(t, await(t, out).err, "the load must succeed")

			assert.Nil(t, <-seen, "a caller's values must not reach fn")
		})
	})

	t.Run("LoadAll", func(t *testing.T) {
		t.Parallel()

		t.Run("dispatches without waiting for the window", func(t *testing.T) {
			t.Parallel()
			// A caller holding the full key set has nothing to wait for.
			var r resolve
			l := mustLoader(t, fake.New(originUTC), 10, r.fn)

			got, err := l.LoadAll(t.Context(), []int{1, 2, 3})
			assert.NoError(t, err, "LoadAll must succeed")
			assert.Equal(t, got, map[int]int{1: 2, 2: 4, 3: 6}, "every key must be resolved")
			assert.Equal(t, r.sizes(), []int{3}, "one batch must carry them all")
		})

		t.Run("splits keys beyond the batch limit", func(t *testing.T) {
			t.Parallel()
			var r resolve
			l := mustLoader(t, fake.New(originUTC), 2, r.fn)

			got, err := l.LoadAll(t.Context(), []int{1, 2, 3, 4, 5})
			assert.NoError(t, err, "LoadAll must succeed")
			assert.Length(t, got, 5, "every key must be resolved")
			assert.Equal(t, r.sizes(), []int{2, 2, 1}, "the limit must bound each call")
		})

		t.Run("collapses duplicate keys", func(t *testing.T) {
			t.Parallel()
			var r resolve
			l := mustLoader(t, fake.New(originUTC), 10, r.fn)

			got, err := l.LoadAll(t.Context(), []int{1, 1, 2, 1})
			assert.NoError(t, err, "LoadAll must succeed")
			assert.Equal(t, got, map[int]int{1: 2, 2: 4}, "each key must resolve once")
			assert.Equal(t, r.sizes(), []int{2}, "a key must not travel twice")
		})

		t.Run("returns an empty map without a call for no keys", func(t *testing.T) {
			t.Parallel()
			var r resolve
			l := mustLoader(t, fake.New(originUTC), 10, r.fn)

			got, err := l.LoadAll(t.Context(), nil)
			assert.NoError(t, err, "an empty request is not an error")
			assert.Equal(t, got, map[int]int{}, "no keys must return an empty map, not nil")
			assert.Equal(t, r.calls(), 0, "there is nothing to call")
		})

		t.Run("omits a key missing from the result", func(t *testing.T) {
			t.Parallel()
			// LoadAll returns a map, so absence is representable without an
			// error, where Load has no such option.
			l := mustLoader(t, fake.New(originUTC), 10, func(_ context.Context, keys []int) (map[int]int, error) {
				return map[int]int{keys[0]: 1}, nil
			})

			got, err := l.LoadAll(t.Context(), []int{1, 2})
			assert.NoError(t, err, "an unresolved key is not a failed call")
			assert.Equal(t, got, map[int]int{1: 1}, "only the resolved key must be returned")
		})

		t.Run("returns the error of a failed batch", func(t *testing.T) {
			t.Parallel()
			l := mustLoader(t, fake.New(originUTC), 1, failOn(2, errDown))

			got, err := l.LoadAll(t.Context(), []int{1, 2})
			assert.ErrorIs(t, err, errDown, "one failed chunk must fail the call")
			assert.Nil(t, got, "a partial result must not be returned")
		})

		t.Run("cancels the other batches of the call when one fails", func(t *testing.T) {
			t.Parallel()
			// The result of the other batches is discarded, so their calls
			// stop as soon as one batch fails. The failing batch waits for
			// the other to start, because task.Each does not call fn for an
			// element it claims after the first failure.
			var cause error
			started := make(chan struct{})
			l := mustLoader(t, fake.New(originUTC), 1, func(ctx context.Context, keys []int) (map[int]int, error) {
				if keys[0] == 2 {
					<-started

					return nil, errDown
				}

				close(started)
				select {
				case <-ctx.Done():
					cause = context.Cause(ctx)
				case <-time.After(joinBound):
				}

				return nil, ctx.Err()
			})

			_, err := l.LoadAll(t.Context(), []int{1, 2})
			assert.ErrorIs(t, err, errDown, "the failed batch must be the result")
			assert.ErrorIs(t, cause, errDown, "the other batch must be cancelled with the failure as the cause")
		})

		t.Run("passes the values of ctx to the batch function", func(t *testing.T) {
			t.Parallel()
			// LoadAll serves one caller, so the batch function sees that
			// caller's trace span and request identifier.
			type spanKey struct{}
			var value any
			l := mustLoader(t, fake.New(originUTC), 10, func(ctx context.Context, keys []int) (map[int]int, error) {
				value = ctx.Value(spanKey{})

				return map[int]int{keys[0]: 1}, nil
			})

			_, err := l.LoadAll(context.WithValue(t.Context(), spanKey{}, "span"), []int{1})
			assert.NoError(t, err, "LoadAll must succeed")
			assert.Equal(t, value, any("span"), "the caller's values must reach the batch function")
		})

		t.Run("releases the Loader for the calls after it", func(t *testing.T) {
			t.Parallel()
			var r resolve
			l := mustLoader(t, fake.New(originUTC), 10, r.fn)

			_, err := l.LoadAll(t.Context(), []int{1})
			assert.NoError(t, err, "LoadAll must succeed")
			assert.CompletesWithin(t, joinBound, func(context.Context) error { return l.Close() },
				"a call after LoadAll must not wait for the Loader")
		})
	})

	t.Run("Close", func(t *testing.T) {
		t.Parallel()

		t.Run("makes Load return ErrClosed", func(t *testing.T) {
			t.Parallel()
			var r resolve
			l := mustLoader(t, fake.New(originUTC), 10, r.fn)
			assert.FailsAfterClose(t, l.Close, func() error {
				_, err := l.Load(t.Context(), 1)

				return err
			}, batch.ErrClosed, "a closed loader must refuse a Load")
		})

		t.Run("makes LoadAll return ErrClosed", func(t *testing.T) {
			t.Parallel()
			var r resolve
			l := mustLoader(t, fake.New(originUTC), 10, r.fn)
			assert.FailsAfterClose(t, l.Close, func() error {
				_, err := l.LoadAll(t.Context(), []int{1})

				return err
			}, batch.ErrClosed, "a closed loader must refuse a LoadAll")
		})

		t.Run("leaves the Loader as one Close left it when called again", func(t *testing.T) {
			t.Parallel()
			// Close belongs in a defer, and a defer that fails on the second
			// call is worse than one that does nothing.
			var r resolve
			l := mustLoader(t, fake.New(originUTC), 10, r.fn)
			assert.Idempotent(t, func(struct{}) error { return l.Close() }, struct{}{}, func() error {
				_, err := l.Load(t.Context(), 1)

				return err
			}, "a second Close must change nothing")
		})

		t.Run("lets an accumulating batch run", func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			var r resolve
			l := mustLoader(t, c, 10, r.fn)

			out := loadAsync(t.Context(), l, 1)
			c.AwaitWaiters(1)
			assert.NoError(t, l.Close(), "Close must succeed")
			c.Advance(window)

			got := await(t, out)
			assert.NoError(t, got.err, "a caller already waiting must still be served")
			assert.Equal(t, got.v, 2, "the batch in flight must run to completion")
		})
	})
}

// failOn returns a batch function that refuses any batch containing key
// and resolves the first key of every other batch.
func failOn(key int, err error) func(context.Context, []int) (map[int]int, error) {
	return func(_ context.Context, keys []int) (map[int]int, error) {
		if slices.Contains(keys, key) {
			return nil, err
		}

		return map[int]int{keys[0]: 1}, nil
	}
}

// mustLoader returns a Loader under c with the window of the tests, and
// fails the test when NewLoader refuses the config.
func mustLoader(
	tb testing.TB, c *fake.Clock, maxBatch int,
	fn func(context.Context, []int) (map[int]int, error),
) *batch.Loader[int, int] {
	tb.Helper()

	l, err := batch.NewLoader(batch.LoaderConfig{Clock: c, Wait: window, MaxBatch: maxBatch}, fn)
	assert.NoError(tb, err, "NewLoader must accept a valid config")

	return l
}

// loadAsync starts a Load and returns the channel that receives its
// outcome.
func loadAsync(ctx context.Context, l *batch.Loader[int, int], key int) <-chan result {
	out := make(chan result, 1)
	go func() {
		v, err := l.Load(ctx, key)
		out <- result{err, v}
	}()

	return out
}

// await returns one outcome, and fails the test when none arrives within
// joinBound.
func await(tb testing.TB, out <-chan result) result {
	tb.Helper()

	select {
	case r := <-out:
		return r
	case <-time.After(joinBound):
		tb.Fatal("Load never returned")

		return result{}
	}
}
