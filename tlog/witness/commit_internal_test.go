// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/blob"
	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/tlog/checkpoint"
	"go.thesmos.sh/core/version"
)

// brokenUTC is a clock.UTCSource whose ReadUTC fails with err. It returns
// reading with the error, a reading that the contract of clock.UTCSource
// leaves undefined, so a caller must not use it.
type brokenUTC struct {
	reading clock.UTCReading
	err     error
}

// sumCounter is a telemetry.Counter that keeps the sum of its values.
type sumCounter struct {
	sum atomic.Int64
}

var (
	_ clock.UTCSource   = brokenUTC{}
	_ telemetry.Counter = (*sumCounter)(nil)
)

// ReadUTC returns the reading and the error of u.
func (u brokenUTC) ReadUTC() (clock.UTCReading, error) {
	return u.reading, u.err
}

// Add adds value to the sum of c.
func (c *sumCounter) Add(_ context.Context, value int64) {
	c.sum.Add(value)
}

// With returns c.
func (c *sumCounter) With([]telemetry.Attr) telemetry.Counter {
	return c
}

// Release does nothing.
func (*sumCounter) Release() {}

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

	t.Run("commit", func(t *testing.T) {
		t.Parallel()

		t.Run("runs no commit for a queue that its callers emptied", func(t *testing.T) {
			t.Parallel()
			_, st, s := faultyServer(t)

			var reads atomic.Int64

			st.get = func(key string) error {
				if key == headKey {
					reads.Add(1)
				}

				return nil
			}

			// The committer that enqueue starts finds the queue empty when every
			// caller left before the committer took the queue.
			s.qmu.Lock()
			s.committing = true
			s.qmu.Unlock()

			s.commit()

			s.qmu.Lock()
			committing := s.committing
			s.qmu.Unlock()

			assert.Equal(t, reads.Load(), int64(0), "commit must read no head")
			assert.False(t, committing, "commit must record that no commit runs")
		})
	})

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

			first := make(chan struct{})

			wg.Go(func() {
				defer close(first)

				other.advance(t, s, 0, 5, nil)
			})
			awaitBefore(t, g.started, first, "the first commit must sign")

			var stale []Failure

			wg.Go(func() { l.advance(t, s, 0, 5, nil) })
			waitQueue(t, s, 1)
			wg.Go(func() { l.advance(t, s, 5, 6, nil) })
			waitQueue(t, s, 2)
			wg.Go(func() {
				_, stale, _ = s.Advance(bounded(t), l.note(t, 7), []Update{l.update(t, 0, 7, nil)}, nil)
			})
			waitQueue(t, s, 3)
			close(g.release)
			waitAll(t, &wg, "every call must return")

			assert.Length(t, stale, 1, "the third call must fail against the first two")

			se := assert.ErrorAs[*SizeError](t, stale[0].Err, "the failure must be a SizeError")
			assert.Equal(t, se.Size, uint64(6), "the SizeError must contain the size of the second call")
		})

		t.Run("counts the new origins of the earlier calls of the commit against MaxOrigins", func(t *testing.T) {
			t.Parallel()
			first, b, c := newInternalLog(t, "example.com/first"), newInternalLog(t, "example.com/b"),
				newInternalLog(t, "example.com/c")
			f := newInternalFixture(t, first, b, c)
			g := &gated{Cosigner: f.signer, started: make(chan struct{}), release: make(chan struct{})}
			f.signer = g
			cfg := f.config()
			cfg.MaxOrigins = 2
			s := newInternalServer(t, cfg)

			var wg sync.WaitGroup

			returned := make(chan struct{})

			wg.Go(func() {
				defer close(returned)

				first.advance(t, s, 0, 5, nil)
			})
			awaitBefore(t, g.started, returned, "the first commit must sign")

			// The next commit takes both calls. The new origin of b is the
			// second origin, and the new origin of c a third.
			var failures []Failure

			wg.Go(func() { b.advance(t, s, 0, 5, nil) })
			waitQueue(t, s, 1)
			wg.Go(func() {
				_, failures, _ = s.Advance(bounded(t), c.note(t, 5), []Update{c.update(t, 0, 5, nil)}, nil)
			})
			waitQueue(t, s, 2)
			close(g.release)
			waitAll(t, &wg, "every call must return")

			assert.Length(t, failures, 1, "the call of c must fail")
			assert.ErrorIs(t, failures[0].Err, ErrUnknownOrigin, "the failure must be ErrUnknownOrigin")
		})

		t.Run("accepts an update of an origin of a state at MaxOrigins", func(t *testing.T) {
			t.Parallel()
			f := newInternalFixture(t, l)
			cfg := f.config()
			cfg.MaxOrigins = 1
			s := newInternalServer(t, cfg)
			l.advance(t, s, 0, 5, nil)

			l.advance(t, s, 5, 6, nil)
		})
	})

	t.Run("check", func(t *testing.T) {
		t.Parallel()

		t.Run("checks an origin that came back after its retirement against the state", func(t *testing.T) {
			t.Parallel()
			other := newInternalLog(t, "example.com/b")
			f := newInternalFixture(t, l, other)
			s := f.server(t)
			l.advance(t, s, 0, 5, nil)
			f.refuse(l.origin)
			f.clock.Advance(2 * time.Hour)
			other.advance(t, s, 0, 5, nil)
			assert.NoError(t, s.snapshot(bounded(t)), "the retiring snapshot must install")
			assert.False(t, s.st.origins.Has(hashOrigin(l.origin)), "the snapshot must retire the origin")

			f.mu.Lock()
			f.accepted[l.origin] = l.log
			f.mu.Unlock()

			l.advance(t, s, 5, 6, nil)
			l.advance(t, s, 6, 7, nil)
		})
	})

	t.Run("admit", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the error of a UTC source that fails", func(t *testing.T) {
			t.Parallel()
			errUTC := errors.New("the source failed")
			cfg := newInternalFixture(t, l).config()
			cfg.UTC = brokenUTC{reading: clock.UTCReading{Time: internalTime, Synced: true}, err: errUTC}
			s := newInternalServer(t, cfg)

			_, _, err := s.Advance(bounded(t), l.note(t, 5), []Update{l.update(t, 0, 5, nil)}, nil)
			expect.ErrorIs(t, err, errUTC, "Advance must return the error of the source")
			expect.ErrorIs(t, err, checkpoint.ErrClock, "the error must wrap ErrClock")
		})

		t.Run("returns an error that states the Synced of an unsynchronised reading", func(t *testing.T) {
			t.Parallel()
			f := newInternalFixture(t, l)
			f.clock.SetUTCError(0, false)
			s := f.server(t)

			_, _, err := s.Advance(bounded(t), l.note(t, 5), []Update{l.update(t, 0, 5, nil)}, nil)
			assert.ErrorIs(t, err, checkpoint.ErrClock, "Advance must refuse the reading")
			assert.Contains(t, err.Error(), "Synced false", "the error must state the Synced of the reading")
		})
	})

	t.Run("applyCommit", func(t *testing.T) {
		t.Parallel()

		t.Run("leaves the state of a refresh that applied the commit first", func(t *testing.T) {
			t.Parallel()
			_, st, s := faultyServer(t)

			var (
				once    atomic.Bool
				refresh error
			)

			// The hook runs on the goroutine of the commit, where a failed
			// assertion would end the commit before it delivers the call.
			// The case checks the error of the refresh after Advance.
			st.afterPut = func(key string) {
				if key == headKey && once.CompareAndSwap(false, true) {
					_, refresh = s.sync(bounded(t), "")
				}
			}

			l.advance(t, s, 0, 5, nil)
			assert.NoError(t, refresh, "the refresh must catch up to the head")

			o, _ := s.st.origins.Get(hashOrigin(l.origin))
			assert.Equal(t, o.size, uint64(5), "the state must contain the commit")
			assert.Equal(t, s.st.seq, uint64(1), "the state must contain one record")
			assert.Equal(t, o.served, o.latest, "the commit must move the served position")
		})

		t.Run("leaves a state that does not reflect the head of the base of the commit", func(t *testing.T) {
			t.Parallel()
			_, _, s := faultyServer(t)
			l.advance(t, s, 0, 5, nil)
			seq, v := s.st.seq, s.st.version

			b := &batch{rec: record{Seq: 2, Time: 1, Prev: s.st.head.Record}}
			name := string(appendName(nil, 2, []byte("a record")))
			s.applyCommit(bounded(t), b, base{version: "a version before the state"}, recordPrefix+name, 10,
				"a later version")

			expect.Equal(t, s.st.seq, seq, "the state must keep its record")
			expect.Equal(t, s.st.version, v, "the state must keep its version")
		})
	})

	t.Run("commitCalls", func(t *testing.T) {
		t.Parallel()

		t.Run("drops the references of the batch to its calls after a commit", func(t *testing.T) {
			t.Parallel()
			_, _, s := faultyServer(t)
			l.advance(t, s, 0, 5, nil)

			assert.Equal(t, s.batch.calls, make([]*pending, len(s.batch.calls)), "the batch must drop its calls")
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

			lines, _, err := s.Advance(bounded(t), l.note(t, 5), []Update{l.update(t, 0, 5, nil)}, nil)
			assert.NoError(t, err, "Advance must return before the write of the lines")
			assert.NotEmpty(t, lines, "Advance must return the lines")
			assert.Empty(t, listKeys(t, st.Store, linesPrefix), "the commit must not have stored its lines")
			assert.Equal(t, getRoute(t, s, l.origin).Code, http.StatusNotFound,
				"the route must not serve the commit before its lines")

			open()

			s.lmu.Lock()
			s.mu.RLock()
			o, _ := s.st.origins.Get(hashOrigin(l.origin))
			s.mu.RUnlock()
			s.lmu.Unlock()

			assert.Equal(t, o.served, o.latest, "the write of the lines must move the served position")
		})

		t.Run("logs a write of lines that fails and moves no served position", func(t *testing.T) {
			t.Parallel()
			st, h, s := logging(t, l)
			st.refuse(linesPrefix)

			l.advance(t, s, 0, 5, nil)

			s.mu.RLock()
			o, _ := s.st.origins.Get(hashOrigin(l.origin))
			s.mu.RUnlock()

			assert.Contains(t, h.kept(), "witness: a commit did not store its lines", "the commit must log the write")
			assert.Equal(t, o.served.key, "", "the commit must move no served position")
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

			assert.Equal(t, o.served, o.latest, "the commit must move the served position")
			assert.NotContains(t, h.kept(), "witness: a commit did not store its lines",
				"the commit must log no failure")
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

			_, _, err := s.Advance(bounded(t), l.note(t, 5), []Update{l.update(t, 0, 5, nil)}, nil)
			assert.NoError(t, err, "the first commit must return before its lines")

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

			assert.False(t, returned.Load(), "the second call must wait for the lines of the first commit")
			assert.Equal(t, started.Load(), int64(1), "the second commit must not write its lines yet")

			open()
			waitAll(t, &wg, "the second call must return")

			assert.Equal(t, started.Load(), int64(2), "the second commit must write its lines after the first")
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

			_, failures, err := s.Advance(bounded(t), l.note(t, 6), []Update{l.update(t, 0, 6, nil)}, nil)
			assert.NoError(t, err, "Advance must check the update")
			assert.Length(t, failures, 1, "Advance must return the conflict")

			s.lmu.Lock()
			n := puts.Load()
			s.lmu.Unlock()

			assert.Equal(t, n, int64(0), "the commit must write nothing")
		})

		t.Run("moves the served position of an origin to the later of two calls of the record", func(t *testing.T) {
			t.Parallel()
			other := newInternalLog(t, "example.com/b")
			f := newInternalFixture(t, l, other)
			g := &gated{Cosigner: f.signer, started: make(chan struct{}), release: make(chan struct{})}
			f.signer = g
			s := f.server(t)

			var wg sync.WaitGroup

			returned := make(chan struct{})

			wg.Go(func() {
				defer close(returned)

				other.advance(t, s, 0, 5, nil)
			})
			awaitBefore(t, g.started, returned, "the first commit must sign")

			// The next commit takes both calls of l in one record.
			wg.Go(func() { l.advance(t, s, 0, 5, nil) })
			waitQueue(t, s, 1)
			wg.Go(func() { l.advance(t, s, 5, 6, nil) })
			waitQueue(t, s, 2)
			close(g.release)
			waitAll(t, &wg, "every call must return")

			rec := getRoute(t, s, l.origin)
			assert.Equal(t, rec.Code, http.StatusOK, "the route must serve the origin")
			assert.HasPrefix(t, rec.Body.String(), string(l.note(t, 6)),
				"the route must serve the later call of the record")
		})

		t.Run("adds no origin that left the state during the write of its lines", func(t *testing.T) {
			t.Parallel()
			_, st, s := faultyServer(t)
			h := hashOrigin(l.origin)

			st.put = func(key string) error {
				if strings.HasPrefix(key, linesPrefix) {
					s.mu.Lock()
					s.st.origins.Delete(h)
					s.mu.Unlock()
				}

				return nil
			}

			l.advance(t, s, 0, 5, nil)
			assert.False(t, s.st.origins.Has(h), "the write of the lines must not add the origin again")
		})
	})

	t.Run("release", func(t *testing.T) {
		t.Parallel()

		t.Run("drops the references of the batch to the memory of its calls", func(t *testing.T) {
			t.Parallel()
			p := &pending{}
			b := batch{
				calls:  []*pending{p},
				passed: []*pending{p},
				texts:  [][]byte{[]byte("a text")},
				lines:  lines{Calls: [][]byte{[]byte("a line")}},
				rec:    record{Calls: []call{{Note: []byte("a note")}}},
			}

			b.release()

			expect.Equal(t, b.calls, []*pending{nil}, "release must drop the calls")
			expect.Equal(t, b.passed, []*pending{nil}, "release must drop the passed calls")
			expect.Equal(t, b.texts, [][]byte{nil}, "release must drop the texts")
			expect.Equal(t, b.lines.Calls, [][]byte{nil}, "release must drop the lines")
			expect.Equal(t, b.rec.Calls, []call{{}}, "release must drop the calls of the record")
		})
	})

	t.Run("snapshotIfDue", func(t *testing.T) {
		t.Parallel()

		// idle returns a server without a running commit over a store in
		// which another server committed the first update of l, and a
		// function that commits the next update of l there and makes the
		// state of the idle server catch up and make a snapshot due.
		idle := func(tb testing.TB) (*internalFixture, *Server, func(size uint64)) {
			tb.Helper()

			f := newInternalFixture(tb, l)
			writer := f.server(tb)
			l.advance(tb, writer, 0, 5, nil)
			s := f.server(tb)

			next := func(size uint64) {
				l.advance(tb, writer, size-1, size, nil)
				_, err := s.sync(bounded(tb), "")
				assert.NoError(tb, err, "the idle server must catch up")

				s.mu.Lock()
				s.st.end = s.st.snap.end + snapshotBytes
				s.mu.Unlock()
			}

			s.mu.Lock()
			s.st.end = snapshotBytes
			s.mu.Unlock()

			return f, s, next
		}

		t.Run("writes no snapshot while another snapshot of the server runs", func(t *testing.T) {
			t.Parallel()
			f, s, _ := idle(t)
			s.snapshotting.Store(true)

			s.snapshotIfDue()
			assert.Empty(t, listKeys(t, f.store, snapshotPrefix), "snapshotIfDue must write no snapshot")
		})

		t.Run("writes the snapshot that is due after the snapshot before it", func(t *testing.T) {
			t.Parallel()
			_, s, next := idle(t)

			s.snapshotIfDue()
			first := s.st.snap.name
			assert.NotEqual(t, first, "", "the first snapshot must install")

			next(6)
			s.snapshotIfDue()
			assert.NotEqual(t, s.st.snap.name, first, "the second snapshot must install")
		})
	})

	t.Run("run", func(t *testing.T) {
		t.Parallel()

		errRead := errors.New("the read of the head failed")
		failHead := func(st *faulty) {
			st.get = func(key string) error {
				if key == headKey {
					return errRead
				}

				return nil
			}
		}

		t.Run("fails the calls of a commit of failed calls whose head does not read", func(t *testing.T) {
			t.Parallel()
			_, st, s := faultyServer(t)
			l.advance(t, s, 0, 5, nil)
			failHead(st)

			_, failures, err := s.Advance(bounded(t), l.note(t, 6), []Update{l.update(t, 0, 6, nil)}, nil)
			assert.ErrorIs(t, err, errRead, "Advance must return the error of the read")
			assert.Empty(t, failures, "Advance must return no failure with an error")
		})

		// readHead returns the zero version for a head that it does not
		// read, which is the version of the base of a fresh server.
		t.Run("fails the calls of a first commit of failed calls whose head does not read", func(t *testing.T) {
			t.Parallel()
			_, st, s := faultyServer(t)
			failHead(st)

			_, failures, err := s.Advance(bounded(t), l.note(t, 5), []Update{l.update(t, 3, 5, nil)}, nil)
			assert.ErrorIs(t, err, errRead, "Advance must return the error of the read")
			assert.Empty(t, failures, "Advance must return no failure with an error")
		})

		t.Run("fails the passed calls with the error of a replacement of the head that fails", func(t *testing.T) {
			t.Parallel()
			_, st, s := faultyServer(t)

			var once atomic.Bool

			errWrite := errors.New("the write of the head failed")
			st.put = func(key string) error {
				if key == headKey && once.CompareAndSwap(false, true) {
					return errWrite
				}

				return nil
			}

			_, _, err := s.Advance(bounded(t), l.note(t, 5), []Update{l.update(t, 0, 5, nil)}, nil)
			assert.ErrorIs(t, err, errWrite, "Advance must return the error of the write")
		})

		t.Run("fails the calls of a commit of failed calls whose catch-up fails", func(t *testing.T) {
			t.Parallel()
			f, st, s := faultyServer(t)
			cfg := f.config()
			cfg.State = st
			l.advance(t, newInternalServer(t, cfg), 0, 5, nil)

			errRecord := errors.New("the read of the record failed")
			st.get = func(key string) error {
				if strings.HasPrefix(key, recordPrefix) {
					return errRecord
				}

				return nil
			}

			_, _, err := s.Advance(bounded(t), l.note(t, 6), []Update{l.update(t, 3, 6, nil)}, nil)
			assert.ErrorIs(t, err, errRecord, "Advance must return the error of the catch-up")
		})

		t.Run("counts no conflict of a call whose commit fails", func(t *testing.T) {
			t.Parallel()
			_, st, s := faultyServer(t)
			conflicts := &sumCounter{}
			s.metrics.conflict = conflicts
			failHead(st)

			_, _, err := s.Advance(bounded(t), l.note(t, 5), []Update{l.update(t, 3, 5, nil)}, nil)
			assert.ErrorIs(t, err, errRead, "Advance must return the error of the read")

			s.lmu.Lock()
			n := conflicts.sum.Load()
			s.lmu.Unlock()

			assert.Equal(t, n, int64(0), "the commit must count no conflict")
		})

		// overtaking returns a server over a store with hooks, whose first
		// write of the head lets writer, another server over the same
		// store, commit an update of other first, and then fails with
		// failure, or writes the head for a nil failure. The error of the
		// commit of writer is in its pointer once the call through the
		// server returns.
		overtaking := func(tb testing.TB, failure error) (*internalFixture, *Server, *error) {
			tb.Helper()

			other := newInternalLog(tb, "example.com/b")
			f := newInternalFixture(tb, l, other)
			st := &faulty{Store: f.store}
			cfg := f.config()
			cfg.State = st
			s := newInternalServer(tb, cfg)
			writer := f.server(tb)

			var (
				once     atomic.Bool
				otherErr error
			)

			st.put = func(key string) error {
				if key != headKey || !once.CompareAndSwap(false, true) {
					return nil
				}

				_, _, otherErr = writer.Advance(bounded(tb), other.note(tb, 5), []Update{other.update(tb, 0, 5, nil)},
					nil)

				return failure
			}

			return f, s, &otherErr
		}

		t.Run("records each passed call once after another process replaced the head first", func(t *testing.T) {
			t.Parallel()
			f, s, otherErr := overtaking(t, nil)
			l.advance(t, s, 0, 5, nil)
			assert.NoError(t, *otherErr, "the other process must commit")

			h, _, err := readHead(t.Context(), f.store)
			assert.NoError(t, err, "the head must read")
			rec, _, err := s.readRecord(bounded(t), h.Record)
			assert.NoError(t, err, "the record of the head must read")
			assert.Length(t, rec.Calls, 1, "the record must contain the call once")
		})

		t.Run("commits the call after an unknown outcome of a replacement that another process came before",
			func(t *testing.T) {
				t.Parallel()
				f, s, otherErr := overtaking(t, version.ErrOutcomeUnknown)
				l.advance(t, s, 0, 5, nil)
				assert.NoError(t, *otherErr, "the other process must commit")

				o, ok := f.server(t).st.origins.Get(hashOrigin(l.origin))
				assert.True(t, ok, "the chain of the head must contain the call")
				assert.Equal(t, o.size, uint64(5), "the chain must contain the update of the call")
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

			_, _, err := s.Advance(bounded(t), l.note(t, 5), []Update{l.update(t, 0, 5, nil)}, nil)
			assert.ErrorIs(t, err, errRead, "Advance must return the error of the read")
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

			_, _, err := s.Advance(bounded(t), l.note(t, 5), []Update{l.update(t, 0, 5, nil)}, nil)
			assert.ErrorIs(t, err, errRead, "Advance must return the error of the read")
		})
	})
}
