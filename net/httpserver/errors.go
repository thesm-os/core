// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package httpserver

//go:generate testkit sentinel -o errors.gen_test.go

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

// Sentinel errors returned by this package.
//
// Each classifies under [go.thesmos.sh/core/errs.Classify], except
// [ErrShutdown], whose documentation states why it has no class. [Server.Run]
// returns a sentinel joined with the error that caused it, and a joined
// error classifies as its branch of highest rank. The cause therefore
// raises the class of the sentinel when it ranks higher, such as Denied for
// a port that the system does not let the process bind.
var (
	// ErrConfig is returned by [New] for a configuration that it refuses: a
	// nil handler, a nil option, a missing clock, logger, reporter or
	// propagator, an address that is not of the form host:port, a zero
	// duration or size, a trusted origin that is not an origin, and a
	// middleware that returns a nil handler. The error that New returns
	// wraps ErrConfig and names the problem. Classifies as Invalid: the
	// same configuration fails the same way every time.
	ErrConfig = errs.WithClass(errors.New("httpserver: invalid configuration"), errs.Invalid)

	// ErrClosed is returned by [Server.Run] for a Server whose Run was called
	// before, because a Server serves once. Classifies as Invalid: the call
	// fails the same way every time.
	ErrClosed = errs.WithClass(errors.New("httpserver: the server has run"), errs.Invalid)

	// ErrListen is returned by [Server.Run], joined with the error of the
	// listen, when Run cannot listen on its address. Classifies as
	// Transient: another process may release the address.
	ErrListen = errs.WithClass(errors.New("httpserver: listen failed"), errs.Transient)

	// ErrServe is returned by [Server.Run], joined with the error of
	// http.Server.Serve, when serving fails before the context of Run ends,
	// such as a listener whose Accept fails with an error that net/http does
	// not retry. Classifies as Transient: a new Server on a new listener
	// may serve.
	ErrServe = errs.WithClass(errors.New("httpserver: serve failed"), errs.Transient)

	// ErrShutdown is returned by [Server.Run], joined with
	// context.DeadlineExceeded when the shutdown timeout elapsed with
	// requests in flight, and with the error of closing a listener
	// otherwise. It has no class: Run has ended when it returns
	// ErrShutdown, so no caller retries it, and the server served every
	// request that finished before the timeout.
	ErrShutdown = errors.New("httpserver: shutdown did not finish")
)
