// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest_test

import (
	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/coretest/cryptotest"
	"go.thesmos.sh/core/crypto/sign/ed25519"
)

// newEd25519 returns the Ed25519 Signer of the key of NewEd25519Sample. It
// fails tb when New refuses the key.
func newEd25519(tb assert.TB) *ed25519.Signer {
	tb.Helper()

	s, err := ed25519.New(cryptotest.NewEd25519Sample().StdlibPriv)
	assert.NoError(tb, err, "New accepts the private key of the sample")

	return s
}
