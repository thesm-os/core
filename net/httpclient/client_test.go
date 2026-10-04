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

	"go.thesmos.sh/testkit"

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

	// patience bounds every wait of a case for a value of a handler or of a
	// call, above any correct wait, so a case fails where a defect would
	// leave it waiting.
	patience = 5 * time.Second

	// traceID and spanID are the identity of every span that the reporter
	// of a case starts.
	traceID telemetry.TraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	spanID  telemetry.SpanID  = "00f067aa0ba902b7"
)

// The message and the keys of the log record of a call that failed, which
// the cases pin.
const (
	messageFailed = "call failed"
	keyDependency = "dependency"
	keyError      = "error"
)

// origin is the time of the fake clocks of the cases.
var origin = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

// The errors of the test doubles.
var (
	errBoom = errors.New("boom")
	errDNS  = errors.New("no resolution")
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
			testkit.ErrorIs(t, err, httpclient.ErrConfig, "New must refuse an empty name")
			testkit.Equal(t, errs.Classify(err), errs.Invalid, "the class of the error")
		})

		t.Run("returns ErrConfig without the options that it requires", func(t *testing.T) {
			t.Parallel()

			_, err := httpclient.New(dependency)
			testkit.ErrorIs(t, err, httpclient.ErrConfig, "New must refuse a Client without a clock")
			testkit.Contains(t, err.Error(), "WithClock", "the error must name the missing option")
		})

		t.Run("returns ErrConfig without a host", func(t *testing.T) {
			t.Parallel()

			_, err := httpclient.New(dependency, required)
			testkit.ErrorIs(t, err, httpclient.ErrConfig, "New must refuse a Client without hosts")
			testkit.Contains(t, err.Error(), "WithHosts", "the error must name the missing option")
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
		}
		for _, tt := range refused {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				_, err := httpclient.New(dependency, required, loopback, tt.give)
				testkit.ErrorIs(t, err, httpclient.ErrConfig, "New must refuse the option")
				testkit.Equal(t, errs.Classify(err), errs.Invalid, "the class of the error")
			})
		}

		t.Run("accepts every form of host", func(t *testing.T) {
			t.Parallel()

			_, err := httpclient.New(
				dependency,
				required,
				httpclient.WithHosts(
					"example.com",
					".example.com",
					"10.0.0.1",
					"::1",
					"_service.internal",
					"Example.COM",
				),
			)
			testkit.NoError(t, err, "New must accept the hosts")
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
			testkit.NoError(t, err, "New must accept the options")

			status, got, err := get(t, c, srv.URL)
			testkit.NoError(t, err, "Do")
			testkit.Equal(t, status, http.StatusOK, "the status")
			testkit.Equal(t, got, body, "the body")
		})

		t.Run("returns the response of a status that the classification refuses without an error", func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			t.Cleanup(srv.Close)

			l := &logs{}
			c, err := httpclient.New(dependency, required, loopback, httpclient.WithLogger(slog.New(l)))
			testkit.NoError(t, err, "New must accept the options")

			status, _, err := get(t, c, srv.URL)
			testkit.NoError(t, err, "Do must return the response")
			testkit.Equal(t, status, http.StatusServiceUnavailable, "the status")

			records := l.all()
			testkit.Len(t, records, 1, "Do must log the failed call")
			testkit.Equal(t, records[0].Level, slog.LevelWarn, "the level of the record")
			testkit.Equal(t, records[0].Message, messageFailed, "the message of the record")
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
				testkit.NoError(t, err, "New must accept the options")

				_, _, err = get(t, c, tt.url)
				testkit.ErrorIs(t, err, httpclient.ErrBlocked, "Do must refuse the request")
				testkit.Equal(t, errs.Classify(err), errs.Denied, "the class of the error")
				testkit.Len(t, l.all(), 1, "Do must log the refused call")
			})
		}

		t.Run("returns ErrBlocked for a request without a URL", func(t *testing.T) {
			t.Parallel()

			c, err := httpclient.New(dependency, required, loopback)
			testkit.NoError(t, err, "New must accept the options")

			req := (&http.Request{Method: http.MethodGet}).WithContext(t.Context())

			_, err = c.Do(req) //nolint:bodyclose // Do returns no response with the error
			testkit.ErrorIs(t, err, httpclient.ErrBlocked, "Do must refuse a request without a URL")
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
				testkit.NoError(t, err, "New must accept the options")

				_, _, err = get(t, c, tt.url)
				testkit.ErrorIsNot(t, err, httpclient.ErrBlocked, "Do must admit the host")

				dnsErr := testkit.ErrorAs[*net.DNSError](t, err, "the call must query the resolver")
				testkit.Equal(t, dnsErr.Err, errDNS.Error(), "the error of the resolver")
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
				testkit.NoError(t, err, "New must accept the options")

				_, _, err = get(t, c, tt.url)
				testkit.ErrorIs(t, err, httpclient.ErrBlocked, "Do must refuse the host")
			})
		}

		t.Run("returns a Transient error for a failure of the transport", func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			srv.Close()

			c, err := httpclient.New(dependency, required, loopback)
			testkit.NoError(t, err, "New must accept the options")

			_, _, err = get(t, c, srv.URL)
			testkit.Error(t, err, "Do must fail without a server")
			testkit.Equal(t, errs.Classify(err), errs.Transient, "the class of the error")
			testkit.Contains(t, err.Error(), "httpclient: "+dependency, "the error must name the dependency")
		})

		t.Run("returns a Transient error for an attempt beyond the timeout", func(t *testing.T) {
			t.Parallel()

			release := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
			t.Cleanup(srv.Close)
			t.Cleanup(func() { close(release) })

			c, err := httpclient.New(dependency, required, loopback, httpclient.WithTimeout(50*time.Millisecond))
			testkit.NoError(t, err, "New must accept the options")

			_, _, err = get(t, c, srv.URL)
			testkit.ErrorIs(t, err, context.DeadlineExceeded, "Do must end at the timeout")
			testkit.Equal(t, errs.Classify(err), errs.Transient, "the class of the error")
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
			testkit.NoError(t, err, "New must accept the options")

			ctx, cancel := context.WithCancel(t.Context())
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, http.NoBody)
			testkit.NoError(t, err, "the request must build")

			go func() {
				<-entered
				cancel()
			}()

			_, err = c.Do(req) //nolint:bodyclose // the call fails without a response
			testkit.ErrorIs(t, err, context.Canceled, "Do must return the error of the context")
			testkit.Equal(t, errs.Classify(err), errs.Unspecified, "the error of a context has no class")
			testkit.Len(t, l.all(), 0, "Do must not log a call that its caller cancelled")
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
			testkit.NoError(t, err, "New must accept the options")

			_, _, err = get(t, c, srv.URL)
			testkit.NoError(t, err, "Do")
			testkit.Equal(t, await(t, parents, "the handler must run"), "00-"+string(traceID)+"-"+string(spanID)+"-00",
				"the traceparent")

			spans := r.started()
			testkit.Len(t, spans, 1, "the client must start a span per attempt")
			testkit.Equal(
				t,
				telemetry.ApplySpanOptions(spans[0].opts),
				telemetry.SpanKindClient,
				"the kind of the span",
			)
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
			testkit.NoError(t, err, "New must accept the options")

			_, _, err = get(t, c, srv.URL)
			testkit.NoError(t, err, "Do")

			u, err := url.Parse(srv.URL)
			testkit.NoError(t, err, "the URL of the server must parse")
			port, err := strconv.Atoi(u.Port())
			testkit.NoError(t, err, "the port of the server must parse")

			sets := r.bound()
			testkit.Len(t, sets, 1, "the client must bind one set")
			testkit.Equal(t, sets[0].attrs, []telemetry.Attr{
				telemetry.AttrString(semconv.RequestMethod, http.MethodGet),
				telemetry.AttrString(semconv.ServerAddress, "127.0.0.1"),
				telemetry.AttrInt(semconv.ServerPort, int64(port)),
				telemetry.AttrInt(semconv.ResponseStatusCode, http.StatusNotFound),
				telemetry.AttrString(semconv.ErrorType, "NotFound"),
			}, "the attributes of the set")
			sets[0].OnRecord.AssertCalledOnce(t, "the set must record the attempt")

			attrs := r.started()[0].OnSetAttributes.AssertCalledOnce(t, "the span must receive the attributes").Attrs
			testkit.Equal(t, attrs, sets[0].attrs, "the attributes of the span")
		})

		t.Run("calls the function of WithPrepare on the request of each attempt", func(t *testing.T) {
			t.Parallel()

			tokens := make(chan string, 2)
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				tokens <- r.Header.Get("Authorization")
				if hits.Add(1) == 1 {
					w.WriteHeader(http.StatusServiceUnavailable)
				}
			}))
			t.Cleanup(srv.Close)

			var issued atomic.Int32
			c, err := httpclient.New(
				dependency,
				required,
				loopback,
				httpclient.WithRetrier(retrier(t, fake.New(origin))),
				httpclient.WithPrepare(func(r *http.Request) error {
					r.Header.Set("Authorization", "Bearer "+strconv.Itoa(int(issued.Add(1))))

					return nil
				}),
			)
			testkit.NoError(t, err, "New must accept the options")

			status, _, err := get(t, c, srv.URL)
			testkit.NoError(t, err, "Do")
			testkit.Equal(t, status, http.StatusOK, "the status of the retried request")
			testkit.Equal(
				t,
				await(t, tokens, "the first attempt must arrive"),
				"Bearer 1",
				"the token of the first attempt",
			)
			testkit.Equal(t, await(t, tokens, "the second attempt must arrive"), "Bearer 2",
				"the token of the second attempt")
		})

		t.Run("returns the error of the function of WithPrepare without a request", func(t *testing.T) {
			t.Parallel()

			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
			t.Cleanup(srv.Close)

			c, err := httpclient.New(dependency, required, loopback,
				httpclient.WithPrepare(func(*http.Request) error { return errBoom }))
			testkit.NoError(t, err, "New must accept the options")

			_, _, err = get(t, c, srv.URL)
			testkit.ErrorIs(t, err, errBoom, "Do must return the error of prepare")
			testkit.Equal(t, hits.Load(), int32(0), "the server must receive no request")
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
			testkit.NoError(t, err, "New must accept the options")

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, http.NoBody)
			testkit.NoError(t, err, "the request must build")

			resp, err := c.Do(req)
			testkit.NoError(t, err, "Do")
			testkit.NoError(t, resp.Body.Close(), "the body must close")
			testkit.Equal(t, req.Header, http.Header{}, "the headers of the caller's request")
		})

		t.Run("sends a request without headers", func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			t.Cleanup(srv.Close)

			c, err := httpclient.New(dependency, required, loopback, httpclient.WithReporter(newReporter(t)))
			testkit.NoError(t, err, "New must accept the options")

			u, err := url.Parse(srv.URL)
			testkit.NoError(t, err, "the URL of the server must parse")

			resp, err := c.Do((&http.Request{Method: http.MethodGet, URL: u}).WithContext(t.Context()))
			testkit.NoError(t, err, "Do must send a request without a header map")
			testkit.NoError(t, resp.Body.Close(), "the body must close")
		})

		t.Run("follows a redirect to an admitted host", func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(redirector())
			t.Cleanup(srv.Close)

			c, err := httpclient.New(dependency, required, loopback)
			testkit.NoError(t, err, "New must accept the options")

			status, got, err := get(t, c, srv.URL+"/redirect/1")
			testkit.NoError(t, err, "Do")
			testkit.Equal(t, status, http.StatusOK, "the status after the redirect")
			testkit.Equal(t, got, body, "the body after the redirect")
		})

		t.Run("follows 5 redirects", func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(redirector())
			t.Cleanup(srv.Close)

			c, err := httpclient.New(dependency, required, loopback)
			testkit.NoError(t, err, "New must accept the options")

			status, _, err := get(t, c, srv.URL+"/redirect/5")
			testkit.NoError(t, err, "Do")
			testkit.Equal(t, status, http.StatusOK, "the status after 5 redirects")
		})

		t.Run("returns the sixth redirect response as it is", func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(redirector())
			t.Cleanup(srv.Close)

			c, err := httpclient.New(dependency, required, loopback)
			testkit.NoError(t, err, "New must accept the options")

			status, _, err := get(t, c, srv.URL+"/redirect/6")
			testkit.NoError(t, err, "Do must return the redirect response")
			testkit.Equal(t, status, http.StatusFound, "the status of the sixth redirect")
		})

		t.Run("returns ErrBlocked for a redirect to a host that WithHosts does not admit", func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.RedirectHandler("http://127.0.0.2/", http.StatusFound))
			t.Cleanup(srv.Close)

			c, err := httpclient.New(dependency, required, loopback)
			testkit.NoError(t, err, "New must accept the options")

			_, _, err = get(t, c, srv.URL)
			testkit.ErrorIs(t, err, httpclient.ErrBlocked, "Do must refuse the redirect")
		})

		t.Run("returns ErrBlocked for a redirect from https to http", func(t *testing.T) {
			t.Parallel()

			plain := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			t.Cleanup(plain.Close)

			secure := httptest.NewTLSServer(http.RedirectHandler(plain.URL, http.StatusFound))
			t.Cleanup(secure.Close)

			cfg := secure.Client().Transport.(*http.Transport).TLSClientConfig
			c, err := httpclient.New(dependency, required, loopback, httpclient.WithTLS(cfg))
			testkit.NoError(t, err, "New must accept the options")

			_, _, err = get(t, c, secure.URL)
			testkit.ErrorIs(t, err, httpclient.ErrBlocked, "Do must refuse the downgrade")
		})

		t.Run("uses the classification of WithClassify", func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			t.Cleanup(srv.Close)

			r := newReporter(t)
			c, err := httpclient.New(dependency, required, loopback, httpclient.WithReporter(r),
				httpclient.WithClassify(func(*http.Response) error { return errs.WithClass(errBoom, errs.Conflict) }))
			testkit.NoError(t, err, "New must accept the options")

			status, _, err := get(t, c, srv.URL)
			testkit.NoError(t, err, "Do returns the response that the classification refuses")
			testkit.Equal(t, status, http.StatusOK, "the status")

			end := r.started()[0].OnEnd.AssertCalledOnce(t, "the span must end once")
			testkit.ErrorIs(t, end.Err, errBoom, "the span must end with the error of the classification")
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
			testkit.NoError(t, err, "New must accept the options")

			got, err := fetch(t, c, http.MethodGet, srv.URL, http.NoBody, nil)
			testkit.NoError(t, err, "Fetch")
			testkit.Equal(t, string(got), body, "the body")
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
			testkit.NoError(t, err, "New must accept the options")

			_, err = fetch(t, c, http.MethodGet, srv.URL, http.NoBody, nil)
			se := testkit.ErrorAs[*httpclient.StatusError](t, err, "Fetch must return a StatusError")
			testkit.Equal(t, se.Status, http.StatusNotFound, "the status")
			testkit.Equal(t, se.Dependency, dependency, "the dependency")
			testkit.Equal(t, string(se.Body), long[:1024], "the first 1 KiB of the body")
			testkit.Equal(t, errs.Classify(err), errs.NotFound, "the class of the error")
		})

		t.Run("returns a StatusError for a status of 300", func(t *testing.T) {
			t.Parallel()

			// net/http follows no redirect for 300.
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusMultipleChoices)
			}))
			t.Cleanup(srv.Close)

			c, err := httpclient.New(dependency, required, loopback)
			testkit.NoError(t, err, "New must accept the options")

			_, err = fetch(t, c, http.MethodGet, srv.URL, http.NoBody, nil)
			se := testkit.ErrorAs[*httpclient.StatusError](t, err, "Fetch must refuse a status of 300")
			testkit.Equal(t, se.Status, http.StatusMultipleChoices, "the status")
		})

		t.Run("returns nil for a response to HEAD", func(t *testing.T) {
			t.Parallel()

			// The server declares the length of the body that a GET returns.
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, body)
			}))
			t.Cleanup(srv.Close)

			c, err := httpclient.New(dependency, required, loopback)
			testkit.NoError(t, err, "New must accept the options")

			got, err := fetch(t, c, http.MethodHead, srv.URL, http.NoBody, nil)
			testkit.NoError(t, err, "Fetch")
			testkit.True(t, got == nil, "the body of a HEAD must be nil")
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
			testkit.NoError(t, err, "New must accept the options")

			_, err = fetch(t, c, http.MethodGet, srv.URL, http.NoBody, nil)
			testkit.Error(t, err, "Fetch must fail on a body that ends early")
			testkit.Equal(t, errs.Classify(err), errs.Transient, "the class of the error")
		})

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
				testkit.NoError(t, err, "New must accept the options")

				got, err := fetch(t, c, http.MethodGet, srv.URL, http.NoBody, nil)
				if tt.fails {
					testkit.ErrorIs(t, err, httpclient.ErrTooLarge, "Fetch must refuse the body")
					testkit.Equal(t, errs.Classify(err), errs.Invalid, "the class of the error")

					return
				}

				testkit.NoError(t, err, "Fetch")
				testkit.Equal(t, string(got), payload, "the body")
			})
		}
	})
}

// get sends a GET of url with c.Do, and returns the status and the body of
// its response, or the error of Do.
func get(t *testing.T, c *httpclient.Client, url string) (int, string, error) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
	testkit.NoError(t, err, "the request of the case must build")

	resp, err := c.Do(req)
	if err != nil {
		return 0, "", err
	}

	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	testkit.NoError(t, err, "the body of the response must read")

	return resp.StatusCode, string(b), nil
}

// fetch sends a request of method to url with body and header through
// c.Fetch, and returns what Fetch returns.
func fetch(t *testing.T, c *httpclient.Client, method, url string, body io.Reader, header http.Header) ([]byte, error) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, url, body)
	testkit.NoError(t, err, "the request of the case must build")
	maps.Copy(req.Header, header)

	return c.Fetch(req)
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
