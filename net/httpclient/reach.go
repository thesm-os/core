// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package httpclient

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"syscall"
)

// Reach states the addresses that a Client may connect to.
type Reach uint8

const (
	// ReachPublic refuses a connection to an address that is not public:
	// loopback, private, link-local, unspecified, multicast and broadcast
	// addresses, and the special-purpose ranges of IANA that are not
	// globally reachable, such as 100.64.0.0/10 of carrier-grade NAT and
	// 198.18.0.0/15 of benchmarking. It checks an IPv4 address that IPv6
	// maps, and the IPv4 address of a NAT64 address of 64:ff9b::/96, as
	// that IPv4 address. It is the zero value of Reach.
	ReachPublic Reach = 0

	// ReachPrivate admits every address, for calls inside a deployment.
	ReachPrivate Reach = 1
)

// Valid reports whether r is one of the constants of Reach.
func (r Reach) Valid() bool { return r <= ReachPrivate }

// hostChars are the bytes of a label of a DNS name that [WithHosts]
// accepts: letters, digits, the hyphen, and the underscore of service
// names.
const hostChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"

// reserved lists the special-purpose ranges of IANA that are not globally
// reachable and that netip's IsGlobalUnicast and IsPrivate admit.
var reserved = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // this network
	netip.MustParsePrefix("100.64.0.0/10"),   // shared address space of carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // documentation
	netip.MustParsePrefix("192.88.99.0/24"),  // the deprecated relays of 6to4
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // documentation
	netip.MustParsePrefix("203.0.113.0/24"),  // documentation
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved
	netip.MustParsePrefix("64:ff9b:1::/48"),  // local-use NAT64
	netip.MustParsePrefix("100::/64"),        // discard-only
	netip.MustParsePrefix("2001::/23"),       // IETF protocol assignments, Teredo among them
	netip.MustParsePrefix("2001:db8::/32"),   // documentation
	netip.MustParsePrefix("2002::/16"),       // 6to4
	netip.MustParsePrefix("fec0::/10"),       // deprecated site-local
}

// nat64 is the well-known prefix of NAT64. An address of it contains an
// IPv4 address in its last four bytes, which a NAT64 gateway connects to.
var nat64 = netip.MustParsePrefix("64:ff9b::/96")

// checkAddress is the ControlContext of the dialer of a client of
// ReachPublic. The dialer calls it for each address of a name, after the
// resolver returned it and before the connection starts, so a name that
// resolves to an internal address fails whatever an earlier resolution
// returned. It returns an error that wraps [ErrBlocked] for an address that
// is not public, and the dialer then tries the next address of the name.
func checkAddress(_ context.Context, _, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("%w: %s is not an address and a port", ErrBlocked, address)
	}

	a := ap.Addr().Unmap()
	if nat64.Contains(a) {
		b := a.As16()
		a = netip.AddrFrom4([4]byte(b[12:]))
	}

	special := slices.ContainsFunc(reserved, func(p netip.Prefix) bool { return p.Contains(a) })
	if !a.IsGlobalUnicast() || a.IsPrivate() || special {
		return fmt.Errorf("%w: %s is not a public address", ErrBlocked, a)
	}

	return nil
}

// hosts is the set of hosts that [WithHosts] admits, in lower case: exact
// hosts, and suffixes of the form .example.com, which admit example.com
// and its subdomains.
type hosts struct {
	exact    []string
	suffixes []string
}

// admits reports whether hs admits host, the host name of a URL, compared
// without regard to case.
func (hs hosts) admits(host string) bool {
	for _, h := range hs.exact {
		if strings.EqualFold(host, h) {
			return true
		}
	}

	for _, s := range hs.suffixes {
		if strings.EqualFold(host, s[1:]) {
			return true
		}

		if n := len(host) - len(s); n > 0 && strings.EqualFold(host[n:], s) {
			return true
		}
	}

	return false
}

// validHost reports whether h is a host that [WithHosts] accepts: an IP
// address, a DNS name, or a DNS name after a dot. A DNS name consists of
// labels of the bytes of hostChars, separated by single dots.
func validHost(h string) bool {
	name := strings.TrimPrefix(h, ".")
	if _, err := netip.ParseAddr(name); err == nil {
		// An IP address has no subdomains.
		return name == h
	}

	for label := range strings.SplitSeq(name, ".") {
		if label == "" || strings.Trim(label, hostChars) != "" {
			return false
		}
	}

	return true
}
