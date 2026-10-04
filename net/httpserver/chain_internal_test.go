// Copyright Thesmos 2026
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

	"go.dokimi.dev/assert/bench"
	"go.thesmos.sh/testkit"

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
// record, so a benchmark counts the allocations of the record and not of a
// handler.
type enabled struct{}

// Enabled reports true.
func (enabled) Enabled(context.Context, slog.Level) bool { return true }

// Handle discards r.
//
//nolint:gocritic // hugeParam: slog.Handler passes the Record by value
func (enabled) Handle(context.Context, slog.Record) error { return nil }

// WithAttrs returns the handler.
func (h enabled) WithAttrs([]slog.Attr) slog.Handler { return h }

// WithGroup returns the handler.
func (h enabled) WithGroup(string) slog.Handler { return h }

// traced is the noop reporter with a tracer whose spans have the identity
// of a sampled trace and record nothing, so a benchmark counts the work of
// the chain for a traced request and none of a tracer.
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

func BenchmarkChain(b *testing.B) {
	attrs := []telemetry.Attr{telemetry.AttrString("tenant", "acme"), telemetry.AttrString("plan", "enterprise")}
	notFound := errs.WithClass(errors.New("no such item"), errs.NotFound)
	noContent := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }

	routes := http.NewServeMux()
	routes.HandleFunc("GET /items", noContent)

	tests := []struct {
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
	}{
		{
			name:    "ServeHTTP of a request without a body",
			handler: noContent,
			status:  http.StatusNoContent,
			want:    2,
		},
		{
			name:    "ServeHTTP of a request with the trace of a caller",
			handler: noContent,
			header:  http.Header{"Traceparent": {"00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"}},
			status:  http.StatusNoContent,
			want:    2,
		},
		{
			name:    "ServeHTTP of a request with a declared body",
			handler: noContent,
			method:  http.MethodPost,
			body:    "body",
			length:  4,
			status:  http.StatusNoContent,
			want:    2,
		},
		{
			name:    "ServeHTTP of a request with a body of unknown length",
			handler: noContent,
			method:  http.MethodPost,
			body:    "body",
			length:  -1,
			status:  http.StatusNoContent,
			want:    3,
		},
		{
			name:    "ServeHTTP of a request that declares a body beyond the limit",
			handler: noContent,
			method:  http.MethodPost,
			body:    "body",
			length:  5 << 20,
			status:  http.StatusRequestEntityTooLarge,
			want:    2,
		},
		{
			name:    "ServeHTTP of a request that the cross-origin protection refuses",
			handler: noContent,
			method:  http.MethodPost,
			header:  http.Header{"Sec-Fetch-Site": {"cross-site"}},
			status:  http.StatusForbidden,
			want:    2,
		},
		{
			name:    "ServeHTTP of a request whose handler calls Error",
			handler: func(w http.ResponseWriter, r *http.Request) { Error(w, r, notFound) },
			status:  http.StatusNotFound,
			want:    2,
		},
		{
			name: "ServeHTTP of a request that a handler annotates",
			handler: func(w http.ResponseWriter, r *http.Request) {
				Annotate(r.Context(), attrs...)
				w.WriteHeader(http.StatusNoContent)
			},
			status: http.StatusNoContent,
			want:   2,
		},
		{
			name:    "ServeHTTP of a request with a log record",
			handler: noContent,
			opts:    []Option{WithLogger(slog.New(enabled{}))},
			status:  http.StatusNoContent,
			want:    2,
		},
		{
			name:    "ServeHTTP of a traced request that matched a route",
			handler: routes.ServeHTTP,
			opts:    []Option{WithReporter(traced{})},
			path:    "/items",
			status:  http.StatusNoContent,
			want:    3,
		},
	}
	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			s, err := New(tt.handler, append([]Option{required}, tt.opts...)...)
			testkit.NoError(b, err, "New must accept the options")

			target := cmp.Or(tt.path, "/")
			body := strings.NewReader(tt.body)
			req := httptest.NewRequestWithContext(b.Context(), http.MethodGet, target, nil)
			if tt.method != "" {
				req = httptest.NewRequestWithContext(b.Context(), tt.method, target, body)
				req.ContentLength = tt.length
			}

			maps.Copy(req.Header, tt.header)

			w := &discard{header: http.Header{}}

			c := bench.Start(b).MaxAllocs(tt.want)
			defer c.End()

			for c.Loop() {
				body.Reset(tt.body)
				s.srv.Handler.ServeHTTP(w, req)
			}

			testkit.Equal(b, w.status, tt.status, "the benchmark must measure the response of the case")
		})
	}
}
