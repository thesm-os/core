// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package httpclient

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/net/internal/semconv"
	"go.thesmos.sh/core/resilience"
	"go.thesmos.sh/core/telemetry"
)

// The names of the client's telemetry.
const (
	// scope is the instrumentation scope of the client's tracer.
	scope telemetry.InstrumentName = "go.thesmos.sh/core/net/httpclient"

	// durationDescription is the description of the duration histogram,
	// as the semantic conventions word it.
	durationDescription = "Duration of HTTP client requests."
)

// The message and the keys of the log record of a call that failed.
const (
	msgFailed     = "call failed"
	keyDependency = "dependency"
	keyError      = "error"
)

// The schemes that a client sends, and the ports of server.port when a URL
// has none.
const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
	portHTTP    = 80
	portHTTPS   = 443
)

// The headers that mark a request as safe to send again, as net/http's own
// replay of a request reads them.
const (
	headerIdempotencyKey  = "Idempotency-Key"
	headerXIdempotencyKey = "X-Idempotency-Key"
)

// The bounds of a call.
const (
	// maxRedirects is the number of redirects that a client follows.
	maxRedirects = 5

	// statusBodyBytes is the number of bytes of the body of a refused
	// response that Fetch and AppendFetch keep in its StatusError.
	statusBodyBytes = 1024

	// readBytes is the room that the read of a body in steps makes in its
	// buffer before each step. It is the size of the first buffer of
	// io.ReadAll.
	readBytes = 512

	// drainBytes is the number of bytes of an unread body that the client
	// reads before it closes the body, so the transport can reuse the
	// connection. The transport closes a connection whose body is longer.
	drainBytes = 65536

	// unbounded is the response limit that a Client stores for a negative
	// WithMaxResponseBytes, which turns the limit off. New refuses a zero
	// limit, so zero is free to mean no limit.
	unbounded = 0

	// noTimeout is the dial timeout that a Client applies for a negative
	// WithDialTimeout, which turns the timeout off. New refuses a zero
	// timeout, so zero is free to mean no timeout.
	noTimeout = 0
)

// clientSpan is the options of every span of the client.
var clientSpan = []telemetry.SpanOption{telemetry.WithSpanKind(telemetry.SpanKindClient)}

// series identifies an attribute set of http.client.request.duration.
type series struct {
	method    string
	host      string
	errorType string
	port      int
	status    int
}

// attrs returns the attributes of s, without the status of an attempt
// that received no response, and without the type of the error of one that
// succeeded. It allocates the slice once, with room for every attribute.
func (s series) attrs() []telemetry.Attr {
	attrs := make([]telemetry.Attr, 0, 5)
	attrs = append(attrs,
		telemetry.AttrString(semconv.RequestMethod, s.method),
		telemetry.AttrString(semconv.ServerAddress, s.host),
		telemetry.AttrInt(semconv.ServerPort, int64(s.port)),
	)

	if s.status != 0 {
		attrs = append(attrs, telemetry.AttrInt(semconv.ResponseStatusCode, int64(s.status)))
	}

	if s.errorType != "" {
		attrs = append(attrs, telemetry.AttrString(semconv.ErrorType, s.errorType))
	}

	return attrs
}

// Client calls one HTTP dependency, such as a time-stamp authority or a
// registry, on net/http. It sends each request through a transport of its
// own, never net/http's DefaultTransport, with the limits, the address
// check and the guards that the package documentation describes.
//
// # Concurrency
//
// Safe for concurrent use. A process shares one Client per dependency.
//
// # Allocation contract
//
// New allocates the client, its transport and its instruments. A call
// allocates as the package documentation states.
type Client struct {
	http       *http.Client
	logger     *slog.Logger
	tracer     telemetry.Tracer
	propagator telemetry.Propagator
	clock      clock.Clock
	breaker    *resilience.Breaker
	retrier    *resilience.Retrier
	prepare    func(*http.Request) error
	classify   func(*http.Response) error
	durations  *semconv.Durations[series]
	name       string
	hosts      hosts

	// maxResponse is the response limit, or unbounded.
	maxResponse int64
}

