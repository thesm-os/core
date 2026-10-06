// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

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

		t.Run("responds with 503 for an origin of a snapshot without a served position", func(t *testing.T) {
			t.Parallel()
			f, st, s := faultyServer(t)
			st.refuse(linesPrefix)
			a.advance(t, s, 0, 5, nil)
			st.refuse("")
			testkit.NoError(t, s.snapshot(bounded(t)), "the snapshot must install")

			testkit.Equal(t, getRoute(t, f.server(t), a.origin).Code, http.StatusServiceUnavailable,
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

			testkit.Equal(t, getRoute(t, s, a.origin).Code, http.StatusInternalServerError,
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

				testkit.Equal(t, getRoute(t, s, a.origin).Code, http.StatusInternalServerError,
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

			testkit.Equal(t, getRoute(t, s, a.origin).Code, http.StatusInternalServerError,
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
			testkit.Equal(tb, getRoute(tb, stale, a.origin).Code, http.StatusOK, "the route must serve the record")

			for size := uint64(6); size <= 8; size++ {
				testkit.NoError(tb, s.snapshot(bounded(tb)), "the snapshot must install")
				b.advance(tb, s, size-1, size, nil)
			}

			o, _ := stale.st.origins.Get(hashOrigin(a.origin))
			stale.objects.Delete(o.served.key)
			_, err := f.store.Stat(tb.Context(), o.served.key)
			testkit.Error(tb, err, "garbage collection must delete the served record")

			return f, stale, lines
		}

		t.Run("serves the group of a newer snapshot after garbage collection deleted the served record",
			func(t *testing.T) {
				t.Parallel()
				_, stale, lines := collected(t)

				rec := getRoute(t, stale, a.origin)
				testkit.Equal(t, rec.Code, http.StatusOK, "the route must serve the group")
				testkit.Equal(
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
				testkit.NoError(t, f.store.Delete(t.Context(), key, version.Unspecified), "the group must delete")
			}

			testkit.Equal(t, getRoute(t, stale, a.origin).Code, http.StatusServiceUnavailable,
				"the route must report the missing group")
		})

		t.Run("responds with 503 when the served group is missing", func(t *testing.T) {
			t.Parallel()
			f, _, _ := collected(t)

			for _, key := range listKeys(t, f.store, groupPrefix) {
				testkit.NoError(t, f.store.Delete(t.Context(), key, version.Unspecified), "the group must delete")
			}

			testkit.Equal(t, getRoute(t, f.server(t), a.origin).Code, http.StatusServiceUnavailable,
				"the route must report the missing group")
		})

		t.Run("returns the error of a refresh after the served object went missing", func(t *testing.T) {
			t.Parallel()
			f, st, s := faultyServer(t)
			a.advance(t, s, 0, 5, nil)

			cfg := f.config()
			cfg.State = st
			again := newInternalServer(t, cfg)

			o, _ := s.st.origins.Get(hashOrigin(a.origin))
			testkit.NoError(t, f.store.Delete(t.Context(), o.latest.key, version.Unspecified), "the record must delete")
			st.get = func(key string) error {
				if key == headKey {
					return errors.New("the read of the head failed")
				}

				return nil
			}

			testkit.Equal(t, getRoute(t, again, a.origin).Code, http.StatusInternalServerError,
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

			deadline := time.Now().Add(patience)
			for !h.has("witness: a refresh failed") {
				if time.Now().After(deadline) {
					t.Fatal("the refresh must log its failure")
				}

				time.Sleep(time.Millisecond)
			}
		})
	})

	t.Run("parseRoute", func(t *testing.T) {
		t.Parallel()

		h := hashOrigin(a.origin)
		digits := hex.EncodeToString(h[:])

		t.Run("returns the hash of a path of only the hash and the checkpoint element", func(t *testing.T) {
			t.Parallel()
			got, ok := parseRoute("/" + digits + checkpointPath)
			testkit.True(t, ok, "parseRoute must accept the path")
			testkit.True(t, got == h, "parseRoute must return the hash")
		})

		t.Run("reports false for a path without a slash before the hash", func(t *testing.T) {
			t.Parallel()
			_, ok := parseRoute(digits + checkpointPath)
			testkit.False(t, ok, "parseRoute must refuse the path")
		})
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
			testkit.True(t, got == o, "advanceServed must return the state that it received")
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
			testkit.True(t, got.served == o.served, "advanceServed must keep the later served position")
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
			testkit.True(t, got.served == o.served, "advanceServed must keep the served position of the record")
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
			testkit.NoError(t, err, "readLoaded must read the record")

			name := o.served.key[len(recordPrefix):]
			rec, err := read(t.Context(), f.store, recordPrefix+name, maxObjectBytes)
			testkit.NoError(t, err, "the record must read")
			ls, err := read(t.Context(), f.store, linesPrefix+name, maxObjectBytes)
			testkit.NoError(t, err, "the lines must read")

			testkit.Equal(t, got.size, int64(len(rec)+len(ls)),
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
			testkit.False(t, s.refreshing.Load(), "a state of exactly refreshAge must start no refresh")
		})
	})
}
