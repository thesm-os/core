// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package httpserver serves HTTP on net/http with the limits, the
// telemetry, the recovery and the graceful shutdown that a production
// server needs.
//
// [New] builds a [Server] of a handler and options. [Server.Run] listens,
// serves until its context ends, and then drains. A server limits every
// phase of a connection without further options, because net/http leaves
// most limits off at their zero values.
//
// # Dependencies
//
// A Server reads no process-wide default. New requires four options, and
// refuses a Server without any of them with [ErrConfig]:
//
//   - [WithClock], which times each request and the drain.
//   - [WithLogger], which receives the log record of each request.
//   - [WithReporter], which records the duration histogram and the spans.
//   - [WithPropagator], which extracts the caller's trace.
//
// A process passes the same four to every Server, and bundles them as one
// option with [Options].
//
// # Limits
//
// Each limit has an option and a default:
//
//   - [WithReadHeaderTimeout]: 5 s. A client that sends its headers slowly
//     occupies a connection and a goroutine.
//   - [WithReadTimeout]: 30 s, for the whole request, its body included.
//   - [WithWriteTimeout]: 30 s, for the write of a response to a client
//     that reads slowly.
//   - [WithIdleTimeout]: 120 s, for a keep-alive connection between
//     requests.
//   - [WithMaxHeaderBytes]: 64 KiB, where net/http allows 1 MiB.
//   - [WithMaxBodyBytes]: 4 MiB, for every request body.
//   - [WithDrainDelay]: 5 s, during which a load balancer stops routing to
//     the server.
//   - [WithShutdownTimeout]: 20 s, so that the drain and the shutdown end
//     within the 30 s that Kubernetes grants a pod by default.
//
// A negative value turns a limit off, and New refuses a zero value with
// [ErrConfig]. [WithMaxInFlight] has no default: the cost of a request is
// the handler's, so no bound that this package chose would fit.
//
// # The chain of a request
//
// The server wraps the handler in these steps, outermost first:
//
//  1. Recovery. The server writes 500 for a panic of a later step before
//     the handler wrote a header. A panic after the header, and a panic
//     with http.ErrAbortHandler, abort the response, so the client does not
//     read a partial body as a complete one.
//  2. Telemetry. The propagator extracts the caller's trace from the
//     headers, and a server span starts as its child through
//     [go.thesmos.sh/core/telemetry.WithRemoteParent]. The span is named by
//     the method of the request.
//  3. Body limit. net/http stops a body at its declared length, so a
//     request that declares a body beyond the limit receives 413 at once.
//     http.MaxBytesReader bounds a body of unknown length as the later
//     steps read it, and a read beyond the limit fails with
//     *http.MaxBytesError.
//  4. Middleware. The functions of [WithMiddleware], the first outermost.
//  5. In-flight limit. With [WithMaxInFlight], the server responds to each
//     request beyond the bound with 503 and Retry-After: 1.
//  6. Cross-origin protection. http.CrossOriginProtection refuses with 403
//     a state-changing request that a browser sent from another origin. It
//     admits the safe methods, requests without the Sec-Fetch-Site and
//     Origin headers, which come from clients other than browsers, and the
//     origins of [WithTrustedOrigins].
//
// When the request ends, the server records its duration, writes its log
// record and ends its span.
//
// # Telemetry
//
// The server records the histogram http.server.request.duration of the
// OpenTelemetry semantic conventions, in seconds, with the attributes
// http.request.method, http.route, http.response.status_code and
// url.scheme. The route is the path of the pattern of the ServeMux that
// matched the request, such as /items/{id}, so a path parameter does not
// create a series of its own. A method outside RFC 9110 and PATCH records
// as _OTHER. A span with a trace identity receives the same attributes. A
// span of a status of 500 and above ends with the error that [Error]
// recorded, and a span of a handler that panicked ends with an error.
//
// The server writes one log record per request, at slog.LevelInfo, and at
// slog.LevelError for a status of 500 and above. It contains the method,
// the route, the status, the duration, the bytes of the body, the trace ID,
// the error that Error recorded, the value and the stack of a panic, and
// the attributes of [Annotate]. A logger whose handler does not handle the
// level of a record receives none, at no cost per request. A deployment
// that needs fewer records wraps its handler in
// [go.thesmos.sh/core/telemetry.RateLimitHandler].
//
// # Errors of a handler
//
// [Error] writes the problem details of RFC 9457 for an error, with the
// status of its class under [go.thesmos.sh/core/errs], and records the
// error in the log record and the span of the request. The response
// contains no text of the error. The responses of the in-flight limit and
// of the cross-origin protection have the same form.
//
// # Drain
//
// When the context of Run ends, [Server.Ready] responds with 503, and the
// server keeps serving for the drain delay, while a load balancer that
// probes readiness stops routing to it. http.Server.Shutdown then stops
// accepting connections and waits for the requests in flight. When the
// shutdown timeout elapses first, Run closes their connections and returns
// [ErrShutdown].
//
// A deployment serves [Server.Live] and [Server.Ready] on a second Server of
// its own port, so the probes are not reachable on the port that serves
// traffic, and the readiness probe responds with 503 for the whole drain:
//
//	deps := httpserver.Options(
//	    httpserver.WithClock(clk),
//	    httpserver.WithLogger(logger),
//	    httpserver.WithReporter(reporter),
//	    httpserver.WithPropagator(w3c.Propagator{}),
//	)
//	api, err := httpserver.New(mux, deps, httpserver.WithAddr(":8443"), httpserver.WithTLS(cfg))
//
//	probes := http.NewServeMux()
//	probes.Handle("GET /healthz", api.Live())
//	probes.Handle("GET /readyz", api.Ready())
//	ops, err := httpserver.New(probes, deps, httpserver.WithAddr(":9090"))
//
// # Errors
//
// Every error of this package classifies under
// [go.thesmos.sh/core/errs.Classify], except [ErrShutdown], whose
// documentation states why:
//
//   - [ErrConfig], Invalid: New refuses the configuration.
//   - [ErrClosed], Invalid: Run was called before.
//   - [ErrListen], Transient: Run cannot listen on its address.
//   - [ErrServe], Transient: serving failed.
//   - [ErrShutdown]: the drain did not finish within the shutdown timeout.
//
// Run joins each sentinel with the error that caused it, so errors.Is
// matches both, and the class of the cause applies when it ranks higher.
//
// # Allocation contract
//
// The chain allocates per request:
//
//   - One exchange, which is the context of the request, its
//     ResponseWriter, and the storage of four attributes of Annotate and of
//     the header values of a problem response.
//   - The copy of the request that Request.WithContext makes. net/http
//     offers no other way to give a handler a context.
//   - The reader of http.MaxBytesReader, for a body of unknown length.
//   - The attributes of a span with a trace identity.
//
// A problem response, of [Error] or of the chain, allocates nothing of its
// own. A log record of more than five attributes allocates its overflow in
// slog.Record. The tracer, the propagator, the logger's handler, the
// middleware and the handler allocate on their own. The benchmarks of the
// package check the counts of the chain: 2 objects for a request, and 3
// for a body of unknown length.
//
// # Dependency position
//
// Imports bufio, cmp, context, crypto/tls, encoding/json, errors, fmt, io,
// log/slog, net, net/http, runtime/debug, slices, strconv, strings, sync,
// sync/atomic and time from the standard library, and
// go.thesmos.sh/core/clock, go.thesmos.sh/core/errs,
// go.thesmos.sh/core/net/internal/semconv and go.thesmos.sh/core/telemetry
// from this module.
package httpserver
