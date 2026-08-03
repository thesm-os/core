// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package aesgcm

import (
	"crypto/cipher"

	"go.thesmos.sh/core/crypto"
)

// NewGCMForTest exposes newGCM so the cipher.NewGCM failure path can
// be exercised. That path fires only for a block size other than 128
// bits, which AES never produces, so reaching it requires handing the
// constructor a different cipher entirely.
var NewGCMForTest = func(block cipher.Block) (crypto.AEAD, error) {
	return newGCM(block, id256, crypto.AlgAES256GCM)
}
