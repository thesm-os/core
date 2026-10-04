// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package httpserver_test

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/net/httpserver"
	"go.thesmos.sh/core/net/internal/semconv"
	"go.thesmos.sh/core/telemetry"
)

// The fixture values of the cases of the chain.
const (
	// itemPattern, itemPath and itemRoute are a ServeMux pattern, a path
	// that it matches, and the route that the server records for it.
	itemPattern = "GET /items/{id}"
	itemPath    = "/items/7"
	itemRoute   = "/items/{id}"

	// traceparent is the W3C context of the caller of a case, and
	// callerTrace and callerSpan its identity.
	traceparent = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
	callerTrace = "0af7651916cd43dd8448eb211c80319c"
	callerSpan  = "b7ad6b7169203331"

	// problemJSON is the media type of the problem details of RFC 9457.
	problemJSON = "application/problem+json"

	// elapsed is the time that the handler of a case spends on the fake
	// clock.
	elapsed = 250 * time.Millisecond
)

func TestChain(t *testing.T) {
	t.Parallel()

	t.Run("Recovery", func(t *testing.T) {
		t.Parallel()

		t.Run("writes 500 problem details for a panic before the handler wrote a header", func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				panic(errBoom) //nolint:forbidigo // the case tests the recovery of a panic
			}))

			got := f.send(t, http.MethodGet, "/", http.NoBody, nil)
			testkit.Equal(t, got.status, http.StatusInternalServerError, "the status")
			testkit.Equal(t, got.header.Get("Content-Type"), problemJSON, "the type of the body")
			testkit.Equal(t, got.body, `{"type":"about:blank","title":"Internal Server Error","status":500}`,
				"the body")

			testkit.NoError(t, f.stop(), "Run must drain")
			records := f.logs.find(messageRequest)
			testkit.Len(t, records, 1, "the server must write one record")
			testkit.Equal(t, records[0].Level, slog.LevelError, "the level of the record")
			recovered, _ := value(t, &records[0], keyPanic).Any().(error)
			testkit.ErrorIs(t, recovered, errBoom, "the value of the panic")
			testkit.Contains(t, value(t, &records[0], keyStack).String(), "TestChain", "the stack of the panic")

			end := f.reporter.started()[0].OnEnd.AssertCalledOnce(t, "the span must end once")
			testkit.Contains(t, end.Err.Error(), "panicked", "the span must end with the error of a panic")
		})

		t.Run("aborts the response for a panic after the handler wrote the body", func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, "partial")
				w.(http.Flusher).Flush()
				panic(errBoom) //nolint:forbidigo // the case tests the recovery of a panic
			}))

			testkit.Error(t, request(t, f.client, f.url), "the client must not read a partial body as complete")

			testkit.NoError(t, f.stop(), "Run must drain")
			records := f.logs.find(messageRequest)
			testkit.Len(t, records, 1, "the server must write one record")
			status := value(t, &records[0], semconv.ResponseStatusCode).Int64()
			testkit.Equal(t, status, int64(http.StatusOK), "the status")
			recovered, _ := value(t, &records[0], keyPanic).Any().(error)
			testkit.ErrorIs(t, recovered, errBoom, "the value of the panic")
		})

		t.Run("aborts the response for a panic after WriteHeader", func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				panic(errBoom) //nolint:forbidigo // the case tests the recovery of a panic
			}))

			testkit.Error(t, request(t, f.client, f.url), "the client must not receive a response")
		})

		t.Run("aborts the response without a stack for a panic with http.ErrAbortHandler", func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, "partial")
				w.(http.Flusher).Flush()
				panic(http.ErrAbortHandler) //nolint:forbidigo // the case tests the recovery of a panic
			}))

			testkit.Error(t, request(t, f.client, f.url), "the client must not read a partial body as complete")

			testkit.NoError(t, f.stop(), "Run must drain")
			records := f.logs.find(messageRequest)
			testkit.Len(t, records, 1, "the server must write one record")
			records[0].Attrs(func(a slog.Attr) bool {
				testkit.True(t, a.Key != keyPanic && a.Key != keyStack, "the record of an abort must contain no panic")

				return true
			})
		})

		t.Run("records 500 for a panic with http.ErrAbortHandler before a header", func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				panic(http.ErrAbortHandler) //nolint:forbidigo // the case tests the recovery of a panic
			}))

			testkit.Error(t, request(t, f.client, f.url), "the client must not receive a response")

			testkit.NoError(t, f.stop(), "Run must drain")
			records := f.logs.find(messageRequest)
			testkit.Len(t, records, 1, "the server must write one record")
			testkit.Equal(t, value(t, &records[0], semconv.ResponseStatusCode).Int64(),
				int64(http.StatusInternalServerError), "the status")
		})
	})

	t.Run("Telemetry", func(t *testing.T) {
		t.Parallel()

		t.Run("starts a server span named by the method", func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			testkit.Equal(t, f.send(t, http.MethodGet, "/", http.NoBody, nil).status, http.StatusOK, "the status")

			spans := f.reporter.started()
			testkit.Len(t, spans, 1, "the server must start one span")
			testkit.Equal(t, spans[0].name, telemetry.SpanName(http.MethodGet), "the name of the span")
			testkit.Equal(t, telemetry.ApplySpanOptions(spans[0].opts), telemetry.SpanKindServer, "the kind")

			_, remote := telemetry.RemoteParent(spans[0].opts)
			testkit.False(t, remote, "a request without a trace must start a new trace")
		})

		t.Run("starts the span as the child of the trace of the caller", func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			got := f.send(t, http.MethodGet, "/", http.NoBody, http.Header{"Traceparent": {traceparent}})
			testkit.Equal(t, got.status, http.StatusOK, "the status")

			parent, remote := telemetry.RemoteParent(f.reporter.started()[0].opts)
			testkit.True(t, remote, "the span must have the remote parent of the caller")
			testkit.Equal(t, parent, telemetry.SpanContext{TraceID: callerTrace, SpanID: callerSpan, Sampled: true},
				"the parent")
		})

		t.Run("names the span _OTHER for a method outside RFC 9110", func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			testkit.Equal(t, f.send(t, "PROPFIND", "/", http.NoBody, nil).status, http.StatusOK, "the status")
			testkit.Equal(t, f.reporter.started()[0].name, telemetry.SpanName(semconv.OtherMethod), "the name")
		})

		t.Run("sets the attributes of the request on a span with an identity", func(t *testing.T) {
			t.Parallel()

			mux := http.NewServeMux()
			mux.HandleFunc(itemPattern, func(http.ResponseWriter, *http.Request) {})
			f := newFixture(t, mux)
			testkit.Equal(t, f.send(t, http.MethodGet, itemPath, http.NoBody, nil).status, http.StatusOK, "the status")

			call := f.reporter.started()[0].OnSetAttributes.AssertCalledOnce(t, "the span must receive attributes")
			testkit.Equal(t, call.Attrs, []telemetry.Attr{
				telemetry.AttrString(semconv.RequestMethod, http.MethodGet),
				telemetry.AttrInt(semconv.ResponseStatusCode, http.StatusOK),
				telemetry.AttrString(semconv.URLScheme, "http"),
				telemetry.AttrString(semconv.Route, itemRoute),
			}, "the attributes")
		})

		t.Run("sets no attributes on a span without an identity", func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			f.reporter.mu.Lock()
			f.reporter.anonymous = true
			f.reporter.mu.Unlock()

			testkit.Equal(t, f.send(t, http.MethodGet, "/", http.NoBody, nil).status, http.StatusOK, "the status")
			testkit.NoError(t, f.stop(), "Run must drain")

			span := f.reporter.started()[0]
			span.OnSetAttributes.AssertNotCalled(t, "a span without an identity must receive no attributes")
			span.OnEnd.AssertCalledOnce(t, "the span must end once")
		})

		tests := []struct {
			handler http.HandlerFunc
			name    string
			want    string
		}{
			{
				name: "ends the span without an error for a status below 500",
				handler: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNotFound)
				},
			},
			{
				name: "ends the span without an error for a status below 500 that Error wrote",
				handler: func(w http.ResponseWriter, r *http.Request) {
					httpserver.Error(w, r, errs.WithClass(errBoom, errs.Invalid))
				},
			},
			{
				name: "ends the span with the error of Error for a status of 500 and above",
				handler: func(w http.ResponseWriter, r *http.Request) {
					httpserver.Error(w, r, errs.WithClass(errBoom, errs.Transient))
				},
				want: errBoom.Error(),
			},
			{
				name: "ends the span with an error for a status of 500 that Error did not write",
				handler: func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
				},
				want: "httpserver: the response has a server error status",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				f := newFixture(t, tt.handler)
				f.send(t, http.MethodGet, "/", http.NoBody, nil)
				testkit.NoError(t, f.stop(), "Run must drain")

				end := f.reporter.started()[0].OnEnd.AssertCalledOnce(t, "the span must end once")
				if tt.want == "" {
					testkit.NoError(t, end.Err, "the span must end without an error")

					return
				}

				testkit.Equal(t, end.Err.Error(), tt.want, "the error of the span")
			})
		}

		t.Run("records the duration in seconds into the set of the request", func(t *testing.T) {
			t.Parallel()

			clk := fake.New(origin)
			mux := http.NewServeMux()
			mux.HandleFunc(itemPattern, func(http.ResponseWriter, *http.Request) { clk.Advance(elapsed) })
			f := newFixture(t, mux, httpserver.WithClock(clk))
			testkit.Equal(t, f.send(t, http.MethodGet, itemPath, http.NoBody, nil).status, http.StatusOK, "the status")

			sets := f.reporter.bound()
			testkit.Len(t, sets, 1, "the server must bind one set")
			testkit.Equal(t, sets[0].attrs, []telemetry.Attr{
				telemetry.AttrString(semconv.RequestMethod, http.MethodGet),
				telemetry.AttrInt(semconv.ResponseStatusCode, http.StatusOK),
				telemetry.AttrString(semconv.URLScheme, "http"),
				telemetry.AttrString(semconv.Route, itemRoute),
			}, "the attributes of the set")
			testkit.Equal(t, sets[0].OnRecord.AssertCalledOnce(t, "the set must record once").Value, elapsed.Seconds(),
				"the duration in seconds")
		})

		t.Run("records the set of a request that matched no route without the route", func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, http.NewServeMux())
			got := f.send(t, "PROPFIND", "/missing", http.NoBody, nil)
			testkit.Equal(t, got.status, http.StatusNotFound, "the status")

			testkit.Equal(t, f.reporter.bound()[0].attrs, []telemetry.Attr{
				telemetry.AttrString(semconv.RequestMethod, semconv.OtherMethod),
				telemetry.AttrInt(semconv.ResponseStatusCode, http.StatusNotFound),
				telemetry.AttrString(semconv.URLScheme, "http"),
			}, "the attributes of the set")
		})

		t.Run("records the scheme https for a request over TLS", func(t *testing.T) {
			t.Parallel()

			cfg, client := tlsPair(t)
			f := newFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), httpserver.WithTLS(cfg))
			testkit.NoError(t, request(t, client, "https"+strings.TrimPrefix(f.url, "http")), "the request over TLS")

			testkit.Equal(t, f.reporter.bound()[0].attrs[2], telemetry.AttrString(semconv.URLScheme, "https"),
				"the scheme of the set")
		})
	})

	t.Run("Log", func(t *testing.T) {
		t.Parallel()

		t.Run("writes one record per request at LevelInfo", func(t *testing.T) {
			t.Parallel()

			clk := fake.New(origin)
			mux := http.NewServeMux()
			mux.HandleFunc(itemPattern, func(w http.ResponseWriter, _ *http.Request) {
				clk.Advance(elapsed)
				_, _ = io.WriteString(w, body)
			})
			f := newFixture(t, mux, httpserver.WithClock(clk))
			testkit.Equal(t, f.send(t, http.MethodGet, itemPath, http.NoBody, nil).status, http.StatusOK, "the status")
			testkit.NoError(t, f.stop(), "Run must drain")

			records := f.logs.find(messageRequest)
			testkit.Len(t, records, 1, "the server must write one record")
			r := records[0]
			testkit.Equal(t, r.Level, slog.LevelInfo, "the level")
			testkit.Equal(t, value(t, &r, semconv.RequestMethod).String(), http.MethodGet, "the method")
			testkit.Equal(t, value(t, &r, semconv.Route).String(), itemRoute, "the route")
			testkit.Equal(t, value(t, &r, semconv.ResponseStatusCode).Int64(), int64(http.StatusOK), "the status")
			testkit.Equal(t, value(t, &r, keyDuration).Duration(), elapsed, "the duration")
			testkit.Equal(t, value(t, &r, keyBodySize).Int64(), int64(len(body)), "the bytes of the body")
			testkit.Equal(t, value(t, &r, keyTraceID).String(), string(traceID), "the trace ID of the span")
		})

		t.Run("writes the record at LevelError for a status of 500 and above", func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}))
			f.send(t, http.MethodGet, "/", http.NoBody, nil)
			testkit.NoError(t, f.stop(), "Run must drain")

			testkit.Equal(t, f.logs.find(messageRequest)[0].Level, slog.LevelError, "the level")
		})

		t.Run("contains the error that Error recorded", func(t *testing.T) {
			t.Parallel()

			notFound := errs.WithClass(errBoom, errs.NotFound)
			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				httpserver.Error(w, r, notFound)
			}))
			testkit.Equal(t, f.send(t, http.MethodGet, "/", http.NoBody, nil).status, http.StatusNotFound, "the status")
			testkit.NoError(t, f.stop(), "Run must drain")

			r := f.logs.find(messageRequest)[0]
			testkit.Equal(t, r.Level, slog.LevelInfo, "the level of a record below 500")
			logged, _ := value(t, &r, keyError).Any().(error)
			testkit.ErrorIs(t, logged, notFound, "the error")
		})

		t.Run("writes no record of a level that the logger does not handle", func(t *testing.T) {
			t.Parallel()

			warn := &logs{level: slog.LevelWarn}
			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/fail" {
					w.WriteHeader(http.StatusInternalServerError)
				}
			}), httpserver.WithLogger(slog.New(warn)))

			f.send(t, http.MethodGet, "/", http.NoBody, nil)
			f.send(t, http.MethodGet, "/fail", http.NoBody, nil)
			testkit.NoError(t, f.stop(), "Run must drain")

			records := warn.find(messageRequest)
			testkit.Len(t, records, 1, "the logger must receive the record of the failed request only")
			testkit.Equal(t, value(t, &records[0], semconv.ResponseStatusCode).Int64(),
				int64(http.StatusInternalServerError), "the status of the record")
		})

		t.Run("contains the trace ID of the caller for a span without an identity", func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			f.reporter.mu.Lock()
			f.reporter.anonymous = true
			f.reporter.mu.Unlock()

			f.send(t, http.MethodGet, "/", http.NoBody, http.Header{"Traceparent": {traceparent}})
			f.send(t, http.MethodGet, "/", http.NoBody, nil)
			testkit.NoError(t, f.stop(), "Run must drain")

			records := f.logs.find(messageRequest)
			testkit.Len(t, records, 2, "the server must write a record per request")
			testkit.Equal(t, value(t, &records[0], keyTraceID).String(), callerTrace, "the trace ID of the caller")
			records[1].Attrs(func(a slog.Attr) bool {
				testkit.True(t, a.Key != keyTraceID, "a request without a trace must have no trace ID")

				return true
			})
		})
	})

	t.Run("CrossOriginProtection", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			header http.Header
			name   string
			method string
			want   int
		}{
			{
				name:   "responds with 403 to a cross-origin POST of a browser",
				method: http.MethodPost,
				header: http.Header{headerFetchSite: {crossSite}},
				want:   http.StatusForbidden,
			},
			{
				name:   "admits a POST without the headers of a browser",
				method: http.MethodPost,
				want:   http.StatusOK,
			},
			{
				name:   "admits a cross-origin GET of a browser",
				method: http.MethodGet,
				header: http.Header{headerFetchSite: {crossSite}},
				want:   http.StatusOK,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				f := newFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
				testkit.Equal(t, f.send(t, tt.method, "/", http.NoBody, tt.header).status, tt.want, "the status")
			})
		}
	})

	t.Run("Route", func(t *testing.T) {
		t.Parallel()

		t.Run("records the path of a pattern with a method and a host", func(t *testing.T) {
			t.Parallel()

			mux := http.NewServeMux()
			mux.HandleFunc("GET example.com/items/{id}", func(http.ResponseWriter, *http.Request) {})
			f := newFixture(t, mux)

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, f.url+itemPath, http.NoBody)
			testkit.NoError(t, err, "the request must build")
			req.Host = "example.com"
			testkit.Equal(t, do(t, f.client, req).status, http.StatusOK, "the status")
			testkit.NoError(t, f.stop(), "Run must drain")

			testkit.Equal(t, value(t, &f.logs.find(messageRequest)[0], semconv.Route).String(), itemRoute, "the route")
		})

		t.Run("records the path of a pattern without a method", func(t *testing.T) {
			t.Parallel()

			mux := http.NewServeMux()
			mux.HandleFunc("/static/", func(http.ResponseWriter, *http.Request) {})
			f := newFixture(t, mux)
			got := f.send(t, http.MethodGet, "/static/app.js", http.NoBody, nil)
			testkit.Equal(t, got.status, http.StatusOK, "the status")
			testkit.NoError(t, f.stop(), "Run must drain")

			testkit.Equal(t, value(t, &f.logs.find(messageRequest)[0], semconv.Route).String(), "/static/", "the route")
		})

		t.Run("records no route when a middleware replaced the context", func(t *testing.T) {
			t.Parallel()

			mux := http.NewServeMux()
			mux.HandleFunc(itemPattern, func(http.ResponseWriter, *http.Request) {})
			f := newFixture(t, mux, httpserver.WithMiddleware(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					next.ServeHTTP(w, r.WithContext(t.Context())) //nolint:contextcheck // the case replaces the context
				})
			}))
			testkit.Equal(t, f.send(t, http.MethodGet, itemPath, http.NoBody, nil).status, http.StatusOK, "the status")
			testkit.NoError(t, f.stop(), "Run must drain")

			testkit.Equal(t, value(t, &f.logs.find(messageRequest)[0], semconv.Route).String(), "", "the route")
		})
	})
}
