// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package aesgcm

import (
	"crypto/cipher"
	"crypto/des" //nolint:gosec // 64-bit block cipher, used only to prove GCM rejects it
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/errs"
)

// TestNewGCMRejectsANonAESBlock is in package aesgcm because newGCM is
// unexported. An export_test.go bridge is not an option: gobco
// type-checks the external test package without the internal one, so
// a symbol the bridge declares reads as undefined and aborts the
// branch-coverage run.
func TestNewGCMRejectsANonAESBlock(t *testing.T) {
	t.Parallel()

	// GCM is defined only over a 128-bit block, and the random-nonce
	// mode only over an AES block. AES always gives both, so this path
	// is reachable through New only in FIPS 140-only mode. DES, with
	// its 64-bit block, reaches it without that mode.
	modes := []struct {
		mode func(cipher.Block) (cipher.AEAD, error)
		name string
	}{
		{cipher.NewGCM, "caller nonces"},
		{cipher.NewGCMWithRandomNonce, "module nonces"},
	}
	for _, tt := range modes {
		t.Run("wraps the refusal for "+tt.name, func(t *testing.T) {
			t.Parallel()
			block, err := des.NewCipher(make([]byte, 8))
			testkit.NoError(t, err, "DES must accept an 8-byte key")

			_, err = newGCM(block, tt.mode, id256, crypto.AlgAES256GCM)
			testkit.Error(t, err, "GCM must reject a 64-bit block cipher")
			testkit.Equal(t, errs.Classify(err), errs.Unsupported,
				"the refusal must classify as Unsupported")
		})
	}
}
