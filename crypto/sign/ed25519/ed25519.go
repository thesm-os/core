// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package ed25519

import (
	stded25519 "crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sign"
	"go.thesmos.sh/core/rand"
)

// ErrInvalidPublicKeySize is returned when a verifier
// constructor receives a public-key byte slice whose length is
// not [crypto/ed25519.PublicKeySize] (32 bytes).
var ErrInvalidPublicKeySize = errors.New("crypto/sign/ed25519: public key must be 32 bytes")

// ErrInvalidPrivateKeySize is returned when a signer
// constructor receives a private-key byte slice whose length is
// not [crypto/ed25519.PrivateKeySize] (64 bytes — Go's expanded
// representation including the public-key suffix).
var ErrInvalidPrivateKeySize = errors.New(
	"crypto/sign/ed25519: private key must be 64 bytes (Go expanded representation)",
)

// Verifier verifies Ed25519 signatures against a fixed public
// key. Safe for concurrent use.
//
// # Allocation contract
//
// [Verifier.KeyID], [Verifier.PublicKey], [Verifier.Algorithm]
// are zero-alloc. [Verifier.Verify] is zero-alloc on the success
// path — [crypto/ed25519.Verify] does not allocate.
type Verifier struct {
	pub   stded25519.PublicKey
	keyID sign.KeyID
}

// Compile-time interface check.
var _ sign.Verifier = (*Verifier)(nil)

// NewVerifier wraps a public key in a [Verifier]. The public-key
// bytes are retained by reference; callers must not mutate the
// underlying buffer afterwards.
func NewVerifier(pub stded25519.PublicKey) (*Verifier, error) {
	if len(pub) != stded25519.PublicKeySize {
		return nil, ErrInvalidPublicKeySize
	}
	return &Verifier{pub: pub, keyID: KeyIDFromPub(pub)}, nil
}

// NewVerifierFromBytes wraps a 32-byte public-key slice as a
// [Verifier]. The bytes are copied; callers may zero or reuse
// the source buffer immediately.
func NewVerifierFromBytes(pubBytes []byte) (*Verifier, error) {
	if len(pubBytes) != stded25519.PublicKeySize {
		return nil, ErrInvalidPublicKeySize
	}
	pub := make(stded25519.PublicKey, stded25519.PublicKeySize)
	copy(pub, pubBytes)
	return &Verifier{pub: pub, keyID: KeyIDFromPub(pub)}, nil
}

// KeyID returns the canonical key identifier
// SHA-256(public-key)[:16].
func (v *Verifier) KeyID() sign.KeyID { return v.keyID }

// PublicKey returns the 32-byte raw Ed25519 public key. The
// returned slice aliases internal storage; callers must treat
// it as immutable.
func (v *Verifier) PublicKey() []byte { return v.pub }

// Algorithm returns [crypto.AlgEd25519].
func (*Verifier) Algorithm() crypto.Algorithm { return crypto.AlgEd25519 }

// Verify reports whether sig is a valid Ed25519 signature over
// msg under v's public key. Returns false for any reason
// verification cannot succeed (wrong key, malformed signature,
// length mismatch, cryptographic invalidity).
func (v *Verifier) Verify(msg, sig []byte) bool {
	return stded25519.Verify(v.pub, msg, sig)
}

// Signer signs messages with a fixed Ed25519 private key. Safe
// for concurrent use.
type Signer struct {
	*Verifier
	priv stded25519.PrivateKey
}

// Compile-time interface check.
var _ sign.Signer = (*Signer)(nil)

// New wraps an existing Ed25519 private key in a [Signer]. The
// private-key bytes are copied; callers may zero or reuse the
// source buffer immediately after construction. The defensive
// copy closes the foot-gun where a caller follows secure-coding
// practice and zeroes the key, only to find subsequent [Sign]
// calls produce silently invalid signatures.
//
// The 64-byte private-key representation embeds the matching
// public key in its trailing 32 bytes (per
// [crypto/ed25519.PrivateKey]), so [Verifier.PublicKey] /
// [Verifier.KeyID] are derived without a separate stdlib call.
func New(priv stded25519.PrivateKey) (*Signer, error) {
	if len(priv) != stded25519.PrivateKeySize {
		return nil, ErrInvalidPrivateKeySize
	}
	privCopy := make(stded25519.PrivateKey, stded25519.PrivateKeySize)
	copy(privCopy, priv)
	// privCopy[PublicKeySize:] is exactly 32 bytes, so
	// NewVerifier cannot fail on the size check.
	pub := stded25519.PublicKey(privCopy[stded25519.PublicKeySize:])
	v, _ := NewVerifier(pub)
	return &Signer{Verifier: v, priv: privCopy}, nil
}

// Generate creates a fresh Ed25519 keypair drawing from r. The
// caller-supplied [rand.Rand] is wrapped to satisfy stdlib's
// [io.Reader] expectation. Tests pass a deterministic
// [rand/seeded.Rand] for reproducible key generation.
func Generate(r rand.Rand) (*Signer, error) {
	pub, priv, err := stded25519.GenerateKey(randReader{r: r})
	if err != nil {
		return nil, fmt.Errorf("crypto/sign/ed25519: generate: %w", err)
	}
	// pub is always 32 bytes from a successful GenerateKey,
	// so NewVerifier cannot fail.
	v, _ := NewVerifier(pub)
	return &Signer{Verifier: v, priv: priv}, nil
}

// Sign returns the Ed25519 signature for msg per RFC 8032
// §5.1.6 (PureEdDSA). The 64-byte signature is freshly
// allocated; the stdlib offers no buffer-passing API.
//
// Returns a nil error always — Ed25519 is deterministic and has
// no failure path. The error return is for [sign.Signer]
// interface conformance.
func (s *Signer) Sign(msg []byte) ([]byte, error) {
	return stded25519.Sign(s.priv, msg), nil
}

// KeyIDFromPub derives the canonical [sign.KeyID] from a public
// key: SHA-256(raw 32 public-key bytes)[:16]. Same key produces
// the same KeyID across runs, builds, and verifier services —
// that's the load-bearing property for receipt-routing trust
// stores.
//
// # Allocation contract
//
// Zero alloc.
func KeyIDFromPub(pub stded25519.PublicKey) sign.KeyID {
	h := sha256.Sum256(pub)
	var id sign.KeyID
	copy(id[:], h[:sign.KeyIDSize])
	return id
}

// randReader adapts a [rand.Rand] to [io.Reader] so stdlib
// constructors that take a Reader can consume our seam-friendly
// random source. Identical to the adapter in id/ulid, id/uuidv4,
// id/ksuid.
type randReader struct{ r rand.Rand }

// Read fills p from the wrapped [rand.Rand]. Always returns
// (len(p), nil) — the [rand.Rand] contract has no error path.
func (rr randReader) Read(p []byte) (int, error) {
	return rr.r.Read(p)
}
