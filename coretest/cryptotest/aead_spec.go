// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest

import (
	"bytes"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto"
	randcrypto "go.thesmos.sh/core/rand/crypto"
)

// AEADOption is one conformance subtest. The suite for this seam is
// hand-written rather than generated — see the AEAD section of
// doc.go for why — so this type and [AEADCustom] stand in for the
// usual generated scaffolding.
type AEADOption struct {
	fn   func(*testing.T, crypto.AEAD)
	name string
}

// AEADCustom names a conformance subtest over an implementation
// produced by the factory.
func AEADCustom(name string, fn func(*testing.T, crypto.AEAD)) AEADOption {
	return AEADOption{name: name, fn: fn}
}

// AssertAEADContract runs opts against implementations produced by
// factory, each in its own parallel subtest with a fresh instance.
//
// factory must return an AEAD over a fixed key: several assertions
// seal with one instance and open with another, which only holds when
// both hold the same key.
func AssertAEADContract(t *testing.T, factory func() crypto.AEAD, opts ...AEADOption) {
	t.Helper()

	for _, opt := range opts {
		t.Run(opt.name, func(t *testing.T) {
			t.Parallel()
			opt.fn(t, factory())
		})
	}
}

// AEADContractAssertions returns the generic assertions every
// [crypto.AEAD] implementation must satisfy: round-tripping through
// [crypto.Seal] and [crypto.Open], a fresh nonce per Seal,
// authentication of both ciphertext and associated data, and the
// sizes it advertises.
//
// Compose with [AEADIDAssertion] and [AEADAlgorithmAssertion] at the
// consumer call site to add the impl-specific constants:
//
//	cryptotest.AssertAEADContract(t, factory,
//	    append(cryptotest.AEADContractAssertions(),
//	        cryptotest.AEADIDAssertion(wantID),
//	        cryptotest.AEADAlgorithmAssertion(crypto.AlgAES256GCM),
//	    )...,
//	)
//
// factory must return an AEAD over a fixed key: several assertions
// seal with one instance and open with another, which only holds
// when both hold the same key.
func AEADContractAssertions() []AEADOption {
	return []AEADOption{
		// --- round trip ---

		AEADCustom("Seal output opens back to the plaintext", func(t *testing.T, a crypto.AEAD) {
			for _, plaintext := range [][]byte{nil, {}, []byte("x"), bytes.Repeat([]byte{0x5A}, 4096)} {
				sealed, err := crypto.Seal(a, randcrypto.New(), plaintext, []byte("aad"))
				testkit.NoError(t, err, "Seal must succeed")

				opened, err := crypto.Open(a, sealed, []byte("aad"))
				testkit.NoError(t, err, "Open must succeed on Seal's own output")
				testkit.True(t, bytes.Equal(opened, plaintext),
					"Open must recover the plaintext")
			}
		}),

		AEADCustom("associated data may be absent", func(t *testing.T, a crypto.AEAD) {
			sealed, err := crypto.Seal(a, randcrypto.New(), []byte("payload"), nil)
			testkit.NoError(t, err, "Seal must accept nil associated data")

			opened, err := crypto.Open(a, sealed, nil)
			testkit.NoError(t, err, "Open must accept nil associated data")
			testkit.True(t, bytes.Equal(opened, []byte("payload")),
				"Open must recover the plaintext without associated data")
		}),

		// --- nonce discipline ---

		AEADCustom("each Seal draws a fresh nonce", func(t *testing.T, a crypto.AEAD) {
			// The property the whole construction rests on. Identical
			// output for identical input means the nonce repeated,
			// which discloses the XOR of the two plaintexts and
			// permits tag forgery for every message under the key.
			r := randcrypto.New()
			first, err := crypto.Seal(a, r, []byte("payload"), nil)
			testkit.NoError(t, err, "Seal must succeed")
			second, err := crypto.Seal(a, r, []byte("payload"), nil)
			testkit.NoError(t, err, "Seal must succeed")

			testkit.NotEqual(t, first, second,
				"sealing one plaintext twice must not repeat the nonce")
		}),

		AEADCustom("sealed output is nonce plus ciphertext plus overhead", func(t *testing.T, a crypto.AEAD) {
			plaintext := []byte("payload")
			sealed, err := crypto.Seal(a, randcrypto.New(), plaintext, nil)
			testkit.NoError(t, err, "Seal must succeed")
			testkit.Equal(t, len(sealed), a.NonceSize()+len(plaintext)+a.Overhead(),
				"Seal must prepend the nonce and append the tag")
		}),

		// --- authentication ---

		AEADCustom("a modified ciphertext does not open", func(t *testing.T, a crypto.AEAD) {
			sealed := mustSeal(t, a, []byte("payload"), []byte("aad"))
			sealed[a.NonceSize()] ^= 0x01
			_, err := crypto.Open(a, sealed, []byte("aad"))
			testkit.Error(t, err, "a flipped ciphertext bit must fail authentication")
		}),

		AEADCustom("a modified nonce does not open", func(t *testing.T, a crypto.AEAD) {
			sealed := mustSeal(t, a, []byte("payload"), []byte("aad"))
			sealed[0] ^= 0x01
			_, err := crypto.Open(a, sealed, []byte("aad"))
			testkit.Error(t, err, "a flipped nonce bit must fail authentication")
		}),

		AEADCustom("a modified tag does not open", func(t *testing.T, a crypto.AEAD) {
			sealed := mustSeal(t, a, []byte("payload"), []byte("aad"))
			sealed[len(sealed)-1] ^= 0x01
			_, err := crypto.Open(a, sealed, []byte("aad"))
			testkit.Error(t, err, "a flipped tag bit must fail authentication")
		}),

		AEADCustom("associated data is authenticated", func(t *testing.T, a crypto.AEAD) {
			sealed := mustSeal(t, a, []byte("payload"), []byte("aad"))
			_, err := crypto.Open(a, sealed, []byte("different"))
			testkit.Error(t, err, "associated data must be covered by the tag")
		}),

		AEADCustom("truncated input is a size error", func(t *testing.T, a crypto.AEAD) {
			for _, n := range []int{0, 1, a.NonceSize() - 1} {
				_, err := crypto.Open(a, make([]byte, n), nil)
				testkit.ErrorIs(t, err, crypto.ErrCiphertextShort,
					"input shorter than the nonce must report ErrCiphertextShort")
			}
		}),

		// --- advertised sizes ---

		AEADCustom("NonceSize and Overhead are positive and stable", func(t *testing.T, a crypto.AEAD) {
			testkit.True(t, a.NonceSize() > 0, "NonceSize must be positive")
			testkit.True(t, a.Overhead() > 0, "Overhead must be positive")
			testkit.Equal(t, a.NonceSize(), a.NonceSize(), "NonceSize must be stable")
			testkit.Equal(t, a.Overhead(), a.Overhead(), "Overhead must be stable")
		}),

		AEADCustom("Algorithm is non-empty and stable", func(t *testing.T, a crypto.AEAD) {
			testkit.NotEqual(t, string(a.Algorithm()), "",
				"Algorithm must name the construction — it is persisted with the ciphertext")
			testkit.Equal(t, a.Algorithm(), a.Algorithm(), "Algorithm must be stable")
		}),

		AEADCustom("ID is non-zero and stable", func(t *testing.T, a crypto.AEAD) {
			// ID identifies the implementation within a build, so it
			// must not depend on the key or on which instance is
			// asked.
			var zero crypto.ID
			testkit.NotEqual(t, a.ID(), zero, "ID must identify the implementation")
			testkit.Equal(t, a.ID(), a.ID(), "ID must be stable")
		}),
	}
}

