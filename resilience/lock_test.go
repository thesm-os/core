// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package resilience_test

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"

	"go.thesmos.sh/core/resilience"
)

// The causes that end the contexts of the cases: the end of a caller, and the
// failure of the disk whose context is the owner of a lock.
var (
	errCaller = errors.New("the caller stopped")
	errDisk   = errors.New("the disk failed")
)

// The operations that the history of the concurrent callers records.
const (
	opLock   = "lock"
	opUnlock = "unlock"
)

// section is the last section of a Close that a case passes to Finally. It
// counts its runs, and records a run that found the lock free.
type section struct {
	l    *resilience.Lock
	runs atomic.Int32
	free atomic.Bool
}

// run counts a run of s, and records whether the lock was free during the
// run. TryLock takes a free lock, and the case then fails on free.
func (s *section) run() {
	s.runs.Add(1)
	if s.l.TryLock() {
		s.free.Store(true)
	}
}

func TestLock(t *testing.T) {
	t.Parallel()

	t.Run("NewLock", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a free Lock", func(t *testing.T) {
			t.Parallel()
			assert.True(t, resilience.NewLock(t.Context()).TryLock(), "a new Lock must be free")
		})
	})

	t.Run("Lock", func(t *testing.T) {
		t.Parallel()

		t.Run("takes a free lock", func(t *testing.T) {
			t.Parallel()
			l := resilience.NewLock(t.Context())
			assert.NoError(t, l.Lock(bounded(t)), "Lock must take a free lock")
			assert.False(t, l.TryLock(), "a second caller must not take the lock that Lock took")
		})

		// synctest.Wait returns only once every other goroutine of the bubble
		// is durably blocked, so the case also checks that a wait is durable.
		t.Run("waits until Unlock frees the lock", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				l := resilience.NewLock(t.Context())
				assert.True(t, l.TryLock(), "the case must take the lock")
				got := startLock(t.Context(), l)
				synctest.Wait()
				assert.Empty(t, got, "Lock must wait while another caller has the lock")

				l.Unlock()
				synctest.Wait()
				assert.NoError(t, <-got, "Lock must take the lock that Unlock frees")
				assert.False(t, l.TryLock(), "the caller of Lock must have the lock")
			})
		})

		t.Run("returns the cause of ctx when ctx ends during the wait", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				l := resilience.NewLock(t.Context())
				assert.True(t, l.TryLock(), "the case must take the lock")
				ctx, cancel := context.WithCancelCause(t.Context())
				got := startLock(ctx, l)
				synctest.Wait()

				cancel(errCaller)
				synctest.Wait()
				assert.ErrorIs(t, <-got, errCaller, "Lock must return the cause of the context of its caller")
				l.Unlock()
				assert.True(t, l.TryLock(), "a caller whose wait ended must not take the lock")
			})
		})

		t.Run("returns the cause of the owner when the owner ends during the wait", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				owner, fail := context.WithCancelCause(t.Context())
				l := resilience.NewLock(owner)
				assert.True(t, l.TryLock(), "the case must take the lock")
				got := startLock(t.Context(), l)
				synctest.Wait()

				fail(errDisk)
				synctest.Wait()
				assert.ErrorIs(t, <-got, errDisk, "Lock must return the cause of the context of the owner")
				l.Unlock()
				assert.True(t, l.TryLock(), "a caller whose wait ended must not take the lock")
			})
		})

		t.Run("returns the cause of ctx that ended before the call", func(t *testing.T) {
			t.Parallel()
			l := resilience.NewLock(t.Context())
			ctx, cancel := context.WithCancelCause(t.Context())
			cancel(errCaller)
			assert.ErrorIs(t, l.Lock(ctx), errCaller, "Lock must refuse a caller whose context ended")
			assert.True(t, l.TryLock(), "Lock must leave the lock free for a caller whose context ended")
		})

		t.Run("returns the cause of the owner that ended before the call", func(t *testing.T) {
			t.Parallel()
			owner, fail := context.WithCancelCause(t.Context())
			fail(errDisk)
			l := resilience.NewLock(owner)
			assert.ErrorIs(t, l.Lock(bounded(t)), errDisk, "Lock must refuse every caller once the owner ended")
			assert.True(t, l.TryLock(), "Lock must leave the lock free once the owner ended")
		})

		t.Run("returns the cause of ctx before the cause of the owner", func(t *testing.T) {
			t.Parallel()
			owner, fail := context.WithCancelCause(t.Context())
			fail(errDisk)
			ctx, cancel := context.WithCancelCause(t.Context())
			cancel(errCaller)
			assert.ErrorIs(t, resilience.NewLock(owner).Lock(ctx), errCaller,
				"Lock must check the context of its caller first")
		})

		// The callers take and free the lock in rounds, and the history of the
		// calls must linearize against a lock that one caller at a time has.
		t.Run("lets one caller at a time take the lock", func(t *testing.T) {
			t.Parallel()
			const (
				callers = 8
				rounds  = 25
			)

			l := resilience.NewLock(t.Context())
			h := history.New()
			outcomes := history.Concurrently(callers, patience, func(client int) (any, error) {
				for range rounds {
					call := h.Invoke(client, opLock, nil)
					err := l.Lock(bounded(t))
					call.OK(err)
					if err != nil {
						return client, err
					}

					// The caller keeps the lock across a scheduling point, so
					// that the callers contend.
					runtime.Gosched()

					call = h.Invoke(client, opUnlock, nil)
					l.Unlock()
					call.OK(nil)
				}

				return client, nil
			})
			for _, o := range outcomes {
				assert.True(t, o.Finished, "every caller must return")
			}

			history.Linearizable(t, h, history.Spec[bool]{
				Initial: func() bool { return false },
				Next: func(taken bool, op history.Operation) []bool {
					if op.Name == opUnlock {
						return []bool{false}
					}
					if taken || !op.Returned(nil) {
						return nil
					}

					return []bool{true}
				},
			}, "a Lock must take the lock that an Unlock frees")
			assert.True(t, l.TryLock(), "the lock must be free once every caller returned")
		})
	})

	t.Run("TryLock", func(t *testing.T) {
		t.Parallel()

		t.Run("reports false while another caller has the lock", func(t *testing.T) {
			t.Parallel()
			l := resilience.NewLock(t.Context())
			assert.NoError(t, l.Lock(bounded(t)), "the case must take the lock")
			assert.False(t, l.TryLock(), "TryLock must not take the lock of another caller")
		})

		t.Run("takes a free lock after the owner ended", func(t *testing.T) {
			t.Parallel()
			owner, fail := context.WithCancelCause(t.Context())
			fail(errDisk)
			assert.True(t, resilience.NewLock(owner).TryLock(),
				"TryLock must not wait, so the end of the owner must not refuse it")
		})
	})

	t.Run("Unlock", func(t *testing.T) {
		t.Parallel()

		t.Run("frees the lock", func(t *testing.T) {
			t.Parallel()
			l := resilience.NewLock(t.Context())
			assert.NoError(t, l.Lock(bounded(t)), "the case must take the lock")
			l.Unlock()
			assert.True(t, l.TryLock(), "a caller must take the lock that Unlock freed")
		})

		t.Run("does nothing to a free lock", func(t *testing.T) {
			t.Parallel()
			l := resilience.NewLock(t.Context())
			l.Unlock()
			assert.True(t, l.TryLock(), "Unlock of a free lock must leave it free")
			assert.False(t, l.TryLock(), "Unlock of a free lock must not let a second caller take it")
		})

		t.Run("runs the section that Finally marked", func(t *testing.T) {
			t.Parallel()
			owner, fail := context.WithCancelCause(t.Context())
			l := resilience.NewLock(owner)
			assert.True(t, l.TryLock(), "the case must take the lock")
			fail(errDisk)
			s := &section{l: l}
			assert.False(t, l.Finally(s.run), "Finally must leave the section to the caller that has the lock")

			l.Unlock()
			expect.Equal(t, s.runs.Load(), 1, "Unlock must run the marked section once")
			expect.False(t, s.free.Load(), "Unlock must run the marked section under the lock")
			expect.True(t, l.TryLock(), "Unlock must free the lock after the section")
		})
	})

	t.Run("Finally", func(t *testing.T) {
		t.Parallel()

		t.Run("runs f at once on a free lock", func(t *testing.T) {
			t.Parallel()
			l := resilience.NewLock(t.Context())
			s := &section{l: l}
			expect.True(t, l.Finally(s.run), "Finally must report that f ran")
			expect.Equal(t, s.runs.Load(), 1, "Finally must run f once")
			expect.False(t, s.free.Load(), "Finally must run f under the lock")
			expect.True(t, l.TryLock(), "Finally must free the lock after f")
		})

		t.Run("runs f after the caller that has the lock frees it", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				l := resilience.NewLock(t.Context())
				assert.True(t, l.TryLock(), "the case must take the lock")
				s := &section{l: l}
				got := startFinally(l, s.run)
				synctest.Wait()
				assert.Empty(t, got, "Finally must wait while another caller has the lock")
				assert.Equal(t, s.runs.Load(), 0, "Finally must not run f while another caller has the lock")

				l.Unlock()
				synctest.Wait()
				expect.True(t, <-got, "Finally must report that f ran")
				expect.Equal(t, s.runs.Load(), 1, "Finally must run f once")
				expect.False(t, s.free.Load(), "Finally must run f under the lock")
				expect.True(t, l.TryLock(), "Finally must free the lock after f")
			})
		})

		t.Run("returns false when the owner ends during the wait", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				owner, fail := context.WithCancelCause(t.Context())
				l := resilience.NewLock(owner)
				assert.True(t, l.TryLock(), "the case must take the lock")
				s := &section{l: l}
				got := startFinally(l, s.run)
				synctest.Wait()

				fail(errDisk)
				synctest.Wait()
				expect.False(t, <-got, "Finally must return at the end of the owner")
				expect.Equal(t, s.runs.Load(), 0, "Finally must leave f to the caller that has the lock")
				l.Unlock()
			})
		})

		t.Run("returns false once the owner ended while another caller has the lock", func(t *testing.T) {
			t.Parallel()
			owner, fail := context.WithCancelCause(t.Context())
			l := resilience.NewLock(owner)
			assert.True(t, l.TryLock(), "the case must take the lock")
			fail(errDisk)
			s := &section{l: l}
			expect.False(t, l.Finally(s.run), "Finally must not wait once the owner ended")
			expect.Equal(t, s.runs.Load(), 0, "Finally must leave f to the caller that has the lock")
		})

		t.Run("runs f at once on a free lock after the owner ended", func(t *testing.T) {
			t.Parallel()
			owner, fail := context.WithCancelCause(t.Context())
			fail(errDisk)
			l := resilience.NewLock(owner)
			s := &section{l: l}
			expect.True(t, l.Finally(s.run), "Finally must report that f ran")
			expect.Equal(t, s.runs.Load(), 1, "Finally must run f once")
			expect.False(t, s.free.Load(), "Finally must run f under the lock")
			expect.True(t, l.TryLock(), "Finally must free the lock after f")
		})

		t.Run("does not run f while another section is marked", func(t *testing.T) {
			t.Parallel()
			owner, fail := context.WithCancelCause(t.Context())
			l := resilience.NewLock(owner)
			assert.True(t, l.TryLock(), "the case must take the lock")
			fail(errDisk)
			first, second := &section{l: l}, &section{l: l}
			assert.False(t, l.Finally(first.run), "the first Finally must mark its section")

			expect.False(t, l.Finally(second.run), "Finally must not mark f while another section is marked")
			l.Unlock()
			expect.Equal(t, first.runs.Load(), 1, "Unlock must run the section that the first Finally marked")
			expect.Equal(t, second.runs.Load(), 0, "Finally must not run f while another section is marked")
		})

		// The caller that has the lock frees it while the owner ends and
		// Finally runs, so the trials cover the orders of the three calls.
		t.Run("runs f once when the caller frees the lock as the owner ends", func(t *testing.T) {
			t.Parallel()
			const trials = 200

			for range trials {
				owner, fail := context.WithCancelCause(t.Context())
				l := resilience.NewLock(owner)
				assert.True(t, l.TryLock(), "the case must take the lock")
				s := &section{l: l}

				var (
					ran    atomic.Bool
					before atomic.Int32
				)
				outcomes := history.Concurrently(3, patience, func(client int) (any, error) {
					switch client {
					case 0:
						l.Unlock()
					case 1:
						fail(errDisk)
					default:
						ran.Store(l.Finally(s.run))
						before.Store(s.runs.Load())
					}

					return client, nil
				})
				for _, o := range outcomes {
					assert.True(t, o.Finished, "every call must return")
				}

				assert.Equal(t, s.runs.Load(), 1, "f must run once")
				assert.False(t, s.free.Load(), "f must run under the lock")
				if ran.Load() {
					assert.Equal(t, before.Load(), 1, "Finally must report true only after f ran")
				}
				assert.True(t, l.TryLock(), "the lock must be free once f ran")
			}
		})
	})
}

