// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package crypto

import (
	"crypto/cipher"
	"encoding/binary"
	"slices"

	"go.thesmos.sh/core/pool"
	"go.thesmos.sh/core/rand"
)

// AEAD is an authenticated-encryption primitive with associated data. It
// embeds the contract of [cipher.AEAD] and adds an identity.
//
// A caller persists [AEAD.Algorithm] with each ciphertext, as it persists
// [Hasher.Algorithm] with a digest, and selects the implementation of
// that algorithm when it opens the ciphertext. A later build can then
// open the ciphertext with an implementation that it chooses at run time.
//
// A caller that manages its own nonces calls the methods of the embedded
// [cipher.AEAD] directly. A counter under a key per message is such a
// nonce, and it costs less than a random one. Every other caller uses
// [Seal] and [Open], which manage the nonce.
//
// # Concurrency
//
// Implementations must be safe for concurrent use.
//
//dokimi:stub
//dokimi:suite
//dokimi:out path=../coretest/cryptotest/
type AEAD interface {
	// Seal and Open panic when nonce is not NonceSize() bytes, as
	// [cipher.AEAD] documents. AEAD embeds the contract of the standard
	// library, so a cipher.AEAD of any package implements it without an
	// adapter, and the panic is part of that contract.
	//
	// It is the only part of core that panics. [Seal] and [Open] build
	// the nonce themselves and never pass one of another length. A caller
	// that calls these methods directly checks the length of its nonce
	// first.
	cipher.AEAD

	// Seal encrypts and authenticates plaintext, and appends the result to
	// dst, as [cipher.AEAD] documents. An AEAD with random nonces
	// generates a nonce in each call, so two calls with one input return
	// different ciphertexts.
	//
	//dokimi:mixin sample builder=SealInputs
	//dokimi:refuse deterministic reason="an AEAD with random nonces generates a new nonce in each Seal"
	Seal(dst, nonce, plaintext, additionalData []byte) []byte

	// Open authenticates and decrypts ciphertext, and appends the
	// plaintext to dst, as [cipher.AEAD] documents.
	//
	//dokimi:mixin sample builder=OpenInputs
	Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error)

	// ID returns the identifier of the implementation in this build.
	ID() ID

	// Algorithm returns the name of the algorithm, which every build
	// shares. Persist it with each ciphertext.
	Algorithm() Algorithm
}

// These constants describe a sealed envelope. A verifier in another
// language rebuilds the authenticated bytes from them.
const (
	// EnvelopeVersion is the layout of the envelope that this build
	// writes, and the only layout that it opens.
	//
	// The version is in two places: the first byte of the envelope, and
	// the [Domain.Version] of the bytes that the tag covers. The byte lets
	// a reader return [ErrEnvelopeVersion] before it uses a key, so an
	// operator who migrates a format sees that error. The domain version
	// makes the refusal unforgeable. Bytes written under one layout do not
	// authenticate under another, whatever an attacker writes into the
	// plain byte.
	EnvelopeVersion = 1

	// EnvelopeDomainName separates the authenticated bytes of an envelope
	// from every other framed sequence of a deployment.
	EnvelopeDomainName = "thesmos.crypto.aead"

	// EnvelopeVersionSize is the width of the leading version byte, and
	// so the offset of the algorithm length within a sealed envelope.
	EnvelopeVersionSize = 1

	// EnvelopeAlgorithmLenSize is the width of the algorithm-length byte.
	// A caller that computes the offset of the nonce in an envelope adds
	// it to [EnvelopeVersionSize] and to the length of the algorithm.
	EnvelopeAlgorithmLenSize = 1

	// envelopeHeaderFixed is the width of the part of the header that does
	// not depend on the algorithm. The part consists of
	// [EnvelopeVersionSize] and [EnvelopeAlgorithmLenSize], one byte each.
	// It is a literal, because an operator in a const declaration has no
	// coverage counter and a mutation tool never runs a mutant of it.
	envelopeHeaderFixed = 2

	// maxAlgorithmLen is the length of the longest algorithm name, the
	// largest value of one byte.
	maxAlgorithmLen = 255

	// lengthPrefixSize is the width of the length that [Framer.Bytes]
	// writes before a field.
	lengthPrefixSize = 8
)

// samplePlaintext is the plaintext of the inputs that [SealInputs]
// returns.
const samplePlaintext = "plaintext"

