// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package localkey provides an in-process [crypto.Keeper] for tests
// and local development.
//
// # This is not custody
//
// The root key lives in this process's memory. It can be read from a
// core dump, from /proc, by a debugger, and by any code in the same
// address space. Nothing here is a hardware module or a hosted key
// service, and nothing here should hold a key protecting production
// data.
//
// It exists so the conformance suite has a subject, and so a caller
// can exercise an envelope-encryption path end to end without
// provisioning a custodian.
//
// # What it does model faithfully
//
// The observable contract: wrapping is non-deterministic, tampered
// material fails rather than returning a wrong answer, and
// [Keeper.Destroy] makes previously wrapped material permanently
// unreadable. Code written against this Keeper works unchanged
// against a real custodian; only the guarantee behind it changes.
package localkey

import (
	"context"
	"crypto/subtle"
	"sync"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/aesgcm"
	"go.thesmos.sh/core/rand"
)

// RootKeySize is the required root-key length, selecting AES-256-GCM
// as the wrapping construction.
const RootKeySize = aesgcm.KeySize256

// Keeper is an in-process [crypto.Keeper]. It also satisfies
// [crypto.Destroyer] and [crypto.KeyGenerator].
//
// # Concurrency
//
// Safe for concurrent use. Destroy races against in-flight Wrap and
// Unwrap calls without either observing a half-destroyed key.
// Field order groups the pointer-bearing fields ahead of the mutex,
// which narrows the range the garbage collector scans.
type Keeper struct {
	rand rand.Rand

	// aead is guarded by mu, which Destroy takes to clear it. Wrap
	// and Unwrap read it under the lock and use the value
	// afterwards: crypto.AEAD is itself safe for concurrent use, so
	// only the pointer swap needs guarding.
	aead  crypto.AEAD
	keyID string

	mu sync.RWMutex
}

// Compile-time proof that Keeper satisfies the seam and both
// capabilities. A caller type-asserts for these at wiring time, so a
// method dropped by a refactor must fail the build rather than the
// assertion.
var (
	_ crypto.Keeper       = (*Keeper)(nil)
	_ crypto.Destroyer    = (*Keeper)(nil)
	_ crypto.KeyGenerator = (*Keeper)(nil)
)

// New returns a Keeper wrapping data keys under rootKey, identified by
// keyID.
//
// rootKey must be [RootKeySize] bytes; any other length returns
// [crypto.ErrKeySize]. An empty keyID returns [crypto.ErrKeyID] —
// the identifier is persisted with every wrapped key, so an empty one
// strands the material it names.
//
// rootKey is copied into the cipher's key schedule, so the caller may
// zero its own copy immediately afterwards. r supplies the nonce for
// each wrap and must be a cryptographically secure source; a
// deterministic one repeats nonces and breaks the construction.
func New(keyID string, rootKey []byte, r rand.Rand) (*Keeper, error) {
	if keyID == "" {
		return nil, crypto.ErrKeyID
	}

	// aesgcm validates first, so its error is a live path rather than
	// dead defensive code: every length AES rejects arrives here. The
	// narrower check below then rejects the one length AES accepts
	// and this package does not.
	a, err := aesgcm.New(rootKey)
	if err != nil {
		return nil, err
	}

	if len(rootKey) != RootKeySize {
		return nil, crypto.ErrKeySize
	}

	return &Keeper{keyID: keyID, rand: r, aead: a}, nil
}

// KeyID returns the identifier given at construction.
func (k *Keeper) KeyID() string { return k.keyID }

// Wrap encrypts dek under the root key.
//
// Each call draws a fresh nonce, so wrapping one data key twice
// produces different bytes. Returns [crypto.ErrKeyDestroyed] once
// [Keeper.Destroy] has run.
func (k *Keeper) Wrap(_ context.Context, dek []byte) ([]byte, error) {
	a, err := k.cipher()
	if err != nil {
		return nil, err
	}

	return crypto.Seal(a, k.rand, dek, nil)
}

// Unwrap decrypts a wrapped data key.
//
// Material corrupted in any position, truncated, or wrapped under a
// different root key fails authentication and returns an error rather
// than wrong key material. Returns [crypto.ErrKeyDestroyed] once
// [Keeper.Destroy] has run — distinct from a corruption failure,
// because the two have different remedies.
func (k *Keeper) Unwrap(_ context.Context, wrapped []byte) ([]byte, error) {
	a, err := k.cipher()
	if err != nil {
		return nil, err
	}

	return crypto.Open(a, wrapped, nil)
}

// GenerateKey mints a fresh data key of size bytes and returns it both
// in the clear and wrapped.
//
// A non-positive size returns [crypto.ErrKeySize]. Callers that only
// encrypt should zero plaintext once the payload is sealed.
func (k *Keeper) GenerateKey(ctx context.Context, size int) (plaintext, wrapped []byte, err error) {
	if size <= 0 {
		return nil, nil, crypto.ErrKeySize
	}

	// Read before the destroyed check is harmless — the key material
	// is discarded if Wrap then fails.
	dek := make([]byte, size)
	if _, err = k.rand.Read(dek); err != nil {
		return nil, nil, err
	}

	wrapped, err = k.Wrap(ctx, dek)
	if err != nil {
		return nil, nil, err
	}

	return dek, wrapped, nil
}

// Destroy irreversibly destroys the root key, after which no material
// wrapped under it can be recovered by this Keeper.
//
// keyID must name this Keeper's key; any other value returns
// [crypto.ErrKeyID], because succeeding silently would leave a caller
// believing data was erased when it was not. Destroying an
// already-destroyed key returns [crypto.ErrKeyDestroyed] rather than
// succeeding, for the same reason.
//
// The guarantee here is only as strong as process memory: this drops
// the cipher and zeroes what it can reach, but Go gives no way to
// guarantee no copy of the key schedule survives elsewhere in the
// heap. A real custodian destroys the key inside its own boundary.
func (k *Keeper) Destroy(_ context.Context, keyID string) error {
	if subtle.ConstantTimeCompare([]byte(keyID), []byte(k.keyID)) != 1 {
		return crypto.ErrKeyID
	}

	k.mu.Lock()
	defer k.mu.Unlock()

	if k.aead == nil {
		return crypto.ErrKeyDestroyed
	}
	k.aead = nil

	return nil
}

// cipher returns the wrapping cipher, or [crypto.ErrKeyDestroyed] if
// the key is gone.
func (k *Keeper) cipher() (crypto.AEAD, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()

	if k.aead == nil {
		return nil, crypto.ErrKeyDestroyed
	}

	return k.aead, nil
}
