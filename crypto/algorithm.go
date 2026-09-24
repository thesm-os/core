// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto

// Algorithm is an open-string vocabulary type identifying a
// cryptographic algorithm. Receipts, entry headers and signature
// envelopes persist it alongside [Digest] values. A verifier then
// picks the matching [Hasher] or signature verifier offline, and
// artefacts survive algorithm rotation.
//
// Algorithm is the long-term, cross-build identifier. It is
// stable across language ports, build tags, and host
// architectures, in contrast to [ID] which is a build-local
// identifier scoped to in-process implementation selection.
//
// A new algorithm is a new string literal and changes no interface.
// Consumers dispatch by comparing against the constants declared here
// or their own additions.
//
// # Naming convention
//
// Lowercase, hyphen-separated, with a digest-size or
// security-level suffix. Hyphens (not slashes) so the values are
// directly usable as filename suffixes, URL components, and
// configuration keys.
//
// # Family-specific spellings
//
// The family-name token is treated as a single word and is NOT
// hyphenated against its size suffix uniformly across families:
//
//   - SHA-2 spells the size with a hyphen:
//     [AlgSHA256] is "sha-256", [AlgSHA384] is "sha-384",
//     [AlgSHA512] is "sha-512".
//   - SHA-3 spells the size without a hyphen:
//     [AlgSHA3_256] is "sha3-256", [AlgSHA3_384] is "sha3-384",
//     [AlgSHA3_512] is "sha3-512".
//
// These spellings match the upstream specifications (FIPS 180-4
// for SHA-2, FIPS 202 for SHA-3) and the IETF / NIST registry
// conventions consumers will encounter in receipts produced
// elsewhere. Code that compares Algorithm strings must use the
// declared constants — do NOT normalise to a single spelling
// (for example "sha-3-256" is wrong; the correct value is
// "sha3-256").
type Algorithm string

// Hash algorithms.
const (
	// AlgSHA256 is SHA-256 per FIPS 180-4. Mainstream choice for
	// general-purpose digests, content addressing, and HMAC.
	AlgSHA256 Algorithm = "sha-256"
	// AlgSHA384 is SHA-384 per FIPS 180-4. CNSA 2.0 mandates
	// ≥ 384-bit digests for U.S. federal traffic by 2027, and AlgSHA384
	// is the lowest-cost compliant choice.
	AlgSHA384 Algorithm = "sha-384"
	// AlgSHA512 is SHA-512 per FIPS 180-4. Faster than SHA-256 on
	// 64-bit hosts for inputs above ~64 bytes; chosen when wire
	// space is plentiful and CPU time matters.
	AlgSHA512 Algorithm = "sha-512"
	// AlgSHA3_256 is SHA3-256 per FIPS 202, a Keccak sponge
	// construction. Use it where length-extension resistance or
	// NIST-approved diversity from the SHA-2 family is needed.
	AlgSHA3_256 Algorithm = "sha3-256"
	// AlgSHA3_384 is SHA3-384 per FIPS 202. Sibling of
	// [AlgSHA384] in the SHA-3 family for protocols that mandate
	// Keccak.
	AlgSHA3_384 Algorithm = "sha3-384"
	// AlgSHA3_512 is SHA3-512 per FIPS 202. Used for PQC
	// parameter-set expansion (ML-DSA, SLH-DSA) and high-margin
	// audit chains.
	AlgSHA3_512 Algorithm = "sha3-512"
)

// Keyed-MAC algorithms. Each names HMAC (RFC 2104) over the
// indicated underlying hash. The wire encoding follows the same
// family-spelling convention as the bare hashes:
//
//   - SHA-2 family keeps the size hyphen
//     ("hmac-sha-256", "hmac-sha-384", "hmac-sha-512").
//   - SHA-3 family does not
//     ("hmac-sha3-256", "hmac-sha3-384", "hmac-sha3-512").
//
// Spellings match RFC 4231 and the IETF / NIST registry
// conventions consumers will encounter in receipts produced
// elsewhere. Identifier names follow Go's no-underscore rule
// for SHA-2 ([AlgHMACSHA256] etc.) and use one underscore to
// disambiguate the size suffix in SHA-3 names
// ([AlgHMACSHA3_256] etc.) — exactly mirroring [AlgSHA256] vs
// [AlgSHA3_256].
const (
	// AlgHMACSHA256 is HMAC-SHA-256 per RFC 2104 / RFC 4231.
	// Default keyed-authentication choice for webhook and
	// request-signing flows.
	AlgHMACSHA256 Algorithm = "hmac-sha-256"
	// AlgHMACSHA384 is HMAC-SHA-384 per RFC 2104. The CNSA 2.0
	// minimum-strength keyed-MAC for U.S. federal traffic.
	AlgHMACSHA384 Algorithm = "hmac-sha-384"
	// AlgHMACSHA512 is HMAC-SHA-512 per RFC 2104. Preferred on
	// 64-bit hosts for long inputs where the SHA-512 throughput
	// advantage dominates the per-call HMAC overhead.
	AlgHMACSHA512 Algorithm = "hmac-sha-512"
	// AlgHMACSHA3_256 is HMAC-SHA3-256 per RFC 2104 over the
	// FIPS 202 sponge. Selected where Keccak diversity from the
	// SHA-2 family is required.
	AlgHMACSHA3_256 Algorithm = "hmac-sha3-256"
	// AlgHMACSHA3_384 is HMAC-SHA3-384 per RFC 2104 over the
	// FIPS 202 sponge.
	AlgHMACSHA3_384 Algorithm = "hmac-sha3-384"
	// AlgHMACSHA3_512 is HMAC-SHA3-512 per RFC 2104 over the
	// FIPS 202 sponge. High-margin audit-chain authentication.
	AlgHMACSHA3_512 Algorithm = "hmac-sha3-512"
)