// New returns a Client of the dependency name with opts and a transport of
// its own. opts include [WithClock], [WithLogger], [WithReporter],
// [WithPropagator] and [WithHosts], which New requires. name labels the
// dependency in errors, metrics and logs.
//
// The transport negotiates HTTP/2 over TLS and HTTP/1.1 otherwise. Its
// HTTP/2 connections send a ping after 30 s without a frame, and close
// after 15 s without its response. It waits 1 s for the 100 Continue of a
// request that expects one, as net/http's DefaultTransport does.
//
// Error modes: an error that wraps [ErrConfig], classified Invalid, for an
// empty name and for a missing or refused option value. The error names
// the problem.
func New(name string, opts ...Option) (*Client, error) {
	if name == "" {
		return nil, fmt.Errorf("%w: the name is empty", ErrConfig)
	}

	s := settings{
		timeout:             10 * time.Second,
		dialTimeout:         5 * time.Second,
		tlsHandshakeTimeout: 5 * time.Second,
		idleConnTimeout:     90 * time.Second,
		maxIdleConnsPerHost: 32,
		maxResponseBytes:    8 << 20,
	}
	s.apply(opts)

	if problem := s.problem(); problem != "" {
		return nil, fmt.Errorf("%w: %s", ErrConfig, problem)
	}

	var admitted hosts
	for _, h := range s.hosts {
		if h = strings.ToLower(h); strings.HasPrefix(h, ".") {
			admitted.suffixes = append(admitted.suffixes, h)
		} else {
			admitted.exact = append(admitted.exact, h)
		}
	}

	// net/http fails a dial or a handshake at once for a negative timeout,
	// and waits without a bound for a zero one.
	dialTimeout := max(s.dialTimeout, noTimeout)
	dialer := &net.Dialer{Timeout: dialTimeout, Resolver: s.resolver}
	if s.reach == ReachPublic && s.proxy == nil {
		dialer.ControlContext = checkAddress
	}

	dial := dialer.DialContext
	if caller := s.dial; caller != nil {
		// net/http dials under a context that the cancellation of a request
		// does not end, so the client ends the context of a dial of the
		// caller at the dial timeout, and when the dial returns.
		dial = func(ctx context.Context, network, address string) (net.Conn, error) {
			var cancel context.CancelFunc
			if dialTimeout == noTimeout {
				ctx, cancel = context.WithCancel(ctx)
			} else {
				ctx, cancel = context.WithTimeout(ctx, dialTimeout)
			}
			defer cancel()

			return caller(ctx, network, address)
		}
	}

	var protocols http.Protocols
	protocols.SetHTTP1(true)
	protocols.SetHTTP2(true)

	transport := &http.Transport{
		DialContext:           dial,
		TLSClientConfig:       s.tls,
		TLSHandshakeTimeout:   max(s.tlsHandshakeTimeout, 0),
		IdleConnTimeout:       max(s.idleConnTimeout, 0),
		MaxIdleConnsPerHost:   s.maxIdleConnsPerHost,
		ExpectContinueTimeout: time.Second,
		Protocols:             &protocols,
		HTTP2:                 &http.HTTP2Config{SendPingTimeout: 30 * time.Second, PingTimeout: 15 * time.Second},
	}
	if s.proxy != nil {
		transport.Proxy = http.ProxyURL(s.proxy)
	}

	durations := semconv.NewDurations(s.reporter, semconv.ClientDuration, durationDescription, s.clock, series.attrs)
	c := &Client{
		logger:      s.logger,
		tracer:      s.reporter.Tracer(scope),
		propagator:  s.propagator,
		clock:       s.clock,
		breaker:     s.breaker,
		retrier:     s.retrier,
		prepare:     s.prepare,
		classify:    s.classify,
		durations:   durations,
		name:        name,
		hosts:       admitted,
		maxResponse: max(s.maxResponseBytes, unbounded),
	}

	if c.classify == nil {
		c.classify = c.statusError
	}

	c.http = &http.Client{Transport: transport, Timeout: max(s.timeout, 0), CheckRedirect: c.redirect}

	return c, nil
}

