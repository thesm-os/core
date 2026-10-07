// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package httpclient

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"

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

	// largeBody is the length of the body of large.
	largeBody = 8192

	// pipeWarmup is the number of calls that a benchmark over the pipe of
	// piped makes before it measures. The first dials the connection. The
	// calls fill the runtime's per-processor caches of the sudogs on which
	// the goroutines of the pipe block. The caches start empty, and the
	// first 200 calls of Do at 4 CPUs allocate about 165 sudogs, one more
	// allocation per call in their rounded mean.
	pipeWarmup = 1000
)

// origin is the time of the fake clocks of the internal cases.
var origin = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

// errRefused is the error of a classification of an internal case.
var errRefused = errors.New("the classification refused the response")

// The bytes that respond reads and writes.
var (
	// endOfHeader ends the header of a request.
	endOfHeader = []byte("\r\n\r\n")

	// declared is a response that declares the length of its body.
	declared = []byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\n" + payload)

	// large is a response that declares the length of a body of 8 KiB.
	large = []byte("HTTP/1.1 200 OK\r\nContent-Length: 8192\r\n\r\n" + strings.Repeat("a", largeBody))

	// chunked is a response of unknown length, with its body in one chunk.
	chunked = []byte("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n2\r\n" + payload + "\r\n0\r\n\r\n")
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

// fetches are the responses of the allocation ceilings of Fetch. Under a
// limit, Fetch allocates a declared body once, whatever its length.
// net/http allocates 1 object more for the Content-Length of 8 KiB: the
// value of the header, which has more than one digit, where it returns a
// static string for the one digit of 2.
var fetches = []struct {
	name  string
	reply []byte
	want  int
	limit uint64
}{
	{name: "of a declared body of 2 bytes", reply: declared, want: len(payload), limit: 55},
	{name: "of a declared body of 8 KiB", reply: large, want: largeBody, limit: 56},
}

// bodies are the responses of the allocation ceilings of AppendFetch.
// net/http allocates 3 objects more for a chunked response: the key and the
// value of its Transfer-Encoding header, and the TransferEncoding of the
// Response.
var bodies = []struct {
	name  string
	reply []byte
	want  uint64
}{
	{name: "of a declared body into a buffer with room", reply: declared, want: 54},
	{name: "of a body of unknown length into a buffer with room", reply: chunked, want: 57},
}

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

// tracked is a response body over a reader that counts the bytes read from
// it and records its Close. Its methods are safe for concurrent use.
type tracked struct {
	r      io.Reader
	read   atomic.Int64
	closed atomic.Bool
}

// Read reads from the reader of b, and counts the bytes that it read.
func (b *tracked) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	b.read.Add(int64(n))

	return n, err //nolint:wrapcheck // the reader's own error is the end of the body
}

// Close records the call, and returns nil.
func (b *tracked) Close() error {
	b.closed.Store(true)

	return nil
}

// doConfig is a configuration of the allocation ceilings of Do: the options
// of the client and the ceiling of a call.
type doConfig struct {
	name string
	opts []Option
	want uint64
}

