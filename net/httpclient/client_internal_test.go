// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package httpclient

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"go.dokimi.dev/assert/bench"
	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/coretest/telemetrytest"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/net/internal/semconv"
	"go.thesmos.sh/core/rand/pcg"
	"go.thesmos.sh/core/resilience"
	"go.thesmos.sh/core/telemetry"
	"go.thesmos.sh/core/telemetry/noop"
	"go.thesmos.sh/core/telemetry/w3c"
)

// The fixture values of the internal cases.
const (
	// dependency is the name of the clients of the internal cases.
	dependency = "registry"

	// payload is the body of the response of respond.
	payload = "ok"
)

// origin is the time of the fake clocks of the internal cases.
var origin = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

// The bytes that respond reads and writes.
var (
	// endOfHeader ends the header of a request.
	endOfHeader = []byte("\r\n\r\n")

	// response is the response of respond to every request.
	response = []byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\n" + payload)
)

// required bundles the four options of the dependencies that New requires,
// and admits example.test, the host of the internal cases.
var required = Options(
	WithClock(fake.New(origin)),
	WithLogger(slog.New(slog.DiscardHandler)),
	WithReporter(noop.Reporter{}),
	WithPropagator(w3c.Propagator{}),
	WithHosts("example.test"),
)

// roundTrip is an http.RoundTripper of a function. An internal case puts
// one in place of the transport of a client, so no request goes to a
// network.
type roundTrip func(*http.Request) (*http.Response, error)

var _ http.RoundTripper = roundTrip(nil)

// RoundTrip returns the response of f for r.
func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// traced is the noop reporter with a tracer whose spans have the identity
// of a sampled trace and record nothing, so a benchmark counts the work of
// the client for a traced attempt and none of a tracer.
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

func TestClientInternal(t *testing.T) {
	t.Parallel()

	t.Run("New", func(t *testing.T) {
		t.Parallel()

		t.Run("sets the documented default of every limit of net/http's client and transport", func(t *testing.T) {
			t.Parallel()

			c, err := New(dependency, required)
			testkit.NoError(t, err, "New must accept the required options")
			testkit.Equal(t, c.http.Timeout, 10*time.Second, "the timeout of an attempt")
			testkit.Equal(t, c.maxResponse, int64(8<<20), "the response limit")

			tr, ok := c.http.Transport.(*http.Transport)
			testkit.True(t, ok, "the client must send through a transport of net/http")
			testkit.Equal(t, tr.TLSHandshakeTimeout, 5*time.Second, "the TLS handshake timeout")
			testkit.Equal(t, tr.IdleConnTimeout, 90*time.Second, "the idle connection timeout")
			testkit.Equal(t, tr.MaxIdleConnsPerHost, 32, "the idle connections per host")
			testkit.Equal(t, tr.ExpectContinueTimeout, time.Second, "the wait for 100 Continue")
			testkit.Equal(t, tr.HTTP2.SendPingTimeout, 30*time.Second, "the idle time before an HTTP/2 ping")
			testkit.Equal(t, tr.HTTP2.PingTimeout, 15*time.Second, "the wait for the response to an HTTP/2 ping")
			testkit.True(t, tr.Proxy == nil, "the transport must ignore the proxy of the environment")

			p := tr.Protocols
			testkit.True(t, p.HTTP1() && p.HTTP2() && !p.UnencryptedHTTP2(), "the protocols of the transport")
		})

		t.Run("turns a limit off for a negative value", func(t *testing.T) {
			t.Parallel()

			c, err := New(dependency, required,
				WithTimeout(-1), WithTLSHandshakeTimeout(-1), WithIdleConnTimeout(-1), WithMaxResponseBytes(-7))
			testkit.NoError(t, err, "New must accept negative limits")
			testkit.Equal(t, c.http.Timeout, time.Duration(0), "net/http's client has no timeout for zero")
			testkit.Equal(t, c.maxResponse, int64(unbounded), "the response limit")

			tr, ok := c.http.Transport.(*http.Transport)
			testkit.True(t, ok, "the client must send through a transport of net/http")
			testkit.Equal(t, tr.TLSHandshakeTimeout, time.Duration(0), "net/http has no handshake timeout for zero")
			testkit.Equal(t, tr.IdleConnTimeout, time.Duration(0), "net/http has no idle timeout for zero")
		})
	})

	t.Run("Do", func(t *testing.T) {
		t.Parallel()

		ports := []struct {
			name string
			url  string
			want int64
		}{
			{name: "records the port 443 for an https URL without a port", url: "https://example.test/", want: 443},
			{name: "records the port of a URL with a port", url: "https://example.test:8443/", want: 8443},
		}
		for _, tt := range ports {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				histogram := telemetrytest.NewHistogramStub(t)
				histogram.OnWith.Returns(noop.Reporter{}.Histogram(telemetry.InstrumentSpec{}))

				reporter := telemetrytest.NewReporterStub(t)
				reporter.OnTracer.Returns(noop.Reporter{}.Tracer(scope))
				reporter.OnHistogram.Returns(histogram)

				c, err := New(dependency, required, WithReporter(reporter))
				testkit.NoError(t, err, "New must accept the options")

				c.http.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody, Request: r}, nil
				})

				req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, tt.url, http.NoBody)
				testkit.NoError(t, err, "the request must build")

				resp, err := c.Do(req)
				testkit.NoError(t, err, "Do")
				testkit.NoError(t, resp.Body.Close(), "the body must close")

				with := histogram.OnWith.AssertCalledOnce(t, "the client must bind one set")
				testkit.Equal(t, with.Attrs[2], telemetry.AttrInt(semconv.ServerPort, tt.want), "the port of the set")
			})
		}
	})
}

