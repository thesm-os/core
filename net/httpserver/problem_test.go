// Copyright Thesmos 2026
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

	"go.dokimi.dev/assert/bench"
	"go.thesmos.sh/testkit"

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
				name: "writes 413 for an error of a body beyond the limit, whatever its class",
				err:  errs.WithClass(&http.MaxBytesError{Limit: 8}, errs.Invalid),
				want: 413,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				rec := httptest.NewRecorder()
				httpserver.Error(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil), tt.err)

				testkit.Equal(t, rec.Code, tt.want, "the status")
				testkit.Equal(t, rec.Header().Get("Content-Type"), problemJSON, "the type of the body")
				testkit.Equal(t, rec.Header().Get("X-Content-Type-Options"), "nosniff", "the sniffing of the body")
				testkit.Equal(t, rec.Body.String(),
					`{"type":"about:blank","title":"`+http.StatusText(tt.want)+`","status":`+strconv.Itoa(tt.want)+`}`,
					"the problem details, without the text of the error")
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
				testkit.Equal(t, rec.Header().Get("Retry-After"), tt.want, "the Retry-After header")
			})
		}

		t.Run("removes a Content-Length that the handler set for another body", func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			rec.Header().Set("Content-Length", "1000")
			httpserver.Error(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil), errBoom)
			testkit.Equal(t, rec.Header().Get("Content-Length"), "", "the Content-Length")
		})

		t.Run("records the first of two errors of a request of a Server", func(t *testing.T) {
			t.Parallel()

			first, second := errs.WithClass(errBoom, errs.NotFound), errs.WithClass(errBoom, errs.Conflict)
			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				httpserver.Error(w, r, first)
				httpserver.Error(w, r, second)
			}))
			testkit.Equal(t, f.send(t, http.MethodGet, "/", http.NoBody, nil).status, http.StatusNotFound, "the status")
			testkit.NoError(t, f.stop(), "Run must drain")

			logged, _ := value(t, &f.logs.find(messageRequest)[0], keyError).Any().(error)
			testkit.ErrorIs(t, logged, first, "the error of the record")
			testkit.ErrorIsNot(t, logged, second, "the record must contain the first error")
		})

		t.Run("only records the error when the handler wrote the header", func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, body)
				httpserver.Error(w, r, errs.WithClass(errBoom, errs.Transient))
			}))
			got := f.send(t, http.MethodGet, "/", http.NoBody, nil)
			testkit.Equal(t, got.status, http.StatusOK, "the status of the response")
			testkit.Equal(t, got.body, body, "the body of the response")
			testkit.NoError(t, f.stop(), "Run must drain")

			r := f.logs.find(messageRequest)[0]
			testkit.Equal(t, r.Level, slog.LevelInfo, "the level of a response of 200")
			logged, _ := value(t, &r, keyError).Any().(error)
			testkit.ErrorIs(t, logged, errBoom, "the error")
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
		err := errs.WithClass(errBoom, errs.NotFound)

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			httpserver.Error(w, req, err)
		}

		testkit.Equal(b, w.status, http.StatusNotFound, "the benchmark must measure a problem response")
	})

	b.Run("Error with a Retry-After", func(b *testing.B) {
		err := errs.WithRetryAfter(errs.WithClass(errBoom, errs.Transient), time.Second)

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			httpserver.Error(w, req, err)
		}

		testkit.Equal(b, w.status, http.StatusServiceUnavailable, "the benchmark must measure a problem response")
	})
}
