// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness_test

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/blob"
	"go.thesmos.sh/core/page"
	"go.thesmos.sh/core/tlog/checkpoint"
	"go.thesmos.sh/core/tlog/witness"
	"go.thesmos.sh/core/version"
)

// checkpointCall is a request to the route of the checkpoint of an origin,
// a writer that discards its response, and the length of the note and the
// lines that the route serves.
type checkpointCall struct {
	h    http.Handler
	req  *http.Request
	w    *discard
	want int
}

// newCheckpointCall returns a request to the route of a log that committed
// the size 5 and stored its lines. It serves the request once, which reads
// the record and its lines into the cache of the route.
func newCheckpointCall(tb testing.TB) *checkpointCall {
	tb.Helper()

	l := newTestLog(tb, logName)
	f := newFixture(tb, l)
	s := newServer(tb, f.config())
	lines := advance(tb, s, l, l.update(tb, 0, 5))
	settle(tb, s, l)
	assert.Length(tb, keys(tb, f.store, "lines/"), 1, "the commit must store its lines")

	path := monitoringPath + "/" + originHashText(l.origin) + "/checkpoint"
	c := &checkpointCall{
		h:    s.Checkpoint(),
		req:  httptest.NewRequestWithContext(tb.Context(), http.MethodGet, path, nil),
		w:    &discard{header: make(http.Header)},
		want: len(l.notes[5]) + len(lines),
	}
	c.serve()

	return c
}

// serve serves the request of c again, into a reset response.
func (c *checkpointCall) serve() {
	c.w.code, c.w.n = 0, 0
	c.h.ServeHTTP(c.w, c.req)
}

func TestRoute(t *testing.T) {
	t.Parallel()

	t.Run("Checkpoint", func(t *testing.T) {
		t.Parallel()

		l := newTestLog(t, logName)

		t.Run("serves the prefix, the note and the lines of the latest update", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, l)
			s := newServer(t, f.config())

			u := l.update(t, 0, 5)
			u.Prefix = []byte("a prefix\n")
			lines, _, err := s.Advance(bounded(t), l.notes[5], []witness.Update{u}, nil)
			assert.NoError(t, err, "Advance must commit the update")
			settle(t, s, l)
			assert.Length(t, keys(t, f.store, "lines/"), 1, "the commit must store its lines")

			rec := get(t, s, l.origin)
			assert.Equal(t, rec.Code, http.StatusOK, "the route must serve the origin")
			assert.Equal(t, rec.Body.String(), "a prefix\n"+string(l.notes[5])+string(lines),
				"the route must serve the prefix, the note and the lines")
		})

		t.Run("serves the type text/plain for a prefix that net/http detects as HTML", func(t *testing.T) {
			t.Parallel()
			s := newServer(t, newFixture(t, l).config())

			u := l.update(t, 0, 5)
			u.Prefix = []byte("<!DOCTYPE html>\n")
			_, _, err := s.Advance(bounded(t), l.notes[5], []witness.Update{u}, nil)
			assert.NoError(t, err, "Advance must commit the update")
			settle(t, s, l)

			assert.Equal(t, get(t, s, l.origin).Header().Get("Content-Type"), linesType,
				"the route must serve text")
		})

		t.Run("serves the update of a record of another process whose lines exist", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, l)
			s := newServer(t, f.config())
			lines := advance(t, s, l, l.update(t, 0, 5))
			settle(t, s, l)
			assert.Length(t, keys(t, f.store, "lines/"), 1, "the commit must store its lines")

			rec := get(t, newServer(t, f.config()), l.origin)
			assert.Equal(t, rec.Code, http.StatusOK, "the route must serve the origin")
			assert.Equal(t, rec.Body.String(), string(l.notes[5])+string(lines),
				"the route must serve the update of the other process")
		})

		t.Run("serves the earlier update while the lines of the latest are missing", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, l)
			s := newServer(t, f.config())
			lines := advance(t, s, l, l.update(t, 0, 5))
			settle(t, s, l)
			assert.Length(t, keys(t, f.store, "lines/"), 1, "the first commit must store its lines")

			refused := refuseLines(f)
			advance(t, s, l, l.update(t, 5, 6))
			settle(t, s, l)
			assert.Equal(t, refused.Load(), int64(1), "the store must refuse the lines of the second commit")

			rec := get(t, s, l.origin)
			assert.Equal(t, rec.Code, http.StatusOK, "the route must serve the origin")
			assert.Equal(t, rec.Body.String(), string(l.notes[5])+string(lines), "the route must serve size 5")
		})

		t.Run("responds with 404 for an origin whose first commit has no lines", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, l)
			s := newServer(t, f.config())
			refused := refuseLines(f)

			advance(t, s, l, l.update(t, 0, 5))
			settle(t, s, l)
			assert.Equal(t, refused.Load(), int64(1), "the store must refuse the lines")
			assert.Equal(t, get(t, s, l.origin).Code, http.StatusNotFound, "the route must not serve the commit")
		})

		t.Run("responds with 404 for an origin that the state does not contain", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, get(t, newServer(t, newFixture(t, l).config()), l.origin).Code, http.StatusNotFound,
				"the route must not serve an unknown origin")
		})

		hash := originHashText(l.origin)
		paths := []struct {
			name string
			give string
		}{
			{name: "responds with 404 for a path without the checkpoint element", give: "/m/" + hash},
			{name: "responds with 404 for a hash of upper case", give: "/m/" + strings.ToUpper(hash) + "/checkpoint"},
			{name: "responds with 404 for a short hash", give: "/m/" + hash[1:] + "/checkpoint"},
			{name: "responds with 404 for a hash without a slash before it", give: "/m" + hash + "/checkpoint"},
			{
				name: "responds with 404 for a hash with a character that is not a digit",
				give: "/m/" + hash[1:] + "x/checkpoint",
			},
		}
		for _, tt := range paths {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				s := newServer(t, newFixture(t, l).config())
				advance(t, s, l, l.update(t, 0, 5))

				rec := httptest.NewRecorder()
				s.Checkpoint().ServeHTTP(rec, httptest.NewRequestWithContext(bounded(t), http.MethodGet, tt.give, nil))
				assert.Equal(t, rec.Code, http.StatusNotFound, "the route must refuse the path")
			})
		}

		t.Run("responds with 503 when the object of the served update is missing", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, l)
			first := newServer(t, f.config())
			advance(t, first, l, l.update(t, 0, 5))
			settle(t, first, l)
			assert.Length(t, keys(t, f.store, "lines/"), 1, "the commit must store its lines")

			s := newServer(t, f.config())
			deleteAll(t, f.store, "records/")
			assert.Equal(t, get(t, s, l.origin).Code, http.StatusServiceUnavailable,
				"the route must report the missing object")
		})

		t.Run("responds with 500 for lines that do not decode", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, l)
			first := newServer(t, f.config())
			advance(t, first, l, l.update(t, 0, 5))
			settle(t, first, l)
			assert.Length(t, keys(t, f.store, "lines/"), 1, "the commit must store its lines")

			s := newServer(t, f.config())
			overwriteAll(t, f.store, "lines/", []byte{0xff})
			assert.Equal(t, get(t, s, l.origin).Code, http.StatusInternalServerError,
				"the route must report the corrupt lines")
		})

		t.Run("serves a newer update of another process after a refresh of a state older than a minute",
			func(t *testing.T) {
				t.Parallel()
				f := newFixture(t, l)
				s := newServer(t, f.config())
				advance(t, s, l, l.update(t, 0, 5))
				settle(t, s, l)
				assert.Length(t, keys(t, f.store, "lines/"), 1, "the first commit must store its lines")

				other := newServer(t, f.config())
				newer := advance(t, other, l, l.update(t, 5, 7))

				assert.Equal(t, get(t, s, l.origin).Body.String()[:len(l.notes[5])], string(l.notes[5]),
					"the route must serve its state before the refresh")

				f.clock.Advance(2 * time.Minute)

				want := string(l.notes[7]) + string(newer)
				assert.Eventually(t, patience, time.Millisecond, func(tb assert.TB) {
					assert.Equal(tb, get(t, s, l.origin).Body.String(), want, "the route must serve the newer update")
				}, "the route must serve the newer update after the refresh")
			})
	})
}

