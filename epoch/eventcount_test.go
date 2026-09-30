// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package epoch_test

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/epoch"
)

// errEngine is the failure that the Fail cases record.
var errEngine = testkit.TestError("engine failed")

// Parameters of TestEventCount and BenchmarkEventCount.
const (
	// waitTimeout bounds a Wait that a case expects to block, in the fake
	// time of a synctest bubble.
	waitTimeout = time.Minute

	// concurrentWaiters is the number of goroutines that wait while
	// another goroutine advances the count.
	concurrentWaiters = 64

	// concurrentTop is the count that the concurrent case advances to.
	concurrentTop = 1000

	// concurrentTimeout bounds the waits of the concurrent case, which
	// return in milliseconds.
	concurrentTimeout = 10 * time.Second

	// benchRuns is the number of calls over which a benchmark averages
	// the allocations it checks.
	benchRuns = 100
)

func TestEventCount(t *testing.T) {
	t.Parallel()

	t.Run("Current returns Zero for a zero EventCount", func(t *testing.T) {
		t.Parallel()
		var c epoch.EventCount
		testkit.Equal(t, c.Current(), epoch.Zero, "a zero EventCount must count Zero")
	})

	t.Run("Current returns the count that Advance raised", func(t *testing.T) {
		t.Parallel()
		var c epoch.EventCount
		c.Advance(5)
		testkit.Equal(t, c.Current(), epoch.Epoch(5), "Current must return the raised count")
	})

	t.Run("Advance does nothing for a value at or below the count", func(t *testing.T) {
		t.Parallel()
		var c epoch.EventCount
		c.Advance(5)
		c.Advance(3)
		c.Advance(5)
		testkit.Equal(t, c.Current(), epoch.Epoch(5), "a lower or equal value must not change the count")
	})

	t.Run("Advance raises the count after Fail", func(t *testing.T) {
		t.Parallel()
		var c epoch.EventCount
		c.Fail(errEngine)
		c.Advance(4)
		testkit.Equal(t, c.Current(), epoch.Epoch(4), "Advance must raise a failed count")
		testkit.NoError(t, c.Wait(endedContext(t), 4), "a met target must return nil after Fail")
	})

	t.Run("Advance wakes a waiter whose target it meets", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			var c epoch.EventCount
			done := waitAsync(t, &c, 3)
			synctest.Wait()
			c.Advance(3)
			testkit.NoError(t, <-done, "the waiter must return nil once the count meets its target")
		})
	})

	t.Run("Advance leaves a waiter blocked below its target", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			var c epoch.EventCount
			done := waitAsync(t, &c, 3)
			synctest.Wait()
			c.Advance(2)
			synctest.Wait()
			select {
			case err := <-done:
				t.Fatalf("Wait(3) returned %v at a count of 2", err)
			default:
			}
			c.Advance(3)
			testkit.NoError(t, <-done, "the waiter must return nil once the count meets its target")
		})
	})

	t.Run("Wait returns nil for a target that the count has met", func(t *testing.T) {
		t.Parallel()
		var c epoch.EventCount
		c.Advance(3)
		for _, v := range []epoch.Epoch{epoch.Zero, 1, 3} {
			testkit.NoError(t, c.Wait(endedContext(t), v), "a met target must return nil")
		}
	})

	t.Run("Wait returns nil for a met target after the context ended", func(t *testing.T) {
		t.Parallel()
		var c epoch.EventCount
		c.Advance(3)
		testkit.NoError(t, c.Wait(endedContext(t), 3), "a met target must return nil for an ended context")
	})

	t.Run("Wait returns the context's error for an ended context", func(t *testing.T) {
		t.Parallel()
		var c epoch.EventCount
		testkit.ErrorIs(t, c.Wait(endedContext(t), 1), context.Canceled,
			"an unmet target must return the context's error")
	})

	t.Run("Wait returns the context's error when the context ends first", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			var c epoch.EventCount
			ctx, cancel := context.WithTimeout(t.Context(), waitTimeout)
			defer cancel()
			testkit.ErrorIs(t, c.Wait(ctx, 1), context.DeadlineExceeded, "the deadline must end the wait")
		})
	})

	t.Run("Wait returns the error of Fail when the count fails first", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			var c epoch.EventCount
			done := waitAsync(t, &c, 1)
			synctest.Wait()
			c.Fail(errEngine)
			testkit.ErrorIs(t, <-done, errEngine, "a blocked waiter must return the error of Fail")
		})
	})

	t.Run("Wait returns the error of Fail for a later call", func(t *testing.T) {
		t.Parallel()
		var c epoch.EventCount
		c.Fail(errEngine)
		testkit.ErrorIs(t, c.Wait(endedContext(t), 1), errEngine, "a later Wait must return the error of Fail")
	})

	t.Run("Wait returns nil for a target that the count met before Fail", func(t *testing.T) {
		t.Parallel()
		var c epoch.EventCount
		c.Advance(2)
		c.Fail(errEngine)
		testkit.NoError(t, c.Wait(endedContext(t), 2), "a target met before Fail must return nil")
	})

	t.Run("Wait returns only once the count meets its target under concurrent Advance", func(t *testing.T) {
		t.Parallel()
		var c epoch.EventCount
		type result struct {
			err    error
			at     epoch.Epoch
			target epoch.Epoch
		}
		ctx, cancel := context.WithTimeout(t.Context(), concurrentTimeout)
		defer cancel()
		results := make([]result, concurrentWaiters)
		var wg sync.WaitGroup
		for i := range results {
			target := epoch.Epoch(1 + i*concurrentTop/concurrentWaiters)
			wg.Go(func() {
				err := c.Wait(ctx, target)
				results[i] = result{err: err, at: c.Current(), target: target}
			})
		}
		for v := range epoch.Epoch(concurrentTop + 1) {
			c.Advance(v)
		}
		wg.Wait()
		for _, r := range results {
			testkit.NoError(t, r.err, "every waiter must return nil")
			testkit.True(t, r.at >= r.target, "a waiter must return only once the count meets its target")
		}
	})

	t.Run("Fail does nothing for a nil error", func(t *testing.T) {
		t.Parallel()
		var c epoch.EventCount
		c.Fail(nil)
		c.Fail(errEngine)
		testkit.ErrorIs(t, c.Wait(endedContext(t), 1), errEngine, "Fail(nil) must not record a failure")
	})

	t.Run("Fail keeps the error of the first call", func(t *testing.T) {
		t.Parallel()
		var c epoch.EventCount
		first := testkit.TestError("first failure")
		c.Fail(first)
		c.Fail(errEngine)
		testkit.ErrorIs(t, c.Wait(endedContext(t), 1), first, "only the first Fail may record its error")
	})
}

