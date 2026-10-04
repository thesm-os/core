// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package httpclient

//go:generate testkit sentinel -o errors.gen_test.go

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

// Sentinel errors returned by this package. Each classifies under
// [go.thesmos.sh/core/errs.Classify]. A call joins a sentinel with the
// detail that caused it, so errors.Is matches the sentinel.
var (
	// ErrConfig is returned by [New] for a configuration that it refuses:
	// an empty name, a nil option, a missing clock, logger, reporter or
	// propagator, no host, a host that is not a host, a Reach outside its
	// constants, a zero duration, size or count, and a dial function of
	// [WithDialContext] for a client of [ReachPublic] or beside a resolver.
	// The error that New returns wraps ErrConfig and names the problem.
	// Classifies as Invalid: the same configuration fails the same way every
	// time.
	ErrConfig = errs.WithClass(errors.New("httpclient: invalid configuration"), errs.Invalid)

	// ErrBlocked is returned by [Client.Do], [Client.Fetch] and
	// [Client.AppendFetch] for a request that the client does not send: a
	// scheme other than http and https, a host that [WithHosts] does not
	// admit, a redirect from https to http, and an address outside the
	// [Reach] of the client. Classifies as Denied: the client refuses the
	// call by policy, and the dependency took no part in it.
	ErrBlocked = errs.WithClass(errors.New("httpclient: blocked"), errs.Denied)

	// ErrTooLarge is returned by [Client.Fetch] and [Client.AppendFetch] for
	// a response body beyond the limit of [WithMaxResponseBytes]. Classifies
	// as Invalid: the same call returns the same body, so a caller raises the
	// limit or changes the request.
	ErrTooLarge = errs.WithClass(errors.New("httpclient: response body beyond the limit"), errs.Invalid)
)
