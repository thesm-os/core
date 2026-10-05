// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/tlog/checkpoint"
)

// gated is a cosigner whose first signature waits until release closes,
// after it closes started.
type gated struct {
	checkpoint.Cosigner

	started, release chan struct{}
	once             sync.Once
}

func TestQueueInternal(t *testing.T) {
	t.Parallel()

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

			wg.Go(func() { logs[0].advance(t, s, 0, 5, nil) })
			<-g.started

			for _, l := range logs[1:] {
				wg.Go(func() { l.advance(t, s, 0, 5, nil) })
			}

			waitQueue(t, s, 3)
			close(g.release)
			wg.Wait()

			s.mu.RLock()
			seq := s.st.seq
			s.mu.RUnlock()

			testkit.Equal(t, seq, uint64(2), "the waiting calls must commit in one record")
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
			testkit.ErrorIs(t, err, cause, "wait must return the cause of ctx")
			testkit.True(t, owned, "the call must remain the caller's")
			testkit.Len(t, s.queue, 0, "the call must leave the queue")
		})

		t.Run("abandons a call that a commit took", func(t *testing.T) {
			t.Parallel()
			s := newInternalFixture(t).server(t)
			p := &pending{done: make(chan struct{}, 1), taken: true}

			ctx, cancel := context.WithCancelCause(t.Context())
			cancel(cause)

			owned, err := s.wait(ctx, p)
			testkit.ErrorIs(t, err, cause, "wait must return the cause of ctx")
			testkit.False(t, owned, "the call must pass to the commit")
			testkit.True(t, p.abandoned, "the call must be abandoned")
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
			wg.Wait()

			testkit.NoError(t, err, "wait must return the delivered result")
			testkit.True(t, owned, "the call must remain the caller's")
		})

		t.Run("returns the result of a commit", func(t *testing.T) {
			t.Parallel()
			s := newInternalFixture(t).server(t)
			p := &pending{done: make(chan struct{}, 1)}
			p.done <- struct{}{}

			owned, err := s.wait(t.Context(), p)
			testkit.NoError(t, err, "wait must return the result")
			testkit.True(t, owned, "the call must remain the caller's")
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
				testkit.Len(t, got, tt.want, "take must take the calls within the bounds")
				testkit.Len(t, s.queue, len(queued)-tt.want, "take must leave the other calls")

				for i, p := range queued {
					testkit.Equal(t, p.taken, i < tt.want, "take must mark the calls that it takes")
				}
			})
		}
	})

	t.Run("deliver", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a call that its caller abandoned to the pool", func(t *testing.T) {
			t.Parallel()
			s := newInternalFixture(t).server(t)
			p := &pending{done: make(chan struct{}, 1), taken: true, abandoned: true, lines: []byte("lines")}

			s.deliver([]*pending{p})
			testkit.False(t, p.taken, "deliver must reset the call")
			testkit.Len(t, p.done, 0, "deliver must send no result")
		})

		t.Run("sends the result of a call to its caller", func(t *testing.T) {
			t.Parallel()
			s := newInternalFixture(t).server(t)
			p := &pending{done: make(chan struct{}, 1), taken: true}

			s.deliver([]*pending{p})
			testkit.True(t, p.delivered, "deliver must mark the call")
			testkit.Len(t, p.done, 1, "deliver must send the result")
		})
	})
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

// waitQueue waits until n calls wait in the queue of s, for at most 5
// seconds.
func waitQueue(tb testing.TB, s *Server, n int) {
	tb.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for {
		s.qmu.Lock()
		got := len(s.queue)
		s.qmu.Unlock()

		if got == n {
			return
		}

		if time.Now().After(deadline) {
			tb.Fatalf("%d calls wait, not %d", got, n)
		}

		time.Sleep(time.Millisecond)
	}
}