func TestClientInternal(t *testing.T) {
	t.Parallel()

	t.Run("New", func(t *testing.T) {
		t.Parallel()

		t.Run("sets the documented default of every limit", func(t *testing.T) {
			t.Parallel()
			c, err := New(dependency, required)
			assert.NoError(t, err, "New must accept the required options")
			expect.Equal(t, c.http.Timeout, 10*time.Second, "the timeout of an attempt must be 10 s")
			expect.Equal(t, c.maxResponse, int64(8<<20), "the response limit must be 8 MiB")

			tr, ok := c.http.Transport.(*http.Transport)
			assert.True(t, ok, "the client must send through a transport of net/http")
			expect.Equal(t, tr.TLSHandshakeTimeout, 5*time.Second, "the TLS handshake timeout must be 5 s")
			expect.Equal(t, tr.IdleConnTimeout, 90*time.Second, "the idle connection timeout must be 90 s")
			expect.Equal(t, tr.MaxIdleConnsPerHost, 32, "the pool must keep 32 idle connections per host")
			expect.Equal(t, tr.ExpectContinueTimeout, time.Second, "the wait for 100 Continue must be 1 s")
			expect.Equal(t, tr.HTTP2.SendPingTimeout, 30*time.Second, "an idle HTTP/2 connection must ping after 30 s")
			expect.Equal(t, tr.HTTP2.PingTimeout, 15*time.Second, "the wait for the response to a ping must be 15 s")
			expect.Nil(t, tr.Proxy, "the transport must ignore the proxy of the environment")
			expect.True(t, tr.Protocols.HTTP1(), "the transport must speak HTTP/1.1")
			expect.True(t, tr.Protocols.HTTP2(), "the transport must speak HTTP/2 over TLS")
			expect.False(t, tr.Protocols.UnencryptedHTTP2(), "the transport must not speak HTTP/2 without TLS")
		})

		t.Run("applies every limit that an option sets", func(t *testing.T) {
			t.Parallel()
			c, err := New(dependency, required, WithTimeout(3*time.Second), WithTLSHandshakeTimeout(4*time.Second),
				WithIdleConnTimeout(6*time.Second), WithMaxIdleConnsPerHost(7), WithMaxResponseBytes(1024))
			assert.NoError(t, err, "New must accept the limits")
			expect.Equal(t, c.http.Timeout, 3*time.Second, "WithTimeout must set the timeout of an attempt")
			expect.Equal(t, c.maxResponse, int64(1024), "WithMaxResponseBytes must set the response limit")

			tr, ok := c.http.Transport.(*http.Transport)
			assert.True(t, ok, "the client must send through a transport of net/http")
			expect.Equal(t, tr.TLSHandshakeTimeout, 4*time.Second, "WithTLSHandshakeTimeout must set its timeout")
			expect.Equal(t, tr.IdleConnTimeout, 6*time.Second, "WithIdleConnTimeout must set its timeout")
			expect.Equal(t, tr.MaxIdleConnsPerHost, 7, "WithMaxIdleConnsPerHost must set the pool")
		})

		t.Run("turns a limit off for a negative value", func(t *testing.T) {
			t.Parallel()
			c, err := New(dependency, required,
				WithTimeout(-1), WithTLSHandshakeTimeout(-1), WithIdleConnTimeout(-1), WithMaxResponseBytes(-7))
			assert.NoError(t, err, "New must accept negative limits")
			expect.Equal(t, c.http.Timeout, time.Duration(0), "the client of net/http has no timeout for zero")
			expect.Equal(t, c.maxResponse, int64(unbounded), "the response limit must be off")

			tr, ok := c.http.Transport.(*http.Transport)
			assert.True(t, ok, "the client must send through a transport of net/http")
			expect.Equal(t, tr.TLSHandshakeTimeout, time.Duration(0), "net/http has no handshake timeout for zero")
			expect.Equal(t, tr.IdleConnTimeout, time.Duration(0), "net/http has no idle timeout for zero")
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
				assert.NoError(t, err, "New must accept the options")

				c.http.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody, Request: r}, nil
				})

				req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, tt.url, http.NoBody)
				assert.NoError(t, err, "the request must build")

				resp, err := c.Do(req)
				assert.NoError(t, err, "Do must return the response")
				assert.NoError(t, resp.Body.Close(), "the body must close")

				with := histogram.OnWith.AssertCalledOnce(t, "the client must bind one set")
				assert.Length(t, with.Attrs, 4, "the set must have the method, the address, the port and the status")
				assert.Equal(t, with.Attrs[2], telemetry.AttrInt(semconv.ServerPort, tt.want),
					"the set must record the port of the URL")
			})
		}

		t.Run("discards the response of a call whose context ended", func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			c, err := New(dependency, required, WithClassify(func(*http.Response) error {
				cancel()

				return errRefused
			}))
			assert.NoError(t, err, "New must accept the options")

			b := &tracked{r: strings.NewReader(payload)}
			c.http.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: b, Request: r}, nil
			})

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.test/", http.NoBody)
			assert.NoError(t, err, "the request must build")

			resp, err := c.Do(req) //nolint:bodyclose // Do returns no response with the error
			expect.ErrorIs(t, err, errRefused, "Do must return the error of the classification")
			expect.Nil(t, resp, "Do must return no response once the context ended")
			expect.True(t, b.closed.Load(), "Do must close the body of the response that it does not return")
		})
	})

	t.Run("AppendFetch", func(t *testing.T) {
		t.Parallel()

		t.Run("closes the body of a refused response", func(t *testing.T) {
			t.Parallel()
			c, err := New(dependency, required)
			assert.NoError(t, err, "New must accept the options")

			b := &tracked{r: strings.NewReader(payload)}
			c.http.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusNotFound, Body: b, Header: http.Header{}, Request: r}, nil
			})

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.test/", http.NoBody)
			assert.NoError(t, err, "the request must build")

			_, err = c.AppendFetch(nil, req)
			_ = assert.ErrorAs[*StatusError](t, err, "AppendFetch must refuse a status of 404")
			assert.True(t, b.closed.Load(), "AppendFetch must close the body of the refused response")
		})

		t.Run("reads a body without a limit in steps whatever length it declares", func(t *testing.T) {
			t.Parallel()
			c, err := New(dependency, required, WithMaxResponseBytes(-1))
			assert.NoError(t, err, "New must accept the options")

			// A declared length that no memory holds fails at once when the
			// read allocates it in advance.
			c.http.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode:    http.StatusOK,
					ContentLength: 1 << 62,
					Body:          io.NopCloser(strings.NewReader(payload)),
					Request:       r,
				}, nil
			})

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.test/", http.NoBody)
			assert.NoError(t, err, "the request must build")

			got, err := c.AppendFetch(nil, req)
			assert.NoError(t, err, "AppendFetch must read the body that the server sent")
			assert.Equal(t, string(got), payload, "AppendFetch must return the body that the server sent")
		})

		t.Run("stops the read of a body of unknown length one byte beyond the limit", func(t *testing.T) {
			t.Parallel()
			c, err := New(dependency, required, WithMaxResponseBytes(8))
			assert.NoError(t, err, "New must accept the options")

			b := &tracked{r: bytes.NewReader(make([]byte, 1<<20))}
			c.http.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, ContentLength: -1, Body: b, Request: r}, nil
			})

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.test/", http.NoBody)
			assert.NoError(t, err, "the request must build")

			_, err = c.AppendFetch(nil, req)
			assert.ErrorIs(t, err, ErrTooLarge, "AppendFetch must refuse a body beyond the limit")
			assert.Equal(t, b.read.Load(), int64(9), "AppendFetch must read the limit and one byte more")
		})
	})

	t.Run("statusError", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a StatusError for a status below 200", func(t *testing.T) {
			t.Parallel()
			c, err := New(dependency, required)
			assert.NoError(t, err, "New must accept the options")

			err = c.statusError(&http.Response{StatusCode: http.StatusSwitchingProtocols, Header: http.Header{}})
			se := assert.ErrorAs[*StatusError](t, err, "statusError must refuse a status below 200")
			assert.Equal(t, se.Status, http.StatusSwitchingProtocols, "the StatusError must carry the status")
		})
	})
}

