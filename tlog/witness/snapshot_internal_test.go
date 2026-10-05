// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"iter"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go.thesmos.sh/kanon"
	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/blob"
	"go.thesmos.sh/core/blob/memory"
	"go.thesmos.sh/core/page"
	"go.thesmos.sh/core/resilience"
	"go.thesmos.sh/core/tlog/checkpoint"
	"go.thesmos.sh/core/version"
)

// faulty is a blob.Store over blob/memory that refuses the writes of keys
// under refused, fails a read for which get returns an error, a write for
// which put returns one, and a walk for which list returns one, and counts
// the walks of each prefix. A walk for which page returns an error yields
// that error.
type faulty struct {
	*memory.Store

	get      func(key string) error
	put      func(key string) error
	del      func(key string) error
	afterPut func(key string)
	list     func(prefix string) error
	page     func(prefix string) error
	lists    map[string]int
	refused  string
	mu       sync.Mutex
}

// logged is a slog.Handler that keeps the message of every record.
type logged struct {
	messages []string
	mu       sync.Mutex
}

// failingCursor is a page.Cursor whose walk yields err.
type failingCursor struct {
	err error
}

func TestSnapshotInternal(t *testing.T) {
	t.Parallel()

	t.Run("snapshot", func(t *testing.T) {
		t.Parallel()

		t.Run("installs a first snapshot whose positions are records", func(t *testing.T) {
			t.Parallel()
			a, b := newInternalLog(t, "example.com/a"), newInternalLog(t, "example.com/b")
			f := newInternalFixture(t, a, b)
			s := f.server(t)
			a.advance(t, s, 0, 5, nil)
			b.advance(t, s, 0, 5, nil)

			testkit.NoError(t, s.snapshot(t.Context()), "snapshot must install")

			snap := installedSnapshot(t, f.store)
			testkit.Equal(t, snap.Base, "", "the first snapshot must have no base")
			testkit.Len(t, snap.Origins, 2, "the snapshot must contain both origins")

			for _, obj := range snap.Objects {
				testkit.True(t, strings.HasPrefix(obj.Key, recordPrefix), "every position must be a record")
			}
		})

		t.Run("returns nil without a record after the installed snapshot", func(t *testing.T) {
			t.Parallel()
			a := newInternalLog(t, "example.com/a")
			f := newInternalFixture(t, a)
			s := f.server(t)
			a.advance(t, s, 0, 5, nil)
			testkit.NoError(t, s.snapshot(t.Context()), "snapshot must install")

			h, _, err := readHead(t.Context(), f.store)
			testkit.NoError(t, err, "the head must read")
			testkit.NoError(t, s.snapshot(t.Context()), "a second snapshot must do nothing")

			again, _, err := readHead(t.Context(), f.store)
			testkit.NoError(t, err, "the head must read")
			testkit.Equal(t, again, h, "the head must not change")
		})

		t.Run("moves the update of an idle origin into a group that the route serves", func(t *testing.T) {
			t.Parallel()
			a, b := newInternalLog(t, "example.com/a"), newInternalLog(t, "example.com/b")
			f := newInternalFixture(t, a, b)
			s := f.server(t)
			lines := a.advance(t, s, 0, 5, []byte("a prefix\n"))
			b.advance(t, s, 0, 5, nil)
			testkit.NoError(t, s.snapshot(t.Context()), "the first snapshot must install")

			b.advance(t, s, 5, 6, nil)
			testkit.NoError(t, s.snapshot(t.Context()), "the second snapshot must install")

			snap := installedSnapshot(t, f.store)
			groups := 0

			for _, obj := range snap.Objects {
				if strings.HasPrefix(obj.Key, groupPrefix) {
					groups++
					testkit.Equal(t, obj.Updates, uint32(1), "the group must contain the update of a")
				}
			}

			testkit.Equal(t, groups, 1, "the snapshot must refer to one group")

			again := f.server(t)
			rec := getRoute(t, again, a.origin)
			testkit.Equal(t, rec.Code, http.StatusOK, "the route must serve the group")
			testkit.Equal(t, rec.Body.String(), "a prefix\n"+string(a.note(t, 5))+string(lines),
				"the route must serve the prefix, the note and the lines of the group")
		})

		t.Run("compacts a group in which fewer than half of the updates are current", func(t *testing.T) {
			t.Parallel()
			logs := make([]*internalLog, 5)
			for i := range logs {
				logs[i] = newInternalLog(t, "example.com/log/"+strconv.Itoa(i))
			}

			f := newInternalFixture(t, logs...)
			s := f.server(t)

			for _, l := range logs[:4] {
				l.advance(t, s, 0, 5, nil)
			}

			logs[4].advance(t, s, 0, 5, nil)
			testkit.NoError(t, s.snapshot(t.Context()), "the first snapshot must install")
			logs[4].advance(t, s, 5, 6, nil)
			testkit.NoError(t, s.snapshot(t.Context()), "the second snapshot must install")

			first := groupKeys(installedSnapshot(t, f.store))
			testkit.Len(t, first, 1, "the four idle origins must share a group")

			for _, l := range logs[:3] {
				l.advance(t, s, 5, 6, nil)
			}

			logs[4].advance(t, s, 6, 7, nil)
			testkit.NoError(t, s.snapshot(t.Context()), "the third snapshot must install")

			third := groupKeys(installedSnapshot(t, f.store))
			for key := range first {
				testkit.False(t, third[key], "the snapshot must compact the group")
			}
		})

		t.Run("collects only what neither of the two newest snapshots refers to", func(t *testing.T) {
			t.Parallel()
			a, b := newInternalLog(t, "example.com/a"), newInternalLog(t, "example.com/b")
			f := newInternalFixture(t, a, b)
			s := f.server(t)
			a.advance(t, s, 0, 5, nil)

			for size := uint64(1); size <= 4; size++ {
				b.advance(t, s, size-1, size, nil)
				testkit.NoError(t, s.snapshot(t.Context()), "the snapshot must install")
			}

			snap := installedSnapshot(t, f.store)
			keep := map[string]bool{}

			for _, obj := range snap.Objects {
				keep[obj.Key] = true
			}

			base := readSnapshotNamed(t, f.store, snap.Base)
			for _, obj := range base.Objects {
				keep[obj.Key] = true
			}

			for _, key := range listKeys(t, f.store, recordPrefix) {
				seq, _ := parseName(key[len(recordPrefix):])
				baseSeq, _ := parseName(base.Record)
				testkit.True(t, keep[key] || seq >= baseSeq, "the store must keep only the records that it needs: "+key)
			}

			testkit.Len(t, listKeys(t, f.store, snapshotPrefix), 2, "the store must keep the two newest snapshots")
		})

		t.Run("deletes a record off the chain", func(t *testing.T) {
			t.Parallel()
			a := newInternalLog(t, "example.com/a")
			f := newInternalFixture(t, a)
			s := f.server(t)
			a.advance(t, s, 0, 5, nil)

			orphan := recordPrefix + string(appendName(nil, 2, []byte("an orphan")))
			_, err := blob.PutBytes(t.Context(), f.store, orphan, []byte("an orphan"), blob.PutOptions{})
			testkit.NoError(t, err, "the orphan must store")

			a.advance(t, s, 5, 6, nil)
			a.advance(t, s, 6, 7, nil)
			testkit.NoError(t, s.snapshot(t.Context()), "the snapshot must install")

			_, err = f.store.Stat(t.Context(), orphan)
			testkit.Error(t, err, "the snapshot must collect the record off the chain")
		})

		t.Run("leaves the update of a record whose repair the circuit refuses on the record", func(t *testing.T) {
			t.Parallel()
			a, b := newInternalLog(t, "example.com/a"), newInternalLog(t, "example.com/b")
			f := newInternalFixture(t, a, b)
			st := &faulty{Store: f.store}
			cfg := f.config()
			cfg.State = st
			s := newInternalServer(t, cfg)

			st.refuse(linesPrefix)
			a.advance(t, s, 0, 5, nil)
			st.refuse("")

			b.advance(t, s, 0, 5, nil)
			testkit.NoError(t, s.snapshot(t.Context()), "the first snapshot must install")

			f.clock.Advance(time.Minute)
			b.advance(t, s, 5, 6, nil)

			for range 2 {
				f.breaker.Record(s.targets[0], true)
			}

			testkit.Equal(t, f.breaker.State(s.targets[0]), resilience.Open, "the circuit must be open")
			testkit.NoError(t, s.snapshot(t.Context()), "the snapshot must install")

			o, _ := s.st.origins.Get(hashOrigin(a.origin))
			testkit.False(t, o.latest.group, "the update of a must remain on its record")

			f.clock.Advance(time.Minute)
			b.advance(t, s, 6, 7, nil)
			testkit.NoError(t, s.snapshot(t.Context()), "the next snapshot must install")

			o, _ = s.st.origins.Get(hashOrigin(a.origin))
			testkit.True(t, o.latest.group, "the next snapshot must move the update of a")
		})

		t.Run("repairs one of two records without lines", func(t *testing.T) {
			t.Parallel()
			a, b, c := newInternalLog(t, "example.com/a"), newInternalLog(t, "example.com/b"),
				newInternalLog(t, "example.com/c")
			f := newInternalFixture(t, a, b, c)
			st := &faulty{Store: f.store}
			cfg := f.config()
			cfg.State = st
			s := newInternalServer(t, cfg)

			st.refuse(linesPrefix)
			for _, l := range []*internalLog{a, b} {
				l.advance(t, s, 0, 5, nil)
			}

			st.refuse("")
			c.advance(t, s, 0, 5, nil)
			testkit.NoError(t, s.snapshot(t.Context()), "the first snapshot must install")

			f.clock.Advance(time.Minute)
			c.advance(t, s, 5, 6, nil)
			testkit.NoError(t, s.snapshot(t.Context()), "the second snapshot must install")

			moved := 0

			for _, l := range []*internalLog{a, b} {
				if o, _ := s.st.origins.Get(hashOrigin(l.origin)); o.latest.group {
					moved++
				}
			}

			testkit.Equal(t, moved, 1, "the snapshot must repair one record and move its update")
		})

		t.Run("repairs nothing without a synchronised reading", func(t *testing.T) {
			t.Parallel()
			a, b := newInternalLog(t, "example.com/a"), newInternalLog(t, "example.com/b")
			f := newInternalFixture(t, a, b)
			st := &faulty{Store: f.store}
			cfg := f.config()
			cfg.State = st
			s := newInternalServer(t, cfg)

			st.refuse(linesPrefix)
			a.advance(t, s, 0, 5, nil)
			st.refuse("")

			b.advance(t, s, 0, 5, nil)
			testkit.NoError(t, s.snapshot(t.Context()), "the first snapshot must install")
			f.clock.Advance(time.Minute)
			b.advance(t, s, 5, 6, nil)

			f.clock.SetUTCError(0, false)
			testkit.NoError(t, s.snapshot(t.Context()), "the snapshot must install")

			o, _ := s.st.origins.Get(hashOrigin(a.origin))
			testkit.False(t, o.latest.group, "the snapshot must not repair the record")
		})

		t.Run("abandons a snapshot when another process installs one first", func(t *testing.T) {
			t.Parallel()
			a, b := newInternalLog(t, "example.com/a"), newInternalLog(t, "example.com/b")
			f := newInternalFixture(t, a, b)
			s := f.server(t)
			a.advance(t, s, 0, 5, nil)
			b.advance(t, s, 0, 5, nil)

			other := f.server(t)
			testkit.NoError(t, other.snapshot(t.Context()), "the other process must install")

			installed := installedSnapshot(t, f.store)
			testkit.NoError(t, s.snapshot(t.Context()), "the snapshot must be abandoned without an error")
			testkit.Equal(t, installedSnapshot(t, f.store).Record, installed.Record,
				"the snapshot of the other process must remain installed")
		})

		t.Run("retires an origin that Logs refuses after Retention", func(t *testing.T) {
			t.Parallel()
			a, b := newInternalLog(t, "example.com/a"), newInternalLog(t, "example.com/b")
			f := newInternalFixture(t, a, b)
			s := f.server(t)
			a.advance(t, s, 0, 5, nil)

			f.refuse(a.origin)
			f.clock.Advance(2 * time.Hour)
			b.advance(t, s, 0, 5, nil)
			testkit.NoError(t, s.snapshot(t.Context()), "the snapshot must install")

			testkit.Len(t, listKeys(t, f.store, retiredPrefix), 1, "the snapshot must create the retired object")
			testkit.Equal(t, getRoute(t, s, a.origin).Code, http.StatusNotFound, "the route must refuse the origin")

			f.mu.Lock()
			f.accepted[a.origin] = a.log
			f.mu.Unlock()

			for _, server := range []*Server{s, f.server(t)} {
				_, failures, err := server.Advance(t.Context(), a.note(t, 6), []Update{a.update(t, 0, 6, nil)}, nil)
				testkit.NoError(t, err, "Advance must check the update")
				testkit.Len(t, failures, 1, "Advance must refuse the old size 0")

				se, ok := errors.AsType[*SizeError](failures[0].Err)
				testkit.True(t, ok, "the failure must be a SizeError")
				testkit.Equal(t, se.Size, uint64(5), "the SizeError must contain the retired size")
			}
		})

		t.Run("returns the retired size to a call that waits while another process retires its origin",
			func(t *testing.T) {
				t.Parallel()
				a, b := newInternalLog(t, "example.com/a"), newInternalLog(t, "example.com/b")
				f := newInternalFixture(t, a, b)

				// The first commit of p signs through a gated cosigner, so the
				// call of a waits in the queue of p, whose state never contains
				// a, while another process commits a and retires it. The cleanup
				// releases the commit when an assertion fails first.
				g := &gated{Cosigner: f.signer, started: make(chan struct{}), release: make(chan struct{})}
				open := sync.OnceFunc(func() { close(g.release) })
				t.Cleanup(open)

				cfg := f.config()
				cfg.Cosigners = []checkpoint.Cosigner{g}
				p := newInternalServer(t, cfg)

				var (
					failures   []Failure
					berr, aerr error
					wg         sync.WaitGroup
				)

				bNote, bUpdates := b.note(t, 5), []Update{b.update(t, 0, 5, nil)}
				aNote, aUpdates := a.note(t, 5), []Update{a.update(t, 0, 5, nil)}
				first := make(chan struct{})

				wg.Go(func() {
					defer close(first)

					_, _, berr = p.Advance(t.Context(), bNote, bUpdates, nil)
				})
				awaitBefore(t, g.started, first, "the commit of b must sign")

				wg.Go(func() { _, failures, aerr = p.Advance(t.Context(), aNote, aUpdates, nil) })
				waitQueue(t, p, 1)

				other := f.server(t)
				a.advance(t, other, 0, 5, nil)
				f.refuse(a.origin)
				f.clock.Advance(2 * time.Hour)
				b.advance(t, other, 5, 6, nil)
				testkit.NoError(t, other.snapshot(t.Context()), "the snapshot must install")
				testkit.Len(t, listKeys(t, f.store, retiredPrefix), 1, "the snapshot must retire a")

				open()
				waitAll(t, &wg, "both calls must return")

				testkit.NoError(t, berr, "the commit of b must succeed")
				testkit.NoError(t, aerr, "Advance must check the update of a")
				testkit.Len(t, failures, 1, "Advance must refuse the old size 0")

				se, ok := errors.AsType[*SizeError](failures[0].Err)
				testkit.True(t, ok, "the failure must be a SizeError")
				testkit.Equal(t, se.Size, uint64(5), "the SizeError must contain the retired size")
			})

		t.Run("lists no retired object for an Advance of 4,096 new origins", func(t *testing.T) {
			t.Parallel()
			batch := newInternalLog(t, "example.com/batch")
			f := newInternalFixture(t)
			st := &faulty{Store: f.store}
			cfg := f.config()
			cfg.State = st
			s := newInternalServer(t, cfg)

			updates := make([]Update, maxUpdates)
			for i := range updates {
				origin := checkpoint.Origin("example.com/log/" + strconv.Itoa(i))
				f.accepted[origin] = batch.log
				updates[i] = Update{
					Body: checkpoint.Body{Origin: origin, Size: 5, Root: batch.update(t, 0, 5, nil).Body.Root},
				}
			}

			_, failures, err := s.Advance(t.Context(), batch.note(t, 5), updates, nil)
			testkit.NoError(t, err, "Advance must commit the updates")
			testkit.Len(t, failures, 0, "Advance must return no failure")
			testkit.Equal(t, st.walks(retiredPrefix), 0, "the commit must not list the retired objects")
		})

		t.Run("keeps at least half of the updates of every group current over many snapshots", func(t *testing.T) {
			t.Parallel()
			batch := newInternalLog(t, "example.com/batch")
			f := newInternalFixture(t)
			s := f.server(t)
			r := rand.New(rand.NewPCG(1, 2))

			const origins = 10000

			sizes := make([]uint64, origins)
			root := batch.update(t, 0, 5, nil).Body.Root

			for i := range origins {
				f.accepted[checkpoint.Origin("example.com/log/"+strconv.Itoa(i))] = batch.log
			}

			msg := batch.note(t, 5)

			for range 12 {
				updates := make([]Update, 0, maxUpdates)
				seen := map[int]bool{}

				for len(updates) < 2000 {
					i := r.IntN(origins)
					if seen[i] {
						continue
					}

					seen[i] = true
					updates = append(updates, Update{
						Body: checkpoint.Body{
							Origin: checkpoint.Origin("example.com/log/" + strconv.Itoa(i)),
							Size:   5,
							Root:   root,
						},
						OldSize: sizes[i],
					})
					sizes[i] = 5
				}

				_, failures, err := s.Advance(t.Context(), msg, updates, nil)
				testkit.NoError(t, err, "Advance must commit the updates")
				testkit.Len(t, failures, 0, "Advance must return no failure")
				testkit.NoError(t, s.snapshot(t.Context()), "the snapshot must install")

				snap := installedSnapshot(t, f.store)
				current := map[uint32]uint32{}

				for _, o := range snap.Origins {
					current[o.Object]++
				}

				for i, obj := range snap.Objects {
					if obj.Updates > 0 {
						testkit.True(t, 2*current[uint32(i)] >= obj.Updates, //nolint:gosec // an index
							"at least half of the updates of "+obj.Key+" must be current")
					}
				}
			}
		})

		a, b := newInternalLog(t, "example.com/a"), newInternalLog(t, "example.com/b")

		// idle returns a fixture, a store with hooks and a server whose next
		// snapshot moves the update of a into a group.
		idle := func(tb testing.TB) (*internalFixture, *faulty, *Server) {
			tb.Helper()

			f := newInternalFixture(tb, a, b)
			st := &faulty{Store: f.store}
			cfg := f.config()
			cfg.State = st
			s := newInternalServer(tb, cfg)

			a.advance(tb, s, 0, 5, nil)
			b.advance(tb, s, 0, 5, nil)
			testkit.NoError(tb, s.snapshot(tb.Context()), "the first snapshot must install")
			b.advance(tb, s, 5, 6, nil)

			return f, st, s
		}

		refusals := []struct {
			name   string
			prefix string
		}{
			{name: "returns the error of the store for a group that it does not create", prefix: groupPrefix},
			{name: "returns the error of the store for a snapshot that it does not create", prefix: snapshotPrefix},
		}
		for _, tt := range refusals {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, st, s := idle(t)
				st.refuse(tt.prefix)
				testkit.Error(t, s.snapshot(t.Context()), "the snapshot must fail")
			})
		}

		t.Run("returns the error of the store for a group that it creates between two calls", func(t *testing.T) {
			t.Parallel()
			c := newInternalLog(t, "example.com/c")
			f := newInternalFixture(t, a, b, c)
			st := &faulty{Store: f.store}
			cfg := f.config()
			cfg.State = st
			s := newInternalServer(t, cfg)

			for _, l := range []*internalLog{a, c} {
				l.advance(t, s, 0, 5, make([]byte, 600<<10))
			}

			b.advance(t, s, 0, 5, nil)
			testkit.NoError(t, s.snapshot(t.Context()), "the first snapshot must install")
			b.advance(t, s, 5, 6, nil)

			st.refuse(groupPrefix)
			testkit.Error(t, s.snapshot(t.Context()), "the snapshot must fail")
		})

		t.Run("returns the error of the store for a retired object that it does not create", func(t *testing.T) {
			t.Parallel()
			f, st, s := idle(t)
			f.refuse(a.origin)
			f.clock.Advance(2 * time.Hour)
			b.advance(t, s, 6, 7, nil)
			st.refuse(retiredPrefix)
			testkit.Error(t, s.snapshot(t.Context()), "the snapshot must fail")
		})

		t.Run("returns the error of the store for the record of a move that it does not read", func(t *testing.T) {
			t.Parallel()
			_, st, s := idle(t)
			st.get = func(key string) error {
				if strings.HasPrefix(key, recordPrefix) {
					return errors.New("the read failed")
				}

				return nil
			}

			testkit.Error(t, s.snapshot(t.Context()), "the snapshot must fail")
		})

		positions := []struct {
			edit func(p *position)
			name string
		}{
			{
				name: "returns ErrJournal for a position of a call that the record does not have",
				edit: func(p *position) { p.call = 5 },
			},
			{
				name: "returns ErrJournal for a position of an update that the call does not have",
				edit: func(p *position) { p.update = 5 },
			},
		}
		for _, tt := range positions {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, _, s := idle(t)
				h := hashOrigin(a.origin)
				o, _ := s.st.origins.Get(h)
				tt.edit(&o.latest)
				s.st.origins.Set(h, o)

				testkit.ErrorIs(t, s.snapshot(t.Context()), ErrJournal, "the snapshot must refuse the position")
			})
		}

		t.Run("returns the cause of ctx while another repair of the record runs", func(t *testing.T) {
			t.Parallel()
			f, st, s := idle(t)
			st.refuse(linesPrefix)
			a.advance(t, s, 5, 6, nil)
			st.refuse("")
			b.advance(t, s, 6, 7, nil)
			testkit.NoError(t, s.snapshot(t.Context()), "the second snapshot must install")
			b.advance(t, s, 7, 8, nil)
			f.clock.Advance(time.Minute)

			o, _ := s.st.origins.Get(hashOrigin(a.origin))
			s.repairs[o.latest.key] = &repairing{done: make(chan struct{})}

			ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
			defer cancel()

			testkit.ErrorIs(t, s.snapshot(ctx), context.DeadlineExceeded, "the snapshot must end with ctx")
		})

		t.Run("returns the error of the store for a head that the install does not read", func(t *testing.T) {
			t.Parallel()
			_, st, s := idle(t)
			st.get = func(key string) error {
				if key == headKey {
					return errors.New("the read failed")
				}

				return nil
			}

			testkit.Error(t, s.snapshot(t.Context()), "the snapshot must fail")
		})

		t.Run("returns the error of the store for a head that the install does not write", func(t *testing.T) {
			t.Parallel()
			_, st, s := idle(t)
			st.put = func(key string) error {
				if key == headKey {
					return errors.New("the write failed")
				}

				return nil
			}

			testkit.Error(t, s.snapshot(t.Context()), "the snapshot must fail")
		})

		t.Run("returns ErrContention after 16 tries of the install", func(t *testing.T) {
			t.Parallel()
			_, st, s := idle(t)
			st.put = func(key string) error {
				if key == headKey {
					return version.ErrMismatch
				}

				return nil
			}

			testkit.ErrorIs(t, s.snapshot(t.Context()), ErrContention, "the snapshot must give up")
		})

		t.Run("returns the error of the store for a walk of the garbage that fails", func(t *testing.T) {
			t.Parallel()
			_, st, s := idle(t)
			st.list = func(prefix string) error {
				if prefix == recordPrefix {
					return errors.New("the walk failed")
				}

				return nil
			}

			testkit.Error(t, s.snapshot(t.Context()), "the snapshot must fail")
		})

		t.Run("returns the error of the store for garbage that it does not delete", func(t *testing.T) {
			t.Parallel()
			_, st, s := idle(t)
			b.advance(t, s, 6, 7, nil)
			testkit.NoError(t, s.snapshot(t.Context()), "the second snapshot must install")
			b.advance(t, s, 7, 8, nil)
			st.del = func(string) error { return errors.New("the delete failed") }

			testkit.Error(t, s.snapshot(t.Context()), "the snapshot must fail")
		})

		t.Run("logs a snapshot that fails", func(t *testing.T) {
			t.Parallel()
			f := newInternalFixture(t, a)
			st := &faulty{Store: f.store}
			h := &logged{}
			cfg := f.config()
			cfg.State, cfg.Logger = st, slog.New(h)
			s := newInternalServer(t, cfg)
			a.advance(t, s, 0, 5, nil)
			st.refuse(snapshotPrefix)

			s.writeSnapshot()
			testkit.True(t, h.has("witness: a snapshot failed"), "the snapshot must log its failure")
		})

		t.Run("logs a state above 90% of MaxOrigins", func(t *testing.T) {
			t.Parallel()
			f := newInternalFixture(t, a)
			h := &logged{}
			cfg := f.config()
			cfg.MaxOrigins, cfg.Logger = 1, slog.New(h)
			s := newInternalServer(t, cfg)
			a.advance(t, s, 0, 5, nil)

			testkit.NoError(t, s.snapshot(t.Context()), "the snapshot must install")
			testkit.True(t, h.has("witness: the state is above 90% of MaxOrigins"), "the snapshot must warn")
		})
	})

	t.Run("snapshotIfDue", func(t *testing.T) {
		t.Parallel()

		t.Run("writes a snapshot after 64 MiB of records", func(t *testing.T) {
			t.Parallel()
			a := newInternalLog(t, "example.com/a")
			f := newInternalFixture(t, a)
			s := f.server(t)

			for size := uint64(1); size <= 5; size++ {
				a.advance(t, s, size-1, size, make([]byte, 14<<20))
			}

			deadline := time.Now().Add(10 * time.Second)

			for {
				h, _, err := readHead(t.Context(), f.store)
				testkit.NoError(t, err, "the head must read")

				if h.Snapshot != "" {
					break
				}

				if time.Now().After(deadline) {
					t.Fatal("the commit must write a snapshot")
				}

				time.Sleep(time.Millisecond)
			}
		})
	})
}

