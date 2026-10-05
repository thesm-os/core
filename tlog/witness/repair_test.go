// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/tlog/checkpoint"
	"go.thesmos.sh/core/tlog/witness"
)

func TestRepair(t *testing.T) {
	t.Parallel()

	t.Run("Checkpoint", func(t *testing.T) {
		t.Parallel()

		l := newTestLog(t, logName)

		// unlined returns a fixture and a server whose first commit of l
		// advanced the origin to 5 without lines, at the time of the clock.
		unlined := func(tb testing.TB) (*fixture, *witness.Server) {
			tb.Helper()

			f := newFixture(tb, l)
			s := newServer(tb, f.config())
			refused := refuseLines(f)

			advance(tb, s, l, l.update(tb, 0, 5))
			waitFor(tb, func() bool { return refused.Load() == 1 }, "the store must refuse the lines")
			f.store.reset()

			return f, s
		}

		t.Run("repairs a record older than the repair age and serves its lines at the time of the record",
			func(t *testing.T) {
				t.Parallel()
				f, s := unlined(t)
				f.clock.Advance(time.Minute)

				rec := get(t, s, l.origin)
				testkit.Equal(t, rec.Code, http.StatusOK, "the route must serve the repaired record")

				lines := rec.Body.Bytes()[len(l.notes[5]):]
				testkit.Equal(t, lineTime(t, l, 5, lines), clockTime, "the repair must sign at the time of the record")
			})

		t.Run("repairs at the time of the record after a later commit of the origin", func(t *testing.T) {
			t.Parallel()
			f, s := unlined(t)
			stale := newServer(t, f.config())

			f.clock.Advance(time.Minute)
			advance(t, s, l, l.update(t, 5, 6))

			rec := get(t, stale, l.origin)
			testkit.Equal(t, rec.Code, http.StatusOK, "the route must serve the repaired record")
			testkit.Equal(t, lineTime(t, l, 5, rec.Body.Bytes()[len(l.notes[5]):]), clockTime,
				"the repair must sign at the time of the record")
		})

		t.Run("repairs a record whose read, write and signatures take nearly their bounds", func(t *testing.T) {
			t.Parallel()

			// The read of the record and the write of the lines take 80% of
			// Timeout, and the signature 80% of SignTimeout. A store whose
			// deadline were Timeout alone would end the repair before its
			// lines.
			var slow atomic.Bool

			f := newFixture(t, l)
			c := &flaky{Cosigner: f.cosigners[0], before: func(context.Context) error {
				if slow.Load() {
					time.Sleep(400 * time.Millisecond)
				}

				return nil
			}}
			f.cosigners = []checkpoint.Cosigner{c}
			cfg := f.config()
			cfg.Timeout, cfg.SignTimeout = 500*time.Millisecond, 500*time.Millisecond
			s := newServer(t, cfg)
			refused := refuseLines(f)

			advance(t, s, l, l.update(t, 0, 5))
			waitFor(t, func() bool { return refused.Load() == 1 }, "the store must refuse the lines")
			f.store.reset()
			f.clock.Advance(time.Minute)

			pause := func(context.Context, string) error {
				time.Sleep(200 * time.Millisecond)

				return nil
			}
			f.store.intercept(hook{op: opGet, prefix: "records/", before: pause})
			f.store.intercept(hook{op: opPut, prefix: "lines/", before: pause})
			slow.Store(true)

			testkit.Equal(t, get(t, s, l.origin).Code, http.StatusOK, "the route must serve the repaired record")
		})

		t.Run("starts one repair for concurrent GETs of one record", func(t *testing.T) {
			t.Parallel()

			var signed atomic.Int64

			f := newFixture(t, l)
			release := make(chan struct{})
			c := &flaky{Cosigner: f.cosigners[0], before: func(context.Context) error {
				if signed.Add(1) > 1 {
					<-release
				}

				return nil
			}}
			f.cosigners = []checkpoint.Cosigner{c}
			s := newServer(t, f.config())
			refused := refuseLines(f)

			advance(t, s, l, l.update(t, 0, 5))
			waitFor(t, func() bool { return refused.Load() == 1 }, "the store must refuse the lines")
			f.store.reset()
			f.clock.Advance(time.Minute)

			var wg sync.WaitGroup

			codes := make([]int, 4)
			for i := range codes {
				wg.Go(func() { codes[i] = get(t, s, l.origin).Code })
			}

			waitFor(t, func() bool { return signed.Load() == 2 }, "the repair must sign")
			time.Sleep(10 * time.Millisecond)
			close(release)
			wg.Wait()

			testkit.Equal(t, signed.Load(), int64(2), "the GETs must start one repair")

			for _, code := range codes {
				testkit.Equal(t, code, http.StatusOK, "every GET must serve the repaired record")
			}
		})

		t.Run("starts no repair of a record younger than the repair age", func(t *testing.T) {
			t.Parallel()
			f, s := unlined(t)
			f.clock.Advance(5 * time.Second)

			testkit.Equal(t, get(t, s, l.origin).Code, http.StatusNotFound, "the route must not repair the record")
			testkit.Len(t, keys(t, f.store, "lines/"), 0, "the route must store no lines")
		})

		t.Run("starts no repair of a record at the repair age while its commit signs", func(t *testing.T) {
			t.Parallel()

			var signed atomic.Int64

			// The cleanup releases the commit when an assertion fails first.
			release := make(chan struct{})
			open := sync.OnceFunc(func() { close(release) })
			t.Cleanup(open)

			f := newFixture(t, l)
			c := &flaky{Cosigner: f.cosigners[0], before: func(context.Context) error {
				if signed.Add(1) == 1 {
					<-release
				}

				return nil
			}}
			f.cosigners = []checkpoint.Cosigner{c}
			s := newServer(t, f.config())

			var (
				lines []byte
				err   error
				wg    sync.WaitGroup
			)

			updates := []witness.Update{l.update(t, 0, 5)}
			wg.Go(func() { lines, _, err = s.Advance(t.Context(), l.notes[5], updates, nil) })
			waitFor(t, func() bool { return signed.Load() == 1 }, "the commit must sign")

			// The repair age: the deadline of a commit, one second for the
			// whole seconds of the time of its record, and twice MaxError
			// between two readings. At that age the commit can still store
			// its lines.
			f.clock.Advance(testTimeout + testSignTimeout + time.Second + 2*testMaxError)

			testkit.Equal(t, get(t, s, l.origin).Code, http.StatusNotFound, "the route must not repair the record")
			testkit.Equal(t, signed.Load(), int64(1), "the route must start no repair")

			open()
			wg.Wait()
			testkit.NoError(t, err, "the commit must succeed")
			waitFor(t, func() bool { return len(keys(t, f.store, "lines/")) == 1 }, "the commit must store its lines")

			rec := get(t, s, l.origin)
			testkit.Equal(t, rec.Code, http.StatusOK, "the route must serve the record of the commit")
			testkit.Equal(t, rec.Body.String(), string(l.notes[5])+string(lines),
				"the route must serve the lines of the commit")
		})

		t.Run("starts no repair without a synchronised reading", func(t *testing.T) {
			t.Parallel()
			f, s := unlined(t)
			f.clock.Advance(time.Minute)
			f.clock.SetUTCError(0, false)

			testkit.Equal(t, get(t, s, l.origin).Code, http.StatusNotFound, "the route must not repair the record")
			testkit.Len(t, keys(t, f.store, "lines/"), 0, "the route must store no lines")
		})

		t.Run("serves the served position when the circuit refuses the repair", func(t *testing.T) {
			t.Parallel()
			f, s := unlined(t)
			f.clock.Advance(time.Minute)

			for range 2 {
				f.breaker.Record(target(f.cosigners[0]), true)
			}

			testkit.Equal(t, get(t, s, l.origin).Code, http.StatusNotFound, "the route must not serve the record")
			testkit.Len(t, keys(t, f.store, "lines/"), 0, "the route must store no lines")
		})

		t.Run("starts no second repair of a record whose repair failed", func(t *testing.T) {
			t.Parallel()

			var signed atomic.Int64

			f := newFixture(t, l)
			c := &flaky{Cosigner: f.cosigners[0], before: func(context.Context) error {
				signed.Add(1)

				return nil
			}}
			f.cosigners = []checkpoint.Cosigner{c}
			s := newServer(t, f.config())
			refused := refuseLines(f)

			advance(t, s, l, l.update(t, 0, 5))
			waitFor(t, func() bool { return refused.Load() == 1 }, "the store must refuse the lines")
			f.clock.Advance(time.Minute)

			for range 3 {
				testkit.Equal(t, get(t, s, l.origin).Code, http.StatusNotFound, "the route must not serve the record")
			}

			testkit.Equal(t, signed.Load(), int64(2), "the GETs must start one repair")
		})

		t.Run("serves the lines that another process stored first", func(t *testing.T) {
			t.Parallel()
			f, s := unlined(t)
			other := newServer(t, f.config())
			f.clock.Advance(time.Minute)

			first := get(t, other, l.origin)
			testkit.Equal(t, first.Code, http.StatusOK, "the other process must repair the record")

			var hidden atomic.Bool

			hidden.Store(true)
			f.store.intercept(hook{op: opStat, prefix: "lines/", before: func(context.Context, string) error {
				if hidden.CompareAndSwap(true, false) {
					return errs.WithClass(errors.New("the lines are not visible yet"), errs.NotFound)
				}

				return nil
			}})

			rec := get(t, s, l.origin)
			testkit.Equal(t, rec.Code, http.StatusOK, "the route must serve the stored lines")
			testkit.Equal(
				t,
				rec.Body.String(),
				first.Body.String(),
				"the route must serve the lines of the first repair",
			)
		})

		t.Run("serves the served position to a GET whose context ends during the repair", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, l)
			release := make(chan struct{})
			c := &flaky{Cosigner: f.cosigners[0], before: func(context.Context) error {
				<-release

				return nil
			}}
			f.cosigners = []checkpoint.Cosigner{c}
			s := newServer(t, f.config())
			refused := refuseLines(f)

			ctx, updates := t.Context(), []witness.Update{l.update(t, 0, 5)}

			go func() { _, _, _ = s.Advance(ctx, l.notes[5], updates, nil) }()

			release <- struct{}{}
			waitFor(t, func() bool { return refused.Load() == 1 }, "the store must refuse the lines")
			f.store.reset()
			f.clock.Advance(time.Minute)

			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer cancel()

			rec := httptest.NewRecorder()
			path := monitoringPath + "/" + originHashText(l.origin) + "/checkpoint"
			s.Checkpoint().ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil))
			close(release)
			testkit.Equal(t, rec.Code, http.StatusNotFound, "the route must serve the served position")
		})
	})
}
