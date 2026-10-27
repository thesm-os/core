// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest

import (
	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sign"
)

// StdlibSignerSpec is the key and the functions of a [sign.Signer] over the
// standard library, which [NewStdlibSignerStub] wraps. A test fills it with the
// values of one key and with the Sign and the Verify of the standard library
// under that key.
type StdlibSignerSpec struct {
	// Algorithm is the name of the algorithm that Algorithm returns.
	Algorithm crypto.Algorithm

	// KeyID is the key ID that KeyID returns.
	KeyID sign.KeyID

	// PublicKey is the encoding of the public key that PublicKey returns: the
	// 32 bytes of an Ed25519 key, or the PKIX encoding of an ECDSA P-384 key.
	PublicKey []byte

	// Sign returns the signature of msg under the key.
	Sign func(msg []byte) ([]byte, error)

	// Verify reports whether sig is a signature of msg under the key.
	Verify func(msg, sig []byte) bool
}

// stdlibSigner is the [sign.Signer] of a [StdlibSignerSpec]. Each method
// returns a value of the spec or the result of a function of the spec.
type stdlibSigner struct {
	spec StdlibSignerSpec
}

// KeyID returns the key ID of the spec.
func (s *stdlibSigner) KeyID() sign.KeyID { return s.spec.KeyID }

// PublicKey returns the public key of the spec.
func (s *stdlibSigner) PublicKey() []byte { return s.spec.PublicKey }

// Algorithm returns the algorithm of the spec.
func (s *stdlibSigner) Algorithm() crypto.Algorithm { return s.spec.Algorithm }

// Sign returns the result of the Sign of the spec.
func (s *stdlibSigner) Sign(msg []byte) ([]byte, error) { return s.spec.Sign(msg) }

// Verify returns the result of the Verify of the spec.
func (s *stdlibSigner) Verify(msg, sig []byte) bool { return s.spec.Verify(msg, sig) }

// NewStdlibSignerStub returns a [SignerStub] that delegates each call to the
// Signer of spec. The double records the calls, and the functions of spec
// compute the signatures.
func NewStdlibSignerStub(tb assert.TB, spec StdlibSignerSpec) *SignerStub {
	return NewSignerStub(tb,
		SignerStubDelegateTo(&stdlibSigner{spec: spec}),
	)
}
