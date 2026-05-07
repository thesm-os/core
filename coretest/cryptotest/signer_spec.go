// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest

import (
	"bytes"
	"testing"

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
			first := s.Algorithm()
			second := s.Algorithm()
			if first != second {
				t.Fatalf("Algorithm changed between calls: %q -> %q", first, second)
			}
		}),

		SignerCustom("KeyID is stable across calls", func(t *testing.T, s sign.Signer) {
			first := s.KeyID()
			second := s.KeyID()
			if first != second {
				t.Fatalf("KeyID changed between calls: %x -> %x", first, second)
			}
		}),

		SignerCustom("PublicKey returns equal bytes across calls", func(t *testing.T, s sign.Signer) {
			if !bytes.Equal(s.PublicKey(), s.PublicKey()) {
				t.Fatal("PublicKey returned different bytes across calls")
			}
		}),

		// --- Sign + Verify round-trip ---

		SignerCustom("Sign then Verify accepts canonical signature", func(t *testing.T, s sign.Signer) {
			msg := []byte("the quick brown fox jumps over the lazy dog")
			sig, err := s.Sign(msg)
			if err != nil {
				t.Fatalf("Sign: %v", err)
			}
			if !s.Verify(msg, sig) {
				t.Fatal("Verify rejected the canonical signature")
			}
		}),

		SignerCustom("Sign over empty message round-trips", func(t *testing.T, s sign.Signer) {
			sig, err := s.Sign(nil)
			if err != nil {
				t.Fatalf("Sign(nil): %v", err)
			}
			if !s.Verify(nil, sig) {
				t.Fatal("Verify rejected a signature over nil message")
			}
			if !s.Verify([]byte{}, sig) {
				t.Fatal("nil and empty-slice messages must verify identically")
			}
		}),

		// --- Tamper rejection ---

		SignerCustom("Verify rejects flipped signature bit", func(t *testing.T, s sign.Signer) {
			msg := []byte("verify-me")
			sig, err := s.Sign(msg)
			if err != nil {
				t.Fatalf("Sign: %v", err)
			}
			tampered := bytes.Clone(sig)
			tampered[0] ^= 0x01
			if s.Verify(msg, tampered) {
				t.Fatal("Verify accepted a tampered signature")
			}
		}),

		SignerCustom("Verify rejects flipped message bit", func(t *testing.T, s sign.Signer) {
			msg := []byte("verify-me")
			sig, err := s.Sign(msg)
			if err != nil {
				t.Fatalf("Sign: %v", err)
			}
			tampered := bytes.Clone(msg)
			tampered[0] ^= 0x01
			if s.Verify(tampered, sig) {
				t.Fatal("Verify accepted a signature over a tampered message")
			}
		}),

		// --- Verify length guarding ---

		SignerCustom("Verify rejects wrong-length signature", func(t *testing.T, s sign.Signer) {
			msg := []byte("any")
			cases := [][]byte{nil, {}, make([]byte, 1), make([]byte, 16)}
			for _, c := range cases {
				if s.Verify(msg, c) {
					t.Fatalf("Verify accepted a %d-byte signature", len(c))
				}
			}
		}),
	}
}

// SignerAlgorithmAssertion verifies [sign.Signer.Algorithm]
// returns the expected long-term cross-build algorithm name.
func SignerAlgorithmAssertion(want crypto.Algorithm) SignerOption {
	return SignerCustom("Algorithm matches", func(t *testing.T, s sign.Signer) {
		if got := s.Algorithm(); got != want {
			t.Fatalf("Algorithm: got %q, want %q", got, want)
		}
	})
}

// SignerKeyIDAssertion verifies [sign.Signer.KeyID] returns the
// expected canonical key identifier for the test's fixed key.
func SignerKeyIDAssertion(want sign.KeyID) SignerOption {
	return SignerCustom("KeyID matches", func(t *testing.T, s sign.Signer) {
		if got := s.KeyID(); got != want {
			t.Fatalf("KeyID: got %x, want %x", got, want)
		}
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
		if err != nil {
			t.Fatalf("Sign: %v", err)
		}
		if !stdlibVerify(s.PublicKey(), msg, sig) {
			t.Fatal("stdlib rejected SUT-produced signature")
		}
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
		if !s.Verify(msg, sig) {
			t.Fatal("SUT rejected stdlib-produced signature")
		}
	})
}
