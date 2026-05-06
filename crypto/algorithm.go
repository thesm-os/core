// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto

// Algorithm is an open-string vocabulary type identifying a
// cryptographic algorithm. Persisted into receipts, entry
// headers, and signature envelopes alongside [Digest] values, so
// verifiers can pick the matching [Hasher] (or [Signer]) offline
// and so artefacts survive algorithm rotation.
//
// Algorithm is the long-term, cross-build identifier. It is
// stable across language ports, build tags, and host
// architectures, in contrast to [ID] which is a build-local
// identifier scoped to in-process implementation selection.
//
// New algorithms can be added by string literal without changing
// any interface — consumers compare against the constants
// declared here (or their own additions) to dispatch.
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
	// ≥ 384-bit digests for U.S. federal traffic by 2027; AlgSHA384
	// is the minimum-cost compliant choice.
	AlgSHA384 Algorithm = "sha-384"
	// AlgSHA512 is SHA-512 per FIPS 180-4. Faster than SHA-256 on
	// 64-bit hosts for inputs above ~64 bytes; chosen when wire
	// space is plentiful and CPU time matters.
	AlgSHA512 Algorithm = "sha-512"
	// AlgSHA3_256 is SHA3-256 per FIPS 202. Keccak sponge
	// construction; preferred where length-extension resistance
	// or NIST-approved diversity from the SHA-2 family is needed.
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