// TestClientAllocs checks the allocation ceilings of a call on a
// connection that the transport reuses, which the package documentation
// states with Go 1.27.1, and of the classification of a refused response.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
//
//nolint:paralleltest // see above
func TestClientAllocs(t *testing.T) {
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.test/", http.NoBody)
	assert.NoError(t, err, "the request must build")

	t.Run("Do", func(t *testing.T) {
		for _, tt := range dos(t) {
			t.Run(tt.name, func(t *testing.T) {
				client := piped(t, declared, tt.opts...)

				var (
					status int
					errDo  error
				)
				expect.MaxAllocs(t, func() {
					var resp *http.Response
					if resp, errDo = client.Do(req); errDo != nil {
						return
					}

					// A body read to its end returns the connection for the
					// next call.
					status = resp.StatusCode
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
				}, tt.want, "Do must allocate as the package documentation states")
				assert.NoError(t, errDo, "the test must measure a call that succeeded")
				assert.Equal(t, status, http.StatusOK, "the test must measure a response that succeeded")
			})
		}
	})

	t.Run("Fetch", func(t *testing.T) {
		for _, tt := range fetches {
			t.Run(tt.name, func(t *testing.T) {
				client := piped(t, tt.reply)

				var (
					got      []byte
					errFetch error
				)
				expect.MaxAllocs(t, func() { got, errFetch = client.Fetch(req) }, tt.limit,
					"Fetch must allocate as the package documentation states")
				assert.NoError(t, errFetch, "the test must measure a call that succeeded")
				assert.Length(t, got, tt.want, "the test must measure a body that the client read")
			})
		}
	})

	t.Run("AppendFetch", func(t *testing.T) {
		for _, tt := range bodies {
			t.Run(tt.name, func(t *testing.T) {
				client := piped(t, tt.reply)

				// The buffer has room for the body, and for the 512 bytes that
				// each read of a body of unknown length makes room for.
				buf := make([]byte, 0, 1024)

				var (
					got       []byte
					errAppend error
				)
				expect.MaxAllocs(t, func() { got, errAppend = client.AppendFetch(buf, req) }, tt.want,
					"AppendFetch must allocate as the package documentation states")
				assert.NoError(t, errAppend, "the test must measure a call that succeeded")
				assert.Equal(t, string(got), payload, "the test must measure a body that the client read")
			})
		}
	})

	t.Run("statusError", func(t *testing.T) {
		t.Run("of a refused response without Retry-After", func(t *testing.T) {
			c, err := New(dependency, required)
			assert.NoError(t, err, "New must accept the options")

			resp := &http.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{}}

			var refused error
			expect.MaxAllocs(t, func() { refused = c.statusError(resp) }, 1,
				"the classification must allocate the StatusError alone")
			_ = assert.ErrorAs[*StatusError](t, refused, "the test must measure a refused response")
		})
	})
}

