// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"go.dokimi.dev/assert"
	"go.thesmos.sh/kanon"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/version"
)

func TestRouteInternal(t *testing.T) {
	t.Parallel()

	a, b := newInternalLog(t, "example.com/a"), newInternalLog(t, "example.com/b")

	// faultyServer returns a fixture, a store with hooks and a server over
	// that store.
	faultyServer := func(tb testing.TB) (*internalFixture, *faulty, *Server) {
		tb.Helper()

		f := newInternalFixture(tb, a, b)
		st := &faulty{Store: f.store}
		cfg := f.config()
		cfg.State = st

		return f, st, newInternalServer(tb, cfg)
	}

	t.Run("serveCheckpoint", func(t *testing.T) {
		t.Parallel()

		t.Run("starts no refresh of a stale state for a path that does not parse", func(t *testing.T) {
			t.Parallel()
			f, st, s := faultyServer(t)

			// A refresh that starts waits in its read of the head until the
			// cleanup, so its flag remains set when the case reads it.
			gate := make(chan struct{})
			t.Cleanup(func() { close(gate) })
			st.get = func(key string) error {
				if key == headKey {
					<-gate
				}

				return nil
			}

			f.clock.Advance(2 * refreshAge)
			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(bounded(t), http.MethodGet, "/m/x"+checkpointPath, nil)
			s.Checkpoint().ServeHTTP(rec, req)
			assert.Equal(t, rec.Code, http.StatusNotFound, "the route must refuse the path")
			assert.False(t, s.refreshing.Load(), "a path that does not parse must start no refresh")
		})

		t.Run("responds with 500 for a served position at an update of another origin", func(t *testing.T) {
			t.Parallel()
			c := newInternalLog(t, "example.com/c")
			f := newInternalFixture(t, a, b, c)
			g := &gated{Cosigner: f.signer, started: make(chan struct{}), release: make(chan struct{})}
			f.signer = g
			s := f.server(t)

			var wg sync.WaitGroup

			first := make(chan struct{})

			wg.Go(func() {
				defer close(first)

				c.advance(t, s, 0, 5, nil)
			})
			awaitBefore(t, g.started, first, "the first commit must sign")

			// The next commit takes the calls of a and b in one record.
			wg.Go(func() { a.advance(t, s, 0, 5, nil) })
			waitQueue(t, s, 1)
			wg.Go(func() { b.advance(t, s, 0, 5, nil) })
			waitQueue(t, s, 2)
			close(g.release)
			waitAll(t, &wg, "every call must return")

			h := hashOrigin(a.origin)

			s.mu.Lock()
			o, _ := s.st.origins.Get(h)
			o.served.call = 1 - o.served.call
			o.latest = o.served
			s.st.origins.Set(h, o)
			s.mu.Unlock()

			assert.Equal(t, getRoute(t, s, a.origin).Code, http.StatusInternalServerError,
				"the route must refuse the update of b")
		})

		t.Run("responds with 500 for a group with a byte after its encoding", func(t *testing.T) {
			t.Parallel()
			f, _, s := faultyServer(t)
			lines := a.advance(t, s, 0, 5, nil)

			h := hashOrigin(a.origin)
			o, _ := s.st.origins.Get(h)
			g := group{Calls: []call{{
				Note: a.note(t, 5), Updates: []entry{{Origin: a.origin, Root: o.root, Size: 5}}, Lines: lines,
			}}}
			data, err := g.MarshalBinary()
			assert.NoError(t, err, "the group must encode")
			data = append(data, 0)

			key := groupPrefix + string(appendName(nil, 1, data))
			putObject(t, f.store, key, data)

			s.mu.Lock()
			o.served = position{key: key, seq: 1, group: true}
			o.latest = o.served
			s.st.origins.Set(h, o)
			s.mu.Unlock()

			assert.Equal(t, getRoute(t, s, a.origin).Code, http.StatusInternalServerError,
				"the route must refuse the group")
		})

		// otherLines are edits of the encoding of the lines of a record of
		// one call.
		otherLines := []struct {
			edit func(tb testing.TB, data []byte) []byte
			name string
		}{
			{
				name: "responds with 500 for lines with a byte after their encoding",
				edit: func(_ testing.TB, data []byte) []byte { return append(data, 0) },
			},
			{
				name: "responds with 500 for lines of another number of calls than their record",
				edit: func(tb testing.TB, data []byte) []byte {
					tb.Helper()

					var ls lines
					assert.NoError(tb, ls.DecodeKanon(data, kanon.Options{}), "the lines must decode")
					ls.Calls = append(ls.Calls, ls.Calls[0])

					more, err := ls.MarshalBinary()
					assert.NoError(tb, err, "the lines must encode")

					return more
				},
			},
		}
		for _, tt := range otherLines {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				f, _, s := faultyServer(t)
				a.advance(t, s, 0, 5, nil)

				keys := listKeys(t, f.store, linesPrefix)
				assert.Length(t, keys, 1, "the commit must store its lines")
				data, err := read(t.Context(), f.store, keys[0], maxObjectBytes)
				assert.NoError(t, err, "the lines must read")
				putObject(t, f.store, keys[0], tt.edit(t, data))

				assert.Equal(t, getRoute(t, f.server(t), a.origin).Code, http.StatusInternalServerError,
					"the route must refuse the lines")
			})
		}

		t.Run("serves the earlier update while the Stat of the lines of the latest fails", func(t *testing.T) {
			t.Parallel()
			f, st, s := faultyServer(t)
			a.advance(t, s, 0, 5, nil)
			st.refuse(linesPrefix)
			a.advance(t, s, 5, 6, nil)
			st.refuse("")
			st.mu.Lock()
			st.stat = func(key string) error {
				if strings.HasPrefix(key, linesPrefix) {
					return errs.WithClass(errors.New("the store is busy"), errs.Transient)
				}

				return nil
			}
			st.mu.Unlock()

			// Past the repair age, and within the age that starts a refresh.
			f.clock.Advance(30 * time.Second)

			rec := getRoute(t, s, a.origin)
			assert.Equal(t, rec.Code, http.StatusOK, "the route must serve the origin")
			assert.HasPrefix(t, rec.Body.String(), string(a.note(t, 5)), "the route must serve the earlier update")
		})

		t.Run("caches the object of the served position at the length of its encodings", func(t *testing.T) {
			t.Parallel()
			f, _, s := faultyServer(t)
			a.advance(t, s, 0, 5, nil)
			assert.Equal(t, getRoute(t, s, a.origin).Code, http.StatusOK, "the route must serve the origin")

			o, _ := s.st.origins.Get(hashOrigin(a.origin))
			name := o.served.key[len(recordPrefix):]
			rec, err := read(t.Context(), f.store, recordPrefix+name, maxObjectBytes)
			assert.NoError(t, err, "the record must read")
			ls, err := read(t.Context(), f.store, linesPrefix+name, maxObjectBytes)
			assert.NoError(t, err, "the lines must read")

			assert.Equal(t, s.objects.Cost(), int64(len(rec)+len(ls)), "the cache must count the length of the object")
		})

		t.Run("responds with 503 for an origin of a snapshot without a served position", func(t *testing.T) {
			t.Parallel()
			f, st, s := faultyServer(t)
			st.refuse(linesPrefix)
			a.advance(t, s, 0, 5, nil)
			st.refuse("")
			assert.NoError(t, s.snapshot(bounded(t)), "the snapshot must install")

			assert.Equal(t, getRoute(t, f.server(t), a.origin).Code, http.StatusServiceUnavailable,
				"the route must report the origin of the snapshot")
		})

		t.Run("responds with 500 for a served position that names no update of the origin", func(t *testing.T) {
			t.Parallel()
			_, _, s := faultyServer(t)
			a.advance(t, s, 0, 5, nil)

			h := hashOrigin(a.origin)
			o, _ := s.st.origins.Get(h)
			o.served.update = 5
			o.latest = o.served
			s.st.origins.Set(h, o)

			assert.Equal(t, getRoute(t, s, a.origin).Code, http.StatusInternalServerError,
				"the route must refuse the position")
		})

		positions := []struct {
			edit func(p *position)
			name string
		}{
			{
				name: "responds with 500 for a served position one past the updates of its call",
				edit: func(p *position) { p.update = 1 },
			},
			{
				name: "responds with 500 for a served position one past the calls of its record",
				edit: func(p *position) { p.call = 1 },
			},
		}
		for _, tt := range positions {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, _, s := faultyServer(t)
				a.advance(t, s, 0, 5, nil)

				h := hashOrigin(a.origin)
				o, _ := s.st.origins.Get(h)
				tt.edit(&o.served)
				o.latest = o.served
				s.st.origins.Set(h, o)

				assert.Equal(t, getRoute(t, s, a.origin).Code, http.StatusInternalServerError,
					"the route must refuse the position")
			})
		}

		t.Run("responds with 500 for a group that does not decode", func(t *testing.T) {
			t.Parallel()
			f, _, s := faultyServer(t)
			a.advance(t, s, 0, 5, nil)

			data := []byte{0xff}
			key := groupPrefix + string(appendName(nil, 1, data))
			putObject(t, f.store, key, data)

			h := hashOrigin(a.origin)
			o, _ := s.st.origins.Get(h)
			o.served = position{key: key, seq: 1, group: true}
			o.latest = o.served
			s.st.origins.Set(h, o)

			assert.Equal(t, getRoute(t, s, a.origin).Code, http.StatusInternalServerError,
				"the route must refuse the group")
		})

		// collected returns a fixture, a server whose served record of a
		// garbage collection deleted, and the lines of that record. A newer
		// snapshot moved the update into a group.
		collected := func(tb testing.TB) (*internalFixture, *Server, []byte) {
			tb.Helper()

			f, _, s := faultyServer(tb)
			lines := a.advance(tb, s, 0, 5, nil)
			b.advance(tb, s, 0, 5, nil)

			stale := f.server(tb)
			assert.Equal(tb, getRoute(tb, stale, a.origin).Code, http.StatusOK, "the route must serve the record")

			for size := uint64(6); size <= 8; size++ {
				assert.NoError(tb, s.snapshot(bounded(tb)), "the snapshot must install")
				b.advance(tb, s, size-1, size, nil)
			}

			o, _ := stale.st.origins.Get(hashOrigin(a.origin))
			stale.objects.Delete(o.served.key)
			_, err := f.store.Stat(tb.Context(), o.served.key)
			assert.HasError(tb, err, "garbage collection must delete the served record")

			return f, stale, lines
		}

		t.Run("serves the group of a newer snapshot after garbage collection deleted the served record",
			func(t *testing.T) {
				t.Parallel()
				_, stale, lines := collected(t)

				rec := getRoute(t, stale, a.origin)
				assert.Equal(t, rec.Code, http.StatusOK, "the route must serve the group")
				assert.Equal(
					t,
					rec.Body.String(),
					string(a.note(t, 5))+string(lines),
					"the route must serve the update",
				)
			})

		t.Run("responds with 503 when the group of the newer snapshot is missing too", func(t *testing.T) {
			t.Parallel()
			f, stale, _ := collected(t)

			for _, key := range listKeys(t, f.store, groupPrefix) {
				assert.NoError(t, f.store.Delete(t.Context(), key, version.Unspecified), "the group must delete")
			}

			assert.Equal(t, getRoute(t, stale, a.origin).Code, http.StatusServiceUnavailable,
				"the route must report the missing group")
		})

		t.Run("responds with 503 when the served group is missing", func(t *testing.T) {
			t.Parallel()
			f, _, _ := collected(t)

			for _, key := range listKeys(t, f.store, groupPrefix) {
				assert.NoError(t, f.store.Delete(t.Context(), key, version.Unspecified), "the group must delete")
			}

			assert.Equal(t, getRoute(t, f.server(t), a.origin).Code, http.StatusServiceUnavailable,
				"the route must report the missing group")
		})

		t.Run("commits a call after it served the group of a newer snapshot", func(t *testing.T) {
			t.Parallel()
			_, stale, _ := collected(t)
			assert.Equal(t, getRoute(t, stale, a.origin).Code, http.StatusOK, "the route must serve the group")

			b.advance(t, stale, 8, 9, nil)
		})

		t.Run("responds with 503 for an origin that a newer snapshot retired after a collection deleted its record",
			func(t *testing.T) {
				t.Parallel()
				f, _, s := faultyServer(t)
				a.advance(t, s, 0, 5, nil)
				b.advance(t, s, 0, 5, nil)

				stale := f.server(t)
				assert.Equal(t, getRoute(t, stale, a.origin).Code, http.StatusOK, "the route must serve the record")

				f.refuse(a.origin)
				f.clock.Advance(2 * time.Hour)

				for size := uint64(6); size <= 7; size++ {
					b.advance(t, s, size-1, size, nil)
					assert.NoError(t, s.snapshot(bounded(t)), "the snapshot must install")
				}

				o, _ := stale.st.origins.Get(hashOrigin(a.origin))
				stale.objects.Delete(o.served.key)
				_, err := f.store.Stat(t.Context(), o.served.key)
				assert.HasError(t, err, "garbage collection must delete the served record")

				assert.Equal(t, getRoute(t, stale, a.origin).Code, http.StatusServiceUnavailable,
					"the route must report the retired origin")
			})

		t.Run("returns the error of a refresh after the served object went missing", func(t *testing.T) {
			t.Parallel()
			f, st, s := faultyServer(t)
			a.advance(t, s, 0, 5, nil)

			cfg := f.config()
			cfg.State = st
			again := newInternalServer(t, cfg)

			o, _ := s.st.origins.Get(hashOrigin(a.origin))
			assert.NoError(t, f.store.Delete(t.Context(), o.latest.key, version.Unspecified), "the record must delete")
			st.get = func(key string) error {
				if key == headKey {
					return errors.New("the read of the head failed")
				}

				return nil
			}

			assert.Equal(t, getRoute(t, again, a.origin).Code, http.StatusInternalServerError,
				"the route must report the refresh")
		})

		t.Run("logs a refresh that fails", func(t *testing.T) {
			t.Parallel()
			f := newInternalFixture(t, a)
			st := &faulty{Store: f.store}
			h := &logged{}
			cfg := f.config()
			cfg.State, cfg.Logger = st, slog.New(h)
			s := newInternalServer(t, cfg)
			a.advance(t, s, 0, 5, nil)

			st.get = func(key string) error {
				if key == headKey {
					return errors.New("the read of the head failed")
				}

				return nil
			}

			f.clock.Advance(2 * time.Minute)
			getRoute(t, s, a.origin)

			failed := func() bool { return slices.Contains(h.kept(), "witness: a refresh failed") }
			assert.EventuallyTrue(t, patience, failed, "the refresh must log its failure")
		})
	})

	t.Run("parseRoute", func(t *testing.T) {
		t.Parallel()

		h := hashOrigin(a.origin)
		digits := hex.EncodeToString(h[:])

		t.Run("returns the hash of a path of only the hash and the checkpoint element", func(t *testing.T) {
			t.Parallel()
			got, ok := parseRoute("/" + digits + checkpointPath)
			assert.True(t, ok, "parseRoute must accept the path")
			assert.Equal(t, got, h, "parseRoute must return the hash")
		})

		paths := []struct {
			name string
			give string
		}{
			{name: "reports false for a path without a slash before the hash", give: digits + checkpointPath},
			{
				name: "reports false for a path with another character before the hash",
				give: "x" + digits + checkpointPath,
			},
			{
				name: "reports false for a hash with a character below the digits",
				give: "/" + digits[1:] + "." + checkpointPath,
			},
			{
				name: "reports false for a hash with a character above the letters",
				give: "/" + digits[1:] + "g" + checkpointPath,
			},
		}
		for _, tt := range paths {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, ok := parseRoute(tt.give)
				assert.False(t, ok, "parseRoute must refuse the path")
			})
		}
	})

	t.Run("advanceServed", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the state of an origin that left the state", func(t *testing.T) {
			t.Parallel()
			_, _, s := faultyServer(t)
			a.advance(t, s, 0, 5, nil)

			h := hashOrigin(a.origin)
			o, _ := s.st.origins.Get(h)
			s.st.origins.Delete(h)

			got := s.advanceServed(bounded(t), h, o)
			assert.Equal(t, got, o, "advanceServed must return the state that it received")
		})

		t.Run("moves no served position back to an earlier record", func(t *testing.T) {
			t.Parallel()
			_, _, s := faultyServer(t)
			a.advance(t, s, 0, 5, nil)
			first := s.st.head.Record
			a.advance(t, s, 5, 6, nil)

			h := hashOrigin(a.origin)
			o, _ := s.st.origins.Get(h)

			// The state of a GET that read it before the second commit.
			stale := o
			stale.latest = position{key: recordPrefix + first, seq: 1}

			got := s.advanceServed(bounded(t), h, stale)
			assert.Equal(t, got.served, o.served, "advanceServed must keep the later served position")
		})

		t.Run("moves no served position to another update of its record", func(t *testing.T) {
			t.Parallel()
			_, _, s := faultyServer(t)
			a.advance(t, s, 0, 5, nil)

			h := hashOrigin(a.origin)
			o, _ := s.st.origins.Get(h)

			other := o
			other.latest.call = 1

			got := s.advanceServed(bounded(t), h, other)
			assert.Equal(t, got.served, o.served, "advanceServed must keep the served position of the record")
		})
	})

	t.Run("repairDue", func(t *testing.T) {
		t.Parallel()

		t.Run("reports false for the reading of a UTC source that fails", func(t *testing.T) {
			t.Parallel()
			_, _, s := faultyServer(t)
			s.utc = brokenUTC{
				reading: clock.UTCReading{Time: internalTime.Add(time.Hour), Synced: true},
				err:     errors.New("the source failed"),
			}

			assert.False(t, s.repairDue(uint64(internalTime.Unix())),
				"repairDue must not trust a reading with an error")
		})
	})

	t.Run("readLoaded", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the length of a record and of its lines as the size of the object", func(t *testing.T) {
			t.Parallel()
			f, _, s := faultyServer(t)
			a.advance(t, s, 0, 5, nil)

			o, _ := s.st.origins.Get(hashOrigin(a.origin))
			got, err := s.readLoaded(bounded(t), o.served)
			assert.NoError(t, err, "readLoaded must read the record")

			name := o.served.key[len(recordPrefix):]
			rec, err := read(t.Context(), f.store, recordPrefix+name, maxObjectBytes)
			assert.NoError(t, err, "the record must read")
			ls, err := read(t.Context(), f.store, linesPrefix+name, maxObjectBytes)
			assert.NoError(t, err, "the lines must read")

			assert.Equal(t, got.size, int64(len(rec)+len(ls)),
				"the size must be the length of the record and its lines")
		})
	})

	t.Run("refreshIfStale", func(t *testing.T) {
		t.Parallel()

		t.Run("starts no refresh of a state of exactly refreshAge", func(t *testing.T) {
			t.Parallel()
			f, st, s := faultyServer(t)

			// A refresh that starts waits in its read of the head until the
			// cleanup, so its flag remains set when the case reads it.
			gate := make(chan struct{})
			t.Cleanup(func() { close(gate) })
			st.get = func(key string) error {
				if key == headKey {
					<-gate
				}

				return nil
			}

			f.clock.Advance(refreshAge)
			s.refreshIfStale()
			assert.False(t, s.refreshing.Load(), "a state of exactly refreshAge must start no refresh")
		})

		// The bubble waits for every goroutine of the server, so a refresh
		// that started has read the head before the case counts.
		t.Run("starts no refresh while a refresh runs", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				f, st, s := faultyServer(t)

				var reads atomic.Int64

				st.mu.Lock()
				st.get = func(key string) error {
					if key == headKey {
						reads.Add(1)
					}

					return nil
				}
				st.mu.Unlock()

				f.clock.Advance(2 * refreshAge)
				s.refreshing.Store(true)
				s.refreshIfStale()
				synctest.Wait()
				assert.Equal(t, reads.Load(), int64(0), "refreshIfStale must start no refresh")
			})
		})
	})

	t.Run("refresh", func(t *testing.T) {
		t.Parallel()

		t.Run("clears the flag of the refresh when it ends", func(t *testing.T) {
			t.Parallel()
			_, _, s := faultyServer(t)
			s.refreshing.Store(true)

			s.refresh()
			assert.False(t, s.refreshing.Load(), "refresh must clear its flag")
		})
	})
}
