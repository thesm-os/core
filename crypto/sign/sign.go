// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sign

import (
	"encoding/hex"
	"io"

	"go.thesmos.sh/core/crypto"
)

// KeyIDSize is the byte size of every [KeyID].
const KeyIDSize = 16

// KeyID identifies a specific public key. Persisted on every
// signed entry / receipt / audit record so verifiers dispatch
// through KeyID to look up the matching public key in their
// trust store.
//
// KeyID is distinct from [crypto.ID]: ID identifies an
// *implementation* (for example "ed25519/v1"); KeyID identifies
// a *key* (the SHA-256 prefix of a specific public key). Two
// different signing-implementation builds that both hold the
// same Ed25519 public key produce the same KeyID — that's the
// load-bearing property for verifier routing.
//
// Each per-algorithm package ships a `KeyIDFromPub` helper that
// derives KeyID from public-key bytes via a canonical encoding:
//
//   - Ed25519: SHA-256(raw 32 public-key bytes)[:16].
//   - ECDSA P-384: SHA-256(SEC 1 uncompressed encoding,
//     0x04 || X(48 BE) || Y(48 BE), 97 bytes)[:16].
//
// These encodings are part of the public contract. The
// per-package `TestKeyIDStability` fixture locks the bytes via
// hardcoded vectors.
//
// # Allocation contract
//
// Value type; pass by value. Zero alloc.
type KeyID [KeyIDSize]byte

// String returns the hex-encoded KeyID. Allocates the result
// string; intended for diagnostic output, not the hot path.
func (k KeyID) String() string {
	return hex.EncodeToString(k[:])
}

// Verifier verifies signatures over messages with a public key.
//
// # Method semantics
//
//   - [Verifier.KeyID] returns the canonical key identifier for
//     this Verifier's public key. Persist alongside signatures
//     so verifiers dispatch through KeyID.
//   - [Verifier.PublicKey] returns the public-key bytes in the
//     algorithm's canonical encoding. Format is documented per
//     implementation: Ed25519 returns the raw 32 bytes; ECDSA
//     P-384 returns PKIX (X.509 SubjectPublicKeyInfo) DER bytes.
//   - [Verifier.Algorithm] returns the long-term cross-build
//     algorithm name (for example [crypto.AlgEd25519]). Persist
//     it in artefacts that may outlive the producing build.
//   - [Verifier.Verify] reports whether sig is a valid signature
//     over msg under the embedded public key. Returns false for
//     any reason verification cannot succeed — wrong public key,
//     malformed signature bytes, signature length mismatch,
//     cryptographic invalidity. The single bool collapses these
//     by design: distinguishing them risks timing-side-channel
//     oracles. Callers requiring per-failure diagnostics
//     validate signature length and format separately before
//     calling Verify.
//
// # Concurrency
//
// Implementations of [Verifier] must be safe for concurrent use;
// consumers share one Verifier across many goroutines.
//
// # Allocation contract
//
// [Verifier.KeyID], [Verifier.PublicKey], [Verifier.Algorithm]
// are zero-allocation on every implementation in this module.
// [Verifier.Verify] is zero-allocation on Ed25519 (the stdlib
// primitive does not allocate); ECDSA P-384 verification
// allocates because [crypto/ecdsa.VerifyASN1] performs big.Int
// arithmetic. Implementations document their own allocation
// behaviour.
type Verifier interface {
	// KeyID returns the canonical key identifier for this
	// Verifier's public key. Persist alongside signatures so
	// verifiers dispatch through KeyID to look up the matching
	// public key in their trust store.
	KeyID() KeyID

	// PublicKey returns the public-key bytes in the algorithm's
	// canonical encoding. Format is documented per
	// implementation: Ed25519 returns the raw 32 bytes; ECDSA
	// P-384 returns PKIX (X.509 SubjectPublicKeyInfo) DER bytes.
	// The returned slice aliases internal storage; callers must
	// treat it as immutable.
	PublicKey() []byte

	// Algorithm returns the long-term cross-build algorithm
	// name (for example [crypto.AlgEd25519]). Persist it in
	// artefacts that may outlive the producing build.
	Algorithm() crypto.Algorithm

	// Verify reports whether signature is a valid signature
	// over message under this Verifier's public key. Returns
	// false for any reason verification cannot succeed —
	// wrong public key, malformed signature bytes, signature
	// length mismatch, cryptographic invalidity. The single
	// bool collapses these by design: distinguishing them
	// risks timing-side-channel oracles.
	Verify(message, signature []byte) bool
}

