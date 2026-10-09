// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package httpserver_test

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/coretest/telemetrytest"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/net/httpserver"
	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/telemetry/noop"
	"go.thesmos.sh/core/telemetry/w3c"
)

// The identity of every span that the reporter of a fixture starts.
const (
	traceID telemetry.TraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	spanID  telemetry.SpanID  = "00f067aa0ba902b7"
)

// The fixture values of the cases.
const (
	// loopback is the address of a listener on a port that the system
	// chooses.
	loopback = "127.0.0.1:0"

	// tcp is the network of the listeners of the cases.
	tcp = "tcp"

	// unbound is an address of TEST-NET-1 (RFC 5737), which no interface of
	// the host has. A case that serves on a listener of its own sets it as
	// the address too, so a Server that listens on its address instead of
	// the listener fails to listen at once.
	unbound = "192.0.2.1:0"

	// body is the body that the handlers of the cases write.
	body = "ok"
)

// The messages and the keys of the log records of the package, which the
// cases pin.
const (
	messageRequest  = "request"
	messageNotReady = "ready check failed"

	keyError    = "error"
	keyDuration = "duration"
	keyBodySize = "http.response.body.size"
	keyTraceID  = "trace_id"
	keyPanic    = "panic"
	keyStack    = "stack"
)

// origin is the time of the fake clocks of the cases.
var origin = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

// The errors of the test doubles.
var (
	errCheck  = errors.New("dependency unavailable")
	errAccept = errors.New("accept failed")
	errClose  = errors.New("close failed")
	errBoom   = errors.New("boom")

	// errHung is the error of Run that a fixture records when Run does not
	// return within patience of the end of its context.
	errHung = errors.New("run did not return within the patience of the case")
)

// required bundles the four options that New requires: a fake clock that
// the cases do not advance, a logger that discards its records, the noop
// reporter and the W3C propagator. A case that advances its clock or reads
// its records passes its own clock or logger after required.
var required = httpserver.Options(
	httpserver.WithClock(fake.New(origin)),
	httpserver.WithLogger(slog.New(slog.DiscardHandler)),
	httpserver.WithReporter(noop.Reporter{}),
	httpserver.WithPropagator(w3c.Propagator{}),
)

// logs is a slog.Handler that keeps a clone of every record at or above
// its level, and sends a clone of each record of a request to requests
// when requests is not nil. Its methods are safe for concurrent use.
type logs struct {
	records  []slog.Record
	requests chan slog.Record
	mu       sync.Mutex
	level    slog.Level
}

var _ slog.Handler = (*logs)(nil)

// Enabled reports whether level is at or above the level of l.
func (l *logs) Enabled(_ context.Context, level slog.Level) bool { return level >= l.level }

// Handle keeps a clone of r, and sends one to requests for a record of a
// request. A case that sets requests gives it room for every record.
func (l *logs) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.records = append(l.records, r.Clone())

	if l.requests != nil && r.Message == messageRequest {
		l.requests <- r.Clone()
	}

	return nil
}

// WithAttrs returns l, which keeps no attributes of its own.
func (l *logs) WithAttrs([]slog.Attr) slog.Handler { return l }

// WithGroup returns l, which keeps no groups of its own.
func (l *logs) WithGroup(string) slog.Handler { return l }

// find returns the records whose message is msg, in order.
func (l *logs) find(msg string) []slog.Record {
	l.mu.Lock()
	defer l.mu.Unlock()

	var found []slog.Record
	for i := range l.records {
		if l.records[i].Message == msg {
			found = append(found, l.records[i])
		}
	}

	return found
}

// span is a span stub that the tracer of a reporter started, with the name
// and the options of its start.
type span struct {
	*telemetrytest.SpanStub

	name telemetry.SpanName
	opts []telemetry.SpanOption
}

// set is a histogram stub of one attribute set, with the attributes that
// bound it.
type set struct {
	*telemetrytest.HistogramStub

	attrs []telemetry.Attr
}

// reporter is a telemetry.Reporter whose tracer starts a span stub, with
// the identity of traceID and spanID unless anonymous, and whose histogram
// binds a histogram stub for each attribute set. It keeps the spans and the
// sets in order, and its methods are safe for concurrent use.
type reporter struct {
	*telemetrytest.ReporterStub

	spans     []*span
	sets      []*set
	mu        sync.Mutex
	anonymous bool
}

