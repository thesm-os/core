// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package sign defines the public-key signing seams: [Signer], which
// signs, and [Verifier], which checks a signature against a public key.
//
// Library code signs and verifies through an injected [Signer] or
// [Verifier] without binding to an algorithm. A verifier-only consumer,
// such as an audit service, a webhook receiver or an offline auditor,
// builds a [Verifier] from public-key bytes and never has a private
// key. The symmetric [crypto.MAC] seam cannot separate the two roles.
//
// # Provided implementations
//
//   - go.thesmos.sh/core/crypto/sign/ed25519: Ed25519 PureEdDSA per
//     RFC 8032 §5.1.6, backed by [crypto/ed25519]. [Signer] only, see
//     Streaming.
//   - go.thesmos.sh/core/crypto/sign/ecdsap384: ECDSA over NIST P-384
//     with SHA-384 per FIPS 186-5 and ASN.1 DER signatures, backed by
//     [crypto/ecdsa]. Implements [Signer] and [StreamingSigner].
//   - go.thesmos.sh/core/crypto/sign/mldsa: ML-DSA-44, ML-DSA-65 and
//     ML-DSA-87 per FIPS 204, backed by [crypto/mldsa]. [Signer] only.
//
// The implementations run through Go's FIPS 140-3 module when
// GODEBUG=fips140=on is set. This package does not refuse to run
// outside FIPS mode. A caller that must refuse applies that policy
// itself.
//
// # Algorithm vocabulary
//
// Each [Signer] reports its algorithm through [Verifier.Algorithm] as a
// value of the open-string vocabulary [crypto.Algorithm], for example
// [crypto.AlgEd25519] or [crypto.AlgMLDSA87]. Receipts persist the
// algorithm string, so a verifier selects the matching [Verifier]
// implementation offline.
//
// # KeyID
//
// [KeyID] is a 16-byte value that identifies one public key. It differs
// from [crypto.ID], which identifies an implementation. Each
// implementation derives a canonical [KeyID] from its public-key bytes
// through a KeyIDFromPub function in its own package:
//
//   - Ed25519: SHA-256(raw 32 public-key bytes)[:16].
//   - ECDSA P-384: SHA-256(SEC 1 uncompressed point:
//     0x04 || X(48 BE) || Y(48 BE))[:16].
//   - ML-DSA: SHA-256(FIPS 204 public-key encoding)[:16].
//
// These derivations are part of the public contract. The same public
// key produces the same KeyID across builds, languages and verifier
// services. Each package's TestKeyIDStability pins its derivation with
// fixed vectors.
//
// # Streaming
//
// A hash-then-sign algorithm can sign a stream through the optional
// [StreamingSigner] and [StreamingVerifier] capabilities. ECDSA P-384
// implements them. A consumer that signs with any algorithm asserts for
// the capability and falls back to whole-message [Signer.Sign].
//
// Ed25519 PureEdDSA cannot stream. RFC 8032 §5.1.6 defines:
//
//	R = SHA-512(dom2 || prefix || M) mod L
//	S = (r + SHA-512(R || A || M) * s) mod L
//
// The message M appears in two SHA-512 computations, and the second
// depends on the first through R. A streaming signer would have to
// buffer M and hash it twice when the stream closes, so the Ed25519
// [Signer] does not implement [StreamingSigner].
//
// # Signing across a process boundary
//
// A [Signer] backed by a hosted key service or a hardware module
// implements [ContextSigner], so a caller can bound the wait with a
// context. Callers sign through [SignContext], which uses the
// capability when a signer has it and calls [Signer.Sign] otherwise.
// The in-process signers in this module need no context and do not
// implement it.
//
// # Failure semantics
//
// The package separates two classes of failure:
//
//   - Runtime errors are returned. They include entropy exhaustion in
//     ECDSA signing and anything else the environment causes.
//   - A failed verification returns false. Separate results for a
//     malformed signature and a cryptographic mismatch would risk a
//     timing side channel, so [Verifier.Verify] collapses every
//     failure to false. A caller that needs per-failure diagnostics
//     checks the signature's length and format before it calls Verify.
//
// Constructors return errors for a wrong curve or a wrong key size.
// Those are runtime input and not programmer errors, and the module
// does not panic in production code.
//
// # Generate API asymmetry
//
// The constructors differ deliberately. Each one's signature reflects
// what the underlying standard library function honours:
//
//   - [go.thesmos.sh/core/crypto/sign/ed25519.Generate] and
//     [go.thesmos.sh/core/crypto/sign/mldsa.Generate] take a
//     [go.thesmos.sh/core/rand.Rand]. [crypto/ed25519.GenerateKey]
//     honours its [io.Reader], and the ML-DSA Generate reads its
//     32-byte seed from the source. A deterministic source, such as
//     [go.thesmos.sh/core/rand/seeded.Rand], produces deterministic
//     keys for tests.
//   - [go.thesmos.sh/core/crypto/sign/ecdsap384.Generate] takes no
//     argument. Since Go 1.26, [crypto/ecdsa.GenerateKey] ignores its
//     [io.Reader] and reads the runtime's internal entropy unless
//     GODEBUG=cryptocustomrand=1 is set. A source parameter that the
//     standard library ignores would mislead the caller. Tests that
//     need deterministic ECDSA keys use
//     [testing/cryptotest.SetGlobalRandom], which seeds the runtime's
//     internal generator.
//
// # Allocation contract
//
// [Verifier.KeyID], [Verifier.PublicKey] and [Verifier.Algorithm] are
// zero-allocation. [Verifier.Verify] is zero-allocation for Ed25519 and
// ML-DSA. ECDSA P-384 verification allocates, because
// [crypto/ecdsa.VerifyASN1] performs big.Int arithmetic.
// Implementations document their own allocation behaviour.
//
// [Signer.Sign] allocates the returned signature, because the standard
// library's signing functions, [crypto/ed25519.Sign],
// [crypto/ecdsa.SignASN1] and [crypto/mldsa.PrivateKey.Sign], take no
// destination buffer. A hot-path consumer signs once per batch and not
// once per entry.
package sign
