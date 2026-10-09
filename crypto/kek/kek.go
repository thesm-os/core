// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package kek

import (
	"bytes"
	"context"
	"crypto/hkdf"
	"crypto/sha256"
	"crypto/subtle"
	"hash"
	"io"
	"runtime"
	"sync"
	"sync/atomic"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/aesgcm"
	"go.thesmos.sh/core/rand"
)

// Sizes of a KEK, of the record that the parent wraps, and of the salt
// at the start of a wrapped DEK.
const (
	// KeySize is the length of a KEK.
	KeySize = 32

	// RecordSize is the length of the record that the parent wraps: the
	// KEK, then its binding to the key ID.
	RecordSize = 64

	// SaltSize is the length of the random salt at the start of every
	// wrapped DEK.
	SaltSize = 16
)

// DomainName separates the derivations, the bindings and the associated
// data of this package from every other derivation and framed sequence.
const DomainName = "thesmos.crypto.kek"

const (
	// domainVersion is the version of the framed key ID.
	domainVersion = 1

	// wrapsPerKey is the number of DEKs a wrapping key seals before the
	// Keeper derives the next one: 2^30.
	wrapsPerKey = 1073741824

	// cipherCacheSize is the number of derived ciphers that Unwrap keeps.
	cipherCacheSize = 1024
)

// kdf derives a key of keyLength bytes from secret, salt and info, as
// [hkdf.Key] does.
type kdf func(h func() hash.Hash, secret, salt []byte, info string, keyLength int) ([]byte, error)

// wrappingKey is one derived wrapping key: its cipher, the number of
// wraps reserved on it, and its salt.
type wrappingKey struct {
	aead  crypto.AEAD
	wraps atomic.Uint64
	salt  [SaltSize]byte
}

// Keeper is a [crypto.Keeper] over an intermediate KEK that a parent
// Keeper protects at rest. It is also an [io.Closer].
//
// A cleanup zeroes the KEK of a Keeper that becomes unreachable, which
// matters for a Keeper that was never closed. The runtime does not
// guarantee that the cleanup runs, in particular before the program
// exits.
//
// # Concurrency
//
// Safe for concurrent use. Close waits for the Wrap and Unwrap calls in
// progress.
type Keeper struct {
	// r is the source of every salt.
	r rand.Rand

	// derive is hkdf.Key. A test replaces it to make a derivation fail,
	// which hkdf.Key never does with the parameters of this package, and
	// to read the key that a derivation returns.
	derive kdf

	// ciphers maps a salt to the cipher derived for it, for Unwrap. It is
	// guarded by ciphersMu.
	ciphers map[[SaltSize]byte]crypto.AEAD

	// current is the wrapping key that Wrap seals under. It is nil until
	// the first Wrap.
	current atomic.Pointer[wrappingKey]

	keyID string

	// info and aad are the framed key ID, as HKDF info and as the
	// associated data of every wrapped DEK.
	info string
	aad  []byte

	kek []byte

	// limit is the number of DEKs one wrapping key seals, 2^30. A test
	// lowers it to observe a rotation.
	limit uint64

	// mu guards closed. Close takes it for writing, and Wrap and Unwrap
	// take it for reading, so Close waits for the calls in progress.
	mu sync.RWMutex

	// rotateMu serialises the derivation of the next wrapping key.
	rotateMu sync.Mutex

	// ciphersMu guards ciphers.
	ciphersMu sync.Mutex

	closed bool
}

// Compile-time proof that Keeper is a crypto.Keeper and an io.Closer.
var (
	_ crypto.Keeper = (*Keeper)(nil)
	_ io.Closer     = (*Keeper)(nil)
)

// Generate creates a KEK for keyID. It returns the KEK's Keeper and the
// wrapped record, which the caller persists with keyID and
// parent.KeyID().
//
// Generate reads the KEK from r and wraps the record with parent.Wrap.
// The Keeper reads the salt of every wrapping key from r. Outside tests
// r must be a cryptographic source, because two keepers that read the
// same salt share one wrapping key.
//
// Returns [crypto.ErrKeyID] for an empty keyID, the error of r, and the
// parent's error. No Keeper and no key material accompany an error.
func Generate(ctx context.Context, parent crypto.Keeper, r rand.Rand, keyID string) (*Keeper, []byte, error) {
	return generate(r, keyID, func(record []byte) ([]byte, error) {
		return parent.Wrap(ctx, record)
	})
}

// GenerateAAD is [Generate] for a parent that binds associated data. It
// wraps the record with keyID as the associated data.
//
// A record from GenerateAAD opens only with [NewAAD], and a record from
// Generate opens only with [New].
func GenerateAAD(ctx context.Context, parent crypto.AADKeeper, r rand.Rand, keyID string) (*Keeper, []byte, error) {
	return generate(r, keyID, func(record []byte) ([]byte, error) {
		return parent.WrapAAD(ctx, record, []byte(keyID))
	})
}