// newReporter returns a reporter whose spans have a trace identity.
func newReporter(tb testing.TB) *reporter {
	tb.Helper()

	r := &reporter{ReporterStub: telemetrytest.NewReporterStub(tb)}

	tracer := telemetrytest.NewTracerStub(tb)
	tracer.OnStart.Func(r.start)
	r.OnTracer.Returns(tracer)

	histogram := telemetrytest.NewHistogramStub(tb)
	histogram.OnWith.Func(r.bind)
	r.OnHistogram.Returns(histogram)

	return r
}

// start starts a span stub of name, and keeps it.
func (r *reporter) start(
	ctx context.Context, name telemetry.SpanName, opts ...telemetry.SpanOption,
) (context.Context, telemetry.Span) {
	s := &span{SpanStub: telemetrytest.NewSpanStub(nil), name: name, opts: slices.Clone(opts)}

	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.anonymous {
		s.OnSpanContext.Returns(telemetry.SpanContext{TraceID: traceID, SpanID: spanID})
	}

	r.spans = append(r.spans, s)

	return ctx, s
}

// bind binds a histogram stub of attrs, and keeps it.
func (r *reporter) bind(attrs []telemetry.Attr) telemetry.Histogram {
	s := &set{HistogramStub: telemetrytest.NewHistogramStub(nil), attrs: attrs}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.sets = append(r.sets, s)

	return s
}

// started returns the spans that the tracer started, in order.
func (r *reporter) started() []*span {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.spans)
}

// bound returns the attribute sets that the histogram bound, in order.
func (r *reporter) bound() []*set {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.sets)
}

// served is the listener of a fixture. It closes accepting at its first
// Accept, which tells the fixture that Run serves, and closed when it is
// closed. Its Close returns closeErr after it closes the listener under it,
// when a case set closeErr before it stopped the fixture.
type served struct {
	net.Listener

	closeErr              error
	accepting, closed     chan struct{}
	acceptOnce, closeOnce sync.Once
}

// Accept closes accepting once, and returns the next connection of the
// listener under it.
func (l *served) Accept() (net.Conn, error) {
	l.acceptOnce.Do(func() { close(l.accepting) })

	return l.Listener.Accept() //nolint:wrapcheck // a test listener returns the error of the listener under it
}

// Close closes closed once and the listener under it, and returns closeErr
// when a case set it.
func (l *served) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })

	err := l.Listener.Close()
	if l.closeErr != nil {
		return l.closeErr
	}

	return err //nolint:wrapcheck // as in Accept
}

// fixture is a Server that a case runs on a loopback listener. It reads a
// fake clock, writes its log records to logs, and reports to a reporter.
type fixture struct {
	server   *httpserver.Server
	listener *served
	clock    *fake.Clock
	logs     *logs
	reporter *reporter
	client   *http.Client
	cancel   context.CancelFunc
	done     chan error
	url      string
	err      error
	once     sync.Once

	// checked reports that the case received the error of Run from stop.
	checked bool
}

// newFixture runs a Server of h with the options of a fixture, a drain
// without a delay, and then opts, which override them. It returns once Run
// accepts on the listener of the fixture, and fails t at once when Run
// returns first. The cleanup of t stops the Server, and fails t when Run
// returned an error that the case did not receive from stop.
func newFixture(t *testing.T, h http.Handler, opts ...httpserver.Option) *fixture {
	t.Helper()

	ln := &served{Listener: listen(t), accepting: make(chan struct{}), closed: make(chan struct{})}
	f := &fixture{
		listener: ln,
		clock:    fake.New(origin),
		logs:     &logs{level: slog.LevelDebug},
		reporter: newReporter(t),
		client:   &http.Client{Transport: &http.Transport{}, Timeout: patience},
		done:     make(chan error, 1),
		url:      "http://" + ln.Addr().String(),
	}

	all := append([]httpserver.Option{
		httpserver.WithClock(f.clock),
		httpserver.WithLogger(slog.New(f.logs)),
		httpserver.WithReporter(f.reporter),
		httpserver.WithPropagator(w3c.Propagator{}),
		httpserver.WithListener(ln),
		httpserver.WithAddr(unbound),
		httpserver.WithDrainDelay(-1),
	}, opts...)

	s, err := httpserver.New(h, all...)
	assert.NoError(t, err, "New must accept the options of the fixture")
	f.server = s

	ctx, cancel := context.WithCancel(t.Context())
	f.cancel = cancel

	go func() { f.done <- s.Run(ctx) }()

	t.Cleanup(func() {
		f.wait()

		if !f.checked {
			assert.NoError(t, f.err, "Run must drain")
		}
	})

	// A case of a Server that does not serve fails here at once, and does
	// not wait for a response that no server sends.
	timer := time.NewTimer(patience)
	defer timer.Stop()

	select {
	case <-ln.accepting:
	case err := <-f.done:
		// wait reads the error of Run again.
		f.done <- err
		t.Fatalf("Run returned before it served: %v", err)
	case <-timer.C:
		t.Fatalf("Run did not serve within %s", patience)
	}

	return f
}

