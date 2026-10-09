// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package httpclient

import (
	"testing"

	"go.dokimi.dev/assert"
)

func TestReachInternal(t *testing.T) {
	t.Parallel()

	t.Run("checkAddress", func(t *testing.T) {
		t.Parallel()

		admitted := []struct {
			name    string
			address string
		}{
			{name: "returns nil for a public IPv4 address", address: "8.8.8.8:443"},
			{name: "returns nil for a public IPv6 address", address: "[2001:4860:4860::8888]:443"},
			{name: "returns nil for the NAT64 address of a public IPv4 address", address: "[64:ff9b::808:808]:443"},
		}
		for _, tt := range admitted {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.NoError(t, checkAddress(t.Context(), "tcp", tt.address, nil), "the address must be public")
			})
		}

		blocked := []struct {
			name    string
			address string
		}{
			{name: "returns ErrBlocked for an address without a port", address: "10.0.0.1"},
			{name: "returns ErrBlocked for the IPv4 loopback address", address: "127.0.0.1:80"},
			{name: "returns ErrBlocked for the IPv6 loopback address", address: "[::1]:80"},
			{name: "returns ErrBlocked for a private IPv4 address", address: "10.1.2.3:80"},
			{name: "returns ErrBlocked for a unique local IPv6 address", address: "[fd00::1]:80"},
			{name: "returns ErrBlocked for a link-local IPv4 address", address: "169.254.169.254:80"},
			{name: "returns ErrBlocked for a link-local IPv6 address", address: "[fe80::1]:80"},
			{name: "returns ErrBlocked for the unspecified IPv4 address", address: "0.0.0.0:80"},
			{name: "returns ErrBlocked for the unspecified IPv6 address", address: "[::]:80"},
			{name: "returns ErrBlocked for a multicast address", address: "224.0.0.1:80"},
			{name: "returns ErrBlocked for the broadcast address", address: "255.255.255.255:80"},
			{name: "returns ErrBlocked for a private IPv4 address that IPv6 maps", address: "[::ffff:10.0.0.1]:80"},
			{
				name:    "returns ErrBlocked for the NAT64 address of a private IPv4 address",
				address: "[64:ff9b::a00:1]:80",
			},
			{name: "returns ErrBlocked for an address of this network in 0.0.0.0/8", address: "0.1.2.3:80"},
			{name: "returns ErrBlocked for an address of carrier-grade NAT in 100.64.0.0/10", address: "100.64.0.1:80"},
			{name: "returns ErrBlocked for an IETF protocol assignment in 192.0.0.0/24", address: "192.0.0.8:80"},
			{name: "returns ErrBlocked for a documentation address of 192.0.2.0/24", address: "192.0.2.1:80"},
			{name: "returns ErrBlocked for a relay of 6to4 in 192.88.99.0/24", address: "192.88.99.1:80"},
			{name: "returns ErrBlocked for a benchmarking address in 198.18.0.0/15", address: "198.19.0.1:80"},
			{name: "returns ErrBlocked for a documentation address of 198.51.100.0/24", address: "198.51.100.1:80"},
			{name: "returns ErrBlocked for a documentation address of 203.0.113.0/24", address: "203.0.113.1:80"},
			{name: "returns ErrBlocked for a reserved address in 240.0.0.0/4", address: "240.0.0.1:80"},
			{name: "returns ErrBlocked for a local-use NAT64 address in 64:ff9b:1::/48", address: "[64:ff9b:1::1]:80"},
			{name: "returns ErrBlocked for a discard-only address in 100::/64", address: "[100::1]:80"},
			{name: "returns ErrBlocked for an IETF protocol assignment in 2001::/23", address: "[2001::1]:80"},
			{name: "returns ErrBlocked for a documentation address of 2001:db8::/32", address: "[2001:db8::1]:80"},
			{name: "returns ErrBlocked for a 6to4 address in 2002::/16", address: "[2002::1]:80"},
			{name: "returns ErrBlocked for a site-local address in fec0::/10", address: "[fec0::1]:80"},
		}
		for _, tt := range blocked {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.ErrorIs(t, checkAddress(t.Context(), "tcp", tt.address, nil), ErrBlocked,
					"checkAddress must refuse the address")
			})
		}

		t.Run("names an address without a port in its error", func(t *testing.T) {
			t.Parallel()
			err := checkAddress(t.Context(), "tcp", "10.0.0.1", nil)
			assert.ErrorIs(t, err, ErrBlocked, "checkAddress must refuse an address without a port")
			assert.Contains(t, err.Error(), "10.0.0.1 is not an address and a port",
				"the error must state that the address has no port")
		})
	})
}