// BenchmarkClient reports the cost of a call on a connection that the
// transport reuses, and of the classification of a refused response, and
// fails above the ceilings that the package documentation states. The
// warm-up of a call dials the connection, as pipeWarmup states.
func BenchmarkClient(b *testing.B) {
	req, err := http.NewRequestWithContext(b.Context(), http.MethodGet, "http://example.test/", http.NoBody)
	assert.NoError(b, err, "the request must build")

	b.Run("Do", func(b *testing.B) {
		for _, tt := range dos(b) {
			b.Run(tt.name, func(b *testing.B) {
				client := piped(b, declared, tt.opts...)

				var status int

				c := bench.Start(b).Warmup(pipeWarmup).MaxAllocs(tt.want)
				defer c.End()

				for c.Loop() {
					resp, errDo := client.Do(req)
					if errDo != nil {
						b.Fatalf("Do: %v", errDo)
					}

					// A body read to its end returns the connection for the
					// next call.
					status = resp.StatusCode
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
				}

				assert.Equal(b, status, http.StatusOK, "the benchmark must measure a response that succeeded")
			})
		}
	})

	b.Run("Fetch", func(b *testing.B) {
		for _, tt := range fetches {
			b.Run(tt.name, func(b *testing.B) {
				client := piped(b, tt.reply)

				var (
					got      []byte
					errFetch error
				)

				c := bench.Start(b).Warmup(pipeWarmup).MaxAllocs(tt.limit)
				defer c.End()

				for c.Loop() {
					got, errFetch = client.Fetch(req)
				}

				assert.NoError(b, errFetch, "the benchmark must measure a call that succeeded")
				assert.Length(b, got, tt.want, "the benchmark must measure a body that the client read")
			})
		}
	})

	b.Run("AppendFetch", func(b *testing.B) {
		for _, tt := range bodies {
			b.Run(tt.name, func(b *testing.B) {
				client := piped(b, tt.reply)

				// The buffer has room for the body, and for the 512 bytes that
				// each read of a body of unknown length makes room for.
				buf := make([]byte, 0, 1024)

				var (
					got       []byte
					errAppend error
				)

				c := bench.Start(b).Warmup(pipeWarmup).MaxAllocs(tt.want)
				defer c.End()

				for c.Loop() {
					got, errAppend = client.AppendFetch(buf, req)
				}

				assert.NoError(b, errAppend, "the benchmark must measure a call that succeeded")
				assert.Equal(b, string(got), payload, "the benchmark must measure a body that the client read")
			})
		}
	})

	b.Run("statusError", func(b *testing.B) {
		b.Run("of a refused response without Retry-After", func(b *testing.B) {
			client, err := New(dependency, required)
			assert.NoError(b, err, "New must accept the options")

			resp := &http.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{}}

			var refused error

			c := bench.Start(b).MaxAllocs(1)
			defer c.End()

			for c.Loop() {
				refused = client.statusError(resp)
			}

			_ = assert.ErrorAs[*StatusError](b, refused, "the benchmark must measure a refused response")
		})
	})
}