// wait ends the context of Run once, and waits for Run to return, at most
// for patience, after which it records errHung. After wait, the log record
// of every request that the fixture served is in its logs.
func (f *fixture) wait() {
	f.once.Do(func() {
		f.cancel()

		timer := time.NewTimer(patience)
		defer timer.Stop()

		select {
		case f.err = <-f.done:
		case <-timer.C:
			f.err = errHung
		}

		f.client.CloseIdleConnections()
	})
}

// stop waits for Run as wait does, and returns the error of Run to the
// case, which asserts it.
func (f *fixture) stop() error {
	f.wait()
	f.checked = true

	return f.err
}

// response is a response that a case received, with its body read.
type response struct {
	header http.Header
	body   string
	status int
}

// send sends a request of method to path of f with body and header, and
// returns its response.
func (f *fixture) send(t *testing.T, method, path string, body io.Reader, header http.Header) response {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, f.url+path, body)
	assert.NoError(t, err, "the request of the case must build")
	maps.Copy(req.Header, header)

	return do(t, f.client, req)
}

// failingListener is a listener whose Accept fails with err once fail is
// closed, and which closes closed when it is closed.
type failingListener struct {
	net.Listener

	fail   chan struct{}
	closed chan struct{}
	err    error
	once   sync.Once
}

// Accept waits for fail, and returns err.
func (l *failingListener) Accept() (net.Conn, error) {
	<-l.fail

	return nil, l.err
}

// Close closes the listener under it and closed.
func (l *failingListener) Close() error {
	l.once.Do(func() { close(l.closed) })

	return l.Listener.Close() //nolint:wrapcheck // a test listener returns the error of the listener under it
}

// discardWriter is a ResponseWriter that keeps the status of its last
// response and discards every body. It records a status only when a
// handler writes one, so a handler that writes none leaves 0.
type discardWriter struct {
	header http.Header
	status int
}

// Header returns the header map of the writer.
func (w *discardWriter) Header() http.Header { return w.header }

// WriteHeader keeps code.
func (w *discardWriter) WriteHeader(code int) { w.status = code }

// Write discards b.
func (*discardWriter) Write(b []byte) (int, error) { return len(b), nil }

