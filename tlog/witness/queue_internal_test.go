// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/tlog/checkpoint"
)

// patience bounds each wait of an internal case: a call into a Server, a
// value that a hook sends, and the goroutines that a case starts.
const patience = 5 * time.Second

// gated is a cosigner whose first signature waits until release closes,
// after it closes started.
type gated struct {
	checkpoint.Cosigner

	started, release chan struct{}
	once             sync.Once
}

func TestQueueInternal(t *testing.T) {
	t.Parallel()

	t.Run("reset", func(t *testing.T) {
		t.Parallel()

		t.Run("drops the updates and the entries of the call before", func(t *testing.T) {
			t.Parallel()
			p := &pending{
				updates: []update{{origin: "example.com/a", size: 5}},
				entries: []entry{{Origin: "example.com/a", Size: 5}},
			}

			p.reset()
			expect.Equal(t, p.updates[:1], []update{{}}, "reset must drop the updates")
			expect.Equal(t, p.entries[:1], []entry{{}}, "reset must drop the entries")
		})
	})

	t.Run("enqueue", func(t *testing.T) {
		t.Parallel()

		t.Run("commits the calls that wait for a commit in one record", func(t *testing.T) {
			t.Parallel()
			logs := make([]*internalLog, 4)
			for i := range logs {
				logs[i] = newInternalLog(t, "example.com/log/"+strconv.Itoa(i))
			}

			f := newInternalFixture(t, logs...)
			g := &gated{Cosigner: f.signer, started: make(chan struct{}), release: make(chan struct{})}
			f.signer = g
			s := f.server(t)

			var wg sync.WaitGroup

			first := make(chan struct{})

			wg.Go(func() {
				defer close(first)

				logs[0].advance(t, s, 0, 5, nil)
			})
			awaitBefore(t, g.started, first, "the first commit must sign")

			for _, l := range logs[1:] {
				wg.Go(func() { l.advance(t, s, 0, 5, nil) })
			}

			waitQueue(t, s, 3)
			close(g.release)
			waitAll(t, &wg, "every call must return")

			s.mu.RLock()
			seq := s.st.seq
			s.mu.RUnlock()

			assert.Equal(t, seq, uint64(2), "the waiting calls must commit in one record")
		})
	})

	t.Run("wait", func(t *testing.T) {
		t.Parallel()

		cause := errors.New("the caller left")

		t.Run("removes a call that no commit took from the queue", func(t *testing.T) {
			t.Parallel()
			s := newInternalFixture(t).server(t)
			p := &pending{done: make(chan struct{}, 1)}
			s.queue = append(s.queue, p)

			ctx, cancel := context.WithCancelCause(t.Context())
			cancel(cause)

			owned, err := s.wait(ctx, p)
			assert.ErrorIs(t, err, cause, "wait must return the cause of ctx")
			expect.True(t, owned, "the call must remain the caller's")
			expect.Empty(t, s.queue, "the call must leave the queue")
			expect.True(t, s.qmu.TryLock(), "wait must unlock the queue")
		})

		t.Run("abandons a call that a commit took", func(t *testing.T) {
			t.Parallel()
			s := newInternalFixture(t).server(t)
			p := &pending{done: make(chan struct{}, 1), taken: true}

			ctx, cancel := context.WithCancelCause(t.Context())
			cancel(cause)

			owned, err := s.wait(ctx, p)
			assert.ErrorIs(t, err, cause, "wait must return the cause of ctx")
			assert.False(t, owned, "the call must pass to the commit")
			assert.True(t, p.abandoned, "the call must be abandoned")
		})

		t.Run("returns the result that a commit delivered after the end of ctx", func(t *testing.T) {
			t.Parallel()
			s := newInternalFixture(t).server(t)
			p := &pending{done: make(chan struct{}, 1), taken: true}

			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			s.qmu.Lock()

			var (
				owned bool
				err   error
				wg    sync.WaitGroup
			)

			wg.Go(func() { owned, err = s.wait(ctx, p) })
			time.Sleep(20 * time.Millisecond)
			p.delivered = true
			p.done <- struct{}{}
			s.qmu.Unlock()
			waitAll(t, &wg, "wait must return")

			assert.NoError(t, err, "wait must return the delivered result")
			expect.True(t, owned, "the call must remain the caller's")
			expect.Empty(t, p.done, "wait must receive the delivered result")
			expect.True(t, s.qmu.TryLock(), "wait must unlock the queue")
		})

		t.Run("returns the result of a commit", func(t *testing.T) {
			t.Parallel()
			s := newInternalFixture(t).server(t)
			p := &pending{done: make(chan struct{}, 1)}
			p.done <- struct{}{}

			owned, err := s.wait(bounded(t), p)
			assert.NoError(t, err, "wait must return the result")
			assert.True(t, owned, "the call must remain the caller's")
		})
	})

	t.Run("take", func(t *testing.T) {
		t.Parallel()

		calls := func(sizes, updates []int) []*pending {
			out := make([]*pending, len(sizes))
			for i := range out {
				out[i] = &pending{size: sizes[i], updates: make([]update, updates[i])}
			}

			return out
		}

		tests := []struct {
			name    string
			sizes   []int
			updates []int
			want    int
		}{
			{name: "takes every call within the bounds", sizes: []int{10, 10, 10}, updates: []int{1, 1, 1}, want: 3},
			{
				name: "stops at 16 MiB of calls", sizes: []int{maxCommitBytes - 10, 10, 1}, updates: []int{1, 1, 1},
				want: 2,
			},
			{
				name: "stops before a call beyond 16 MiB", sizes: []int{maxCommitBytes - 10, 11}, updates: []int{1, 1},
				want: 1,
			},
			{name: "stops at 4,096 updates", sizes: []int{1, 1, 1}, updates: []int{4000, 96, 1}, want: 2},
			{name: "stops before a call beyond 4,096 updates", sizes: []int{1, 1}, updates: []int{4000, 97}, want: 1},
			{
				name: "takes a first call above the bounds", sizes: []int{maxCommitBytes + 1, 1}, updates: []int{1, 1},
				want: 1,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				s := newInternalFixture(t).server(t)
				queued := calls(tt.sizes, tt.updates)
				s.queue = append(s.queue, queued...)

				got := s.take(nil)
				assert.Length(t, got, tt.want, "take must take the calls within the bounds")
				assert.Length(t, s.queue, len(queued)-tt.want, "take must leave the other calls")

				for i, p := range queued {
					assert.Equal(t, p.taken, i < tt.want, "take must mark the calls that it takes")
				}
			})
		}

		t.Run("drops the references of the queue to the calls that it takes", func(t *testing.T) {
			t.Parallel()
			s := newInternalFixture(t).server(t)
			s.queue = append(s.queue, calls([]int{10, 10}, []int{1, 1})...)

			s.take(nil)
			assert.Equal(t, s.queue[:cap(s.queue)], make([]*pending, cap(s.queue)), "take must drop the calls")
		})
	})

	t.Run("deliver", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a call that its caller abandoned to the pool", func(t *testing.T) {
			t.Parallel()
			s := newInternalFixture(t).server(t)
			p := &pending{done: make(chan struct{}, 1), taken: true, abandoned: true, lines: []byte("lines")}

			s.deliver([]*pending{p})
			assert.False(t, p.taken, "deliver must reset the call")
			assert.Empty(t, p.done, "deliver must send no result")
		})

		t.Run("sends the result of a call to its caller", func(t *testing.T) {
			t.Parallel()
			s := newInternalFixture(t).server(t)
			p := &pending{done: make(chan struct{}, 1), taken: true}

			s.deliver([]*pending{p})
			assert.True(t, p.delivered, "deliver must mark the call")
			assert.Length(t, p.done, 1, "deliver must send the result")
		})
	})
}

