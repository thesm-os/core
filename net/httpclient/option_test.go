// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package httpclient_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/net/httpclient"
	"go.thesmos.sh/core/net/internal/semconv"
	"go.thesmos.sh/core/rand/pcg"
	"go.thesmos.sh/core/resilience"
	"go.thesmos.sh/core/telemetry"
)

func TestOption(t *testing.T) {
	t.Parallel()

	t.Run("Options", func(t *testing.T) {
		t.Parallel()

		t.Run("applies the options in order, so a later option overrides an earlier one", func(t *testing.T) {
			t.Parallel()

			_, err := httpclient.New(dependency, httpclient.Options(
				required, loopback, httpclient.WithReach(7), httpclient.WithReach(httpclient.ReachPrivate),
			))
			testkit.NoError(t, err, "the later reach must override the reach that New refuses")
		})

		t.Run("makes New return ErrConfig for a nil option that it bundles", func(t *testing.T) {
			t.Parallel()

			_, err := httpclient.New(dependency, required, loopback, httpclient.Options(nil))
			testkit.ErrorIs(t, err, httpclient.ErrConfig, "New must refuse the nil option")
		})
	})

	t.Run("WithRetrier", func(t *testing.T) {
		t.Parallel()

		t.Run("retries a Transient failure of an idempotent request on the same connection", func(t *testing.T) {
			t.Parallel()

			var hits, conns atomic.Int32
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if hits.Add(1) == 1 {
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = io.WriteString(w, "busy")

					return
				}

				_, _ = io.WriteString(w, body)
			}))
			srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
				if s == http.StateNew {
					conns.Add(1)
				}
			}
			srv.Start()
			t.Cleanup(srv.Close)

			c, err := httpclient.New(
				dependency,
				required,
				loopback,
				httpclient.WithRetrier(retrier(t, fake.New(origin))),
			)
			testkit.NoError(t, err, "New must accept the options")

			status, got, err := get(t, c, srv.URL)
			testkit.NoError(t, err, "Do")
			testkit.Equal(t, status, http.StatusOK, "the status of the retry")
			testkit.Equal(t, got, body, "the body of the retry")
			testkit.Equal(t, hits.Load(), int32(2), "the attempts")
			testkit.Equal(t, conns.Load(), int32(1), "the retry must reuse the connection of the discarded response")
		})

		replays := []struct {
			header http.Header
			body   func() io.Reader
			name   string
			method string
			want   int32
		}{
			{
				name:   "retries a POST with an Idempotency-Key header, and replays its body",
				method: http.MethodPost,
				header: http.Header{"Idempotency-Key": {"k1"}},
				body:   func() io.Reader { return strings.NewReader(body) },
				want:   2,
			},
			{
				name:   "retries a POST with an X-Idempotency-Key header",
				method: http.MethodPost,
				header: http.Header{"X-Idempotency-Key": {"k1"}},
				body:   func() io.Reader { return strings.NewReader(body) },
				want:   2,
			},
			{
				name:   "sends a POST without an idempotency key once",
				method: http.MethodPost,
				body:   func() io.Reader { return strings.NewReader(body) },
				want:   1,
			},
			{
				name:   "sends a request whose body cannot be produced again once",
				method: http.MethodPut,
				body:   func() io.Reader { return io.MultiReader(strings.NewReader(body)) },
				want:   1,
			},
			{
				name:   "retries a DELETE without a body",
				method: http.MethodDelete,
				body:   func() io.Reader { return http.NoBody },
				want:   2,
			},
		}
		for _, tt := range replays {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				var hits atomic.Int32

				received := make(chan string, 2)
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					b, _ := io.ReadAll(r.Body)
					received <- string(b)

					if hits.Add(1) == 1 {
						w.WriteHeader(http.StatusServiceUnavailable)
					}
				}))
				t.Cleanup(srv.Close)

				c, err := httpclient.New(
					dependency,
					required,
					loopback,
					httpclient.WithRetrier(retrier(t, fake.New(origin))),
				)
				testkit.NoError(t, err, "New must accept the options")

				_, _ = fetch(t, c, tt.method, srv.URL, tt.body(), tt.header)
				testkit.Equal(t, hits.Load(), tt.want, "the attempts")

				first := await(t, received, "the first attempt must arrive")
				if tt.want == 2 {
					testkit.Equal(
						t,
						await(t, received, "the retry must arrive"),
						first,
						"the retry must send the body again",
					)
				}
			})
		}

		t.Run("makes Fetch return the error of a GetBody that cannot replay the body", func(t *testing.T) {
			t.Parallel()

			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			t.Cleanup(srv.Close)

			c, err := httpclient.New(
				dependency,
				required,
				loopback,
				httpclient.WithRetrier(retrier(t, fake.New(origin))),
			)
			testkit.NoError(t, err, "New must accept the options")

			req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, srv.URL, strings.NewReader(body))
			testkit.NoError(t, err, "the request must build")
			req.GetBody = func() (io.ReadCloser, error) { return nil, errBoom }

			_, err = c.Fetch(req)
			testkit.ErrorIs(t, err, errBoom, "Fetch must return the error of GetBody")
			testkit.Equal(t, hits.Load(), int32(1), "the client must send no attempt without its body")
		})

		t.Run("waits for the delay of Retry-After before the retry", func(t *testing.T) {
			t.Parallel()

			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if hits.Add(1) == 1 {
					w.Header().Set("Retry-After", "30")
					w.WriteHeader(http.StatusServiceUnavailable)

					return
				}

				_, _ = io.WriteString(w, body)
			}))
			t.Cleanup(srv.Close)

			clk := fake.New(origin)
			c, err := httpclient.New(dependency, required, loopback, httpclient.WithRetrier(retrier(t, clk)))
			testkit.NoError(t, err, "New must accept the options")

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, http.NoBody)
			testkit.NoError(t, err, "the request must build")

			done := make(chan error, 1)
			go func() {
				_, err := c.Fetch(req)
				done <- err
			}()

			clk.AwaitWaiters(1)
			testkit.Equal(t, hits.Load(), int32(1), "the retry must wait for the delay")
			clk.Advance(30 * time.Second)
			testkit.NoError(t, await(t, done, "Fetch must return"), "the retry after the delay must succeed")
			testkit.Equal(t, hits.Load(), int32(2), "the attempts")
		})

		t.Run(
			"makes Do return the error of the context when the caller cancels the wait of a retry",
			func(t *testing.T) {
				t.Parallel()

				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Retry-After", "30")
					w.WriteHeader(http.StatusServiceUnavailable)
				}))
				t.Cleanup(srv.Close)

				clk := fake.New(origin)
				c, err := httpclient.New(dependency, required, loopback, httpclient.WithRetrier(retrier(t, clk)))
				testkit.NoError(t, err, "New must accept the options")

				ctx, cancel := context.WithCancel(t.Context())
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, http.NoBody)
				testkit.NoError(t, err, "the request must build")

				go func() {
					clk.AwaitWaiters(1)
					cancel()
				}()

				resp, err := c.Do(req) //nolint:bodyclose // Do returns no response with the error
				testkit.ErrorIs(t, err, context.Canceled, "Do must return the error of the context")
				testkit.True(t, resp == nil, "Do must return no response")
			},
		)
	})

	t.Run("WithBreaker", func(t *testing.T) {
		t.Parallel()

		t.Run("opens the circuit of a host after its failures", func(t *testing.T) {
			t.Parallel()

			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				w.WriteHeader(http.StatusBadGateway)
			}))
			t.Cleanup(srv.Close)

			b := breaker(t, 2)
			c, err := httpclient.New(dependency, required, loopback, httpclient.WithBreaker(b))
			testkit.NoError(t, err, "New must accept the options")

			for range 2 {
				_, err = fetch(t, c, http.MethodGet, srv.URL, http.NoBody, nil)
				testkit.Equal(t, errs.Classify(err), errs.Transient, "a 502 must classify as Transient")
			}

			testkit.Equal(t, b.State("127.0.0.1"), resilience.Open, "the circuit of the host")

			_, err = fetch(t, c, http.MethodGet, srv.URL, http.NoBody, nil)
			testkit.ErrorIs(t, err, resilience.ErrOpen, "the open circuit must refuse the call")
			testkit.Equal(t, hits.Load(), int32(2), "the open circuit must send no request")
		})

		t.Run("makes Do close the response of an attempt before the circuit refuses the retry", func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = io.WriteString(w, "busy")
			}))
			t.Cleanup(srv.Close)

			// Two attempts open the circuit, and the third finds it open.
			c, err := httpclient.New(dependency, required, loopback,
				httpclient.WithBreaker(breaker(t, 2)), httpclient.WithRetrier(retrier(t, fake.New(origin))))
			testkit.NoError(t, err, "New must accept the options")

			// The transport returns the connection of a response with a body to
			// its pool once the body is read to its end and closed.
			idle := make(chan struct{}, 2)
			ctx := httptrace.WithClientTrace(t.Context(), &httptrace.ClientTrace{
				PutIdleConn: func(err error) {
					if err == nil {
						idle <- struct{}{}
					}
				},
			})

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, http.NoBody)
			testkit.NoError(t, err, "the request must build")

			_, err = c.Do(req) //nolint:bodyclose // Do returns no response with the error
			testkit.ErrorIs(t, err, resilience.ErrOpen, "Do must return the refusal of the circuit")
			await(t, idle, "the response of the first attempt must close")
			await(t, idle, "the response of the second attempt must close")
		})

		t.Run("records no outcome in the circuit when the caller cancels", func(t *testing.T) {
			t.Parallel()

			entered, release := make(chan struct{}), make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				close(entered)
				<-release
			}))
			t.Cleanup(srv.Close)
			t.Cleanup(func() { close(release) })

			// One failure would open the circuit.
			b := breaker(t, 1)
			c, err := httpclient.New(dependency, required, loopback, httpclient.WithBreaker(b))
			testkit.NoError(t, err, "New must accept the options")

			ctx, cancel := context.WithCancel(t.Context())
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, http.NoBody)
			testkit.NoError(t, err, "the request must build")

			go func() {
				<-entered
				cancel()
			}()

			_, err = c.Fetch(req)
			testkit.ErrorIs(t, err, context.Canceled, "Fetch must return the error of the context")
			testkit.Equal(t, b.State("127.0.0.1"), resilience.Closed, "a cancelled call must leave the circuit closed")
		})
	})

	t.Run("WithProxy", func(t *testing.T) {
		t.Parallel()

		t.Run("sends a request through the proxy without the address check", func(t *testing.T) {
			t.Parallel()

			targets := make(chan string, 1)
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				targets <- r.URL.String()
				_, _ = io.WriteString(w, body)
			}))
			t.Cleanup(proxy.Close)

			u, err := url.Parse(proxy.URL)
			testkit.NoError(t, err, "the URL of the proxy must parse")

			r := newReporter(t)
			c, err := httpclient.New(dependency, required,
				httpclient.WithHosts("dependency.test"), httpclient.WithProxy(u), httpclient.WithReporter(r))
			testkit.NoError(t, err, "New must accept the options")

			got, err := fetch(t, c, http.MethodGet, "http://dependency.test/items", http.NoBody, nil)
			testkit.NoError(t, err, "Fetch through the proxy")
			testkit.Equal(t, string(got), body, "the body of the proxy")
			testkit.Equal(t, await(t, targets, "the proxy must receive the request"), "http://dependency.test/items",
				"the target that the proxy received")
			testkit.Equal(t, r.bound()[0].attrs[1:3], []telemetry.Attr{
				telemetry.AttrString(semconv.ServerAddress, "dependency.test"),
				telemetry.AttrInt(semconv.ServerPort, 80),
			}, "the address and the default port of http")
		})
	})

	t.Run("WithTLS", func(t *testing.T) {
		t.Parallel()

		t.Run("verifies the certificate of a server with the configuration", func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, body)
			}))
			t.Cleanup(srv.Close)

			transport, ok := srv.Client().Transport.(*http.Transport)
			testkit.True(t, ok, "the client of httptest must have an http.Transport")

			c, err := httpclient.New(dependency, required, loopback, httpclient.WithTLS(transport.TLSClientConfig))
			testkit.NoError(t, err, "New must accept the options")

			got, err := fetch(t, c, http.MethodGet, srv.URL, http.NoBody, nil)
			testkit.NoError(t, err, "Fetch over TLS")
			testkit.Equal(t, string(got), body, "the body")
		})

		t.Run("makes Fetch return an Integrity error for a certificate that fails verification", func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			t.Cleanup(srv.Close)

			c, err := httpclient.New(dependency, required, loopback)
			testkit.NoError(t, err, "New must accept the options")

			_, err = fetch(t, c, http.MethodGet, srv.URL, http.NoBody, nil)
			_ = testkit.ErrorAs[*tls.CertificateVerificationError](t, err, "Fetch must fail the verification")
			testkit.Equal(t, errs.Classify(err), errs.Integrity, "the class of the error")
		})
	})

	t.Run("WithTLSHandshakeTimeout", func(t *testing.T) {
		t.Parallel()

		t.Run("makes Fetch return a Transient error for a handshake beyond the timeout", func(t *testing.T) {
			t.Parallel()

			var lc net.ListenConfig

			ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
			testkit.NoError(t, err, "the silent listener must listen")

			// The listener accepts connections and never writes to them. The
			// cleanup closes the listener and every connection.
			var (
				mu    sync.Mutex
				conns []net.Conn
			)

			t.Cleanup(func() {
				_ = ln.Close()

				mu.Lock()
				defer mu.Unlock()

				for _, conn := range conns {
					_ = conn.Close()
				}
			})

			go func() {
				for {
					conn, errAccept := ln.Accept()
					if errAccept != nil {
						return
					}

					mu.Lock()
					conns = append(conns, conn)
					mu.Unlock()
				}
			}()

			c, err := httpclient.New(dependency, required, loopback,
				httpclient.WithTLSHandshakeTimeout(50*time.Millisecond))
			testkit.NoError(t, err, "New must accept the options")

			_, err = fetch(t, c, http.MethodGet, "https://"+ln.Addr().String(), http.NoBody, nil)
			testkit.Contains(t, err.Error(), "TLS handshake timeout", "Fetch must end at the timeout of the handshake")
			testkit.Equal(t, errs.Classify(err), errs.Transient, "the class of the error")
		})
	})

	t.Run("WithDialTimeout", func(t *testing.T) {
		t.Parallel()

		t.Run("makes Fetch return a Transient error for a resolution beyond the timeout", func(t *testing.T) {
			t.Parallel()

			// The resolver responds to no query, so the dial ends at its
			// timeout, which bounds the resolution of the name as well.
			silent := &net.Resolver{
				PreferGo: true,
				Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
					<-ctx.Done()

					return nil, ctx.Err()
				},
			}

			c, err := httpclient.New(dependency, required, httpclient.WithHosts("example.test"),
				httpclient.WithResolver(silent), httpclient.WithDialTimeout(50*time.Millisecond))
			testkit.NoError(t, err, "New must accept the options")

			// Without the option, the dial waits for its default of 5 s or for
			// the retries of the resolver, so a dial that ends within 2 s ended
			// at the option.
			start := time.Now()
			_, err = fetch(t, c, http.MethodGet, "http://example.test/", http.NoBody, nil)
			testkit.ErrorIs(t, err, context.DeadlineExceeded, "the dial must end at its timeout")
			testkit.Equal(t, errs.Classify(err), errs.Transient, "the class of the error")
			testkit.True(t, time.Since(start) < 2*time.Second, "the dial must end at the timeout of the option")
		})
	})

	t.Run("WithDialContext", func(t *testing.T) {
		t.Parallel()

		// unresolved admits dependency.test, a name that does not resolve, so
		// a call connects only through the dial function of a case.
		unresolved := httpclient.Options(
			httpclient.WithHosts("dependency.test"),
			httpclient.WithReach(httpclient.ReachPrivate),
		)

		t.Run("connects the client through the dial function", func(t *testing.T) {
			t.Parallel()

			type dialed struct{ Network, Address string }

			// The dial function returns one end of a pipe, and a goroutine of
			// the case writes the response to one request on the other end.
			dials := make(chan dialed, 1)
			c, err := httpclient.New(dependency, required, unresolved,
				httpclient.WithDialContext(func(_ context.Context, network, address string) (net.Conn, error) {
					dials <- dialed{Network: network, Address: address}

					conn, peer := net.Pipe()
					go func() {
						defer peer.Close()

						req, errRead := http.ReadRequest(bufio.NewReader(peer))
						if errRead != nil {
							return
						}

						resp := &http.Response{
							StatusCode:    http.StatusOK,
							ProtoMajor:    1,
							ProtoMinor:    1,
							ContentLength: int64(len(body)),
							Body:          io.NopCloser(strings.NewReader(body)),
							Request:       req,
						}
						_ = resp.Write(peer)
					}()

					return conn, nil
				}))
			testkit.NoError(t, err, "New must accept the options")

			got, err := fetch(t, c, http.MethodGet, "http://dependency.test/items", http.NoBody, nil)
			testkit.NoError(t, err, "Fetch through the dial function")
			testkit.Equal(t, string(got), body, "the body of the response on the pipe")
			testkit.Equal(t, await(t, dials, "the client must call the dial function"),
				dialed{Network: "tcp", Address: "dependency.test:80"}, "the network and the address of the dial")
		})

		t.Run("ends a dial at the dial timeout", func(t *testing.T) {
			t.Parallel()

			// The dial function returns when its context ends, and errHung
			// when the context has not ended within patience.
			c, err := httpclient.New(dependency, required, unresolved, httpclient.WithDialTimeout(50*time.Millisecond),
				httpclient.WithDialContext(func(ctx context.Context, _, _ string) (net.Conn, error) {
					timer := time.NewTimer(patience)
					defer timer.Stop()

					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-timer.C:
						return nil, errHung
					}
				}))
			testkit.NoError(t, err, "New must accept the options")

			_, err = fetch(t, c, http.MethodGet, "http://dependency.test/", http.NoBody, nil)
			testkit.ErrorIs(t, err, context.DeadlineExceeded, "the dial must end at its timeout")
			testkit.Equal(t, errs.Classify(err), errs.Transient, "the class of the error")
		})

		t.Run("ends the context of a dial when the dial returns", func(t *testing.T) {
			t.Parallel()

			contexts := make(chan context.Context, 1)
			c, err := httpclient.New(dependency, required, unresolved,
				httpclient.WithDialContext(func(ctx context.Context, _, _ string) (net.Conn, error) {
					contexts <- ctx

					return nil, errBoom
				}))
			testkit.NoError(t, err, "New must accept the options")

			_, err = fetch(t, c, http.MethodGet, "http://dependency.test/", http.NoBody, nil)
			testkit.ErrorIs(t, err, errBoom, "Fetch must return the error of the dial")

			ctx := await(t, contexts, "the client must call the dial function")
			testkit.ErrorIs(t, ctx.Err(), context.Canceled, "the context of the dial must end when the dial returns")
		})

		t.Run("dials without a deadline when the dial timeout is off", func(t *testing.T) {
			t.Parallel()

			deadlines := make(chan bool, 1)
			c, err := httpclient.New(dependency, required, unresolved, httpclient.WithDialTimeout(-1),
				httpclient.WithDialContext(func(ctx context.Context, _, _ string) (net.Conn, error) {
					_, ok := ctx.Deadline()
					deadlines <- ok

					return nil, errBoom
				}))
			testkit.NoError(t, err, "New must accept the options")

			_, err = fetch(t, c, http.MethodGet, "http://dependency.test/", http.NoBody, nil)
			testkit.ErrorIs(t, err, errBoom, "Fetch must return the error of the dial")
			testkit.False(t, await(t, deadlines, "the client must call the dial function"),
				"the context of the dial must have no deadline")
		})

		t.Run("restores the client's dialer for nil", func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			t.Cleanup(srv.Close)

			// The client's dialer checks the addresses of ReachPublic, the
			// default, which refuses the loopback address of the server.
			c, err := httpclient.New(dependency, required, httpclient.WithHosts("127.0.0.1"),
				httpclient.WithDialContext(func(context.Context, string, string) (net.Conn, error) {
					return nil, errBoom
				}),
				httpclient.WithDialContext(nil))
			testkit.NoError(t, err, "New must accept a nil dial function for a client of ReachPublic")

			_, err = fetch(t, c, http.MethodGet, srv.URL, http.NoBody, nil)
			testkit.ErrorIs(t, err, httpclient.ErrBlocked, "the client's dialer must check the address")
		})
	})
}

// retrier returns a Retrier of 3 attempts on clk, whose backoff is below a
// nanosecond, so a retry waits only for the delay of a Retry-After header.
func retrier(t *testing.T, clk *fake.Clock) *resilience.Retrier {
	t.Helper()

	r, err := resilience.NewRetrier(resilience.RetryConfig{
		Clock:         clk,
		Rand:          pcg.New(1),
		Attempts:      3,
		Base:          1,
		Max:           1,
		MaxRetryAfter: time.Minute,
		MinRetries:    10,
		BudgetWindow:  time.Minute,
	})
	testkit.NoError(t, err, "the retrier of the case must build")

	return r
}

// breaker returns a Breaker that opens a circuit after failures Transient
// failures, for an hour of a fake clock.
func breaker(t *testing.T, failures int) *resilience.Breaker {
	t.Helper()

	b, err := resilience.NewBreaker(resilience.BreakerConfig{
		Clock:            fake.New(origin),
		TripOn:           []errs.Class{errs.Transient},
		FailureThreshold: failures,
		SuccessThreshold: 1,
		OpenFor:          time.Hour,
	})
	testkit.NoError(t, err, "the breaker of the case must build")

	return b
}
