// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package localkey provides an in-process [crypto.Keeper] for tests
// and local development.
//
// # This is not custody
//
// The root keys are stored in this process's memory. They can be read
// from a core dump, from /proc, by a debugger, and by any code in the
// same address space. Nothing here is a hardware module or a hosted key
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
//
// # Key creation
//
// [Keeper.CreateKey] reads a root key from the source passed to [New] and
// names it keyID/n, where keyID is the ID passed to New and n counts from
// 1. A Keeper from New and every Keeper that CreateKey or [Keeper.OpenKey]
// returns share one key table. The table is the scope of OpenKey, and a
// key destroyed through any of these Keepers is destroyed for all. The
// table is in process memory, and a restart discards it.
package localkey

import (
	"context"
	"strconv"
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

// keySeparator separates the key ID passed to [New] from the number of a
// created key.
const keySeparator = "/"

// Keeper is an in-process [crypto.Keeper] over one root key of a shared
// key table. It also implements [crypto.Destroyer], [crypto.KeyGenerator],
// [crypto.AADKeeper] and [crypto.KeyCreator].
//
// # Concurrency
//
// Safe for concurrent use. Destroy races against in-flight Wrap and
// Unwrap calls without either observing a half-destroyed key.
type Keeper struct {
	// ring is the key table that this Keeper shares.
	ring *ring

	// key is this Keeper's root key in ring.
	key *key

	keyID string
}

// Compile-time proof that Keeper satisfies the seam and its
// capabilities. A caller type-asserts for these at wiring time, so a
// method dropped by a refactor fails the build and not the assertion.
var (
	_ crypto.Keeper       = (*Keeper)(nil)
	_ crypto.Destroyer    = (*Keeper)(nil)
	_ crypto.KeyGenerator = (*Keeper)(nil)
	_ crypto.AADKeeper    = (*Keeper)(nil)
	_ crypto.KeyCreator   = (*Keeper)(nil)
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
// 140-only mode. r supplies the data keys of [Keeper.GenerateKey] and the
// root keys of [Keeper.CreateKey], and must be a cryptographically secure
// source. c supplies the time that [Keeper.Destroy] returns.
func New(keyID string, rootKey []byte, r rand.Rand, c clock.Clock) (*Keeper, error) {
	if keyID == "" {
		return nil, crypto.ErrKeyID
	}

	a, err := cipherFor(rootKey)
	if err != nil {
		return nil, err
	}

	k := &key{aead: a}
	table := &ring{rand: r, clock: c, keys: map[string]*key{keyID: k}, base: keyID}

	return &Keeper{ring: table, key: k, keyID: keyID}, nil
}

// KeyID returns the key ID: the one passed to [New], or the one that
// [Keeper.CreateKey] assigned.
func (k *Keeper) KeyID() string { return k.keyID }

// Wrap encrypts dek under the root key. It is [Keeper.WrapAAD] with
// empty aad.
func (k *Keeper) Wrap(ctx context.Context, dek []byte) ([]byte, error) {
	return k.WrapAAD(ctx, dek, nil)
}

// WrapAAD encrypts dek under the root key and binds aad to the result,
// as the envelope's associated data.
//
// The standard library's FIPS 140-3 module generates a fresh 96-bit
// nonce for each call, so wrapping one data key twice produces
// different bytes. Random nonces are safe for at most 2^32 wraps under
// one root key. Returns [crypto.ErrKeyDestroyed] after the key is
// destroyed.
func (k *Keeper) WrapAAD(_ context.Context, dek, aad []byte) ([]byte, error) {
	a, err := k.cipher()
	if err != nil {
		return nil, err
	}

	// The cipher generates its own nonce, so Seal does not read from a
	// random source.
	return crypto.Seal(a, nil, dek, aad)
}

// Unwrap decrypts a wrapped data key. It is [Keeper.UnwrapAAD] with
// empty aad.
func (k *Keeper) Unwrap(ctx context.Context, wrapped []byte) ([]byte, error) {
	return k.UnwrapAAD(ctx, wrapped, nil)
}

// UnwrapAAD decrypts a data key that [Keeper.WrapAAD] wrapped with the
// same aad.
//
// Material wrapped with other aad, corrupted in any position,
// truncated, or wrapped under a different root key fails authentication
// and returns an error, never wrong key material. Returns
// [crypto.ErrKeyDestroyed] after the key is destroyed. That error is
// distinct from a corruption failure, because the two have different
// remedies.
func (k *Keeper) UnwrapAAD(_ context.Context, wrapped, aad []byte) ([]byte, error) {
	a, err := k.cipher()
	if err != nil {
		return nil, err
	}

	return crypto.Open(a, wrapped, aad)
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
	if _, err = k.ring.rand.Read(dek); err != nil {
		return nil, nil, err
	}

	wrapped, err = k.Wrap(ctx, dek)
	if err != nil {
		return nil, nil, err
	}

	return dek, wrapped, nil
}

// Destroy destroys the root key that keyID names and returns the time of
// the first call, read from the clock passed to [New]. Destruction is
// immediate and applies to every Keeper of the key table.
//
// keyID must name the key passed to New or a key that [Keeper.CreateKey]
// created. Any other key ID returns [crypto.ErrKeyID], because a silent
// success would report an erasure that did not happen. Repeated calls
// return the time of the first call.
//
// The guarantee is only as strong as process memory. Destroy drops the
// cipher, but Go cannot guarantee that no copy of the key schedule
// remains in the heap. A real custodian destroys the key inside its own
// boundary.
func (k *Keeper) Destroy(_ context.Context, keyID string) (time.Time, error) {
	found, ok := k.ring.lookup(keyID)
	if !ok {
		return time.Time{}, crypto.ErrKeyID
	}

	return k.ring.destroy(found), nil
}

// CreateKey reads a root key from the source passed to [New] and returns
// its Keeper. The key ID is keyID/n, where keyID is the ID passed to New
// and n is the number of keys created so far, so the first key created
// from "local/root" is "local/root/1". The new Keeper shares the key
// table, so it has every capability of this Keeper and the same scope.
//
// A failure of the random source returns its error, and CreateKey does
// not create a key.
func (k *Keeper) CreateKey(_ context.Context) (crypto.Keeper, error) {
	a, err := k.ring.newCipher()
	if err != nil {
		return nil, err
	}

	return k.ring.add(a), nil
}

// OpenKey returns the Keeper of the key that keyID names in the key
// table: the key passed to [New], or one that [Keeper.CreateKey] created.
// Any other key ID returns [crypto.ErrKeyID]. The Keeper of a destroyed
// key fails Wrap and Unwrap with [crypto.ErrKeyDestroyed].
func (k *Keeper) OpenKey(_ context.Context, keyID string) (crypto.Keeper, error) {
	found, ok := k.ring.lookup(keyID)
	if !ok {
		return nil, crypto.ErrKeyID
	}

	return &Keeper{ring: k.ring, key: found, keyID: keyID}, nil
}

// cipher returns the wrapping cipher, or [crypto.ErrKeyDestroyed] after
// the key is destroyed.
func (k *Keeper) cipher() (crypto.AEAD, error) {
	k.ring.mu.RLock()
	defer k.ring.mu.RUnlock()

	if k.key.aead == nil {
		return nil, crypto.ErrKeyDestroyed
	}

	return k.key.aead, nil
}

// ring is the key table that a Keeper from [New] shares with every Keeper
// it creates or opens.
type ring struct {
	rand  rand.Rand
	clock clock.Clock

	// keys maps a key ID to its root key.
	keys map[string]*key

	// base is the key ID passed to New. It prefixes every created key ID.
	base string

	// created counts the keys that CreateKey has created.
	created uint64

	// mu guards keys, created, and the aead and destroyedAt fields of
	// every key.
	mu sync.RWMutex
}

// newCipher reads a root key from the random source, returns the cipher
// over it, and zeroes the root key.
func (r *ring) newCipher() (crypto.AEAD, error) {
	rootKey := make([]byte, RootKeySize)
	defer clear(rootKey)

	if _, err := r.rand.Read(rootKey); err != nil {
		return nil, err
	}

	return cipherFor(rootKey)
}

// add registers a created key with cipher a and returns its Keeper.
func (r *ring) add(a crypto.AEAD) *Keeper {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.created++
	keyID := r.base + keySeparator + strconv.FormatUint(r.created, 10)
	k := &key{aead: a}
	r.keys[keyID] = k

	return &Keeper{ring: r, key: k, keyID: keyID}
}

// lookup returns the key that keyID names and whether the table has it.
func (r *ring) lookup(keyID string) (*key, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	k, ok := r.keys[keyID]

	return k, ok
}

// destroy clears the cipher of k on the first call and returns the time
// of that call.
func (r *ring) destroy(k *key) time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()

	if k.aead != nil {
		k.aead = nil
		k.destroyedAt = r.clock.Time()
	}

	return k.destroyedAt
}

// key is one root key of a ring.
type key struct {
	// aead is the wrapping cipher, or nil after destruction. Wrap and
	// Unwrap read it under the ring's lock and use it afterwards, which
	// is safe because crypto.AEAD supports concurrent use.
	aead crypto.AEAD

	// destroyedAt is the time of the first Destroy.
	destroyedAt time.Time
}

// cipherFor returns the wrapping cipher over rootKey, or
// [crypto.ErrKeySize] for a root key that is not [RootKeySize] bytes.
// aesgcm accepts a 16-byte key as well, so the length is checked here
// first.
func cipherFor(rootKey []byte) (crypto.AEAD, error) {
	if len(rootKey) != RootKeySize {
		return nil, crypto.ErrKeySize
	}

	return aesgcm.NewRandomNonce(rootKey)
}
