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

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

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

		t.Run("lets a later option override an earlier one", func(t *testing.T) {
			t.Parallel()
			_, err := httpclient.New(dependency, httpclient.Options(
				required, loopback, httpclient.WithReach(7), httpclient.WithReach(httpclient.ReachPrivate),
			))
			assert.NoError(t, err, "the later reach must override the reach that New refuses")
		})

		t.Run("makes New return ErrConfig for a nil option that it bundles", func(t *testing.T) {
			t.Parallel()
			_, err := httpclient.New(dependency, required, loopback, httpclient.Options(nil))
			assert.ErrorIs(t, err, httpclient.ErrConfig, "New must refuse the nil option")
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

			retry := retrier(t, fake.New(origin))
			c, err := httpclient.New(dependency, required, loopback, httpclient.WithRetrier(retry))
			assert.NoError(t, err, "New must accept the options")

			status, got, err := get(t, c, srv.URL)
			assert.NoError(t, err, "Do must return the response of the retry")
			expect.Equal(t, status, http.StatusOK, "the retry must succeed")
			expect.Equal(t, got, body, "Do must return the body of the retry")
			expect.Equal(t, hits.Load(), int32(2), "the client must send two attempts")
			expect.Equal(t, conns.Load(), int32(1), "the retry must reuse the connection of the discarded response")
		})

		tests := []struct {
			header http.Header
			body   func() io.Reader
			name   string
			method string
			want   int32
		}{
			{
				name:   "retries a POST with an Idempotency-Key header",
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
			{
				name:   "retries a GET with a nil body",
				method: http.MethodGet,
				body:   func() io.Reader { return nil },
				want:   2,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				var hits atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.Copy(io.Discard, r.Body)
					if hits.Add(1) == 1 {
						w.WriteHeader(http.StatusServiceUnavailable)
					}
				}))
				t.Cleanup(srv.Close)

				retry := retrier(t, fake.New(origin))
				c, err := httpclient.New(dependency, required, loopback, httpclient.WithRetrier(retry))
				assert.NoError(t, err, "New must accept the options")

				_, _ = fetch(t, c, tt.method, srv.URL, tt.body(), tt.header)
				assert.Equal(t, hits.Load(), tt.want, "the client must send the request as often as it may")
			})
		}

		t.Run("sends the body again on a retry", func(t *testing.T) {
			t.Parallel()
			var hits atomic.Int32
			received := make(chan string, attempts)
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

			keyed := http.Header{"Idempotency-Key": {"k1"}}
			_, err = fetch(t, c, http.MethodPost, srv.URL, strings.NewReader(body), keyed)
			assert.NoError(t, err, "the retry must succeed")
			expect.Equal(t, await(t, received, "the first attempt must arrive"), body, "the first attempt must send it")
			expect.Equal(t, await(t, received, "the retry must arrive"), body, "the retry must send the body again")
		})

		t.Run("produces the body of a retry with one call of GetBody", func(t *testing.T) {
			t.Parallel()
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if hits.Add(1) == 1 {
					w.WriteHeader(http.StatusServiceUnavailable)
				}
			}))
			t.Cleanup(srv.Close)

			retry := retrier(t, fake.New(origin))
			c, err := httpclient.New(dependency, required, loopback, httpclient.WithRetrier(retry))
			assert.NoError(t, err, "New must accept the options")

			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL, strings.NewReader(body))
			assert.NoError(t, err, "the request must build")
			req.Header.Set("Idempotency-Key", "k1")

			// net/http calls GetBody itself to send a body again that it read
			// already, so a second call shows that the retry sent the read body.
			var gets atomic.Int32
			getBody := req.GetBody
			req.GetBody = func() (io.ReadCloser, error) {
				gets.Add(1)

				return getBody()
			}

			_, err = c.Fetch(req)
			assert.NoError(t, err, "the retry must succeed")
			assert.Equal(t, gets.Load(), int32(1), "the retry must send the body of one call of GetBody")
		})

		t.Run("makes Fetch return the error of a GetBody that cannot replay the body", func(t *testing.T) {
			t.Parallel()
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			t.Cleanup(srv.Close)

			retry := retrier(t, fake.New(origin))
			c, err := httpclient.New(dependency, required, loopback, httpclient.WithRetrier(retry))
			assert.NoError(t, err, "New must accept the options")

			req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, srv.URL, strings.NewReader(body))
			assert.NoError(t, err, "the request must build")
			req.GetBody = func() (io.ReadCloser, error) { return nil, errBoom }

			_, err = c.Fetch(req)
			expect.ErrorIs(t, err, errBoom, "Fetch must return the error of GetBody")
			expect.Equal(t, hits.Load(), int32(1), "the client must send no attempt without its body")
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
			assert.NoError(t, err, "New must accept the options")

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, http.NoBody)
			assert.NoError(t, err, "the request must build")

			done := make(chan error, 1)
			go func() {
				_, err := c.Fetch(req)
				done <- err
			}()

			clk.AwaitWaiters(1)
			assert.Equal(t, hits.Load(), int32(1), "the retry must wait for the delay")
			clk.Advance(30 * time.Second)
			assert.NoError(t, await(t, done, "Fetch must return"), "the retry after the delay must succeed")
			assert.Equal(t, hits.Load(), int32(2), "the client must send the retry after the delay")
		})

		t.Run("makes Do return the error of the context when the caller cancels a wait", func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", "30")
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			t.Cleanup(srv.Close)

			clk := fake.New(origin)
			c, err := httpclient.New(dependency, required, loopback, httpclient.WithRetrier(retrier(t, clk)))
			assert.NoError(t, err, "New must accept the options")

			ctx, cancel := context.WithCancel(t.Context())
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, http.NoBody)
			assert.NoError(t, err, "the request must build")

			go func() {
				clk.AwaitWaiters(1)
				cancel()
			}()

			resp, err := c.Do(req) //nolint:bodyclose // Do returns no response with the error
			expect.ErrorIs(t, err, context.Canceled, "Do must return the error of the context")
			expect.Nil(t, resp, "Do must return no response")
		})
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
			assert.NoError(t, err, "New must accept the options")

			for range 2 {
				_, err = fetch(t, c, http.MethodGet, srv.URL, http.NoBody, nil)
				assert.Equal(t, errs.Classify(err), errs.Transient, "a 502 must classify as Transient")
			}

			assert.Equal(t, b.State("127.0.0.1"), resilience.Open, "two failures must open the circuit of the host")

			_, err = fetch(t, c, http.MethodGet, srv.URL, http.NoBody, nil)
			expect.ErrorIs(t, err, resilience.ErrOpen, "the open circuit must refuse the call")
			expect.Equal(t, hits.Load(), int32(2), "the open circuit must send no request")
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
			assert.NoError(t, err, "New must accept the options")

			// The transport returns the connection of a response with a body to
			// its pool once the body is read to its end and closed.
			idle := make(chan struct{}, attempts)
			ctx := httptrace.WithClientTrace(t.Context(), &httptrace.ClientTrace{
				PutIdleConn: func(err error) {
					if err == nil {
						idle <- struct{}{}
					}
				},
			})

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, http.NoBody)
			assert.NoError(t, err, "the request must build")

			_, err = c.Do(req) //nolint:bodyclose // Do returns no response with the error
			assert.ErrorIs(t, err, resilience.ErrOpen, "Do must return the refusal of the circuit")
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
			assert.NoError(t, err, "New must accept the options")

			ctx, cancel := context.WithCancel(t.Context())
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, http.NoBody)
			assert.NoError(t, err, "the request must build")

			go func() {
				<-entered
				cancel()
			}()

			_, err = c.Fetch(req)
			expect.ErrorIs(t, err, context.Canceled, "Fetch must return the error of the context")
			expect.Equal(t, b.State("127.0.0.1"), resilience.Closed, "a cancelled call must leave the circuit closed")
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
			assert.NoError(t, err, "the URL of the proxy must parse")

			r := newReporter(t)
			c, err := httpclient.New(dependency, required,
				httpclient.WithHosts("dependency.test"), httpclient.WithProxy(u), httpclient.WithReporter(r))
			assert.NoError(t, err, "New must accept the options")

			got, err := fetch(t, c, http.MethodGet, "http://dependency.test/items", http.NoBody, nil)
			assert.NoError(t, err, "Fetch must send the request through the proxy")
			expect.Equal(t, string(got), body, "Fetch must return the body of the proxy")
			expect.Equal(t, await(t, targets, "the proxy must receive the request"), "http://dependency.test/items",
				"the proxy must receive the URL of the request")

			sets := r.bound()
			assert.Length(t, sets, 1, "the client must bind one set")
			assert.Equal(t, sets[0].attrs[1:3], []telemetry.Attr{
				telemetry.AttrString(semconv.ServerAddress, "dependency.test"),
				telemetry.AttrInt(semconv.ServerPort, 80),
			}, "the set must record the host of the URL and the default port of http")
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
			assert.True(t, ok, "the client of httptest must have an http.Transport")

			c, err := httpclient.New(dependency, required, loopback, httpclient.WithTLS(transport.TLSClientConfig))
			assert.NoError(t, err, "New must accept the options")

			got, err := fetch(t, c, http.MethodGet, srv.URL, http.NoBody, nil)
			assert.NoError(t, err, "Fetch must verify the certificate of the server")
			assert.Equal(t, string(got), body, "Fetch must return the body over TLS")
		})

		t.Run("makes Fetch return an Integrity error for a certificate that fails verification", func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			t.Cleanup(srv.Close)

			c, err := httpclient.New(dependency, required, loopback)
			assert.NoError(t, err, "New must accept the options")

			_, err = fetch(t, c, http.MethodGet, srv.URL, http.NoBody, nil)
			_ = assert.ErrorAs[*tls.CertificateVerificationError](t, err, "Fetch must fail the verification")
			assert.Equal(t, errs.Classify(err), errs.Integrity, "a failed verification must be Integrity")
		})
	})

	t.Run("WithTLSHandshakeTimeout", func(t *testing.T) {
		t.Parallel()

		t.Run("makes Fetch return a Transient error for a handshake beyond the timeout", func(t *testing.T) {
			t.Parallel()
			var lc net.ListenConfig

			ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
			assert.NoError(t, err, "the silent listener must listen")

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
			assert.NoError(t, err, "New must accept the options")

			_, err = fetch(t, c, http.MethodGet, "https://"+ln.Addr().String(), http.NoBody, nil)
			assert.HasError(t, err, "Fetch must fail without a handshake")
			expect.Contains(t, err.Error(), "TLS handshake timeout", "Fetch must end at the timeout of the handshake")
			expect.Equal(t, errs.Classify(err), errs.Transient, "a handshake beyond its timeout must be Transient")
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
			assert.NoError(t, err, "New must accept the options")

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.test/", http.NoBody)
			assert.NoError(t, err, "the request must build")

			// Without the option, the dial waits for its default of 5 s or for
			// the retries of the resolver, so a dial that ends within 2 s ended
			// at the option.
			var errFetch error
			assert.CompletesWithin(t, 2*time.Second, func(ctx context.Context) error {
				// Fetch reads the context of its request, which carries ctx.
				_, errFetch = c.Fetch(req.WithContext(ctx)) //nolint:contextcheck // see above

				return errFetch
			}, "the dial must end at the timeout of the option")
			expect.ErrorIs(t, errFetch, context.DeadlineExceeded, "the dial must end at its timeout")
			expect.Equal(t, errs.Classify(errFetch), errs.Transient, "a dial beyond its timeout must be Transient")
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
			assert.NoError(t, err, "New must accept the options")

			got, err := fetch(t, c, http.MethodGet, "http://dependency.test/items", http.NoBody, nil)
			assert.NoError(t, err, "Fetch must connect through the dial function")
			expect.Equal(t, string(got), body, "Fetch must return the body of the response on the pipe")
			expect.Equal(t, await(t, dials, "the client must call the dial function"),
				dialed{Network: "tcp", Address: "dependency.test:80"}, "the dial must receive the host and the port")
		})

		t.Run("ends a dial at the dial timeout", func(t *testing.T) {
			t.Parallel()
			const dialTimeout = 50 * time.Millisecond

			// The dial function returns when its context ends, and errHung
			// when the context has not ended within twenty dial timeouts.
			c, err := httpclient.New(dependency, required, unresolved, httpclient.WithDialTimeout(dialTimeout),
				httpclient.WithDialContext(func(ctx context.Context, _, _ string) (net.Conn, error) {
					timer := time.NewTimer(20 * dialTimeout)
					defer timer.Stop()

					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-timer.C:
						return nil, errHung
					}
				}))
			assert.NoError(t, err, "New must accept the options")

			_, err = fetch(t, c, http.MethodGet, "http://dependency.test/", http.NoBody, nil)
			expect.ErrorIs(t, err, context.DeadlineExceeded, "the dial must end at its timeout")
			expect.Equal(t, errs.Classify(err), errs.Transient, "a dial beyond its timeout must be Transient")
		})

		t.Run("ends the context of a dial when the dial returns", func(t *testing.T) {
			t.Parallel()
			contexts := make(chan context.Context, 1)
			c, err := httpclient.New(dependency, required, unresolved,
				httpclient.WithDialContext(func(ctx context.Context, _, _ string) (net.Conn, error) {
					contexts <- ctx

					return nil, errBoom
				}))
			assert.NoError(t, err, "New must accept the options")

			_, err = fetch(t, c, http.MethodGet, "http://dependency.test/", http.NoBody, nil)
			assert.ErrorIs(t, err, errBoom, "Fetch must return the error of the dial")

			ctx := await(t, contexts, "the client must call the dial function")
			assert.ErrorIs(t, ctx.Err(), context.Canceled, "the context of the dial must end when the dial returns")
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
			assert.NoError(t, err, "New must accept the options")

			_, err = fetch(t, c, http.MethodGet, "http://dependency.test/", http.NoBody, nil)
			assert.ErrorIs(t, err, errBoom, "Fetch must return the error of the dial")
			assert.False(t, await(t, deadlines, "the client must call the dial function"),
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
			assert.NoError(t, err, "New must accept a nil dial function for a client of ReachPublic")

			_, err = fetch(t, c, http.MethodGet, srv.URL, http.NoBody, nil)
			assert.ErrorIs(t, err, httpclient.ErrBlocked, "the client's dialer must check the address")
		})
	})
}

// retrier returns a Retrier on clk with the number of attempts of the cases
// and a backoff below a nanosecond, so a retry waits only for the delay of
// a Retry-After header.
func retrier(t *testing.T, clk *fake.Clock) *resilience.Retrier {
	t.Helper()

	r, err := resilience.NewRetrier(resilience.RetryConfig{
		Clock:         clk,
		Rand:          pcg.New(1),
		Attempts:      attempts,
		Base:          1,
		Max:           1,
		MaxRetryAfter: time.Minute,
		MinRetries:    10,
		BudgetWindow:  time.Minute,
	})
	assert.NoError(t, err, "the retrier of the case must build")

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
	assert.NoError(t, err, "the breaker of the case must build")

	return b
}
