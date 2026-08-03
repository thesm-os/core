// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto

import "context"

// Keeper wraps and unwraps data encryption keys.
//
// The data key crosses this boundary; the key protecting it does not.
// That is what allows a hardware module or a hosted service whose root
// key is non-exportable by design to implement the seam at all — a
// Keeper that returned root key material could not be.
//
// This is envelope encryption's custodian half: a per-object data key
// encrypts the payload through an [AEAD], and the Keeper protects the
// data key. Nothing here ever sees a payload.
//
// # Concurrency
//
// Implementations must be safe for concurrent use.
//
// # Allocation contract
//
// Unspecified. Every method crosses a process boundary in any
// implementation that matters, so allocation is not the cost that
// governs, and a contract only an in-memory implementation could meet
// would be a contract in name only.
type Keeper interface {
	// KeyID names the wrapping key in whatever form the custodian
	// uses — a resource identifier, a slot number, a label. Persist
	// it with each wrapped key so material wrapped before a rotation
	// can still be unwrapped after one.
	//
	// core cannot derive this and does not reshape it, which is why
	// it is the custodian's own string rather than an [ID] or a
	// [crypto/sign.KeyID].
	KeyID() string

	// Wrap encrypts a data key.
	//
	// Implementations must not wrap deterministically: two calls with
	// the same data key must produce different bytes, or a holder of
	// two wrapped keys could tell whether the underlying data keys
	// are equal without unwrapping either.
	Wrap(ctx context.Context, dek []byte) ([]byte, error)

	// Unwrap decrypts a data key. Material wrapped under a different
	// key, or corrupted in any position, is an error rather than a
	// wrong answer.
	Unwrap(ctx context.Context, wrapped []byte) ([]byte, error)
}

// Destroyer is the optional capability for custodians that can
// irreversibly destroy a wrapping key — the primitive underneath
// erasure of encrypted-at-rest data. The payload stays where it is and
// becomes unreadable, which is what makes erasure tractable for data
// that is replicated, backed up, or on tape.
//
// Callers requiring erasure assert for Destroyer at wiring time and
// fail fast when the assertion does not hold. A custodian that cannot
// destroy cannot provide erasure, and that must surface during
// configuration rather than during a deletion request the caller has
// already promised to honour.
type Destroyer interface {
	Keeper

	// Destroy irreversibly destroys the named wrapping key. Material
	// wrapped under it no longer unwraps, by this or any other
	// instance.
	//
	// Destroying a key the custodian does not hold is an error:
	// succeeding silently would leave a caller believing data was
	// erased when it was not.
	Destroy(ctx context.Context, keyID string) error
}

// KeyGenerator is the optional capability for custodians that can mint
// a data key internally and return it wrapped.
//
// This is how hosted custodians prefer to be used, and it is the
// stronger shape: the plaintext data key is generated inside the
// custodian's entropy boundary rather than the caller's, and a caller
// that only ever encrypts can discard the plaintext immediately and
// never hold key material it does not need.
type KeyGenerator interface {
	Keeper

	// GenerateKey returns a fresh data key of size bytes, both in the
	// clear and wrapped under this Keeper's key. Callers that only
	// encrypt should zero plaintext as soon as the payload is sealed.
	//
	// A non-positive size is an error.
	GenerateKey(ctx context.Context, size int) (plaintext, wrapped []byte, err error)
}
