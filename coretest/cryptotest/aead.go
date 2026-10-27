// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"

	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/crypto"
	randcrypto "go.thesmos.sh/core/rand/crypto"
)

// The sizes of the sound AEAD of the planted defects, AES-256-GCM.
const (
	// aeadKeySize is the size of an AES-256 key.
	aeadKeySize = 32

	// gcmNonceSize is the size of a nonce of AES-GCM.
	gcmNonceSize = 12

	// gcmTagSize is the size of a tag of AES-GCM.
	gcmTagSize = 16

	// aeadLargePlaintext is the size of the largest plaintext of
	// AEADRoundTripsAPlaintext.
	aeadLargePlaintext = 4096
)

// The plaintexts, the associated data and the keys of the checks of an AEAD.
var (
	aeadPayload  = []byte("payload")
	aeadAAD      = []byte("aad")
	aeadOtherAAD = []byte("different")

	// aeadEmptyAAD is the end of the frame of crypto.Open for an empty
	// associated data: its length 0 in 8 bytes.
	aeadEmptyAAD = make([]byte, 8)

	// aeadSoundKey is the key of the sound AEAD and of the sound Keeper of
	// the planted defects.
	aeadSoundKey = bytes.Repeat([]byte{0x42}, aeadKeySize)

	// aeadFixedNonce is the nonce of every Seal of the planted defect of
	// AEADSealDrawsAFreshNonce.
	aeadFixedNonce = make([]byte, gcmNonceSize)
)

// aeadSoundID is the ID of the AES-256-GCM AEAD of the standard library that
// the planted defects of the suite of AEAD change.
var aeadSoundID = crypto.ID{'c', 'r', 'y', 'p', 't', 'o', 't', 'e', 's', 't', '/', 'a', 'e', 'a', 'd'}

// AEADRoundTripsAPlaintext checks that Open recovers the plaintext that Seal
// sealed, for a nil, an empty, a 1-byte and a 4096-byte plaintext.
//
//dokimi:check AEAD.Open defect=aeadChangesALargePlaintext
func AEADRoundTripsAPlaintext(h *AEADHandle) {
	a := h.Subject()
	for _, plaintext := range [][]byte{nil, {}, []byte("x"), bytes.Repeat([]byte{0x5A}, aeadLargePlaintext)} {
		opened, err := crypto.Open(a, sealEnvelope(h, a, plaintext, aeadAAD), aeadAAD)
		assert.NoError(h, err, h.Statement())
		assert.Equal(h, opened, plaintext, h.Statement(), assert.EquateEmpty())
	}
}

// AEADOpensWithoutAssociatedData checks that Open of an envelope that Seal
// sealed without associated data recovers the plaintext.
//
//dokimi:check AEAD.Open defect=aeadRefusesEmptyAssociatedData
func AEADOpensWithoutAssociatedData(h *AEADHandle) {
	a := h.Subject()
	assert.RoundTrip(h, func(p []byte) ([]byte, error) { return crypto.Seal(a, randcrypto.New(), p, nil) },
		func(sealed []byte) ([]byte, error) { return crypto.Open(a, sealed, nil) }, aeadPayload, h.Statement())
}

// AEADSealDrawsAFreshNonce checks that two Seals of one plaintext return
// different envelopes.
//
//dokimi:check AEAD.Seal defect=aeadRepeatsItsNonce
func AEADSealDrawsAFreshNonce(h *AEADHandle) {
	a := h.Subject()
	assert.NotEqual(h, sealEnvelope(h, a, aeadPayload, nil), sealEnvelope(h, a, aeadPayload, nil), h.Statement())
}

// AEADSealedLengthIsTheEnvelope checks that an envelope is as long as its
// header, its nonce, its plaintext and the Overhead of the AEAD.
//
//dokimi:check AEAD.Overhead defect=aeadOverstatesItsOverhead
func AEADSealedLengthIsTheEnvelope(h *AEADHandle) {
	a := h.Subject()
	assert.Length(h, sealEnvelope(h, a, aeadPayload, nil), crypto.SealedSize(a, len(aeadPayload)), h.Statement())
}

