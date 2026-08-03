// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package batch_test

import (
	"context"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/batch"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/errs"
)

var originUTC = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// errDown stands in for a dependency that refused a batch.
var errDown = testkit.TestError("dependency down")

// failOn returns a batch function that refuses any batch containing
// key and resolves the rest.
func failOn(key int, err error) func(context.Context, []int) (map[int]int, error) {
	return func(_ context.Context, keys []int) (map[int]int, error) {
		if slices.Contains(keys, key) {
			return nil, err
		}

		return map[int]int{keys[0]: 1}, nil
	}
}

const window = 5 * time.Millisecond

// awaitPending spins until n callers are waiting. Mirrors
// fake.Clock.AwaitWaiters: coalescing is only observable once every
// caller has joined, and a sleep would be a guess about when that is.
func awaitPending(l *batch.Loader[int, int], n int) {
	for l.Pending() < n {
		runtime.Gosched()
	}
}

// resolve answers every key with its own doubling, recording the
// batches it was given.
type resolve struct {
	batches [][]int
	mu      sync.Mutex
}

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

func mustLoader(
	tb testing.TB, c *fake.Clock, maxBatch int,
	fn func(context.Context, []int) (map[int]int, error),
) *batch.Loader[int, int] {
	tb.Helper()

	l, err := batch.NewLoader(batch.LoaderConfig{
		Clock: c, Wait: window, MaxBatch: maxBatch,
	}, fn)
	testkit.NoError(tb, err, "NewLoader must accept a valid config")

	return l
}

// result is one Load outcome, carried back from its goroutine.
type result struct {
	err error
	v   int
}

// loadAsync starts a Load and returns the channel carrying its
// outcome.
func loadAsync(ctx context.Context, l *batch.Loader[int, int], key int) <-chan result {
	out := make(chan result, 1)
	go func() {
		v, err := l.Load(ctx, key)
		out <- result{err, v}
	}()

	return out
}

// await takes one outcome, failing rather than hanging.
func await(tb testing.TB, out <-chan result) result {
	tb.Helper()

	select {
	case r := <-out:
		return r
	case <-time.After(time.Second):
		tb.Fatal("Load never returned")

		return result{}
	}
}

func TestNewLoader(t *testing.T) {
	t.Parallel()

	var r resolve

	invalid := []struct {
		fn   func(context.Context, []int) (map[int]int, error)
		name string
		cfg  batch.LoaderConfig
	}{
		{r.fn, "nil clock", batch.LoaderConfig{Wait: window, MaxBatch: 1}},
		{r.fn, "zero wait", batch.LoaderConfig{
			Clock: fake.New(originUTC), MaxBatch: 1,
		}},
		{r.fn, "negative wait", batch.LoaderConfig{
			Clock: fake.New(originUTC), Wait: -window, MaxBatch: 1,
		}},
		{r.fn, "zero batch size", batch.LoaderConfig{
			Clock: fake.New(originUTC), Wait: window,
		}},
		{r.fn, "negative batch size", batch.LoaderConfig{
			Clock: fake.New(originUTC), Wait: window, MaxBatch: -1,
		}},
		{nil, "nil function", batch.LoaderConfig{
			Clock: fake.New(originUTC), Wait: window, MaxBatch: 1,
		}},
	}
	for _, tc := range invalid {
		t.Run("rejects a "+tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := batch.NewLoader(tc.cfg, tc.fn)
			testkit.ErrorIs(t, err, batch.ErrConfig, "an invalid config must be rejected")
		})
	}
}