func TestServer(t *testing.T) {
	t.Parallel()

	t.Run("New", func(t *testing.T) {
		t.Parallel()

		t.Run("returns ErrConfig for a nil handler", func(t *testing.T) {
			t.Parallel()
			_, err := httpserver.New(nil, required)
			expect.ErrorIs(t, err, httpserver.ErrConfig, "New must refuse a nil handler")
			expect.Equal(t, errs.Classify(err), errs.Invalid, "ErrConfig must classify as Invalid")
		})

		t.Run("returns ErrConfig for a trusted origin that is not an origin", func(t *testing.T) {
			t.Parallel()
			_, err := httpserver.New(http.NotFoundHandler(), required, httpserver.WithTrustedOrigins("app.example.com"))
			expect.ErrorIs(t, err, httpserver.ErrConfig, "New must refuse an origin without a scheme")
			expect.Equal(t, errs.Classify(err), errs.Invalid, "ErrConfig must classify as Invalid")
		})

		t.Run("returns ErrConfig for a middleware that returns a nil handler", func(t *testing.T) {
			t.Parallel()
			_, err := httpserver.New(http.NotFoundHandler(), required, httpserver.WithMiddleware(
				func(h http.Handler) http.Handler { return h },
				func(http.Handler) http.Handler { return nil },
			))
			assert.ErrorIs(t, err, httpserver.ErrConfig, "New must refuse a middleware that returns nil")
			assert.Contains(t, err.Error(), "index 1", "the error must name the middleware")
		})

		t.Run("logs the errors of net/http through the logger at LevelWarn", func(t *testing.T) {
			t.Parallel()
			cfg, _ := tlsPair(t)
			f := newFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), httpserver.WithTLS(cfg))
			conn := dial(t, f)

			_, err := io.WriteString(conn, "garbage that is not a TLS record\r\n")
			assert.NoError(t, err, "the bytes must write")
			_, _ = conn.Read(make([]byte, 1))
			assert.NoError(t, f.stop(), "Run must drain")

			f.logs.mu.Lock()
			defer f.logs.mu.Unlock()

			assert.True(t, slices.ContainsFunc(f.logs.records, func(r slog.Record) bool {
				return r.Level == slog.LevelWarn && strings.Contains(r.Message, "TLS handshake error")
			}), "the logger must receive the TLS handshake error of net/http")
		})

		t.Run("returns ErrConfig without the options that it requires", func(t *testing.T) {
			t.Parallel()
			_, err := httpserver.New(http.NotFoundHandler())
			assert.ErrorIs(t, err, httpserver.ErrConfig, "New must refuse a Server without a clock")
			assert.Contains(t, err.Error(), "WithClock", "the error must name the missing option")
		})

		t.Run("ignores the address beside a listener", func(t *testing.T) {
			t.Parallel()
			_, err := httpserver.New(http.NotFoundHandler(), required,
				httpserver.WithListener(listen(t)), httpserver.WithAddr("localhost"))
			assert.NoError(t, err, "a listener must take precedence over an address without a port")
		})

		tests := []struct {
			give httpserver.Option
			name string
		}{
			{name: "returns ErrConfig for a nil option"},
			{name: "returns ErrConfig for a nil clock", give: httpserver.WithClock(nil)},
			{name: "returns ErrConfig for a nil logger", give: httpserver.WithLogger(nil)},
			{name: "returns ErrConfig for a nil reporter", give: httpserver.WithReporter(nil)},
			{name: "returns ErrConfig for a nil propagator", give: httpserver.WithPropagator(nil)},
			{name: "returns ErrConfig for a nil listener", give: httpserver.WithListener(nil)},
			{name: "returns ErrConfig for an address without a port", give: httpserver.WithAddr("localhost")},
			{name: "returns ErrConfig for a nil TLS configuration", give: httpserver.WithTLS(nil)},
			{name: "returns ErrConfig for a nil middleware", give: httpserver.WithMiddleware(nil)},
			{name: "returns ErrConfig for a nil ready check", give: httpserver.WithReadyCheck(nil)},
			{name: "returns ErrConfig for a zero read header timeout", give: httpserver.WithReadHeaderTimeout(0)},
			{name: "returns ErrConfig for a zero read timeout", give: httpserver.WithReadTimeout(0)},
			{name: "returns ErrConfig for a zero write timeout", give: httpserver.WithWriteTimeout(0)},
			{name: "returns ErrConfig for a zero idle timeout", give: httpserver.WithIdleTimeout(0)},
			{name: "returns ErrConfig for a zero drain delay", give: httpserver.WithDrainDelay(0)},
			{name: "returns ErrConfig for a zero shutdown timeout", give: httpserver.WithShutdownTimeout(0)},
			{name: "returns ErrConfig for a zero header limit", give: httpserver.WithMaxHeaderBytes(0)},
			{name: "returns ErrConfig for a negative header limit", give: httpserver.WithMaxHeaderBytes(-1)},
			{name: "returns ErrConfig for a zero body limit", give: httpserver.WithMaxBodyBytes(0)},
			{name: "returns ErrConfig for a zero in-flight limit", give: httpserver.WithMaxInFlight(0)},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, err := httpserver.New(http.NotFoundHandler(), required, tt.give)
				expect.ErrorIs(t, err, httpserver.ErrConfig, "New must refuse the option")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "ErrConfig must classify as Invalid")
			})
		}
	})

	t.Run("Run", func(t *testing.T) {
		t.Parallel()

		t.Run("serves the handler until the context ends", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, body)
			}))
			got := f.send(t, http.MethodGet, "/", http.NoBody, nil)
			expect.Equal(t, got.status, http.StatusOK, "the handler must serve the request")
			expect.Equal(t, got.body, body, "the client must receive the body of the handler")
			assert.NoError(t, f.stop(), "Run must return nil after the drain")
		})

		t.Run("drains at once for a context that ended before Run", func(t *testing.T) {
			t.Parallel()
			s, err := httpserver.New(http.NotFoundHandler(), required,
				httpserver.WithAddr(loopback), httpserver.WithDrainDelay(-1))
			assert.NoError(t, err, "New must accept the options")

			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			assert.NoError(t, s.Run(ctx), "Run must drain and return nil")
		})

		t.Run("leaves no goroutine after the drain", func(t *testing.T) {
			t.Parallel()
			check := assert.NoGoroutineLeaks(t, "Run must end every goroutine that it starts")

			s, err := httpserver.New(http.NotFoundHandler(), required,
				httpserver.WithAddr(loopback), httpserver.WithDrainDelay(-1))
			assert.NoError(t, err, "New must accept the options")

			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			assert.NoError(t, s.Run(ctx), "Run must drain")
			check()
		})

		t.Run("returns ErrClosed when it is called again", func(t *testing.T) {
			t.Parallel()
			s, err := httpserver.New(http.NotFoundHandler(), required,
				httpserver.WithAddr(loopback), httpserver.WithDrainDelay(-1))
			assert.NoError(t, err, "New must accept the options")

			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			assert.NoError(t, s.Run(ctx), "the first Run must return nil")

			err = s.Run(t.Context())
			expect.ErrorIs(t, err, httpserver.ErrClosed, "a second Run must return ErrClosed")
			expect.Equal(t, errs.Classify(err), errs.Invalid, "ErrClosed must classify as Invalid")
		})

		t.Run("returns ErrListen for an address that another listener uses", func(t *testing.T) {
			t.Parallel()
			addr := listen(t).Addr().String()

			// The errno of a listen on an address in use depends on the
			// platform. Unix returns EADDRINUSE, and Windows returns
			// WSAEADDRINUSE, which package syscall does not declare. A second
			// listen on the address returns the errno of the platform.
			var lc net.ListenConfig

			second, lerr := lc.Listen(t.Context(), tcp, addr)
			if second != nil {
				_ = second.Close()
			}

			inUse := assert.ErrorAs[syscall.Errno](t, lerr, "a second listen on the address must fail with an errno")

			s, err := httpserver.New(http.NotFoundHandler(), required, httpserver.WithAddr(addr))
			assert.NoError(t, err, "New must accept the options")

			err = s.Run(t.Context())
			expect.That(t, err).
				ErrorIs(httpserver.ErrListen, "Run must return ErrListen").
				ErrorIs(inUse, "the error must contain its cause")
			expect.Equal(t, errs.Classify(err), errs.Transient, "ErrListen must classify as Transient")
		})

		t.Run("returns ErrServe joined with the error of an Accept that fails", func(t *testing.T) {
			t.Parallel()
			ln := &failingListener{
				Listener: listen(t), fail: make(chan struct{}), closed: make(chan struct{}), err: errAccept,
			}
			close(ln.fail)

			s, err := httpserver.New(http.NotFoundHandler(), required, httpserver.WithListener(ln),
				httpserver.WithAddr(unbound))
			assert.NoError(t, err, "New must accept the options")

			err = s.Run(t.Context())
			expect.That(t, err).
				ErrorIs(httpserver.ErrServe, "Run must return ErrServe").
				ErrorIs(errAccept, "the error must contain its cause")
			expect.Equal(t, errs.Classify(err), errs.Transient, "ErrServe must classify as Transient")
		})

		t.Run("returns ErrServe when serving fails during the drain", func(t *testing.T) {
			t.Parallel()
			clk := fake.New(origin)
			ln := &failingListener{
				Listener: listen(t), fail: make(chan struct{}), closed: make(chan struct{}), err: errAccept,
			}
			s, err := httpserver.New(http.NotFoundHandler(), required, httpserver.WithClock(clk),
				httpserver.WithListener(ln), httpserver.WithAddr(unbound), httpserver.WithDrainDelay(time.Second))
			assert.NoError(t, err, "New must accept the options")

			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			go func() { done <- s.Run(ctx) }()

			cancel()
			clk.AwaitWaiters(1)
			close(ln.fail)
			await(t, ln.closed, "serving must close the listener")
			clk.Advance(time.Second)

			err = await(t, done, "Run must return")
			expect.That(t, err).
				ErrorIs(httpserver.ErrServe, "Run must return ErrServe").
				ErrorIs(errAccept, "the error must contain its cause")
		})

		t.Run("serves for the drain delay while Ready responds with 503", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
				httpserver.WithDrainDelay(time.Second))
			assert.Equal(t, f.send(t, http.MethodGet, "/", http.NoBody, nil).status, http.StatusOK,
				"the server must serve before the drain")

			f.cancel()
			f.clock.AwaitWaiters(1)

			rec := httptest.NewRecorder()
			f.server.Ready().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
			expect.Equal(t, rec.Code, http.StatusServiceUnavailable, "Ready must respond with 503 during the drain")
			expect.Equal(t, f.send(t, http.MethodGet, "/", http.NoBody, nil).status, http.StatusOK,
				"the server must serve during the drain delay")

			f.clock.Advance(time.Second)
			assert.NoError(t, f.stop(), "Run must return nil after the drain")
		})

		t.Run("returns ErrShutdown when the shutdown timeout elapses with a request in flight", func(t *testing.T) {
			t.Parallel()
			entered, release := make(chan struct{}), make(chan struct{})
			defer close(release)

			f := newFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				close(entered)
				<-release
			}), httpserver.WithShutdownTimeout(time.Minute))
			sent := make(chan error, 1)
			go func() { sent <- request(t, f.client, f.url) }()

			awaitBefore(t, entered, sent, "the handler must receive the request")
			f.cancel()
			f.clock.AwaitWaiters(1)
			f.clock.Advance(time.Minute)

			err := f.stop()
			expect.That(t, err).
				ErrorIs(httpserver.ErrShutdown, "Run must return ErrShutdown").
				ErrorIs(context.DeadlineExceeded, "the error must contain the elapsed deadline")
			expect.Equal(t, errs.Classify(err), errs.Unspecified, "ErrShutdown must have no class")
			expect.HasError(t, await(t, sent, "the request must end"), "the request in flight must lose its connection")
		})

		t.Run("waits for a request in flight when the shutdown timeout is negative", func(t *testing.T) {
			t.Parallel()
			entered, gate := make(chan struct{}), make(chan struct{})
			release := sync.OnceFunc(func() { close(gate) })
			defer release()

			f := newFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				close(entered)
				<-gate
			}), httpserver.WithShutdownTimeout(-1))
			sent := make(chan error, 1)
			go func() { sent <- request(t, f.client, f.url) }()

			awaitBefore(t, entered, sent, "the handler must receive the request")
			f.cancel()
			await(t, f.listener.closed, "the shutdown must close the listener")
			release()

			expect.NoError(t, await(t, sent, "the request must end"), "the request in flight must finish")
			expect.NoError(t, f.stop(), "Run must return nil after the request finished")
		})

		t.Run("returns ErrShutdown joined with the error of closing the listener", func(t *testing.T) {
			t.Parallel()
			// The request makes Serve track the listener, which the shutdown
			// closes.
			f := newFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			f.listener.closeErr = errClose
			assert.NoError(t, request(t, f.client, f.url), "the request before the drain must succeed")

			err := f.stop()
			expect.That(t, err).
				ErrorIs(httpserver.ErrShutdown, "Run must return ErrShutdown").
				ErrorIs(errClose, "the error must contain its cause")
		})

		t.Run("serves HTTP/2 to a cleartext client that starts with its preface", func(t *testing.T) {
			t.Parallel()
			protos := make(chan int, 1)
			f := newFixture(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				protos <- r.ProtoMajor
			}))

			var p http.Protocols
			p.SetUnencryptedHTTP2(true)
			client := &http.Client{Transport: &http.Transport{Protocols: &p}, Timeout: patience}
			defer client.CloseIdleConnections()

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, f.url, http.NoBody)
			assert.NoError(t, err, "the request of the case must build")
			assert.Equal(t, do(t, client, req).status, http.StatusOK, "the request of HTTP/2 must succeed")
			assert.Equal(t, await(t, protos, "the handler must run"), 2, "the request must use HTTP/2")
		})
	})

	t.Run("Addr", func(t *testing.T) {
		t.Parallel()

		t.Run("returns nil before Run listens", func(t *testing.T) {
			t.Parallel()
			s, err := httpserver.New(http.NotFoundHandler(), required)
			assert.NoError(t, err, "New must accept the options")
			assert.Nil(t, s.Addr(), "Addr before Run must be nil")
		})

		t.Run("returns the address that Run listened on", func(t *testing.T) {
			t.Parallel()
			s, err := httpserver.New(http.NotFoundHandler(), required,
				httpserver.WithAddr(loopback), httpserver.WithDrainDelay(-1))
			assert.NoError(t, err, "New must accept the options")

			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			assert.NoError(t, s.Run(ctx), "Run must drain")

			addr, ok := s.Addr().(*net.TCPAddr)
			assert.True(t, ok, "Addr must return the address of a TCP listener")
			expect.True(t, addr.IP.IsLoopback(), "the address must be on the host of WithAddr")
			expect.NotEqual(t, addr.Port, 0, "the port must be the port that the system chose")
		})
	})

	t.Run("Live", func(t *testing.T) {
		t.Parallel()

		t.Run("responds with 200 before Run listens", func(t *testing.T) {
			t.Parallel()
			s, err := httpserver.New(http.NotFoundHandler(), required)
			assert.NoError(t, err, "New must accept the options")

			w := &discardWriter{header: http.Header{}}
			s.Live().ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
			assert.Equal(t, w.status, http.StatusOK, "Live must write the status 200")
		})
	})

	t.Run("Ready", func(t *testing.T) {
		t.Parallel()

		t.Run("responds with 503 before Run listens", func(t *testing.T) {
			t.Parallel()
			s, err := httpserver.New(http.NotFoundHandler(), required)
			assert.NoError(t, err, "New must accept the options")

			rec := httptest.NewRecorder()
			s.Ready().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
			assert.Equal(t, rec.Code, http.StatusServiceUnavailable, "Ready must respond with 503 before Run")
		})

		t.Run("responds with 200 while Run serves", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			assert.Equal(t, f.send(t, http.MethodGet, "/", http.NoBody, nil).status, http.StatusOK,
				"the server must serve")

			w := &discardWriter{header: http.Header{}}
			f.server.Ready().ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
			assert.Equal(t, w.status, http.StatusOK, "Ready must write the status 200 while Run serves")
		})

		t.Run("responds with 503 after Run returns", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			assert.NoError(t, f.stop(), "Run must drain")

			rec := httptest.NewRecorder()
			f.server.Ready().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
			assert.Equal(t, rec.Code, http.StatusServiceUnavailable, "Ready must respond with 503 after Run")
		})

		t.Run("responds with 503 after serving failed", func(t *testing.T) {
			t.Parallel()
			ln := &failingListener{
				Listener: listen(t), fail: make(chan struct{}), closed: make(chan struct{}), err: errAccept,
			}
			close(ln.fail)

			s, err := httpserver.New(http.NotFoundHandler(), required, httpserver.WithListener(ln),
				httpserver.WithAddr(unbound))
			assert.NoError(t, err, "New must accept the options")
			assert.ErrorIs(t, s.Run(t.Context()), httpserver.ErrServe, "Run must fail")

			rec := httptest.NewRecorder()
			s.Ready().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
			assert.Equal(t, rec.Code, http.StatusServiceUnavailable, "Ready must respond with 503 after a failure")
		})

		t.Run("responds with 503 while a ready check fails", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
				httpserver.WithReadyCheck(func(context.Context) error { return errCheck }))
			assert.Equal(t, f.send(t, http.MethodGet, "/", http.NoBody, nil).status, http.StatusOK,
				"the server must serve")

			rec := httptest.NewRecorder()
			f.server.Ready().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
			assert.Equal(t, rec.Code, http.StatusServiceUnavailable, "Ready must respond with 503")
		})

		t.Run("logs the error of a ready check that fails", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
				httpserver.WithReadyCheck(func(context.Context) error { return errCheck }))

			rec := httptest.NewRecorder()
			f.server.Ready().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

			records := f.logs.find(messageNotReady)
			assert.Length(t, records, 1, "Ready must log the failed check once")
			expect.Equal(t, records[0].Level, slog.LevelWarn, "the record must have the level Warn")
			logged, _ := value(t, &records[0], keyError).Any().(error)
			expect.ErrorIs(t, logged, errCheck, "the record must contain the error of the check")
		})

		t.Run("passes the context of the probe's request to a ready check", func(t *testing.T) {
			t.Parallel()

			type key struct{}

			checked := make(chan any, 1)
			f := newFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
				httpserver.WithReadyCheck(func(ctx context.Context) error {
					checked <- ctx.Value(key{})

					return nil
				}))
			assert.Equal(t, f.send(t, http.MethodGet, "/", http.NoBody, nil).status, http.StatusOK,
				"the server must serve")

			ctx := context.WithValue(t.Context(), key{}, "probe")
			req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			f.server.Ready().ServeHTTP(rec, req)

			expect.Equal(t, rec.Code, http.StatusOK, "Ready must respond with 200 for a check that passes")
			expect.Equal(t, await(t, checked, "the check must run"), any("probe"),
				"the check must receive the context of the probe")
		})
	})
}

