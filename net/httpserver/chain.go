// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package httpserver

import (
	"cmp"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"time"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/net/internal/semconv"
	"go.thesmos.sh/core/telemetry"
)

// The names of the server's telemetry.
const (
	// scope is the instrumentation scope of the server's tracer.
	scope telemetry.InstrumentName = "go.thesmos.sh/core/net/httpserver"

	// durationDescription is the description of the duration histogram,
	// as the semantic conventions word it.
	durationDescription = "Duration of HTTP server requests."
)

// The message and the keys of the log record of a request. The keys of the
// method, the route and the status are the attribute keys of the semantic
// conventions, in internal/semconv.
const (
	msgRequest = "request"

	keyDuration = "duration"
	keyBodySize = "http.response.body.size"
	keyTraceID  = "trace_id"
	keyError    = "error"
	keyPanic    = "panic"
	keyStack    = "stack"
)

// The schemes of url.scheme.
const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

// unbounded is the body limit and the in-flight bound that a Server stores
// for a limit that is off: a negative WithMaxBodyBytes, and a negative or
// absent WithMaxInFlight. New refuses a zero limit, so zero is free to mean
// no limit.
const unbounded = 0

// logAttrs is the number of attributes of a log record that the chain
// builds on its stack: five of the request, the trace ID and the error,
// and five for a panic and the attributes of Annotate.
const logAttrs = 12

// The errors with which the chain ends the span of a request that failed.
// No caller receives them, so they have no class.
var (
	// errStatus ends the span of a response of a status of 500 and above
	// for which the handler recorded no error with Error.
	errStatus = errors.New("httpserver: the response has a server error status")

	// errPanic ends the span of a request whose handler panicked.
	errPanic = errors.New("httpserver: the handler panicked")
)

// serverKind is the option of the kind of every span of the server.
var serverKind = telemetry.WithSpanKind(telemetry.SpanKindServer)

// series identifies an attribute set of http.server.request.duration.
type series struct {
	method string
	route  string
	scheme string
	status int
}

// attrs returns the attributes of s, without the route when the request
// matched none. It allocates the slice once, with room for every
// attribute.
func (s series) attrs() []telemetry.Attr {
	attrs := make([]telemetry.Attr, 0, 4)
	attrs = append(attrs,
		telemetry.AttrString(semconv.RequestMethod, s.method),
		telemetry.AttrInt(semconv.ResponseStatusCode, int64(s.status)),
		telemetry.AttrString(semconv.URLScheme, s.scheme),
	)

	if s.route != "" {
		attrs = append(attrs, telemetry.AttrString(semconv.Route, s.route))
	}

	return attrs
}

// chain is the handler of the http.Server. It serves each request through
// the steps of the server around next, which is the middleware and the
// admission: recovery, the span, the body limit, and when the request ends,
// its duration, its log record and the end of its span.
//
// # Concurrency
//
// Safe for concurrent use. Its fields do not change after New.
//
// # Allocation contract
//
// ServeHTTP allocates the exchange of the request and the copy of the
// request with the exchange as its context, which Request.WithContext
// makes. A body of unknown length allocates the reader of
// http.MaxBytesReader, and a span with a trace identity its attributes. A
// problem response that the chain writes allocates nothing of its own. The
// tracer, the propagator, the logger's handler, the middleware and the
// handler allocate on their own.
type chain struct {
	next       http.Handler
	clock      clock.Clock
	logger     *slog.Logger
	tracer     telemetry.Tracer
	propagator telemetry.Propagator
	durations  *semconv.Durations[series]

	// maxBody is the body limit, or unbounded.
	maxBody int64
}

// ServeHTTP serves r through the chain. It extracts the caller's trace,
// starts the server span, bounds the body, and calls next with the
// exchange as the ResponseWriter and as the context of the request. It
// then finishes the request, also when next panics.
//
// net/http stops a body at its declared length, for HTTP/1.1 and HTTP/2
// alike, so a declared length within the limit needs no reader of its own.
// A request that declares a longer body receives 413 before next runs. A
// body of unknown length is bounded as next reads it.
func (c *chain) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// unknownLength is the ContentLength that net/http gives a request whose
	// body has no declared length, such as a chunked body.
	const unknownLength = -1

	start := c.clock.Time()

	e := &exchange{Context: r.Context(), w: w}
	e.attrs = e.inline[:0]
	e.opts[0] = serverKind
	opts := e.opts[:1]

	// The exchange embeds the context of r, which contextcheck cannot see.
	if parent, ok := c.propagator.Extract(e, telemetry.HeaderCarrier(r.Header)); ok { //nolint:contextcheck // see above
		e.remote = parent.TraceID
		e.opts[1] = telemetry.WithRemoteParent(parent)
		opts = e.opts[:2]
	}

	ctx, span := c.tracer.Start(e, telemetry.SpanName(semconv.Method(r.Method)), opts...)
	e.span = span

	r = r.WithContext(ctx) //nolint:contextcheck // ctx derives from the exchange, which embeds the context of r

	defer func() { c.finish(e, r, start, recover()) }()

	if c.maxBody != unbounded && r.ContentLength > c.maxBody {
		writeProblem(e, r, http.StatusRequestEntityTooLarge, 0)

		return
	}

	if c.maxBody != unbounded && r.ContentLength == unknownLength {
		// The reader receives the ResponseWriter of net/http, which closes
		// the connection after a body that exceeds the limit.
		r.Body = http.MaxBytesReader(w, r.Body, c.maxBody)
	}

	c.next.ServeHTTP(e, r)
}