// Signer produces signatures over messages with a private key.
// Every Signer is also a [Verifier] because it holds the
// matching public key — this composition lets generic code that
// only verifies accept either type without an additional
// adapter.
//
// # Method semantics
//
//   - All [Verifier] methods carry forward.
//   - [Signer.Sign] returns a freshly-allocated signature over
//     msg. The stdlib's underlying primitives ([crypto/ed25519.Sign],
//     [crypto/ecdsa.SignASN1]) do not expose buffer-passing
//     APIs, so per-call allocation is unavoidable in this
//     implementation. Hot-path consumers amortise this by
//     signing once per batch (batch-root mode) rather than per
//     entry.
//
// Sign signs the bytes passed in as-is. Ed25519 signs the raw
// message per RFC 8032 §5.1.6 (PureEdDSA, not Ed25519ph). ECDSA
// P-384 internally hashes with SHA-384 per FIPS 186-5 before
// signing. Messages too large to buffer should be hashed
// externally to a Merkle root via [crypto.Hasher.NewStream]; the
// root is then signed as a normal small message. This package
// does not implement Ed25519ph (RFC 8032 §5.1) — external
// verifiers expecting PreHash will not accept signatures
// produced here.
//
// # Concurrency
//
// Implementations of [Signer] must be safe for concurrent use.
//
// # Streaming capability
//
// Hash-then-sign algorithms (ECDSA P-384 today; future ML-DSA,
// SLH-DSA, Ed25519ph) additionally implement [StreamingSigner].
// Ed25519 PureEdDSA does not — see the package documentation's
// "Streaming" section.
type Signer interface {
	Verifier

	// Sign returns a freshly-allocated signature over message.
	// The stdlib's underlying primitives ([crypto/ed25519.Sign],
	// [crypto/ecdsa.SignASN1]) do not expose buffer-passing
	// APIs, so per-call allocation is unavoidable. Hot-path
	// consumers amortise this by signing once per batch
	// (batch-root mode) rather than per entry.
	//
	// Sign signs the bytes passed in as-is. Ed25519 signs the
	// raw message per RFC 8032 §5.1.6 (PureEdDSA, not Ed25519ph);
	// ECDSA P-384 internally hashes with SHA-384 per FIPS 186-5
	// before signing. Messages too large to buffer should be
	// hashed externally to a Merkle root via
	// [crypto.Hasher.NewStream] and the root signed as a normal
	// small message.
	Sign(message []byte) ([]byte, error)
}

// StreamingSigner is the optional capability interface for
// [Signer] implementations whose signing operation is
// hash-then-sign (the message is absorbed by an incremental
// hash, then the curve / lattice operation runs once on the
// digest at finalise). Consumers signing arbitrary algorithms
// type-assert to access streaming and fall back to whole-message
// [Signer.Sign] when the assertion fails.
//
// Ed25519 PureEdDSA does not satisfy this interface — its
// signing equation requires the message in two separate hash
// computations, so a streaming API would force internal
// buffering.
type StreamingSigner interface {
	Signer

	// NewSignStream returns a fresh [SignStream] that absorbs
	// bytes via Write and finalises to a signature via
	// [SignStream.SignAndReset]. The returned stream is
	// single-goroutine; one stream per goroutine that streams.
	NewSignStream() SignStream
}

// SignStream is an in-progress hash-then-sign computation.
// Bytes are absorbed via Write (embedded [io.Writer]);
// [SignStream.SignAndReset] finalises the digest, runs the
// signing operation, returns the signature, and resets the
// stream so the next message can be streamed into the same
// instance.
//
// SignStream is the symmetric pair of [VerifyStream] — produced
// by [StreamingSigner.NewSignStream] for hash-then-sign
// algorithms (ECDSA P-384 today; ML-DSA, SLH-DSA, Ed25519ph in
// the future).
//
// # Concurrency
//
// SignStream is NOT safe for concurrent use. Each goroutine that
// streams should hold its own SignStream.
//
// # Allocation contract
//
// Write is zero-allocation. [SignStream.SignAndReset] allocates
// the returned signature slice (stdlib constraint — see
// [Signer.Sign]).
//
//nolint:revive // SignStream / VerifyStream is an intentional symmetric pair; renaming one breaks the symmetry, renaming both just trades stutter for verbosity.
type SignStream interface {
	io.Writer

	// SignAndReset finalises the in-progress hash, signs the
	// resulting digest, and returns the signature. After this
	// call the stream is in the same state as a freshly-returned
	// StreamingSigner.NewSignStream result and can be reused.
	SignAndReset() ([]byte, error)
}

// StreamingVerifier is the optional capability interface for
// [Verifier] implementations whose verification operation is
// hash-then-verify (the message is absorbed by an incremental
// hash, then the curve / lattice verification runs once on the
// digest at finalise).
//
// Mirrors [StreamingSigner]: ECDSA P-384 satisfies it today,
// PQ-signature implementations will, Ed25519 does not.
type StreamingVerifier interface {
	Verifier

	// NewVerifyStream returns a fresh [VerifyStream] that
	// absorbs bytes via Write and finalises to a verification
	// outcome via [VerifyStream.Verify]. The returned stream
	// is single-use and single-goroutine.
	NewVerifyStream() VerifyStream
}

// VerifyStream is an in-progress hash-then-verify computation.
// Bytes are absorbed via Write (embedded [io.Writer]);
// [VerifyStream.Verify] finalises the digest, verifies sig
// against it, and reports the outcome. The stream is single-use
// — call [StreamingVerifier.NewVerifyStream] for the next
// message.
//
// # Concurrency
//
// VerifyStream is NOT safe for concurrent use.
//
// # Allocation contract
//
// Write and [VerifyStream.Verify] are zero-allocation in the
// success path.
type VerifyStream interface {
	io.Writer

	// Verify finalises the in-progress hash and reports whether
	// sig is a valid signature over the absorbed bytes. The
	// stream is consumed by this call; callers create a new one
	// via StreamingVerifier.NewVerifyStream for the next
	// message.
	Verify(sig []byte) bool
}