// TestQueueInternalAllocs checks the allocation contract of deliver that
// BenchmarkQueueInternal states. MaxAllocs counts the allocations of the
// whole process, so the test does not run in parallel.
func TestQueueInternalAllocs(t *testing.T) {
	t.Run("deliver", func(t *testing.T) {
		t.Run("of an abandoned call", func(t *testing.T) {
			s := newInternalFixture(t).server(t)
			expect.MaxAllocs(t, func() { deliverAbandoned(s) }, 0,
				"deliver must return an abandoned call to the pool, from which the next call takes it")
		})
	})
}

func BenchmarkQueueInternal(b *testing.B) {
	b.Run("deliver", func(b *testing.B) {
		b.Run("of an abandoned call", func(b *testing.B) {
			s := newInternalFixture(b).server(b)

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				deliverAbandoned(s)
			}
		})
	})
}

// deliverAbandoned takes a call from the pool of s, marks it taken and
// abandoned, as a commit finds the call of a caller whose context ended,
// and delivers it.
func deliverAbandoned(s *Server) {
	p := s.pendings.Get()
	p.taken, p.abandoned = true, true
	s.deliver([]*pending{p})
}

// AppendSignAt closes g.started and waits for g.release on the first
// signature.
func (g *gated) AppendSignAt(ctx context.Context, dst, text []byte, t time.Time) ([]byte, error) {
	g.once.Do(func() {
		close(g.started)
		<-g.release
	})

	return g.Cosigner.AppendSignAt(ctx, dst, text, t)
}

// waitQueue waits until n calls wait in the queue of s, for at most
// patience.
func waitQueue(tb testing.TB, s *Server, n int) {
	tb.Helper()

	assert.Eventually(tb, patience, time.Millisecond, func(attempt assert.TB) {
		s.qmu.Lock()
		got := len(s.queue)
		s.qmu.Unlock()

		assert.Equal(attempt, got, n, "the queue must contain n calls")
	}, "n calls must wait in the queue")
}

// bounded returns a context of tb that ends after patience. A call into a
// Server under it returns the error of the context when a defect stalls
// the call, so the case fails instead of waiting for the end of the test.
func bounded(tb testing.TB) context.Context {
	tb.Helper()

	ctx, cancel := context.WithTimeout(tb.Context(), patience)
	tb.Cleanup(cancel)

	return ctx
}

// awaitBefore returns the next value of ch, and fails tb at once when end
// delivers first while ch has no value, or when nothing arrives within
// patience. A test passes the end of the call that leads to the value, so
// it fails as soon as that call returns without the value.
func awaitBefore[T, E any](tb testing.TB, ch <-chan T, end <-chan E, what string) T {
	tb.Helper()

	timer := time.NewTimer(patience)
	defer timer.Stop()

	var zero T

	select {
	case v := <-ch:
		return v
	case <-end:
		// A value that came before the end of the call is in ch.
		select {
		case v := <-ch:
			return v
		default:
		}

		tb.Fatalf("%s: the call returned first", what)
	case <-timer.C:
		tb.Fatalf("%s: nothing arrived within %s", what, patience)
	}

	return zero
}

// waitAll waits for wg, and fails tb when wg does not finish within
// patience.
func waitAll(tb testing.TB, wg *sync.WaitGroup, what string) {
	tb.Helper()

	done := make(chan struct{})

	go func() {
		wg.Wait()
		close(done)
	}()

	timer := time.NewTimer(patience)
	defer timer.Stop()

	select {
	case <-done:
	case <-timer.C:
		tb.Fatal(what)
	}
}
