// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package sign defines the public-key signing seams used by every
// thesmos library that produces or verifies receipts, audit
// envelopes, batch-root attestations, or epoch-close signatures.
//
// The seam exists so library code can sign and verify through an
// injected [Signer] / [Verifier] without binding to a specific
// algorithm. Verifier-only consumers (audit services, webhook
// receivers, offline auditors) construct a [Verifier] from raw
// public-key bytes without ever holding a private key — a
// load-bearing asymmetry in this package's design relative to
// the symmetric [crypto.MAC] seam.
//
// # Provided implementations
//
//   - go.thesmos.sh/core/crypto/sign/ed25519 — Ed25519 PureEdDSA
//     per RFC 8032 §5.1.6, backed by [crypto/ed25519]. [Signer]
//     only — see "Streaming" below.
//   - go.thesmos.sh/core/crypto/sign/ecdsap384 — ECDSA over NIST
//     P-384 with SHA-384 hashing per FIPS 186-5, ASN.1 DER
//     signatures, backed by [crypto/ecdsa]. Implements both
//     [Signer] and [StreamingSigner].
//
// Future additions land additively:
//
//   - PQ signatures (ML-DSA per FIPS 204, SLH-DSA per FIPS 205)
//     when the stdlib promotes them out of internal/fips140.
//     Both are hash-then-sign and will satisfy [StreamingSigner].
//   - Threshold signing (FROST per RFC 9591) ships as its own
//     subpackage when consumers need it; the trivial 1-of-1 case
//     reduces to plain Ed25519.
//
// FIPS-gated wrappers (hard-refusal-outside-FIPS-mode) live in
// consumer modules — they encode policy, not primitive. The
// non-gated implementations in this package run through
// Go's FIPS-validated module under `GODEBUG=fips140=on`
// transparently.
//
// # Algorithm vocabulary
//
// Each [Signer] reports its algorithm via [Verifier.Algorithm];
// the values come from the open-string vocabulary in
// [crypto.Algorithm] (for example [crypto.AlgEd25519],
// [crypto.AlgECDSAP384]). Receipts persist the algorithm string
// so verifiers select the matching [Verifier] implementation
// offline.
//
// # KeyID
//
// [KeyID] is a 16-byte value-type identifier for a specific
// public key, distinct from [crypto.ID] (which identifies an
// implementation, not a key). Each implementation derives a
// canonical [KeyID] from its public-key bytes via a per-package
// `KeyIDFromPub` helper:
//
//   - Ed25519: SHA-256(raw 32 public-key bytes)[:16].
//   - ECDSA P-384: SHA-256(SEC 1 uncompressed point:
//     0x04 || X(48 BE) || Y(48 BE))[:16].
//
// These derivations are part of the public contract — the same
// public key produces the same KeyID across builds, languages,
// and verifier services. The per-package `TestKeyIDStability`
// fixture locks the encoding via hardcoded vectors.
//
// # Streaming
//
// Hash-then-sign algorithms (ECDSA P-384 today; ML-DSA, SLH-DSA,
// Ed25519ph in the future) support streaming via the optional
// [StreamingSigner] / [StreamingVerifier] capability interfaces.
// Consumers signing arbitrary algorithms type-assert and fall
// back to whole-message [Signer.Sign] otherwise.
//
// Ed25519 PureEdDSA cannot stream. RFC 8032 §5.1.6 defines:
//
//	R = SHA-512(dom2 || prefix || M) mod L
//	S = (r + SHA-512(R || A || M) * s) mod L
//
// The message M appears in two SHA-512 computations, with the
// second depending on the first via R. A streaming API that
// accepted M once would force the implementation to buffer it
// internally — that's not streaming, it's "buffer and hash
// twice at finalize." The seam refuses to ship dishonest
// surface; Ed25519 [Signer] does not implement [StreamingSigner].
//
// # Failure semantics
//
// Two failure classes, two disciplines:
//
//   - Runtime errors — entropy exhaustion (ECDSA), transport
//     faults (future threshold), anything caused by the
//     environment — are returned through error channels.
//   - Verification failure returns a single bool. Distinguishing
//     malformed-signature from cryptographic-mismatch through
//     separate returns risks timing-side-channel oracles;
//     [Verifier.Verify] collapses every failure to false.
//     Callers requiring per-failure diagnostics validate
//     signature length and format separately before calling
//     Verify.
//
// Constructor errors are returned (wrong-curve, wrong-size key)
// — these are runtime input, not programmer error, and panic
// would conflict with the no-panic-in-production policy.
//
// # Allocation contract
//
// [Verifier.KeyID], [Verifier.PublicKey], [Verifier.Algorithm]
// are zero-allocation. [Verifier.Verify] is zero-allocation on
// Ed25519 (the stdlib primitive avoids heap allocation); ECDSA
// P-384 verification allocates because [crypto/ecdsa.VerifyASN1]
// performs big.Int arithmetic — a stdlib constraint we can't
// avoid without replacing the verification primitive.
// Implementations document their own allocation behaviour.
//
// [Signer.Sign] allocates the returned signature slice — the
// stdlib's signing primitives ([crypto/ed25519.Sign],
// [crypto/ecdsa.SignASN1]) do not expose buffer-passing APIs,
// so per-call allocation is unavoidable. Hot-path consumers
// amortise this by signing once per batch (batch-root mode)
// rather than per entry.
package sign
