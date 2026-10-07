// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package httpclient_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/coretest/telemetrytest"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/net/httpclient"
	"go.thesmos.sh/core/net/internal/semconv"
	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/telemetry/noop"
	"go.thesmos.sh/core/telemetry/w3c"
)

// The fixture values of the cases.
const (
	// dependency is the name of the clients of the cases.
	dependency = "registry"

	// body is the body that the handlers of the cases write.
	body = "ok"

	// prefix is the content of the dst that the cases of AppendFetch pass,
	// which the result keeps before the body.
	prefix = "items:"

	// requestBody is the body that the cases of AppendFetchBody send.
	requestBody = "a checkpoint"

	// patience bounds every wait of a case for a value of a handler or of a
	// call, above any correct wait, so a case fails where a defect would
	// leave it waiting.
	patience = 5 * time.Second

	// attempts is the number of attempts of the retrier of the cases. A
	// channel to which the handler of a case sends a value per attempt has
	// room for every attempt, so a defect that retries more often fails the
	// case instead of blocking the handler.
	attempts = 3

	// traceID and spanID are the identity of every span that the reporter
	// of a case starts.
	traceID telemetry.TraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	spanID  telemetry.SpanID  = "00f067aa0ba902b7"
)

// The message of the log record of a call that failed, which the cases
// pin.
const messageFailed = "call failed"

// origin is the time of the fake clocks of the cases.
var origin = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

// The errors of the test doubles.
var (
	errBoom = errors.New("boom")
	errDNS  = errors.New("no resolution")

	// errHung is the error of a dial function whose context did not end
	// within patience.
	errHung = errors.New("the context of the dial did not end")
)

// required bundles the four options of the dependencies that New requires:
// a fake clock that the cases do not advance, a logger that discards its
// records, the noop reporter and the W3C propagator.
var required = httpclient.Options(
	httpclient.WithClock(fake.New(origin)),
	httpclient.WithLogger(slog.New(slog.DiscardHandler)),
	httpclient.WithReporter(noop.Reporter{}),
	httpclient.WithPropagator(w3c.Propagator{}),
)

// loopback admits the host of a test server, 127.0.0.1, and its address,
// which ReachPublic refuses.
var loopback = httpclient.Options(httpclient.WithHosts("127.0.0.1"), httpclient.WithReach(httpclient.ReachPrivate))

// failing is a resolver that resolves no name, so a call to a name fails at
// once and without a network.
var failing = &net.Resolver{
	PreferGo: true,
	Dial:     func(context.Context, string, string) (net.Conn, error) { return nil, errDNS },
}

// logs is a slog.Handler that keeps a clone of every record. Its methods
// are safe for concurrent use.
type logs struct {
	records []slog.Record
	mu      sync.Mutex
}

var _ slog.Handler = (*logs)(nil)

// Enabled reports true for every level.
func (*logs) Enabled(context.Context, slog.Level) bool { return true }

// Handle keeps a clone of r.
//
//nolint:gocritic // hugeParam: slog.Handler passes the Record by value
func (l *logs) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.records = append(l.records, r.Clone())

	return nil
}

// WithAttrs returns l, which keeps no attributes of its own.
func (l *logs) WithAttrs([]slog.Attr) slog.Handler { return l }

// WithGroup returns l, which keeps no groups of its own.
func (l *logs) WithGroup(string) slog.Handler { return l }

// all returns the records that l kept, in order.
func (l *logs) all() []slog.Record {
	l.mu.Lock()
	defer l.mu.Unlock()

	return slices.Clone(l.records)
}

// span is a span stub that the tracer of a reporter started.
type span struct {
	*telemetrytest.SpanStub

	opts []telemetry.SpanOption
}

// set is a histogram stub of one attribute set, with the attributes that
// bound it.
type set struct {
	*telemetrytest.HistogramStub

	attrs []telemetry.Attr
}

// reporter is a telemetry.Reporter whose tracer starts a span stub with the
// identity of traceID and spanID, and whose histogram binds a histogram
// stub for each attribute set. It keeps both in order, and its methods are
// safe for concurrent use.
type reporter struct {
	*telemetrytest.ReporterStub

	spans []*span
	sets  []*set
	mu    sync.Mutex
}

