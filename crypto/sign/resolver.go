// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sign

import (
	"fmt"

	"go.thesmos.sh/core/crypto"
)

// Resolver maps an algorithm name to a function that builds a
// [Verifier] from an encoded public key. An offline verifier uses it to
// build the Verifier a stored signature names.
//
// The caller builds a Resolver from the algorithms it trusts, as a map
// literal of each algorithm package's resolve function. There is no
// global registry and there are no default entries, so a stored
// algorithm name selects only an algorithm the caller listed.
//
// Apply a Resolver only to key material the caller trusts: its
// configuration, its trust store, or a key it pinned earlier. A key
// carried inside the artifact being verified proves nothing about who
// signed it.
//
// # Concurrency
//
// Safe for concurrent use once built, as for any map that is only
// read.
type Resolver map[crypto.Algorithm]func(pub []byte) (Verifier, error)

// Verifier returns a Verifier for alg and the encoded public key pub.
//
// Returns [ErrUnknownAlgorithm], classified [errs.Unsupported], when r
// has no entry for alg, when the entry returns neither a Verifier nor
// an error, and when the entry builds a Verifier that reports another
// algorithm. The last two are a Resolver whose table maps a name to the
// wrong constructor. Returns the constructor's error for a key it
// refuses.
//
// # Allocation contract
//
// Whatever the constructor allocates. An error allocates its message.
func (r Resolver) Verifier(alg crypto.Algorithm, pub []byte) (Verifier, error) {
	build := r[alg]
	if build == nil {
		return nil, fmt.Errorf("%w: %q", ErrUnknownAlgorithm, alg)
	}

	v, err := build(pub)
	if err != nil {
		return nil, err
	}

	if v == nil {
		return nil, fmt.Errorf("%w: the %q entry builds no verifier", ErrUnknownAlgorithm, alg)
	}

	if got := v.Algorithm(); got != alg {
		return nil, fmt.Errorf("%w: the %q entry builds a %q verifier", ErrUnknownAlgorithm, alg, got)
	}

	return v, nil
}
