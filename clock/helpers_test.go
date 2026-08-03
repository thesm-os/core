// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package clock_test

import (
	"context"
	"runtime"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/clock/fake"
)

// originUTC is the reference start time for tests that need a
// concrete clock origin. Picking a fixed Date keeps Wall values
// readable in failure output.
var originUTC = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestSleep(t *testing.T) {
	t.Parallel()

	t.Run("blocks until virtual time advances past deadline", func(t *testing.T) {
		t.Parallel()
		c := fake.New(originUTC)
		done := make(chan struct{})
		go func() {
			defer close(done)
			clock.Sleep(c, 5*time.Second)
		}()
		t.Cleanup(func() { c.Advance(time.Hour) }) // ensure goroutine completes on test failure
		c.AwaitWaiters(1)
		c.Advance(6 * time.Second)
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("Sleep did not return after Advance past deadline")
		}
	})

	t.Run("returns without blocking for non-positive duration", func(t *testing.T) {
		t.Parallel()
		c := fake.New(originUTC)
		// Both 0 and -1 must fall through without registering a
		// waiter — the Clock contract for non-positive d requires
		// NewTimer to fire immediately.
		for _, d := range []time.Duration{0, -time.Second} {
			done := make(chan struct{})
			go func() { clock.Sleep(c, d); close(done) }()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatalf("Sleep(d=%v) did not return", d)
			}
		}
	})
}

func TestAfter(t *testing.T) {
	t.Parallel()

	t.Run("channel fires once virtual time advances past deadline", func(t *testing.T) {
		t.Parallel()
		c := fake.New(originUTC)
		ch := clock.After(c, 5*time.Second)

		// Before advance, the channel must not fire.
		select {
		case <-ch:
			t.Fatal("After channel fired before deadline")
		default:
		}

		c.Advance(6 * time.Second)
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatal("After channel did not fire post-Advance")
		}
	})

	t.Run("non-positive duration fires immediately", func(t *testing.T) {
		t.Parallel()
		c := fake.New(originUTC)
		ch := clock.After(c, 0)
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatal("After(0) did not fire immediately")
		}
	})
}

func TestWait(t *testing.T) {
	t.Parallel()

	t.Run("returns nil once virtual time advances past deadline", func(t *testing.T) {
		t.Parallel()
		c := fake.New(originUTC)
		errc := make(chan error, 1)
		go func() { errc <- clock.Wait(t.Context(), c, 5*time.Second) }()
		t.Cleanup(func() { c.Advance(time.Hour) }) // unblock on test failure
		c.AwaitWaiters(1)
		c.Advance(6 * time.Second)

		select {
		case err := <-errc:
			testkit.NoError(t, err, "Wait must return nil when the deadline elapses")
		case <-time.After(time.Second):
			t.Fatal("Wait did not return after Advance past deadline")
		}
	})

	t.Run("returns ctx.Err when the context is cancelled first", func(t *testing.T) {
		t.Parallel()
		c := fake.New(originUTC)
		ctx, cancel := context.WithCancel(t.Context())
		errc := make(chan error, 1)
		go func() { errc <- clock.Wait(ctx, c, time.Hour) }()
		c.AwaitWaiters(1)
		cancel()

		select {
		case err := <-errc:
			testkit.ErrorIs(t, err, context.Canceled, "Wait must return ctx.Err on cancellation")
		case <-time.After(time.Second):
			t.Fatal("Wait did not return after cancellation")
		}
	})

	// The Clock contract requires NewTimer to fire immediately for
	// non-positive d, so Wait must not register a waiter.
	for _, d := range []time.Duration{0, -time.Second} {
		t.Run("returns nil for duration "+d.String(), func(t *testing.T) {
			t.Parallel()
			c := fake.New(originUTC)
			errc := make(chan error, 1)
			go func() { errc <- clock.Wait(t.Context(), c, d) }()
			select {
			case err := <-errc:
				testkit.NoError(t, err, "Wait must return nil for a non-positive duration")
			case <-time.After(time.Second):
				t.Fatal("Wait did not return for a non-positive duration")
			}
		})
	}

	t.Run("returns ctx.Err when the context is already done", func(t *testing.T) {
		t.Parallel()
		c := fake.New(originUTC)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		// Racy by construction: both select arms are ready for a
		// non-positive d, so only the long-duration case is
		// deterministic.
		err := clock.Wait(ctx, c, time.Hour)
		testkit.ErrorIs(t, err, context.Canceled, "Wait must observe an already-cancelled context")
	})
}

func BenchmarkWait(b *testing.B) {
	c := fake.New(originUTC)
	b.ReportAllocs()
	var sink error
	for b.Loop() {
		sink = clock.Wait(b.Context(), c, 0)
	}
	runtime.KeepAlive(sink)
}
