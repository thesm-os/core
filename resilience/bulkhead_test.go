// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package resilience_test

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/resilience"
)

// awaitQueued spins until a caller is waiting for a permit. Mirrors
// fake.Clock.AwaitWaiters: a deterministic hand-off, where a sleep
// would be a guess.
//
// Bounded rather than spinning forever, so a bulkhead that loses track
// of its queue fails the test instead of hanging it.
func awaitQueued(tb testing.TB, b *resilience.Bulkhead) {
	tb.Helper()

	deadline := time.Now().Add(time.Second)
	for b.Queued() == 0 {
		if time.Now().After(deadline) {
			tb.Fatal("no caller ever queued")
		}
		runtime.Gosched()
	}
}

func mustBulkhead(tb testing.TB, cfg resilience.BulkheadConfig) *resilience.Bulkhead {
	tb.Helper()

	b, err := resilience.NewBulkhead(cfg)
	testkit.NoError(tb, err, "NewBulkhead must accept a valid config")

	return b
}

// acquireAll takes every permit and returns the releases.
func acquireAll(tb testing.TB, b *resilience.Bulkhead, n int) []func() {
	tb.Helper()

	releases := make([]func(), 0, n)
	for range n {
		release, err := b.Acquire(bounded(tb))
		testkit.NoError(tb, err, "Acquire must succeed below the limit")
		releases = append(releases, release)
	}

	return releases
}

func TestNewBulkhead(t *testing.T) {
	t.Parallel()

	invalid := []struct {
		name string
		cfg  resilience.BulkheadConfig
	}{
		{"nil clock", resilience.BulkheadConfig{Limit: 1}},
		{"zero limit", resilience.BulkheadConfig{Clock: fake.New(originUTC)}},
		{"negative limit", resilience.BulkheadConfig{Clock: fake.New(originUTC), Limit: -1}},
		{"negative queue", resilience.BulkheadConfig{
			Clock: fake.New(originUTC), Limit: 1, Queue: -1,
		}},
		{"negative wait", resilience.BulkheadConfig{
			Clock: fake.New(originUTC), Limit: 1, Wait: -time.Second,
		}},
	}
	for _, tc := range invalid {
		t.Run("rejects a "+tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := resilience.NewBulkhead(tc.cfg)
			testkit.ErrorIs(t, err, resilience.ErrConfig, "an invalid config must be rejected")
		})
	}

	t.Run("accepts a limit alone", func(t *testing.T) {
		t.Parallel()
		// Queue and Wait have meaningful zero values: no queue, and
		// no bound beyond the caller's context.
		_, err := resilience.NewBulkhead(resilience.BulkheadConfig{
			Clock: fake.New(originUTC), Limit: 1,
		})
		testkit.NoError(t, err, "Limit alone must be a complete config")
	})
}

func TestBulkheadLimit(t *testing.T) {
	t.Parallel()

	t.Run("admits up to the limit", func(t *testing.T) {
		t.Parallel()
		b := mustBulkhead(t, resilience.BulkheadConfig{Clock: fake.New(originUTC), Limit: 3})

		releases := acquireAll(t, b, 3)
		testkit.Equal(t, b.InFlight(), 3, "every permit must be held")

		for _, release := range releases {
			release()
		}
		testkit.Equal(t, b.InFlight(), 0, "releasing must return every permit")
	})

	t.Run("rejects immediately with no queue", func(t *testing.T) {
		t.Parallel()
		b := mustBulkhead(t, resilience.BulkheadConfig{Clock: fake.New(originUTC), Limit: 1})
		defer acquireAll(t, b, 1)[0]()

		_, err := b.Acquire(bounded(t))
		testkit.ErrorIs(t, err, resilience.ErrFull,
			"a call arriving at the limit with no queue must be rejected at once")
	})

	t.Run("a released permit admits the next caller", func(t *testing.T) {
		t.Parallel()
		b := mustBulkhead(t, resilience.BulkheadConfig{Clock: fake.New(originUTC), Limit: 1})

		release := acquireAll(t, b, 1)[0]
		release()

		next, err := b.Acquire(bounded(t))
		testkit.NoError(t, err, "a freed permit must admit the next caller")
		next()
	})
}

