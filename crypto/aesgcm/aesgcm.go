// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package aesgcm

import (
	"crypto/aes"
	"crypto/cipher"

	"go.thesmos.sh/core/crypto"
)

// Key lengths [New] accepts, selecting between the two constructions.
const (
	// KeySize128 selects AES-128-GCM.
	KeySize128 = 16
	// KeySize256 selects AES-256-GCM.
	KeySize256 = 32
)

// id128 and id256 are the build-local implementation identifiers.
// The bytes spell out the construction left-aligned with zero
// padding to [crypto.IDSize]; the compiler rejects a literal too
// long to fit, so no runtime length check is needed. They differ per
// construction so a receipt naming one cannot be satisfied by the
// other.
var (
	id128 = crypto.ID{'a', 'e', 's', '-', '1', '2', '8', '-', 'g', 'c', 'm', '/', 'v', '1'}
	id256 = crypto.ID{'a', 'e', 's', '-', '2', '5', '6', '-', 'g', 'c', 'm', '/', 'v', '1'}
)

// aead is the AES-GCM [crypto.AEAD]. It embeds the standard library's
// [cipher.AEAD], so every method of that contract is inherited
// verbatim and this type adds only identity.
// Field order groups the pointer-bearing fields — the embedded
// interface and the string — ahead of the pointer-free array, which
// narrows the range the garbage collector scans.
type aead struct {
	cipher.AEAD

	algorithm crypto.Algorithm
	id        crypto.ID
}

// New returns an AES-GCM [crypto.AEAD] over key.
//
// len(key) selects the construction: [KeySize128] gives
// AES-128-GCM, [KeySize256] gives AES-256-GCM. Any other length
// returns [crypto.ErrKeySize] — AES-192 is deliberately unsupported,
// as it is in most modern protocol profiles.
//
// key is copied into the cipher's key schedule at construction, so
// the caller may zero its own copy immediately afterwards.
//
// # Allocation contract
//
// Allocates the block cipher and its GCM state once, at
// construction. The returned value is safe for concurrent use and
// should be built once and shared, not per message.
func New(key []byte) (crypto.AEAD, error) {
	// aes.NewCipher validates first, so its error is a live path
	// rather than dead defensive code: every length AES rejects
	// arrives here.
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, crypto.ErrKeySize
	}

	// AES-192 reaches this switch — aes.NewCipher accepts a 24-byte
	// key — and is rejected here, which is what narrows this package
	// to the two sizes modern protocol profiles use.
	var (
		id        crypto.ID
		algorithm crypto.Algorithm
	)

	switch len(key) {
	case KeySize128:
		id, algorithm = id128, crypto.AlgAES128GCM
	case KeySize256:
		id, algorithm = id256, crypto.AlgAES256GCM
	default:
		return nil, crypto.ErrKeySize
	}

	return newGCM(block, id, algorithm)
}

// newGCM wraps block in GCM and attaches identity.
//
// Split out from [New] so the cipher.NewGCM failure path is
// reachable from a test: it fails only for a block size other than
// 128 bits, which AES never produces, so the only way to exercise it
// is to hand it a different cipher.
func newGCM(block cipher.Block, id crypto.ID, algorithm crypto.Algorithm) (crypto.AEAD, error) {
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, crypto.ErrKeySize
	}

	return aead{AEAD: gcm, id: id, algorithm: algorithm}, nil
}

// ID returns the build-local implementation identifier, distinct per
// key size.
func (a aead) ID() crypto.ID { return a.id }

// Algorithm returns the long-term, cross-build name — either
// [crypto.AlgAES128GCM] or [crypto.AlgAES256GCM], per the key length
// given to [New]. Persist it alongside every ciphertext.
func (a aead) Algorithm() crypto.Algorithm { return a.algorithm }
