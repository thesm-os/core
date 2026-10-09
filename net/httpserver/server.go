// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"sync/atomic"
	"time"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/net/internal/semconv"
)

// tcp is the network of the listener that Run opens on the address.
const tcp = "tcp"

// noTimeout is the shutdown timeout that a Server stores for a negative
// WithShutdownTimeout: the shutdown waits for every request in flight. New
// refuses a zero timeout, so zero is free to mean none.
const noTimeout time.Duration = 0

// msgNotReady is the message of the log record of a ready check that
// failed.
const msgNotReady = "ready check failed"

// Server serves HTTP through net/http's Server, with the limits, the
// telemetry, the recovery and the drain that the package documentation
// describes. [New] builds it, and [Server.Run] serves until a context ends.
//
// # Concurrency
//
// Safe for concurrent use. Run serves once. Addr, Live and Ready are safe
// to call while Run serves.
//
// # Allocation contract
//
// New allocates the server, its chain and its instruments. A request
// allocates as the package documentation states.
type Server struct {
	srv      *http.Server
	listener net.Listener
	clock    clock.Clock
	logger   *slog.Logger

	// bound is the address of the listener that Run serves on, once it
	// listens.
	bound atomic.Pointer[net.Addr]

	addr        string
	readyChecks []func(context.Context) error

	drainDelay time.Duration

	// shutdownTimeout is the shutdown timeout, or noTimeout.
	shutdownTimeout time.Duration

	// ready reports that Run serves and has not started to drain.
	ready atomic.Bool

	// ran reports that Run was called.
	ran atomic.Bool
}

// New returns a Server of h with opts. opts include [WithClock],
// [WithLogger], [WithReporter] and [WithPropagator], which New requires.
// Without other options, the Server listens on ":8080" with the default
// limits of the package documentation.
//
// New wraps h in the chain of the package documentation, and builds the
// http.Server with the timeouts, the header limit and the protocols: HTTP/1.1
// and HTTP/2, over TLS with [WithTLS] and in cleartext otherwise. net/http
// logs its own errors, such as a failed TLS handshake, through the logger at
// slog.LevelWarn.
//
// Error modes: an error that wraps [ErrConfig], classified Invalid, for a nil
// h, for a missing or refused option value, for a trusted origin that is not
// of the form scheme://host[:port], and for a middleware that returns a nil
// handler. The error names the problem.
func New(h http.Handler, opts ...Option) (*Server, error) {
	if h == nil {
		return nil, fmt.Errorf("%w: the handler is nil", ErrConfig)
	}

	s := settings{
		addr:              ":8080",
		readHeaderTimeout: 5 * time.Second,
		readTimeout:       30 * time.Second,
		writeTimeout:      30 * time.Second,
		idleTimeout:       120 * time.Second,
		maxHeaderBytes:    64 << 10,
		maxBodyBytes:      4 << 20,
		drainDelay:        5 * time.Second,
		shutdownTimeout:   20 * time.Second,
	}
	s.apply(opts)

	if problem := s.problem(); problem != "" {
		return nil, fmt.Errorf("%w: %s", ErrConfig, problem)
	}

	origins := http.NewCrossOriginProtection()
	for _, origin := range s.trustedOrigins {
		if err := origins.AddTrustedOrigin(origin); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrConfig, err)
		}
	}

	var next http.Handler = &admission{handler: h, origins: origins, max: max(int64(s.maxInFlight), unbounded)}
	for i, mw := range slices.Backward(s.middleware) {
		if next = mw(next); next == nil {
			return nil, fmt.Errorf("%w: the middleware at index %d returned a nil handler", ErrConfig, i)
		}
	}

	durations := semconv.NewDurations(s.reporter, semconv.ServerDuration, durationDescription, s.clock, series.attrs)
	c := &chain{
		next:       next,
		clock:      s.clock,
		logger:     s.logger,
		tracer:     s.reporter.Tracer(scope),
		propagator: s.propagator,
		durations:  durations,
		maxBody:    max(s.maxBodyBytes, unbounded),
	}

	var protocols http.Protocols
	protocols.SetHTTP1(true)
	protocols.SetHTTP2(true)
	protocols.SetUnencryptedHTTP2(true)

	return &Server{
		srv: &http.Server{
			Handler:           c,
			TLSConfig:         s.tls,
			ReadHeaderTimeout: s.readHeaderTimeout,
			ReadTimeout:       s.readTimeout,
			WriteTimeout:      s.writeTimeout,
			IdleTimeout:       s.idleTimeout,
			MaxHeaderBytes:    s.maxHeaderBytes,
			ErrorLog:          slog.NewLogLogger(s.logger.Handler(), slog.LevelWarn),
			Protocols:         &protocols,
		},
		listener:        s.listener,
		clock:           s.clock,
		logger:          s.logger,
		addr:            s.addr,
		readyChecks:     s.readyChecks,
		drainDelay:      s.drainDelay,
		shutdownTimeout: max(s.shutdownTimeout, noTimeout),
	}, nil
}