// newReporter returns a reporter.
func newReporter(tb testing.TB) *reporter {
	tb.Helper()

	r := &reporter{ReporterStub: telemetrytest.NewReporterStub(tb)}

	tracer := telemetrytest.NewTracerStub(tb)
	tracer.OnStart.Func(func(
		ctx context.Context, _ telemetry.SpanName, opts ...telemetry.SpanOption,
	) (context.Context, telemetry.Span) {
		s := &span{SpanStub: telemetrytest.NewSpanStub(nil), opts: slices.Clone(opts)}
		s.OnSpanContext.Returns(telemetry.SpanContext{TraceID: traceID, SpanID: spanID})

		r.mu.Lock()
		defer r.mu.Unlock()

		r.spans = append(r.spans, s)

		return ctx, s
	})
	r.OnTracer.Returns(tracer)

	histogram := telemetrytest.NewHistogramStub(tb)
	histogram.OnWith.Func(func(attrs []telemetry.Attr) telemetry.Histogram {
		s := &set{HistogramStub: telemetrytest.NewHistogramStub(nil), attrs: attrs}

		r.mu.Lock()
		defer r.mu.Unlock()

		r.sets = append(r.sets, s)

		return s
	})
	r.OnHistogram.Returns(histogram)

	return r
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

func TestClient(t *testing.T) {
	t.Parallel()

	t.Run("New", func(t *testing.T) {
		t.Parallel()

		t.Run("returns ErrConfig for an empty name", func(t *testing.T) {
			t.Parallel()
			_, err := httpclient.New("", required, loopback)
			expect.ErrorIs(t, err, httpclient.ErrConfig, "New must refuse an empty name")
			expect.Equal(t, errs.Classify(err), errs.Invalid, "ErrConfig must classify as Invalid")
		})

		t.Run("returns ErrConfig without the options that it requires", func(t *testing.T) {
			t.Parallel()
			_, err := httpclient.New(dependency)
			assert.ErrorIs(t, err, httpclient.ErrConfig, "New must refuse a Client without a clock")
			assert.Contains(t, err.Error(), "WithClock", "the error must name the missing option")
		})

		t.Run("returns ErrConfig without a host", func(t *testing.T) {
			t.Parallel()
			_, err := httpclient.New(dependency, required)
			assert.ErrorIs(t, err, httpclient.ErrConfig, "New must refuse a Client without hosts")
			assert.Contains(t, err.Error(), "WithHosts", "the error must name the missing option")
		})

		refused := []struct {
			give httpclient.Option
			name string
		}{
			{name: "returns ErrConfig for a nil option"},
			{name: "returns ErrConfig for a nil clock", give: httpclient.WithClock(nil)},
			{name: "returns ErrConfig for a nil logger", give: httpclient.WithLogger(nil)},
			{name: "returns ErrConfig for a nil reporter", give: httpclient.WithReporter(nil)},
			{name: "returns ErrConfig for a nil propagator", give: httpclient.WithPropagator(nil)},
			{name: "returns ErrConfig for a host with a scheme", give: httpclient.WithHosts("https://example.com")},
			{name: "returns ErrConfig for a host with a port", give: httpclient.WithHosts("example.com:443")},
			{name: "returns ErrConfig for a host with a wildcard", give: httpclient.WithHosts("*.example.com")},
			{name: "returns ErrConfig for a host with an empty label", give: httpclient.WithHosts("example..com")},
			{name: "returns ErrConfig for an empty host", give: httpclient.WithHosts("")},
			{name: "returns ErrConfig for a dot alone", give: httpclient.WithHosts(".")},
			{name: "returns ErrConfig for an IP address after a dot", give: httpclient.WithHosts(".10.0.0.1")},
			{name: "returns ErrConfig for a reach outside its constants", give: httpclient.WithReach(2)},
			{name: "returns ErrConfig for a zero timeout", give: httpclient.WithTimeout(0)},
			{name: "returns ErrConfig for a zero dial timeout", give: httpclient.WithDialTimeout(0)},
			{name: "returns ErrConfig for a zero TLS handshake timeout", give: httpclient.WithTLSHandshakeTimeout(0)},
			{name: "returns ErrConfig for a zero idle timeout", give: httpclient.WithIdleConnTimeout(0)},
			{name: "returns ErrConfig for zero idle connections", give: httpclient.WithMaxIdleConnsPerHost(0)},
			{name: "returns ErrConfig for negative idle connections", give: httpclient.WithMaxIdleConnsPerHost(-1)},
			{name: "returns ErrConfig for a zero response limit", give: httpclient.WithMaxResponseBytes(0)},
			{
				name: "returns ErrConfig for a dial function of a client of ReachPublic",
				give: httpclient.Options(
					httpclient.WithReach(httpclient.ReachPublic),
					httpclient.WithDialContext(func(context.Context, string, string) (net.Conn, error) {
						return nil, errBoom
					}),
				),
			},
			{
				name: "returns ErrConfig for a dial function together with a resolver",
				give: httpclient.Options(
					httpclient.WithResolver(failing),
					httpclient.WithDialContext(func(context.Context, string, string) (net.Conn, error) {
						return nil, errBoom
					}),
				),
			},
		}
		for _, tt := range refused {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, err := httpclient.New(dependency, required, loopback, tt.give)
				expect.ErrorIs(t, err, httpclient.ErrConfig, "New must refuse the option")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "ErrConfig must classify as Invalid")
			})
		}

		t.Run("returns a Client for every form of host", func(t *testing.T) {
			t.Parallel()
			_, err := httpclient.New(dependency, required, httpclient.WithHosts(
				"example.com", ".example.com", "10.0.0.1", "::1", "_service.internal", "Example.COM",
			))
			assert.NoError(t, err, "New must accept the hosts")
		})
	})

	t.Run("Do", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the response of a request to an admitted host", func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, body)
			}))
			t.Cleanup(srv.Close)

			c, err := httpclient.New(dependency, required, loopback)
			assert.NoError(t, err, "New must accept the options")

			status, got, err := get(t, c, srv.URL)
			assert.NoError(t, err, "Do must return the response")
			expect.Equal(t, status, http.StatusOK, "Do must return the status of the response")
			expect.Equal(t, got, body, "Do must return the body of the response")
		})

		t.Run("returns the response of a status that the classification refuses without an error", func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			t.Cleanup(srv.Close)

			l := &logs{}
			c, err := httpclient.New(dependency, required, loopback, httpclient.WithLogger(slog.New(l)))
			assert.NoError(t, err, "New must accept the options")

			status, _, err := get(t, c, srv.URL)
			assert.NoError(t, err, "Do must return the response")
			assert.Equal(t, status, http.StatusServiceUnavailable, "Do must return the status of the response")

			records := l.all()
			assert.Length(t, records, 1, "Do must log the failed call")
			expect.Equal(t, records[0].Level, slog.LevelWarn, "the record must have the level Warn")
			expect.Equal(t, records[0].Message, messageFailed, "the record must have the message of a failed call")
		})

		t.Run("logs no call that succeeded", func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			t.Cleanup(srv.Close)

			l := &logs{}
			c, err := httpclient.New(dependency, required, loopback, httpclient.WithLogger(slog.New(l)))
			assert.NoError(t, err, "New must accept the options")

			_, _, err = get(t, c, srv.URL)
			assert.NoError(t, err, "Do must return the response")
			assert.Empty(t, l.all(), "Do must not log a call that succeeded")
		})

		blocked := []struct {
			name string
			url  string
		}{
			{name: "returns ErrBlocked for a host that WithHosts does not admit", url: "http://127.0.0.2/"},
			{name: "returns ErrBlocked for a scheme other than http and https", url: "ftp://127.0.0.1/"},
		}
		for _, tt := range blocked {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				l := &logs{}
				c, err := httpclient.New(dependency, required, loopback, httpclient.WithLogger(slog.New(l)))
				assert.NoError(t, err, "New must accept the options")

				_, _, err = get(t, c, tt.url)
				expect.ErrorIs(t, err, httpclient.ErrBlocked, "Do must refuse the request")
				expect.Equal(t, errs.Classify(err), errs.Denied, "ErrBlocked must classify as Denied")
				expect.Length(t, l.all(), 1, "Do must log the refused call")
			})
		}

		t.Run("returns ErrBlocked for a request without a URL", func(t *testing.T) {
			t.Parallel()
			c, err := httpclient.New(dependency, required, loopback)
			assert.NoError(t, err, "New must accept the options")

			req := (&http.Request{Method: http.MethodGet}).WithContext(t.Context())

			_, err = c.Do(req) //nolint:bodyclose // Do returns no response with the error
			assert.ErrorIs(t, err, httpclient.ErrBlocked, "Do must refuse a request without a URL")
		})

		admitted := []struct {
			name string
			url  string
		}{
			{name: "admits a host of WithHosts without regard to case", url: "http://API.Example.Test/"},
			{name: "admits a subdomain of a host after a dot", url: "http://v2.api.example.test/"},
			{name: "admits the host of a suffix", url: "http://example.test/"},
		}
		for _, tt := range admitted {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				// The resolver fails, so an admitted host fails at the
				// resolution and a refused host fails before it.
				c, err := httpclient.New(dependency, required,
					httpclient.WithHosts("api.example.test", ".example.test"), httpclient.WithResolver(failing))
				assert.NoError(t, err, "New must accept the options")

				_, _, err = get(t, c, tt.url)
				assert.ErrorIsNot(t, err, httpclient.ErrBlocked, "Do must admit the host")

				dnsErr := assert.ErrorAs[*net.DNSError](t, err, "the call must query the resolver")
				assert.Equal(t, dnsErr.Err, errDNS.Error(), "the call must fail with the error of the resolver")
			})
		}

		refusedHosts := []struct {
			name string
			url  string
		}{
			{name: "returns ErrBlocked for a host that only ends with a suffix", url: "http://badexample.test/"},
			{name: "returns ErrBlocked for a host that starts with a dot", url: "http://.example.test/"},
		}
		for _, tt := range refusedHosts {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				c, err := httpclient.New(dependency, required,
					httpclient.WithHosts(".example.test"), httpclient.WithResolver(failing))
				assert.NoError(t, err, "New must accept the options")

				_, _, err = get(t, c, tt.url)
				assert.ErrorIs(t, err, httpclient.ErrBlocked, "Do must refuse the host")
			})
		}

		t.Run("returns a Transient error for a failure of the transport", func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			srv.Close()

			c, err := httpclient.New(dependency, required, loopback)
			assert.NoError(t, err, "New must accept the options")

			_, _, err = get(t, c, srv.URL)
			assert.HasError(t, err, "Do must fail without a server")
			expect.Equal(t, errs.Classify(err), errs.Transient, "a failure of the transport must be Transient")
			expect.Contains(t, err.Error(), "httpclient: "+dependency, "the error must name the dependency")
		})

		t.Run("returns a Transient error for an attempt beyond the timeout", func(t *testing.T) {
			t.Parallel()
			release := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
			t.Cleanup(srv.Close)
			t.Cleanup(func() { close(release) })

			c, err := httpclient.New(dependency, required, loopback, httpclient.WithTimeout(50*time.Millisecond))
			assert.NoError(t, err, "New must accept the options")

			_, _, err = get(t, c, srv.URL)
			expect.ErrorIs(t, err, context.DeadlineExceeded, "Do must end at the timeout")
			expect.Equal(t, errs.Classify(err), errs.Transient, "an attempt beyond its timeout must be Transient")
		})

		t.Run("returns the error of the context without a log when the caller cancels", func(t *testing.T) {
			t.Parallel()
			entered, release := make(chan struct{}), make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				close(entered)
				<-release
			}))
			t.Cleanup(srv.Close)
			t.Cleanup(func() { close(release) })

			l := &logs{}
			c, err := httpclient.New(dependency, required, loopback, httpclient.WithLogger(slog.New(l)))
			assert.NoError(t, err, "New must accept the options")

			ctx, cancel := context.WithCancel(t.Context())
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, http.NoBody)
			assert.NoError(t, err, "the request must build")

			go func() {
				<-entered
				cancel()
			}()

			_, err = c.Do(req) //nolint:bodyclose // the call fails without a response
			expect.ErrorIs(t, err, context.Canceled, "Do must return the error of the context")
			expect.Equal(t, errs.Classify(err), errs.Unspecified, "the error of a context must have no class")
			expect.Empty(t, l.all(), "Do must not log a call that its caller cancelled")
		})

		t.Run("returns the response when the context ends after a classification that succeeded", func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, body)
			}))
			t.Cleanup(srv.Close)

			ctx, cancel := context.WithCancel(t.Context())
			classify := func(*http.Response) error {
				cancel()

				return nil
			}
			c, err := httpclient.New(dependency, required, loopback, httpclient.WithClassify(classify))
			assert.NoError(t, err, "New must accept the options")

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, http.NoBody)
			assert.NoError(t, err, "the request must build")

			resp, err := c.Do(req)
			assert.NoError(t, err, "Do must return the response of a call that succeeded")
			assert.NotNil(t, resp, "Do must return the response of a call that succeeded")
			assert.NoError(t, resp.Body.Close(), "the body must close")
		})

		t.Run("sends the headers of the caller's request", func(t *testing.T) {
			t.Parallel()
			received := make(chan string, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				received <- r.Header.Get("Accept")
			}))
			t.Cleanup(srv.Close)

			c, err := httpclient.New(dependency, required, loopback)
			assert.NoError(t, err, "New must accept the options")

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, http.NoBody)
			assert.NoError(t, err, "the request must build")
			req.Header.Set("Accept", "application/json")

			resp, err := c.Do(req)
			assert.NoError(t, err, "Do must return the response")
			assert.NoError(t, resp.Body.Close(), "the body must close")
			assert.Equal(t, await(t, received, "the server must receive the request"), "application/json",
				"the server must receive the header of the caller")
		})

		t.Run("sends the body of the caller's request with its length", func(t *testing.T) {
			t.Parallel()
			lengths := make(chan int64, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				lengths <- r.ContentLength
			}))
			t.Cleanup(srv.Close)

			c, err := httpclient.New(dependency, required, loopback)
			assert.NoError(t, err, "New must accept the options")

			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL, strings.NewReader(body))
			assert.NoError(t, err, "the request must build")

			resp, err := c.Do(req)
			assert.NoError(t, err, "Do must return the response")
			assert.NoError(t, resp.Body.Close(), "the body must close")
			assert.Equal(t, await(t, lengths, "the server must receive the request"), int64(len(body)),
				"the request must declare the length of the body of the caller")
		})

		t.Run("injects the trace of the span of the attempt", func(t *testing.T) {
			t.Parallel()
			parents := make(chan string, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				parents <- r.Header.Get("Traceparent")
			}))
			t.Cleanup(srv.Close)

			r := newReporter(t)
			c, err := httpclient.New(dependency, required, loopback, httpclient.WithReporter(r))
			assert.NoError(t, err, "New must accept the options")

			_, _, err = get(t, c, srv.URL)
			assert.NoError(t, err, "Do must return the response")
			assert.Equal(t, await(t, parents, "the handler must run"), "00-"+string(traceID)+"-"+string(spanID)+"-00",
				"the request must carry the traceparent of the span")

			spans := r.started()
			assert.Length(t, spans, 1, "the client must start a span per attempt")
			expect.Equal(t, telemetry.ApplySpanOptions(spans[0].opts), telemetry.SpanKindClient,
				"the span must be of the kind client")
			spans[0].OnEnd.AssertCalledOnce(t, "the span must end")
		})

		t.Run("records the duration of an attempt into the set of its request", func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			}))
			t.Cleanup(srv.Close)

			r := newReporter(t)
			c, err := httpclient.New(dependency, required, loopback, httpclient.WithReporter(r))
			assert.NoError(t, err, "New must accept the options")

			_, _, err = get(t, c, srv.URL)
			assert.NoError(t, err, "Do must return the response")

			sets := r.bound()
			assert.Length(t, sets, 1, "the client must bind one set")
			assert.Equal(t, sets[0].attrs, []telemetry.Attr{
				telemetry.AttrString(semconv.RequestMethod, http.MethodGet),
				telemetry.AttrString(semconv.ServerAddress, "127.0.0.1"),
				telemetry.AttrInt(semconv.ServerPort, int64(port(t, srv.URL))),
				telemetry.AttrInt(semconv.ResponseStatusCode, http.StatusNotFound),
				telemetry.AttrString(semconv.ErrorType, "NotFound"),
			}, "the set must have the attributes of the request")
			sets[0].OnRecord.AssertCalledOnce(t, "the set must record the attempt")

			attrs := r.started()[0].OnSetAttributes.AssertCalledOnce(t, "the span must receive the attributes").Attrs
			assert.Equal(t, attrs, sets[0].attrs, "the span must receive the attributes of the set")
		})

		t.Run("records no error type for an attempt that succeeded", func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			t.Cleanup(srv.Close)

			r := newReporter(t)
			c, err := httpclient.New(dependency, required, loopback, httpclient.WithReporter(r))
			assert.NoError(t, err, "New must accept the options")

			_, _, err = get(t, c, srv.URL)
			assert.NoError(t, err, "Do must return the response")

			sets := r.bound()
			assert.Length(t, sets, 1, "the client must bind one set")
			assert.Equal(t, sets[0].attrs, []telemetry.Attr{
				telemetry.AttrString(semconv.RequestMethod, http.MethodGet),
				telemetry.AttrString(semconv.ServerAddress, "127.0.0.1"),
				telemetry.AttrInt(semconv.ServerPort, int64(port(t, srv.URL))),
				telemetry.AttrInt(semconv.ResponseStatusCode, http.StatusOK),
			}, "the set of an attempt that succeeded must have no error type")
		})

		t.Run("records no status for an attempt without a response", func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			srv.Close()

			r := newReporter(t)
			c, err := httpclient.New(dependency, required, loopback, httpclient.WithReporter(r))
			assert.NoError(t, err, "New must accept the options")

			_, _, err = get(t, c, srv.URL)
			assert.HasError(t, err, "Do must fail without a server")

			sets := r.bound()
			assert.Length(t, sets, 1, "the client must bind one set")
			assert.Equal(t, sets[0].attrs, []telemetry.Attr{
				telemetry.AttrString(semconv.RequestMethod, http.MethodGet),
				telemetry.AttrString(semconv.ServerAddress, "127.0.0.1"),
				telemetry.AttrInt(semconv.ServerPort, int64(port(t, srv.URL))),
				telemetry.AttrString(semconv.ErrorType, "Transient"),
			}, "the set of an attempt without a response must have no status")
		})

		t.Run("calls the function of WithPrepare on the request of each attempt", func(t *testing.T) {
			t.Parallel()
			tokens := make(chan string, attempts)
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				tokens <- r.Header.Get("Authorization")
				if hits.Add(1) == 1 {
					w.WriteHeader(http.StatusServiceUnavailable)
				}
			}))
			t.Cleanup(srv.Close)

			var issued atomic.Int32
			c, err := httpclient.New(dependency, required, loopback,
				httpclient.WithRetrier(retrier(t, fake.New(origin))),
				httpclient.WithPrepare(func(r *http.Request) error {
					r.Header.Set("Authorization", "Bearer "+strconv.Itoa(int(issued.Add(1))))

					return nil
				}))
			assert.NoError(t, err, "New must accept the options")

			status, _, err := get(t, c, srv.URL)
			assert.NoError(t, err, "Do must return the response of the retry")
			expect.Equal(t, status, http.StatusOK, "the retry must succeed")
			expect.Equal(t, await(t, tokens, "the first attempt must arrive"), "Bearer 1",
				"the first attempt must carry the first token")
			expect.Equal(t, await(t, tokens, "the second attempt must arrive"), "Bearer 2",
				"the second attempt must carry a token of its own")
		})

		t.Run("returns the error of the function of WithPrepare without a request", func(t *testing.T) {
			t.Parallel()
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
			t.Cleanup(srv.Close)

			c, err := httpclient.New(dependency, required, loopback,
				httpclient.WithPrepare(func(*http.Request) error { return errBoom }))
			assert.NoError(t, err, "New must accept the options")

			_, _, err = get(t, c, srv.URL)
			assert.ErrorIs(t, err, errBoom, "Do must return the error of prepare")
			expect.Equal(t, err.Error(), "httpclient: "+dependency+": prepare the request: "+errBoom.Error(),
				"the error must name the dependency and the step")
			expect.Equal(t, hits.Load(), int32(0), "the server must receive no request")
		})

		t.Run("ends the span of an attempt whose preparation failed", func(t *testing.T) {
			t.Parallel()
			r := newReporter(t)
			c, err := httpclient.New(dependency, required, loopback, httpclient.WithReporter(r),
				httpclient.WithPrepare(func(*http.Request) error { return errBoom }))
			assert.NoError(t, err, "New must accept the options")

			_, _, err = get(t, c, "http://127.0.0.1/")
			assert.ErrorIs(t, err, errBoom, "Do must return the error of prepare")

			spans := r.started()
			assert.Length(t, spans, 1, "the client must start a span for the attempt")
			end := spans[0].OnEnd.AssertCalledOnce(t, "the span must end once")
			assert.ErrorIs(t, end.Err, errBoom, "the span must end with the error of prepare")
		})

		t.Run("leaves the headers of the caller's request unchanged", func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			t.Cleanup(srv.Close)

			c, err := httpclient.New(dependency, required, loopback, httpclient.WithReporter(newReporter(t)),
				httpclient.WithPrepare(func(r *http.Request) error {
					r.Header.Set("Authorization", "Bearer token")

					return nil
				}))
			assert.NoError(t, err, "New must accept the options")

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, http.NoBody)
			assert.NoError(t, err, "the request must build")

			resp, err := c.Do(req)
			assert.NoError(t, err, "Do must return the response")
			assert.NoError(t, resp.Body.Close(), "the body must close")
			assert.Equal(t, req.Header, http.Header{}, "Do must leave the headers of the caller's request unchanged")
		})

		t.Run("sends a request without headers", func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			t.Cleanup(srv.Close)

			c, err := httpclient.New(dependency, required, loopback, httpclient.WithReporter(newReporter(t)))
			assert.NoError(t, err, "New must accept the options")

			u, err := url.Parse(srv.URL)
			assert.NoError(t, err, "the URL of the server must parse")

			resp, err := c.Do((&http.Request{Method: http.MethodGet, URL: u}).WithContext(t.Context()))
			assert.NoError(t, err, "Do must send a request without a header map")
			assert.NoError(t, resp.Body.Close(), "the body must close")
		})

		t.Run("follows a redirect to an admitted host", func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(redirector())
			t.Cleanup(srv.Close)

			c, err := httpclient.New(dependency, required, loopback)
			assert.NoError(t, err, "New must accept the options")

			status, got, err := get(t, c, srv.URL+"/redirect/1")
			assert.NoError(t, err, "Do must follow the redirect")
			expect.Equal(t, status, http.StatusOK, "Do must return the status after the redirect")
			expect.Equal(t, got, body, "Do must return the body after the redirect")
		})

		t.Run("follows a redirect from https to https", func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewTLSServer(redirector())
			t.Cleanup(srv.Close)

			transport, ok := srv.Client().Transport.(*http.Transport)
			assert.True(t, ok, "the client of httptest must have an http.Transport")

			c, err := httpclient.New(dependency, required, loopback, httpclient.WithTLS(transport.TLSClientConfig))
			assert.NoError(t, err, "New must accept the options")

			status, _, err := get(t, c, srv.URL+"/redirect/1")
			assert.NoError(t, err, "Do must follow a redirect that keeps https")
			assert.Equal(t, status, http.StatusOK, "Do must return the status after the redirect")
		})

		t.Run("follows 5 redirects", func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(redirector())
			t.Cleanup(srv.Close)

			c, err := httpclient.New(dependency, required, loopback)
			assert.NoError(t, err, "New must accept the options")

			status, _, err := get(t, c, srv.URL+"/redirect/5")
			assert.NoError(t, err, "Do must follow the redirects")
			assert.Equal(t, status, http.StatusOK, "Do must return the status after 5 redirects")
		})

		t.Run("returns the sixth redirect response as it is", func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(redirector())
			t.Cleanup(srv.Close)

			c, err := httpclient.New(dependency, required, loopback)
			assert.NoError(t, err, "New must accept the options")

			status, _, err := get(t, c, srv.URL+"/redirect/6")
			assert.NoError(t, err, "Do must return the redirect response")
			assert.Equal(t, status, http.StatusFound, "Do must return the status of the sixth redirect")
		})

		t.Run("returns ErrBlocked for a redirect to a host that WithHosts does not admit", func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.RedirectHandler("http://127.0.0.2/", http.StatusFound))
			t.Cleanup(srv.Close)

			c, err := httpclient.New(dependency, required, loopback)
			assert.NoError(t, err, "New must accept the options")

			_, _, err = get(t, c, srv.URL)
			assert.ErrorIs(t, err, httpclient.ErrBlocked, "Do must refuse the redirect")
		})

		t.Run("returns ErrBlocked for a redirect from https to http", func(t *testing.T) {
			t.Parallel()
			plain := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			t.Cleanup(plain.Close)

			secure := httptest.NewTLSServer(http.RedirectHandler(plain.URL, http.StatusFound))
			t.Cleanup(secure.Close)

			transport, ok := secure.Client().Transport.(*http.Transport)
			assert.True(t, ok, "the client of httptest must have an http.Transport")

			c, err := httpclient.New(dependency, required, loopback, httpclient.WithTLS(transport.TLSClientConfig))
			assert.NoError(t, err, "New must accept the options")

			_, _, err = get(t, c, secure.URL)
			assert.ErrorIs(t, err, httpclient.ErrBlocked, "Do must refuse the downgrade")
		})

		t.Run("uses the classification of WithClassify", func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			t.Cleanup(srv.Close)

			r := newReporter(t)
			c, err := httpclient.New(dependency, required, loopback, httpclient.WithReporter(r),
				httpclient.WithClassify(func(*http.Response) error { return errs.WithClass(errBoom, errs.Conflict) }))
			assert.NoError(t, err, "New must accept the options")

			status, _, err := get(t, c, srv.URL)
			assert.NoError(t, err, "Do must return the response that the classification refuses")
			assert.Equal(t, status, http.StatusOK, "Do must return the status of the response")

			end := r.started()[0].OnEnd.AssertCalledOnce(t, "the span must end once")
			assert.ErrorIs(t, end.Err, errBoom, "the span must end with the error of the classification")
		})
	})

	t.Run("Fetch", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the body of a response that succeeded", func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, body)
			}))
			t.Cleanup(srv.Close)

			c, err := httpclient.New(dependency, required, loopback)
			assert.NoError(t, err, "New must accept the options")

			got, err := fetch(t, c, http.MethodGet, srv.URL, http.NoBody, nil)
			assert.NoError(t, err, "Fetch must read the body")
			assert.Equal(t, string(got), body, "Fetch must return the body")
		})

		t.Run("returns a StatusError with the first 1 KiB of the body of a refused response", func(t *testing.T) {
			t.Parallel()
			long := strings.Repeat("e", 2048)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, long)
			}))
			t.Cleanup(srv.Close)

			c, err := httpclient.New(dependency, required, loopback)
			assert.NoError(t, err, "New must accept the options")

			_, err = fetch(t, c, http.MethodGet, srv.URL, http.NoBody, nil)
			se := assert.ErrorAs[*httpclient.StatusError](t, err, "Fetch must return a StatusError")
			expect.Equal(t, se.Status, http.StatusNotFound, "the StatusError must carry the status")
			expect.Equal(t, se.Dependency, dependency, "the StatusError must name the dependency")
			expect.Equal(t, string(se.Body), long[:1024], "the StatusError must carry the first 1 KiB of the body")
			expect.Equal(t, errs.Classify(err), errs.NotFound, "a 404 must classify as NotFound")
		})

		t.Run("returns a StatusError for a status of 300", func(t *testing.T) {
			t.Parallel()
			// net/http follows no redirect for 300.
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusMultipleChoices)
			}))
			t.Cleanup(srv.Close)

			c, err := httpclient.New(dependency, required, loopback)
			assert.NoError(t, err, "New must accept the options")

			_, err = fetch(t, c, http.MethodGet, srv.URL, http.NoBody, nil)
			se := assert.ErrorAs[*httpclient.StatusError](t, err, "Fetch must refuse a status of 300")
			assert.Equal(t, se.Status, http.StatusMultipleChoices, "the StatusError must carry the status")
		})

		t.Run("returns nil for a response to HEAD", func(t *testing.T) {
			t.Parallel()
			// The server declares the length of the body that a GET returns.
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, body)
			}))
			t.Cleanup(srv.Close)

			c, err := httpclient.New(dependency, required, loopback)
			assert.NoError(t, err, "New must accept the options")

			got, err := fetch(t, c, http.MethodHead, srv.URL, http.NoBody, nil)
			assert.NoError(t, err, "Fetch must accept a response to HEAD")
			assert.Nil(t, got, "the body of a HEAD must be nil")
		})

		t.Run("returns a Transient error for a body that ends early", func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Length", "10")
				_, _ = io.WriteString(w, "ab")
				conn, _, err := http.NewResponseController(w).Hijack()
				if err == nil {
					_ = conn.Close()
				}
			}))
			t.Cleanup(srv.Close)

			c, err := httpclient.New(dependency, required, loopback)
			assert.NoError(t, err, "New must accept the options")

			_, err = fetch(t, c, http.MethodGet, srv.URL, http.NoBody, nil)
			assert.HasError(t, err, "Fetch must fail on a body that ends early")
			expect.Equal(t, errs.Classify(err), errs.Transient, "a read that fails must be Transient")
			expect.Contains(t, err.Error(), "httpclient: "+dependency+": read the body",
				"the error must name the dependency and the step")
		})

		// stalled sends the header of a body of 10 bytes, and no byte of the
		// body before the client stops waiting or patience ends.
		stalled := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "10")
			w.WriteHeader(http.StatusOK)
			_ = http.NewResponseController(w).Flush()

			timer := time.NewTimer(patience)
			defer timer.Stop()

			select {
			case <-r.Context().Done():
			case <-timer.C:
			}
		})

		stalls := []struct {
			opts     []httpclient.Option
			name     string
			deadline time.Duration
			want     errs.Class
		}{
			{
				name:     "returns a Transient error for a body beyond the timeout",
				opts:     []httpclient.Option{httpclient.WithTimeout(50 * time.Millisecond)},
				deadline: patience,
				want:     errs.Transient,
			},
			{
				name:     "returns the error of the context for a body beyond the deadline of the caller",
				deadline: 50 * time.Millisecond,
				want:     errs.Unspecified,
			},
		}
		for _, tt := range stalls {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				srv := httptest.NewServer(stalled)
				t.Cleanup(srv.Close)

				c, err := httpclient.New(dependency, required, loopback, httpclient.Options(tt.opts...))
				assert.NoError(t, err, "New must accept the options")

				ctx, cancel := context.WithTimeout(t.Context(), tt.deadline)
				defer cancel()

				req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, http.NoBody)
				assert.NoError(t, err, "the request must build")

				_, err = c.Fetch(req)
				expect.ErrorIs(t, err, context.DeadlineExceeded, "the read must end at the earlier deadline")
				expect.Equal(t, errs.Classify(err), tt.want, "the error must have the class of its deadline")
			})
		}

		limits := []struct {
			opts    []httpclient.Option
			name    string
			size    int
			chunked bool
			fails   bool
		}{
			{
				name:  "returns ErrTooLarge for a declared body beyond the limit",
				opts:  []httpclient.Option{httpclient.WithMaxResponseBytes(8)},
				size:  9,
				fails: true,
			},
			{
				name: "returns a declared body of the limit",
				opts: []httpclient.Option{httpclient.WithMaxResponseBytes(8)},
				size: 8,
			},
			{
				name:    "returns ErrTooLarge for a body of unknown length beyond the limit",
				opts:    []httpclient.Option{httpclient.WithMaxResponseBytes(8)},
				size:    9,
				chunked: true,
				fails:   true,
			},
			{
				name:    "returns a body of unknown length of the limit",
				opts:    []httpclient.Option{httpclient.WithMaxResponseBytes(8)},
				size:    8,
				chunked: true,
			},
			{
				name: "returns a declared body beyond the limit for a negative limit",
				opts: []httpclient.Option{httpclient.WithMaxResponseBytes(-1)},
				size: 9,
			},
			{
				name:    "returns a body of unknown length for a negative limit",
				opts:    []httpclient.Option{httpclient.WithMaxResponseBytes(-1)},
				size:    9,
				chunked: true,
			},
		}
		for _, tt := range limits {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				payload := strings.Repeat("a", tt.size)
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if tt.chunked {
						w.(http.Flusher).Flush()
					}

					_, _ = io.WriteString(w, payload)
				}))
				t.Cleanup(srv.Close)

				c, err := httpclient.New(dependency, required, loopback, httpclient.Options(tt.opts...))
				assert.NoError(t, err, "New must accept the options")

				got, err := fetch(t, c, http.MethodGet, srv.URL, http.NoBody, nil)
				if tt.fails {
					expect.ErrorIs(t, err, httpclient.ErrTooLarge, "Fetch must refuse the body")
					expect.Equal(t, errs.Classify(err), errs.Invalid, "ErrTooLarge must classify as Invalid")

					return
				}

				assert.NoError(t, err, "Fetch must read the body")
				assert.Equal(t, string(got), payload, "Fetch must return the body")
			})
		}
	})

	t.Run("AppendFetch", func(t *testing.T) {
		t.Parallel()

		// A dst with room has 1 KiB of capacity beyond prefix: room for the
		// body, and for the 512 bytes that each read of a body of unknown
		// length makes room for.
		appends := []struct {
			name    string
			room    int
			chunked bool
		}{
			{name: "appends a declared body to the bytes of dst"},
			{name: "appends a body of unknown length to the bytes of dst", chunked: true},
			{name: "writes a declared body into the room of dst", room: 1024},
			{name: "writes a body of unknown length into the room of dst", room: 1024, chunked: true},
		}
		for _, tt := range appends {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if tt.chunked {
						w.(http.Flusher).Flush()
					}

					_, _ = io.WriteString(w, body)
				}))
				t.Cleanup(srv.Close)

				c, err := httpclient.New(dependency, required, loopback)
				assert.NoError(t, err, "New must accept the options")

				dst := append(make([]byte, 0, len(prefix)+tt.room), prefix...)
				got, err := appendFetch(t, c, dst, http.MethodGet, srv.URL)
				assert.NoError(t, err, "AppendFetch must read the body")
				assert.Equal(t, string(got), prefix+body, "AppendFetch must keep the bytes of dst before the body")

				if tt.room > 0 {
					assert.Equal(t, &got[0], &dst[0], "AppendFetch must write into the array of dst",
						assert.ByIdentity())
				}
			})
		}

		// prefix is 6 bytes, so a body of 8 bytes fits the limit of 8 only
		// when the limit counts the appended bytes alone.
		limits := []struct {
			name    string
			size    int
			chunked bool
			fails   bool
		}{
			{name: "returns a declared body of the limit after the bytes of dst", size: 8},
			{name: "returns a body of unknown length of the limit after the bytes of dst", size: 8, chunked: true},
			{name: "returns dst with ErrTooLarge for a declared body beyond the limit", size: 9, fails: true},
			{
				name:    "returns dst with ErrTooLarge for a body of unknown length beyond the limit",
				size:    9,
				chunked: true,
				fails:   true,
			},
		}
		for _, tt := range limits {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				payload := strings.Repeat("a", tt.size)
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if tt.chunked {
						w.(http.Flusher).Flush()
					}

					_, _ = io.WriteString(w, payload)
				}))
				t.Cleanup(srv.Close)

				c, err := httpclient.New(dependency, required, loopback, httpclient.WithMaxResponseBytes(8))
				assert.NoError(t, err, "New must accept the options")

				got, err := appendFetch(t, c, []byte(prefix), http.MethodGet, srv.URL)
				if tt.fails {
					expect.ErrorIs(t, err, httpclient.ErrTooLarge, "AppendFetch must refuse the body")
					expect.Equal(t, string(got), prefix, "AppendFetch must return dst unchanged")

					return
				}

				assert.NoError(t, err, "AppendFetch must read the body")
				assert.Equal(t, string(got), prefix+payload, "AppendFetch must keep the bytes of dst before the body")
			})
		}

		t.Run("returns dst with a StatusError for a refused response", func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, body)
			}))
			t.Cleanup(srv.Close)

			c, err := httpclient.New(dependency, required, loopback)
			assert.NoError(t, err, "New must accept the options")

			got, err := appendFetch(t, c, []byte(prefix), http.MethodGet, srv.URL)
			se := assert.ErrorAs[*httpclient.StatusError](t, err, "AppendFetch must return a StatusError")
			expect.Equal(t, string(se.Body), body, "the StatusError must carry the body")
			expect.Equal(t, string(got), prefix, "AppendFetch must return dst unchanged")
		})

		t.Run("returns dst for a response to HEAD", func(t *testing.T) {
			t.Parallel()
			// The server declares the length of the body that a GET returns.
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, body)
			}))
			t.Cleanup(srv.Close)

			c, err := httpclient.New(dependency, required, loopback)
			assert.NoError(t, err, "New must accept the options")

			got, err := appendFetch(t, c, []byte(prefix), http.MethodHead, srv.URL)
			assert.NoError(t, err, "AppendFetch must accept a response to HEAD")
			assert.Equal(t, string(got), prefix, "AppendFetch must append nothing for HEAD")
		})

		t.Run("drops the bytes of an attempt that failed", func(t *testing.T) {
			t.Parallel()
			// The first attempt declares 10 bytes, sends 2 and closes the
			// connection, so its read fails with a Transient error after it
			// wrote 2 bytes into the room of dst.
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if hits.Add(1) == 1 {
					w.Header().Set("Content-Length", "10")
					_, _ = io.WriteString(w, "ab")
					conn, _, err := http.NewResponseController(w).Hijack()
					if err == nil {
						_ = conn.Close()
					}

					return
				}

				_, _ = io.WriteString(w, body)
			}))
			t.Cleanup(srv.Close)

			retry := retrier(t, fake.New(origin))
			c, err := httpclient.New(dependency, required, loopback, httpclient.WithRetrier(retry))
			assert.NoError(t, err, "New must accept the options")

			dst := append(make([]byte, 0, len(prefix)+1024), prefix...)
			got, err := appendFetch(t, c, dst, http.MethodGet, srv.URL)
			assert.NoError(t, err, "AppendFetch must return the body of the retry")
			expect.Equal(t, string(got), prefix+body, "AppendFetch must keep dst and the body of the retry alone")
			expect.Equal(t, hits.Load(), int32(2), "the client must send two attempts")
		})
	})

	t.Run("AppendFetchBody", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the body of a response that succeeded to the bytes of dst", func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, body)
			}))
			t.Cleanup(srv.Close)

			c, err := httpclient.New(dependency, required, loopback)
			assert.NoError(t, err, "New must accept the options")

			got, err := appendFetchBody(t, c, []byte(prefix), http.MethodPost, srv.URL, []byte(requestBody))
			assert.NoError(t, err, "AppendFetchBody must read the body of the response")
			assert.Equal(t, string(got), prefix+body, "AppendFetchBody must keep the bytes of dst before the body")
		})

		t.Run("sends body with its length as the body of the request", func(t *testing.T) {
			t.Parallel()
			received := make(chan string, 1)
			lengths := make(chan int64, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				received <- string(b)
				lengths <- r.ContentLength
			}))
			t.Cleanup(srv.Close)

			c, err := httpclient.New(dependency, required, loopback)
			assert.NoError(t, err, "New must accept the options")

			_, err = appendFetchBody(t, c, nil, http.MethodPost, srv.URL, []byte(requestBody))
			assert.NoError(t, err, "AppendFetchBody must send the request")
			expect.Equal(t, await(t, received, "the server must receive the body"), requestBody,
				"the server must receive body")
			expect.Equal(t, await(t, lengths, "the server must receive the length"), int64(len(requestBody)),
				"the request must declare the length of body")
		})

		redirects := []struct {
			name   string
			status int
		}{
			{name: "sends body again after a 307", status: http.StatusTemporaryRedirect},
			{name: "sends body again after a 308", status: http.StatusPermanentRedirect},
		}
		for _, tt := range redirects {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				received := make(chan string, 1)
				mux := http.NewServeMux()
				mux.HandleFunc("/first", func(w http.ResponseWriter, r *http.Request) {
					http.Redirect(w, r, "/second", tt.status)
				})
				mux.HandleFunc("/second", func(_ http.ResponseWriter, r *http.Request) {
					b, _ := io.ReadAll(r.Body)
					received <- string(b)
				})
				srv := httptest.NewServer(mux)
				t.Cleanup(srv.Close)

				c, err := httpclient.New(dependency, required, loopback)
				assert.NoError(t, err, "New must accept the options")

				_, err = appendFetchBody(t, c, nil, http.MethodPost, srv.URL+"/first", []byte(requestBody))
				assert.NoError(t, err, "AppendFetchBody must follow the redirect")
				assert.Equal(t, await(t, received, "the target of the redirect must receive the request"), requestBody,
					"the target of the redirect must receive body")
			})
		}

		t.Run("sends body on each attempt of a retry", func(t *testing.T) {
			t.Parallel()
			received := make(chan string, attempts)
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				received <- string(b)
				if hits.Add(1) == 1 {
					w.WriteHeader(http.StatusServiceUnavailable)
				}
			}))
			t.Cleanup(srv.Close)

			retry := retrier(t, fake.New(origin))
			c, err := httpclient.New(dependency, required, loopback, httpclient.WithRetrier(retry))
			assert.NoError(t, err, "New must accept the options")

			_, err = appendFetchBody(t, c, nil, http.MethodPut, srv.URL, []byte(requestBody))
			assert.NoError(t, err, "AppendFetchBody must return the body of the retry")
			expect.Equal(t, await(t, received, "the first attempt must arrive"), requestBody,
				"the first attempt must send body")
			expect.Equal(t, await(t, received, "the second attempt must arrive"), requestBody,
				"the second attempt must send body")
		})

		t.Run("sends body over HTTP/2", func(t *testing.T) {
			t.Parallel()
			protocols := make(chan int, 1)
			received := make(chan string, 1)
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				protocols <- r.ProtoMajor
				received <- string(b)
			}))
			srv.EnableHTTP2 = true
			srv.StartTLS()
			t.Cleanup(srv.Close)

			transport, ok := srv.Client().Transport.(*http.Transport)
			assert.True(t, ok, "the client of httptest must have an http.Transport")

			c, err := httpclient.New(dependency, required, loopback, httpclient.WithTLS(transport.TLSClientConfig))
			assert.NoError(t, err, "New must accept the options")

			_, err = appendFetchBody(t, c, nil, http.MethodPost, srv.URL, []byte(requestBody))
			assert.NoError(t, err, "AppendFetchBody must send the request")
			assert.Equal(t, await(t, protocols, "the server must receive the request"), 2,
				"the request must arrive over HTTP/2")
			assert.Equal(t, await(t, received, "the server must receive the body"), requestBody,
				"the server must receive body over HTTP/2")
		})

		t.Run("sends body for a request whose Body is http.NoBody", func(t *testing.T) {
			t.Parallel()
			received := make(chan string, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				received <- string(b)
			}))
			t.Cleanup(srv.Close)

			c, err := httpclient.New(dependency, required, loopback)
			assert.NoError(t, err, "New must accept the options")

			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL, http.NoBody)
			assert.NoError(t, err, "the request must build")

			_, err = c.AppendFetchBody(nil, req, []byte(requestBody))
			assert.NoError(t, err, "AppendFetchBody must send a request whose Body is http.NoBody")
			assert.Equal(t, await(t, received, "the server must receive the body"), requestBody,
				"the server must receive body")
		})

		t.Run("sends a request without a body for an empty body", func(t *testing.T) {
			t.Parallel()
			received := make(chan string, 1)
			lengths := make(chan int64, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				received <- string(b)
				lengths <- r.ContentLength
			}))
			t.Cleanup(srv.Close)

			c, err := httpclient.New(dependency, required, loopback)
			assert.NoError(t, err, "New must accept the options")

			_, err = appendFetchBody(t, c, nil, http.MethodPost, srv.URL, nil)
			assert.NoError(t, err, "AppendFetchBody must send the request")
			expect.Empty(t, await(t, received, "the server must receive the body"), "the request must have no body")
			expect.Equal(t, await(t, lengths, "the server must receive the length"), int64(0),
				"the request must declare a length of 0")
		})

		t.Run("returns ErrRequestBody for a request with a body of its own", func(t *testing.T) {
			t.Parallel()
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
			t.Cleanup(srv.Close)

			c, err := httpclient.New(dependency, required, loopback)
			assert.NoError(t, err, "New must accept the options")

			own := strings.NewReader(requestBody)
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL, own)
			assert.NoError(t, err, "the request must build")

			got, err := c.AppendFetchBody([]byte(prefix), req, []byte(requestBody))
			expect.ErrorIs(t, err, httpclient.ErrRequestBody, "AppendFetchBody must refuse the request")
			expect.Equal(t, errs.Classify(err), errs.Invalid, "ErrRequestBody must classify as Invalid")
			expect.Equal(t, string(got), prefix, "AppendFetchBody must return dst unchanged")
			expect.Equal(t, hits.Load(), int32(0), "the server must receive no request")
		})
	})
}

