// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package httpclient_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/net/httpclient"
)

func TestReach(t *testing.T) {
	t.Parallel()

	t.Run("Valid", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give httpclient.Reach
			want bool
		}{
			{name: "reports true for ReachPublic", give: httpclient.ReachPublic, want: true},
			{name: "reports true for ReachPrivate", give: httpclient.ReachPrivate, want: true},
			{name: "reports false for a value outside the constants", give: 2},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, tt.give.Valid(), tt.want, "Valid must report the constants of Reach")
			})
		}
	})

	t.Run("ReachPublic", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			host string
		}{
			{name: "makes Do return ErrBlocked for a name that resolves to a loopback address", host: "localhost"},
			{name: "makes Do return ErrBlocked for a loopback address", host: "127.0.0.1"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				var hits atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
				t.Cleanup(srv.Close)

				u, err := url.Parse(srv.URL)
				assert.NoError(t, err, "the URL of the server must parse")

				c, err := httpclient.New(dependency, required, httpclient.WithHosts(tt.host))
				assert.NoError(t, err, "New must accept the options")

				_, _, err = get(t, c, "http://"+tt.host+":"+u.Port()+"/")
				assert.ErrorIs(t, err, httpclient.ErrBlocked, "Do must refuse the address")
				expect.Equal(t, errs.Classify(err), errs.Denied, "a refused address must keep the class of ErrBlocked")
				expect.Contains(t, err.Error(), "is not a public address", "the error must name the address")
				expect.Equal(t, hits.Load(), int32(0), "the server must receive no request")
			})
		}
	})

	t.Run("ReachPrivate", func(t *testing.T) {
		t.Parallel()

		t.Run("admits a loopback address", func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			t.Cleanup(srv.Close)

			c, err := httpclient.New(dependency, required,
				httpclient.WithHosts("127.0.0.1"), httpclient.WithReach(httpclient.ReachPrivate))
			assert.NoError(t, err, "New must accept the options")

			status, _, err := get(t, c, srv.URL)
			assert.NoError(t, err, "Do must connect to a loopback address")
			assert.Equal(t, status, http.StatusOK, "Do must return the response of the server")
		})
	})
}