// AEADRefusesAModifiedCiphertext checks that Open returns an error for an
// envelope whose first byte of ciphertext changed.
//
//dokimi:check AEAD.Open defect=aeadRestoresTheFirstCiphertextByte
func AEADRefusesAModifiedCiphertext(h *AEADHandle) {
	a := h.Subject()
	sealed := sealEnvelope(h, a, aeadPayload, aeadAAD)
	sealed[crypto.SealedSize(a, 0)-a.Overhead()] ^= 0x01
	_, err := crypto.Open(a, sealed, aeadAAD)
	assert.HasError(h, err, h.Statement())
}

// AEADRefusesAModifiedNonce checks that Open returns an error for an envelope
// whose first byte of nonce changed.
//
//dokimi:check AEAD.Open defect=aeadRestoresTheFirstNonceByte
func AEADRefusesAModifiedNonce(h *AEADHandle) {
	a := h.Subject()
	sealed := sealEnvelope(h, a, aeadPayload, aeadAAD)
	sealed[crypto.SealedSize(a, 0)-a.Overhead()-a.NonceSize()] ^= 0x01
	_, err := crypto.Open(a, sealed, aeadAAD)
	assert.HasError(h, err, h.Statement())
}

// AEADRefusesAModifiedTag checks that Open returns an error for an envelope
// whose last byte of tag changed.
//
//dokimi:check AEAD.Open defect=aeadRestoresTheLastTagByte
func AEADRefusesAModifiedTag(h *AEADHandle) {
	a := h.Subject()
	sealed := sealEnvelope(h, a, aeadPayload, aeadAAD)
	sealed[len(sealed)-1] ^= 0x01
	_, err := crypto.Open(a, sealed, aeadAAD)
	assert.HasError(h, err, h.Statement())
}

// AEADAuthenticatesTheAssociatedData checks that Open returns an error for an
// envelope that Seal sealed with other associated data.
//
//dokimi:check AEAD.Open defect=aeadOpensWithTheSealedAssociatedData
func AEADAuthenticatesTheAssociatedData(h *AEADHandle) {
	a := h.Subject()
	_, err := crypto.Open(a, sealEnvelope(h, a, aeadPayload, aeadAAD), aeadOtherAAD)
	assert.HasError(h, err, h.Statement())
}

// AEADIDIdentifiesTheImplementation checks that ID returns an ID that is not
// zero, and the same ID on every call.
//
//dokimi:check AEAD.ID defect=aeadHasNoID
func AEADIDIdentifiesTheImplementation(h *AEADHandle) {
	a := h.Subject()
	assert.NotEqual(h, a.ID(), crypto.ID{}, h.Statement())
	assert.Deterministic(h, func(a crypto.AEAD) (crypto.ID, error) { return a.ID(), nil }, a, h.Statement())
}

// AEADOpensUnderASecondInstance checks that a second instance over the same key
// opens an envelope of the first, and has its ID and its Algorithm.
//
//dokimi:check AEAD.Open defect=aeadOfAFreshKey second=aeadOfAFreshKey
func AEADOpensUnderASecondInstance(h *AEADHandle) {
	a, second := h.Subject(), h.Second()
	assert.RoundTrip(h, func(p []byte) ([]byte, error) { return crypto.Seal(a, randcrypto.New(), p, aeadAAD) },
		func(sealed []byte) ([]byte, error) { return crypto.Open(second, sealed, aeadAAD) }, aeadPayload,
		h.Statement())
	assert.Equal(h, second.ID(), a.ID(), h.Statement())
	assert.Equal(h, second.Algorithm(), a.Algorithm(), h.Statement())
}

// sealEnvelope seals plaintext with aad under a with a nonce of the CSPRNG of the
// operating system, and stops the check when Seal fails.
func sealEnvelope(h assert.TB, a crypto.AEAD, plaintext, aad []byte) []byte {
	h.Helper()

	sealed, err := crypto.Seal(a, randcrypto.New(), plaintext, aad)
	assert.NoError(h, err, "Seal seals the plaintext")

	return sealed
}

// stdlibAEAD is AES-256-GCM of the standard library behind crypto.AEAD. The
// planted defects of the suite of AEAD change one behaviour of it.
type stdlibAEAD struct {
	cipher.AEAD
}

// ID returns aeadSoundID.
func (stdlibAEAD) ID() crypto.ID { return aeadSoundID }