// TestServerAllocs checks the allocation contract of Addr and the probes.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
func TestServerAllocs(t *testing.T) {
	s, err := httpserver.New(http.NotFoundHandler(), required)
	assert.NoError(t, err, "New must accept the options")

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)

	t.Run("Addr", func(t *testing.T) {
		var addr net.Addr
		expect.MaxAllocs(t, func() { addr = s.Addr() }, 0, "Addr must not allocate")
		assert.Nil(t, addr, "the test must measure a Server before Run")
	})

	t.Run("Live", func(t *testing.T) {
		live := s.Live()
		w := &discardWriter{header: http.Header{}}
		expect.MaxAllocs(t, func() { live.ServeHTTP(w, req) }, 0, "Live must not allocate")
		assert.Equal(t, w.status, http.StatusOK, "the test must measure the response of Live")
	})

	t.Run("Ready", func(t *testing.T) {
		ready := s.Ready()
		w := &discardWriter{header: http.Header{}}
		expect.MaxAllocs(t, func() { ready.ServeHTTP(w, req) }, 0, "Ready must not allocate")
		assert.Equal(t, w.status, http.StatusServiceUnavailable, "the test must measure a Server before Run")
	})
}

// BenchmarkServer reports the cost of Addr and the probes, and fails when
// one allocates.
func BenchmarkServer(b *testing.B) {
	s, err := httpserver.New(http.NotFoundHandler(), required)
	assert.NoError(b, err, "New must accept the options")

	req := httptest.NewRequestWithContext(b.Context(), http.MethodGet, "/", nil)

	b.Run("Addr", func(b *testing.B) {
		var addr net.Addr

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			addr = s.Addr()
		}

		assert.Nil(b, addr, "the benchmark must measure a Server before Run")
	})

	b.Run("Live", func(b *testing.B) {
		live := s.Live()
		w := &discardWriter{header: http.Header{}}

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			live.ServeHTTP(w, req)
		}

		assert.Equal(b, w.status, http.StatusOK, "the benchmark must measure the response of Live")
	})

	b.Run("Ready", func(b *testing.B) {
		ready := s.Ready()
		w := &discardWriter{header: http.Header{}}

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			ready.ServeHTTP(w, req)
		}

		assert.Equal(b, w.status, http.StatusServiceUnavailable, "the benchmark must measure a Server before Run")
	})
}

