// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package httpserver

import (
	"log/slog"
	"net/http"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

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
			assert.NoError(t, err, "New must accept the required options")
			expect.Equal(t, s.addr, ":8080", "the address must be :8080")
			expect.Equal(t, s.srv.ReadHeaderTimeout, 5*time.Second, "the read header timeout must be 5 s")
			expect.Equal(t, s.srv.ReadTimeout, 30*time.Second, "the read timeout must be 30 s")
			expect.Equal(t, s.srv.WriteTimeout, 30*time.Second, "the write timeout must be 30 s")
			expect.Equal(t, s.srv.IdleTimeout, 120*time.Second, "the idle timeout must be 120 s")
			expect.Equal(t, s.srv.MaxHeaderBytes, 64<<10, "the header limit must be 64 KiB")
			expect.Equal(t, s.drainDelay, 5*time.Second, "the drain delay must be 5 s")
			expect.Equal(t, s.shutdownTimeout, 20*time.Second, "the shutdown timeout must be 20 s")

			c, ok := s.srv.Handler.(*chain)
			assert.True(t, ok, "the handler of net/http's Server must be the chain")
			expect.Equal(t, c.maxBody, int64(4<<20), "the body limit must be 4 MiB")

			a, ok := c.next.(*admission)
			assert.True(t, ok, "the chain without middleware must call the admission")
			expect.Equal(t, a.max, int64(unbounded), "the in-flight limit must be off")
		})

		t.Run("enables every protocol of net/http", func(t *testing.T) {
			t.Parallel()
			s, err := New(http.NotFoundHandler(), required)
			assert.NoError(t, err, "New must accept the required options")
			expect.True(t, s.srv.Protocols.HTTP1(), "the server must speak HTTP/1.1")
			expect.True(t, s.srv.Protocols.HTTP2(), "the server must speak HTTP/2 over TLS")
			expect.True(t, s.srv.Protocols.UnencryptedHTTP2(), "the server must speak HTTP/2 in cleartext")
		})

		t.Run("turns a limit off for a negative value", func(t *testing.T) {
			t.Parallel()
			s, err := New(http.NotFoundHandler(), required,
				WithReadTimeout(-1), WithShutdownTimeout(-5), WithMaxBodyBytes(-7), WithMaxInFlight(-3))
			assert.NoError(t, err, "New must accept negative limits")
			expect.Equal(t, s.srv.ReadTimeout, time.Duration(-1), "net/http turns a negative timeout off")
			expect.Equal(t, s.shutdownTimeout, noTimeout, "the shutdown timeout must be off")

			c, ok := s.srv.Handler.(*chain)
			assert.True(t, ok, "the handler of net/http's Server must be the chain")
			expect.Equal(t, c.maxBody, int64(unbounded), "the body limit must be off")

			a, ok := c.next.(*admission)
			assert.True(t, ok, "the chain without middleware must call the admission")
			expect.Equal(t, a.max, int64(unbounded), "the in-flight limit must be off")
		})
	})
}
