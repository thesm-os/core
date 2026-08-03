// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto

import (
	"crypto/cipher"

	"go.thesmos.sh/core/rand"
)

// AEAD is an authenticated-encryption primitive with associated data.
// It embeds the standard library contract verbatim and adds identity.
//
// Callers persist [AEAD.Algorithm] alongside the ciphertext and select
// the matching implementation at open time, exactly as they persist
// [Hasher.Algorithm] alongside a digest. That is what lets a
// ciphertext written today be opened by a build that does not yet
// exist, by an implementation chosen at run time rather than compile
// time.
//
// The embedded [cipher.AEAD] is available for callers managing their
// own nonces — a counter under a per-message key, for instance, which
// is both correct and cheaper than randomness. Everyone else uses
// [Seal] and [Open], which manage the nonce.
//
// # Concurrency
//
// Implementations must be safe for concurrent use.
type AEAD interface {
	// Seal and Open panic when nonce is not NonceSize() bytes, as
	// [cipher.AEAD] documents. That is inherited, not chosen: this
	// seam embeds the standard library contract so a cipher.AEAD
	// from anywhere satisfies it without an adapter, and the panic
	// comes with it.
	//
	// It is also the only panicking surface in this module. Use
	// [Seal] and [Open], which construct the nonce themselves and
	// cannot reach the panicking path; reach for these methods only
	// when managing nonces deliberately, and validate the length
	// first.
	cipher.AEAD

	// ID is the build-local implementation identifier.
	ID() ID

	// Algorithm is the long-term, cross-build name. Persist it
	// alongside every ciphertext.
	Algorithm() Algorithm
}

// Seal draws a fresh random nonce from r, encrypts plaintext under a
// with aad as associated data, and returns nonce || ciphertext.
//
// The nonce is prepended rather than returned separately because a
// nonce stored apart from its ciphertext is a nonce that gets lost,
// and a lost nonce makes the ciphertext unrecoverable.
//
// Use this unless you have a specific reason to manage nonces
// yourself. Reusing a nonce under one key breaks AES-GCM completely:
// it discloses the XOR of the two plaintexts and permits tag forgery
// for every message under that key. Passing a deterministic
// [rand.Rand] — a fixed or seeded source — reuses the nonce by
// construction and must never be done outside a test asserting that
// property.
//
// Random 96-bit nonces are safe for any message volume a single key
// will realistically see; the birthday bound is roughly 2^32 messages
// per key for a 2^-32 collision probability.
//
// # Allocation contract
//
// One allocation, of NonceSize() + len(plaintext) + Overhead() bytes.
// Callers managing their own buffers use the embedded
// [cipher.AEAD.Seal] directly, which appends into a caller-supplied
// destination.
func Seal(a AEAD, r rand.Rand, plaintext, aad []byte) ([]byte, error) {
	nonceSize := a.NonceSize()

	out := make([]byte, nonceSize, nonceSize+len(plaintext)+a.Overhead())
	if _, err := r.Read(out[:nonceSize]); err != nil {
		return nil, err
	}

	return a.Seal(out, out[:nonceSize], plaintext, aad), nil
}

// Open splits sealed into its nonce prefix and ciphertext, and
// decrypts it under a with aad as associated data.
//
// Returns [ErrCiphertextShort] when sealed is smaller than
// a.NonceSize(). Every other failure — a modified ciphertext, a
// modified tag, mismatched associated data, the wrong key — returns
// the underlying [cipher.AEAD] error unwrapped and indistinguishable
// from the others. That is deliberate: a caller must not be able to
// branch on why authentication failed.
//
// A successfully opened empty plaintext returns nil rather than an
// empty non-nil slice, following [cipher.AEAD.Open]. Callers compare
// contents or length, not nil-ness; guaranteeing non-nil would cost
// an allocation to carry a distinction the payload does not have.
//
// # Allocation contract
//
// One allocation for the returned plaintext.
func Open(a AEAD, sealed, aad []byte) ([]byte, error) {
	nonceSize := a.NonceSize()
	if len(sealed) < nonceSize {
		return nil, ErrCiphertextShort
	}

	plaintext, err := a.Open(nil, sealed[:nonceSize], sealed[nonceSize:], aad)
	if err != nil {
		return nil, err //nolint:wrapcheck // authentication failures must stay indistinguishable
	}

	return plaintext, nil
}
