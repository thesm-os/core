// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package blobtest_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/conform"

	"go.thesmos.sh/core/blob"
	"go.thesmos.sh/core/blob/memory"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/coretest/blobtest"
)

// The path of the base URL of a served Server, and the timeout of a request of
// a check to its httptest server.
const (
	servedPath    = "/blobs"
	signerTimeout = 5 * time.Second
)

// clockStep is the step of the clock of a store whose clock moves at each read.
const clockStep = time.Millisecond

// tickingClock is the fake clock of a store that advances by step at each read
// of its time, as a real clock moves between two reads.
type tickingClock struct {
	*fake.Clock

	step time.Duration
}

// Time advances the clock by step and returns its time.
func (c tickingClock) Time() time.Time {
	c.Advance(c.step)

	return c.Clock.Time()
}

// served is the Server of a store in memory that an httptest server serves,
// with the Backend of the checks of its signers.
type served struct {
	*memory.Server

	backend clockBackend
}

// newServed returns the Server of an empty store over a fake clock at origin,
// which advances by step at each read of the store. An httptest server serves
// the Server at servedPath until the test of tb ends.
func newServed(tb assert.TB, step time.Duration) *served {
	tb.Helper()

	ts := httptest.NewUnstartedServer(nil)
	assert.Cleanup(tb, ts.Close)

	clk := fake.New(origin)
	base := &url.URL{Scheme: "http", Host: ts.Listener.Addr().String(), Path: servedPath}
	srv, err := memory.NewServer(memory.New(tickingClock{Clock: clk, step: step}), base)
	assert.NoError(tb, err, "NewServer takes the URL of an httptest server")

	ts.Config.Handler = srv
	ts.Start()

	client := ts.Client()
	client.Timeout = signerTimeout

	return &served{Server: srv, backend: clockBackend{client: client, clock: clk, delay: step}}
}

// clockBackend is the Backend of a served Server. Its clock is the fake clock
// under the clock of the store, which does not advance at a read of the check.
type clockBackend struct {
	client *http.Client
	clock  *fake.Clock

	// delay is the step of the clock of the store, which the store reads one
	// step after each read of the check.
	delay time.Duration
}

// Do sends req with the client of the httptest server.
func (b clockBackend) Do(req *http.Request) (*http.Response, error) {
	return b.client.Do(req)
}

// Time returns the time of the fake clock of the store.
func (b clockBackend) Time() time.Time {
	return b.clock.Time()
}

// SignDelay returns the step of the clock of the store.
func (b clockBackend) SignDelay() time.Duration {
	return b.delay
}

// Elapse advances the fake clock of the store by d. It returns the error of
// ctx when ctx has ended before the call.
func (b clockBackend) Elapse(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	b.clock.Advance(d)

	return nil
}

// TestSign checks that the Server of a store in memory passes the checks of the
// suites of blob.GetSigner and blob.CreateSigner, over a clock that does not
// move between two reads and over a clock that moves at each read.
func TestSign(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		giveStep time.Duration
	}{
		{name: "passes the Server of a store in memory", giveStep: 0},
		{name: "passes the Server of a store whose clock moves at each read", giveStep: clockStep},
	}

	t.Run("GetSigner", func(t *testing.T) {
		t.Parallel()

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				blobtest.RunGetSignerSuiteFromHandle(t,
					func(h *blobtest.GetSignerHandle) *served { return newServed(h, tt.giveStep) },
					conform.Supplies(conform.Supply(backendOf[blob.GetSigner])))
			})
		}
	})

	t.Run("CreateSigner", func(t *testing.T) {
		t.Parallel()

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				blobtest.RunCreateSignerSuiteFromHandle(t,
					func(h *blobtest.CreateSignerHandle) *served { return newServed(h, tt.giveStep) },
					conform.Supplies(conform.Supply(backendOf[blob.CreateSigner])))
			})
		}
	})
}

// backendOf returns the Backend of the served Server s, and stops the check of
// h when s is no served Server.
func backendOf[S any](h *conform.Handle[S], s S) blobtest.Backend {
	h.Helper()

	srv, ok := any(s).(*served)
	assert.True(h, ok, "the subject is a served Server")

	return srv.backend
}
