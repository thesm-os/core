// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package ecdsap384 provides an ECDSA P-384 + SHA-384
// [sign.Signer] / [sign.Verifier] implementation backed by
// [crypto/ecdsa].
//
// The curve is NIST P-384 (FIPS 186-5); the hash is SHA-384,
// matching the curve's 192-bit security level. Signatures are
// ASN.1 DER (the FIPS-friendly encoding produced by
// [crypto/ecdsa.SignASN1]). Public keys are exposed in PKIX
// (X.509 SubjectPublicKeyInfo) DER, the same shape consumed by
// [crypto/x509.ParsePKIXPublicKey].
//
// Because the algorithm is hash-then-sign, this package
// satisfies the optional [sign.StreamingSigner] /
// [sign.StreamingVerifier] capability interfaces. Hot-path
// consumers signing or verifying long messages absorb bytes
// into a SHA-384 stream and finalise to one curve operation —
// avoiding a buffered-message allocation.
//
// Under `GODEBUG=fips140=on` (or `=only`), every operation runs
// through Go's FIPS-validated module. ECDSA P-384 + SHA-384 is
// approved under FIPS 186-5, so both modes work without
// modification. FIPS-gated wrappers (hard-refusal-outside-FIPS-
// mode) live in consumer modules, not here.
//
// # KeyID derivation
//
// [KeyIDFromPub] hashes the SEC 1 uncompressed point encoding
// (`0x04 || X(48 BE) || Y(48 BE)`, 97 bytes) with SHA-256 and
// truncates to 16 bytes. The fixed-width big-endian encoding
// makes the derivation deterministic across builds and
// languages — distinct from the variable-length PKIX encoding
// returned by [Verifier.PublicKey].
package ecdsap384
