// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package mldsa

import (
	stdmldsa "crypto/mldsa"
	"errors"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/errs"
)

// TestMLDSAResults checks the unexported functions that take the result
// of a call into crypto/mldsa. The parser and the key expansion fail
// only in a FIPS 140-3 module without ML-DSA, and Sign only for the zero
// private key, so only these tests run the error branches.
func TestMLDSAResults(t *testing.T) {
	t.Parallel()

	errModule := errors.New("module does not provide ML-DSA")

	t.Run("parseVerifier", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the error of the parser under the class Unsupported", func(t *testing.T) {
			t.Parallel()
			parse := func(stdmldsa.Parameters, []byte) (*stdmldsa.PublicKey, error) { return nil, errModule }
			_, err := parseVerifier(MLDSA44, make([]byte, stdmldsa.MLDSA44PublicKeySize), "", parse)
			expect.ErrorIs(t, err, errModule, "parseVerifier must wrap the error of the parser")
			expect.Equal(t, errs.Classify(err), errs.Unsupported, "the error must classify as Unsupported")
		})
	})

	t.Run("expandSigner", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the error of the expansion under the class Unsupported", func(t *testing.T) {
			t.Parallel()
			expand := func(stdmldsa.Parameters, []byte) (*stdmldsa.PrivateKey, error) { return nil, errModule }
			_, err := expandSigner(MLDSA44, make([]byte, SeedSize), "", expand)
			expect.ErrorIs(t, err, errModule, "expandSigner must wrap the error of the expansion")
			expect.Equal(t, errs.Classify(err), errs.Unsupported, "the error must classify as Unsupported")
		})
	})

	t.Run("appendSignature", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the error of Sign", func(t *testing.T) {
			t.Parallel()
			errSign := errors.New("signing failed")
			_, err := appendSignature([]byte("dst"), nil, errSign)
			assert.ErrorIs(t, err, errSign, "appendSignature must return the error of Sign")
		})

		t.Run("returns dst unchanged for an error of Sign", func(t *testing.T) {
			t.Parallel()
			got, _ := appendSignature([]byte("dst"), []byte("sig"), errors.New("signing failed"))
			assert.Equal(t, string(got), "dst", "appendSignature must not append after an error")
		})

		t.Run("appends the signature to dst", func(t *testing.T) {
			t.Parallel()
			got, err := appendSignature([]byte("dst"), []byte("sig"), nil)
			assert.NoError(t, err, "appendSignature must accept a signature")
			assert.Equal(t, string(got), "dstsig", "appendSignature must append the signature")
		})
	})
}
