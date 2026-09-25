// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package localkey provides an in-process [crypto.Keeper] for tests
// and local development.
//
// # This is not custody
//
// The root key is stored in this process's memory. It can be read from a
// core dump, from /proc, by a debugger, and by any code in the same
// address space. Nothing here is a hardware module or a hosted key
// service. Do not use it to protect production data.
//
// It exists so the conformance suite has a subject, and so a caller
// can exercise an envelope-encryption path end to end without
// provisioning a custodian.
//
// # What it does model faithfully
//
// It keeps the observable contract of a real custodian. Wrapping is
// non-deterministic, tampered material fails instead of returning a
// wrong result, and [Keeper.Destroy] makes previously wrapped material
// permanently unreadable. Code written against this Keeper works
// unchanged against a real custodian. Only the guarantee behind it
// differs.
package localkey

import (
	"context"
	"crypto/subtle"
	"sync"
	"time"

	"go.thesmos.sh/core/clock"
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
	rand  rand.Rand
	clock clock.Clock

	// aead is guarded by mu, which Destroy takes to clear it. Wrap
	// and Unwrap read it under the lock and use the value
	// afterwards: crypto.AEAD is itself safe for concurrent use, so
	// only the pointer swap needs guarding.
	aead crypto.AEAD

	// destroyedAt is the time of the first Destroy, guarded by mu.
	destroyedAt time.Time

	keyID string

	mu sync.RWMutex
}

// Compile-time proof that Keeper satisfies the seam and both
// capabilities. A caller type-asserts for these at wiring time, so a
// method dropped by a refactor fails the build and not the assertion.
var (
	_ crypto.Keeper       = (*Keeper)(nil)
	_ crypto.Destroyer    = (*Keeper)(nil)
	_ crypto.KeyGenerator = (*Keeper)(nil)
)

// New returns a Keeper wrapping data keys under rootKey, identified by
// keyID.
//
// rootKey must be [RootKeySize] bytes. Any other length returns
// [crypto.ErrKeySize]. An empty keyID returns [crypto.ErrKeyID]: the
// identifier is persisted with every wrapped key, and an empty one does
// not identify the key that unwraps the material.
//
// rootKey is copied into the cipher's key schedule, so the caller may
// zero its own copy immediately afterwards. The cipher is AES-256-GCM
// from [aesgcm.NewRandomNonce], which generates every nonce inside the
// standard library's FIPS 140-3 module, so New also succeeds in FIPS
// 140-only mode. r supplies the data keys [Keeper.GenerateKey] returns
// and must be a cryptographically secure source. c supplies the time
// [Keeper.Destroy] reports.
func New(keyID string, rootKey []byte, r rand.Rand, c clock.Clock) (*Keeper, error) {
	if keyID == "" {
		return nil, crypto.ErrKeyID
	}

	// aesgcm validates first, so its error path is live: every length
	// AES rejects returns here. The narrower check below then rejects
	// the one length AES accepts and this package does not.
	a, err := aesgcm.NewRandomNonce(rootKey)
	if err != nil {
		return nil, err
	}

	if len(rootKey) != RootKeySize {
		return nil, crypto.ErrKeySize
	}

	return &Keeper{keyID: keyID, rand: r, clock: c, aead: a}, nil
}

// KeyID returns the identifier given at construction.
func (k *Keeper) KeyID() string { return k.keyID }

// Wrap encrypts dek under the root key.
//
// The standard library's FIPS 140-3 module generates a fresh 96-bit
// nonce for each call, so wrapping one data key twice produces
// different bytes. Random nonces are safe for at most 2^32 wraps under
// one root key. Returns [crypto.ErrKeyDestroyed] once [Keeper.Destroy]
// has run.
func (k *Keeper) Wrap(_ context.Context, dek []byte) ([]byte, error) {
	a, err := k.cipher()
	if err != nil {
		return nil, err
	}

	// The cipher generates its own nonce, so Seal does not read from a
	// random source.
	return crypto.Seal(a, nil, dek, nil)
}

// Unwrap decrypts a wrapped data key.
//
// Material corrupted in any position, truncated, or wrapped under a
// different root key fails authentication and returns an error, never
// wrong key material. Returns [crypto.ErrKeyDestroyed] once
// [Keeper.Destroy] has run. That error is distinct from a corruption
// failure, because the two have different remedies.
func (k *Keeper) Unwrap(_ context.Context, wrapped []byte) ([]byte, error) {
	a, err := k.cipher()
	if err != nil {
		return nil, err
	}

	return crypto.Open(a, wrapped, nil)
}

// GenerateKey generates a fresh data key of size bytes and returns it
// both in the clear and wrapped. The data key is read from the source
// given to [New].
//
// A non-positive size returns [crypto.ErrKeySize]. Callers that only
// encrypt should zero plaintext once the payload is sealed.
func (k *Keeper) GenerateKey(ctx context.Context, size int) (plaintext, wrapped []byte, err error) {
	if size <= 0 {
		return nil, nil, crypto.ErrKeySize
	}

	// Reading before the destroyed check is harmless: the key material
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

// Destroy destroys the root key at once and returns the time of the
// call, read from the clock given to [New]. No material wrapped under
// the key can be recovered by this Keeper afterwards.
//
// keyID must name this Keeper's key. Any other value returns
// [crypto.ErrKeyID], because silent success would leave a caller
// believing data was erased when it was not. Destroying the key again
// returns the time of the first call, so a caller that retries learns
// the same time.
//
// The guarantee here is only as strong as process memory: this drops
// the cipher, but Go cannot guarantee that no copy of the key schedule
// survives elsewhere in the heap. A real custodian destroys the key
// inside its own boundary.
func (k *Keeper) Destroy(_ context.Context, keyID string) (time.Time, error) {
	if subtle.ConstantTimeCompare([]byte(keyID), []byte(k.keyID)) != 1 {
		return time.Time{}, crypto.ErrKeyID
	}

	k.mu.Lock()
	defer k.mu.Unlock()

	if k.aead != nil {
		k.aead = nil
		k.destroyedAt = k.clock.Time()
	}

	return k.destroyedAt, nil
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
