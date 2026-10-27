// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package cryptotest_test

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/conform"

	"go.thesmos.sh/core/coretest/cryptotest"
	"go.thesmos.sh/core/crypto/sign"
	"go.thesmos.sh/core/crypto/sign/ecdsap384"
	"go.thesmos.sh/core/crypto/sign/ed25519"
)

// TestSign checks that the Ed25519 signer of crypto/sign/ed25519 passes the
// checks of the suites of sign.Signer and sign.Verifier, and that the ECDSA
// P-384 signer of crypto/sign/ecdsap384 passes the checks of the suites of
// sign.StreamingSigner and sign.StreamingVerifier. The second subject of a
// check of a verifier is the Signer of its key.
func TestSign(t *testing.T) {
	t.Parallel()

	t.Run("Signer", func(t *testing.T) {
		t.Parallel()

		cryptotest.RunSignerSuiteFromHandle(t,
			func(h *cryptotest.SignerHandle) *ed25519.Signer { return newEd25519(h) })
	})

	t.Run("Verifier", func(t *testing.T) {
		t.Parallel()

		cryptotest.RunVerifierSuiteFromHandle(t,
			func(h *cryptotest.VerifierHandle) *ed25519.Verifier { return newEd25519(h).Verifier },
			conform.Second(func(h *cryptotest.VerifierHandle) sign.Verifier { return newEd25519(h) }))
	})

	t.Run("StreamingSigner", func(t *testing.T) {
		t.Parallel()

		cryptotest.RunStreamingSignerSuiteFromHandle(t,
			func(h *cryptotest.StreamingSignerHandle) *ecdsap384.Signer { return newECDSAP384(h) })
	})

	t.Run("StreamingVerifier", func(t *testing.T) {
		t.Parallel()

		cryptotest.RunStreamingVerifierSuiteFromHandle(t,
			func(h *cryptotest.StreamingVerifierHandle) *ecdsap384.Verifier { return newECDSAP384(h).Verifier },
			conform.Second(func(h *cryptotest.StreamingVerifierHandle) sign.StreamingVerifier {
				return newECDSAP384(h)
			}))
	})
}

// newECDSAP384 returns the ECDSA P-384 Signer of the key of
// NewECDSAP384Sample. It fails tb when New refuses the key.
func newECDSAP384(tb assert.TB) *ecdsap384.Signer {
	tb.Helper()

	s, err := ecdsap384.New(cryptotest.NewECDSAP384Sample().StdlibPriv)
	assert.NoError(tb, err, "New accepts the private key of the sample")

	return s
}
