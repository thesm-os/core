// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package httpserver_test

import (
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/net/httpserver"
)

func TestProblem(t *testing.T) {
	t.Parallel()

	t.Run("Error", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			err  error
			name string
			want int
		}{
			{name: "writes 400 for an Invalid error", err: errs.WithClass(errBoom, errs.Invalid), want: 400},
			{name: "writes 403 for a Denied error", err: errs.WithClass(errBoom, errs.Denied), want: 403},
			{name: "writes 404 for a NotFound error", err: errs.WithClass(errBoom, errs.NotFound), want: 404},
			{name: "writes 409 for a Conflict error", err: errs.WithClass(errBoom, errs.Conflict), want: 409},
			{name: "writes 501 for an Unsupported error", err: errs.WithClass(errBoom, errs.Unsupported), want: 501},
			{name: "writes 503 for a Transient error", err: errs.WithClass(errBoom, errs.Transient), want: 503},
			{name: "writes 500 for an Integrity error", err: errs.WithClass(errBoom, errs.Integrity), want: 500},
			{name: "writes 500 for an error without a class", err: errBoom, want: 500},
			{
				name: "writes 413 for an error of a body beyond the limit of any class",
				err:  errs.WithClass(&http.MaxBytesError{Limit: 8}, errs.Invalid),
				want: 413,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				rec := httptest.NewRecorder()
				httpserver.Error(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil), tt.err)

				expect.Equal(t, rec.Code, tt.want, "Error must write the status of the class")
				expect.Equal(t, rec.Header().Get("Content-Type"), problemJSON, "the body must be problem details")
				expect.Equal(t, rec.Header().Get("X-Content-Type-Options"), "nosniff",
					"a browser must not sniff the body")
				expect.Equal(t, rec.Body.String(),
					`{"type":"about:blank","title":"`+http.StatusText(tt.want)+`","status":`+strconv.Itoa(tt.want)+`}`,
					"the problem details must contain no text of the error")
			})
		}

		delays := []struct {
			err  error
			name string
			want string
		}{
			{
				name: "writes Retry-After of a delay rounded up to whole seconds",
				err:  errs.WithRetryAfter(errs.WithClass(errBoom, errs.Transient), 1500*time.Millisecond),
				want: "2",
			},
			{
				name: "writes Retry-After of a delay of whole seconds",
				err:  errs.WithRetryAfter(errs.WithClass(errBoom, errs.Transient), time.Second),
				want: "1",
			},
			{
				name: "writes Retry-After of the longest delay without an overflow",
				err:  errs.WithRetryAfter(errs.WithClass(errBoom, errs.Transient), math.MaxInt64),
				want: "9223372037",
			},
			{
				name: "writes no Retry-After for a Transient error without a delay",
				err:  errs.WithClass(errBoom, errs.Transient),
			},
			{
				name: "writes no Retry-After for a status other than 503",
				err:  errs.WithRetryAfter(errs.WithClass(errBoom, errs.Invalid), time.Second),
			},
		}
		for _, tt := range delays {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				rec := httptest.NewRecorder()
				httpserver.Error(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil), tt.err)
				assert.Equal(t, rec.Header().Get("Retry-After"), tt.want, "Error must write the delay of the error")
			})
		}

		t.Run("removes a Content-Length that the handler set for another body", func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			rec.Header().Set("Content-Length", "1000")
			httpserver.Error(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil), errBoom)
			assert.Empty(t, rec.Header().Get("Content-Length"), "Error must remove the length of another body")
		})

		t.Run("records the first of two errors of a request of a Server", func(t *testing.T) {
			t.Parallel()
			first, second := errs.WithClass(errBoom, errs.NotFound), errs.WithClass(errBoom, errs.Conflict)
			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				httpserver.Error(w, r, first)
				httpserver.Error(w, r, second)
			}))
			assert.Equal(t, f.send(t, http.MethodGet, "/", http.NoBody, nil).status, http.StatusNotFound,
				"the response must have the status of the first error")
			assert.NoError(t, f.stop(), "Run must drain")

			logged, _ := value(t, &f.logs.find(messageRequest)[0], keyError).Any().(error)
			expect.That(t, logged).
				ErrorIs(first, "the record must contain the first error").
				ErrorIsNot(second, "the record must not contain the second error")
		})

		t.Run("only records the error when the handler wrote the header", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, body)
				httpserver.Error(w, r, errs.WithClass(errBoom, errs.Transient))
			}))
			got := f.send(t, http.MethodGet, "/", http.NoBody, nil)
			expect.Equal(t, got.status, http.StatusOK, "the response must keep the status that the handler wrote")
			expect.Equal(t, got.body, body, "the response must keep the body that the handler wrote")
			assert.NoError(t, f.stop(), "Run must drain")

			r := f.logs.find(messageRequest)[0]
			expect.Equal(t, r.Level, slog.LevelInfo, "the record of a response of 200 must have the level Info")
			logged, _ := value(t, &r, keyError).Any().(error)
			expect.ErrorIs(t, logged, errBoom, "the record must contain the error")
		})
	})
}

// TestProblemAllocs checks the allocation contract of Error for a request
// of another server, whose header values need storage of their own. The
// allocation test of the chain checks Error for a request of a Server.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
func TestProblemAllocs(t *testing.T) {
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	w := &discardWriter{header: http.Header{}}

	t.Run("Error", func(t *testing.T) {
		t.Run("of a NotFound error", func(t *testing.T) {
			err := errs.WithClass(errBoom, errs.NotFound)
			expect.MaxAllocs(t, func() { httpserver.Error(w, req, err) }, 1,
				"Error must allocate the storage of the header values alone")
			assert.Equal(t, w.status, http.StatusNotFound, "the test must measure a problem response")
		})

		t.Run("with a Retry-After", func(t *testing.T) {
			err := errs.WithRetryAfter(errs.WithClass(errBoom, errs.Transient), time.Second)
			expect.MaxAllocs(t, func() { httpserver.Error(w, req, err) }, 1,
				"Error must allocate the storage of the header values alone")
			assert.Equal(t, w.status, http.StatusServiceUnavailable, "the test must measure a problem response")
		})
	})
}

// BenchmarkProblem measures Error for a request of another server, whose
// header values need storage of their own. The benchmark of the chain
// measures Error for a request of a Server.
func BenchmarkProblem(b *testing.B) {
	req := httptest.NewRequestWithContext(b.Context(), http.MethodGet, "/", nil)
	w := &discardWriter{header: http.Header{}}

	b.Run("Error", func(b *testing.B) {
		b.Run("of a NotFound error", func(b *testing.B) {
			err := errs.WithClass(errBoom, errs.NotFound)

			c := bench.Start(b).MaxAllocs(1)
			defer c.End()

			for c.Loop() {
				httpserver.Error(w, req, err)
			}

			assert.Equal(b, w.status, http.StatusNotFound, "the benchmark must measure a problem response")
		})

		b.Run("with a Retry-After", func(b *testing.B) {
			err := errs.WithRetryAfter(errs.WithClass(errBoom, errs.Transient), time.Second)

			c := bench.Start(b).MaxAllocs(1)
			defer c.End()

			for c.Loop() {
				httpserver.Error(w, req, err)
			}

			assert.Equal(b, w.status, http.StatusServiceUnavailable, "the benchmark must measure a problem response")
		})
	})
}