// listen returns a TCP listener on a loopback port that the system chooses,
// which the cleanup of tb closes.
func listen(tb testing.TB) net.Listener {
	tb.Helper()

	var lc net.ListenConfig

	ln, err := lc.Listen(tb.Context(), tcp, loopback)
	assert.NoError(tb, err, "the listener of the case must listen")
	tb.Cleanup(func() { _ = ln.Close() })

	return ln
}

// do sends req with client, and returns its response with the body read
// and closed.
func do(t *testing.T, client *http.Client, req *http.Request) response {
	t.Helper()

	resp, err := client.Do(req)
	assert.NoError(t, err, "the request of the case must receive a response")

	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	assert.NoError(t, err, "the body of the response must read")

	return response{header: resp.Header, body: string(b), status: resp.StatusCode}
}

// request sends a GET of url with client, reads the body of its response,
// and returns the error of the exchange, for a goroutine of a case that
// cannot fail the test itself.
func request(t *testing.T, client *http.Client, url string) error {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
	if err != nil {
		return err //nolint:wrapcheck // the case asserts the error
	}

	resp, err := client.Do(req)
	if err != nil {
		return err //nolint:wrapcheck // the case asserts the error
	}

	defer resp.Body.Close()

	_, err = io.Copy(io.Discard, resp.Body)

	return err //nolint:wrapcheck // the case asserts the error
}

