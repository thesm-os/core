// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package httpserver

import (
	"log/slog"
	"net/http"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/telemetry/noop"
	"go.thesmos.sh/core/telemetry/w3c"
)

// origin is the time of the fake clocks of the internal cases.
var origin = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

// required bundles the four options that New requires, for the internal
// cases.
var required = Options(
	WithClock(fake.New(origin)),
	WithLogger(slog.New(slog.DiscardHandler)),
	WithReporter(noop.Reporter{}),
	WithPropagator(w3c.Propagator{}),
)

func TestServerInternal(t *testing.T) {
	t.Parallel()

	t.Run("New", func(t *testing.T) {
		t.Parallel()

		t.Run("sets the documented default of every limit", func(t *testing.T) {
			t.Parallel()

			s, err := New(http.NotFoundHandler(), required)
			testkit.NoError(t, err, "New must accept the required options")

			testkit.Equal(t, s.addr, ":8080", "the address")
			testkit.Equal(t, s.srv.ReadHeaderTimeout, 5*time.Second, "the read header timeout")
			testkit.Equal(t, s.srv.ReadTimeout, 30*time.Second, "the read timeout")
			testkit.Equal(t, s.srv.WriteTimeout, 30*time.Second, "the write timeout")
			testkit.Equal(t, s.srv.IdleTimeout, 120*time.Second, "the idle timeout")
			testkit.Equal(t, s.srv.MaxHeaderBytes, 64<<10, "the header limit")
			testkit.Equal(t, s.drainDelay, 5*time.Second, "the drain delay")
			testkit.Equal(t, s.shutdownTimeout, 20*time.Second, "the shutdown timeout")

			c, ok := s.srv.Handler.(*chain)
			testkit.True(t, ok, "the handler of net/http's Server must be the chain")
			testkit.Equal(t, c.maxBody, int64(4<<20), "the body limit")

			a, ok := c.next.(*admission)
			testkit.True(t, ok, "the chain without middleware must call the admission")
			testkit.Equal(t, a.max, int64(unbounded), "the in-flight limit")
		})

		t.Run("serves HTTP/1.1, HTTP/2 over TLS and HTTP/2 in cleartext", func(t *testing.T) {
			t.Parallel()

			s, err := New(http.NotFoundHandler(), required)
			testkit.NoError(t, err, "New must accept the required options")

			p := s.srv.Protocols
			testkit.True(t, p.HTTP1() && p.HTTP2() && p.UnencryptedHTTP2(), "the protocols of net/http's Server")
		})

		t.Run("turns a limit off for a negative value", func(t *testing.T) {
			t.Parallel()

			s, err := New(http.NotFoundHandler(), required,
				WithReadTimeout(-1), WithShutdownTimeout(-5), WithMaxBodyBytes(-7), WithMaxInFlight(-3))
			testkit.NoError(t, err, "New must accept negative limits")

			testkit.Equal(t, s.srv.ReadTimeout, time.Duration(-1), "net/http turns a negative timeout off")
			testkit.Equal(t, s.shutdownTimeout, noTimeout, "the shutdown timeout")

			c, ok := s.srv.Handler.(*chain)
			testkit.True(t, ok, "the handler of net/http's Server must be the chain")
			testkit.Equal(t, c.maxBody, int64(unbounded), "the body limit")

			a, ok := c.next.(*admission)
			testkit.True(t, ok, "the chain without middleware must call the admission")
			testkit.Equal(t, a.max, int64(unbounded), "the in-flight limit")
		})
	})
}