// Do sends req as http.Client.Do does, under the guards of the client, and
// returns the response of the last attempt, whatever its status. The
// caller reads and closes its body. Closing the body ends the timeout of
// that attempt.
//
// Do runs these steps:
//
//  1. The scheme of req is http or https, and [WithHosts] admits its host.
//     Otherwise Do returns [ErrBlocked], and the breaker records no outcome.
//  2. With [WithBreaker], each attempt runs through resilience.Call with the
//     host as its target, which returns resilience.ErrOpen when the circuit
//     refuses it.
//  3. Each attempt starts a client span, injects its trace into a copy of
//     the headers of req, calls the function of [WithPrepare], and sends
//     the request under the timeout of [WithTimeout].
//  4. An attempt fails for an error of the transport, classified Transient
//     unless its cause has a class, and for a response that the
//     classification refuses. resilience.Call records a failure for both
//     when the breaker's TripOn lists their class, and no outcome when the
//     context of req ended.
//  5. With [WithRetrier], resilience.Do retries an attempt that failed with
//     a Transient error, and waits at least the delay of its Retry-After.
//     It retries only a request that is safe to send again: its method is
//     idempotent under RFC 9110, or it has an Idempotency-Key header, and
//     its body is empty or has a GetBody that produces it again. Do closes
//     the response of each attempt before the next one.
//
// The client follows at most 5 redirects, and returns the sixth redirect
// response as it is. Each redirect goes to a host that WithHosts admits, and
// none goes from https to http.
//
// Do logs a call that fails at slog.LevelWarn, unless the context of req
// ended.
//
// Error modes: [ErrBlocked], classified Denied; resilience.ErrOpen and
// resilience.ErrBudget, classified Transient; an error of a certificate that
// fails verification, classified Integrity; another error of the transport,
// classified Transient unless its cause has a class; the error of the
// function of WithPrepare; and the context's error when the context of req
// ends. A response that the classification refuses is no error of Do.
//
// # Allocation contract
//
// Each attempt allocates 2 objects of the client: the copies of req and of
// its headers, which leave the caller's request unchanged. An attempt whose
// span has a trace identity also allocates the attributes of the span. The
// state of the call and the guards allocate nothing. On a connection that
// the transport reuses, a call allocates 54 objects with Go 1.27.1, 52 of
// them in net/http's Client and Transport, and a traced call allocates 60.
// The tracer, the propagator, and the functions of WithPrepare and
// WithClassify allocate on their own.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	k := &call{c: c, req: req}
	resp, err := attempts(k, func(resp *http.Response, err error) (*http.Response, error) {
		k.prev = resp

		return resp, err
	})

	// When the breaker refuses the last attempt, the call returns no
	// response, and the response of the attempt before it is still open.
	if resp == nil && k.prev != nil {
		discard(k.prev)
	}

	if resp == nil {
		return nil, err
	}

	if err != nil && req.Context().Err() != nil {
		discard(resp)

		return nil, err
	}

	return resp, nil
}

// Fetch sends req as Do does, and returns the body of a response that
// succeeded. It calls [Client.AppendFetch] with a nil dst, so it reads at
// most the bytes of [WithMaxResponseBytes] within the attempt, and the
// timeout, the breaker and the retries cover the read. A response to HEAD
// returns nil.
//
// Error modes: the errors of Do; the error of the classification of a
// refused response, by default a *[StatusError] with the first 1 KiB of the
// body; [ErrTooLarge], classified Invalid, for a body beyond the limit; and
// an error of the read of the body, classified Transient unless the context
// of req ended.
//
// # Allocation contract
//
// Fetch allocates as Do does, and the body: 55 objects for a call on a
// connection that the transport reuses. Under a limit, it allocates the
// body once when the response declares its length. A refused response
// allocates its StatusError and the 1 KiB buffer of its body as well.
func (c *Client) Fetch(req *http.Request) ([]byte, error) {
	return c.AppendFetch(nil, req)
}