// aadPool is the pool of the buffers in which the helpers build the frame
// of the associated data. The frame does not escape the call that builds
// it, so an allocation per call would be the whole cost of the helpers.
var aadPool = pool.NewPool(func() *[]byte {
	b := make([]byte, 0, 128)

	return &b
})

// SealedSize returns the length of the envelope that [AppendSeal] writes
// for n bytes of plaintext under a. [Seal] sizes its buffer with it.
//
// The result is exact for an AEAD whose ciphertext is always n +
// a.Overhead() bytes long, as the ciphertext of AES-GCM is.
// [cipher.AEAD] documents Overhead as a maximum. [AppendSealChunk]
// refuses a chunk whose envelope has another length, so every sealed
// chunk has the length SealedSize returns.
//
// # Allocation contract
//
// Zero alloc.
func SealedSize(a AEAD, n int) int {
	return envelopeHeaderFixed + len(a.Algorithm()) + a.NonceSize() + n + a.Overhead()
}

// frameAAD appends to dst the bytes that the tag of a sealed envelope
// covers. They are the domain tag, the algorithm name and the associated
// data of the caller. Both fields have a length prefix, so no aad of a
// caller can imitate a longer algorithm name.
//
// For a chunk, the chunk frame of chunk and aad takes the place of the
// caller's associated data. It is built in dst after its length, so the
// envelope frame and the chunk frame share one buffer and one copy.
func frameAAD(dst, name, aad []byte, chunk *chunkAt) []byte {
	f := NewFramer(dst, Domain{Name: EnvelopeDomainName, Version: EnvelopeVersion})
	f.Bytes(name)

	if chunk == nil {
		f.Bytes(aad)

		return f.Frame()
	}

	// The encoding Framer.Bytes gives a field: its length, then its
	// bytes. The length is written once the chunk frame's size is known.
	out := f.Frame()
	at := len(out)
	out = binary.BigEndian.AppendUint64(out, 0)
	out = appendChunkFrame(out, chunk, aad)
	frame := out[at+lengthPrefixSize:]
	binary.BigEndian.PutUint64(out[at:], uint64(len(frame)))

	return out
}

// splitEnvelope parses the header of a sealed envelope, and returns the
// algorithm name of the header and the bytes after it.
func splitEnvelope(sealed []byte) (name, rest []byte, err error) {
	if len(sealed) < envelopeHeaderFixed {
		return nil, nil, ErrCiphertextShort
	}

	// The version is checked before anything is measured: an unknown
	// layout means the fields below may not be where they look.
	if sealed[0] != EnvelopeVersion {
		return nil, nil, ErrEnvelopeVersion
	}

	algLen := int(sealed[1])
	if algLen == 0 {
		return nil, nil, ErrAlgorithmSize
	}

	if len(sealed) < envelopeHeaderFixed+algLen {
		return nil, nil, ErrCiphertextShort
	}

	return sealed[envelopeHeaderFixed : envelopeHeaderFixed+algLen],
		sealed[envelopeHeaderFixed+algLen:], nil
}

// Seal reads a random nonce from r, encrypts plaintext under a with aad
// as associated data, and returns a sealed envelope:
//
//	version || algLen || algorithm || nonce || ciphertext || tag
//
// An AEAD whose NonceSize is 0 generates the nonce itself and prepends
// it to its own output, as
// [go.thesmos.sh/core/crypto/aesgcm.NewRandomNonce] does. Seal then
// reads nothing from r, which may be nil, and the envelope has the
// same layout.
//
// Seal writes the algorithm and the nonce into the envelope, because
// both are needed to open it and neither is secret. A value stored
// apart from its ciphertext can be lost, and the loss of either makes
// the ciphertext unrecoverable.
//
// The tag covers the header, so an attacker cannot rewrite the algorithm
// to make an open use another primitive.
//
// A caller without a reason to manage its own nonces uses Seal. A nonce
// that repeats under one key discloses the XOR of the two plaintexts of
// AES-GCM, and permits the forgery of a tag for every message under that
// key. A deterministic [rand.Rand], such as a fixed or a seeded source,
// repeats the nonce, so only a test that checks that property passes
// one.
//
// A random 96-bit nonce bounds one key to about 2^32 messages for a
// collision probability of 2^-32. Seal reads the first 96 bits of a
// 192-bit nonce for an AEAD of [go.thesmos.sh/core/crypto/xaesgcm], and
// one of its keys seals about 2^80 messages at the same risk.
//
// Returns [ErrAlgorithmSize] when a reports a name that the header
// cannot express, and the error of r when its entropy fails. Seal
// returns no output with an error.
//
// # Allocation contract
//
// Allocates the returned envelope once, at its full size. [AppendSeal]
// reuses the buffer of a caller and allocates nothing.
func Seal(a AEAD, r rand.Rand, plaintext, aad []byte) ([]byte, error) {
	return AppendSeal(make([]byte, 0, SealedSize(a, len(plaintext))), a, r, plaintext, aad)
}

