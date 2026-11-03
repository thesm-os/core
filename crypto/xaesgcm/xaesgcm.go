// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package xaesgcm

import (
	"crypto/aes"
	"crypto/cipher"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/pool"
)

// The sizes of the key and of the nonce of the AEAD.
const (
	// KeySize is the length of an XAES-256-GCM key.
	KeySize = 32

	// NonceSize is the length of the nonce that [crypto.Seal] reads for the
	// AEAD: the first 96 bits of the 192-bit nonce of XAES-256-GCM.
	NonceSize = 12
)

// The constants of the key derivation and of the output of Seal.
const (
	// label is the label of the counter-mode KDF of the specification, the
	// ASCII letter X.
	label = 0x58

	// rb is the constant of NIST SP 800-38B that the CMAC subkey K1 adds
	// into its last byte when the first bit of L is set.
	rb = 0x87

	// overhead is the length of the second half of the nonce and of the
	// tag, which Seal writes beside the ciphertext.
	overhead = 12 + 16
)

// id is the build-local identifier of the AEAD of [New].
var id = crypto.ID{'x', 'a', 'e', 's', '-', '2', '5', '6', '-', 'g', 'c', 'm', '/', 'v', '1'}

// scratches pools the scratch of a derivation. A stack array that a call of
// [cipher.Block.Encrypt] receives through its interface moves to the heap, so
// a scratch of the pool keeps the derivation from allocating.
var scratches = pool.NewResetPool(func() *scratch { return new(scratch) })

// scratch is the memory of one derivation: the block of the CMAC input and
// the derived key Kx.
//
// # Concurrency
//
// One call of Seal or Open uses a scratch at a time.
//
// # Allocation contract
//
// Reset does not allocate.
type scratch struct {
	block [aes.BlockSize]byte
	key   [KeySize]byte
}

// Reset clears the block and the derived key, so the pool does not keep a byte
// of a derivation.
func (s *scratch) Reset() {
	*s = scratch{}
}

// aead is the XAES-256-GCM [crypto.AEAD] of [New]. block is AES-256 under the
// key, and k1 is the CMAC subkey K1 of that key.
//
// # Concurrency
//
// Safe for concurrent use. New sets both fields and nothing changes them
// after it, and AES-256 under one key encrypts concurrent blocks.
//
// # Allocation contract
//
// Seal and Open allocate the AES-256 block and the GCM state of the derived
// key, 2 objects, besides the growth of dst.
type aead struct {
	block cipher.Block
	k1    [aes.BlockSize]byte
}

// Compile-time interface check.
var _ crypto.AEAD = (*aead)(nil)

// New returns an XAES-256-GCM [crypto.AEAD] over key. Its NonceSize is
// [NonceSize], the first half of the 192-bit nonce, which [crypto.Seal] reads
// from its random source. Its Overhead is 28: the second half of the nonce,
// which the FIPS 140-3 module of the standard library generates, and the
// 16-byte tag.
//
// New copies key into an AES-256 block and computes the CMAC subkey K1 of the
// specification, so the caller can zero key after New returns.
//
// Error modes: a key that is not [KeySize] bytes long returns
// [crypto.ErrKeySize].
//
// # Allocation contract
//
// Allocates the AES-256 block of key and the AEAD, once. Build the AEAD once
// per key and share it, not per message.
func New(key []byte) (crypto.AEAD, error) {
	if len(key) != KeySize {
		return nil, crypto.ErrKeySize
	}

	// aes.NewCipher refuses only a key that is not 16, 24 or 32 bytes long,
	// and the check above has refused every such key.
	block, _ := aes.NewCipher(key)

	// L is AES-256 of a zero block. K1 is L shifted left by one bit, with rb
	// added into the last byte when the first bit of L is set. The product
	// of that bit and rb keeps the computation free of a branch on the key.
	a := &aead{block: block}
	block.Encrypt(a.k1[:], a.k1[:])
	first := a.k1[0] >> 7
	for i := range aes.BlockSize - 1 {
		a.k1[i] = a.k1[i]<<1 | a.k1[i+1]>>7
	}
	a.k1[aes.BlockSize-1] = a.k1[aes.BlockSize-1]<<1 ^ first*rb

	return a, nil
}

