// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package httpclient

import (
	"crypto/tls"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"time"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/resilience"
	"go.thesmos.sh/core/telemetry"
)

// Option configures a [Client]. [New] applies the options in order, so a
// later option overrides an earlier one, and then checks the result once.
// An option does not fail itself: New returns [ErrConfig] for a value that
// it refuses. [WithHosts] adds to the hosts that earlier calls added.
//
// [WithClock], [WithLogger], [WithReporter], [WithPropagator] and
// [WithHosts] are required, because a Client reads no process-wide default
// and calls no host that its configuration does not name. Every limit has
// a default. A nil argument of another option restores its default.
//
// A negative duration or size turns off the limit that it sets. Zero is out
// of range for every duration, size and count, so a configuration that
// leaves a field unset does not turn a protection off without notice.
//
// A consumer builds its own options from these, and bundles several with
// [Options], such as the four required options of every client of a
// process.
type Option func(*settings)

// settings is the configuration of a Client: the defaults, with the options
// applied in order.
type settings struct {
	logger     *slog.Logger
	reporter   telemetry.Reporter
	propagator telemetry.Propagator
	clock      clock.Clock
	breaker    *resilience.Breaker
	retrier    *resilience.Retrier
	tls        *tls.Config
	proxy      *url.URL
	resolver   *net.Resolver
	prepare    func(*http.Request) error
	classify   func(*http.Response) error
	hosts      []string

	timeout             time.Duration
	dialTimeout         time.Duration
	tlsHandshakeTimeout time.Duration
	idleConnTimeout     time.Duration
	maxResponseBytes    int64
	maxIdleConnsPerHost int

	reach Reach

	// nilOption reports that New or Options received a nil Option.
	nilOption bool
}

// apply applies opts to s in order, and records a nil option.
func (s *settings) apply(opts []Option) {
	for _, o := range opts {
		if o == nil {
			s.nilOption = true

			continue
		}

		o(s)
	}
}

// problem returns the description of the first value of s that New
// refuses, and the empty string when New accepts every value.
func (s *settings) problem() string {
	invalidHost := slices.ContainsFunc(s.hosts, func(h string) bool { return !validHost(h) })

	problems := [...]struct {
		what    string
		refused bool
	}{
		{"an option is nil", s.nilOption},
		{"the clock is nil or missing: New requires WithClock", s.clock == nil},
		{"the logger is nil or missing: New requires WithLogger", s.logger == nil},
		{"the reporter is nil or missing: New requires WithReporter", s.reporter == nil},
		{"the propagator is nil or missing: New requires WithPropagator", s.propagator == nil},
		{"no host is admitted: New requires WithHosts", len(s.hosts) == 0},
		{"a host is not an IP address, a DNS name or a DNS name after a dot", invalidHost},
		{"the reach is not ReachPublic or ReachPrivate", !s.reach.Valid()},
		{"the timeout is zero", s.timeout == 0},
		{"the dial timeout is zero", s.dialTimeout == 0},
		{"the TLS handshake timeout is zero", s.tlsHandshakeTimeout == 0},
		{"the idle connection timeout is zero", s.idleConnTimeout == 0},
		{"the idle connections per host are not positive", s.maxIdleConnsPerHost <= 0},
		{"the response limit is zero", s.maxResponseBytes == 0},
	}

	for _, p := range problems {
		if p.refused {
			return p.what
		}
	}

	return ""
}

// Options returns an Option that applies opts in order, so a consumer's own
// option can bundle several of this package's.
func Options(opts ...Option) Option {
	return func(s *settings) { s.apply(opts) }
}

// WithClock sets the clock that times each attempt, and that dates a
// Retry-After header of a response without a Date header. Required. A
// production client passes a clock/hlc clock. net/http measures its own
// timeouts in real time.
func WithClock(c clock.Clock) Option {
	return func(s *settings) { s.clock = c }
}

// WithLogger sets the logger of the record of each call that fails.
// Required. A client without logs passes a logger of slog.DiscardHandler.
func WithLogger(l *slog.Logger) Option {
	return func(s *settings) { s.logger = l }
}

// WithReporter sets the reporter of the client's duration histogram and
// spans. Required. A client without metrics and spans passes the reporter
// of telemetry/noop.
func WithReporter(r telemetry.Reporter) Option {
	return func(s *settings) { s.reporter = r }
}