// AppendSeal appends the sealed envelope to dst and returns the
// extended slice, as [Seal] does for a new one.
//
// AppendSeal does not overwrite the bytes of dst. Pass buf[:0] to reuse
// the capacity of a buffer. On failure AppendSeal returns nil and leaves
// the slice header of the caller unchanged.
//
// # Allocation contract
//
// Zero alloc when dst has capacity for the finished envelope and the Read
// of r does not allocate, as the Read of
// [go.thesmos.sh/core/rand/crypto.Rand] does not. AppendSeal reads the
// nonce into dst, and builds the frame of the associated data in a
// buffer of a package pool.
//
// A caller that passes r as a concrete type wider than a pointer causes
// one allocation per call, which boxes r into the [rand.Rand] interface.
// A caller that converts r once, outside its loop, avoids it.
func AppendSeal(dst []byte, a AEAD, r rand.Rand, plaintext, aad []byte) ([]byte, error) {
	return appendSeal(dst, a, r, plaintext, aad, nil)
}

// appendSeal is [AppendSeal], with the chunk frame of chunk and aad as
// the associated data when chunk is not nil.
func appendSeal(dst []byte, a AEAD, r rand.Rand, plaintext, aad []byte, chunk *chunkAt) ([]byte, error) {
	alg := a.Algorithm()
	if len(alg) == 0 || len(alg) > maxAlgorithmLen {
		return nil, ErrAlgorithmSize
	}

	nonceSize := a.NonceSize()
	body := len(dst)

	//nolint:gosec // G115: bounded by the maxAlgorithmLen check above.
	dst = append(dst, EnvelopeVersion, byte(len(alg)))
	dst = append(dst, alg...)

	// dst grows by the size of the nonce, so r reads the nonce into the
	// envelope without a temporary buffer.
	dst = slices.Grow(dst, nonceSize)
	dst = dst[:len(dst)+nonceSize]
	nonce := dst[len(dst)-nonceSize:]

	// An AEAD whose NonceSize is 0 generates the nonce inside Seal, so
	// appendSeal does not read r.
	if nonceSize > 0 {
		if _, err := r.Read(nonce); err != nil {
			return nil, err
		}
	}

	// name is sliced after the last append to dst, so no growth of dst
	// moves its bytes.
	name := dst[body+envelopeHeaderFixed : body+envelopeHeaderFixed+len(alg)]

	scratch := aadPool.Get()
	framed := frameAAD((*scratch)[:0], name, aad, chunk)
	out := a.Seal(dst, nonce, plaintext, framed)
	*scratch = framed[:0]
	aadPool.Put(scratch)

	return out, nil
}

// Open parses a sealed envelope, checks that its header has the
// algorithm of a, and decrypts it with aad as the associated data of the
// caller.
//
// Returns [ErrEnvelopeVersion] for a layout that this build does not
// know, [ErrCiphertextShort] for an envelope shorter than the lengths in
// its header, [ErrAlgorithmSize] for a header with an empty algorithm
// name, and [ErrAlgorithmMismatch] for an algorithm other than that of a.
// Open decides all four from the header, before it uses a key.
//
// Every other failure returns the error of the underlying [cipher.AEAD],
// unwrapped, so a caller cannot tell the failures apart. They are a
// modified ciphertext, a modified tag, a rewritten algorithm, other
// associated data and the wrong key. A caller must not be able to branch
// on the reason that authentication failed.
//
// Open has no fallback to a ciphertext with only a nonce prefix. An
// attacker who strips bytes would force a reader with such a fallback
// into the weaker mode, and the binding of the algorithm would then
// cover nothing.
//
// Open returns nil for an empty plaintext, as [cipher.AEAD] does, and not
// an empty slice. A caller compares the contents or the length. A result
// that is never nil would cost an allocation for a distinction that the
// payload does not have.
//
// # Allocation contract
//
// One allocation for the returned plaintext. [AppendOpen] reuses a buffer
// and allocates nothing.
func Open(a AEAD, sealed, aad []byte) ([]byte, error) {
	return AppendOpen(nil, a, sealed, aad)
}

