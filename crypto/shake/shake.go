// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package shake provides SHAKE128 and SHAKE256 [crypto.XOF]
// implementations backed by [crypto/sha3].
//
// SHAKE is the extendable-output half of FIPS 202: the same Keccak
// sponge as SHA-3, with the output length chosen by the caller rather
// than fixed by the algorithm. Use it where a fixed-width digest is
// the wrong shape — key derivation, parameter expansion, deterministic
// sampling.
//
// # Why this wraps rather than aliases
//
// The standard library's SHAKE type satisfies [crypto.XOFStream]'s
// method set, but not its contract: it panics on a write after a
// read. This package tracks the phase itself and returns
// [crypto.ErrXOFSqueezing] before the standard library can panic, so
// a caller threading a stream through code that does not know its
// phase gets an error rather than a crash.
//
// # Choosing a construction
//
// SHAKE128 and SHAKE256 differ in security strength, not in output
// length — both produce as much as asked for. The number is the
// strength ceiling: SHAKE128 offers 128-bit collision resistance no
// matter how much output is taken, SHAKE256 offers 256-bit. Take at
// least 2×strength bits of output to reach that ceiling.
//
// # Concurrency
//
// The returned [crypto.XOF] values are safe for concurrent use; the
// streams they produce are not. One stream per goroutine.
package shake

import (
	"crypto/sha3"

	"go.thesmos.sh/core/crypto"
)

// Build-local implementation identifiers. The bytes spell out the
// construction left-aligned with zero padding to [crypto.IDSize]; the
// compiler rejects a literal too long to fit.
var (
	id128 = crypto.ID{'s', 'h', 'a', 'k', 'e', '1', '2', '8', '/', 'v', '1'}
	id256 = crypto.ID{'s', 'h', 'a', 'k', 'e', '2', '5', '6', '/', 'v', '1'}
)

// xof is a SHAKE [crypto.XOF]. It holds no sponge state — that lives
// on each stream — so a single value is safe to share.
type xof struct {
	newStream func() *sha3.SHAKE
	algorithm crypto.Algorithm
	id        crypto.ID
}

// Compile-time proof that both constructions satisfy the seam.
var _ crypto.XOF = xof{}

// New128 returns a SHAKE128 [crypto.XOF].
func New128() crypto.XOF {
	return xof{id: id128, algorithm: crypto.AlgSHAKE128, newStream: sha3.NewSHAKE128}
}

// New256 returns a SHAKE256 [crypto.XOF].
func New256() crypto.XOF {
	return xof{id: id256, algorithm: crypto.AlgSHAKE256, newStream: sha3.NewSHAKE256}
}

// ID returns the build-local implementation identifier.
func (x xof) ID() crypto.ID { return x.id }

// Algorithm returns the long-term, cross-build name — either
// [crypto.AlgSHAKE128] or [crypto.AlgSHAKE256].
func (x xof) Algorithm() crypto.Algorithm { return x.algorithm }

// NewXOFStream returns a fresh absorbing stream.
//
// # Allocation contract
//
// Allocates the sponge state once. Write and Read allocate nothing
// thereafter.
func (x xof) NewXOFStream() crypto.XOFStream {
	return &stream{shake: x.newStream()}
}

// stream is a [crypto.XOFStream] over the standard library's sponge,
// with the absorb/squeeze phase tracked here so a write after a read
// is an error rather than the panic the standard library raises.
type stream struct {
	shake     *sha3.SHAKE
	squeezing bool
}

// Write absorbs p.
//
// Returns [crypto.ErrXOFSqueezing] once Read has been called, having
// absorbed nothing — the check precedes the call, so the standard
// library never reaches its panic and the sponge is left untouched
// and still readable.
func (s *stream) Write(p []byte) (int, error) {
	if s.squeezing {
		return 0, crypto.ErrXOFSqueezing
	}

	//nolint:wrapcheck // sha3.SHAKE.Write is documented never to fail
	return s.shake.Write(p)
}

// Read squeezes output, extending one stream across successive calls.
// The stream never ends, so this never reports [io.EOF].
func (s *stream) Read(p []byte) (int, error) {
	s.squeezing = true

	//nolint:wrapcheck // sha3.SHAKE.Read is documented never to fail
	return s.shake.Read(p)
}

// Reset clears the sponge and returns the stream to absorbing.
func (s *stream) Reset() {
	s.shake.Reset()
	s.squeezing = false
}