// NonceSize returns [NonceSize], the length of the first half of the nonce.
func (*aead) NonceSize() int { return NonceSize }

// Overhead returns 28, the length of the second half of the nonce and of the
// tag.
func (*aead) Overhead() int { return overhead }

// Seal derives the key of nonce, seals plaintext under it with
// additionalData, and appends the second half of the nonce, the ciphertext
// and the tag to dst. The FIPS 140-3 module of the standard library
// generates the second half of the nonce.
//
// Seal panics when nonce is not [NonceSize] bytes long, as [cipher.AEAD]
// documents.
func (a *aead) Seal(dst, nonce, plaintext, additionalData []byte) []byte {
	//nolint:gosec // G407: the GCM of NewGCMWithRandomNonce takes no nonce and generates one for each Seal
	return a.subkey(nonce).Seal(dst, nil, plaintext, additionalData)
}

// Open derives the key of nonce, opens the second half of the nonce, the
// ciphertext and the tag in ciphertext under it with additionalData, and
// appends the plaintext to dst.
//
// Error modes: a ciphertext shorter than 28 bytes, and a ciphertext, a tag,
// a nonce or associated data that differs from what Seal sealed, return the
// error of AES-GCM, unwrapped, so a caller cannot tell the causes apart.
// Open panics when nonce is not [NonceSize] bytes long, as [cipher.AEAD]
// documents.
func (a *aead) Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error) {
	//nolint:wrapcheck // authentication failures must remain indistinguishable
	return a.subkey(nonce).Open(dst, nil, ciphertext, additionalData)
}

// ID returns the build-local identifier of XAES-256-GCM.
func (*aead) ID() crypto.ID { return id }

// Algorithm returns [crypto.AlgXAES256GCM]. Persist it beside every
// ciphertext.
func (*aead) Algorithm() crypto.Algorithm { return crypto.AlgXAES256GCM }

// subkey returns AES-256-GCM under the key that nonce derives, with the
// nonces of the FIPS 140-3 module of the standard library. It panics when
// nonce is not [NonceSize] bytes long.
//
// aes.NewCipher refuses only a key that is not 16, 24 or 32 bytes long, and
// cipher.NewGCMWithRandomNonce only a block that crypto/aes did not return.
// The derived key is 32 bytes long and its block comes from aes.NewCipher,
// so subkey has no error to return.
func (a *aead) subkey(nonce []byte) cipher.AEAD {
	if len(nonce) != NonceSize {
		panic("xaesgcm: incorrect nonce length given to XAES-256-GCM") //nolint:forbidigo // the contract of cipher.AEAD
	}

	s := scratches.Get()
	a.derive(s, nonce)
	block, _ := aes.NewCipher(s.key[:])
	scratches.Put(s)

	gcm, _ := cipher.NewGCMWithRandomNonce(block)

	return gcm
}

// derive writes the key Kx that nonce derives from the key of a into s.key,
// by steps 3 to 5 of the specification. The CMAC inputs M1 and M2 differ in
// their counter byte alone, so derive builds M1 xor K1 once and changes its
// counter byte for M2. nonce is [NonceSize] bytes long.
func (a *aead) derive(s *scratch, nonce []byte) {
	s.block = a.k1
	s.block[1] ^= 0x01
	s.block[2] ^= label
	for i, b := range nonce {
		s.block[4+i] ^= b
	}

	a.block.Encrypt(s.key[:aes.BlockSize], s.block[:])
	s.block[1] ^= 0x01 ^ 0x02
	a.block.Encrypt(s.key[aes.BlockSize:], s.block[:])
}