// WithPropagator sets the propagator that injects the trace of each attempt
// into its headers, such as the W3C Trace Context of telemetry/w3c.
// Required.
func WithPropagator(p telemetry.Propagator) Option {
	return func(s *settings) { s.propagator = p }
}

// WithHosts admits the hosts that the client may call: an IP address, a DNS
// name, or a DNS name after a dot, such as ".example.com", which admits
// example.com and its subdomains. Required. The client compares hosts
// without regard to case, and admits any port of a host.
func WithHosts(hosts ...string) Option {
	return func(s *settings) { s.hosts = append(s.hosts, hosts...) }
}

// WithReach sets the addresses that the client may connect to. The default
// is [ReachPublic].
func WithReach(r Reach) Option {
	return func(s *settings) { s.reach = r }
}

// WithBreaker guards each attempt with b, with a circuit per host. Without
// it, the client has no circuit breaker.
func WithBreaker(b *resilience.Breaker) Option {
	return func(s *settings) { s.breaker = b }
}

// WithRetrier retries a call that failed with a Transient error with r,
// when its request is safe to send again. Without it, the client sends each
// request once.
func WithRetrier(r *resilience.Retrier) Option {
	return func(s *settings) { s.retrier = r }
}

// WithTLS sets the TLS configuration of the connections of the client. The
// default is the configuration of net/http's Transport.
func WithTLS(cfg *tls.Config) Option {
	return func(s *settings) { s.tls = cfg }
}

// WithProxy sends each request through the proxy at u. The default is no
// proxy, whatever the environment sets. With a proxy, the address check of
// [ReachPublic] does not apply, because the dialer connects to the proxy:
// the proxy enforces the addresses that a deployment admits.
func WithProxy(u *url.URL) Option {
	return func(s *settings) { s.proxy = u }
}

// WithResolver sets the resolver of the names that the client dials. The
// default is net.DefaultResolver.
func WithResolver(r *net.Resolver) Option {
	return func(s *settings) { s.resolver = r }
}

// WithTimeout bounds one attempt, from its connection to the end of the
// read of its body. The default is 10 s. A caller bounds a whole call, its
// retries included, with the deadline of the request's context.
func WithTimeout(d time.Duration) Option {
	return func(s *settings) { s.timeout = d }
}

// WithDialTimeout bounds the connection of an attempt. The default is 5 s,
// where net/http waits 30 s.
func WithDialTimeout(d time.Duration) Option {
	return func(s *settings) { s.dialTimeout = d }
}

// WithTLSHandshakeTimeout bounds the TLS handshake of a connection. The
// default is 5 s, where net/http waits 10 s.
func WithTLSHandshakeTimeout(d time.Duration) Option {
	return func(s *settings) { s.tlsHandshakeTimeout = d }
}

// WithIdleConnTimeout closes a connection that idles longer in the pool of
// the client. The default is 90 s.
func WithIdleConnTimeout(d time.Duration) Option {
	return func(s *settings) { s.idleConnTimeout = d }
}

// WithMaxIdleConnsPerHost sets the connections per host that the pool of
// the client keeps open between calls. The default is 32, where net/http
// keeps 2 and closes the other connections of a burst that it opened. New
// refuses a value that is not positive.
func WithMaxIdleConnsPerHost(n int) Option {
	return func(s *settings) { s.maxIdleConnsPerHost = n }
}

// WithMaxResponseBytes bounds the body that [Client.Fetch] and
// [Client.AppendFetch] read. The default is 8 MiB.
func WithMaxResponseBytes(n int64) Option {
	return func(s *settings) { s.maxResponseBytes = n }
}

// WithPrepare calls prepare on the request of each attempt, after the
// client injected the trace and before it sends the request, so a token or
// a signature with a timestamp is fresh on every retry. prepare receives a
// copy of the caller's request with headers of its own. An error of prepare
// ends the call with that error.
func WithPrepare(prepare func(*http.Request) error) Option {
	return func(s *settings) { s.prepare = prepare }
}

// WithClassify replaces the classification of the responses. classify
// returns nil for a response that succeeded and an error otherwise, whose
// class decides the breaker and the retries, and which Fetch and
// AppendFetch return. A classify that reads the body sets a new one for the
// caller of Do.
func WithClassify(classify func(*http.Response) error) Option {
	return func(s *settings) { s.classify = classify }
}
