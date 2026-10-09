// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package httpserver

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"time"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/telemetry"
)

// Option configures a [Server]. [New] applies the options in order, so a
// later option overrides an earlier one, and then checks the result once.
// An option does not fail itself: New returns [ErrConfig] for a value that
// it refuses. [WithMiddleware], [WithReadyCheck] and [WithTrustedOrigins]
// add to what earlier calls added.
//
// [WithClock], [WithLogger], [WithReporter] and [WithPropagator] are
// required, because a Server reads no process-wide default: New refuses a
// Server without any of them. Every limit has a default.
//
// A negative duration turns off the limit that it sets, as a negative
// timeout does in net/http. Zero is out of range for every duration and
// size. net/http reads a zero timeout as no timeout, so a configuration
// that leaves a field unset would turn a protection off without notice.
//
// A consumer builds its own options from these, and bundles several with
// [Options], such as the four required options of every server of a
// process.
type Option func(*settings)

// settings is the configuration of a Server: the defaults, with the options
// applied in order.
type settings struct {
	listener       net.Listener
	tls            *tls.Config
	logger         *slog.Logger
	reporter       telemetry.Reporter
	propagator     telemetry.Propagator
	clock          clock.Clock
	addr           string
	middleware     []func(http.Handler) http.Handler
	readyChecks    []func(context.Context) error
	trustedOrigins []string

	readHeaderTimeout time.Duration
	readTimeout       time.Duration
	writeTimeout      time.Duration
	idleTimeout       time.Duration
	drainDelay        time.Duration
	shutdownTimeout   time.Duration
	maxBodyBytes      int64
	maxHeaderBytes    int

	// maxInFlight is the bound of WithMaxInFlight, and zero without one.
	maxInFlight int

	// listenerSet, tlsSet and inFlightSet report that WithListener, WithTLS
	// and WithMaxInFlight were applied, so that problem refuses a nil
	// listener, a nil TLS configuration and a zero bound. Without them,
	// these values would read as no listener, no TLS and no bound.
	listenerSet bool
	tlsSet      bool
	inFlightSet bool

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
	_, _, addrErr := net.SplitHostPort(s.addr)
	nilMiddleware := slices.ContainsFunc(s.middleware, func(mw func(http.Handler) http.Handler) bool {
		return mw == nil
	})
	nilCheck := slices.ContainsFunc(s.readyChecks, func(check func(context.Context) error) bool {
		return check == nil
	})

	problems := [...]struct {
		what    string
		refused bool
	}{
		{"an option is nil", s.nilOption},
		{"the clock is nil or missing: New requires WithClock", s.clock == nil},
		{"the logger is nil or missing: New requires WithLogger", s.logger == nil},
		{"the reporter is nil or missing: New requires WithReporter", s.reporter == nil},
		{"the propagator is nil or missing: New requires WithPropagator", s.propagator == nil},
		{"the listener is nil", s.listenerSet && s.listener == nil},
		{"the address is not of the form host:port", s.listener == nil && addrErr != nil},
		{"the TLS configuration is nil", s.tlsSet && s.tls == nil},
		{"a middleware is nil", nilMiddleware},
		{"a ready check is nil", nilCheck},
		{"the read header timeout is zero", s.readHeaderTimeout == 0},
		{"the read timeout is zero", s.readTimeout == 0},
		{"the write timeout is zero", s.writeTimeout == 0},
		{"the idle timeout is zero", s.idleTimeout == 0},
		{"the drain delay is zero", s.drainDelay == 0},
		{"the shutdown timeout is zero", s.shutdownTimeout == 0},
		{"the header limit is not positive", s.maxHeaderBytes <= 0},
		{"the body limit is zero", s.maxBodyBytes == 0},
		{"the in-flight limit is zero", s.inFlightSet && s.maxInFlight == 0},
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

// WithClock sets the clock that times each request, the drain delay and
// the shutdown timeout. Required. A production server passes a clock/hlc
// clock, and a test passes a fake clock to time the drain without waiting.
// net/http measures its own timeouts in real time.
func WithClock(c clock.Clock) Option {
	return func(s *settings) { s.clock = c }
}

// WithLogger sets the logger of the log record of each request and of the
// errors of net/http, such as a failed TLS handshake. Required. A server
// without logs passes a logger of slog.DiscardHandler.
func WithLogger(l *slog.Logger) Option {
	return func(s *settings) { s.logger = l }
}

// WithReporter sets the reporter of the server's duration histogram and
// spans. Required. A server without metrics and spans passes the reporter
// of telemetry/noop.
func WithReporter(r telemetry.Reporter) Option {
	return func(s *settings) { s.reporter = r }
}

// WithPropagator sets the propagator that extracts the caller's trace from
// the headers of a request, such as the W3C Trace Context of telemetry/w3c.
// Required.
func WithPropagator(p telemetry.Propagator) Option {
	return func(s *settings) { s.propagator = p }
}

// WithAddr sets the TCP address that Run listens on, of the form host:port.
// An empty host listens on every interface, and port 0 on a port that the
// system chooses. The default is ":8080". [WithListener] takes precedence.
func WithAddr(addr string) Option {
	return func(s *settings) { s.addr = addr }
}

// WithListener makes Run serve on ln, a listener of the caller, in place of
// a listener on the address. Run closes ln when it returns. New refuses a
// nil ln.
func WithListener(ln net.Listener) Option {
	return func(s *settings) { s.listener, s.listenerSet = ln, true }
}

// WithTLS makes Run serve TLS with cfg, which provides the certificates
// through Certificates or GetCertificate. The server then negotiates
// HTTP/2 and HTTP/1.1 through ALPN. Without WithTLS, the server serves
// cleartext HTTP/1.1, and HTTP/2 to a client that starts with its preface.
// New refuses a nil cfg, so a missing configuration does not serve
// cleartext.
func WithTLS(cfg *tls.Config) Option {
	return func(s *settings) { s.tls, s.tlsSet = cfg, true }
}

// WithReadHeaderTimeout bounds the time in which a client sends the headers
// of a request. The default is 5 s.
func WithReadHeaderTimeout(d time.Duration) Option {
	return func(s *settings) { s.readHeaderTimeout = d }
}

// WithReadTimeout bounds the time in which a client sends a request, its
// body included. The default is 30 s.
func WithReadTimeout(d time.Duration) Option {
	return func(s *settings) { s.readTimeout = d }
}

// WithWriteTimeout bounds the time from the end of the headers of a request
// to the end of the write of its response. The default is 30 s. A handler
// that streams sets its own deadline with
// http.ResponseController.SetWriteDeadline.
func WithWriteTimeout(d time.Duration) Option {
	return func(s *settings) { s.writeTimeout = d }
}

// WithIdleTimeout bounds the time that a keep-alive connection waits for
// its next request. The default is 120 s.
func WithIdleTimeout(d time.Duration) Option {
	return func(s *settings) { s.idleTimeout = d }
}

// WithMaxHeaderBytes bounds the bytes of the request line and the headers
// of a request. The default is 64 KiB. net/http has no unbounded header
// size, so New refuses a value that is not positive.
func WithMaxHeaderBytes(n int) Option {
	return func(s *settings) { s.maxHeaderBytes = n }
}

// WithMaxBodyBytes bounds the bytes of a request body that the middleware
// and the handler can read. The default is 4 MiB. A request that declares a
// longer body receives 413 before the middleware runs. A read of a body of
// unknown length beyond the bound returns an *http.MaxBytesError, which
// [Error] writes as 413, and the server closes the connection after the
// response.
func WithMaxBodyBytes(n int64) Option {
	return func(s *settings) { s.maxBodyBytes = n }
}

// WithDrainDelay sets the time between the end of the context of Run and
// the start of the shutdown, during which Ready responds with 503 and the
// server still serves. The default is 5 s.
func WithDrainDelay(d time.Duration) Option {
	return func(s *settings) { s.drainDelay = d }
}

// WithShutdownTimeout bounds the time that the shutdown waits for the
// requests in flight before it closes their connections. The default is
// 20 s.
func WithShutdownTimeout(d time.Duration) Option {
	return func(s *settings) { s.shutdownTimeout = d }
}

// WithMaxInFlight bounds the requests that the handler serves at once to n,
// and the server responds to each request beyond the bound with 503 and
// Retry-After: 1. Without it, or for a negative n, the server sets no bound.
// New refuses zero.
func WithMaxInFlight(n int) Option {
	return func(s *settings) { s.maxInFlight, s.inFlightSet = n, true }
}

// WithTrustedOrigins admits the cross-origin requests of origins, each of
// the form scheme://host[:port], through the cross-origin protection.
func WithTrustedOrigins(origins ...string) Option {
	return func(s *settings) { s.trustedOrigins = append(s.trustedOrigins, origins...) }
}

// WithMiddleware adds mw to the chain of each request, inside the recovery,
// the span, the log record and the body limit, and outside the in-flight
// limit and the cross-origin protection. The first middleware is the
// outermost.
func WithMiddleware(mw ...func(http.Handler) http.Handler) Option {
	return func(s *settings) { s.middleware = append(s.middleware, mw...) }
}

// WithReadyCheck adds check to readiness: Ready responds with 503 while
// check returns an error for the context of the probe's request.
func WithReadyCheck(check func(context.Context) error) Option {
	return func(s *settings) { s.readyChecks = append(s.readyChecks, check) }
}
