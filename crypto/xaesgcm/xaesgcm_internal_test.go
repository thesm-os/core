// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package xaesgcm

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha3"
	"encoding/hex"
	"testing"

	"go.dokimi.dev/assert"
)

// vectorNonce is the first half of the 192-bit nonce of both vectors of the
// specification.
const vectorNonce = "ABCDEFGHIJKL"

// TestDerivation is in package xaesgcm because the CMAC subkey, the derived key
// and the scratch of a derivation are unexported. It checks them against the
// test vectors and the accumulated randomized tests of the specification.
func TestDerivation(t *testing.T) {
	t.Parallel()

	t.Run("New", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			giveKey byte
			wantK1  string
		}{
			{
				name:    "computes the subkey K1 of a key whose L starts with a 0 bit",
				giveKey: 0x01,
				wantK1:  "e531954aca063d5b8d9c47a47d4cc6f0",
			},
			{
				name:    "computes the subkey K1 of a key whose L starts with a 1 bit",
				giveKey: 0x03,
				wantK1:  "23810ec50edb99f374409466ed1f4b7b",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				a := vectorAEAD(t, tt.giveKey)
				assert.Equal(t, hex.EncodeToString(a.k1[:]), tt.wantK1, "K1 must be the subkey of the vector")
			})
		}
	})

	t.Run("derive", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name        string
			giveKey     byte
			wantDerived string
		}{
			{
				name:        "derives the key of the vector without associated data",
				giveKey:     0x01,
				wantDerived: "c8612c9ed53fe43e8e005b828a1631a0bbcb6ab2f46514ec4f439fcfd0fa969b",
			},
			{
				name:        "derives the key of the vector with associated data",
				giveKey:     0x03,
				wantDerived: "e9c621d4cdd9b11b00a6427ad7e559aeedd66b3857646677748f8ca796cb3fd8",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				var s scratch
				vectorAEAD(t, tt.giveKey).derive(&s, []byte(vectorNonce))
				assert.Equal(t, hex.EncodeToString(s.key[:]), tt.wantDerived,
					"Kx must be the derived key of the vector")
			})
		}

		accumulated := []struct {
			name       string
			iterations int
			want       string
		}{
			{
				name:       "gives the hash of the specification over 10,000 accumulated randomized tests",
				iterations: 10_000,
				want:       "e6b9edf2df6cec60c8cbd864e2211b597fb69a529160cd040d56c0c210081939",
			},
			{
				name:       "gives the hash of the specification over 1,000,000 accumulated randomized tests",
				iterations: 1_000_000,
				want:       "2163ae1445985a30b60585ee67daa55674df06901b890593e824b8a7c885ab15",
			},
		}
		for _, tt := range accumulated {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, accumulate(t, tt.iterations), tt.want,
					"the ciphertexts of the accumulated tests must hash to the value of the specification")
			})
		}
	})

	t.Run("scratch", func(t *testing.T) {
		t.Parallel()

		t.Run("Reset", func(t *testing.T) {
			t.Parallel()

			t.Run("clears the block and the derived key", func(t *testing.T) {
				t.Parallel()
				s := &scratch{block: [aes.BlockSize]byte{1}, key: [KeySize]byte{2}}
				s.Reset()
				assert.Equal(t, *s, scratch{}, "Reset must leave no byte of the derivation in the scratch")
			})
		})
	})
}

// accumulate runs the accumulated randomized tests of the specification for
// iterations: it reads each key, nonce, plaintext and associated data from a
// SHAKE-128 stream of an empty input, and hashes each ciphertext with a second
// SHAKE-128. The ciphertext is AES-256-GCM under the key that derive derives,
// with the second half of the nonce from the stream. accumulate also checks
// that Open recovers each plaintext, and returns the first 32 bytes of the
// hash in hexadecimal.
func accumulate(tb assert.TB, iterations int) string {
	tb.Helper()

	stream, hash := sha3.NewSHAKE128(), sha3.NewSHAKE128()
	key, nonce, length := make([]byte, KeySize), make([]byte, 2*NonceSize), make([]byte, 1)
	var s scratch
	for range iterations {
		_, _ = stream.Read(key)
		_, _ = stream.Read(nonce)
		_, _ = stream.Read(length)
		plaintext := make([]byte, int(length[0]))
		_, _ = stream.Read(plaintext)
		_, _ = stream.Read(length)
		aad := make([]byte, int(length[0]))
		_, _ = stream.Read(aad)

		got, err := New(key)
		assert.NoError(tb, err, "New must accept a key of 32 bytes")
		a, ok := got.(*aead)
		assert.True(tb, ok, "New must return the AEAD of the package")
		a.derive(&s, nonce[:NonceSize])
		block, err := aes.NewCipher(s.key[:])
		assert.NoError(tb, err, "AES must accept the derived key")
		gcm, err := cipher.NewGCM(block)
		assert.NoError(tb, err, "GCM must accept the block cipher of AES")
		ciphertext := gcm.Seal(nil, nonce[NonceSize:], plaintext, aad)

		opened, err := a.Open(nil, nonce[:NonceSize], append(bytes.Clone(nonce[NonceSize:]), ciphertext...), aad)
		assert.NoError(tb, err, "Open must open the ciphertext of the accumulated test")
		assert.Equal(tb, opened, plaintext, "Open must recover the plaintext of the accumulated test",
			assert.EquateEmpty())

		_, _ = hash.Write(ciphertext)
	}

	sum := make([]byte, 32)
	_, _ = hash.Read(sum)

	return hex.EncodeToString(sum)
}

// vectorAEAD returns the AEAD of New over the 32-byte key of a vector, which
// repeats key. It fails tb when New refuses the key.
func vectorAEAD(tb assert.TB, key byte) *aead {
	tb.Helper()

	got, err := New(bytes.Repeat([]byte{key}, KeySize))
	assert.NoError(tb, err, "New must accept a key of 32 bytes")
	a, ok := got.(*aead)
	assert.True(tb, ok, "New must return the AEAD of the package")

	return a
}
