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

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

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
			expect.Equal(t, got.status, http.StatusInternalServerError, "the response must have the status 500")
			expect.Equal(t, got.header.Get("Content-Type"), problemJSON, "the body must be problem details")
			expect.Equal(t, got.body, `{"type":"about:blank","title":"Internal Server Error","status":500}`,
				"the body must be the problem details of 500")

			assert.NoError(t, f.stop(), "Run must drain")
			records := f.logs.find(messageRequest)
			assert.Length(t, records, 1, "the server must write one record")
			expect.Equal(t, records[0].Level, slog.LevelError, "the record must have the level Error")
			recovered, _ := value(t, &records[0], keyPanic).Any().(error)
			expect.ErrorIs(t, recovered, errBoom, "the record must contain the value of the panic")
			expect.Contains(t, value(t, &records[0], keyStack).String(), "TestChain",
				"the record must contain the stack of the panic")

			end := f.reporter.started()[0].OnEnd.AssertCalledOnce(t, "the span must end once")
			assert.HasError(t, end.Err, "the span must end with an error")
			assert.Contains(t, end.Err.Error(), "panicked", "the span must end with the error of a panic")
		})

		t.Run("aborts the response for a panic after the handler wrote the body", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, "partial")
				w.(http.Flusher).Flush()
				panic(errBoom) //nolint:forbidigo // the case tests the recovery of a panic
			}))

			assert.HasError(t, request(t, f.client, f.url), "the client must not read a partial body as complete")

			assert.NoError(t, f.stop(), "Run must drain")
			records := f.logs.find(messageRequest)
			assert.Length(t, records, 1, "the server must write one record")
			status := value(t, &records[0], semconv.ResponseStatusCode).Int64()
			expect.Equal(t, status, int64(http.StatusOK), "the record must contain the status that the handler wrote")
			recovered, _ := value(t, &records[0], keyPanic).Any().(error)
			expect.ErrorIs(t, recovered, errBoom, "the record must contain the value of the panic")
		})

		tests := []struct {
			write func(http.ResponseWriter)
			name  string
		}{
			{
				name:  "aborts the response for a panic after WriteHeader",
				write: func(w http.ResponseWriter) { w.WriteHeader(http.StatusOK) },
			},
			{
				name:  "aborts the response for a panic after Write",
				write: func(w http.ResponseWriter) { _, _ = w.Write([]byte(body)) },
			},
			{
				name:  "aborts the response for a panic after WriteString",
				write: func(w http.ResponseWriter) { _, _ = w.(io.StringWriter).WriteString(body) },
			},
			{
				name:  "aborts the response for a panic after ReadFrom",
				write: func(w http.ResponseWriter) { _, _ = w.(io.ReaderFrom).ReadFrom(strings.NewReader(body)) },
			},
			{
				name:  "aborts the response for a panic after Flush",
				write: func(w http.ResponseWriter) { w.(http.Flusher).Flush() },
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					tt.write(w)
					panic(errBoom) //nolint:forbidigo // the case tests the recovery of a panic
				}))

				assert.HasError(t, request(t, f.client, f.url),
					"the client must not read a partial response as complete")
			})
		}

		t.Run("aborts the response without a stack for a panic with http.ErrAbortHandler", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, "partial")
				w.(http.Flusher).Flush()
				panic(http.ErrAbortHandler) //nolint:forbidigo // the case tests the recovery of a panic
			}))

			assert.HasError(t, request(t, f.client, f.url), "the client must not read a partial body as complete")

			assert.NoError(t, f.stop(), "Run must drain")
			records := f.logs.find(messageRequest)
			assert.Length(t, records, 1, "the server must write one record")
			keys := keysOf(&records[0])
			expect.NotContains(t, keys, keyPanic, "the record of an abort must contain no panic")
			expect.NotContains(t, keys, keyStack, "the record of an abort must contain no stack")
		})

		t.Run("records 500 for a panic with http.ErrAbortHandler before a header", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				panic(http.ErrAbortHandler) //nolint:forbidigo // the case tests the recovery of a panic
			}))

			assert.HasError(t, request(t, f.client, f.url), "the client must not receive a response")

			assert.NoError(t, f.stop(), "Run must drain")
			records := f.logs.find(messageRequest)
			assert.Length(t, records, 1, "the server must write one record")
			assert.Equal(t, value(t, &records[0], semconv.ResponseStatusCode).Int64(),
				int64(http.StatusInternalServerError), "the record must contain the status 500")
		})

		t.Run("writes no body for a panic with http.ErrAbortHandler before a header", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				panic(http.ErrAbortHandler) //nolint:forbidigo // the case tests the recovery of a panic
			}))

			assert.HasError(t, request(t, f.client, f.url), "the client must not receive a response")

			assert.NoError(t, f.stop(), "Run must drain")
			records := f.logs.find(messageRequest)
			assert.Length(t, records, 1, "the server must write one record")
			assert.Equal(t, value(t, &records[0], keyBodySize).Int64(), int64(0),
				"the record must contain no bytes of problem details")
		})
	})

	t.Run("Telemetry", func(t *testing.T) {
		t.Parallel()

		t.Run("starts a server span named by the method", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			assert.Equal(t, f.send(t, http.MethodGet, "/", http.NoBody, nil).status, http.StatusOK,
				"the request must succeed")

			spans := f.reporter.started()
			assert.Length(t, spans, 1, "the server must start one span")
			expect.Equal(t, spans[0].name, telemetry.SpanName(http.MethodGet),
				"the span must have the name of the method")
			expect.Equal(t, telemetry.ApplySpanOptions(spans[0].opts), telemetry.SpanKindServer,
				"the span must be of the kind server")

			_, remote := telemetry.RemoteParent(spans[0].opts)
			expect.False(t, remote, "a request without a trace must start a new trace")
		})

		t.Run("starts the span as the child of the trace of the caller", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			got := f.send(t, http.MethodGet, "/", http.NoBody, http.Header{"Traceparent": {traceparent}})
			assert.Equal(t, got.status, http.StatusOK, "the request must succeed")

			parent, remote := telemetry.RemoteParent(f.reporter.started()[0].opts)
			assert.True(t, remote, "the span must have the remote parent of the caller")
			assert.Equal(t, parent, telemetry.SpanContext{TraceID: callerTrace, SpanID: callerSpan, Sampled: true},
				"the parent must be the span of the caller")
		})

		t.Run("names the span _OTHER for a method outside RFC 9110", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			assert.Equal(t, f.send(t, "PROPFIND", "/", http.NoBody, nil).status, http.StatusOK,
				"the request must succeed")
			assert.Equal(t, f.reporter.started()[0].name, telemetry.SpanName(semconv.OtherMethod),
				"the span must have the name _OTHER")
		})

		t.Run("sets the attributes of the request on a span with an identity", func(t *testing.T) {
			t.Parallel()
			mux := http.NewServeMux()
			mux.HandleFunc(itemPattern, func(http.ResponseWriter, *http.Request) {})
			f := newFixture(t, mux)
			assert.Equal(t, f.send(t, http.MethodGet, itemPath, http.NoBody, nil).status, http.StatusOK,
				"the request must succeed")

			call := f.reporter.started()[0].OnSetAttributes.AssertCalledOnce(t, "the span must receive attributes")
			assert.Equal(t, call.Attrs, []telemetry.Attr{
				telemetry.AttrString(semconv.RequestMethod, http.MethodGet),
				telemetry.AttrInt(semconv.ResponseStatusCode, http.StatusOK),
				telemetry.AttrString(semconv.URLScheme, "http"),
				telemetry.AttrString(semconv.Route, itemRoute),
			}, "the span must receive the attributes of the request")
		})

		t.Run("sets no attributes on a span without an identity", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			f.reporter.mu.Lock()
			f.reporter.anonymous = true
			f.reporter.mu.Unlock()

			assert.Equal(t, f.send(t, http.MethodGet, "/", http.NoBody, nil).status, http.StatusOK,
				"the request must succeed")
			assert.NoError(t, f.stop(), "Run must drain")

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
				assert.NoError(t, f.stop(), "Run must drain")

				end := f.reporter.started()[0].OnEnd.AssertCalledOnce(t, "the span must end once")
				if tt.want == "" {
					assert.NoError(t, end.Err, "the span must end without an error")

					return
				}

				assert.HasError(t, end.Err, "the span must end with an error")
				assert.Equal(t, end.Err.Error(), tt.want, "the span must end with the error of the response")
			})
		}

		t.Run("records the duration in seconds into the set of the request", func(t *testing.T) {
			t.Parallel()
			clk := fake.New(origin)
			mux := http.NewServeMux()
			mux.HandleFunc(itemPattern, func(http.ResponseWriter, *http.Request) { clk.Advance(elapsed) })
			f := newFixture(t, mux, httpserver.WithClock(clk))
			assert.Equal(t, f.send(t, http.MethodGet, itemPath, http.NoBody, nil).status, http.StatusOK,
				"the request must succeed")

			sets := f.reporter.bound()
			assert.Length(t, sets, 1, "the server must bind one set")
			expect.Equal(t, sets[0].attrs, []telemetry.Attr{
				telemetry.AttrString(semconv.RequestMethod, http.MethodGet),
				telemetry.AttrInt(semconv.ResponseStatusCode, http.StatusOK),
				telemetry.AttrString(semconv.URLScheme, "http"),
				telemetry.AttrString(semconv.Route, itemRoute),
			}, "the set must have the attributes of the request")
			expect.Equal(t, sets[0].OnRecord.AssertCalledOnce(t, "the set must record once").Value, elapsed.Seconds(),
				"the set must record the duration in seconds")
		})

		t.Run("records the set of a request that matched no route without the route", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, http.NewServeMux())
			got := f.send(t, "PROPFIND", "/missing", http.NoBody, nil)
			assert.Equal(t, got.status, http.StatusNotFound, "the request must match no route")

			sets := f.reporter.bound()
			assert.Length(t, sets, 1, "the server must bind one set")
			assert.Equal(t, sets[0].attrs, []telemetry.Attr{
				telemetry.AttrString(semconv.RequestMethod, semconv.OtherMethod),
				telemetry.AttrInt(semconv.ResponseStatusCode, http.StatusNotFound),
				telemetry.AttrString(semconv.URLScheme, "http"),
			}, "the set must have no route")
		})

		t.Run("records the scheme https for a request over TLS", func(t *testing.T) {
			t.Parallel()
			cfg, client := tlsPair(t)
			f := newFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), httpserver.WithTLS(cfg))
			assert.NoError(t, request(t, client, "https"+strings.TrimPrefix(f.url, "http")),
				"the request over TLS must succeed")

			sets := f.reporter.bound()
			assert.Length(t, sets, 1, "the server must bind one set")
			assert.Equal(t, sets[0].attrs[2], telemetry.AttrString(semconv.URLScheme, "https"),
				"the set must have the scheme https")
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
			assert.Equal(t, f.send(t, http.MethodGet, itemPath, http.NoBody, nil).status, http.StatusOK,
				"the request must succeed")
			assert.NoError(t, f.stop(), "Run must drain")

			records := f.logs.find(messageRequest)
			assert.Length(t, records, 1, "the server must write one record")
			r := records[0]
			expect.Equal(t, r.Level, slog.LevelInfo, "the record must have the level Info")
			expect.Equal(t, value(t, &r, semconv.RequestMethod).String(), http.MethodGet,
				"the record must contain the method")
			expect.Equal(t, value(t, &r, semconv.Route).String(), itemRoute, "the record must contain the route")
			expect.Equal(t, value(t, &r, semconv.ResponseStatusCode).Int64(), int64(http.StatusOK),
				"the record must contain the status")
			expect.Equal(t, value(t, &r, keyDuration).Duration(), elapsed, "the record must contain the duration")
			expect.Equal(t, value(t, &r, keyBodySize).Int64(), int64(len(body)),
				"the record must contain the bytes of the body")
			expect.Equal(t, value(t, &r, keyTraceID).String(), string(traceID),
				"the record must contain the trace ID of the span")
			expect.Equal(t, keysOf(&r), []string{
				semconv.RequestMethod, semconv.Route, semconv.ResponseStatusCode, keyDuration, keyBodySize, keyTraceID,
			}, "the record must contain only the attributes of a request that succeeded")
		})

		t.Run("writes the record at LevelError for a status of 500 and above", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}))
			f.send(t, http.MethodGet, "/", http.NoBody, nil)
			assert.NoError(t, f.stop(), "Run must drain")

			records := f.logs.find(messageRequest)
			assert.Length(t, records, 1, "the server must write one record")
			assert.Equal(t, records[0].Level, slog.LevelError, "the record of a 500 must have the level Error")
		})

		t.Run("contains the error that Error recorded", func(t *testing.T) {
			t.Parallel()
			notFound := errs.WithClass(errBoom, errs.NotFound)
			f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				httpserver.Error(w, r, notFound)
			}))
			assert.Equal(t, f.send(t, http.MethodGet, "/", http.NoBody, nil).status, http.StatusNotFound,
				"the response must have the status of the error")
			assert.NoError(t, f.stop(), "Run must drain")

			r := f.logs.find(messageRequest)[0]
			expect.Equal(t, r.Level, slog.LevelInfo, "the record below 500 must have the level Info")
			logged, _ := value(t, &r, keyError).Any().(error)
			expect.ErrorIs(t, logged, notFound, "the record must contain the error")
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
			assert.NoError(t, f.stop(), "Run must drain")

			records := warn.find(messageRequest)
			assert.Length(t, records, 1, "the logger must receive the record of the failed request alone")
			assert.Equal(t, value(t, &records[0], semconv.ResponseStatusCode).Int64(),
				int64(http.StatusInternalServerError), "the record must be the one of the failed request")
		})

		t.Run("contains the trace ID of the caller for a span without an identity", func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			f.reporter.mu.Lock()
			f.reporter.anonymous = true
			f.reporter.mu.Unlock()

			f.send(t, http.MethodGet, "/", http.NoBody, http.Header{"Traceparent": {traceparent}})
			f.send(t, http.MethodGet, "/", http.NoBody, nil)
			assert.NoError(t, f.stop(), "Run must drain")

			records := f.logs.find(messageRequest)
			assert.Length(t, records, 2, "the server must write a record per request")
			expect.Equal(t, value(t, &records[0], keyTraceID).String(), callerTrace,
				"the record must contain the trace ID of the caller")
			expect.NotContains(t, keysOf(&records[1]), keyTraceID, "a request without a trace must have no trace ID")
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
				assert.Equal(t, f.send(t, tt.method, "/", http.NoBody, tt.header).status, tt.want,
					"the protection must decide the request")
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
			assert.NoError(t, err, "the request must build")
			req.Host = "example.com"
			assert.Equal(t, do(t, f.client, req).status, http.StatusOK, "the request must match the pattern")
			assert.NoError(t, f.stop(), "Run must drain")

			assert.Equal(t, value(t, &f.logs.find(messageRequest)[0], semconv.Route).String(), itemRoute,
				"the record must contain the path of the pattern")
		})

		t.Run("records the path of a pattern without a method", func(t *testing.T) {
			t.Parallel()
			mux := http.NewServeMux()
			mux.HandleFunc("/static/", func(http.ResponseWriter, *http.Request) {})
			f := newFixture(t, mux)
			got := f.send(t, http.MethodGet, "/static/app.js", http.NoBody, nil)
			assert.Equal(t, got.status, http.StatusOK, "the request must match the pattern")
			assert.NoError(t, f.stop(), "Run must drain")

			assert.Equal(t, value(t, &f.logs.find(messageRequest)[0], semconv.Route).String(), "/static/",
				"the record must contain the pattern")
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
			assert.Equal(t, f.send(t, http.MethodGet, itemPath, http.NoBody, nil).status, http.StatusOK,
				"the request must match the pattern")
			assert.NoError(t, f.stop(), "Run must drain")

			assert.Empty(t, value(t, &f.logs.find(messageRequest)[0], semconv.Route).String(),
				"the record must contain no route")
		})
	})
}

// keysOf returns the keys of the attributes of r, in order.
func keysOf(r *slog.Record) []string {
	keys := make([]string, 0, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		keys = append(keys, a.Key)

		return true
	})

	return keys
}
