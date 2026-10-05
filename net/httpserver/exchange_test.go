// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package httpserver_test

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"go.dokimi.dev/assert/bench"
	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/net/httpserver"
	"go.thesmos.sh/core/net/internal/semconv"
	"go.thesmos.sh/core/telemetry"
)

// The attributes of the cases of Annotate.
var (
	tenant = telemetry.AttrString("tenant", "acme")
	plan   = telemetry.AttrString("plan", "enterprise")
)

// upgrade is the response of a handler of a case that takes over its
// connection.
const upgrade = "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: test\r\n\r\n"

func TestExchange(t *testing.T) {
	t.Parallel()

	t.Run("Annotate", func(t *testing.T) {
		t.Parallel()

		t.Run("adds the attributes to the log record and to the span of the request", func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				httpserver.Annotate(r.Context(), tenant, plan)
			}))
			f.send(t, http.MethodGet, "/", http.NoBody, nil)
			testkit.NoError(t, f.stop(), "Run must drain")

			r := f.logs.find(messageRequest)[0]
			testkit.Equal(t, value(t, &r, tenant.Key).String(), tenant.Value.Str, "the first attribute")
			testkit.Equal(t, value(t, &r, plan.Key).String(), plan.Value.Str, "the second attribute")

			calls := f.reporter.started()[0].OnSetAttributes.Calls()
			testkit.Equal(t, calls[0].Attrs, []telemetry.Attr{tenant, plan}, "the attributes of the span")
		})

		t.Run("adds the attributes beyond the first four", func(t *testing.T) {
			t.Parallel()

			keys := []string{"a", "b", "c", "d", "e", "f"}
			f := newFixture(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				for _, k := range keys {
					httpserver.Annotate(r.Context(), telemetry.AttrString(k, k))
				}
			}))
			f.send(t, http.MethodGet, "/", http.NoBody, nil)
			testkit.NoError(t, f.stop(), "Run must drain")

			r := f.logs.find(messageRequest)[0]
			for _, k := range keys {
				testkit.Equal(t, value(t, &r, k).String(), k, "the attribute "+k)
			}
		})

		t.Run("is safe for concurrent use by goroutines of the handler", func(t *testing.T) {
			t.Parallel()

			keys := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
			f := newFixture(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				ctx := r.Context()

				var wg sync.WaitGroup
				for _, k := range keys {
					wg.Go(func() { httpserver.Annotate(ctx, telemetry.AttrString(k, k)) })
				}
				wg.Wait()
			}))
			f.send(t, http.MethodGet, "/", http.NoBody, nil)
			testkit.NoError(t, f.stop(), "Run must drain")

			r := f.logs.find(messageRequest)[0]
			for _, k := range keys {
				testkit.Equal(t, value(t, &r, k).String(), k, "the attribute "+k)
			}
		})

		t.Run("adds nothing after the server finished the request", func(t *testing.T) {
			t.Parallel()

			contexts := make(chan context.Context, 1)
			f := newFixture(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				contexts <- r.Context()
			}))
			testkit.Equal(t, f.send(t, http.MethodGet, "/", http.NoBody, nil).status, http.StatusOK, "the status")
			testkit.NoError(t, f.stop(), "Run must drain")

			httpserver.Annotate(await(t, contexts, "the handler must run"), tenant)
			f.reporter.started()[0].OnSetAttributes.AssertCalledOnce(t,
				"the span must receive only the attributes of the request")
		})

		t.Run("does nothing for a context of no request of a Server", func(t *testing.T) {
			t.Parallel()

			httpserver.Annotate(t.Context(), tenant)
		})
	})

	t.Run("WriteHeader", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name  string
			codes []int
			want  int
		}{
			{name: "records the status of the response", codes: []int{http.StatusCreated}, want: http.StatusCreated},
			{
				name:  "records the status after 100 Continue",
				codes: []int{http.StatusContinue, http.StatusNoContent},
				want:  http.StatusNoContent,
			},
			{
				name:  "records the status after 103 Early Hints",
				codes: []int{http.StatusEarlyHints, http.StatusNoContent},
				want:  http.StatusNoContent,
			},
			{
				name:  "records the first status of two",
				codes: []int{http.StatusAccepted, http.StatusInternalServerError},
				want:  http.StatusAccepted,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					for _, code := range tt.codes {
						w.WriteHeader(code)
					}
				}))
				testkit.Equal(t, f.send(t, http.MethodGet, "/", http.NoBody, nil).status, tt.want, "the status")
				testkit.NoError(t, f.stop(), "Run must drain")

				got := value(t, &f.logs.find(messageRequest)[0], semconv.ResponseStatusCode).Int64()
				testkit.Equal(t, got, int64(tt.want), "the status of the record")
			})
		}
	})

	t.Run("Write", func(t *testing.T) {
		t.Parallel()

		t.Run("records 200 and counts the bytes of a body without a header", func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
				_, _ = w.Write([]byte(body))
			}))
			testkit.Equal(t, f.send(t, http.MethodGet, "/", http.NoBody, nil).body, body+body, "the body")
			testkit.NoError(t, f.stop(), "Run must drain")

			r := f.logs.find(messageRequest)[0]
			testkit.Equal(t, value(t, &r, semconv.ResponseStatusCode).Int64(), int64(http.StatusOK), "the status")
			testkit.Equal(t, value(t, &r, keyBodySize).Int64(), int64(2*len(body)), "the bytes of the body")
		})
	})

	t.Run("WriteString", func(t *testing.T) {
		t.Parallel()

		t.Run("records 200 and counts the bytes of a string", func(t *testing.T) {
			t.Parallel()

			wrote := make(chan int, 1)
			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				n, _ := w.(io.StringWriter).WriteString(body)
				wrote <- n
			}))
			testkit.Equal(t, f.send(t, http.MethodGet, "/", http.NoBody, nil).body, body, "the body")
			testkit.Equal(t, await(t, wrote, "the handler must write"), len(body), "the bytes that WriteString returns")
			testkit.NoError(t, f.stop(), "Run must drain")

			r := f.logs.find(messageRequest)[0]
			testkit.Equal(t, value(t, &r, semconv.ResponseStatusCode).Int64(), int64(http.StatusOK), "the status")
			testkit.Equal(t, value(t, &r, keyBodySize).Int64(), int64(len(body)), "the bytes of the body")
		})
	})

	t.Run("ReadFrom", func(t *testing.T) {
		t.Parallel()

		t.Run("records 200 and counts the bytes that it copies", func(t *testing.T) {
			t.Parallel()

			copied := make(chan int64, 1)
			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				n, _ := w.(io.ReaderFrom).ReadFrom(strings.NewReader(body))
				copied <- n
			}))
			testkit.Equal(t, f.send(t, http.MethodGet, "/", http.NoBody, nil).body, body, "the body")
			testkit.Equal(
				t,
				await(t, copied, "the handler must copy"),
				int64(len(body)),
				"the bytes that ReadFrom returns",
			)
			testkit.NoError(t, f.stop(), "Run must drain")

			r := f.logs.find(messageRequest)[0]
			testkit.Equal(t, value(t, &r, semconv.ResponseStatusCode).Int64(), int64(http.StatusOK), "the status")
			testkit.Equal(t, value(t, &r, keyBodySize).Int64(), int64(len(body)), "the bytes of the body")
		})
	})

	t.Run("Flush", func(t *testing.T) {
		t.Parallel()

		t.Run("sends the body before the handler returns", func(t *testing.T) {
			t.Parallel()

			gate := make(chan struct{})
			release := sync.OnceFunc(func() { close(gate) })
			defer release()

			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, body)
				w.(http.Flusher).Flush()
				<-gate
			}))

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, f.url, http.NoBody)
			testkit.NoError(t, err, "the request must build")

			resp, err := f.client.Do(req)
			testkit.NoError(t, err, "the response must arrive")

			defer resp.Body.Close()

			got := make([]byte, len(body))
			_, err = io.ReadFull(resp.Body, got)
			release()
			testkit.NoError(t, err, "the flushed body must read while the handler waits")
			testkit.Equal(t, string(got), body, "the flushed body")
		})

		t.Run("records 200 for a flush without a header", func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.(http.Flusher).Flush()
			}))
			testkit.Equal(t, f.send(t, http.MethodGet, "/", http.NoBody, nil).status, http.StatusOK, "the status")
			testkit.NoError(t, f.stop(), "Run must drain")

			got := value(t, &f.logs.find(messageRequest)[0], semconv.ResponseStatusCode).Int64()
			testkit.Equal(t, got, int64(http.StatusOK), "the status of the record")
		})
	})

	t.Run("Hijack", func(t *testing.T) {
		t.Parallel()

		t.Run("takes over the connection of HTTP/1.1 and records 101", func(t *testing.T) {
			t.Parallel()

			l := &logs{level: slog.LevelDebug, requests: make(chan slog.Record, 1)}
			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				conn, rw, err := w.(http.Hijacker).Hijack()
				if err != nil {
					return
				}

				defer conn.Close()

				_, _ = rw.WriteString(upgrade)
				_ = rw.Flush()
			}), httpserver.WithLogger(slog.New(l)))
			conn := dial(t, f)

			_, err := io.WriteString(conn,
				"GET / HTTP/1.1\r\nHost: test\r\nConnection: Upgrade\r\nUpgrade: test\r\n\r\n")
			testkit.NoError(t, err, "the request must write")

			resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
			testkit.NoError(t, err, "the response of the connection must read")
			testkit.NoError(t, resp.Body.Close(), "the body must close")
			testkit.Equal(t, resp.StatusCode, http.StatusSwitchingProtocols, "the status")

			// Shutdown does not wait for a hijacked connection, so the case
			// waits for the record that the chain writes when the handler
			// returns.
			r := await(t, l.requests, "the server must log the request")
			got := value(t, &r, semconv.ResponseStatusCode).Int64()
			testkit.Equal(t, got, int64(http.StatusSwitchingProtocols), "the status of the record")
			testkit.NoError(t, f.stop(), "Run must drain")
		})

		t.Run("returns http.ErrNotSupported over HTTP/2", func(t *testing.T) {
			t.Parallel()

			hijacked := make(chan error, 1)
			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _, err := w.(http.Hijacker).Hijack()
				hijacked <- err
			}))

			var p http.Protocols
			p.SetUnencryptedHTTP2(true)
			client := &http.Client{Transport: &http.Transport{Protocols: &p}, Timeout: patience}
			defer client.CloseIdleConnections()

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, f.url, http.NoBody)
			testkit.NoError(t, err, "the request of the case must build")
			testkit.Equal(t, do(t, client, req).status, http.StatusOK, "the status of the request of HTTP/2")
			testkit.ErrorIs(t, await(t, hijacked, "the handler must run"), http.ErrNotSupported, "Hijack over HTTP/2")
		})
	})

	t.Run("Unwrap", func(t *testing.T) {
		t.Parallel()

		t.Run("lets http.ResponseController set the write deadline of the connection", func(t *testing.T) {
			t.Parallel()

			set := make(chan error, 1)
			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				set <- http.NewResponseController(w).SetWriteDeadline(time.Now().Add(time.Minute))
			}))
			testkit.Equal(t, f.send(t, http.MethodGet, "/", http.NoBody, nil).status, http.StatusOK, "the status")
			testkit.NoError(
				t,
				await(t, set, "the handler must run"),
				"SetWriteDeadline must set the deadline of the connection",
			)
		})
	})

	t.Run("Value", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the values of the context of net/http", func(t *testing.T) {
			t.Parallel()

			servers := make(chan any, 1)
			f := newFixture(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				servers <- r.Context().Value(http.ServerContextKey)
			}))
			testkit.Equal(t, f.send(t, http.MethodGet, "/", http.NoBody, nil).status, http.StatusOK, "the status")

			_, ok := await(t, servers, "the handler must run").(*http.Server)
			testkit.True(t, ok, "the context must return the http.Server of the request")
		})

		t.Run("ends with the context of net/http when the client goes away", func(t *testing.T) {
			t.Parallel()

			entered, ended := make(chan struct{}), make(chan error, 1)
			f := newFixture(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				close(entered)
				<-r.Context().Done()
				ended <- r.Context().Err()
			}))

			ctx, cancel := context.WithCancel(t.Context())
			sent := make(chan error, 1)
			go func() {
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url, http.NoBody)
				if err == nil {
					_, err = f.client.Do(req) //nolint:bodyclose // the request is cancelled before a response
				}
				sent <- err
			}()

			awaitBefore(t, entered, sent, "the handler must receive the request")
			cancel()
			testkit.ErrorIs(t, await(t, ended, "the context must end"), context.Canceled,
				"the context of the request must end")
			testkit.ErrorIs(t, await(t, sent, "the request must end"), context.Canceled,
				"the client must report its cancellation")
		})
	})
}

func BenchmarkExchange(b *testing.B) {
	b.Run("Annotate of a context of no request", func(b *testing.B) {
		ctx := b.Context()

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			httpserver.Annotate(ctx, tenant)
		}
	})
}