// TestLockAllocs checks the allocation contract of Lock, TryLock and Unlock.
// MaxAllocs counts the allocations of the whole process, so the test does not
// run in parallel.
func TestLockAllocs(t *testing.T) {
	ctx := t.Context()

	t.Run("Lock", func(t *testing.T) {
		l := resilience.NewLock(ctx)

		var err error
		expect.MaxAllocs(t, func() {
			err = l.Lock(ctx)
			l.Unlock()
		}, 0, "Lock and Unlock of a free lock must not allocate")
		assert.NoError(t, err, "the test must measure a lock that Lock took")
	})

	t.Run("TryLock", func(t *testing.T) {
		l := resilience.NewLock(ctx)

		var got bool
		expect.MaxAllocs(t, func() {
			got = l.TryLock()
			l.Unlock()
		}, 0, "TryLock and Unlock must not allocate")
		assert.True(t, got, "the test must measure a lock that TryLock took")
	})
}

// BenchmarkLock reports the cost of Lock and TryLock, and fails above the
// allocations that their contracts state.
func BenchmarkLock(b *testing.B) {
	ctx := b.Context()

	b.Run("Lock", func(b *testing.B) {
		b.Run("of a free lock", func(b *testing.B) {
			l := resilience.NewLock(ctx)

			var err error

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				err = l.Lock(ctx)
				l.Unlock()
			}

			assert.NoError(b, err, "the benchmark must measure a lock that Lock took")
		})

		b.Run("of callers that contend", func(b *testing.B) {
			l := resilience.NewLock(ctx)

			var failed atomic.Int64

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			c.RunParallel(func(pb *bench.PB) {
				for pb.Next() {
					if l.Lock(ctx) != nil {
						failed.Add(1)

						continue
					}

					l.Unlock()
				}
			})

			assert.Equal(b, failed.Load(), 0, "the benchmark must measure locks that Lock took")
		})
	})

	b.Run("TryLock", func(b *testing.B) {
		l := resilience.NewLock(ctx)

		var got bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = l.TryLock()
			l.Unlock()
		}

		assert.True(b, got, "the benchmark must measure a lock that TryLock took")
	})
}

// startLock calls l.Lock(ctx) on a goroutine of its own, and returns the
// channel that receives its error. A case inside a bubble reads the channel
// after synctest.Wait.
func startLock(ctx context.Context, l *resilience.Lock) <-chan error {
	got := make(chan error, 1)
	go func() { got <- l.Lock(ctx) }()

	return got
}

// startFinally calls l.Finally(f) on a goroutine of its own, and returns the
// channel that receives its result. A case inside a bubble reads the channel
// after synctest.Wait.
func startFinally(l *resilience.Lock, f func()) <-chan bool {
	got := make(chan bool, 1)
	go func() { got <- l.Finally(f) }()

	return got
}