// Algorithm returns crypto.AlgAES256GCM.
func (stdlibAEAD) Algorithm() crypto.Algorithm { return crypto.AlgAES256GCM }

// newStdlibAEAD returns the AES-256-GCM AEAD of key, and stops the check when
// the standard library refuses the key.
func newStdlibAEAD(h assert.TB, key []byte) stdlibAEAD {
	h.Helper()

	block, err := aes.NewCipher(key)
	assert.NoError(h, err, "AES accepts a key of 32 bytes")
	gcm, err := cipher.NewGCM(block)
	assert.NoError(h, err, "GCM accepts the block cipher of AES")

	return stdlibAEAD{AEAD: gcm}
}

// aeadOpenDefect returns an AEAD over the sound key whose Open is open.
func aeadOpenDefect(h *AEADHandle, open func(sound stdlibAEAD, dst, nonce, ciphertext, aad []byte) ([]byte, error),
) crypto.AEAD {
	sound := newStdlibAEAD(h, aeadSoundKey)

	return NewAEADStub(h, AEADStubDelegateTo(sound),
		WithAEADOpen(func(dst, nonce, ciphertext, aad []byte) ([]byte, error) {
			return open(sound, dst, nonce, ciphertext, aad)
		}))
}

// aeadChangesALargePlaintext returns the planted defect of
// AEADRoundTripsAPlaintext: an AEAD whose Open changes the first byte of a
// plaintext of 4096 bytes.
func aeadChangesALargePlaintext(h *AEADHandle) crypto.AEAD {
	return aeadOpenDefect(h, func(sound stdlibAEAD, dst, nonce, ciphertext, aad []byte) ([]byte, error) {
		out, err := sound.Open(dst, nonce, ciphertext, aad)
		if err == nil && len(out)-len(dst) == aeadLargePlaintext {
			out[len(dst)] ^= 0x01
		}

		return out, err
	})
}

// aeadRefusesEmptyAssociatedData returns the planted defect of
// AEADOpensWithoutAssociatedData: an AEAD whose Open returns an error for an
// envelope without associated data. The frame of crypto.Open ends with the
// length 0 of the empty associated data.
func aeadRefusesEmptyAssociatedData(h *AEADHandle) crypto.AEAD {
	return aeadOpenDefect(h, func(sound stdlibAEAD, dst, nonce, ciphertext, aad []byte) ([]byte, error) {
		if bytes.HasSuffix(aad, aeadEmptyAAD) {
			return sound.Open(dst, nonce, ciphertext, append(bytes.Clone(aad), 0))
		}

		return sound.Open(dst, nonce, ciphertext, aad)
	})
}

// aeadRepeatsItsNonce returns the planted defect of AEADSealDrawsAFreshNonce:
// an AEAD that draws its own nonce, and draws the same nonce for every Seal.
func aeadRepeatsItsNonce(h *AEADHandle) crypto.AEAD {
	sound := newStdlibAEAD(h, aeadSoundKey)

	return NewAEADStub(h, AEADStubDelegateTo(sound),
		WithAEADNonceSize(func() int { return 0 }),
		WithAEADOverhead(func() int { return gcmNonceSize + gcmTagSize }),
		WithAEADSeal(func(dst, _, plaintext, aad []byte) []byte {
			//nolint:gosec // G407: the planted defect seals every plaintext under one nonce
			return sound.Seal(append(dst, aeadFixedNonce...), aeadFixedNonce, plaintext, aad)
		}),
		WithAEADOpen(func(dst, _, ciphertext, aad []byte) ([]byte, error) {
			if len(ciphertext) < gcmNonceSize {
				return sound.Open(dst, aeadFixedNonce, nil, aad)
			}

			return sound.Open(dst, ciphertext[:gcmNonceSize], ciphertext[gcmNonceSize:], aad)
		}))
}

// aeadOverstatesItsOverhead returns the planted defect of
// AEADSealedLengthIsTheEnvelope: an AEAD whose Overhead is one byte more than
// its tag.
func aeadOverstatesItsOverhead(h *AEADHandle) crypto.AEAD {
	return NewAEADStub(h, AEADStubDelegateTo(newStdlibAEAD(h, aeadSoundKey)),
		WithAEADOverhead(func() int { return gcmTagSize + 1 }))
}