// AppendFetch sends req as Do does, and appends to dst the body of a
// response that succeeded, at most the bytes of [WithMaxResponseBytes]. It
// reads the body within the attempt, so the timeout, the breaker and the
// retries cover the read. Each attempt appends to dst at the length that
// dst had at the call, so the bytes of an attempt that failed are not in
// the result. A response to HEAD appends nothing.
//
// Under a limit, AppendFetch grows dst at most once for a body whose length
// the response declares, and refuses a declared length beyond the limit
// without a read. It reads a body of unknown length, and any body without a
// limit, in steps into the room of dst, and grows dst before a step when
// less than 512 bytes of room are left.
//
// Error modes: those of [Client.Fetch]. With an error, AppendFetch returns
// dst unchanged. An attempt that failed can have written into the capacity
// of dst beyond its length.
//
// # Allocation contract
//
// AppendFetch allocates as Do does when dst has room for the body: 54
// objects for a call on a connection that the transport reuses, with Go
// 1.27.1. For a body of unknown length, room means 512 bytes beyond the
// body, because each step makes room for 512 bytes before its read. A
// chunked response costs 57 objects, because net/http allocates the key and
// the value of its Transfer-Encoding header and the TransferEncoding of the
// response. A body without room in dst grows dst. A refused response
// allocates its StatusError and the 1 KiB buffer of its body as well.
func (c *Client) AppendFetch(dst []byte, req *http.Request) ([]byte, error) {
	b, err := attempts(&call{c: c, req: req}, func(resp *http.Response, err error) ([]byte, error) {
		if resp == nil {
			return nil, err
		}

		if err != nil {
			if se, ok := errors.AsType[*StatusError](err); ok {
				// The body is detail of the error. A read that fails keeps
				// the bytes that it read.
				body := make([]byte, statusBodyBytes)
				n, _ := io.ReadFull(resp.Body, body)
				se.Body = body[:n]
			}

			discard(resp)

			return nil, err
		}

		// appendBody consumes the body to its end or fails, so the body
		// closes without a drain. The transport reuses the connection of a
		// body that it read to its end, and closes any other.
		b, err := c.appendBody(req.Context(), dst, resp)
		_ = resp.Body.Close()

		return b, err
	})
	if err != nil {
		return dst, err
	}

	return b, nil
}

// call is one call of [Client.Do], [Client.Fetch] or [Client.AppendFetch]:
// its request, the host and the port of its URL, and the response of the
// attempt before the current one, which the next attempt discards.
type call struct {
	c    *Client
	req  *http.Request
	prev *http.Response
	host string
	port int

	// sent reports that an attempt sent the request, so the next attempt
	// sends the body that GetBody produces again.
	sent bool
}

// attempts sends the request of k under the guards of its client, as
// [Client.Do] describes, and returns what read makes of the response and
// the error of the last attempt. It logs a call that fails, unless the
// context of the request ended.
func attempts[T any](k *call, read func(*http.Response, error) (T, error)) (T, error) {
	c, req := k.c, k.req
	ctx := req.Context()

	var zero T

	if err := c.admit(req.URL); err != nil {
		c.logger.LogAttrs(ctx, slog.LevelWarn, msgFailed,
			slog.String(keyDependency, c.name), slog.String(semconv.RequestMethod, req.Method), slog.Any(keyError, err))

		return zero, err
	}

	k.host = strings.ToLower(req.URL.Hostname())
	k.port = portHTTP
	if req.URL.Scheme == schemeHTTPS {
		k.port = portHTTPS
	}

	if p := req.URL.Port(); p != "" {
		k.port, _ = strconv.Atoi(p)
	}

	// Every attempt ends in read, which records the response of the attempt
	// in k.prev for Do, so the next attempt discards the response before it.
	attempt := func(ctx context.Context) (T, error) {
		if k.prev != nil {
			discard(k.prev)
		}

		body := req.Body
		if k.sent && req.GetBody != nil {
			replayed, err := req.GetBody()
			if err != nil {
				return read(nil, fmt.Errorf("httpclient: %s: replay the body: %w", c.name, err))
			}

			body = replayed
		}

		k.sent = true

		// read takes over the body: AppendFetch's read closes it, and Do
		// returns it to its caller or discards it at the next attempt.
		return read(c.send(ctx, k, body)) //nolint:bodyclose // see above
	}

	guarded := attempt
	if c.breaker != nil {
		guarded = func(ctx context.Context) (T, error) { return resilience.Call(ctx, c.breaker, k.host, attempt) }
	}

	// A request is safe to send again when its method is idempotent under
	// RFC 9110, or when it has an idempotency key, and when its body can
	// be produced again.
	_, keyed := req.Header[headerIdempotencyKey]
	_, xKeyed := req.Header[headerXIdempotencyKey]

	idempotent := keyed || xKeyed
	switch req.Method {
	case "", http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace, http.MethodPut, http.MethodDelete:
		idempotent = true
	}

	var (
		v   T
		err error
	)

	if c.retrier != nil && idempotent && (req.Body == nil || req.Body == http.NoBody || req.GetBody != nil) {
		v, err = resilience.Do(ctx, c.retrier, guarded)
	} else {
		v, err = guarded(ctx)
	}

	if err != nil && ctx.Err() == nil {
		c.logger.LogAttrs(ctx, slog.LevelWarn, msgFailed,
			slog.String(keyDependency, c.name),
			slog.String(semconv.RequestMethod, req.Method),
			slog.String(semconv.ServerAddress, k.host),
			slog.Any(keyError, err))
	}

	return v, err
}