// value returns the value of the attribute key of r, and fails t when r has
// none.
func value(t *testing.T, r *slog.Record, key string) slog.Value {
	t.Helper()

	var (
		v     slog.Value
		found bool
	)

	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			v, found = a.Value, true
		}

		return !found
	})

	assert.True(t, found, "the record must contain the attribute "+key)

	return v
}

// tlsPair returns a server configuration with the certificate of
// net/http/httptest, and a client that trusts it, negotiates HTTP/2, and
// gives up after patience.
func tlsPair(t *testing.T) (*tls.Config, *http.Client) {
	t.Helper()

	ts := httptest.NewUnstartedServer(http.NotFoundHandler())
	ts.EnableHTTP2 = true
	ts.StartTLS()
	t.Cleanup(ts.Close)

	client := ts.Client()
	client.Timeout = patience

	return ts.TLS.Clone(), client
}

// await returns the next value of ch, and fails t when no value arrives
// within patience, so a case fails where a defect would leave it waiting.
func await[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()

	timer := time.NewTimer(patience)
	defer timer.Stop()

	select {
	case v := <-ch:
		return v
	case <-timer.C:
		t.Fatalf("%s: nothing arrived within %s", what, patience)

		var zero T

		return zero
	}
}

// awaitBefore returns the next value of ch, as await does, and fails t at
// once when end delivers first and ch has no value. A case passes the end
// of the request whose handler sends to ch, so it fails as soon as the
// request ends without the handler.
func awaitBefore[T, E any](t *testing.T, ch <-chan T, end <-chan E, what string) T {
	t.Helper()

	timer := time.NewTimer(patience)
	defer timer.Stop()

	var zero T

	select {
	case v := <-ch:
		return v
	case e := <-end:
		// A handler that sent before the request ended left its value in ch.
		select {
		case v := <-ch:
			return v
		default:
		}

		t.Fatalf("%s: the request ended first with %v", what, e)
	case <-timer.C:
		t.Fatalf("%s: nothing arrived within %s", what, patience)
	}

	return zero
}
