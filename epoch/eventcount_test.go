// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package epoch_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.thesmos.sh/core/epoch"
)

// errEngine is the failure that the Fail cases record.
var errEngine = errors.New("engine failed")

// Parameters of TestEventCount.
const (
	// waitTimeout bounds a Wait that a case expects to block, in the fake
	// time of a synctest bubble.
	waitTimeout = time.Minute

	// concurrentWaiters is the number of clients that wait while another
	// client advances the count.
	concurrentWaiters = 16

	// concurrentTop is the count that the concurrent case advances to.
	concurrentTop = 256

	// concurrentTimeout bounds the waits of the concurrent case, which
	// return in milliseconds.
	concurrentTimeout = 10 * time.Second

	// modelTop is the largest count and target that the steps of the model
	// case draw, small enough that a target is met in many steps.
	modelTop = 8
)

// countState is the state of the sequential model of an EventCount: the
// count, and whether Fail recorded a failure.
type countState struct {
	count  epoch.Epoch
	failed bool
}

func TestEventCount(t *testing.T) {
	t.Parallel()

	t.Run("Current returns Zero for a zero EventCount", func(t *testing.T) {
		t.Parallel()
		var c epoch.EventCount
		assert.Equal(t, c.Current(), epoch.Zero, "a zero EventCount must count Zero")
	})

	t.Run("Current returns the count that Advance raised", func(t *testing.T) {
		t.Parallel()
		var c epoch.EventCount
		c.Advance(5)
		assert.Equal(t, c.Current(), epoch.Epoch(5), "Current must return the raised count")
	})

	t.Run("Advance does nothing for a value at or below the count", func(t *testing.T) {
		t.Parallel()
		var c epoch.EventCount
		c.Advance(5)
		assert.Pure(t, c.Current, func() { c.Advance(3) }, "a lower value must not change the count")
		assert.Pure(t, c.Current, func() { c.Advance(5) }, "an equal value must not change the count")
	})

	t.Run("Advance raises the count after Fail", func(t *testing.T) {
		t.Parallel()
		var c epoch.EventCount
		// A Wait with an ended context takes the wake-up channel
		// without blocking, as a waiter that Fail releases does.
		assert.HonoursCancellation(t, func(ctx context.Context) error { return c.Wait(ctx, 1) },
			"an unmet target must return the context's error")
		c.Fail(errEngine)
		c.Advance(4)
		assert.Equal(t, c.Current(), epoch.Epoch(4), "Advance must raise a failed count")
		assert.NoError(t, c.Wait(endedContext(t), 4), "a met target must return nil after Fail")
	})

	t.Run("Advance wakes a waiter whose target it meets", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			var c epoch.EventCount
			done := waitAsync(t, &c, 3)
			synctest.Wait()
			c.Advance(3)
			assert.NoError(t, <-done, "the waiter must return nil once the count meets its target")
		})
	})

	t.Run("Advance wakes every blocked waiter", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			var c epoch.EventCount
			first := waitAsync(t, &c, 3)
			second := waitAsync(t, &c, 3)
			synctest.Wait()
			c.Advance(3)
			assert.NoError(t, <-first, "the first waiter must return nil once the count meets its target")
			assert.NoError(t, <-second, "the second waiter must return nil once the count meets its target")
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
			assert.Empty(t, done, "Wait(3) must block at a count of 2")
			c.Advance(3)
			assert.NoError(t, <-done, "the waiter must return nil once the count meets its target")
		})
	})

	t.Run("Wait returns nil for a target that the count has met", func(t *testing.T) {
		t.Parallel()
		var c epoch.EventCount
		c.Advance(3)
		assert.Total(t, func(v epoch.Epoch) error {
			return c.Wait(endedContext(t), v)
		}, []epoch.Epoch{epoch.Zero, 1, 3}, "a met target must return nil")
	})

	t.Run("Wait returns nil for a met target after the context ended", func(t *testing.T) {
		t.Parallel()
		var c epoch.EventCount
		c.Advance(3)
		assert.NoError(t, c.Wait(endedContext(t), 3), "a met target must return nil for an ended context")
	})

	t.Run("Wait returns the context's error for a cancelled context", func(t *testing.T) {
		t.Parallel()
		var c epoch.EventCount
		assert.HonoursCancellation(t, func(ctx context.Context) error { return c.Wait(ctx, 1) },
			"an unmet target must return the context's error")
	})

	t.Run("Wait returns the context's error for a passed deadline", func(t *testing.T) {
		t.Parallel()
		var c epoch.EventCount
		assert.HonoursDeadline(t, func(ctx context.Context) error { return c.Wait(ctx, 1) },
			"an unmet target must return the deadline's error")
	})

	t.Run("Wait returns the context's error when the context ends first", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			var c epoch.EventCount
			ctx, cancel := context.WithTimeout(t.Context(), waitTimeout)
			defer cancel()
			assert.ErrorIs(t, c.Wait(ctx, 1), context.DeadlineExceeded, "the deadline must end the wait")
		})
	})

	t.Run("Wait returns the error of Fail when the count fails first", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			var c epoch.EventCount
			done := waitAsync(t, &c, 1)
			synctest.Wait()
			c.Fail(errEngine)
			assert.ErrorIs(t, <-done, errEngine, "a blocked waiter must return the error of Fail")
		})
	})

	t.Run("Wait returns the error of Fail for a later call", func(t *testing.T) {
		t.Parallel()
		var c epoch.EventCount
		c.Fail(errEngine)
		assert.ErrorIs(t, c.Wait(endedContext(t), 1), errEngine, "a later Wait must return the error of Fail")
	})

	t.Run("Wait returns nil for a target that the count met before Fail", func(t *testing.T) {
		t.Parallel()
		var c epoch.EventCount
		c.Advance(2)
		c.Fail(errEngine)
		assert.NoError(t, c.Wait(endedContext(t), 2), "a target met before Fail must return nil")
	})

	t.Run("Wait returns only once the count meets its target under concurrent Advance", func(t *testing.T) {
		t.Parallel()
		var c epoch.EventCount
		h := history.New()
		ctx, cancel := context.WithTimeout(t.Context(), concurrentTimeout)
		defer cancel()

		// The clients outlast the context, so a Wait that never returns
		// fails with the context's error instead of staying pending.
		outcomes := history.Concurrently(concurrentWaiters+1, 2*concurrentTimeout, func(client int) (any, error) {
			if client == 0 {
				for v := range epoch.Epoch(concurrentTop + 1) {
					call := h.Invoke(client, "advance", []any{v})
					c.Advance(v)
					call.OK(nil)
				}

				return epoch.Epoch(concurrentTop), nil
			}

			target := epoch.Epoch(1 + (client-1)*concurrentTop/concurrentWaiters)
			call := h.Invoke(client, "wait", []any{target})
			call.OK(c.Wait(ctx, target))

			return target, nil
		})
		for _, o := range outcomes {
			assert.True(t, o.Finished, "every client must finish its calls")
		}

		history.Linearizable(t, h, history.Spec[epoch.Epoch]{
			Initial: func() epoch.Epoch { return epoch.Zero },
			Next: func(s epoch.Epoch, op history.Operation) []epoch.Epoch {
				v := op.Args[0].(epoch.Epoch)
				if op.Name == "advance" {
					return []epoch.Epoch{max(s, v)}
				}

				if s < v || !op.Returned(nil) {
					return nil
				}

				return []epoch.Epoch{s}
			},
		}, "every Wait must return nil at a point where the count meets its target")
	})

	t.Run("Fail does nothing for a nil error", func(t *testing.T) {
		t.Parallel()
		var c epoch.EventCount
		// A Wait with an ended context takes the wake-up channel
		// without blocking, so Fail(nil) finds a channel to keep.
		assert.HonoursCancellation(t, func(ctx context.Context) error { return c.Wait(ctx, 1) },
			"an unmet target must return the context's error")
		c.Fail(nil)
		assert.HonoursCancellation(t, func(ctx context.Context) error { return c.Wait(ctx, 1) },
			"Fail(nil) must leave the count unfailed")
		c.Advance(1)
		assert.NoError(t, c.Wait(endedContext(t), 1), "Advance must wake the waiters after Fail(nil)")
		c.Fail(errEngine)
		assert.ErrorIs(t, c.Wait(endedContext(t), 2), errEngine, "Fail(nil) must not record a failure")
	})

	t.Run("Fail keeps the error of the first call", func(t *testing.T) {
		t.Parallel()
		var c epoch.EventCount
		first := errors.New("first failure")
		c.Fail(first)
		c.Fail(errEngine)
		assert.ErrorIs(t, c.Wait(endedContext(t), 1), first, "only the first Fail may record its error")
	})

	t.Run("behaves as a sequential model under any sequence of calls", func(t *testing.T) {
		t.Parallel()

		prop.ForAll(t, "Advance, Fail, Current and Wait must behave as one call at a time", func(c *prop.Case) {
			var count epoch.EventCount

			// A Wait with an ended context returns at once, so every step
			// completes and a concurrent section ends.
			ended, cancel := context.WithCancel(c.Context())
			cancel()

			target := func(c *prop.Case, _ countState) any {
				return c.Draw(prop.Integer[epoch.Epoch](0, modelTop), "value")
			}

			stateful.Steps(c, stateful.Machine[countState]{
				Spec: history.Spec[countState]{
					Initial: func() countState { return countState{} },
					Next:    stepCount,
				},
				Actions: []stateful.Action[countState]{
					{
						Name: "current",
						Run: func(c *prop.Case, client int, _ any) {
							call := c.History().Invoke(client, "current", nil)
							call.OK(count.Current())
						},
					},
					{
						Name:  "advance",
						Input: target,
						Run: func(c *prop.Case, client int, v any) {
							call := c.History().Invoke(client, "advance", []any{v})
							count.Advance(v.(epoch.Epoch))
							call.OK(nil)
						},
					},
					{
						Name:  "wait",
						Input: target,
						Run: func(c *prop.Case, client int, v any) {
							call := c.History().Invoke(client, "wait", []any{v})
							call.OK(count.Wait(ended, v.(epoch.Epoch)))
						},
					},
					{
						Name: "fail",
						Run: func(c *prop.Case, client int, _ any) {
							call := c.History().Invoke(client, "fail", nil)
							count.Fail(errEngine)
							call.OK(nil)
						},
					},
				},
			}, stateful.Clients(2))
		})
	})
}

