// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest

import (
	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sign"
)

// StdlibVerifierSpec is the key and the verification function of a
// [sign.Verifier] over the standard library, which [NewStdlibVerifierStub]
// wraps. A test fills it with the values of the key of the Verifier under test
// and with the Verify of the standard library under that key.
type StdlibVerifierSpec struct {
	// Algorithm is the name of the algorithm that Algorithm returns.
	Algorithm crypto.Algorithm

	// KeyID is the key ID that KeyID returns.
	KeyID sign.KeyID

	// PublicKey is the encoding of the public key that PublicKey returns: the
	// 32 bytes of an Ed25519 key, or the PKIX encoding of an ECDSA P-384 key.
	PublicKey []byte

	// Verify reports whether sig is a signature of msg under the key.
	Verify func(msg, sig []byte) bool
}

// stdlibVerifier is the [sign.Verifier] of a [StdlibVerifierSpec]. Each method
// returns a value of the spec or the result of the Verify of the spec.
type stdlibVerifier struct {
	spec StdlibVerifierSpec
}

// KeyID returns the key ID of the spec.
func (v *stdlibVerifier) KeyID() sign.KeyID { return v.spec.KeyID }

// PublicKey returns the public key of the spec.
func (v *stdlibVerifier) PublicKey() []byte { return v.spec.PublicKey }

// Algorithm returns the algorithm of the spec.
func (v *stdlibVerifier) Algorithm() crypto.Algorithm { return v.spec.Algorithm }

// Verify returns the result of the Verify of the spec.
func (v *stdlibVerifier) Verify(msg, sig []byte) bool { return v.spec.Verify(msg, sig) }

// NewStdlibVerifierStub returns a [VerifierStub] that delegates each call to the
// Verifier of spec. The double records the calls, and the Verify of spec checks
// the signatures.
func NewStdlibVerifierStub(tb assert.TB, spec StdlibVerifierSpec) *VerifierStub {
	return NewVerifierStub(tb,
		VerifierStubDelegateTo(&stdlibVerifier{spec: spec}),
	)
}
