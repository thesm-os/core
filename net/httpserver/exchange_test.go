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

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/net/httpserver"
	"go.thesmos.sh/core/net/internal/semconv"
	"go.thesmos.sh/core/telemetry"
)

// upgrade is the response of a handler of a case that takes over its
// connection.
const upgrade = "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: test\r\n\r\n"

// The attributes of the cases of Annotate.
var (
	tenant = telemetry.AttrString("tenant", "acme")
	plan   = telemetry.AttrString("plan", "enterprise")
)

func TestExchange(t *testing.T) {
	t.Parallel()

	t.Run("Annotate", func(t *testing.T) {
		t.Parallel()

		t.Run("adds the attributes to the telemetry of the request", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				httpserver.Annotate(r.Context(), tenant, plan)
			}))
			f.send(t, http.MethodGet, "/", http.NoBody, nil)
			assert.NoError(t, f.stop(), "Run must drain")

			r := f.logs.find(messageRequest)[0]
			expect.Equal(t, value(t, &r, tenant.Key).String(), tenant.Value.Str,
				"the record must contain the first attribute")
			expect.Equal(t, value(t, &r, plan.Key).String(), plan.Value.Str,
				"the record must contain the second attribute")

			calls := f.reporter.started()[0].OnSetAttributes.Calls()
			assert.NotEmpty(t, calls, "the span must receive the attributes")
			expect.Equal(t, calls[0].Attrs, []telemetry.Attr{tenant, plan}, "the span must receive both attributes")
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
			assert.NoError(t, f.stop(), "Run must drain")

			r := f.logs.find(messageRequest)[0]
			for _, k := range keys {
				expect.Equal(t, value(t, &r, k).String(), k, "the record must contain the attribute "+k)
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
			assert.NoError(t, f.stop(), "Run must drain")

			r := f.logs.find(messageRequest)[0]
			for _, k := range keys {
				expect.Equal(t, value(t, &r, k).String(), k, "the record must contain the attribute "+k)
			}
		})

		t.Run("adds nothing after the server finished the request", func(t *testing.T) {
			t.Parallel()
			contexts := make(chan context.Context, 1)
			f := newFixture(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				contexts <- r.Context()
			}))
			assert.Equal(t, f.send(t, http.MethodGet, "/", http.NoBody, nil).status, http.StatusOK,
				"the request must succeed")
			assert.NoError(t, f.stop(), "Run must drain")

			httpserver.Annotate(await(t, contexts, "the handler must run"), tenant)
			f.reporter.started()[0].OnSetAttributes.AssertCalledOnce(t,
				"the span must receive only the attributes of the request")
		})

		t.Run("adds no attributes to a span without an identity", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				httpserver.Annotate(r.Context(), tenant)
			}))
			f.reporter.mu.Lock()
			f.reporter.anonymous = true
			f.reporter.mu.Unlock()

			f.send(t, http.MethodGet, "/", http.NoBody, nil)
			assert.NoError(t, f.stop(), "Run must drain")

			spans := f.reporter.started()
			assert.Length(t, spans, 1, "the server must start one span")
			spans[0].OnSetAttributes.AssertNotCalled(t, "a span without an identity must receive no attributes")
		})

		t.Run("does nothing for a context of no request of a Server", func(t *testing.T) {
			t.Parallel()
			assert.NotPanics(t, func() { httpserver.Annotate(t.Context(), tenant) },
				"Annotate must ignore a context of no request")
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
			{
				name:  "records 101 without a takeover of the connection",
				codes: []int{http.StatusSwitchingProtocols},
				want:  http.StatusSwitchingProtocols,
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
				assert.Equal(t, f.send(t, http.MethodGet, "/", http.NoBody, nil).status, tt.want,
					"the client must receive the status")
				assert.NoError(t, f.stop(), "Run must drain")

				got := value(t, &f.logs.find(messageRequest)[0], semconv.ResponseStatusCode).Int64()
				assert.Equal(t, got, int64(tt.want), "the record must contain the status")
			})
		}

		t.Run("aborts the response for a status below 100", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusContinue - 1)
			}))

			assert.HasError(t, request(t, f.client, f.url), "the client must not receive a response")
		})
	})

	t.Run("Write", func(t *testing.T) {
		t.Parallel()

		t.Run("records 200 for a body without a header", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			assert.Equal(t, f.send(t, http.MethodGet, "/", http.NoBody, nil).body, body,
				"the client must receive the body")
			assert.NoError(t, f.stop(), "Run must drain")

			r := f.logs.find(messageRequest)[0]
			assert.Equal(t, value(t, &r, semconv.ResponseStatusCode).Int64(), int64(http.StatusOK),
				"the record must contain the status 200")
		})

		t.Run("counts the bytes of every write", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
				_, _ = w.Write([]byte(body))
			}))
			assert.Equal(t, f.send(t, http.MethodGet, "/", http.NoBody, nil).body, body+body,
				"the client must receive both writes")
			assert.NoError(t, f.stop(), "Run must drain")

			r := f.logs.find(messageRequest)[0]
			assert.Equal(t, value(t, &r, keyBodySize).Int64(), int64(2*len(body)),
				"the record must contain the bytes of both writes")
		})
	})

	t.Run("WriteString", func(t *testing.T) {
		t.Parallel()

		t.Run("records 200 for a string without a header", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.(io.StringWriter).WriteString(body)
			}))
			assert.Equal(t, f.send(t, http.MethodGet, "/", http.NoBody, nil).body, body,
				"the client must receive the body")
			assert.NoError(t, f.stop(), "Run must drain")

			r := f.logs.find(messageRequest)[0]
			assert.Equal(t, value(t, &r, semconv.ResponseStatusCode).Int64(), int64(http.StatusOK),
				"the record must contain the status 200")
		})

		t.Run("counts the bytes of a string", func(t *testing.T) {
			t.Parallel()
			wrote := make(chan int, 1)
			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				n, _ := w.(io.StringWriter).WriteString(body)
				wrote <- n
			}))
			assert.Equal(t, f.send(t, http.MethodGet, "/", http.NoBody, nil).body, body,
				"the client must receive the body")
			expect.Equal(t, await(t, wrote, "the handler must write"), len(body), "WriteString must return the bytes")
			assert.NoError(t, f.stop(), "Run must drain")

			r := f.logs.find(messageRequest)[0]
			expect.Equal(t, value(t, &r, keyBodySize).Int64(), int64(len(body)), "the record must contain the bytes")
		})
	})

	t.Run("ReadFrom", func(t *testing.T) {
		t.Parallel()

		t.Run("records 200 for a copy without a header", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.(io.ReaderFrom).ReadFrom(strings.NewReader(body))
			}))
			assert.Equal(t, f.send(t, http.MethodGet, "/", http.NoBody, nil).body, body,
				"the client must receive the body")
			assert.NoError(t, f.stop(), "Run must drain")

			r := f.logs.find(messageRequest)[0]
			assert.Equal(t, value(t, &r, semconv.ResponseStatusCode).Int64(), int64(http.StatusOK),
				"the record must contain the status 200")
		})

		t.Run("counts the bytes that it copies", func(t *testing.T) {
			t.Parallel()
			copied := make(chan int64, 1)
			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				n, _ := w.(io.ReaderFrom).ReadFrom(strings.NewReader(body))
				copied <- n
			}))
			assert.Equal(t, f.send(t, http.MethodGet, "/", http.NoBody, nil).body, body,
				"the client must receive the body")
			expect.Equal(t, await(t, copied, "the handler must copy"), int64(len(body)),
				"ReadFrom must return the bytes")
			assert.NoError(t, f.stop(), "Run must drain")

			r := f.logs.find(messageRequest)[0]
			expect.Equal(t, value(t, &r, keyBodySize).Int64(), int64(len(body)), "the record must contain the bytes")
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
			assert.NoError(t, err, "the request must build")

			resp, err := f.client.Do(req)
			assert.NoError(t, err, "the response must arrive")

			defer resp.Body.Close()

			got := make([]byte, len(body))
			_, err = io.ReadFull(resp.Body, got)
			release()
			assert.NoError(t, err, "the flushed body must read while the handler waits")
			assert.Equal(t, string(got), body, "the client must receive the flushed body")
		})

		t.Run("records 200 for a flush without a header", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.(http.Flusher).Flush()
			}))
			assert.Equal(t, f.send(t, http.MethodGet, "/", http.NoBody, nil).status, http.StatusOK,
				"the client must receive the status 200")
			assert.NoError(t, f.stop(), "Run must drain")

			got := value(t, &f.logs.find(messageRequest)[0], semconv.ResponseStatusCode).Int64()
			assert.Equal(t, got, int64(http.StatusOK), "the record must contain the status 200")
		})
	})

	t.Run("Hijack", func(t *testing.T) {
		t.Parallel()

		t.Run("records 101 for a connection of HTTP/1.1 that it takes over", func(t *testing.T) {
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
			assert.NoError(t, err, "the request must write")

			resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
			assert.NoError(t, err, "the response of the connection must read")
			assert.NoError(t, resp.Body.Close(), "the body must close")
			assert.Equal(t, resp.StatusCode, http.StatusSwitchingProtocols, "the handler must write the upgrade")

			// Shutdown does not wait for a hijacked connection, so the case
			// waits for the record that the chain writes when the handler
			// returns.
			r := await(t, l.requests, "the server must log the request")
			got := value(t, &r, semconv.ResponseStatusCode).Int64()
			assert.Equal(t, got, int64(http.StatusSwitchingProtocols), "the record must contain the status 101")
			assert.NoError(t, f.stop(), "Run must drain")
		})

		t.Run("keeps the status that the handler wrote before it takes over the connection", func(t *testing.T) {
			t.Parallel()
			l := &logs{level: slog.LevelDebug, requests: make(chan slog.Record, 1)}
			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)

				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					return
				}

				_ = conn.Close()
			}), httpserver.WithLogger(slog.New(l)))
			conn := dial(t, f)

			_, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: test\r\n\r\n")
			assert.NoError(t, err, "the request must write")

			r := await(t, l.requests, "the server must log the request")
			got := value(t, &r, semconv.ResponseStatusCode).Int64()
			assert.Equal(t, got, int64(http.StatusOK), "the record must contain the status that the handler wrote")
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
			assert.NoError(t, err, "the request of the case must build")
			assert.Equal(t, do(t, client, req).status, http.StatusOK, "the request of HTTP/2 must succeed")
			assert.ErrorIs(t, await(t, hijacked, "the handler must run"), http.ErrNotSupported,
				"Hijack must refuse a stream of HTTP/2")
		})

		t.Run("records 200 for a stream of HTTP/2 that it cannot take over", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _, _ = w.(http.Hijacker).Hijack()
			}))

			var p http.Protocols
			p.SetUnencryptedHTTP2(true)
			client := &http.Client{Transport: &http.Transport{Protocols: &p}, Timeout: patience}
			defer client.CloseIdleConnections()

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, f.url, http.NoBody)
			assert.NoError(t, err, "the request of the case must build")
			assert.Equal(t, do(t, client, req).status, http.StatusOK, "the request of HTTP/2 must succeed")
			assert.NoError(t, f.stop(), "Run must drain")

			records := f.logs.find(messageRequest)
			assert.Length(t, records, 1, "the server must write one record")
			assert.Equal(t, value(t, &records[0], semconv.ResponseStatusCode).Int64(), int64(http.StatusOK),
				"the record must contain the status that net/http sends")
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
			assert.Equal(t, f.send(t, http.MethodGet, "/", http.NoBody, nil).status, http.StatusOK,
				"the request must succeed")
			assert.NoError(t, await(t, set, "the handler must run"),
				"SetWriteDeadline must set the deadline of the connection")
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
			assert.Equal(t, f.send(t, http.MethodGet, "/", http.NoBody, nil).status, http.StatusOK,
				"the request must succeed")

			_, ok := await(t, servers, "the handler must run").(*http.Server)
			assert.True(t, ok, "the context must return the http.Server of the request")
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
			expect.ErrorIs(t, await(t, ended, "the context must end"), context.Canceled,
				"the context of the request must end")
			expect.ErrorIs(t, await(t, sent, "the request must end"), context.Canceled,
				"the client must report its cancellation")
		})
	})
}

// TestExchangeAllocs checks the allocation contract of Annotate for a
// context of no request. MaxAllocs counts the allocations of the whole
// process, so the test does not run in parallel.
//
//nolint:paralleltest // see above
func TestExchangeAllocs(t *testing.T) {
	t.Run("Annotate", func(t *testing.T) {
		t.Run("of a context of no request", func(t *testing.T) {
			ctx := t.Context()
			expect.MaxAllocs(t, func() { httpserver.Annotate(ctx, tenant) }, 0,
				"Annotate must not allocate for a context of no request")
		})
	})
}

// BenchmarkExchange reports the cost of Annotate for a context of no
// request, and fails when it allocates.
func BenchmarkExchange(b *testing.B) {
	b.Run("Annotate", func(b *testing.B) {
		b.Run("of a context of no request", func(b *testing.B) {
			ctx := b.Context()

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				httpserver.Annotate(ctx, tenant)
			}
		})
	})
}