// Put refuses a key under f.refused, fails with the error of f.put, and
// stores any other, after which it calls f.afterPut.
func (f *faulty) Put(ctx context.Context, key string, r io.Reader, opts blob.PutOptions) (blob.Info, error) {
	f.mu.Lock()
	refused, put, after := f.refused, f.put, f.afterPut
	f.mu.Unlock()

	if refused != "" && strings.HasPrefix(key, refused) {
		return blob.Info{}, errors.New("the store refuses the write")
	}

	if put != nil {
		if err := put(key); err != nil {
			return blob.Info{}, err
		}
	}

	info, err := f.Store.Put(ctx, key, r, opts)
	if err == nil && after != nil {
		after(key)
	}

	return info, err
}

// Get fails with the error of f.get for key, and reads the object
// otherwise.
func (f *faulty) Get(ctx context.Context, key string) (io.ReadCloser, blob.Info, error) {
	f.mu.Lock()
	get := f.get
	f.mu.Unlock()

	if get != nil {
		if err := get(key); err != nil {
			return nil, blob.Info{}, err
		}
	}

	return f.Store.Get(ctx, key)
}

// List counts the walk of prefix, fails with the error of f.list, returns a
// cursor that yields the error of f.page, and lists the objects otherwise.
func (f *faulty) List(ctx context.Context, prefix string, p page.Page) (page.Cursor[blob.Info], error) {
	f.mu.Lock()
	if f.lists == nil {
		f.lists = map[string]int{}
	}

	f.lists[prefix]++
	list, pg := f.list, f.page
	f.mu.Unlock()

	if list != nil {
		if err := list(prefix); err != nil {
			return nil, err
		}
	}

	if pg != nil {
		if err := pg(prefix); err != nil {
			return failingCursor{err: err}, nil //nolint:nilerr // the walk yields the error, and List succeeds
		}
	}

	return f.Store.List(ctx, prefix, p)
}