func TestBulkheadQueue(t *testing.T) {
	t.Parallel()

	t.Run("a queued caller waits for a permit", func(t *testing.T) {
		t.Parallel()
		c := fake.New(originUTC)
		b := mustBulkhead(t, resilience.BulkheadConfig{Clock: c, Limit: 1, Queue: 1})

		held := acquireAll(t, b, 1)[0]

		got := make(chan error, 1)
		go func() {
			release, err := b.Acquire(bounded(t))
			if err == nil {
				release()
			}
			got <- err
		}()

		// The waiter must be queued, not rejected.
		awaitQueued(t, b)

		held()

		select {
		case err := <-got:
			testkit.NoError(t, err, "the queued caller must receive the freed permit")
		case <-time.After(time.Second):
			t.Fatal("the queued caller never acquired")
		}
	})

	t.Run("rejects when the limit and the queue are both full", func(t *testing.T) {
		t.Parallel()
		c := fake.New(originUTC)
		b := mustBulkhead(t, resilience.BulkheadConfig{Clock: c, Limit: 1, Queue: 1})

		defer acquireAll(t, b, 1)[0]()

		blocked, cancel := context.WithCancel(t.Context())
		defer cancel()
		go func() {
			release, err := b.Acquire(blocked)
			if err == nil {
				release()
			}
		}()
		awaitQueued(t, b)

		_, err := b.Acquire(bounded(t))
		testkit.ErrorIs(t, err, resilience.ErrFull,
			"a caller arriving at a full limit and full queue must be rejected")
	})
}

func TestBulkheadWait(t *testing.T) {
	t.Parallel()

	t.Run("a queued caller times out", func(t *testing.T) {
		t.Parallel()
		c := fake.New(originUTC)
		b := mustBulkhead(t, resilience.BulkheadConfig{
			Clock: c, Limit: 1, Queue: 1, Wait: 5 * time.Second,
		})

		defer acquireAll(t, b, 1)[0]()

		got := make(chan error, 1)
		go func() {
			_, err := b.Acquire(bounded(t))
			got <- err
		}()

		c.AwaitWaiters(1)
		c.Advance(6 * time.Second)

		select {
		case err := <-got:
			testkit.ErrorIs(t, err, resilience.ErrWaitTimeout,
				"a caller waiting its full allowance must time out")
		case <-time.After(time.Second):
			t.Fatal("the queued caller never timed out")
		}
	})

	t.Run("a permit freed before the deadline wins", func(t *testing.T) {
		t.Parallel()
		c := fake.New(originUTC)
		b := mustBulkhead(t, resilience.BulkheadConfig{
			Clock: c, Limit: 1, Queue: 1, Wait: 5 * time.Second,
		})

		held := acquireAll(t, b, 1)[0]

		got := make(chan error, 1)
		go func() {
			release, err := b.Acquire(bounded(t))
			if err == nil {
				release()
			}
			got <- err
		}()

		c.AwaitWaiters(1)
		held()

		select {
		case err := <-got:
			testkit.NoError(t, err, "a permit freed before the deadline must be taken")
		case <-time.After(time.Second):
			t.Fatal("the queued caller never acquired")
		}
	})

	t.Run("the context still wins inside the allowance", func(t *testing.T) {
		t.Parallel()
		// Wait bounds how long a caller is willing to queue; it does
		// not extend how long its context is willing to run.
		c := fake.New(originUTC)
		b := mustBulkhead(t, resilience.BulkheadConfig{
			Clock: c, Limit: 1, Queue: 1, Wait: time.Hour,
		})

		defer acquireAll(t, b, 1)[0]()

		ctx, cancel := context.WithCancel(t.Context())
		got := make(chan error, 1)
		go func() {
			_, err := b.Acquire(ctx)
			got <- err
		}()

		c.AwaitWaiters(1)
		cancel()

		select {
		case err := <-got:
			testkit.ErrorIs(t, err, context.Canceled,
				"a cancelled caller must not wait out its allowance")
		case <-time.After(time.Second):
			t.Fatal("the queued caller never returned")
		}
	})

	t.Run("zero wait is bounded only by the context", func(t *testing.T) {
		t.Parallel()
		c := fake.New(originUTC)
		b := mustBulkhead(t, resilience.BulkheadConfig{Clock: c, Limit: 1, Queue: 1})

		defer acquireAll(t, b, 1)[0]()

		ctx, cancel := context.WithCancel(t.Context())
		got := make(chan error, 1)
		go func() {
			_, err := b.Acquire(ctx)
			got <- err
		}()

		awaitQueued(t, b)
		cancel()

		select {
		case err := <-got:
			testkit.ErrorIs(t, err, context.Canceled,
				"with no Wait the caller waits until its context ends")
		case <-time.After(time.Second):
			t.Fatal("the queued caller never returned")
		}
	})
}