// TestEventCountAllocs checks the allocation contract of EventCount.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
func TestEventCountAllocs(t *testing.T) {
	t.Run("Current", func(t *testing.T) {
		var c epoch.EventCount
		c.Advance(1)

		var got epoch.Epoch
		expect.MaxAllocs(t, func() { got = c.Current() }, 0, "Current must not allocate")
		assert.Equal(t, got, epoch.Epoch(1), "the test must measure the raised count")
	})

	t.Run("Wait for a met target", func(t *testing.T) {
		var c epoch.EventCount
		c.Advance(1)
		ctx := t.Context()

		var err error
		expect.MaxAllocs(t, func() { err = c.Wait(ctx, 1) }, 0, "Wait for a met target must not allocate")
		assert.NoError(t, err, "the test must measure a met target")
	})

	t.Run("Advance when a waiter took the channel", func(t *testing.T) {
		ctx := endedContext(t)
		expect.MaxAllocsWithSetup(t, func() *epoch.EventCount {
			c := &epoch.EventCount{}
			_ = c.Wait(ctx, 1)

			return c
		}, func(c *epoch.EventCount) { c.Advance(1) }, 1,
			"Advance must allocate one channel in place of the one that a waiter took")
	})

	t.Run("Advance when no waiter took the channel", func(t *testing.T) {
		ctx := endedContext(t)
		expect.MaxAllocsWithSetup(t, func() *epoch.EventCount {
			c := &epoch.EventCount{}
			_ = c.Wait(ctx, 1)
			c.Advance(1)

			return c
		}, func(c *epoch.EventCount) { c.Advance(2) }, 0,
			"Advance must not allocate when no waiter took the current channel")
	})
}

