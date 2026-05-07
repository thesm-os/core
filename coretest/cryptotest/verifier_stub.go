// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest

import (
	"testing"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sign"
)

// StdlibVerifierSpec describes a stdlib-backed [sign.Verifier]
// reference for cross-stdlib byte-equivalence testing. Used to
// parameterise [NewStdlibVerifierStub] — the per-impl test
// supplies the algorithm-of-record values plus the public key
// the SUT is bound to, and the inner companion delegates Verify
// to the supplied stdlib verification function. The outer stub
// adds recording / fault-injection hooks.
type StdlibVerifierSpec struct {
	// Algorithm is the long-term cross-build algorithm name.
	Algorithm crypto.Algorithm

	// KeyID is the canonical identifier for PublicKey under the
	// implementation's KeyID derivation rule.
	KeyID sign.KeyID

	// PublicKey is the public-key bytes returned by
	// [sign.Verifier.PublicKey] — the algorithm's canonical
	// encoding (Ed25519 raw bytes, ECDSA P-384 PKIX DER, etc.).
	PublicKey []byte

	// Verify reports whether sig is a valid signature over msg
	// under the stdlib reference for this key. Drives the
	// companion's [sign.Verifier.Verify] method.
	Verify func(msg, sig []byte) bool
}

// stdlibVerifier is a [sign.Verifier] companion backed by stdlib
// primitives. Used as the inner delegate for
// [NewStdlibVerifierStub] — the generated [VerifierStub] wraps
// it via [VerifierStubDelegateTo] to keep the recorder /
// fault-injector layer on top.
type stdlibVerifier struct {
	spec StdlibVerifierSpec
}

func (v *stdlibVerifier) KeyID() sign.KeyID           { return v.spec.KeyID }
func (v *stdlibVerifier) PublicKey() []byte           { return v.spec.PublicKey }
func (v *stdlibVerifier) Algorithm() crypto.Algorithm { return v.spec.Algorithm }
func (v *stdlibVerifier) Verify(msg, sig []byte) bool { return v.spec.Verify(msg, sig) }

// NewStdlibVerifierStub returns a [VerifierStub] delegating to a
// stdlib-backed companion described by spec. The stub layer adds
// recording / fault-injection hooks; the inner companion supplies
// real behaviour via [StdlibVerifierSpec.Verify].
func NewStdlibVerifierStub(tb testing.TB, spec StdlibVerifierSpec) *VerifierStub {
	return NewVerifierStub(tb,
		VerifierStubDelegateTo(&stdlibVerifier{spec: spec}),
	)
}