// New unwraps a record from [Generate] with parent.Unwrap and returns the
// Keeper of its KEK. The Keeper reads the salt of every wrapping key
// from r, under the rule that Generate states.
//
// Returns [crypto.ErrKeyID] for an empty keyID, the parent's error when
// the unwrap fails, [crypto.ErrKeySize] when the record is not
// [RecordSize] bytes long, and [ErrKeyIDMismatch] when the record is
// bound to another keyID.
func New(ctx context.Context, parent crypto.Keeper, r rand.Rand, keyID string, wrapped []byte) (*Keeper, error) {
	if keyID == "" {
		return nil, crypto.ErrKeyID
	}

	record, err := parent.Unwrap(ctx, wrapped)
	if err != nil {
		return nil, err
	}

	return open(record, r, keyID)
}

// NewAAD is [New] for a record from [GenerateAAD]. It unwraps the record
// with keyID as the associated data, and returns the errors of New.
func NewAAD(
	ctx context.Context, parent crypto.AADKeeper, r rand.Rand, keyID string, wrapped []byte,
) (*Keeper, error) {
	if keyID == "" {
		return nil, crypto.ErrKeyID
	}

	record, err := parent.UnwrapAAD(ctx, wrapped, []byte(keyID))
	if err != nil {
		return nil, err
	}

	return open(record, r, keyID)
}

// KeyID returns the KEK's key ID, given to the constructor.
func (k *Keeper) KeyID() string { return k.keyID }

// Wrap seals dek under the keeper's wrapping key. The keeper derives a
// wrapping key from the KEK and a salt read from r at its first Wrap,
// and derives a new one after every 2^30 wraps. ctx is unused, because
// Wrap does not call anything outside the process.
//
// Returns the error of r when a derivation reads a salt, and [ErrClosed]
// after [Keeper.Close].
//
// # Allocation contract
//
// One allocation for the returned slice, except in a call that derives a
// wrapping key.
func (k *Keeper) Wrap(_ context.Context, dek []byte) ([]byte, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()

	if k.closed {
		return nil, ErrClosed
	}

	w, err := k.reserve()
	if err != nil {
		return nil, err
	}

	out := make([]byte, SaltSize, SaltSize+crypto.SealedSize(w.aead, len(dek)))
	copy(out, w.salt[:])

	// The cipher generates its own nonce, so AppendSeal does not read
	// from a random source.
	return crypto.AppendSeal(out, w.aead, nil, dek, k.aad)
}

// Unwrap derives the wrapping key from the salt in wrapped, or reuses the
// cipher it derived for that salt, and opens the DEK. Material corrupted
// in any position, truncated, or wrapped under another KEK or another
// keyID fails.
//
// Returns [crypto.ErrCiphertextShort] for material with no envelope after
// its salt, before it derives anything, the errors of [crypto.Open], and
// [ErrClosed] after [Keeper.Close].
func (k *Keeper) Unwrap(_ context.Context, wrapped []byte) ([]byte, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()

	if k.closed {
		return nil, ErrClosed
	}

	if len(wrapped) <= SaltSize {
		return nil, crypto.ErrCiphertextShort
	}

	a, err := k.cipher(wrapped[:SaltSize])
	if err != nil {
		return nil, err
	}

	return crypto.Open(a, wrapped[SaltSize:], k.aad)
}

// Close zeroes the KEK and drops the derived wrapping keys once the Wrap
// and Unwrap calls in progress have returned. Later calls return
// [ErrClosed]. Close is idempotent and returns nil: a second call zeroes
// and drops what the first left empty.
//
// Go's AES implementation keeps each cipher's key schedule in memory that
// Close cannot zero. The schedules remain until the collector frees them
// and the allocator reuses the memory.
func (k *Keeper) Close() error {
	k.mu.Lock()
	defer k.mu.Unlock()

	k.closed = true
	clear(k.kek)
	k.current.Store(nil)

	k.ciphersMu.Lock()
	clear(k.ciphers)
	k.ciphersMu.Unlock()

	return nil
}

// generate reads a KEK from r, builds its record for keyID and wraps the
// record with wrap. It zeroes the record before it returns.
func generate(r rand.Rand, keyID string, wrap func(record []byte) ([]byte, error)) (*Keeper, []byte, error) {
	if keyID == "" {
		return nil, nil, crypto.ErrKeyID
	}

	info := frameKeyID(keyID)

	record := make([]byte, RecordSize)
	defer clear(record)

	if _, err := r.Read(record[:KeySize]); err != nil {
		return nil, nil, err
	}

	binding := sha256.Sum256(info)
	copy(record[KeySize:], binding[:])

	wrapped, err := wrap(record)
	if err != nil {
		return nil, nil, err
	}

	return newKeeper(bytes.Clone(record[:KeySize]), keyID, info, r), wrapped, nil
}