// AppendOpen appends the recovered plaintext to dst and returns the
// extended slice, as [Open] does for a new one.
//
// AppendOpen does not overwrite the bytes of dst. Pass buf[:0] to reuse
// the capacity of a buffer. On failure AppendOpen returns nil, so a caller
// cannot mistake a partial result for a plaintext. A failure of the AEAD
// can overwrite the capacity of dst past its length, as [cipher.AEAD]
// documents.
//
// # Allocation contract
//
// Zero alloc when dst has capacity for the plaintext.
func AppendOpen(dst []byte, a AEAD, sealed, aad []byte) ([]byte, error) {
	return appendOpen(dst, a, sealed, aad, nil)
}

// appendOpen is [AppendOpen], with the chunk frame of chunk and aad as
// the associated data when chunk is not nil.
func appendOpen(dst []byte, a AEAD, sealed, aad []byte, chunk *chunkAt) ([]byte, error) {
	name, rest, err := splitEnvelope(sealed)
	if err != nil {
		return nil, err
	}

	// Compared without converting either side to a new string: the
	// compiler recognises this shape and allocates nothing.
	if string(name) != string(a.Algorithm()) {
		return nil, ErrAlgorithmMismatch
	}

	nonceSize := a.NonceSize()
	if len(rest) < nonceSize {
		return nil, ErrCiphertextShort
	}

	scratch := aadPool.Get()
	framed := frameAAD((*scratch)[:0], name, aad, chunk)
	plaintext, err := a.Open(dst, rest[:nonceSize], rest[nonceSize:], framed)
	*scratch = framed[:0]
	aadPool.Put(scratch)

	if err != nil {
		return nil, err
	}

	return plaintext, nil
}

// PeekAlgorithm returns the algorithm in the header of a sealed envelope,
// without a key and without authentication.
//
// A caller with several implementations uses it to choose the one to open
// with, so the key of the dispatch comes from the ciphertext and not from
// a side channel that has to be kept in step with it.
//
// An attacker can write the name, so it is only a hint for the choice of
// a key. It does not show whether the bytes are genuine. The tag covers
// the algorithm, so an open of an envelope with a rewritten name fails to
// authenticate. A caller refuses an envelope whose name it does not
// recognise, and does not try another implementation for it.
//
// Returns [ErrCiphertextShort], [ErrEnvelopeVersion], or
// [ErrAlgorithmSize] for an envelope whose header does not parse.
//
// # Allocation contract
//
// One allocation for the returned string.
func PeekAlgorithm(sealed []byte) (Algorithm, error) {
	name, _, err := splitEnvelope(sealed)
	if err != nil {
		return "", err
	}

	return Algorithm(name), nil
}

// SealInputs returns the inputs of a call of [AEAD.Seal] on a: no dst, a
// nonce of a.NonceSize() zero bytes, a plaintext of nine bytes and no
// associated data. The conformance suite of AEAD calls Seal with them, so
// each call passes a nonce of the length that Seal requires.
//
// The nonce is all zeros, so two calls of Seal with these inputs under
// one key repeat the nonce, which breaks AES-GCM. Only a test calls Seal
// with them.
func SealInputs(a AEAD) (dst, nonce, plaintext, additionalData []byte) {
	return nil, make([]byte, a.NonceSize()), []byte(samplePlaintext), nil
}

// OpenInputs returns the inputs of a call of [AEAD.Open] on a: no dst, the
// nonce of [SealInputs], the ciphertext that Seal returns for the inputs
// of SealInputs, and no associated data. The conformance suite of AEAD
// calls Open with them, so each call opens a ciphertext of a.
func OpenInputs(a AEAD) (dst, nonce, ciphertext, additionalData []byte) {
	_, nonce, plaintext, additionalData := SealInputs(a)

	return nil, nonce, a.Seal(nil, nonce, plaintext, additionalData), additionalData
}