// Run listens, and serves until ctx ends. It then drains: [Server.Ready]
// responds with 503 from that moment, the server keeps serving for the
// drain delay, and http.Server.Shutdown then stops accepting connections
// and waits for the requests in flight, within the shutdown timeout. A
// drain that finishes returns nil. A ctx that has ended before Run starts a
// drain at once.
//
// Run listens on the listener of [WithListener], or on the TCP address of
// [WithAddr], and closes the listener when it returns.
//
// Error modes:
//
//   - [ErrClosed], classified Invalid, for a Server whose Run was called
//     before.
//   - [ErrListen], classified Transient, joined with the error of
//     net.ListenConfig.Listen, for an address that Run cannot listen on.
//   - [ErrServe], classified Transient, joined with the error of
//     http.Server.Serve, when serving fails, such as a listener whose
//     Accept fails.
//   - [ErrShutdown], joined with context.DeadlineExceeded, when the
//     shutdown timeout elapses with requests in flight. Run then closes
//     their connections. ErrShutdown joined with the error of closing a
//     listener, when the close fails.
func (s *Server) Run(ctx context.Context) error {
	if !s.ran.CompareAndSwap(false, true) {
		return ErrClosed
	}

	ln, err := s.listen(ctx)
	if err != nil {
		return err
	}

	addr := ln.Addr()
	s.bound.Store(&addr)

	// Ready reports 200 from before the first request, which the serving
	// goroutine serves after it starts.
	s.ready.Store(true)

	served := make(chan error, 1)
	go func() { served <- s.serve(ln) }()

	select {
	case err := <-served:
		s.ready.Store(false)

		return fmt.Errorf("%w: %w", ErrServe, err)
	case <-ctx.Done():
		return s.drain(context.WithoutCancel(ctx), served)
	}
}

// Addr returns the address that Run listens on, and nil before it listens.
// With [WithAddr] of port 0, it returns the port that the system chose.
func (s *Server) Addr() net.Addr {
	if a := s.bound.Load(); a != nil {
		return *a
	}

	return nil
}

// Live returns a handler that responds with 200 to every request while the
// process runs. A deployment serves it as its liveness probe.
func (*Server) Live() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
}

// Ready returns a handler that responds with 200 while Run serves and every
// check of [WithReadyCheck] returns nil for the context of the probe's
// request. It responds with 503 before Run listens, from the start of the
// drain, after Run returns, and while a check returns an error, which it
// logs at slog.LevelWarn. A deployment serves it as its readiness probe, on
// a second Server, so that it responds with 503 for the whole drain of this
// one.
func (s *Server) Ready() http.Handler { return http.HandlerFunc(s.serveReady) }

// serveReady responds to a readiness probe, as Ready describes.
func (s *Server) serveReady(w http.ResponseWriter, r *http.Request) {
	if !s.ready.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)

		return
	}

	for _, check := range s.readyChecks {
		if err := check(r.Context()); err != nil {
			s.logger.LogAttrs(r.Context(), slog.LevelWarn, msgNotReady, slog.Any(keyError, err))
			w.WriteHeader(http.StatusServiceUnavailable)

			return
		}
	}

	w.WriteHeader(http.StatusOK)
}

// listen returns the listener of WithListener, or a TCP listener on the
// address.
func (s *Server) listen(ctx context.Context) (net.Listener, error) {
	if s.listener != nil {
		return s.listener, nil
	}

	var lc net.ListenConfig

	ln, err := lc.Listen(ctx, tcp, s.addr)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrListen, s.addr, err)
	}

	return ln, nil
}

// serve serves on ln, over TLS when the Server has a TLS configuration,
// until the server shuts down or serving fails.
func (s *Server) serve(ln net.Listener) error {
	if s.srv.TLSConfig != nil {
		return s.srv.ServeTLS(ln, "", "") //nolint:wrapcheck // Run wraps it in ErrServe
	}

	return s.srv.Serve(ln) //nolint:wrapcheck // Run wraps it in ErrServe
}

// drain stops a Server whose Run context has ended, as Run describes.
// served receives the result of serve. ctx is the Run context without its
// cancellation, the parent of the shutdown's context.
func (s *Server) drain(ctx context.Context, served <-chan error) error {
	s.ready.Store(false)

	// The clock expires a timer of a negative delay at once.
	clock.Sleep(s.clock, s.drainDelay)

	if s.shutdownTimeout != noTimeout {
		var cancel context.CancelFunc

		ctx, cancel = s.expire(ctx, s.shutdownTimeout)
		defer cancel()
	}

	err := s.srv.Shutdown(ctx)
	//dokimi:mutate-skip ror-true: Shutdown returns nil after its context ended only when the timer fires as the last connection closes, which no test can order
	if err != nil {
		// Shutdown returns once its context ends, and leaves the connections
		// of the requests in flight open.
		if ctx.Err() != nil {
			_ = s.srv.Close()
			//dokimi:mutate-skip sbr-delete: served has room for the result of serve, which then ends a moment after Run returns
			<-served

			return fmt.Errorf("%w: the timeout of %s elapsed: %w",
				ErrShutdown, s.shutdownTimeout, context.DeadlineExceeded)
		}
	}

	if serveErr := <-served; !errors.Is(serveErr, http.ErrServerClosed) {
		return fmt.Errorf("%w: %w", ErrServe, serveErr)
	}

	if err != nil {
		return fmt.Errorf("%w: %w", ErrShutdown, err)
	}

	return nil
}

// expire returns a context of parent that ends when d elapses on the
// Server's clock, and its cancel function, which stops the timer.
func (s *Server) expire(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	t := s.clock.NewTimer(d)

	go func() {
		//dokimi:mutate-skip sbr-delete: a timer of the clock that runs no goroutine has no effect after the drain that a test can observe
		defer t.Stop()

		select {
		case <-t.C():
			cancel()
		case <-ctx.Done():
		}
	}()

	return ctx, cancel
}