// get sends a GET of url with c.Do, and returns the status and the body of
// its response, or the error of Do.
func get(t *testing.T, c *httpclient.Client, url string) (int, string, error) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
	assert.NoError(t, err, "the request of the case must build")

	resp, err := c.Do(req)
	if err != nil {
		return 0, "", err
	}

	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	assert.NoError(t, err, "the body of the response must read")

	return resp.StatusCode, string(b), nil
}

// fetch sends a request of method to url with body and header through
// c.Fetch, and returns what Fetch returns.
func fetch(t *testing.T, c *httpclient.Client, method, url string, body io.Reader, header http.Header) ([]byte, error) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, url, body)
	assert.NoError(t, err, "the request of the case must build")
	maps.Copy(req.Header, header)

	return c.Fetch(req)
}

// appendFetch sends a request of method to url without a body through
// c.AppendFetch with dst, and returns what AppendFetch returns.
func appendFetch(t *testing.T, c *httpclient.Client, dst []byte, method, url string) ([]byte, error) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, url, http.NoBody)
	assert.NoError(t, err, "the request of the case must build")

	return c.AppendFetch(dst, req)
}

// appendFetchBody sends a request of method to url without a body of its
// own through c.AppendFetchBody with dst and sent, and returns what
// AppendFetchBody returns.
func appendFetchBody(t *testing.T, c *httpclient.Client, dst []byte, method, url string, sent []byte) ([]byte, error) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, url, nil)
	assert.NoError(t, err, "the request of the case must build")

	return c.AppendFetchBody(dst, req, sent)
}

// port returns the port of the URL of a test server, and fails t when the
// URL has none.
func port(t *testing.T, rawURL string) int {
	t.Helper()

	u, err := url.Parse(rawURL)
	assert.NoError(t, err, "the URL of the server must parse")

	n, err := strconv.Atoi(u.Port())
	assert.NoError(t, err, "the port of the server must parse")

	return n
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

// redirector returns a handler that redirects /redirect/n to /redirect/n-1,
// and responds with 200 and body to /redirect/0 and to an n that is not a
// number.
func redirector() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/redirect/{n}", func(w http.ResponseWriter, r *http.Request) {
		n, err := strconv.Atoi(r.PathValue("n"))
		if err != nil || n == 0 {
			_, _ = io.WriteString(w, body)

			return
		}

		http.Redirect(w, r, "/redirect/"+strconv.Itoa(n-1), http.StatusFound)
	})

	return mux
}
