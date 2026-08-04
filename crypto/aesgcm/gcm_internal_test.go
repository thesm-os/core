// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package aesgcm

import (
	"crypto/des" //nolint:gosec // 64-bit block cipher, used only to prove GCM rejects it
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto"
)

// Internal rather than in the aesgcm_test package because newGCM is
// unexported. The export_test.go indirection that used to bridge the
// two is what gobco cannot resolve: it type-checks the external test
// package without merging the internal one, so a symbol declared in
// export_test.go and referenced from aesgcm_test.go reads as
// undefined and aborts the whole branch-coverage run.
func TestNewGCMRejectsANonAESBlockSize(t *testing.T) {
	t.Parallel()

	// GCM is defined only over a 128-bit block. AES always gives
	// that, so this path is unreachable through New — DES, with its
	// 64-bit block, is the only way to prove the guard works rather
	// than merely exists.
	block, err := des.NewCipher(make([]byte, 8))
	testkit.NoError(t, err, "DES must accept an 8-byte key")

	_, err = newGCM(block, id256, crypto.AlgAES256GCM)
	testkit.ErrorIs(t, err, crypto.ErrKeySize,
		"GCM must reject a 64-bit block cipher")
}
