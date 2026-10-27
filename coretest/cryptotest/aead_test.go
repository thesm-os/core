// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest_test

import (
	"bytes"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/conform"

	"go.thesmos.sh/core/coretest/cryptotest"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/aesgcm"
)

// TestAEAD checks that the AES-256-GCM AEAD of crypto/aesgcm passes the checks
// of the suite of crypto.AEAD. The second instance of a check is another AEAD
// of the same key.
func TestAEAD(t *testing.T) {
	t.Parallel()

	key := bytes.Repeat([]byte{0x42}, aesgcm.KeySize256)
	newAEAD := func(h *cryptotest.AEADHandle) crypto.AEAD {
		a, err := aesgcm.New(key)
		assert.NoError(h, err, "aesgcm.New accepts a key of 32 bytes")

		return a
	}
	cryptotest.RunAEADSuiteFromHandle(t, newAEAD, conform.Second(newAEAD))
}