// AEADIDAssertion asserts the implementation reports want from
// [crypto.AEAD.ID]. The ID identifies the implementation within a
// build, so it is a per-impl constant rather than a contract property.
func AEADIDAssertion(want crypto.ID) AEADOption {
	return AEADCustom("ID matches the implementation constant", func(t *testing.T, a crypto.AEAD) {
		testkit.Equal(t, a.ID(), want, "ID must match the implementation's constant")
	})
}

// AEADAlgorithmAssertion asserts the implementation reports want
// from [crypto.AEAD.Algorithm]. This is the value persisted alongside
// every ciphertext, so a change to it is a wire-format change.
func AEADAlgorithmAssertion(want crypto.Algorithm) AEADOption {
	return AEADCustom("Algorithm matches the implementation constant", func(t *testing.T, a crypto.AEAD) {
		testkit.Equal(t, a.Algorithm(), want, "Algorithm must match the implementation's constant")
	})
}

// AEADCrossInstanceAssertion asserts a ciphertext sealed by one
// instance opens under another built from the same key, and that the
// two report the same identity. Implementations caching per-instance
// state get this wrong here rather than in production, where the two
// instances are separate processes.
func AEADCrossInstanceAssertion(other func() crypto.AEAD) AEADOption {
	return AEADCustom("a ciphertext opens under a separate instance", func(t *testing.T, a crypto.AEAD) {
		peer := other()

		sealed := mustSeal(t, a, []byte("payload"), []byte("aad"))
		opened, err := crypto.Open(peer, sealed, []byte("aad"))
		testkit.NoError(t, err, "a separate instance over the same key must open the ciphertext")
		testkit.True(t, bytes.Equal(opened, []byte("payload")),
			"the separate instance must recover the plaintext")

		testkit.Equal(t, peer.ID(), a.ID(), "ID must not vary between instances")
		testkit.Equal(t, peer.Algorithm(), a.Algorithm(),
			"Algorithm must not vary between instances")
	})
}

// mustSeal seals plaintext or fails the test, keeping the assertion
// bodies above focused on what they are asserting.
func mustSeal(t *testing.T, a crypto.AEAD, plaintext, aad []byte) []byte {
	t.Helper()

	sealed, err := crypto.Seal(a, randcrypto.New(), plaintext, aad)
	testkit.NoError(t, err, "Seal must succeed")

	return sealed
}
