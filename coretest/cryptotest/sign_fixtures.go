// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest

import (
	"crypto/ecdsa"
	stded25519 "crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"

	"go.thesmos.sh/core/crypto/sign"
)

// Ed25519Fixture carries canonical Ed25519 test bytes — a fixed
// seed, the derived stdlib key material, the corresponding
// [sign.KeyID], and a (message, signature) pair the matching
// signer produces deterministically. Consumers wrap StdlibPriv
// in their own [sign.Signer] / [sign.Verifier] constructor; the
// fixture stays at stdlib bytes to avoid an import cycle with the
// impl packages under test.
//
// Ed25519 PureEdDSA is deterministic, so Signature is
// reproducible from (Seed, Message) under any RFC-8032-conformant
// implementation — locking it here is just convenience for
// consumers that want a pre-computed sample.
type Ed25519Fixture struct {
	// Seed is the 32-byte raw seed used to derive the keypair.
	// Pattern: 0x01, 0x02, ..., 0x20 — clearly test material.
	Seed []byte

	// StdlibPriv is the 64-byte expanded stdlib representation
	// (seed || derived pub).
	StdlibPriv stded25519.PrivateKey

	// PublicKey is the raw 32 bytes ([sign.Verifier.PublicKey]
	// returns this verbatim for Ed25519 implementations).
	PublicKey []byte

	// KeyID is the canonical [sign.KeyID] for PublicKey under the
	// SHA-256(pub)[:16] derivation rule.
	KeyID sign.KeyID

	// Message is the canonical sample message.
	Message []byte

	// Signature is the canonical Ed25519 signature over Message
	// under StdlibPriv.
	Signature []byte
}

// NewEd25519Sample returns the canonical Ed25519 test fixture.
// Bytes are stable across runs — the seed is a fixed pattern;
// PureEdDSA derivation is deterministic. "Sample" follows the
// coretest fixture convention (see clocktest.NewInstantSample):
// a baseline non-zero canonical value tests reach for first.
func NewEd25519Sample() Ed25519Fixture {
	const (
		seedHex = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
		// Locked vector — derived from seed via stdlib NewKeyFromSeed
		// + Sign of "the quick brown fox..." Both bytes are
		// reproducible from (seed, message) under any RFC-8032
		// implementation; freezing them protects against silent
		// stdlib drift.
		sigHex = "735ec0e7d7cd7996dcd94ad6fb2b6b25f00e5b275beb74e7542ca7747cc4191e" +
			"0e22dd0f11019e791e9de0f4d448134c75b71c4503b5d0205dd26ca26f01110f"
	)
	seed := mustDecodeHexFixture(seedHex)
	priv := stded25519.NewKeyFromSeed(seed)
	pub := priv.Public().(stded25519.PublicKey)

	keyIDFull := sha256.Sum256(pub)
	var keyID sign.KeyID
	copy(keyID[:], keyIDFull[:sign.KeyIDSize])

	return Ed25519Fixture{
		Seed:       seed,
		StdlibPriv: priv,
		PublicKey:  pub,
		KeyID:      keyID,
		Message:    canonicalSampleMessage(),
		Signature:  mustDecodeHexFixture(sigHex),
	}
}

// ECDSAP384Fixture carries canonical ECDSA P-384 test bytes — a
// fixed PKCS#8-encoded private key, the derived public-key
// material, the corresponding [sign.KeyID], and a (message,
// signature) pair valid under the keypair. Consumers wrap
// StdlibPriv in their own [sign.Signer] / [sign.Verifier]
// constructor.
//
// ECDSA P-384 signatures are non-deterministic (Go 1.26's
// [crypto/ecdsa.SignASN1] draws fresh entropy per call), so
// Signature is one *specific* canonical signature — produced
// once, locked here, and verified deterministically by
// [crypto/ecdsa.VerifyASN1] under PublicKey. Distinct signatures
// produced at runtime under the same keypair are equally valid.
type ECDSAP384Fixture struct {
	// PrivPKCS8 is the PKCS#8 (RFC 5958) DER-encoded private
	// key. Stable across stdlib versions — PKCS#8 is the
	// long-term portable encoding for ECDSA private keys.
	PrivPKCS8 []byte

	// StdlibPriv is the parsed *ecdsa.PrivateKey. Reuses
	// PrivPKCS8 internally; convenient for consumers that need
	// the typed value directly.
	StdlibPriv *ecdsa.PrivateKey

	// PublicKey is the PKIX-encoded SubjectPublicKeyInfo DER —
	// the encoding [sign.Verifier.PublicKey] returns for ECDSA
	// implementations.
	PublicKey []byte

	// KeyID is the canonical [sign.KeyID] under the
	// SHA-256(SEC 1 uncompressed point)[:16] derivation rule.
	KeyID sign.KeyID

	// Message is the canonical sample message.
	Message []byte

	// Signature is one canonical ECDSA P-384 + SHA-384 signature
	// over Message under StdlibPriv (ASN.1 DER encoding). Locked
	// for reproducibility; runtime [Sign] under the same key
	// produces different but equally valid signatures.
	Signature []byte
}