func TestLoad(t *testing.T) {
	t.Parallel()

	t.Run("one key dispatches after the window", func(t *testing.T) {
		t.Parallel()
		c := fake.New(originUTC)
		var r resolve
		l := mustLoader(t, c, 10, r.fn)

		out := loadAsync(t.Context(), l, 21)

		c.AwaitWaiters(1)
		testkit.Equal(t, r.calls(), 0, "the window must not have elapsed yet")
		c.Advance(window)

		got := await(t, out)
		testkit.NoError(t, got.err, "the load must succeed")
		testkit.Equal(t, got.v, 42, "the value must be the one fn returned")
		testkit.Equal(t, r.calls(), 1, "one key is still one call")
	})

	t.Run("concurrent distinct keys coalesce into one call", func(t *testing.T) {
		t.Parallel()
		// The reason the package exists: four round trips become one.
		c := fake.New(originUTC)
		var r resolve
		l := mustLoader(t, c, 10, r.fn)

		outs := make([]<-chan result, 0, 4)
		for key := range 4 {
			outs = append(outs, loadAsync(t.Context(), l, key+1))
		}

		awaitPending(l, 4)
		c.Advance(window)

		for i, out := range outs {
			got := await(t, out)
			testkit.NoError(t, got.err, "every caller must be served")
			testkit.Equal(t, got.v, (i+1)*2, "each caller must get its own key's value")
		}
		testkit.Equal(t, r.sizes(), []int{4}, "four keys must have travelled as one batch")
	})

	t.Run("concurrent loads of one key share a result", func(t *testing.T) {
		t.Parallel()
		c := fake.New(originUTC)
		var r resolve
		l := mustLoader(t, c, 10, r.fn)

		outs := make([]<-chan result, 0, 3)
		for range 3 {
			outs = append(outs, loadAsync(t.Context(), l, 7))
		}

		awaitPending(l, 3)
		c.Advance(window)

		for _, out := range outs {
			got := await(t, out)
			testkit.NoError(t, got.err, "every caller must be served")
			testkit.Equal(t, got.v, 14, "every caller must get the same value")
		}
		testkit.Equal(t, r.sizes(), []int{1}, "the key must travel once")
	})

	t.Run("a full batch dispatches without waiting", func(t *testing.T) {
		t.Parallel()
		// MaxBatch bounds the call size so one burst does not produce
		// a request larger than the dependency accepts.
		c := fake.New(originUTC)
		var r resolve
		l := mustLoader(t, c, 2, r.fn)

		first := loadAsync(t.Context(), l, 1)
		awaitPending(l, 1)
		second := loadAsync(t.Context(), l, 2)

		// No Advance: reaching MaxBatch is what fires this batch.
		testkit.NoError(t, await(t, first).err, "the first caller must be served")
		testkit.NoError(t, await(t, second).err, "the second caller must be served")
		testkit.Equal(t, r.sizes(), []int{2}, "the batch must fire at its limit")
	})

	t.Run("a later load starts a fresh batch", func(t *testing.T) {
		t.Parallel()
		// Not a cache: a key loaded once is fetched again.
		c := fake.New(originUTC)
		var r resolve
		l := mustLoader(t, c, 10, r.fn)

		for range 2 {
			out := loadAsync(t.Context(), l, 3)
			c.AwaitWaiters(1)
			c.Advance(window)
			testkit.NoError(t, await(t, out).err, "the load must succeed")
		}

		testkit.Equal(t, r.calls(), 2, "the second load must reach fn")
	})
}

func TestLoadFailure(t *testing.T) {
	t.Parallel()

	t.Run("a missing key is a NotFound", func(t *testing.T) {
		t.Parallel()
		c := fake.New(originUTC)
		l := mustLoader(t, c, 10, func(context.Context, []int) (map[int]int, error) {
			return map[int]int{}, nil
		})

		out := loadAsync(t.Context(), l, 9)
		c.AwaitWaiters(1)
		c.Advance(window)

		got := await(t, out)
		testkit.ErrorIs(t, got.err, batch.ErrNotFound, "an unresolved key must be an error")
		testkit.Equal(t, errs.Classify(got.err), errs.NotFound, "absence must classify as NotFound")
		testkit.Equal(t, got.v, 0, "an unresolved key yields the zero value")
	})

	t.Run("a failed batch reaches every waiter", func(t *testing.T) {
		t.Parallel()
		// The error describes the call rather than any one key, so it
		// is not wrapped per key.
		c := fake.New(originUTC)
		l := mustLoader(t, c, 10, failOn(1, errDown))

		first := loadAsync(t.Context(), l, 1)
		second := loadAsync(t.Context(), l, 2)
		awaitPending(l, 2)
		c.Advance(window)

		testkit.ErrorIs(t, await(t, first).err, errDown, "the first caller must see the failure")
		testkit.ErrorIs(t, await(t, second).err, errDown, "the second caller must see the failure")
	})
}