// send sends one attempt of k with body. It starts a client span, injects
// its trace into a copy of the headers of the request, calls the function
// of WithPrepare, sends the copy, and classifies the response. It records
// the duration of the attempt and ends the span with the error of the
// attempt.
//
// http.Client.Do closes the body of a response that it returns with an
// error, so send returns no response with an error of the transport.
func (c *Client) send(ctx context.Context, k *call, body io.ReadCloser) (*http.Response, error) {
	ctx, span := c.tracer.Start(ctx, telemetry.SpanName(semconv.Method(k.req.Method)), clientSpan...)

	r := k.req.WithContext(ctx)
	r.Body = body
	r.Header = k.req.Header.Clone()
	if r.Header == nil {
		r.Header = http.Header{}
	}

	c.propagator.Inject(ctx, span.SpanContext(), telemetry.HeaderCarrier(r.Header))

	if c.prepare != nil {
		if err := c.prepare(r); err != nil {
			err = fmt.Errorf("httpclient: %s: prepare the request: %w", c.name, err)
			span.End(err)

			return nil, err
		}
	}

	start := c.clock.Time()
	resp, err := c.http.Do(r)
	elapsed := c.clock.Time().Sub(start)

	if err != nil {
		resp = nil
		err = fmt.Errorf("httpclient: %s: %w", c.name, err)

		// A certificate that fails verification fails the same way on every
		// attempt. The caller's context ending is no failure of the
		// dependency, and a cause with a class, such as ErrBlocked, keeps
		// it.
		if _, ok := errors.AsType[*tls.CertificateVerificationError](err); ok {
			err = errs.WithClass(err, errs.Integrity)
		} else if ctx.Err() == nil && errs.Classify(err) == errs.Unspecified {
			err = errs.WithClass(err, errs.Transient)
		}
	} else {
		err = c.classify(resp)
	}

	s := series{method: semconv.Method(k.req.Method), host: k.host, port: k.port}
	if resp != nil {
		s.status = resp.StatusCode
	}

	if err != nil {
		s.errorType = errs.Classify(err).String()
	}

	c.durations.Record(ctx, s, elapsed)

	// A span without a trace identity records nothing, so the client
	// builds no attributes for it.
	if span.SpanContext().TraceID != "" {
		span.SetAttributes(s.attrs())
	}

	span.End(err)

	return resp, err
}