// open returns the Keeper of the KEK in record when the record is bound
// to keyID. It zeroes record before it returns.
func open(record []byte, r rand.Rand, keyID string) (*Keeper, error) {
	defer clear(record)

	if len(record) != RecordSize {
		return nil, crypto.ErrKeySize
	}

	info := frameKeyID(keyID)
	binding := sha256.Sum256(info)

	if subtle.ConstantTimeCompare(record[KeySize:], binding[:]) != 1 {
		return nil, ErrKeyIDMismatch
	}

	return newKeeper(bytes.Clone(record[:KeySize]), keyID, info, r), nil
}

// frameKeyID returns keyID framed under [DomainName]. It is the HKDF info
// of every wrapping key, the associated data of every wrapped DEK, and
// the input of the record's binding.
func frameKeyID(keyID string) []byte {
	f := crypto.NewFramer(nil, crypto.Domain{Name: DomainName, Version: domainVersion})
	f.String(keyID)

	return f.Frame()
}

// newKeeper returns the Keeper of kek, which it takes over, and attaches
// the cleanup that zeroes kek.
func newKeeper(kek []byte, keyID string, info []byte, r rand.Rand) *Keeper {
	k := &Keeper{
		r:       r,
		derive:  hkdf.Key[hash.Hash],
		ciphers: map[[SaltSize]byte]crypto.AEAD{},
		keyID:   keyID,
		info:    string(info),
		aad:     info,
		kek:     kek,
		limit:   wrapsPerKey,
	}
	// The cleanup also runs for a closed Keeper, and zeroes a KEK that
	// Close already zeroed.
	runtime.AddCleanup(k, func(b []byte) { clear(b) }, kek)

	return k
}

// reserve returns a wrapping key with one wrap reserved on it: the current
// key when it has room, or the key that rotate returns. A key has room
// until it has k.limit reservations, so no wrapping key seals more than
// k.limit DEKs.
func (k *Keeper) reserve() (*wrappingKey, error) {
	//dokimi:mutate-skip sbr-delete,lcr-false,ror-false: rotate repeats this check under rotateMu, so the fast path changes only the cost of a Wrap
	if w := k.current.Load(); w != nil && w.wraps.Add(1) <= k.limit {
		return w, nil
	}

	return k.rotate()
}

// rotate returns a wrapping key with one wrap reserved on it. Under
// rotateMu, it reserves the wrap on the current key when another call has
// already replaced the spent key and the replacement has room. Otherwise
// it derives the next wrapping key from a new salt, with the caller's wrap
// reserved on it. A call to rotate derives at most one key.
func (k *Keeper) rotate() (*wrappingKey, error) {
	k.rotateMu.Lock()
	defer k.rotateMu.Unlock()

	if w := k.current.Load(); w != nil && w.wraps.Add(1) <= k.limit {
		return w, nil
	}

	var salt [SaltSize]byte
	if _, err := k.r.Read(salt[:]); err != nil {
		return nil, err
	}

	a, err := k.newCipher(salt[:])
	if err != nil {
		return nil, err
	}

	w := &wrappingKey{aead: a, salt: salt}
	w.wraps.Store(1)
	k.current.Store(w)

	return w, nil
}

// cipher returns the cipher of the wrapping key for salt, the SaltSize
// bytes at the start of a wrapped DEK: the current key's, a cached one,
// or a new derivation, which it caches. It empties the cache when a new
// cipher would exceed cipherCacheSize.
func (k *Keeper) cipher(salt []byte) (crypto.AEAD, error) {
	id := [SaltSize]byte(salt)
	if w := k.current.Load(); w != nil && w.salt == id {
		return w.aead, nil
	}

	k.ciphersMu.Lock()
	a, ok := k.ciphers[id]
	k.ciphersMu.Unlock()

	if ok {
		return a, nil
	}

	a, err := k.newCipher(salt)
	if err != nil {
		return nil, err
	}

	k.ciphersMu.Lock()
	if len(k.ciphers) >= cipherCacheSize {
		clear(k.ciphers)
	}
	k.ciphers[id] = a
	k.ciphersMu.Unlock()

	return a, nil
}

// newCipher derives the wrapping key for salt with HKDF-SHA-256 over the
// KEK, with the framed key ID as info, and returns its AES-256-GCM
// cipher. It zeroes the derived key before it returns. salt is a slice
// of the caller's buffer, so the derivation copies no salt to the heap.
//
// One derivation allocates 19 times, all in the standard library: 3 in
// aesgcm.NewRandomNonce, for the block, the GCM state and the AEAD, and
// the rest in hkdf.Key, for its HMAC and SHA-256 states, their sums and
// the key.
func (k *Keeper) newCipher(salt []byte) (crypto.AEAD, error) {
	key, err := k.derive(sha256.New, k.kek, salt, k.info, aesgcm.KeySize256)
	defer clear(key)

	if err != nil {
		return nil, err
	}

	return aesgcm.NewRandomNonce(key)
}
