// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package httpserver

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/telemetry/noop"
)

// discard is a ResponseWriter that keeps the status of its last response
// and discards every body. Its header map keeps the keys of earlier
// responses, as the map of one response of net/http keeps the keys of its
// handler.
type discard struct {
	header http.Header
	status int
}

// Header returns the header map of the writer.
func (w *discard) Header() http.Header { return w.header }

// WriteHeader keeps code.
func (w *discard) WriteHeader(code int) { w.status = code }

// Write discards b.
func (*discard) Write(b []byte) (int, error) { return len(b), nil }

// enabled is a slog.Handler that handles every level and discards every
// record, so an allocation ceiling counts the allocations of the record and
// not of a handler.
type enabled struct{}

// Enabled reports true.
func (enabled) Enabled(context.Context, slog.Level) bool { return true }

// Handle discards r.
func (enabled) Handle(context.Context, slog.Record) error { return nil }

// WithAttrs returns the handler.
func (h enabled) WithAttrs([]slog.Attr) slog.Handler { return h }

// WithGroup returns the handler.
func (h enabled) WithGroup(string) slog.Handler { return h }

// traced is the noop reporter with a tracer whose spans have the identity
// of a sampled trace and record nothing, so an allocation ceiling counts
// the work of the chain for a traced request and none of a tracer.
type traced struct{ noop.Reporter }

// Tracer returns a tracer of spans with an identity.
func (traced) Tracer(telemetry.InstrumentName) telemetry.Tracer { return tracer{} }

// tracer starts spans with the identity of a sampled trace.
type tracer struct{}

var _ telemetry.Tracer = tracer{}

// Start returns ctx and a span with an identity.
func (tracer) Start(
	ctx context.Context,
	_ telemetry.SpanName,
	_ ...telemetry.SpanOption,
) (context.Context, telemetry.Span) {
	return ctx, span{}
}

// span is a span with the identity of a sampled trace that records nothing.
type span struct{}

var _ telemetry.Span = span{}

// End does nothing.
func (span) End(error) {}

// SetAttributes does nothing.
func (span) SetAttributes([]telemetry.Attr) {}

// AddEvent does nothing.
func (span) AddEvent(telemetry.EventName, []telemetry.Attr) {}

// SpanContext returns the identity of a sampled trace.
func (span) SpanContext() telemetry.SpanContext {
	return telemetry.SpanContext{TraceID: "4bf92f3577b34da6a3ce929d0e0e4736", SpanID: "00f067aa0ba902b7", Sampled: true}
}

// serveCase is a request of the allocation ceilings of the chain: the
// handler and the options of the server, the request, and the status and
// the ceiling of its response.
type serveCase struct {
	handler http.HandlerFunc
	header  http.Header
	opts    []Option
	name    string
	method  string
	body    string
	path    string
	length  int64
	status  int
	want    uint64
}

// TestChainAllocs checks the allocation ceilings of the chain that the
// package documentation states: 2 objects for a request, and 3 for a body
// of unknown length or a traced request that matched a route. MaxAllocs
// counts the allocations of the whole process, so the test does not run in
// parallel.
func TestChainAllocs(t *testing.T) {
	t.Run("ServeHTTP", func(t *testing.T) {
		for _, tt := range serveCases() {
			t.Run(tt.name, func(t *testing.T) {
				h, req, body := serving(t, &tt)
				w := &discard{header: http.Header{}}

				expect.MaxAllocs(t, func() {
					body.Reset(tt.body)
					h.ServeHTTP(w, req)
				}, tt.want, "the chain must allocate as the package documentation states")
				assert.Equal(t, w.status, tt.status, "the test must measure the response of the case")
			})
		}
	})
}

