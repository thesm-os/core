// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest

import (
	"bytes"
	"testing"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sign"
)

// VerifierSample is a canonical message/signature pair produced
// by the matching signer for a fixed key. Consumers supply one to
// drive [VerifierAcceptsAssertion] and the tamper-rejection
// assertions — the [sign.Verifier] surface alone has no way to
// generate fresh signatures, so the test fixture has to provide
// them.
type VerifierSample struct {
	// Message is the message the signature was produced over.
	Message []byte
	// Signature is the canonical signature over Message under
	// the verifier's public key.
	Signature []byte
}

// VerifierContractAssertions returns the generic assertions every
// [sign.Verifier] implementation must satisfy: Algorithm /
// PublicKey / KeyID stability, Verify-on-tampered-signature
// rejection, Verify-on-tampered-message rejection, Verify-on-
// wrong-length rejection. Compose with [VerifierAlgorithmAssertion],
// [VerifierKeyIDAssertion], [VerifierAcceptsAssertion],
// [VerifierCrossStdlibAssertion] at the consumer call site to add
// impl-specific constants and the (msg, sig) sample.
//
//	cryptotest.AssertVerifierContract(t, factory,
//	    append(cryptotest.VerifierContractAssertions(sample),
//	        cryptotest.VerifierAlgorithmAssertion(crypto.AlgEd25519),
//	        cryptotest.VerifierAcceptsAssertion(sample),
//	    )...,
//	)
//
// The supplied sample is reused across all tamper-rejection
// subtests; consumers may pass a zero [VerifierSample] to skip
// the sample-driven subtests, but at least Verify length-rejection
// still runs.
func VerifierContractAssertions(sample VerifierSample) []VerifierOption {
	opts := make([]VerifierOption, 0, 8)
	opts = append(opts,
		// --- Algorithm / KeyID / PublicKey stability ---

		VerifierCustom("Algorithm is stable across calls", func(t *testing.T, v sign.Verifier) {
			first := v.Algorithm()
			second := v.Algorithm()
			if first != second {
				t.Fatalf("Algorithm changed between calls: %q -> %q", first, second)
			}
		}),

		VerifierCustom("KeyID is stable across calls", func(t *testing.T, v sign.Verifier) {
			first := v.KeyID()
			second := v.KeyID()
			if first != second {
				t.Fatalf("KeyID changed between calls: %x -> %x", first, second)
			}
		}),

		VerifierCustom("PublicKey returns equal bytes across calls", func(t *testing.T, v sign.Verifier) {
			if !bytes.Equal(v.PublicKey(), v.PublicKey()) {
				t.Fatal("PublicKey returned different bytes across calls")
			}
		}),

		// --- Verify length rejection (works without a sample) ---

		VerifierCustom("Verify rejects nil signature", func(t *testing.T, v sign.Verifier) {
			if v.Verify([]byte("any"), nil) {
				t.Fatal("Verify accepted a nil signature")
			}
		}),

		VerifierCustom("Verify rejects empty signature", func(t *testing.T, v sign.Verifier) {
			if v.Verify([]byte("any"), []byte{}) {
				t.Fatal("Verify accepted an empty signature")
			}
		}),
	)

	if len(sample.Signature) == 0 {
		return opts
	}

	// Sample-driven subtests: round-trip and tamper rejection.
	return append(opts,
		VerifierCustom("Verify accepts canonical signature", func(t *testing.T, v sign.Verifier) {
			if !v.Verify(sample.Message, sample.Signature) {
				t.Fatal("Verify rejected the canonical (msg, sig) sample")
			}
		}),

		VerifierCustom("Verify rejects flipped signature bit", func(t *testing.T, v sign.Verifier) {
			tampered := bytes.Clone(sample.Signature)
			tampered[0] ^= 0x01
			if v.Verify(sample.Message, tampered) {
				t.Fatal("Verify accepted a tampered signature")
			}
		}),

		VerifierCustom("Verify rejects flipped message bit", func(t *testing.T, v sign.Verifier) {
			if len(sample.Message) == 0 {
				return // skip — empty message has no bit to flip
			}
			tampered := bytes.Clone(sample.Message)
			tampered[0] ^= 0x01
			if v.Verify(tampered, sample.Signature) {
				t.Fatal("Verify accepted a signature over a tampered message")
			}
		}),
	)
}

// VerifierAlgorithmAssertion verifies [sign.Verifier.Algorithm]
// returns the expected long-term cross-build algorithm name.
func VerifierAlgorithmAssertion(want crypto.Algorithm) VerifierOption {
	return VerifierCustom("Algorithm matches", func(t *testing.T, v sign.Verifier) {
		if got := v.Algorithm(); got != want {
			t.Fatalf("Algorithm: got %q, want %q", got, want)
		}
	})
}

// VerifierKeyIDAssertion verifies [sign.Verifier.KeyID] returns
// the expected canonical key identifier for the test's fixed
// public key.
func VerifierKeyIDAssertion(want sign.KeyID) VerifierOption {
	return VerifierCustom("KeyID matches", func(t *testing.T, v sign.Verifier) {
		if got := v.KeyID(); got != want {
			t.Fatalf("KeyID: got %x, want %x", got, want)
		}
	})
}

// VerifierAcceptsAssertion verifies that [sign.Verifier.Verify]
// returns true for the supplied canonical (msg, sig) sample. The
// sample must be a signature produced by the matching signer for
// the verifier's fixed public key.
func VerifierAcceptsAssertion(sample VerifierSample) VerifierOption {
	return VerifierCustom("Verify accepts sample", func(t *testing.T, v sign.Verifier) {
		if !v.Verify(sample.Message, sample.Signature) {
			t.Fatal("Verify rejected the supplied (msg, sig) sample")
		}
	})
}

// VerifierCrossStdlibAssertion verifies the SUT accepts a
// signature produced by the supplied stdlib signing function.
// Used to lock byte-exact wire compatibility with the stdlib
// reference: a stdlib-produced signature must round-trip through
// the SUT.
func VerifierCrossStdlibAssertion(stdlibSign func(msg []byte) []byte) VerifierOption {
	return VerifierCustom("Verify accepts stdlib-produced signature", func(t *testing.T, v sign.Verifier) {
		msg := []byte("the quick brown fox jumps over the lazy dog")
		sig := stdlibSign(msg)
		if !v.Verify(msg, sig) {
			t.Fatal("Verify rejected a stdlib-produced signature for the same key")
		}
	})
}