// Delete fails with the error of f.del, and deletes the object otherwise.
func (f *faulty) Delete(ctx context.Context, key string, ifMatch version.Version) error {
	f.mu.Lock()
	del := f.del
	f.mu.Unlock()

	if del != nil {
		if err := del(key); err != nil {
			return err
		}
	}

	return f.Store.Delete(ctx, key, ifMatch)
}

// Enabled reports true for every level.
func (*logged) Enabled(context.Context, slog.Level) bool {
	return true
}

// Handle keeps the message of r.
func (h *logged) Handle(_ context.Context, r slog.Record) error { //nolint:gocritic // the signature of slog.Handler
	h.mu.Lock()
	defer h.mu.Unlock()

	h.messages = append(h.messages, r.Message)

	return nil
}

// WithAttrs returns h.
func (h *logged) WithAttrs([]slog.Attr) slog.Handler {
	return h
}

// WithGroup returns h.
func (h *logged) WithGroup(string) slog.Handler {
	return h
}

// has reports whether h kept message.
func (h *logged) has(message string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	return slices.Contains(h.messages, message)
}

// Seq yields the error of c.
func (c failingCursor) Seq(context.Context) iter.Seq2[blob.Info, error] {
	return func(yield func(blob.Info, error) bool) { yield(blob.Info{}, c.err) }
}

