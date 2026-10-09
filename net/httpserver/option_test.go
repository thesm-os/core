// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package httpserver_test

import (
	"bufio"
	"cmp"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/net/httpserver"
)

// The fixture values of the cases of the options.
const (
	// limit is the timeout of the cases of the timeouts, short enough that
	// a case waits for it.
	limit = 50 * time.Millisecond

	// patience bounds every wait of a case: for a response, for a handler,
	// for Run to return and for the server to act on a timeout. It is far
	// above limit and above any correct wait, so a case fails where a
	// defect would leave it waiting.
	patience = 5 * time.Second

	// trusted is the trusted origin of the cases of the cross-origin
	// protection.
	trusted = "https://app.example.com"

	// The headers of a request of a browser that the protection reads.
	headerFetchSite = "Sec-Fetch-Site"
	headerOrigin    = "Origin"
	crossSite       = "cross-site"

	// headerOrder is the response header to which the middleware of a case
	// adds its name.
	headerOrder = "X-Order"
)

// errRead is sent by a handler of a case that read a body without an error.
var errRead = errors.New("the body read without an error")

func TestOption(t *testing.T) {
	t.Parallel()

	t.Run("Options", func(t *testing.T) {
		t.Parallel()

		t.Run("lets a later option override an earlier one", func(t *testing.T) {
			t.Parallel()
			_, err := httpserver.New(http.NotFoundHandler(), httpserver.Options(
				required, httpserver.WithAddr("localhost"), httpserver.WithAddr(loopback),
			))
			assert.NoError(t, err, "the later address must override the address that New refuses")
		})

		t.Run("makes New return ErrConfig for a nil option that it bundles", func(t *testing.T) {
			t.Parallel()
			_, err := httpserver.New(http.NotFoundHandler(), required, httpserver.Options(nil))
			assert.ErrorIs(t, err, httpserver.ErrConfig, "New must refuse the nil option")
		})
	})

	t.Run("WithTLS", func(t *testing.T) {
		t.Parallel()

		t.Run("serves HTTP/2 over TLS", func(t *testing.T) {
			t.Parallel()
			cfg, client := tlsPair(t)
			protos := make(chan int, 1)
			f := newFixture(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				protos <- r.ProtoMajor
			}), httpserver.WithTLS(cfg))

			req, err := http.NewRequestWithContext(
				t.Context(), http.MethodGet, "https"+strings.TrimPrefix(f.url, "http"), http.NoBody,
			)
			assert.NoError(t, err, "the request of the case must build")
			assert.Equal(t, do(t, client, req).status, http.StatusOK, "the request over TLS must succeed")
			assert.Equal(t, await(t, protos, "the handler must run"), 2, "the request must use HTTP/2")
		})
	})

	t.Run("WithReadHeaderTimeout", func(t *testing.T) {
		t.Parallel()

		t.Run("closes a connection whose headers arrive slower", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
				httpserver.WithReadHeaderTimeout(limit))
			conn := dial(t, f)

			_, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: test\r\n")
			assert.NoError(t, err, "the start of the headers must write")

			_, err = conn.Read(make([]byte, 1))
			assert.ErrorIs(t, err, io.EOF, "the server must close the connection without a response")
		})
	})

	t.Run("WithReadTimeout", func(t *testing.T) {
		t.Parallel()

		t.Run("fails the read of a body that arrives slower", func(t *testing.T) {
			t.Parallel()
			read := make(chan error, 1)
			f := newFixture(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				_, err := io.ReadAll(r.Body)
				read <- cmp.Or(err, errRead)
			}), httpserver.WithReadTimeout(limit))
			conn := dial(t, f)

			_, err := io.WriteString(conn, "POST / HTTP/1.1\r\nHost: test\r\nContent-Length: 10\r\n\r\nab")
			assert.NoError(t, err, "the request must write")

			got := awaitBefore(t, read, refused(conn), "the handler must read the body")
			ne := assert.ErrorAs[net.Error](t, got, "the read of the body must fail on the connection")
			assert.True(t, ne.Timeout(), "the read of the body must time out")
		})
	})

	t.Run("WithWriteTimeout", func(t *testing.T) {
		t.Parallel()

		t.Run("fails the write of a response that the client does not read", func(t *testing.T) {
			t.Parallel()
			wrote := make(chan error, 1)
			chunk := make([]byte, 64<<10)
			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				for {
					if _, err := w.Write(chunk); err != nil {
						wrote <- err

						return
					}
				}
			}), httpserver.WithWriteTimeout(limit))
			conn := dial(t, f)

			_, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: test\r\n\r\n")
			assert.NoError(t, err, "the request must write")

			got := awaitBefore(t, wrote, refused(conn), "the handler must write the response")
			ne := assert.ErrorAs[net.Error](t, got, "the write of the response must fail on the connection")
			assert.True(t, ne.Timeout(), "the write of the response must time out")
		})
	})

	t.Run("WithIdleTimeout", func(t *testing.T) {
		t.Parallel()

		t.Run("closes a keep-alive connection that idles longer", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
				httpserver.WithIdleTimeout(limit))
			conn := dial(t, f)

			_, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: test\r\n\r\n")
			assert.NoError(t, err, "the request must write")

			r := bufio.NewReader(conn)
			resp, err := http.ReadResponse(r, nil)
			assert.NoError(t, err, "the response must read")
			_, err = io.Copy(io.Discard, resp.Body)
			assert.NoError(t, err, "the body must read")
			assert.NoError(t, resp.Body.Close(), "the body must close")

			_, err = r.ReadByte()
			assert.ErrorIs(t, err, io.EOF, "the server must close the idle connection")
		})
	})

	t.Run("WithMaxHeaderBytes", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			opts   []httpserver.Option
			header int
			want   int
		}{
			{
				name:   "responds with 431 to headers beyond the limit",
				opts:   []httpserver.Option{httpserver.WithMaxHeaderBytes(1 << 10)},
				header: 8 << 10,
				want:   http.StatusRequestHeaderFieldsTooLarge,
			},
			{
				name:   "admits 60 KiB of headers without the option",
				header: 60 << 10,
				want:   http.StatusOK,
			},
			{
				name:   "responds with 431 to 72 KiB of headers without the option",
				header: 72 << 10,
				want:   http.StatusRequestHeaderFieldsTooLarge,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				f := newFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), tt.opts...)
				large := http.Header{"X-Large": {strings.Repeat("a", tt.header)}}
				got := f.send(t, http.MethodGet, "/", http.NoBody, large)
				assert.Equal(t, got.status, tt.want, "the server must apply the header limit")
			})
		}
	})

	t.Run("WithMaxBodyBytes", func(t *testing.T) {
		t.Parallel()

		eight := []httpserver.Option{httpserver.WithMaxBodyBytes(8)}
		off := []httpserver.Option{httpserver.WithMaxBodyBytes(-1)}

		tests := []struct {
			opts    []httpserver.Option
			name    string
			size    int
			want    int
			chunked bool
			served  bool
		}{
			{
				name: "responds with 413 to a declared body beyond the limit before the handler runs",
				opts: eight,
				size: 9,
				want: http.StatusRequestEntityTooLarge,
			},
			{
				name:   "admits a declared body of the limit",
				opts:   eight,
				size:   8,
				want:   http.StatusOK,
				served: true,
			},
			{
				name:    "responds with 413 through Error to a read of a chunked body beyond the limit",
				opts:    eight,
				size:    9,
				want:    http.StatusRequestEntityTooLarge,
				chunked: true,
				served:  true,
			},
			{
				name:    "admits a chunked body of the limit",
				opts:    eight,
				size:    8,
				want:    http.StatusOK,
				chunked: true,
				served:  true,
			},
			{
				name: "responds with 413 to a declared body beyond 4 MiB without the option",
				size: 4<<20 + 1,
				want: http.StatusRequestEntityTooLarge,
			},
			{
				name:   "admits a declared body beyond 4 MiB for a negative limit",
				opts:   off,
				size:   4<<20 + 1,
				want:   http.StatusOK,
				served: true,
			},
			{
				name:    "admits a chunked body beyond 4 MiB for a negative limit",
				opts:    off,
				size:    4<<20 + 1,
				want:    http.StatusOK,
				chunked: true,
				served:  true,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				var served atomic.Bool

				f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					served.Store(true)

					if _, err := io.ReadAll(r.Body); err != nil {
						httpserver.Error(w, r, err)
					}
				}), tt.opts...)

				// The client sends a reader of unknown length chunked.
				var payload io.Reader = strings.NewReader(strings.Repeat("a", tt.size))
				if tt.chunked {
					payload = io.MultiReader(payload)
				}

				expect.Equal(t, f.send(t, http.MethodPost, "/", payload, nil).status, tt.want,
					"the server must apply the body limit")
				expect.Equal(t, served.Load(), tt.served, "the handler must run for a body that the limit admits")
			})
		}

		t.Run("bounds the chunked body that a middleware reads", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
				httpserver.WithMaxBodyBytes(8),
				httpserver.WithMiddleware(func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if _, err := io.ReadAll(r.Body); err != nil {
							httpserver.Error(w, r, err)

							return
						}

						next.ServeHTTP(w, r)
					})
				}))

			got := f.send(t, http.MethodPost, "/", io.MultiReader(strings.NewReader(strings.Repeat("a", 9))), nil)
			assert.Equal(t, got.status, http.StatusRequestEntityTooLarge, "the middleware must read the bounded body")
		})
	})

	t.Run("WithMaxInFlight", func(t *testing.T) {
		t.Parallel()

		t.Run("responds with 503 to a request beyond the bound", func(t *testing.T) {
			t.Parallel()
			entered, gate := make(chan struct{}), make(chan struct{})
			release := sync.OnceFunc(func() { close(gate) })
			defer release()

			var first sync.Once

			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				first.Do(func() {
					close(entered)
					<-gate
				})

				_, _ = io.WriteString(w, body)
			}), httpserver.WithMaxInFlight(1))

			sent := make(chan error, 1)
			go func() { sent <- request(t, f.client, f.url) }()
			awaitBefore(t, entered, sent, "the handler must receive the first request")

			got := f.send(t, http.MethodGet, "/", http.NoBody, nil)
			expect.Equal(t, got.status, http.StatusServiceUnavailable,
				"the server must refuse a request beyond the bound")
			expect.Equal(t, got.header.Get("Retry-After"), "1", "the refusal must ask for a delay of 1 s")
			expect.Equal(t, got.header.Get("Content-Type"), problemJSON, "the refusal must be problem details")

			release()
			assert.NoError(t, await(t, sent, "the first request must end"), "the request within the bound must finish")
		})

		t.Run("frees the slot of a request that it refused", func(t *testing.T) {
			t.Parallel()
			entered, gate := make(chan struct{}), make(chan struct{})
			release := sync.OnceFunc(func() { close(gate) })
			defer release()

			var first sync.Once

			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				first.Do(func() {
					close(entered)
					<-gate
				})

				_, _ = io.WriteString(w, body)
			}), httpserver.WithMaxInFlight(1))

			sent := make(chan error, 1)
			go func() { sent <- request(t, f.client, f.url) }()
			awaitBefore(t, entered, sent, "the handler must receive the first request")
			assert.Equal(t, f.send(t, http.MethodGet, "/", http.NoBody, nil).status, http.StatusServiceUnavailable,
				"the server must refuse a request beyond the bound")

			release()
			assert.NoError(t, await(t, sent, "the first request must end"), "the request within the bound must finish")
			assert.Equal(t, f.send(t, http.MethodGet, "/", http.NoBody, nil).status, http.StatusOK,
				"the server must admit a request after the refusal")
		})
	})

	t.Run("WithTrustedOrigins", func(t *testing.T) {
		t.Parallel()

		t.Run("admits a cross-origin request of a browser from a trusted origin", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
				httpserver.WithTrustedOrigins(trusted))
			got := f.send(t, http.MethodPost, "/", http.NoBody,
				http.Header{headerFetchSite: {crossSite}, headerOrigin: {trusted}})
			assert.Equal(t, got.status, http.StatusOK, "the protection must admit a trusted origin")
		})
	})

	t.Run("WithMiddleware", func(t *testing.T) {
		t.Parallel()

		t.Run("runs the first middleware outermost", func(t *testing.T) {
			t.Parallel()
			named := func(name string) func(http.Handler) http.Handler {
				return func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Add(headerOrder, name)
						next.ServeHTTP(w, r)
					})
				}
			}

			f := newFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
				httpserver.WithMiddleware(named("first"), named("second")))
			got := f.send(t, http.MethodGet, "/", http.NoBody, nil)
			assert.Equal(t, got.header.Values(headerOrder), []string{"first", "second"},
				"the first middleware must run before the second")
		})
	})
}

// dial returns a TCP connection to f, which the cleanup of t closes, with a
// read deadline of patience.
func dial(t *testing.T, f *fixture) net.Conn {
	t.Helper()

	var d net.Dialer

	conn, err := d.DialContext(t.Context(), tcp, strings.TrimPrefix(f.url, "http://"))
	assert.NoError(t, err, "the connection must dial")
	t.Cleanup(func() { _ = conn.Close() })

	assert.NoError(t, conn.SetReadDeadline(time.Now().Add(patience)), "the read deadline must set")

	return conn
}

// refused returns a channel that receives the status of the first response
// on conn when that status is not 200, such as the refusal of a request
// before its handler runs. It reads the status line and the headers of the
// response and no more, so a handler that writes a long body still fills
// the connection. The read ends at the deadline of conn or when the cleanup
// of the case closes conn.
func refused(conn net.Conn) <-chan int {
	statuses := make(chan int, 1)

	go func() {
		// A Close of the body would read the rest of the response, which a
		// case of a write timeout leaves unread.
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil) //nolint:bodyclose // see above
		if err == nil && resp.StatusCode != http.StatusOK {
			statuses <- resp.StatusCode
		}
	}()

	return statuses
}
