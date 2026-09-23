// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package aesgcm

import (
	"crypto/aes"
	"crypto/cipher"
	"fmt"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/errs"
)

// Key lengths [New] and [NewRandomNonce] accept, selecting between the
// two constructions.
const (
	// KeySize128 selects AES-128-GCM.
	KeySize128 = 16
	// KeySize256 selects AES-256-GCM.
	KeySize256 = 32
)

// id128 and id256 are the build-local implementation identifiers of
// [New], and rid128 and rid256 those of [NewRandomNonce]. The bytes
// spell out the construction left-aligned with zero padding to
// [crypto.IDSize]; the compiler rejects a literal too long to fit, so
// no runtime length check is needed. They differ per construction so a
// receipt naming one cannot be satisfied by another.
var (
	id128  = crypto.ID{'a', 'e', 's', '-', '1', '2', '8', '-', 'g', 'c', 'm', '/', 'v', '1'}
	id256  = crypto.ID{'a', 'e', 's', '-', '2', '5', '6', '-', 'g', 'c', 'm', '/', 'v', '1'}
	rid128 = crypto.ID{'a', 'e', 's', '-', '1', '2', '8', '-', 'g', 'c', 'm', '-', 'r', '/', 'v', '1'}
	rid256 = crypto.ID{'a', 'e', 's', '-', '2', '5', '6', '-', 'g', 'c', 'm', '-', 'r', '/', 'v', '1'}
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

// New returns an AES-GCM [crypto.AEAD] over key whose nonces the
// caller supplies: [crypto.Seal] reads each one from its random
// source.
//
// len(key) selects the construction: [KeySize128] gives
// AES-128-GCM, [KeySize256] gives AES-256-GCM. Any other length
// returns [crypto.ErrKeySize] — AES-192 is deliberately unsupported,
// as it is in most modern protocol profiles.
//
// In FIPS 140-only mode the standard library refuses GCM with
// caller-supplied nonces, and New returns an error classified
// [errs.Unsupported]. Use [NewRandomNonce] there.
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
	return build(key, cipher.NewGCM, id128, id256)
}

// NewRandomNonce returns an AES-GCM [crypto.AEAD] over key that
// generates every nonce itself, inside the standard library's FIPS
// 140-3 module. It is the only AES-GCM construction the standard
// library accepts in FIPS 140-only mode.
//
// len(key) selects the construction as it does for [New], and the
// Algorithm is the same. The two open each other's envelopes: the
// nonce this AEAD prepends to its ciphertext is at the offset where
// [crypto.Seal] writes the nonce it reads for an AEAD from [New].
//
// NonceSize is 0 and Overhead is 28, the 12-byte nonce plus the
// 16-byte tag. [crypto.Seal] and [crypto.AppendSeal] read no entropy
// from their random source for this AEAD, so AppendSeal does not
// allocate.
//
// The nonce is 96 random bits per message. Seal at most 2^32 messages
// under one key, the bound the standard library states for this
// construction.
//
// # Allocation contract
//
// Allocates the block cipher and its GCM state once, at
// construction. The returned value is safe for concurrent use and
// should be built once and shared, not per message.
func NewRandomNonce(key []byte) (crypto.AEAD, error) {
	return build(key, cipher.NewGCMWithRandomNonce, rid128, rid256)
}

// build validates key, selects the construction from its length, and
// wraps the AES block in GCM with mode. small and large are the
// identifiers for the 128- and 256-bit constructions.
func build(key []byte, mode func(cipher.Block) (cipher.AEAD, error), small, large crypto.ID) (crypto.AEAD, error) {
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
		id, algorithm = small, crypto.AlgAES128GCM
	case KeySize256:
		id, algorithm = large, crypto.AlgAES256GCM
	default:
		return nil, crypto.ErrKeySize
	}

	return newGCM(block, mode, id, algorithm)
}

// newGCM wraps block in GCM with mode and attaches identity.
//
// mode fails in FIPS 140-only mode for caller-supplied nonces, and for
// any block size other than 128 bits, which AES never produces. The
// error it returns names the cause, so it is wrapped rather than
// replaced. newGCM is split out so a test can exercise the failure
// without FIPS mode by handing mode a cipher with a different block
// size.
func newGCM(
	block cipher.Block, mode func(cipher.Block) (cipher.AEAD, error),
	id crypto.ID, algorithm crypto.Algorithm,
) (crypto.AEAD, error) {
	gcm, err := mode(block)
	if err != nil {
		return nil, errs.WithClass(fmt.Errorf("aesgcm: %w", err), errs.Unsupported)
	}

	return aead{AEAD: gcm, id: id, algorithm: algorithm}, nil
}

// ID returns the build-local implementation identifier, distinct per
// constructor and key size.
func (a aead) ID() crypto.ID { return a.id }

// Algorithm returns the long-term, cross-build name — either
// [crypto.AlgAES128GCM] or [crypto.AlgAES256GCM], per the key length
// given to [New] or [NewRandomNonce]. Persist it alongside every
// ciphertext.
func (a aead) Algorithm() crypto.Algorithm { return a.algorithm }