func BenchmarkClient(b *testing.B) {
	req, err := http.NewRequestWithContext(b.Context(), http.MethodGet, "http://example.test/", http.NoBody)
	testkit.NoError(b, err, "the request must build")

	retrier, err := resilience.NewRetrier(resilience.RetryConfig{
		Clock:         fake.New(origin),
		Rand:          pcg.New(1),
		Attempts:      3,
		Base:          time.Millisecond,
		Max:           time.Second,
		MaxRetryAfter: time.Minute,
		MinRetries:    10,
		BudgetWindow:  time.Minute,
	})
	testkit.NoError(b, err, "the retrier must build")

	breaker, err := resilience.NewBreaker(resilience.BreakerConfig{
		Clock:            fake.New(origin),
		TripOn:           []errs.Class{errs.Transient},
		FailureThreshold: 5,
		SuccessThreshold: 1,
		OpenFor:          time.Second,
	})
	testkit.NoError(b, err, "the breaker must build")

	dos := []struct {
		opts []Option
		name string
		want uint64
	}{
		{name: "Do", want: 54},
		{name: "Do of a traced request", opts: []Option{WithReporter(traced{})}, want: 60},
		{name: "Do with a breaker and a retrier", opts: []Option{WithBreaker(breaker), WithRetrier(retrier)}, want: 54},
	}
	for _, tt := range dos {
		b.Run(tt.name, func(b *testing.B) {
			client := piped(b, tt.opts...)

			c := bench.Start(b).MaxAllocs(tt.want)
			defer c.End()

			var status int
			for c.Loop() {
				got, errDo := client.Do(req)
				if errDo != nil {
					b.Fatalf("Do: %v", errDo)
				}

				// A body read to its end returns the connection for the
				// next call.
				status = got.StatusCode
				_, _ = io.Copy(io.Discard, got.Body)
				_ = got.Body.Close()
			}

			testkit.Equal(b, status, http.StatusOK, "the benchmark must measure a response that succeeded")
		})
	}

	b.Run("Fetch", func(b *testing.B) {
		client := piped(b)

		c := bench.Start(b).MaxAllocs(55)
		defer c.End()

		var (
			got      []byte
			errFetch error
		)

		for c.Loop() {
			got, errFetch = client.Fetch(req)
		}

		testkit.NoError(b, errFetch, "Fetch")
		testkit.Equal(b, string(got), payload, "the benchmark must measure a body that the client read")
	})
}

// piped returns a Client with the options of the internal cases and opts,
// whose transport dials a pipe to respond. Its calls run through net/http's
// Client and Transport on one connection that they reuse, and connect to no
// network. The cleanup of b closes the connection.
func piped(b *testing.B, opts ...Option) *Client {
	b.Helper()

	client, err := New(dependency, append([]Option{required}, opts...)...)
	testkit.NoError(b, err, "New must accept the options")

	tr, ok := client.http.Transport.(*http.Transport)
	testkit.True(b, ok, "the client must send through a transport of net/http")
	tr.DialContext = func(context.Context, string, string) (net.Conn, error) {
		conn, peer := net.Pipe()
		go respond(peer)

		return conn, nil
	}
	b.Cleanup(tr.CloseIdleConnections)

	return client
}

// respond writes response to conn for every request that it reads from
// conn, until conn closes. The requests of the benchmark have no body, so
// each ends with its header, and fits one read of the buffer. respond
// does not allocate per request.
func respond(conn net.Conn) {
	buf := make([]byte, 4096)

	for {
		n, err := conn.Read(buf)
		if err != nil {
			return
		}

		if bytes.Contains(buf[:n], endOfHeader) {
			if _, err := conn.Write(response); err != nil {
				return
			}
		}
	}
}
