// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest

import (
	"bytes"
	"fmt"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sign"
)

// SignerContractAssertions returns the generic assertions every
// [sign.Signer] implementation must satisfy: Algorithm / KeyID /
// PublicKey stability, Sign + Verify round-trip, Verify rejects
// tampered signature, Verify rejects tampered message, Verify
// rejects wrong-length signature.
//
// Composes with [SignerAlgorithmAssertion],
// [SignerKeyIDAssertion], [SignerCrossStdlibVerifyAssertion],
// [SignerCrossStdlibSignAssertion] at the consumer call site to
// add impl-specific constants and stdlib byte-equivalence.
//
//	cryptotest.AssertSignerContract(t, factory,
//	    append(cryptotest.SignerContractAssertions(),
//	        cryptotest.SignerAlgorithmAssertion(crypto.AlgEd25519),
//	        cryptotest.SignerCrossStdlibVerifyAssertion(stdlibVerify),
//	    )...,
//	)
//
// Sign is non-deterministic for some algorithms (ECDSA P-384), so
// these assertions never compare two consecutive Sign outputs for
// byte equality — they only assert the Verify round-trip holds.
func SignerContractAssertions() []SignerOption {
	return []SignerOption{
		// --- Algorithm / KeyID / PublicKey stability ---

		SignerCustom("Algorithm is stable across calls", func(t *testing.T, s sign.Signer) {
			testkit.Equal(t, s.Algorithm(), s.Algorithm(),
				"Algorithm must be stable across consecutive calls")
		}),

		SignerCustom("KeyID is stable across calls", func(t *testing.T, s sign.Signer) {
			testkit.Equal(t, s.KeyID(), s.KeyID(),
				"KeyID must be stable across consecutive calls")
		}),

		SignerCustom("PublicKey returns equal bytes across calls", func(t *testing.T, s sign.Signer) {
			testkit.Equal(t, s.PublicKey(), s.PublicKey(),
				"PublicKey must return equal bytes across consecutive calls")
		}),

		// --- Sign + Verify round-trip ---

		SignerCustom("Sign then Verify accepts canonical signature", func(t *testing.T, s sign.Signer) {
			msg := []byte("the quick brown fox jumps over the lazy dog")
			sig, err := s.Sign(msg)
			testkit.NoError(t, err, "Sign")
			testkit.True(t, s.Verify(msg, sig),
				"Verify must accept the canonical signature")
		}),

		SignerCustom("Sign over empty message round-trips", func(t *testing.T, s sign.Signer) {
			sig, err := s.Sign(nil)
			testkit.NoError(t, err, "Sign(nil)")
			testkit.True(t, s.Verify(nil, sig),
				"Verify must accept a signature over nil message")
			testkit.True(t, s.Verify([]byte{}, sig),
				"nil and empty-slice messages must verify identically")
		}),

		// --- Tamper rejection ---

		SignerCustom("Verify rejects flipped signature bit", func(t *testing.T, s sign.Signer) {
			msg := []byte("verify-me")
			sig, err := s.Sign(msg)
			testkit.NoError(t, err, "Sign")
			tampered := bytes.Clone(sig)
			tampered[0] ^= 0x01
			testkit.False(t, s.Verify(msg, tampered),
				"Verify must reject a tampered signature")
		}),

		SignerCustom("Verify rejects flipped message bit", func(t *testing.T, s sign.Signer) {
			msg := []byte("verify-me")
			sig, err := s.Sign(msg)
			testkit.NoError(t, err, "Sign")
			tampered := bytes.Clone(msg)
			tampered[0] ^= 0x01
			testkit.False(t, s.Verify(tampered, sig),
				"Verify must reject a signature over a tampered message")
		}),

		// --- Verify length guarding ---

		SignerCustom("Verify rejects wrong-length signature", func(t *testing.T, s sign.Signer) {
			msg := []byte("any")
			cases := [][]byte{nil, {}, make([]byte, 1), make([]byte, 16)}
			for _, c := range cases {
				testkit.False(t, s.Verify(msg, c),
					fmt.Sprintf("Verify must reject a %d-byte signature", len(c)))
			}
		}),
	}
}

// SignerAlgorithmAssertion verifies [sign.Signer.Algorithm]
// returns the expected long-term cross-build algorithm name.
func SignerAlgorithmAssertion(want crypto.Algorithm) SignerOption {
	return SignerCustom("Algorithm matches", func(t *testing.T, s sign.Signer) {
		testkit.Equal(t, s.Algorithm(), want, "Algorithm must match expected name")
	})
}

// SignerKeyIDAssertion verifies [sign.Signer.KeyID] returns the
// expected canonical key identifier for the test's fixed key.
func SignerKeyIDAssertion(want sign.KeyID) SignerOption {
	return SignerCustom("KeyID matches", func(t *testing.T, s sign.Signer) {
		testkit.Equal(t, s.KeyID(), want, "KeyID must match expected identifier")
	})
}

// SignerCrossStdlibVerifyAssertion verifies that the SUT's
// signature passes verification under the supplied stdlib
// reference. Used to lock byte-exact wire compatibility: a
// signature produced by the SUT under its public key must be
// accepted by the stdlib's verifier for the same public key.
//
// stdlibVerify is `func(pub, msg, sig []byte) bool`; the consumer
// closes over the stdlib verification primitive ([crypto/ed25519.Verify]
// or [crypto/ecdsa.VerifyASN1]) and supplies the canonical pub
// encoding (raw 32 bytes for Ed25519, *ecdsa.PublicKey wrapped
// closure for ECDSA).
func SignerCrossStdlibVerifyAssertion(stdlibVerify func(pub, msg, sig []byte) bool) SignerOption {
	return SignerCustom("stdlib accepts SUT-produced signature", func(t *testing.T, s sign.Signer) {
		msg := []byte("payload that round-trips between our seam and stdlib")
		sig, err := s.Sign(msg)
		testkit.NoError(t, err, "Sign")
		testkit.True(t, stdlibVerify(s.PublicKey(), msg, sig),
			"stdlib must accept the SUT-produced signature")
	})
}

// SignerCrossStdlibSignAssertion verifies that the SUT accepts a
// signature produced by the supplied stdlib signing function.
// Used to lock byte-exact wire compatibility from the other
// direction: a stdlib-produced signature must be accepted by the
// SUT's verifier.
func SignerCrossStdlibSignAssertion(stdlibSign func(msg []byte) []byte) SignerOption {
	return SignerCustom("SUT accepts stdlib-produced signature", func(t *testing.T, s sign.Signer) {
		msg := []byte("payload from the stdlib direction")
		sig := stdlibSign(msg)
		testkit.True(t, s.Verify(msg, sig),
			"SUT must accept the stdlib-produced signature")
	})
}