// NextPage returns no token.
func (failingCursor) NextPage() string {
	return ""
}

// Close returns nil.
func (failingCursor) Close() error {
	return nil
}

// refuse makes f refuse the writes of keys under prefix, and the empty
// prefix refuses nothing.
func (f *faulty) refuse(prefix string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.refused = prefix
}

// walks returns the number of walks of the prefixes that start with
// prefix.
func (f *faulty) walks(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()

	n := 0

	for p, count := range f.lists {
		if strings.HasPrefix(p, prefix) {
			n += count
		}
	}

	return n
}

// newInternalServer returns a server of cfg, and fails the test when
// NewServer refuses it.
func newInternalServer(tb testing.TB, cfg *ServerConfig) *Server {
	tb.Helper()

	s, err := NewServer(tb.Context(), cfg)
	testkit.NoError(tb, err, "NewServer must accept the configuration")

	return s
}

// installedSnapshot returns the snapshot that the head of st names.
func installedSnapshot(tb testing.TB, st blob.Store) *snapshot {
	tb.Helper()

	h, _, err := readHead(tb.Context(), st)
	testkit.NoError(tb, err, "the head must read")

	return readSnapshotNamed(tb, st, h.Snapshot)
}

// readSnapshotNamed returns the snapshot of st named name.
func readSnapshotNamed(tb testing.TB, st blob.Store, name string) *snapshot {
	tb.Helper()

	data, err := readNamed(tb.Context(), st, snapshotPrefix, name, maxObjectBytes)
	testkit.NoError(tb, err, "the snapshot must read")

	snap := new(snapshot)
	testkit.NoError(tb, snap.DecodeKanon(data, kanon.Options{}), "the snapshot must decode")

	return snap
}

