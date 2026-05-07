// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest

import (
	"testing"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sign"
)

// StdlibSignerSpec describes a stdlib-backed [sign.Signer]
// reference. Used to parameterise [NewStdlibSignerStub] — the
// per-impl test supplies the algorithm-of-record values plus the
// stdlib Sign / Verify primitives keyed with the same key the
// SUT was constructed with. Sign produces non-deterministic
// signatures for ECDSA, so the model layer is left to the
// per-impl test (see [SignerCrossStdlibVerifyAssertion]).
type StdlibSignerSpec struct {
	// Algorithm is the long-term cross-build algorithm name.
	Algorithm crypto.Algorithm

	// KeyID is the canonical identifier for the keypair under
	// the implementation's KeyID derivation rule.
	KeyID sign.KeyID

	// PublicKey is the public-key bytes the companion's
	// [sign.Signer.PublicKey] reports (the algorithm's canonical
	// encoding — Ed25519 raw bytes, ECDSA P-384 PKIX DER).
	PublicKey []byte

	// Sign produces a stdlib-backed signature over msg under the
	// configured key. Drives the companion's
	// [sign.Signer.Sign] method.
	Sign func(msg []byte) ([]byte, error)

	// Verify reports whether sig is a valid signature over msg
	// under the same key. Drives the companion's
	// [sign.Verifier.Verify] method.
	Verify func(msg, sig []byte) bool
}

// stdlibSigner is a [sign.Signer] companion backed by stdlib
// primitives.
type stdlibSigner struct {
	spec StdlibSignerSpec
}

func (s *stdlibSigner) KeyID() sign.KeyID               { return s.spec.KeyID }
func (s *stdlibSigner) PublicKey() []byte               { return s.spec.PublicKey }
func (s *stdlibSigner) Algorithm() crypto.Algorithm     { return s.spec.Algorithm }
func (s *stdlibSigner) Sign(msg []byte) ([]byte, error) { return s.spec.Sign(msg) }
func (s *stdlibSigner) Verify(msg, sig []byte) bool     { return s.spec.Verify(msg, sig) }

// NewStdlibSignerStub returns a [SignerStub] delegating to a
// stdlib-backed companion described by spec. The stub layer adds
// recording / fault-injection hooks; the inner companion supplies
// real behaviour via [StdlibSignerSpec.Sign] /
// [StdlibSignerSpec.Verify].
func NewStdlibSignerStub(tb testing.TB, spec StdlibSignerSpec) *SignerStub {
	return NewSignerStub(tb,
		SignerStubDelegateTo(&stdlibSigner{spec: spec}),
	)
}