func TestBulkheadCancellationIsNotRejection(t *testing.T) {
	t.Parallel()

	// A client-side cancellation storm must not be indistinguishable
	// from a saturated dependency in any metric derived from these
	// errors.
	c := fake.New(originUTC)
	b := mustBulkhead(t, resilience.BulkheadConfig{Clock: c, Limit: 1, Queue: 1})

	defer acquireAll(t, b, 1)[0]()

	ctx, cancel := context.WithCancel(t.Context())
	got := make(chan error, 1)
	go func() {
		_, err := b.Acquire(ctx)
		got <- err
	}()

	awaitQueued(t, b)
	cancel()

	// Bounded like every other receive from an Acquire goroutine: a
	// mutant that makes Acquire ignore its context leaves got empty
	// forever, and a bare receive would hang the test against its
	// own completion.
	var err error
	select {
	case err = <-got:
	case <-time.After(time.Second):
		t.Fatal("Acquire never returned after cancellation")
	}
	testkit.ErrorIs(t, err, context.Canceled, "cancellation must surface as the context error")
	testkit.ErrorIsNot(t, err, resilience.ErrFull, "cancellation is not a rejection")
	testkit.ErrorIsNot(t, err, resilience.ErrWaitTimeout, "cancellation is not a timeout")
}

func TestBulkheadRelease(t *testing.T) {
	t.Parallel()

	t.Run("releasing twice does not raise the effective limit", func(t *testing.T) {
		t.Parallel()
		// A double release would hand back a permit that was never
		// held, silently letting an extra caller through for every
		// bug of this kind.
		b := mustBulkhead(t, resilience.BulkheadConfig{Clock: fake.New(originUTC), Limit: 1})

		release := acquireAll(t, b, 1)[0]
		release()
		release()

		testkit.Equal(t, b.InFlight(), 0, "the permit must be returned exactly once")

		first, err := b.Acquire(bounded(t))
		testkit.NoError(t, err, "the freed permit must be available")
		defer first()

		_, err = b.Acquire(bounded(t))
		testkit.ErrorIs(t, err, resilience.ErrFull,
			"a double release must not admit a second holder")
	})
}

func TestBulkheadConcurrent(t *testing.T) {
	t.Parallel()

	// The limit must hold no matter how many callers contend.
	const (
		limit   = 4
		workers = 64
	)

	c := fake.New(originUTC)
	b := mustBulkhead(t, resilience.BulkheadConfig{Clock: c, Limit: limit, Queue: workers})

	var (
		mu      sync.Mutex
		peak    int
		current int
		wg      sync.WaitGroup
	)
	for range workers {
		wg.Go(func() {
			release, err := b.Acquire(bounded(t))
			if err != nil {
				return
			}

			defer release()

			mu.Lock()
			current++
			peak = max(peak, current)
			mu.Unlock()

			// Hold the permit across a scheduling point, so that
			// concurrent holders actually overlap and peak means
			// something.
			runtime.Gosched()

			mu.Lock()
			current--
			mu.Unlock()
		})
	}
	wg.Wait()

	testkit.True(t, peak <= limit, "concurrent holders must never exceed the limit")
	testkit.Equal(t, b.InFlight(), 0, "every permit must be returned")
}

func BenchmarkBulkhead(b *testing.B) {
	b.Run("Admitted", func(b *testing.B) {
		bh := mustBulkhead(b, resilience.BulkheadConfig{Clock: fake.New(originUTC), Limit: 1})

		b.ReportAllocs()
		for b.Loop() {
			release, err := bh.Acquire(b.Context())
			if err == nil {
				release()
			}
		}
	})

	b.Run("Rejected", func(b *testing.B) {
		bh := mustBulkhead(b, resilience.BulkheadConfig{Clock: fake.New(originUTC), Limit: 1})
		defer acquireAll(b, bh, 1)[0]()

		var sink error

		b.ReportAllocs()
		for b.Loop() {
			_, sink = bh.Acquire(b.Context())
		}

		runtime.KeepAlive(sink)
	})
}
