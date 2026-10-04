// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package httpclient calls one HTTP dependency on net/http with the
// limits, the guards, the telemetry and the error classification that a
// production client needs.
//
// [New] builds a [Client] of a dependency's name and options. [Client.Do]
// sends a request as http.Client.Do does. [Client.Fetch] returns the body of
// a response that succeeded, and [Client.AppendFetch] appends it to a buffer
// of the caller. Each Client has a transport of its own, never net/http's
// DefaultTransport, with a limit on every phase of an attempt.
//
// # Dependencies
//
// A Client reads no process-wide default and calls no host that its
// configuration does not name. New requires five options, and refuses a
// Client without any of them with [ErrConfig]:
//
//   - [WithClock], which times each attempt.
//   - [WithLogger], which receives the record of each call that fails.
//   - [WithReporter], which records the duration histogram and the spans.
//   - [WithPropagator], which injects the trace of each attempt.
//   - [WithHosts], the hosts that the client may call.
//
// # Limits
//
// Each limit has an option and a default:
//
//   - [WithTimeout]: 10 s, for one attempt, from its connection to the end
//     of the read of its body. A caller bounds a whole call with the
//     deadline of its context.
//   - [WithDialTimeout]: 5 s, where net/http waits 30 s.
//   - [WithTLSHandshakeTimeout]: 5 s, where net/http waits 10 s.
//   - [WithIdleConnTimeout]: 90 s.
//   - [WithMaxIdleConnsPerHost]: 32, where net/http keeps 2.
//   - [WithMaxResponseBytes]: 8 MiB, for the body that Fetch and AppendFetch
//     read.
//
// A negative value turns a limit off, and New refuses a zero value.
//
// # Addresses
//
// A Client of [ReachPublic], the default, checks every address that it
// connects to, after the resolver returned it and before the connection
// starts, so a name that resolves to an internal address fails with
// [ErrBlocked] whatever an earlier resolution returned. A request to an
// admitted name therefore cannot connect to the internal network of a
// deployment. [ReachPrivate] admits every address, for calls inside a
// deployment. With [WithProxy], the dialer connects to the proxy, and the
// proxy enforces the addresses that a deployment admits.
//
// [WithDialContext] replaces the dialer with a function of the caller, such
// as one that returns one end of a net.Pipe in a test that counts the
// allocations of a call. New refuses it for a client of ReachPublic,
// because the function replaces the dialer that checks the addresses.
//
// # Guards
//
// [WithBreaker] guards each attempt with a circuit per host, and
// [WithRetrier] retries a Transient failure of a request that is safe to
// send again, waiting at least the delay of its Retry-After header. A call
// whose caller cancelled it records no outcome in the circuit, and is not
// retried. [WithPrepare] adds a function that prepares the request of each
// attempt, such as one that signs it, and [WithClassify] replaces the
// classification of the responses.
//
// # Classification
//
// The default classification refuses every status other than 2xx with a
// *[StatusError], whose class follows the status: 4xx statuses classify as
// the request's fault, 408, 425, 429 and 5xx other than 501 as Transient.
// A certificate that fails verification classifies as Integrity, because it
// fails the same way on every attempt. Another error of the transport
// classifies as Transient unless its cause has a class, and the error of a
// context that ended keeps the class of the context's error.
//
// # Telemetry
//
// The client records the histogram http.client.request.duration of the
// OpenTelemetry semantic conventions, in seconds, per attempt, with the
// attributes http.request.method, server.address, server.port,
// http.response.status_code and error.type. error.type is the class of the
// error of an attempt that failed, such as Transient. A span of an attempt
// with a trace identity receives the same attributes, and ends with the
// error of the attempt. The client logs each call that fails at
// slog.LevelWarn, unless the caller's context ended.
//
// The client binds an attribute set per host. A suffix of WithHosts admits
// any subdomain, so a client that calls many subdomains binds many sets, up
// to the bound of the bound sets that it keeps.
//
// # Allocation contract
//
// Each attempt allocates 2 objects of the client: the copies of the request
// and of its headers, which leave the caller's request unchanged. A traced
// attempt also allocates the attributes of its span. The state of a call
// and the guards allocate nothing. Under a limit, Fetch allocates the body
// once when the response declares its length. AppendFetch does not allocate
// the body when the caller's buffer has room for it. The benchmarks of the
// package measure a call on a connection that the transport reuses, with Go
// 1.27.1:
//
//   - Do: 54 objects, 52 of them in net/http's Client and Transport.
//   - Do of a traced request: 60 objects.
//   - Do with a breaker and a retrier: 54 objects.
//   - Fetch: 55 objects, the body included.
//   - AppendFetch into a buffer with room for the body: 54 objects.
//   - AppendFetch of a chunked response into a buffer with room: 57
//     objects. net/http allocates the key and the value of its
//     Transfer-Encoding header, and the TransferEncoding of the response.
//
// The tracer, the propagator, and the functions of WithPrepare and
// WithClassify allocate on their own.
//
// # Errors
//
// Every error of this package classifies under
// [go.thesmos.sh/core/errs.Classify]:
//
//   - [ErrConfig], Invalid: New refuses the configuration.
//   - [ErrBlocked], Denied: the client does not send the request.
//   - [ErrTooLarge], Invalid: a body beyond the limit of Fetch and
//     AppendFetch.
//   - *[StatusError], by status: a response that the default
//     classification refuses.
//
// # Dependency position
//
// Imports context, crypto/tls, errors, fmt, io, log/slog, math, net,
// net/http, net/netip, net/url, slices, strconv, strings, syscall and time
// from the standard library, and go.thesmos.sh/core/clock,
// go.thesmos.sh/core/errs, go.thesmos.sh/core/net/internal/semconv,
// go.thesmos.sh/core/resilience and go.thesmos.sh/core/telemetry from this
// module.
package httpclient