// BenchmarkChain reports the cost of the chain, and fails above the
// ceilings that the package documentation states.
func BenchmarkChain(b *testing.B) {
	b.Run("ServeHTTP", func(b *testing.B) {
		for _, tt := range serveCases() {
			b.Run(tt.name, func(b *testing.B) {
				h, req, body := serving(b, &tt)
				w := &discard{header: http.Header{}}

				c := bench.Start(b).MaxAllocs(tt.want)
				defer c.End()

				for c.Loop() {
					body.Reset(tt.body)
					h.ServeHTTP(w, req)
				}

				assert.Equal(b, w.status, tt.status, "the benchmark must measure the response of the case")
			})
		}
	})
}

// serveCases returns the requests of the allocation ceilings of the chain.
func serveCases() []serveCase {
	attrs := []telemetry.Attr{telemetry.AttrString("tenant", "acme"), telemetry.AttrString("plan", "enterprise")}
	notFound := errs.WithClass(errors.New("no such item"), errs.NotFound)
	noContent := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }

	routes := http.NewServeMux()
	routes.HandleFunc("GET /items", noContent)

	return []serveCase{
		{
			name:    "of a request without a body",
			handler: noContent,
			status:  http.StatusNoContent,
			want:    2,
		},
		{
			name:    "of a request with the trace of a caller",
			handler: noContent,
			header:  http.Header{"Traceparent": {"00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"}},
			status:  http.StatusNoContent,
			want:    2,
		},
		{
			name:    "of a request with a declared body",
			handler: noContent,
			method:  http.MethodPost,
			body:    "body",
			length:  4,
			status:  http.StatusNoContent,
			want:    2,
		},
		{
			name:    "of a request with a body of unknown length",
			handler: noContent,
			method:  http.MethodPost,
			body:    "body",
			length:  -1,
			status:  http.StatusNoContent,
			want:    3,
		},
		{
			name:    "of a request that declares a body beyond the limit",
			handler: noContent,
			method:  http.MethodPost,
			body:    "body",
			length:  5 << 20,
			status:  http.StatusRequestEntityTooLarge,
			want:    2,
		},
		{
			name:    "of a request that the cross-origin protection refuses",
			handler: noContent,
			method:  http.MethodPost,
			header:  http.Header{"Sec-Fetch-Site": {"cross-site"}},
			status:  http.StatusForbidden,
			want:    2,
		},
		{
			name:    "of a request whose handler calls Error",
			handler: func(w http.ResponseWriter, r *http.Request) { Error(w, r, notFound) },
			status:  http.StatusNotFound,
			want:    2,
		},
		{
			name: "of a request that a handler annotates",
			handler: func(w http.ResponseWriter, r *http.Request) {
				Annotate(r.Context(), attrs...)
				w.WriteHeader(http.StatusNoContent)
			},
			status: http.StatusNoContent,
			want:   2,
		},
		{
			name:    "of a request with a log record",
			handler: noContent,
			opts:    []Option{WithLogger(slog.New(enabled{}))},
			status:  http.StatusNoContent,
			want:    2,
		},
		{
			name:    "of a traced request that matched a route",
			handler: routes.ServeHTTP,
			opts:    []Option{WithReporter(traced{})},
			path:    "/items",
			status:  http.StatusNoContent,
			want:    3,
		},
	}
}

// serving returns the chain of a server of tt, a request of tt, and the
// reader of its body, which the caller resets before each request. It
// fails tb when New refuses the options of tt.
func serving(tb testing.TB, tt *serveCase) (http.Handler, *http.Request, *strings.Reader) {
	tb.Helper()

	s, err := New(tt.handler, append([]Option{required}, tt.opts...)...)
	assert.NoError(tb, err, "New must accept the options")

	target := cmp.Or(tt.path, "/")
	body := strings.NewReader(tt.body)
	req := httptest.NewRequestWithContext(tb.Context(), http.MethodGet, target, nil)
	if tt.method != "" {
		req = httptest.NewRequestWithContext(tb.Context(), tt.method, target, body)
		req.ContentLength = tt.length
	}

	maps.Copy(req.Header, tt.header)

	return s.srv.Handler, req, body
}