// stepCount is the sequential spec of an EventCount. Advance raises the
// count, Fail marks the count failed, and Current returns the count. A Wait
// with an ended context returns nil for a met target, the error of Fail for
// a failed count, and context.Canceled for any other.
func stepCount(s countState, op history.Operation) []countState {
	switch op.Name {
	case "advance":
		return []countState{{count: max(s.count, op.Args[0].(epoch.Epoch)), failed: s.failed}}
	case "fail":
		return []countState{{count: s.count, failed: true}}
	case "current":
		if !op.Returned(s.count) {
			return nil
		}

		return []countState{s}
	}

	want := context.Canceled
	if s.count >= op.Args[0].(epoch.Epoch) {
		want = nil
	} else if s.failed {
		want = errEngine
	}

	if got, _ := op.Output.(error); op.Known && !errors.Is(got, want) {
		return nil
	}

	return []countState{s}
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

		var got epoch.Epoch

		contract := bench.Start(b).MaxAllocs(0)
		defer contract.End()

		for contract.Loop() {
			got = c.Current()
		}

		assert.Equal(b, got, epoch.Epoch(1), "the benchmark must measure the raised count")
	})

	b.Run("Wait for a met target", func(b *testing.B) {
		var c epoch.EventCount
		c.Advance(1)
		ctx := b.Context()

		var err error

		contract := bench.Start(b).MaxAllocs(0)
		defer contract.End()

		for contract.Loop() {
			err = c.Wait(ctx, 1)
		}

		assert.NoError(b, err, "the benchmark must measure a met target")
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

		var (
			v   epoch.Epoch
			err error
		)

		contract := bench.Start(b).MaxAllocs(2)
		for contract.Loop() {
			v++
			ping.Advance(v)
			err = pong.Wait(ctx, v)
		}
		contract.End()

		cancel()
		<-peer
		assert.NoError(b, err, "the benchmark must measure rounds that complete")
	})
}