// dos returns the configurations of the allocation ceilings of Do: an
// untraced request, a traced request, and a client with a breaker and a
// retrier. It fails tb when a guard does not build.
func dos(tb testing.TB) []doConfig {
	tb.Helper()

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
	assert.NoError(tb, err, "the retrier must build")

	breaker, err := resilience.NewBreaker(resilience.BreakerConfig{
		Clock:            fake.New(origin),
		TripOn:           []errs.Class{errs.Transient},
		FailureThreshold: 5,
		SuccessThreshold: 1,
		OpenFor:          time.Second,
	})
	assert.NoError(tb, err, "the breaker must build")

	return []doConfig{
		{name: "of an untraced request", want: 54},
		{name: "of a traced request", opts: []Option{WithReporter(traced{})}, want: 60},
		{name: "with a breaker and a retrier", opts: []Option{WithBreaker(breaker), WithRetrier(retrier)}, want: 54},
	}
}

// piped returns a Client with the options of the internal cases and opts,
// which dials, through WithDialContext, a pipe to respond, which writes
// reply to every request. Its calls run through net/http's Client and
// Transport on one connection that they reuse, and connect to no network.
// The cleanup of tb closes the connection.
func piped(tb testing.TB, reply []byte, opts ...Option) *Client {
	tb.Helper()

	client, err := New(dependency, required, WithReach(ReachPrivate),
		WithDialContext(func(context.Context, string, string) (net.Conn, error) {
			conn, peer := net.Pipe()
			go respond(peer, reply)

			return conn, nil
		}),
		Options(opts...))
	assert.NoError(tb, err, "New must accept the options")
	tb.Cleanup(client.http.CloseIdleConnections)

	return client
}

// respond writes reply to conn for every request that it reads from conn,
// until conn closes. The requests of the benchmark have no body, so each
// ends with its header, and fits one read of the buffer. respond does not
// allocate per request. It yields before each reply, so the transport
// reports the request written before it reads the response, as over a
// network.
func respond(conn net.Conn, reply []byte) {
	buf := make([]byte, 4096)

	for {
		n, err := conn.Read(buf)
		if err != nil {
			return
		}

		if bytes.Contains(buf[:n], endOfHeader) {
			runtime.Gosched()

			if _, err := conn.Write(reply); err != nil {
				return
			}
		}
	}
}
