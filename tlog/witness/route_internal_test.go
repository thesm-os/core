// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
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
			testkit.NoError(t, s.snapshot(t.Context()), "the snapshot must install")

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
				testkit.NoError(tb, s.snapshot(tb.Context()), "the snapshot must install")
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

			deadline := time.Now().Add(5 * time.Second)
			for !h.has("witness: a refresh failed") {
				if time.Now().After(deadline) {
					t.Fatal("the refresh must log its failure")
				}

				time.Sleep(time.Millisecond)
			}
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

			got := s.advanceServed(t.Context(), h, o)
			testkit.True(t, got == o, "advanceServed must return the state that it received")
		})
	})
}