// appendBody appends the body of resp, a response that succeeded, to dst
// under the limit of the client, as [Client.AppendFetch] describes, and
// returns dst unchanged with an error. Without a limit, it reads every body
// in steps, so a declared length that the server does not send allocates
// nothing in advance.
//
// It classifies an error of the read as Transient while ctx, the context of
// the caller's request, is live. The context of resp.Request is not the
// caller's: http.Client ends it at the timeout of the client as well, which
// is a failure of the dependency.
//
// A response to HEAD declares the length of the body that a GET would
// return, and has no body, so appendBody returns dst for it.
func (c *Client) appendBody(ctx context.Context, dst []byte, resp *http.Response) ([]byte, error) {
	// unknownLength is the ContentLength that net/http gives a response
	// without a declared length.
	const unknownLength = -1

	if resp.Request.Method == http.MethodHead {
		return dst, nil
	}

	limited := c.maxResponse != unbounded
	if limited && resp.ContentLength > c.maxResponse {
		return dst, fmt.Errorf("%w: %s declared %d bytes", ErrTooLarge, c.name, resp.ContentLength)
	}

	var (
		b   []byte
		err error
	)

	if limited && resp.ContentLength != unknownLength {
		// The limit bounds the declared length, so it fits an int on a
		// 64-bit platform.
		n := int(resp.ContentLength)
		b = slices.Grow(dst, n)[:len(dst)+n]
		_, err = io.ReadFull(resp.Body, b[len(dst):])
	} else {
		// The reader stops one byte beyond the limit, so a longer body fails
		// the check after the loop. The reader is a local value whose Read
		// the loop calls directly, so it does not escape to the heap.
		lr := io.LimitedReader{R: resp.Body, N: math.MaxInt64}
		if limited {
			lr.N = c.maxResponse + 1
		}

		b = dst
		for err == nil {
			b = slices.Grow(b, readBytes)

			var n int
			n, err = lr.Read(b[len(b):cap(b)])
			b = b[:len(b)+n]
		}

		if limited && int64(len(b)-len(dst)) > c.maxResponse {
			return dst, fmt.Errorf("%w: %s sent more than %d bytes", ErrTooLarge, c.name, c.maxResponse)
		}

		if errors.Is(err, io.EOF) {
			err = nil
		}
	}

	if err != nil {
		err = fmt.Errorf("httpclient: %s: read the body: %w", c.name, err)
		if ctx.Err() == nil {
			err = errs.WithClass(err, errs.Transient)
		}

		return dst, err
	}

	return b, nil
}

// admit returns nil for a URL that the client may call: a scheme of http
// or https, and a host that WithHosts admits. It returns an error that
// wraps [ErrBlocked] otherwise, and for a request without a URL.
func (c *Client) admit(u *url.URL) error {
	if u == nil {
		return fmt.Errorf("%w: %s: the request has no URL", ErrBlocked, c.name)
	}

	if u.Scheme != schemeHTTP && u.Scheme != schemeHTTPS {
		return fmt.Errorf("%w: %s: the scheme %q is not http or https", ErrBlocked, c.name, u.Scheme)
	}

	if !c.hosts.admits(u.Hostname()) {
		return fmt.Errorf("%w: %s: the host %q is not admitted", ErrBlocked, c.name, u.Hostname())
	}

	return nil
}

// redirect is the CheckRedirect of the client. It follows a redirect to a
// URL that the client admits, and returns http.ErrUseLastResponse after
// maxRedirects, so the call returns the redirect response as it is. It
// refuses a redirect from https to http with [ErrBlocked].
func (c *Client) redirect(req *http.Request, via []*http.Request) error {
	if len(via) > maxRedirects {
		return http.ErrUseLastResponse
	}

	if via[len(via)-1].URL.Scheme == schemeHTTPS && req.URL.Scheme != schemeHTTPS {
		return fmt.Errorf("%w: %s: a redirect from https to %s", ErrBlocked, c.name, req.URL.Scheme)
	}

	return c.admit(req.URL)
}

// statusError is the default classification of a response: nil for a status
// of 2xx, and a *[StatusError] otherwise, with the delay of its Retry-After
// header.
func (c *Client) statusError(resp *http.Response) error {
	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		return nil
	}

	return &StatusError{
		Dependency: c.name,
		Status:     resp.StatusCode,
		retryAfter: retryAfter(resp.Header, c.clock.Time()),
	}
}

// discard reads at most drainBytes of the body of resp, so the transport
// can reuse the connection, and closes the body. The transport closes the
// connection of a body that fails to drain or to close, so discard ignores
// both errors.
func discard(resp *http.Response) {
	_, _ = io.CopyN(io.Discard, resp.Body, drainBytes)
	_ = resp.Body.Close()
}