// NewECDSAP384Sample returns the canonical ECDSA P-384 test
// fixture. The keypair was generated once via stdlib's
// [crypto/ecdsa.GenerateKey] and frozen here as PKCS#8 hex —
// Go 1.26's GenerateKey is non-deterministic, so on-the-fly
// regeneration is not an option for stable fixtures.
func NewECDSAP384Sample() ECDSAP384Fixture {
	const (
		// PKCS#8-encoded P-384 priv. Generated once via stdlib
		// and locked here; the SEC 1 uncompressed point and the
		// derived KeyID below are downstream of these bytes.
		privPKCS8Hex = "3081b6020100301006072a8648ce3d020106052b8104002204819e30819b020101" +
			"04302441fa768fe046d037947891f9801cd2452c3251794f3ba719d1d34713fb03" +
			"777a4f67c498c8cf6e3f796d8ee5a1fc64a1640362000489c58840f950f08a8483" +
			"e8a83e3c8beebce510d505eec4e8578497efa48effcc329a4395fa0566d5e55bdd" +
			"0baf16ac7d9ba89621d776cf1d4a71e2cfbb6b7ee7109c291eb1d686f36a3a25ee" +
			"1adbef58b84aaadc713e23ed9a175b2552e59538"
		pkixHex = "3076301006072a8648ce3d020106052b810400220362000489c58840f950f08a8483" +
			"e8a83e3c8beebce510d505eec4e8578497efa48effcc329a4395fa0566d5e55bdd" +
			"0baf16ac7d9ba89621d776cf1d4a71e2cfbb6b7ee7109c291eb1d686f36a3a25ee" +
			"1adbef58b84aaadc713e23ed9a175b2552e59538"
		keyIDHex = "4d11c1a08c4e31c78649382034c191a6"
		// One canonical signature over canonicalSampleMessage()
		// produced once under the locked priv; verifies
		// deterministically.
		sigHex = "30640230302e0e7013bd7ca31ba00055f50ac8291304053b44b45247cafa70378c1" +
			"feb7193759adb65135e1430b97c4fefcfb01a0230509b917450352d052df69a9bc" +
			"03f9d994448db500b2d9280272a81f412587235a26a8b00f414a0fed0c202c577d" +
			"71acb"
	)
	pkcs8 := mustDecodeHexFixture(privPKCS8Hex)
	parsed, err := x509.ParsePKCS8PrivateKey(pkcs8)
	if err != nil {
		panic("cryptotest: ECDSA P-384 fixture priv failed to parse: " + err.Error())
	}
	priv, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		panic("cryptotest: ECDSA P-384 fixture priv is not *ecdsa.PrivateKey")
	}

	var keyID sign.KeyID
	copy(keyID[:], mustDecodeHexFixture(keyIDHex))

	return ECDSAP384Fixture{
		PrivPKCS8:  pkcs8,
		StdlibPriv: priv,
		PublicKey:  mustDecodeHexFixture(pkixHex),
		KeyID:      keyID,
		Message:    canonicalSampleMessage(),
		Signature:  mustDecodeHexFixture(sigHex),
	}
}

// canonicalSampleMessage returns the standard pangram used by
// every sign-fixture sample — a fresh slice per call so callers
// can mutate without poisoning sibling fixtures.
func canonicalSampleMessage() []byte {
	return []byte("the quick brown fox jumps over the lazy dog")
}

// mustDecodeHexFixture decodes hex at fixture-build time. Hex is
// hand-written and locked in source; a decode failure is a
// programmer error in this file, not a test failure — panic.
func mustDecodeHexFixture(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic("cryptotest: invalid hex fixture: " + err.Error())
	}
	return b
}