func TestLoadContext(t *testing.T) {
	t.Parallel()

	t.Run("a caller that gives up returns its own error", func(t *testing.T) {
		t.Parallel()
		c := fake.New(originUTC)
		var r resolve
		l := mustLoader(t, c, 10, r.fn)

		ctx, cancel := context.WithCancel(t.Context())
		out := loadAsync(ctx, l, 1)
		awaitPending(l, 1)
		cancel()

		got := await(t, out)
		testkit.ErrorIs(t, got.err, context.Canceled, "the caller's own context must be reported")
	})

	t.Run("the batch continues for whoever remains", func(t *testing.T) {
		t.Parallel()
		c := fake.New(originUTC)
		var r resolve
		l := mustLoader(t, c, 10, r.fn)

		ctx, cancel := context.WithCancel(t.Context())
		leaving := loadAsync(ctx, l, 1)
		staying := loadAsync(t.Context(), l, 2)
		awaitPending(l, 2)

		cancel()
		testkit.ErrorIs(t, await(t, leaving).err, context.Canceled,
			"the departing caller must be released")

		c.Advance(window)
		got := await(t, staying)
		testkit.NoError(t, got.err, "the remaining caller must still be served")
		testkit.Equal(t, got.v, 4, "the batch must have run")
	})

	t.Run("the batch is abandoned once every caller is gone", func(t *testing.T) {
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

		ctx, cancel := context.WithCancel(t.Context())
		out := loadAsync(ctx, l, 1)
		awaitPending(l, 1)
		c.Advance(window)

		cancel()
		testkit.ErrorIs(t, await(t, out).err, context.Canceled, "the caller must be released")

		select {
		case <-gone:
		case <-time.After(time.Second):
			t.Fatal("the batch context was never cancelled")
		}
	})

	t.Run("the batch context carries no caller's values", func(t *testing.T) {
		t.Parallel()
		// A batch serves several callers, so taking values from one of
		// them would attribute its span to whichever caller happened
		// to arrive first.
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
		testkit.NoError(t, await(t, out).err, "the load must succeed")

		testkit.Equal(t, <-seen, nil, "a caller's values must not reach fn")
	})
}

func TestLoadAll(t *testing.T) {
	t.Parallel()

	t.Run("dispatches without waiting for the window", func(t *testing.T) {
		t.Parallel()
		// A caller holding the full key set has nothing to wait for.
		c := fake.New(originUTC)
		var r resolve
		l := mustLoader(t, c, 10, r.fn)

		got, err := l.LoadAll(t.Context(), []int{1, 2, 3})
		testkit.NoError(t, err, "LoadAll must succeed")
		testkit.Equal(t, got, map[int]int{1: 2, 2: 4, 3: 6}, "every key must be resolved")
		testkit.Equal(t, r.sizes(), []int{3}, "one batch must carry them all")
	})

	t.Run("splits beyond the batch limit", func(t *testing.T) {
		t.Parallel()
		c := fake.New(originUTC)
		var r resolve
		l := mustLoader(t, c, 2, r.fn)

		got, err := l.LoadAll(t.Context(), []int{1, 2, 3, 4, 5})
		testkit.NoError(t, err, "LoadAll must succeed")
		testkit.Len(t, got, 5, "every key must be resolved")
		testkit.Equal(t, r.sizes(), []int{2, 2, 1}, "the limit must bound each call")
	})

	t.Run("collapses duplicate keys", func(t *testing.T) {
		t.Parallel()
		c := fake.New(originUTC)
		var r resolve
		l := mustLoader(t, c, 10, r.fn)

		got, err := l.LoadAll(t.Context(), []int{1, 1, 2, 1})
		testkit.NoError(t, err, "LoadAll must succeed")
		testkit.Equal(t, got, map[int]int{1: 2, 2: 4}, "each key must resolve once")
		testkit.Equal(t, r.sizes(), []int{2}, "a key must not travel twice")
	})

	t.Run("no keys is no call", func(t *testing.T) {
		t.Parallel()
		c := fake.New(originUTC)
		var r resolve
		l := mustLoader(t, c, 10, r.fn)

		got, err := l.LoadAll(t.Context(), nil)
		testkit.NoError(t, err, "an empty request is not an error")
		testkit.Len(t, got, 0, "there is nothing to return")
		testkit.Equal(t, r.calls(), 0, "there is nothing to call")
	})

	t.Run("a missing key is simply absent", func(t *testing.T) {
		t.Parallel()
		// LoadAll returns a map, so absence is representable without
		// an error; Load has no such option.
		c := fake.New(originUTC)
		l := mustLoader(t, c, 10, func(_ context.Context, keys []int) (map[int]int, error) {
			return map[int]int{keys[0]: 1}, nil
		})

		got, err := l.LoadAll(t.Context(), []int{1, 2})
		testkit.NoError(t, err, "an unresolved key is not a failed call")
		testkit.Len(t, got, 1, "only the resolved key must be returned")
	})

	t.Run("a failed batch fails the call", func(t *testing.T) {
		t.Parallel()
		c := fake.New(originUTC)
		l := mustLoader(t, c, 1, failOn(2, errDown))

		got, err := l.LoadAll(t.Context(), []int{1, 2})
		testkit.ErrorIs(t, err, errDown, "one failed chunk fails the call")
		testkit.Equal(t, got, map[int]int(nil), "a partial result would be a trap")
	})
}

