// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package epoch_test

import (
	"math"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"

	"go.thesmos.sh/core/epoch"
)

// Parameters of the concurrent case of TestCounter.
const (
	// counterClients is the number of clients that call Next at once.
	counterClients = 8

	// counterCalls is the number of Next calls of each client.
	counterCalls = 64
)

func TestCounter(t *testing.T) {
	t.Parallel()

	t.Run("zero-value Counter starts at Zero, Next returns 1", func(t *testing.T) {
		t.Parallel()
		var c epoch.Counter
		assert.Equal(t, c.Current(), epoch.Zero,
			"Current on zero-value Counter must equal epoch.Zero")
		assert.Equal(t, c.Next(), epoch.Epoch(1),
			"first Next on zero-value Counter must return 1")
	})

	t.Run("NewCounter advances from start", func(t *testing.T) {
		t.Parallel()
		c := epoch.NewCounter(100)
		assert.Equal(t, c.Current(), epoch.Epoch(100),
			"Current after NewCounter(100) must equal 100")
		assert.Equal(t, c.Next(), epoch.Epoch(101),
			"first Next after NewCounter(100) must return 101")
	})

	t.Run("Next advances the counter by one per call", func(t *testing.T) {
		t.Parallel()
		c := epoch.NewCounter(100)
		assert.Accumulates(t, func(struct{}) error {
			c.Next()

			return nil
		}, struct{}{}, func() int { return int(c.Current()) }, "each Next must advance the counter by the same step")
		assert.Equal(t, c.Current(), epoch.Epoch(102), "two Next calls must advance the counter by two")
	})

	t.Run("Current does not advance the counter", func(t *testing.T) {
		t.Parallel()
		c := epoch.NewCounter(5)
		assert.Pure(t, c.Current, func() { _ = c.Current() }, "Current must not advance the counter")
	})

	t.Run("Next wraps at MaxUint64", func(t *testing.T) {
		t.Parallel()
		// Documented: at MaxUint64 the underlying counter wraps to
		// zero. Unreachable in practice; not guarded.
		c := epoch.NewCounter(math.MaxUint64 - 1)
		assert.Equal(t, c.Next(), epoch.Epoch(math.MaxUint64),
			"Next at MaxUint64-1 must return MaxUint64")
		assert.Equal(t, c.Next(), epoch.Zero,
			"Next at MaxUint64 must wrap to Zero")
	})

	t.Run("concurrent Next calls behave as one call at a time", func(t *testing.T) {
		t.Parallel()
		c := epoch.NewCounter(epoch.Zero)
		h := history.New()

		outcomes := history.Concurrently(counterClients, time.Minute, func(client int) (any, error) {
			var last epoch.Epoch
			for range counterCalls {
				call := h.Invoke(client, "next", nil)
				last = c.Next()
				call.OK(last)
			}

			return last, nil
		})
		for _, o := range outcomes {
			assert.True(t, o.Finished, "every client must finish its calls")
		}

		history.Linearizable(t, h, history.Spec[epoch.Epoch]{
			Initial: func() epoch.Epoch { return epoch.Zero },
			Next: func(s epoch.Epoch, op history.Operation) []epoch.Epoch {
				if !op.Returned(s.Successor()) {
					return nil
				}

				return []epoch.Epoch{s.Successor()}
			},
		}, "concurrent Next calls must return the epochs of a sequential counter")
		assert.Equal(t, c.Current(), epoch.Epoch(counterClients*counterCalls),
			"Current must count every Next call")
	})
}

// TestCounterAllocs checks the allocation ceiling of every method of a
// Counter. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
func TestCounterAllocs(t *testing.T) {
	t.Run("Next", func(t *testing.T) {
		c := epoch.NewCounter(epoch.Zero)

		var got epoch.Epoch
		expect.MaxAllocs(t, func() { got = c.Next() }, 0, "Counter.Next must not allocate")
		assert.Equal(t, got, c.Current(), "the test must measure the issued epochs")
	})

	t.Run("Current", func(t *testing.T) {
		c := epoch.NewCounter(epoch.Epoch(1))

		var got epoch.Epoch
		expect.MaxAllocs(t, func() { got = c.Current() }, 0, "Counter.Current must not allocate")
		assert.Equal(t, got, epoch.Epoch(1), "the test must measure the current epoch")
	})
}

func BenchmarkCounter(b *testing.B) {
	b.Run("Next", func(b *testing.B) {
		c := epoch.NewCounter(epoch.Zero)

		var got epoch.Epoch

		contract := bench.Start(b).MaxAllocs(0)
		defer contract.End()

		for contract.Loop() {
			got = c.Next()
		}

		assert.Equal(b, got, c.Current(), "the benchmark must measure the issued epochs")
	})

	b.Run("Current", func(b *testing.B) {
		c := epoch.NewCounter(epoch.Epoch(1))

		var got epoch.Epoch

		contract := bench.Start(b).MaxAllocs(0)
		defer contract.End()

		for contract.Loop() {
			got = c.Current()
		}

		assert.Equal(b, got, epoch.Epoch(1), "the benchmark must measure the current epoch")
	})

	b.Run("Next in parallel", func(b *testing.B) {
		c := epoch.NewCounter(epoch.Zero)

		contract := bench.Start(b).MaxAllocs(0)
		defer contract.End()

		contract.RunParallel(func(pb *bench.PB) {
			for pb.Next() {
				c.Next()
			}
		})

		assert.NotEqual(b, c.Current(), epoch.Zero, "the benchmark must measure issued epochs")
	})
}
