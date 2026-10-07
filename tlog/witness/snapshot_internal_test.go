// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"iter"
	"log/slog"
	"maps"
	"math"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.thesmos.sh/kanon"

	"go.thesmos.sh/core/blob"
	"go.thesmos.sh/core/blob/memory"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/page"
	"go.thesmos.sh/core/resilience"
	"go.thesmos.sh/core/tlog/checkpoint"
	"go.thesmos.sh/core/version"
)

// faulty is a blob.Store over blob/memory that refuses the writes of keys
// under refused, fails a read for which get returns an error, a Stat for
// which stat returns one, a write for which put returns one, and a walk
// for which list returns one, and counts the walks of each prefix. A walk
// for which page returns an error yields that error, and a walk of a
// reversed store yields each page in the reverse order of its keys.
type faulty struct {
	*memory.Store

	get      func(key string) error
	stat     func(key string) error
	put      func(key string) error
	del      func(key string) error
	afterPut func(key string)
	list     func(prefix string) error
	page     func(prefix string) error
	lists    map[string]int
	refused  string
	reversed bool
	mu       sync.Mutex
}

// listedCursor is a page.Cursor over the objects of one page that a walk
// read, and the token of the next page.
type listedCursor struct {
	infos []blob.Info
	next  string
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

			assert.NoError(t, s.snapshot(bounded(t)), "snapshot must install")

			snap := installedSnapshot(t, f.store)
			assert.Equal(t, snap.Base, "", "the first snapshot must have no base")
			assert.Length(t, snap.Origins, 2, "the snapshot must contain both origins")

			for _, obj := range snap.Objects {
				assert.HasPrefix(t, obj.Key, recordPrefix, "every position must be a record")
			}
		})

		t.Run("creates no group for a snapshot that moves no update", func(t *testing.T) {
			t.Parallel()
			a := newInternalLog(t, "example.com/a")
			f := newInternalFixture(t, a)
			s := f.server(t)
			a.advance(t, s, 0, 5, nil)

			assert.NoError(t, s.snapshot(bounded(t)), "snapshot must install")
			assert.Empty(t, listKeys(t, f.store, groupPrefix), "the snapshot must create no group")
		})

		t.Run("returns nil without a record after the installed snapshot", func(t *testing.T) {
			t.Parallel()
			a := newInternalLog(t, "example.com/a")
			f := newInternalFixture(t, a)
			s := f.server(t)
			a.advance(t, s, 0, 5, nil)
			assert.NoError(t, s.snapshot(bounded(t)), "snapshot must install")

			var err error
			assert.Pure(t, func() head {
				h, _, readErr := readHead(t.Context(), f.store)
				assert.NoError(t, readErr, "the head must read")

				return h
			}, func() { err = s.snapshot(bounded(t)) }, "the head must not change")
			assert.NoError(t, err, "a second snapshot must do nothing")
		})

		t.Run("moves the update of an idle origin into a group that the route serves", func(t *testing.T) {
			t.Parallel()
			a, b := newInternalLog(t, "example.com/a"), newInternalLog(t, "example.com/b")
			f := newInternalFixture(t, a, b)
			s := f.server(t)
			lines := a.advance(t, s, 0, 5, []byte("a prefix\n"))
			b.advance(t, s, 0, 5, nil)
			assert.NoError(t, s.snapshot(bounded(t)), "the first snapshot must install")

			b.advance(t, s, 5, 6, nil)
			assert.NoError(t, s.snapshot(bounded(t)), "the second snapshot must install")

			snap := installedSnapshot(t, f.store)
			groups := 0

			for _, obj := range snap.Objects {
				if strings.HasPrefix(obj.Key, groupPrefix) {
					groups++
					assert.Equal(t, obj.Updates, uint32(1), "the group must contain the update of a")
				}
			}

			assert.Equal(t, groups, 1, "the snapshot must refer to one group")

			again := f.server(t)
			rec := getRoute(t, again, a.origin)
			assert.Equal(t, rec.Code, http.StatusOK, "the route must serve the group")
			assert.Equal(t, rec.Body.String(), "a prefix\n"+string(a.note(t, 5))+string(lines),
				"the route must serve the prefix, the note and the lines of the group")
		})

		// grouped returns a fixture, a server and five logs at the size 5,
		// whose second snapshot moved the updates of the first four into one
		// group, and the key of that group.
		grouped := func(tb testing.TB) (*internalFixture, *Server, []*internalLog, string) {
			tb.Helper()

			logs := make([]*internalLog, 5)
			for i := range logs {
				logs[i] = newInternalLog(tb, "example.com/log/"+strconv.Itoa(i))
			}

			f := newInternalFixture(tb, logs...)
			s := f.server(tb)

			for _, l := range logs {
				l.advance(tb, s, 0, 5, nil)
			}

			assert.NoError(tb, s.snapshot(bounded(tb)), "the first snapshot must install")
			logs[4].advance(tb, s, 5, 6, nil)
			assert.NoError(tb, s.snapshot(bounded(tb)), "the second snapshot must install")

			groups := slices.Collect(maps.Keys(groupKeys(installedSnapshot(tb, f.store))))
			assert.Length(tb, groups, 1, "the four idle origins must share a group")

			return f, s, logs, groups[0]
		}

		t.Run("compacts a group in which fewer than half of the updates are current", func(t *testing.T) {
			t.Parallel()
			f, s, logs, group := grouped(t)

			for _, l := range logs[:3] {
				l.advance(t, s, 5, 6, nil)
			}

			logs[4].advance(t, s, 6, 7, nil)
			assert.NoError(t, s.snapshot(bounded(t)), "the third snapshot must install")
			assert.False(t, groupKeys(installedSnapshot(t, f.store))[group], "the snapshot must compact the group")
		})

		t.Run("keeps a group in which half of the updates are current", func(t *testing.T) {
			t.Parallel()
			f, s, logs, group := grouped(t)

			for _, l := range logs[:2] {
				l.advance(t, s, 5, 6, nil)
			}

			logs[4].advance(t, s, 6, 7, nil)
			assert.NoError(t, s.snapshot(bounded(t)), "the third snapshot must install")
			assert.True(t, groupKeys(installedSnapshot(t, f.store))[group], "the snapshot must keep the group")
		})

		t.Run("compacts a group in which half of the updates are current with one of an origin that it retires",
			func(t *testing.T) {
				t.Parallel()
				f, s, logs, group := grouped(t)
				f.refuse(logs[2].origin)
				f.clock.Advance(2 * time.Hour)

				for _, l := range logs[:2] {
					l.advance(t, s, 5, 6, nil)
				}

				logs[4].advance(t, s, 6, 7, nil)
				assert.NoError(t, s.snapshot(bounded(t)), "the third snapshot must install")
				assert.False(t, groupKeys(installedSnapshot(t, f.store))[group], "the snapshot must compact the group")
			})

		t.Run("returns the error of the store for a group of a move that it does not read", func(t *testing.T) {
			t.Parallel()
			f, s, logs, group := grouped(t)

			for _, l := range logs[:3] {
				l.advance(t, s, 5, 6, nil)
			}

			logs[4].advance(t, s, 6, 7, nil)
			assert.NoError(t, f.store.Delete(t.Context(), group, version.Unspecified), "the group must delete")
			assert.Equal(t, errs.Classify(s.snapshot(bounded(t))), errs.NotFound,
				"the snapshot must return the error of the store")
		})

		t.Run("keeps the update of an origin at the record of the installed snapshot on that record",
			func(t *testing.T) {
				t.Parallel()
				a, b, c := newInternalLog(t, "example.com/a"), newInternalLog(t, "example.com/b"),
					newInternalLog(t, "example.com/c")
				f := newInternalFixture(t, a, b, c)
				s := f.server(t)
				a.advance(t, s, 0, 5, nil)
				c.advance(t, s, 0, 5, nil)
				assert.NoError(t, s.snapshot(bounded(t)), "the first snapshot must install")

				b.advance(t, s, 0, 5, nil)
				assert.NoError(t, s.snapshot(bounded(t)), "the second snapshot must install")

				// The record of c is the record of the first snapshot, and the
				// second snapshot moves only the updates before it.
				snap := installedSnapshot(t, f.store)
				i := slices.IndexFunc(snap.Origins, func(o snapOrigin) bool { return o.Origin == c.origin })
				assert.NotEqual(t, i, -1, "the snapshot must contain c")
				assert.HasPrefix(t, snap.Objects[snap.Origins[i].Object].Key, recordPrefix,
					"the update of c must remain on its record")
			})

		t.Run("collects only what neither of the two newest snapshots refers to", func(t *testing.T) {
			t.Parallel()
			a, b := newInternalLog(t, "example.com/a"), newInternalLog(t, "example.com/b")
			f := newInternalFixture(t, a, b)
			s := f.server(t)
			a.advance(t, s, 0, 5, nil)

			for size := uint64(1); size <= 4; size++ {
				b.advance(t, s, size-1, size, nil)
				assert.NoError(t, s.snapshot(bounded(t)), "the snapshot must install")
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

			baseSeq, _ := parseName(base.Record)

			var unneeded []string

			for _, key := range listKeys(t, f.store, recordPrefix) {
				if seq, _ := parseName(key[len(recordPrefix):]); !keep[key] && seq < baseSeq {
					unneeded = append(unneeded, key)
				}
			}

			assert.Empty(t, unneeded, "the store must keep only the records that the two newest snapshots need")

			assert.Length(t, listKeys(t, f.store, snapshotPrefix), 2, "the store must keep the two newest snapshots")
		})

		t.Run("deletes a record off the chain", func(t *testing.T) {
			t.Parallel()
			a := newInternalLog(t, "example.com/a")
			f := newInternalFixture(t, a)
			s := f.server(t)
			a.advance(t, s, 0, 5, nil)

			orphan := recordPrefix + string(appendName(nil, 2, []byte("an orphan")))
			_, err := blob.PutBytes(t.Context(), f.store, orphan, []byte("an orphan"), blob.PutOptions{})
			assert.NoError(t, err, "the orphan must store")

			a.advance(t, s, 5, 6, nil)
			a.advance(t, s, 6, 7, nil)
			assert.NoError(t, s.snapshot(bounded(t)), "the snapshot must install")

			_, err = f.store.Stat(t.Context(), orphan)
			assert.HasError(t, err, "the snapshot must collect the record off the chain")
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
			assert.NoError(t, s.snapshot(bounded(t)), "the first snapshot must install")

			f.clock.Advance(time.Minute)
			b.advance(t, s, 5, 6, nil)

			for range 2 {
				f.breaker.Record(s.targets[0], true)
			}

			assert.Equal(t, f.breaker.State(s.targets[0]), resilience.Open, "the circuit must be open")
			assert.NoError(t, s.snapshot(bounded(t)), "the snapshot must install")

			o, _ := s.st.origins.Get(hashOrigin(a.origin))
			assert.False(t, o.latest.group, "the update of a must remain on its record")

			f.clock.Advance(time.Minute)
			b.advance(t, s, 6, 7, nil)
			assert.NoError(t, s.snapshot(bounded(t)), "the next snapshot must install")

			o, _ = s.st.origins.Get(hashOrigin(a.origin))
			assert.True(t, o.latest.group, "the next snapshot must move the update of a")
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
			assert.NoError(t, s.snapshot(bounded(t)), "the first snapshot must install")

			f.clock.Advance(time.Minute)
			c.advance(t, s, 5, 6, nil)
			assert.NoError(t, s.snapshot(bounded(t)), "the second snapshot must install")

			moved := 0

			for _, l := range []*internalLog{a, b} {
				if o, _ := s.st.origins.Get(hashOrigin(l.origin)); o.latest.group {
					moved++
				}
			}

			assert.Equal(t, moved, 1, "the snapshot must repair one record and move its update")
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
			assert.NoError(t, s.snapshot(bounded(t)), "the first snapshot must install")
			f.clock.Advance(time.Minute)
			b.advance(t, s, 5, 6, nil)

			f.clock.SetUTCError(0, false)
			assert.NoError(t, s.snapshot(bounded(t)), "the snapshot must install")

			o, _ := s.st.origins.Get(hashOrigin(a.origin))
			assert.False(t, o.latest.group, "the snapshot must not repair the record")
		})

		t.Run("abandons a snapshot when another process installs one first", func(t *testing.T) {
			t.Parallel()
			a, b := newInternalLog(t, "example.com/a"), newInternalLog(t, "example.com/b")
			f := newInternalFixture(t, a, b)
			s := f.server(t)
			a.advance(t, s, 0, 5, nil)
			b.advance(t, s, 0, 5, nil)

			other := f.server(t)
			assert.NoError(t, other.snapshot(bounded(t)), "the other process must install")

			var err error
			assert.Pure(t, func() version.Version {
				_, v, readErr := readHead(t.Context(), f.store)
				assert.NoError(t, readErr, "the head must read")

				return v
			}, func() { err = s.snapshot(bounded(t)) }, "the snapshot must not write the head")
			assert.NoError(t, err, "the snapshot must be abandoned without an error")
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
			assert.NoError(t, s.snapshot(bounded(t)), "the snapshot must install")

			assert.Length(t, listKeys(t, f.store, retiredPrefix), 1, "the snapshot must create the retired object")
			assert.Equal(t, getRoute(t, s, a.origin).Code, http.StatusNotFound, "the route must refuse the origin")

			f.mu.Lock()
			f.accepted[a.origin] = a.log
			f.mu.Unlock()

			for _, server := range []*Server{s, f.server(t)} {
				_, failures, err := server.Advance(bounded(t), a.note(t, 6), []Update{a.update(t, 0, 6, nil)}, nil)
				assert.NoError(t, err, "Advance must check the update")
				assert.Length(t, failures, 1, "Advance must refuse the old size 0")

				se := assert.ErrorAs[*SizeError](t, failures[0].Err, "the failure must be a SizeError")
				assert.Equal(t, se.Size, uint64(5), "the SizeError must contain the retired size")
			}
		})

		t.Run("keeps an origin that Logs refuses for exactly Retention", func(t *testing.T) {
			t.Parallel()
			a, b := newInternalLog(t, "example.com/a"), newInternalLog(t, "example.com/b")
			f := newInternalFixture(t, a, b)
			s := f.server(t)
			a.advance(t, s, 0, 5, nil)

			f.refuse(a.origin)
			f.clock.Advance(time.Hour)
			b.advance(t, s, 0, 5, nil)
			assert.NoError(t, s.snapshot(bounded(t)), "the snapshot must install")

			assert.Empty(t, listKeys(t, f.store, retiredPrefix), "the snapshot must retire no origin")
		})

		t.Run("keeps an origin that Logs accepts after Retention", func(t *testing.T) {
			t.Parallel()
			a, b := newInternalLog(t, "example.com/a"), newInternalLog(t, "example.com/b")
			f := newInternalFixture(t, a, b)
			s := f.server(t)
			a.advance(t, s, 0, 5, nil)

			f.clock.Advance(2 * time.Hour)
			b.advance(t, s, 0, 5, nil)
			assert.NoError(t, s.snapshot(bounded(t)), "the snapshot must install")
			assert.Empty(t, listKeys(t, f.store, retiredPrefix), "the snapshot must retire no origin")
		})

		t.Run("retires an origin whose retired object exists", func(t *testing.T) {
			t.Parallel()
			a, b := newInternalLog(t, "example.com/a"), newInternalLog(t, "example.com/b")
			f := newInternalFixture(t, a, b)
			s := f.server(t)
			a.advance(t, s, 0, 5, nil)

			// Another process retired the origin first.
			data, _ := (&entry{Origin: a.origin, Root: a.update(t, 0, 5, nil).Body.Root, Size: 5}).MarshalBinary()
			putObject(t, f.store, retiredKey(hashOrigin(a.origin), 5), data)

			f.refuse(a.origin)
			f.clock.Advance(2 * time.Hour)
			b.advance(t, s, 0, 5, nil)
			assert.NoError(t, s.snapshot(bounded(t)), "the snapshot must install")
			assert.Equal(t, getRoute(t, s, a.origin).Code, http.StatusNotFound, "the route must refuse the origin")
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

					_, _, berr = p.Advance(bounded(t), bNote, bUpdates, nil)
				})
				awaitBefore(t, g.started, first, "the commit of b must sign")

				wg.Go(func() { _, failures, aerr = p.Advance(bounded(t), aNote, aUpdates, nil) })
				waitQueue(t, p, 1)

				other := f.server(t)
				a.advance(t, other, 0, 5, nil)
				f.refuse(a.origin)
				f.clock.Advance(2 * time.Hour)
				b.advance(t, other, 5, 6, nil)
				assert.NoError(t, other.snapshot(bounded(t)), "the snapshot must install")
				assert.Length(t, listKeys(t, f.store, retiredPrefix), 1, "the snapshot must retire a")

				open()
				waitAll(t, &wg, "both calls must return")

				assert.NoError(t, berr, "the commit of b must succeed")
				assert.NoError(t, aerr, "Advance must check the update of a")
				assert.Length(t, failures, 1, "Advance must refuse the old size 0")

				se := assert.ErrorAs[*SizeError](t, failures[0].Err, "the failure must be a SizeError")
				assert.Equal(t, se.Size, uint64(5), "the SizeError must contain the retired size")
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

			_, failures, err := s.Advance(bounded(t), batch.note(t, 5), updates, nil)
			assert.NoError(t, err, "Advance must commit the updates")
			assert.Empty(t, failures, "Advance must return no failure")
			assert.Equal(t, st.walks(retiredPrefix), 0, "the commit must not list the retired objects")
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

			//dokimi:lint-skip for-all: a fixed workload of 12 snapshots, which prop.ForAll would run 100 times
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

				_, failures, err := s.Advance(bounded(t), msg, updates, nil)
				assert.NoError(t, err, "Advance must commit the updates")
				assert.Empty(t, failures, "Advance must return no failure")
				assert.NoError(t, s.snapshot(bounded(t)), "the snapshot must install")

				snap := installedSnapshot(t, f.store)
				current := map[uint32]uint32{}

				for _, o := range snap.Origins {
					current[o.Object]++
				}

				for i, obj := range snap.Objects {
					if obj.Updates > 0 {
						assert.InRange(t, obj.Updates, 0, float64(2*current[uint32(i)]), //nolint:gosec // an index
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
			assert.NoError(tb, s.snapshot(bounded(tb)), "the first snapshot must install")
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
				assert.HasError(t, s.snapshot(bounded(t)), "the snapshot must fail")
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
			assert.NoError(t, s.snapshot(bounded(t)), "the first snapshot must install")
			b.advance(t, s, 5, 6, nil)

			st.refuse(groupPrefix)
			assert.HasError(t, s.snapshot(bounded(t)), "the snapshot must fail")
		})

		t.Run("moves two calls that together exceed a group into two groups", func(t *testing.T) {
			t.Parallel()
			c := newInternalLog(t, "example.com/c")
			f := newInternalFixture(t, a, b, c)
			s := f.server(t)

			for _, l := range []*internalLog{a, c} {
				l.advance(t, s, 0, 5, make([]byte, 600<<10))
			}

			b.advance(t, s, 0, 5, nil)
			assert.NoError(t, s.snapshot(bounded(t)), "the first snapshot must install")
			b.advance(t, s, 5, 6, nil)
			assert.NoError(t, s.snapshot(bounded(t)), "the second snapshot must install")

			assert.Length(t, groupKeys(installedSnapshot(t, f.store)), 2,
				"the snapshot must create a group for each call")
		})

		t.Run("returns the error of the store for a retired object that it does not create", func(t *testing.T) {
			t.Parallel()
			f, st, s := idle(t)
			f.refuse(a.origin)
			f.clock.Advance(2 * time.Hour)
			b.advance(t, s, 6, 7, nil)
			st.refuse(retiredPrefix)
			assert.HasError(t, s.snapshot(bounded(t)), "the snapshot must fail")
		})

		t.Run("moves no update of an origin that it retires", func(t *testing.T) {
			t.Parallel()
			f, _, s := idle(t)
			f.refuse(a.origin)
			f.clock.Advance(2 * time.Hour)
			b.advance(t, s, 6, 7, nil)

			assert.NoError(t, s.snapshot(bounded(t)), "the snapshot must install")
			assert.Empty(t, listKeys(t, f.store, groupPrefix), "the snapshot must create no group")
		})

		t.Run("keeps the update of an origin on a group of a snapshot before the installed one", func(t *testing.T) {
			t.Parallel()
			_, _, s := idle(t)
			assert.NoError(t, s.snapshot(bounded(t)), "the second snapshot must install")

			h := hashOrigin(a.origin)
			o, _ := s.st.origins.Get(h)
			group := o.latest.key
			assert.HasPrefix(t, group, groupPrefix, "the second snapshot must move the update of a into a group")

			for size := uint64(7); size <= 8; size++ {
				b.advance(t, s, size-1, size, nil)
				assert.NoError(t, s.snapshot(bounded(t)), "the snapshot must install")
			}

			o, _ = s.st.origins.Get(h)
			assert.Equal(t, o.latest.key, group, "the update of a must remain on its group")
		})

		t.Run("moves the updates of idle origins in the order of their records", func(t *testing.T) {
			t.Parallel()
			logs := []*internalLog{
				newInternalLog(t, "example.com/x"),
				newInternalLog(t, "example.com/y"),
				newInternalLog(t, "example.com/z"),
			}

			// The snapshot walks the origins in the order of their hashes, and
			// the logs commit in the reverse of that order.
			slices.SortFunc(logs, func(x, y *internalLog) int {
				hx, hy := hashOrigin(x.origin), hashOrigin(y.origin)

				return bytes.Compare(hy[:], hx[:])
			})

			f := newInternalFixture(t, logs[0], logs[1], logs[2], b)
			s := f.server(t)

			for _, l := range logs {
				l.advance(t, s, 0, 5, nil)
			}

			b.advance(t, s, 0, 5, nil)
			assert.NoError(t, s.snapshot(bounded(t)), "the first snapshot must install")
			b.advance(t, s, 5, 6, nil)
			assert.NoError(t, s.snapshot(bounded(t)), "the second snapshot must install")

			calls := make([]int, len(logs))
			for i, l := range logs {
				o, _ := s.st.origins.Get(hashOrigin(l.origin))
				calls[i] = o.latest.call
			}

			assert.Equal(t, calls, []int{0, 1, 2}, "the group must contain the calls in the order of their records")
		})

		t.Run("sets the gauge of the origins to the origins of the state", func(t *testing.T) {
			t.Parallel()
			_, _, s := idle(t)
			g := &lastGauge{}
			s.metrics.origins = g

			assert.NoError(t, s.snapshot(bounded(t)), "the snapshot must install")
			assert.Equal(t, math.Float64frombits(g.bits.Load()), float64(2), "the gauge must report both origins")
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

			assert.HasError(t, s.snapshot(bounded(t)), "the snapshot must fail")
		})

		positions := []struct {
			edit func(p *position)
			name string
		}{
			{
				name: "returns ErrJournal for a position of a call that the record does not have",
				edit: func(p *position) { p.call = 1 },
			},
			{
				name: "returns ErrJournal for a position of an update that the call does not have",
				edit: func(p *position) { p.update = 1 },
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

				assert.ErrorIs(t, s.snapshot(bounded(t)), ErrJournal, "the snapshot must refuse the position")
			})
		}

		t.Run("returns the cause of ctx while another repair of the record runs", func(t *testing.T) {
			t.Parallel()
			f, st, s := idle(t)
			st.refuse(linesPrefix)
			a.advance(t, s, 5, 6, nil)
			st.refuse("")
			b.advance(t, s, 6, 7, nil)
			assert.NoError(t, s.snapshot(bounded(t)), "the second snapshot must install")
			b.advance(t, s, 7, 8, nil)
			f.clock.Advance(time.Minute)

			o, _ := s.st.origins.Get(hashOrigin(a.origin))
			s.repairs[o.latest.key] = &repairing{done: make(chan struct{})}

			// The store returns the error of ctx, which is not its cause.
			cause := errors.New("the snapshot ran out of time")
			ctx, cancel := context.WithTimeoutCause(t.Context(), 50*time.Millisecond, cause)
			defer cancel()

			assert.ErrorIs(t, s.snapshot(ctx), cause, "the snapshot must end with the cause of ctx")
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

			assert.HasError(t, s.snapshot(bounded(t)), "the snapshot must fail")
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

			assert.HasError(t, s.snapshot(bounded(t)), "the snapshot must fail")
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

			assert.ErrorIs(t, s.snapshot(bounded(t)), ErrContention, "the snapshot must give up")
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

			assert.HasError(t, s.snapshot(bounded(t)), "the snapshot must fail")
		})

		t.Run("returns the error of the store for garbage that it does not delete", func(t *testing.T) {
			t.Parallel()
			_, st, s := idle(t)
			b.advance(t, s, 6, 7, nil)
			assert.NoError(t, s.snapshot(bounded(t)), "the second snapshot must install")
			b.advance(t, s, 7, 8, nil)
			st.del = func(string) error { return errors.New("the delete failed") }

			assert.HasError(t, s.snapshot(bounded(t)), "the snapshot must fail")
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
			assert.Contains(t, h.kept(), "witness: a snapshot failed", "the snapshot must log its failure")
		})

		t.Run("logs a state above 90% of MaxOrigins", func(t *testing.T) {
			t.Parallel()
			f := newInternalFixture(t, a)
			h := &logged{}
			cfg := f.config()
			cfg.MaxOrigins, cfg.Logger = 1, slog.New(h)
			s := newInternalServer(t, cfg)
			a.advance(t, s, 0, 5, nil)

			assert.NoError(t, s.snapshot(bounded(t)), "the snapshot must install")
			assert.Contains(t, h.kept(), "witness: the state is above 90% of MaxOrigins", "the snapshot must warn")
		})

		t.Run("logs nothing for a state at 90% of MaxOrigins", func(t *testing.T) {
			t.Parallel()
			batch := newInternalLog(t, "example.com/batch")
			f := newInternalFixture(t)
			h := &logged{}
			cfg := f.config()
			cfg.MaxOrigins, cfg.Logger = 10, slog.New(h)
			s := newInternalServer(t, cfg)

			root := batch.update(t, 0, 5, nil).Body.Root
			updates := make([]Update, 9)

			for i := range updates {
				origin := checkpoint.Origin("example.com/log/" + strconv.Itoa(i))
				f.accepted[origin] = batch.log
				updates[i] = Update{Body: checkpoint.Body{Origin: origin, Size: 5, Root: root}}
			}

			_, failures, err := s.Advance(bounded(t), batch.note(t, 5), updates, nil)
			assert.NoError(t, err, "Advance must commit the updates")
			assert.Empty(t, failures, "Advance must return no failure")
			assert.NoError(t, s.snapshot(bounded(t)), "the snapshot must install")
			assert.NotContains(t, h.kept(), "witness: the state is above 90% of MaxOrigins",
				"the snapshot must not warn")
		})

		t.Run("takes the snapshot that it installs into its state", func(t *testing.T) {
			t.Parallel()
			f := newInternalFixture(t, a)
			s := f.server(t)
			a.advance(t, s, 0, 5, nil)
			assert.NoError(t, s.snapshot(bounded(t)), "the snapshot must install")

			h, v, err := readHead(t.Context(), f.store)
			assert.NoError(t, err, "the head must read")
			assert.Equal(t, s.st.version, v, "the state must reflect the head of the install")
			assert.Equal(t, s.st.snap.name, h.Snapshot, "the state must take the snapshot")
		})
	})

	t.Run("place", func(t *testing.T) {
		t.Parallel()

		origin := checkpoint.Origin("example.com/a")
		root := crypto.NewDigest256([32]byte{1})

		// filling returns the writer of a snapshot of Seq 1 whose group has
		// one call and used bytes, and an object of one call with a note and
		// lines of 60 bytes each, with the move of its update.
		filling := func(used int) (*snapper, *loaded, move) {
			w := &snapper{placed: make(map[originHash]placed), seq: 1, bytes: used}
			other := entry{Origin: "example.com/b", Root: root, Size: 1}
			w.group.Calls = []call{{Note: []byte("a note"), Updates: []entry{other}}}

			l := &loaded{
				calls: []call{{Note: make([]byte, 60), Updates: []entry{{Origin: origin, Root: root, Size: 1}}}},
				lines: [][]byte{make([]byte, 60)},
			}

			m := move{from: position{key: recordPrefix + "x", seq: 1}, hash: hashOrigin(origin)}

			return w, l, m
		}

		t.Run("creates the group first when the note and the lines of a call exceed maxGroupBytes", func(t *testing.T) {
			t.Parallel()
			w, l, m := filling(maxGroupBytes - 100)
			assert.NoError(t, newInternalFixture(t).server(t).place(bounded(t), w, l, []move{m}),
				"place must place the call")
			assert.Length(t, w.keys, 1, "place must create the full group first")
			assert.Length(t, w.group.Calls, 1, "the call must start the next group")
		})

		t.Run("adds a call that fills the group to maxGroupBytes", func(t *testing.T) {
			t.Parallel()
			w, l, m := filling(maxGroupBytes - 120)
			assert.NoError(t, newInternalFixture(t).server(t).place(bounded(t), w, l, []move{m}),
				"place must place the call")
			assert.Empty(t, w.keys, "place must create no group")
			assert.Length(t, w.group.Calls, 2, "the call must join the group")
		})

		t.Run("adds one call for two updates of one call", func(t *testing.T) {
			t.Parallel()
			other := checkpoint.Origin("example.com/b")
			w := &snapper{placed: make(map[originHash]placed), seq: 1}
			l := &loaded{
				calls: []call{{
					Note:    []byte("a note"),
					Updates: []entry{{Origin: origin, Root: root, Size: 1}, {Origin: other, Root: root, Size: 1}},
				}},
				lines: [][]byte{[]byte("the lines")},
			}

			from := position{key: recordPrefix + "x", seq: 1}
			second := from
			second.update = 1
			moves := []move{{from: from, hash: hashOrigin(origin)}, {from: second, hash: hashOrigin(other)}}

			assert.NoError(t, newInternalFixture(t).server(t).place(bounded(t), w, l, moves),
				"place must place the call")
			assert.Length(t, w.group.Calls, 1, "place must add the call once")
			assert.Equal(t, w.placed[hashOrigin(other)], placed{update: 1},
				"the second update must follow the first in the call")
		})

		t.Run("returns ErrJournal for a position whose update is of another origin", func(t *testing.T) {
			t.Parallel()
			w, l, m := filling(0)
			m.hash = hashOrigin("example.com/b")
			assert.ErrorIs(t, newInternalFixture(t).server(t).place(bounded(t), w, l, []move{m}), ErrJournal,
				"place must refuse the position")
		})

		t.Run("returns the error of the store for the group that it creates first", func(t *testing.T) {
			t.Parallel()
			w, l, m := filling(maxGroupBytes - 100)
			f := newInternalFixture(t)
			st := &faulty{Store: f.store}
			cfg := f.config()
			cfg.State = st
			s := newInternalServer(t, cfg)

			errCreate := errors.New("the create failed")
			st.put = func(string) error { return errCreate }
			assert.ErrorIs(t, s.place(bounded(t), w, l, []move{m}), errCreate,
				"place must return the error of the store")
		})
	})

	t.Run("flush", func(t *testing.T) {
		t.Parallel()

		t.Run("records a group that another process created", func(t *testing.T) {
			t.Parallel()
			f := newInternalFixture(t)
			w := &snapper{seq: 1}
			w.group.Calls = []call{{
				Note:    []byte("a note"),
				Updates: []entry{{Origin: "example.com/a", Root: crypto.NewDigest256([32]byte{1}), Size: 1}},
			}}

			data, _ := w.group.MarshalBinary()
			key := groupPrefix + string(appendName(nil, 1, data))
			putObject(t, f.store, key, data)

			assert.NoError(t, f.server(t).flush(bounded(t), w), "flush must accept the group of the other process")
			assert.Equal(t, w.keys, []string{key}, "flush must record the key of the group")
		})
	})

	t.Run("installed", func(t *testing.T) {
		t.Parallel()

		t.Run("leaves a state that does not reflect the head that the install replaced", func(t *testing.T) {
			t.Parallel()
			a := newInternalLog(t, "example.com/a")
			f := newInternalFixture(t, a)
			s := f.server(t)
			a.advance(t, s, 0, 5, nil)

			other := f.server(t)
			assert.NoError(t, other.snapshot(bounded(t)), "the other process must install")

			h, nv, err := readHead(t.Context(), f.store)
			assert.NoError(t, err, "the head must read")

			snap := installedSnapshot(t, f.store)
			data, _ := snap.MarshalBinary()
			s.installed(bounded(t), snap, h.Snapshot, int64(len(data)), "a version of another head", nv, h)
			assert.Equal(t, s.st.snap.name, "", "the state must not take the snapshot")
		})
	})

	t.Run("snapshotIfDue", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			snapEnd uint64
			after   uint64
			want    bool
		}{
			{
				name:  "writes a snapshot at snapshotBytes of records after the installed snapshot",
				after: snapshotBytes, want: true,
			},
			{
				name:    "writes no snapshot below snapshotBytes of records after a large installed snapshot",
				snapEnd: 2 * snapshotBytes, after: 1,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				// A journal of one record, which no commit of the server wrote, so
				// no committer runs beside the case.
				f := newInternalFixture(t)
				putHead(t, f.store, head{Record: putRecord(t, f.store, &record{Seq: 1, Time: 1})})
				s := f.server(t)

				s.mu.Lock()
				s.st.snap.end, s.st.end = tt.snapEnd, tt.snapEnd+tt.after
				s.mu.Unlock()

				s.snapshotIfDue()

				h, _, err := readHead(t.Context(), f.store)
				assert.NoError(t, err, "the head must read")
				assert.Equal(t, h.Snapshot != "", tt.want, "snapshotIfDue must write a snapshot when one is due")
			})
		}

		t.Run("lets the next snapshot start after a snapshot", func(t *testing.T) {
			t.Parallel()
			f := newInternalFixture(t)
			putHead(t, f.store, head{Record: putRecord(t, f.store, &record{Seq: 1, Time: 1})})
			s := f.server(t)

			s.mu.Lock()
			s.st.end = snapshotBytes
			s.mu.Unlock()

			s.snapshotIfDue()
			assert.False(t, s.snapshotting.Load(), "snapshotIfDue must clear the flag of the running snapshot")
		})

		t.Run("writes a snapshot after 64 MiB of records", func(t *testing.T) {
			t.Parallel()
			a := newInternalLog(t, "example.com/a")
			f := newInternalFixture(t, a)
			s := f.server(t)

			for size := uint64(1); size <= 5; size++ {
				a.advance(t, s, size-1, size, make([]byte, 14<<20))
			}

			assert.Eventually(t, 10*time.Second, time.Millisecond, func(attempt assert.TB) {
				h, _, err := readHead(t.Context(), f.store)
				assert.NoError(attempt, err, "the head must read")
				assert.NotEmpty(attempt, h.Snapshot, "the head must name a snapshot")
			}, "the commit must write a snapshot")
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

// Stat fails with the error of f.stat for key, and returns the metadata of
// the object otherwise.
func (f *faulty) Stat(ctx context.Context, key string) (blob.Info, error) {
	f.mu.Lock()
	stat := f.stat
	f.mu.Unlock()

	if stat != nil {
		if err := stat(key); err != nil {
			return blob.Info{}, err
		}
	}

	return f.Store.Stat(ctx, key)
}

// List counts the walk of prefix, fails with the error of f.list, returns a
// cursor that yields the error of f.page, lists the objects of a reversed
// store in the reverse order of their keys, and lists them in key order
// otherwise.
func (f *faulty) List(ctx context.Context, prefix string, p page.Page) (page.Cursor[blob.Info], error) {
	f.mu.Lock()
	if f.lists == nil {
		f.lists = map[string]int{}
	}

	f.lists[prefix]++
	list, pg, reversed := f.list, f.page, f.reversed
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

	cur, err := f.Store.List(ctx, prefix, p)
	if err != nil || !reversed {
		return cur, err
	}

	defer func() { _ = cur.Close() }()

	var infos []blob.Info

	for info, err := range cur.Seq(ctx) {
		if err != nil {
			return nil, err
		}

		infos = append(infos, info)
	}

	slices.Reverse(infos)

	return &listedCursor{infos: infos, next: cur.NextPage()}, nil
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

// kept returns a copy of the messages that h kept, in the order of their
// records.
func (h *logged) kept() []string {
	h.mu.Lock()
	defer h.mu.Unlock()

	return slices.Clone(h.messages)
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

// Seq yields the objects of c in its order.
func (c *listedCursor) Seq(context.Context) iter.Seq2[blob.Info, error] {
	return func(yield func(blob.Info, error) bool) {
		for _, info := range c.infos {
			if !yield(info, nil) {
				return
			}
		}
	}
}

// NextPage returns the token of the page after c.
func (c *listedCursor) NextPage() string {
	return c.next
}

// Close returns nil.
func (*listedCursor) Close() error {
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

	s, err := NewServer(bounded(tb), cfg)
	assert.NoError(tb, err, "NewServer must accept the configuration")

	return s
}

// installedSnapshot returns the snapshot that the head of st names.
func installedSnapshot(tb testing.TB, st blob.Store) *snapshot {
	tb.Helper()

	h, _, err := readHead(tb.Context(), st)
	assert.NoError(tb, err, "the head must read")

	return readSnapshotNamed(tb, st, h.Snapshot)
}

// readSnapshotNamed returns the snapshot of st named name.
func readSnapshotNamed(tb testing.TB, st blob.Store, name string) *snapshot {
	tb.Helper()

	data, err := readNamed(tb.Context(), st, snapshotPrefix, name, maxObjectBytes)
	assert.NoError(tb, err, "the snapshot must read")

	snap := new(snapshot)
	assert.NoError(tb, snap.DecodeKanon(data, kanon.Options{}), "the snapshot must decode")

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
		assert.NoError(tb, err, "List must list "+prefix)

		for info, err := range cur.Seq(tb.Context()) {
			assert.NoError(tb, err, "the walk must not fail")
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
	s.Checkpoint().ServeHTTP(rec, httptest.NewRequestWithContext(bounded(tb), http.MethodGet, path, nil))

	return rec
}