// Public-key signature algorithms. The wire encoding follows the
// usual lowercase-hyphenated convention.
const (
	// AlgEd25519 is Ed25519 PureEdDSA per RFC 8032 §5.1.6 (also
	// FIPS 186-5 since 2023). The fixed 64-byte signature is
	// produced over the raw message, not a pre-hash.
	AlgEd25519 Algorithm = "ed25519"
	// AlgECDSAP384 is ECDSA over NIST P-384 per FIPS 186-5,
	// hashing the message with SHA-384 (matched curve / hash
	// strength). Signatures are ASN.1 DER (the FIPS-friendly
	// encoding produced by [crypto/ecdsa.SignASN1]).
	AlgECDSAP384 Algorithm = "ecdsa-p384"
	// AlgMLDSA44 is ML-DSA-44 per FIPS 204: 1,312-byte public keys
	// and 2,420-byte signatures, NIST security category 2.
	AlgMLDSA44 Algorithm = "ml-dsa-44"
	// AlgMLDSA65 is ML-DSA-65 per FIPS 204: 1,952-byte public keys
	// and 3,309-byte signatures, NIST security category 3.
	AlgMLDSA65 Algorithm = "ml-dsa-65"
	// AlgMLDSA87 is ML-DSA-87 per FIPS 204: 2,592-byte public keys
	// and 4,627-byte signatures, NIST security category 5. CNSA 2.0
	// requires it for national security systems.
	AlgMLDSA87 Algorithm = "ml-dsa-87"
)

// Authenticated-encryption algorithms, named for the [AEAD] seam.
// Persist the value alongside every ciphertext: it is what lets a
// build that does not yet exist select the implementation that can
// open it.
//
// The ChaCha20 constants are reserved and not implemented. The
// standard library cannot express either construction, so no
// implementation in this module reports them. Declaring the names
// fixes one spelling for any implementation outside this module.
const (
	// AlgAES128GCM is AES-128 in Galois/Counter Mode per NIST
	// SP 800-38D, with a 96-bit nonce and a 128-bit tag.
	AlgAES128GCM Algorithm = "aes-128-gcm"
	// AlgAES256GCM is AES-256 in Galois/Counter Mode per NIST
	// SP 800-38D, with a 96-bit nonce and a 128-bit tag.
	AlgAES256GCM Algorithm = "aes-256-gcm"
	// AlgChaCha20Poly1305 is ChaCha20-Poly1305 per RFC 8439, with a
	// 96-bit nonce. Reserved and not implemented.
	AlgChaCha20Poly1305 Algorithm = "chacha20-poly1305"
	// AlgXChaCha20Poly1305 is XChaCha20-Poly1305 with a 192-bit
	// nonce. At that width the birthday bound on random nonces is
	// not worth tracking. Reserved and not implemented.
	AlgXChaCha20Poly1305 Algorithm = "xchacha20-poly1305"
)

// Extendable-output-function algorithms, named for the [XOF] seam.
// Persist the value alongside anything squeezed: output length is the
// caller's choice, so the algorithm is the only thing that makes a
// squeeze reproducible.
//
// Names follow the FIPS 202 spelling, as the hash and MAC constants
// follow their own registries.
const (
	// AlgSHAKE128 is SHAKE128 per FIPS 202, a sponge with 128-bit
	// security strength against collisions when enough output is
	// taken.
	AlgSHAKE128 Algorithm = "shake128"
	// AlgSHAKE256 is SHAKE256 per FIPS 202, at 256-bit strength.
	AlgSHAKE256 Algorithm = "shake256"
)
