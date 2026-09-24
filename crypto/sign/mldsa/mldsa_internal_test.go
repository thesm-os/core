// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package mldsa

import (
	stdmldsa "crypto/mldsa"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/errs"
)

// TestUnavailable covers the path where the FIPS 140-3 module in use
// does not provide ML-DSA. The standard library's parser and key
// expansion fail only then, so the tests pass a failing function to
// [parseVerifier] and [expandSigner].
func TestUnavailable(t *testing.T) {
	t.Parallel()

	moduleErr := testkit.TestError("module does not provide ML-DSA")

	t.Run("parseVerifier returns the parser's error classified Unsupported", func(t *testing.T) {
		t.Parallel()
		parse := func(stdmldsa.Parameters, []byte) (*stdmldsa.PublicKey, error) { return nil, moduleErr }

		_, err := parseVerifier(MLDSA44, make([]byte, stdmldsa.MLDSA44PublicKeySize), "", parse)
		testkit.ErrorIs(t, err, moduleErr, "the parser's error must be returned")
		testkit.Equal(t, errs.Classify(err), errs.Unsupported, "the error must classify as Unsupported")
	})

	t.Run("expandSigner returns the expansion's error classified Unsupported", func(t *testing.T) {
		t.Parallel()
		expand := func(stdmldsa.Parameters, []byte) (*stdmldsa.PrivateKey, error) { return nil, moduleErr }

		_, err := expandSigner(MLDSA44, make([]byte, SeedSize), "", expand)
		testkit.ErrorIs(t, err, moduleErr, "the expansion's error must be returned")
		testkit.Equal(t, errs.Classify(err), errs.Unsupported, "the error must classify as Unsupported")
	})
}