// TestRouteAllocs checks the allocation contract of the route of a
// checkpoint that BenchmarkRoute states. MaxAllocs counts the allocations
// of the whole process, so the test does not run in parallel.
//
//nolint:paralleltest // see above
func TestRouteAllocs(t *testing.T) {
	t.Run("Checkpoint", func(t *testing.T) {
		c := newCheckpointCall(t)
		expect.MaxAllocs(t, c.serve, 0, "the route must serve a cached checkpoint without an allocation")
		assert.Equal(t, c.w.code, http.StatusOK, "the test must measure a served checkpoint")
		assert.Equal(t, c.w.n, c.want, "the test must measure the note and the lines")
	})
}

func BenchmarkRoute(b *testing.B) {
	b.Run("Checkpoint", func(b *testing.B) {
		call := newCheckpointCall(b)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			call.serve()
		}

		assert.Equal(b, call.w.code, http.StatusOK, "the benchmark must measure a served checkpoint")
		assert.Equal(b, call.w.n, call.want, "the benchmark must measure the note and the lines")
	})
}

// get sends a GET of the monitor retrieval route of origin to the handler
// of s, and returns the response.
func get(tb testing.TB, s *witness.Server, origin checkpoint.Origin) *httptest.ResponseRecorder {
	tb.Helper()

	rec := httptest.NewRecorder()
	path := monitoringPath + "/" + originHashText(origin) + "/checkpoint"
	s.Checkpoint().ServeHTTP(rec, httptest.NewRequestWithContext(bounded(tb), http.MethodGet, path, nil))

	return rec
}

// originHashText returns the lowercase hexadecimal SHA-256 of origin.
func originHashText(origin checkpoint.Origin) string {
	sum := sha256.Sum256([]byte(origin))

	return hex.EncodeToString(sum[:])
}

// deleteAll deletes every object of st under prefix.
func deleteAll(tb testing.TB, st *store, prefix string) {
	tb.Helper()

	for _, key := range keys(tb, st, prefix) {
		assert.NoError(tb, st.Store.Delete(tb.Context(), key, version.Unspecified), "Delete must delete "+key)
	}
}

// overwriteAll replaces every object of st under prefix with data.
func overwriteAll(tb testing.TB, st *store, prefix string, data []byte) {
	tb.Helper()

	for _, key := range keys(tb, st, prefix) {
		_, err := blob.PutBytes(tb.Context(), st.Store, key, data, blob.PutOptions{})
		assert.NoError(tb, err, "Put must overwrite "+key)
	}
}

// keys returns the keys of the objects of st under prefix, from a walk of
// every page.
func keys(tb testing.TB, st *store, prefix string) []string {
	tb.Helper()

	var out []string

	for token := ""; ; {
		cur, err := st.Store.List(tb.Context(), prefix, page.Page{Token: token})
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