// groupKeys returns the keys of the groups that snap refers to.
func groupKeys(snap *snapshot) map[string]bool {
	out := map[string]bool{}

	for _, obj := range snap.Objects {
		if obj.Updates > 0 {
			out[obj.Key] = true
		}
	}

	return out
}

// listKeys returns the keys of the objects of st under prefix.
func listKeys(tb testing.TB, st blob.Store, prefix string) []string {
	tb.Helper()

	var out []string

	for token := ""; ; {
		cur, err := st.List(tb.Context(), prefix, page.Page{Token: token})
		testkit.NoError(tb, err, "List must list "+prefix)

		for info, err := range cur.Seq(tb.Context()) {
			testkit.NoError(tb, err, "the walk must not fail")
			out = append(out, info.Key)
		}

		if token = cur.NextPage(); token == "" {
			return out
		}
	}
}

// getRoute sends a GET of the monitor retrieval route of origin to s.
func getRoute(tb testing.TB, s *Server, origin checkpoint.Origin) *httptest.ResponseRecorder {
	tb.Helper()

	h := hashOrigin(origin)
	path := "/m/" + hex.EncodeToString(h[:]) + "/checkpoint"

	rec := httptest.NewRecorder()
	s.Checkpoint().ServeHTTP(rec, httptest.NewRequestWithContext(tb.Context(), http.MethodGet, path, nil))

	return rec
}
