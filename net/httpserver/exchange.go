// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package httpserver

import (
	"bufio"
	"cmp"
	"context"
	"io"
	"net"
	"net/http"
	"sync"

	"go.thesmos.sh/core/telemetry"
)

// exchangeKey is the key under which the context of a request returns its
// exchange.
type exchangeKey struct{}

// inlineAttrs is the number of attributes of [Annotate] that an exchange
// stores without an allocation.
const inlineAttrs = 4

// exchange is the state of one request that the chain serves. It is two
// things at once, so that the chain allocates one value per request:
//
//   - The context of the request. It embeds the context that net/http
//     created and returns itself under exchangeKey, so [Annotate] and
//     [Error] find it.
//   - The http.ResponseWriter that the middleware and the handler receive.
//     It records the status and the bytes of the response, and passes every
//     call to the ResponseWriter of net/http.
//
// The exchange implements http.Flusher, http.Hijacker, io.ReaderFrom and
// io.StringWriter over the ResponseWriter of net/http, and its Unwrap
// returns that ResponseWriter, so an http.ResponseController sets the
// deadlines and the full-duplex mode of the connection through it.
//
// # Concurrency
//
// The methods of the ResponseWriter, and Error, are for the goroutine that
// serves the request, as net/http requires of a ResponseWriter. [Annotate]
// is safe for concurrent use: a mutex guards attrs, err and done.
//
// # Allocation contract
//
// One allocation per request, which contains the context, the writer, the
// options of the span, the first four attributes of Annotate and the values
// of the headers of a problem response.
type exchange struct {
	context.Context //nolint:containedctx // the exchange is the request's context, as a valueCtx is

	w    http.ResponseWriter
	span telemetry.Span

	// err is the first error that Error recorded for the request.
	err error

	// remote is the trace ID of the context that the propagator extracted,
	// for the log record of a span without an identity.
	remote telemetry.TraceID

	// route is the template of the pattern that the ServeMux matched.
	route string

	// header contains the values of the headers of a problem response: its
	// media type, nosniff, and the delay of Retry-After. The header map of
	// the response refers to them, so a problem response allocates no
	// values.
	header [3]string

	// attrs are the attributes of Annotate. Their storage is inline until
	// a request has more than four.
	attrs []telemetry.Attr

	// opts are the options of the span: its kind, and its remote parent.
	opts [2]telemetry.SpanOption

	inline [inlineAttrs]telemetry.Attr

	// bytes is the number of bytes of the body written.
	bytes int64

	// status is the status of the response, and 0 before the header is
	// written.
	status int

	mu sync.Mutex

	// done reports that the chain has finished the request, after which
	// Annotate adds nothing.
	done bool
}

// Value returns the exchange for exchangeKey, and the value of the context
// of net/http for every other key.
func (e *exchange) Value(key any) any {
	if key == (exchangeKey{}) {
		return e
	}

	return e.Context.Value(key)
}

// Header returns the header map of the response.
func (e *exchange) Header() http.Header { return e.w.Header() }

// WriteHeader writes the header of the response with code, and records code
// as the status unless it is informational. An informational status, 1xx
// other than 101, precedes the status of the response.
func (e *exchange) WriteHeader(code int) {
	if e.status == 0 && (code < http.StatusContinue || code >= http.StatusOK || code == http.StatusSwitchingProtocols) {
		e.status = code
	}

	e.w.WriteHeader(code)
}

// Write writes b to the body of the response, after a header of 200 when
// none is written, and counts the bytes written.
func (e *exchange) Write(b []byte) (int, error) {
	e.status = cmp.Or(e.status, http.StatusOK)
	n, err := e.w.Write(b)
	e.bytes += int64(n)

	return n, err //nolint:wrapcheck // a ResponseWriter returns the error of the writer under it
}

// WriteString writes s as Write writes b, without converting s to a slice.
func (e *exchange) WriteString(s string) (int, error) {
	e.status = cmp.Or(e.status, http.StatusOK)
	n, err := io.WriteString(e.w, s)
	e.bytes += int64(n)

	return n, err //nolint:wrapcheck // a ResponseWriter returns the error of the writer under it
}

// ReadFrom copies r to the body of the response through io.Copy, which
// uses the ReadFrom of net/http's ResponseWriter, and counts the bytes
// written.
func (e *exchange) ReadFrom(r io.Reader) (int64, error) {
	e.status = cmp.Or(e.status, http.StatusOK)
	n, err := io.Copy(e.w, r)
	e.bytes += n

	return n, err //nolint:wrapcheck // a ResponseWriter returns the error of the writer under it
}

// Flush sends the buffered response to the client, after a header of 200
// when none is written. A ResponseWriter that cannot flush ignores it.
func (e *exchange) Flush() {
	e.status = cmp.Or(e.status, http.StatusOK)
	_ = http.NewResponseController(e.w).Flush() //nolint:errcheck // http.Flusher returns no error
}

// Hijack takes over the connection of the request, as http.Hijacker
// describes, and records 101 as the status when none is written. HTTP/2
// returns http.ErrNotSupported.
func (e *exchange) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(e.w).Hijack()
	if err == nil && e.status == 0 {
		e.status = http.StatusSwitchingProtocols
	}

	return conn, rw, err //nolint:wrapcheck // a ResponseWriter returns the error of the writer under it
}

// Unwrap returns the ResponseWriter of net/http, for
// http.ResponseController.
func (e *exchange) Unwrap() http.ResponseWriter { return e.w }

// end marks the request finished, so that Annotate and Error record nothing
// more, and returns the status of the response: the status written, 500
// for a request whose handler panicked before it wrote a header, and 200,
// which net/http sends, for a handler that wrote nothing.
func (e *exchange) end(panicked bool) int {
	e.mu.Lock()
	e.done = true
	e.mu.Unlock()

	if e.status != 0 {
		return e.status
	}

	if panicked {
		return http.StatusInternalServerError
	}

	return http.StatusOK
}

// Annotate adds attrs to the log record and to the span of the request of
// ctx. A handler or a middleware calls it, for example with the tenant of
// the request. Annotate does nothing for a context that is not the context
// of a request of a [Server], or whose request the Server has finished. The
// span receives the attributes when it has a trace identity.
//
// # Concurrency
//
// Safe for concurrent use, also from goroutines that the handler starts.
//
// # Allocation contract
//
// Zero alloc for the first four attributes of a request, apart from what
// the span allocates. Later attributes grow the list of the request.
func Annotate(ctx context.Context, attrs ...telemetry.Attr) {
	e, ok := ctx.Value(exchangeKey{}).(*exchange)
	if !ok {
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if e.done {
		return
	}

	n := len(e.attrs)
	e.attrs = append(e.attrs, attrs...)

	// The span takes the slice by reference, so it receives the part that
	// this call added, which later calls do not change. A span without a
	// trace identity records nothing.
	if e.span.SpanContext().TraceID != "" {
		e.span.SetAttributes(e.attrs[n:len(e.attrs):len(e.attrs)])
	}
}