// aeadRestoresTheFirstCiphertextByte returns the planted defect of
// AEADRefusesAModifiedCiphertext: an AEAD whose Open opens a ciphertext again
// with its first byte changed, when the ciphertext fails to open.
func aeadRestoresTheFirstCiphertextByte(h *AEADHandle) crypto.AEAD {
	return aeadOpenDefect(h, func(sound stdlibAEAD, dst, nonce, ciphertext, aad []byte) ([]byte, error) {
		out, err := sound.Open(dst, nonce, ciphertext, aad)
		if err == nil || len(ciphertext) == 0 {
			return out, err
		}
		restored := bytes.Clone(ciphertext)
		restored[0] ^= 0x01

		return sound.Open(dst, nonce, restored, aad)
	})
}

// aeadRestoresTheFirstNonceByte returns the planted defect of
// AEADRefusesAModifiedNonce: an AEAD whose Open opens a ciphertext again with
// the first byte of its nonce changed, when the ciphertext fails to open.
func aeadRestoresTheFirstNonceByte(h *AEADHandle) crypto.AEAD {
	return aeadOpenDefect(h, func(sound stdlibAEAD, dst, nonce, ciphertext, aad []byte) ([]byte, error) {
		out, err := sound.Open(dst, nonce, ciphertext, aad)
		if err == nil || len(nonce) == 0 {
			return out, err
		}
		restored := bytes.Clone(nonce)
		restored[0] ^= 0x01

		return sound.Open(dst, restored, ciphertext, aad)
	})
}

// aeadRestoresTheLastTagByte returns the planted defect of
// AEADRefusesAModifiedTag: an AEAD whose Open opens a ciphertext again with
// its last byte changed, when the ciphertext fails to open.
func aeadRestoresTheLastTagByte(h *AEADHandle) crypto.AEAD {
	return aeadOpenDefect(h, func(sound stdlibAEAD, dst, nonce, ciphertext, aad []byte) ([]byte, error) {
		out, err := sound.Open(dst, nonce, ciphertext, aad)
		if err == nil || len(ciphertext) == 0 {
			return out, err
		}
		restored := bytes.Clone(ciphertext)
		restored[len(restored)-1] ^= 0x01

		return sound.Open(dst, nonce, restored, aad)
	})
}

// aeadOpensWithTheSealedAssociatedData returns the planted defect of
// AEADAuthenticatesTheAssociatedData: an AEAD whose Open opens a ciphertext
// again with the associated data of the last Seal, when the ciphertext fails
// to open.
func aeadOpensWithTheSealedAssociatedData(h *AEADHandle) crypto.AEAD {
	sound := newStdlibAEAD(h, aeadSoundKey)
	var sealed []byte

	return NewAEADStub(h, AEADStubDelegateTo(sound),
		WithAEADSeal(func(dst, nonce, plaintext, aad []byte) []byte {
			sealed = bytes.Clone(aad)

			return sound.Seal(dst, nonce, plaintext, aad)
		}),
		WithAEADOpen(func(dst, nonce, ciphertext, aad []byte) ([]byte, error) {
			out, err := sound.Open(dst, nonce, ciphertext, aad)
			if err == nil {
				return out, nil
			}

			return sound.Open(dst, nonce, ciphertext, sealed)
		}))
}

// aeadHasNoID returns the planted defect of AEADIDIdentifiesTheImplementation:
// an AEAD whose ID returns the zero ID.
func aeadHasNoID(h *AEADHandle) crypto.AEAD {
	return NewAEADStub(h, AEADStubDelegateTo(newStdlibAEAD(h, aeadSoundKey)),
		WithAEADID(func() crypto.ID { return crypto.ID{} }))
}

// aeadOfAFreshKey returns the planted defect of AEADOpensUnderASecondInstance,
// and its second instance: an AEAD whose every instance draws a key of its
// own.
func aeadOfAFreshKey(h *AEADHandle) crypto.AEAD {
	key := make([]byte, aeadKeySize)
	_, err := randcrypto.New().Read(key)
	assert.NoError(h, err, "the CSPRNG of the operating system fills the key")

	return newStdlibAEAD(h, key)
}
