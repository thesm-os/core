// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package aesgcm

import (
	"crypto/cipher"
	"crypto/des" //nolint:gosec // 64-bit block cipher, used only to prove GCM rejects it
	"testing"

	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/errs"
)

// TestNewGCM is in package aesgcm because newGCM is unexported. An
// export_test.go bridge is not an option: gobco type-checks the external
// test package without the internal one, so a symbol the bridge declares
// reads as undefined and aborts the branch-coverage run.
//
// GCM is defined only over a 128-bit block, and the random-nonce mode
// only over an AES block. AES always gives both, so New reaches this
// path only in FIPS 140-only mode. DES, with its 64-bit block, reaches
// it without that mode.
func TestNewGCM(t *testing.T) {
	t.Parallel()

	modes := []struct {
		name string
		mode func(cipher.Block) (cipher.AEAD, error)
	}{
		{name: "returns an error of class Unsupported for a block of 64 bits with caller nonces", mode: cipher.NewGCM},
		{
			name: "returns an error of class Unsupported for a block of 64 bits with module nonces",
			mode: cipher.NewGCMWithRandomNonce,
		},
	}
	for _, tt := range modes {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			block, err := des.NewCipher(make([]byte, 8))
			assert.NoError(t, err, "DES must accept an 8-byte key")
			_, err = newGCM(block, tt.mode, id256, crypto.AlgAES256GCM)
			assert.Equal(t, errs.Classify(err), errs.Unsupported, "the refusal of GCM must classify as Unsupported")
		})
	}
}