// waitAsync starts a Wait for v on a goroutine of its own and returns the
// channel that receives the result.
func waitAsync(t *testing.T, c *epoch.EventCount, v epoch.Epoch) <-chan error {
	t.Helper()

	done := make(chan error, 1)
	go func() { done <- c.Wait(t.Context(), v) }()

	return done
}

// endedContext returns a context that has already ended, so that a Wait
// that would block returns at once.
func endedContext(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	return ctx
}

// BenchmarkEventCount reports the cost of each method, and fails when a
// method allocates more than the allocation contract of
// [epoch.EventCount] allows.
func BenchmarkEventCount(b *testing.B) {
	b.Run("Current", func(b *testing.B) {
		var c epoch.EventCount
		c.Advance(1)
		b.ReportAllocs()
		for b.Loop() {
			_ = c.Current()
		}
	})

	b.Run("Wait for a met target", func(b *testing.B) {
		var c epoch.EventCount
		c.Advance(1)
		ctx := b.Context()
		wait := func() { _ = c.Wait(ctx, 1) }
		if allocs := testing.AllocsPerRun(benchRuns, wait); allocs != 0 {
			b.Fatalf("Wait for a met target allocates %v times per call, want 0", allocs)
		}
		b.ReportAllocs()
		for b.Loop() {
			wait()
		}
	})

	// Two goroutines take turns, so that every Wait blocks and every
	// Advance finds a waiter. Only the two Advances of a round allocate.
	b.Run("Advance and Wait in turns", func(b *testing.B) {
		var ping, pong epoch.EventCount
		ctx, cancel := context.WithCancel(b.Context())
		_ = ctx.Done()
		peer := make(chan struct{})
		go func() {
			defer close(peer)
			for v := epoch.Epoch(1); ping.Wait(ctx, v) == nil; v++ {
				pong.Advance(v)
			}
		}()
		var v epoch.Epoch
		round := func() {
			v++
			ping.Advance(v)
			_ = pong.Wait(ctx, v)
		}
		if allocs := testing.AllocsPerRun(benchRuns, round); allocs > 2 {
			b.Fatalf("a round of two Advances and two Waits allocates %v times, want at most 2", allocs)
		}
		b.ReportAllocs()
		for b.Loop() {
			round()
		}
		cancel()
		<-peer
	})
}