// finish records the request after next returned or panicked with
// recovered. It writes 500 for a panic before the handler wrote a header,
// records the duration, writes the log record of the request and ends its
// span.
//
// A panic with http.ErrAbortHandler, and a panic after the handler wrote
// the header, abort the response: finish panics with http.ErrAbortHandler,
// which net/http recovers without a log. net/http then closes the
// connection, or resets the stream of HTTP/2, so the client does not read a
// partial response as a complete one.
func (c *chain) finish(e *exchange, r *http.Request, start time.Time, recovered any) {
	panicked := recovered != nil

	// net/http recognises the value of the panic, not an error that wraps
	// it, as its own recover does.
	aborted := recovered == http.ErrAbortHandler //nolint:errorlint // see above
	abort := aborted || panicked && e.status != 0

	if panicked && !abort {
		writeProblem(e, r, http.StatusInternalServerError, 0)
	}

	status := e.end(panicked)
	elapsed := c.clock.Time().Sub(start)

	scheme := schemeHTTP
	if r.TLS != nil {
		scheme = schemeHTTPS
	}

	s := series{method: semconv.Method(r.Method), route: e.route, scheme: scheme, status: status}
	c.durations.Record(r.Context(), s, elapsed)

	// net/http logs no stack for an abort, and neither does the chain.
	var stack []byte
	if panicked && !aborted {
		stack = debug.Stack()
	}

	c.log(e, r, s, elapsed, recovered, stack)
	endSpan(e, s, panicked)

	if abort {
		panic(http.ErrAbortHandler)
	}
}

// log writes the log record of r: at slog.LevelError for a status of 500
// and above, and at slog.LevelInfo otherwise, when the logger handles that
// level. The record contains the method, the route, the status, the
// duration, the bytes of the body, the trace ID, the error that Error
// recorded, the value and the stack of a panic, and the attributes of
// Annotate.
func (c *chain) log(e *exchange, r *http.Request, s series, elapsed time.Duration, recovered any, stack []byte) {
	level := slog.LevelInfo
	if s.status >= http.StatusInternalServerError {
		level = slog.LevelError
	}

	ctx := r.Context()
	//dokimi:mutate-skip sbr-delete: LogAttrs drops a record of a level that the handler does not handle, so the check saves the attributes alone
	if !c.logger.Enabled(ctx, level) {
		return
	}

	var buf [logAttrs]slog.Attr

	attrs := append(buf[:0],
		slog.String(semconv.RequestMethod, r.Method),
		slog.String(semconv.Route, s.route),
		slog.Int(semconv.ResponseStatusCode, s.status),
		slog.Duration(keyDuration, elapsed),
		slog.Int64(keyBodySize, e.bytes),
	)

	// A span without a trace identity leaves the trace ID that the
	// propagator extracted.
	if id := cmp.Or(e.span.SpanContext().TraceID, e.remote); id != "" {
		attrs = append(attrs, slog.String(keyTraceID, string(id)))
	}

	// The chain marked the exchange done before it reads err and attrs, so
	// no call of Error or Annotate writes them any more.
	if e.err != nil {
		attrs = append(attrs, slog.Any(keyError, e.err))
	}

	if stack != nil {
		attrs = append(attrs, slog.Any(keyPanic, recovered), slog.String(keyStack, string(stack)))
	}

	for _, a := range e.attrs {
		attrs = append(attrs, a.SlogAttr())
	}

	c.logger.LogAttrs(ctx, level, msgRequest, attrs...)
}

// endSpan sets the attributes of s on the span of e, when the span has a
// trace identity, and ends the span. The span ends with errPanic for a
// handler that panicked, and for a status of 500 and above with the error
// that Error recorded, or errStatus without one.
func endSpan(e *exchange, s series, panicked bool) {
	// A span without a trace identity, such as the span of the noop
	// tracer, records nothing, so the chain builds no attributes for it.
	if e.span.SpanContext().TraceID != "" {
		e.span.SetAttributes(s.attrs())
	}

	var err error
	if panicked {
		err = errPanic
	} else if s.status >= http.StatusInternalServerError {
		err = cmp.Or(e.err, errStatus)
	}

	e.span.End(err)
}

// admission is the innermost step of the chain. It admits a request under
// the in-flight limit and the cross-origin protection, calls the handler,
// and records the route that the handler matched.
//
// # Concurrency
//
// Safe for concurrent use. inFlight is atomic, and the other fields do not
// change after New.
//
// # Allocation contract
//
// Zero alloc, apart from what the handler and the cross-origin protection
// allocate. The protection parses the Origin header of a request from a
// browser without Sec-Fetch-Site.
type admission struct {
	handler  http.Handler
	origins  *http.CrossOriginProtection
	inFlight atomic.Int64

	// max is the in-flight bound, or unbounded.
	max int64
}

// ServeHTTP admits r and calls the handler. It responds with 503 and
// Retry-After: 1 when the requests in flight are at the bound, and with
// 403 to a cross-origin request that the protection refuses. It records the
// route of r after the handler returns, also when the handler panics.
func (a *admission) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if a.max != unbounded {
		if a.inFlight.Add(1) > a.max {
			a.inFlight.Add(-1)
			writeProblem(w, r, http.StatusServiceUnavailable, time.Second)

			return
		}

		defer a.inFlight.Add(-1)
	}

	if a.origins.Check(r) != nil {
		writeProblem(w, r, http.StatusForbidden, 0)

		return
	}

	// The route is the path of the pattern that a ServeMux matched, such as
	// /items/{id} for "GET example.com/items/{id}". A request that matched
	// no pattern has no route, and neither has a request whose context a
	// middleware replaced.
	defer func() {
		e, ok := r.Context().Value(exchangeKey{}).(*exchange)
		if i := strings.IndexByte(r.Pattern, '/'); ok && i >= 0 {
			e.route = r.Pattern[i:]
		}
	}()

	a.handler.ServeHTTP(w, r)
}
