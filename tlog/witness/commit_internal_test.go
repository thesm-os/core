// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/blob"
	"go.thesmos.sh/core/version"
)

func TestCommitInternal(t *testing.T) {
	t.Parallel()

	l := newInternalLog(t, "example.com/a")

	// faultyServer returns a fixture, a store with hooks and a server over
	// that store.
	faultyServer := func(tb testing.TB) (*internalFixture, *faulty, *Server) {
		tb.Helper()

		f := newInternalFixture(tb, l)
		st := &faulty{Store: f.store}
		cfg := f.config()
		cfg.State = st

		return f, st, newInternalServer(tb, cfg)
	}

	t.Run("checkCall", func(t *testing.T) {
		t.Parallel()

		t.Run("checks a call against the earlier calls of the commit for one origin", func(t *testing.T) {
			t.Parallel()
			other := newInternalLog(t, "example.com/b")
			f := newInternalFixture(t, l, other)
			g := &gated{Cosigner: f.signer, started: make(chan struct{}), release: make(chan struct{})}
			f.signer = g
			s := f.server(t)

			var wg sync.WaitGroup

			wg.Go(func() { other.advance(t, s, 0, 5, nil) })
			<-g.started

			var stale []Failure

			wg.Go(func() { l.advance(t, s, 0, 5, nil) })
			waitQueue(t, s, 1)
			wg.Go(func() { l.advance(t, s, 5, 6, nil) })
			waitQueue(t, s, 2)
			wg.Go(func() {
				_, stale, _ = s.Advance(t.Context(), l.note(t, 7), []Update{l.update(t, 0, 7, nil)}, nil)
			})
			waitQueue(t, s, 3)
			close(g.release)
			wg.Wait()

			testkit.Len(t, stale, 1, "the third call must fail against the first two")

			se, ok := errors.AsType[*SizeError](stale[0].Err)
			testkit.True(t, ok, "the failure must be a SizeError")
			testkit.Equal(t, se.Size, uint64(6), "the SizeError must contain the size of the second call")
		})
	})

	t.Run("applyCommit", func(t *testing.T) {
		t.Parallel()

		t.Run("leaves the state of a refresh that applied the commit first", func(t *testing.T) {
			t.Parallel()
			_, st, s := faultyServer(t)

			var once atomic.Bool

			st.afterPut = func(key string) {
				if key == headKey && once.CompareAndSwap(false, true) {
					_, err := s.sync(t.Context(), "")
					testkit.NoError(t, err, "the refresh must catch up to the head")
				}
			}

			l.advance(t, s, 0, 5, nil)

			o, _ := s.st.origins.Get(hashOrigin(l.origin))
			testkit.Equal(t, o.size, uint64(5), "the state must contain the commit")
			testkit.Equal(t, s.st.seq, uint64(1), "the state must contain one record")
			testkit.True(t, o.served == o.latest, "the commit must move the served position")
		})
	})

	t.Run("storeLines", func(t *testing.T) {
		t.Parallel()

		// logging returns a store with hooks, a handler that keeps the
		// messages of the log, and a server over both.
		logging := func(tb testing.TB, logs ...*internalLog) (*faulty, *logged, *Server) {
			tb.Helper()

			f := newInternalFixture(tb, logs...)
			st := &faulty{Store: f.store}
			h := &logged{}
			cfg := f.config()
			cfg.State, cfg.Logger = st, slog.New(h)

			return st, h, newInternalServer(tb, cfg)
		}

		t.Run("delivers the result of a call before the write of its lines", func(t *testing.T) {
			t.Parallel()
			_, st, s := faultyServer(t)

			// The cleanup releases the write when an assertion fails first.
			release := make(chan struct{})
			open := sync.OnceFunc(func() { close(release) })
			t.Cleanup(open)

			st.put = func(key string) error {
				if strings.HasPrefix(key, linesPrefix) {
					<-release
				}

				return nil
			}

			lines, _, err := s.Advance(t.Context(), l.note(t, 5), []Update{l.update(t, 0, 5, nil)}, nil)
			testkit.NoError(t, err, "Advance must return before the write of the lines")
			testkit.NotEqual(t, len(lines), 0, "Advance must return the lines")
			testkit.Len(t, listKeys(t, st.Store, linesPrefix), 0, "the commit must not have stored its lines")
			testkit.Equal(t, getRoute(t, s, l.origin).Code, http.StatusNotFound,
				"the route must not serve the commit before its lines")

			open()

			s.lmu.Lock()
			s.mu.RLock()
			o, _ := s.st.origins.Get(hashOrigin(l.origin))
			s.mu.RUnlock()
			s.lmu.Unlock()

			testkit.True(t, o.served == o.latest, "the write of the lines must move the served position")
		})

		t.Run("logs a write of lines that fails and moves no served position", func(t *testing.T) {
			t.Parallel()
			st, h, s := logging(t, l)
			st.refuse(linesPrefix)

			l.advance(t, s, 0, 5, nil)

			s.mu.RLock()
			o, _ := s.st.origins.Get(hashOrigin(l.origin))
			s.mu.RUnlock()

			testkit.True(t, h.has("witness: a commit did not store its lines"), "the commit must log the write")
			testkit.Equal(t, o.served.key, "", "the commit must move no served position")
		})

		t.Run("moves the served positions over the lines that a repair stored first", func(t *testing.T) {
			t.Parallel()
			st, h, s := logging(t, l)

			ctx := t.Context()
			st.put = func(key string) error {
				if strings.HasPrefix(key, linesPrefix) {
					data, _ := (&lines{Calls: [][]byte{[]byte("the stored lines\n")}}).MarshalBinary()
					_, err := blob.PutBytes(ctx, st.Store, key, data, blob.PutOptions{})

					return err
				}

				return nil
			}

			l.advance(t, s, 0, 5, nil)

			s.mu.RLock()
			o, _ := s.st.origins.Get(hashOrigin(l.origin))
			s.mu.RUnlock()

			testkit.True(t, o.served == o.latest, "the commit must move the served position")
			testkit.False(t, h.has("witness: a commit did not store its lines"), "the commit must log no failure")
		})

		t.Run("writes the lines of one commit at a time", func(t *testing.T) {
			t.Parallel()
			other := newInternalLog(t, "example.com/b")
			st, _, s := logging(t, l, other)

			var started, heads atomic.Int64

			// The cleanup releases the first write when an assertion fails
			// first.
			release := make(chan struct{})
			open := sync.OnceFunc(func() { close(release) })
			t.Cleanup(open)

			st.put = func(key string) error {
				if strings.HasPrefix(key, linesPrefix) && started.Add(1) == 1 {
					<-release
				}

				return nil
			}
			st.afterPut = func(key string) {
				if key == headKey {
					heads.Add(1)
				}
			}

			_, _, err := s.Advance(t.Context(), l.note(t, 5), []Update{l.update(t, 0, 5, nil)}, nil)
			testkit.NoError(t, err, "the first commit must return before its lines")

			var (
				returned atomic.Bool
				wg       sync.WaitGroup
			)

			wg.Go(func() {
				other.advance(t, s, 0, 5, nil)
				returned.Store(true)
			})

			deadline := time.Now().Add(5 * time.Second)
			for heads.Load() < 2 {
				if time.Now().After(deadline) {
					t.Fatal("the second commit must replace the head")
				}

				time.Sleep(time.Millisecond)
			}

			testkit.False(t, returned.Load(), "the second call must wait for the lines of the first commit")
			testkit.Equal(t, started.Load(), int64(1), "the second commit must not write its lines yet")

			open()
			wg.Wait()

			testkit.Equal(t, started.Load(), int64(2), "the second commit must write its lines after the first")
		})

		t.Run("writes nothing for a commit whose calls fail", func(t *testing.T) {
			t.Parallel()
			_, st, s := faultyServer(t)
			l.advance(t, s, 0, 5, nil)

			var puts atomic.Int64

			st.put = func(string) error {
				puts.Add(1)

				return nil
			}

			_, failures, err := s.Advance(t.Context(), l.note(t, 6), []Update{l.update(t, 0, 6, nil)}, nil)
			testkit.NoError(t, err, "Advance must check the update")
			testkit.Len(t, failures, 1, "Advance must return the conflict")

			s.lmu.Lock()
			n := puts.Load()
			s.lmu.Unlock()

			testkit.Equal(t, n, int64(0), "the commit must write nothing")
		})
	})

	t.Run("run", func(t *testing.T) {
		t.Parallel()

		failHead := func(st *faulty) {
			st.get = func(key string) error {
				if key == headKey {
					return errors.New("the read of the head failed")
				}

				return nil
			}
		}

		t.Run("fails the calls of a commit of failed calls whose head does not read", func(t *testing.T) {
			t.Parallel()
			_, st, s := faultyServer(t)
			l.advance(t, s, 0, 5, nil)
			failHead(st)

			_, failures, err := s.Advance(t.Context(), l.note(t, 6), []Update{l.update(t, 0, 6, nil)}, nil)
			testkit.Error(t, err, "Advance must fail")
			testkit.Len(t, failures, 0, "Advance must return no failure with an error")
		})

		t.Run("fails the calls of a commit of failed calls whose catch-up fails", func(t *testing.T) {
			t.Parallel()
			f, st, s := faultyServer(t)
			cfg := f.config()
			cfg.State = st
			l.advance(t, newInternalServer(t, cfg), 0, 5, nil)
			st.get = func(key string) error {
				if strings.HasPrefix(key, recordPrefix) {
					return errors.New("the read of the record failed")
				}

				return nil
			}

			_, _, err := s.Advance(t.Context(), l.note(t, 6), []Update{l.update(t, 3, 6, nil)}, nil)
			testkit.Error(t, err, "Advance must fail")
		})

		t.Run("fails the passed calls when the catch-up after a failed replacement fails", func(t *testing.T) {
			t.Parallel()
			_, st, s := faultyServer(t)
			st.put = func(key string) error {
				if key == headKey {
					return version.ErrMismatch
				}

				return nil
			}
			failHead(st)

			_, _, err := s.Advance(t.Context(), l.note(t, 5), []Update{l.update(t, 0, 5, nil)}, nil)
			testkit.Error(t, err, "Advance must fail")
			testkit.ErrorIsNot(t, err, ErrContention, "the error must be the error of the store")
		})

		t.Run("fails the passed calls when the head after an unknown outcome does not read", func(t *testing.T) {
			t.Parallel()
			_, st, s := faultyServer(t)
			st.put = func(key string) error {
				if key == headKey {
					return version.ErrOutcomeUnknown
				}

				return nil
			}
			failHead(st)

			_, _, err := s.Advance(t.Context(), l.note(t, 5), []Update{l.update(t, 0, 5, nil)}, nil)
			testkit.Error(t, err, "Advance must fail")
			testkit.ErrorIsNot(t, err, version.ErrOutcomeUnknown, "the error must be the error of the read")
		})
	})
}
