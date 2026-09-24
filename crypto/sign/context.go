// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sign

import "context"

// ContextSigner is the optional capability for a [Signer] whose
// signing crosses a process boundary, such as a hosted key service or
// a hardware module behind a pool of sessions.
//
// An implementation documents what ctx stops: the wait for a session,
// the network round trip, or both. An operation already inside a
// hardware module may run to completion after SignContext returns.
//
// Callers sign through [SignContext], which uses this capability when
// a signer has it and falls back to [Signer.Sign] when it does not.
type ContextSigner interface {
	Signer

	// SignContext returns a signature over message, or an error
	// wrapping context.Cause(ctx) when ctx ends before the signature
	// is available. Sign on the same value behaves as SignContext with
	// a context that never ends.
	//
	//testkit:nondeterministic
	SignContext(ctx context.Context, message []byte) ([]byte, error)
}

// SignContext signs message with s.
//
// When [AsContextSigner] finds a [ContextSigner] in s, SignContext
// calls it, so ctx bounds the wait. Otherwise it returns
// context.Cause(ctx) when ctx has already ended, and calls
// [Signer.Sign] when it has not. An in-process signer has nothing to
// wait for, so the check before the call is the only one.
//
// # Allocation contract
//
// Whatever the signer allocates.
//
//nolint:revive // named for the ContextSigner method it dispatches to, as crypto.SignMessage is for MessageSigner.SignMessage
func SignContext(ctx context.Context, s Signer, message []byte) ([]byte, error) {
	if cs, ok := AsContextSigner(s); ok {
		return cs.SignContext(ctx, message) //nolint:wrapcheck // returned as the signer produced it
	}

	if err := context.Cause(ctx); err != nil {
		return nil, err
	}

	return s.Sign(message) //nolint:wrapcheck // returned as the signer produced it
}