func TestLoaderClose(t *testing.T) {
	t.Parallel()

	newClosed := func(tb testing.TB) *batch.Loader[int, int] {
		tb.Helper()
		var r resolve
		l := mustLoader(tb, fake.New(originUTC), 10, r.fn)
		testkit.NoError(tb, l.Close(), "Close must succeed")

		return l
	}

	t.Run("Load after Close is refused", func(t *testing.T) {
		t.Parallel()
		_, err := newClosed(t).Load(t.Context(), 1)
		testkit.ErrorIs(t, err, batch.ErrClosed, "a closed loader must refuse work")
	})

	t.Run("LoadAll after Close is refused", func(t *testing.T) {
		t.Parallel()
		_, err := newClosed(t).LoadAll(t.Context(), []int{1})
		testkit.ErrorIs(t, err, batch.ErrClosed, "a closed loader must refuse work")
	})

	t.Run("Close is idempotent", func(t *testing.T) {
		t.Parallel()
		// Close belongs in a defer, and a defer that fails on the
		// second call is worse than one that does nothing.
		testkit.NoError(t, newClosed(t).Close(), "closing twice must be safe")
	})

	t.Run("an accumulating batch still runs", func(t *testing.T) {
		t.Parallel()
		c := fake.New(originUTC)
		var r resolve
		l := mustLoader(t, c, 10, r.fn)

		out := loadAsync(t.Context(), l, 1)
		c.AwaitWaiters(1)
		testkit.NoError(t, l.Close(), "Close must succeed")
		c.Advance(window)

		got := await(t, out)
		testkit.NoError(t, got.err, "a caller already waiting must still be served")
		testkit.Equal(t, got.v, 2, "the in-flight batch must run to completion")
	})
}

func TestLoaderConcurrent(t *testing.T) {
	t.Parallel()

	// Every caller must get its own key's answer no matter how the
	// batches happen to divide.
	const (
		workers = 64
		keys    = 8
	)

	c := fake.New(originUTC)
	var r resolve
	l := mustLoader(t, c, 4, r.fn)

	// Batches fire on MaxBatch mostly, but a partial one at the end
	// needs its window, so keep virtual time moving.
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

	var wg sync.WaitGroup
	for i := range workers {
		wg.Go(func() {
			key := i%keys + 1
			v, err := l.Load(t.Context(), key)
			testkit.NoError(t, err, "every caller must be served")
			testkit.Equal(t, v, key*2, "every caller must get its own key's value")
		})
	}
	wg.Wait()

	close(stop)
	<-ticking

	testkit.Equal(t, l.Pending(), 0, "every caller must have been released")
	testkit.NoError(t, l.Close(), "Close must succeed")
}
