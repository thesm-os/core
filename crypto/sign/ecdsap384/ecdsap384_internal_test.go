// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package ecdsap384

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	stdrand "crypto/rand"
	"errors"
	"testing"

	"go.dokimi.dev/assert"
)

// The prefixes that pin the text of the errors of wrapGenerate and
// wrapSign.
const (
	generatePrefix = "ecdsap384: generate: "
	signPrefix     = "ecdsap384: sign: "
)

// TestECDSAP384Results checks the unexported functions that finish the
// result of a call into crypto/ecdsa. Generation and signing use the
// secure source of the runtime, which cannot fail in default mode, so
// only these tests run the error branches.
func TestECDSAP384Results(t *testing.T) {
	t.Parallel()

	t.Run("wrapGenerate", func(t *testing.T) {
		t.Parallel()

		t.Run("wraps the error of crypto/ecdsa under the prefix of the operation", func(t *testing.T) {
			t.Parallel()
			errGenerate := errors.New("entropy source failed")
			_, err := wrapGenerate(nil, errGenerate)
			assert.ErrorIs(t, err, errGenerate, "wrapGenerate must wrap the error of crypto/ecdsa")
			assert.HasPrefix(t, err.Error(), generatePrefix, "the error must name the package and the operation")
		})

		t.Run("returns a Signer of the key", func(t *testing.T) {
			t.Parallel()
			priv, err := ecdsa.GenerateKey(elliptic.P384(), stdrand.Reader)
			assert.NoError(t, err, "GenerateKey must generate a P-384 key")
			s, err := wrapGenerate(priv, nil)
			assert.NoError(t, err, "wrapGenerate must accept a P-384 key")
			assert.Equal(t, s.priv, priv, "the Signer must sign with the key", assert.ByIdentity())
		})
	})

	t.Run("wrapSign", func(t *testing.T) {
		t.Parallel()

		t.Run("wraps the error of crypto/ecdsa under the prefix of the operation", func(t *testing.T) {
			t.Parallel()
			errSign := errors.New("signing failed")
			_, err := wrapSign(nil, errSign)
			assert.ErrorIs(t, err, errSign, "wrapSign must wrap the error of crypto/ecdsa")
			assert.HasPrefix(t, err.Error(), signPrefix, "the error must name the package and the operation")
		})

		t.Run("returns the signature", func(t *testing.T) {
			t.Parallel()
			got, err := wrapSign([]byte{0x01, 0x02, 0x03}, nil)
			assert.NoError(t, err, "wrapSign must accept a signature")
			assert.Equal(t, got, []byte{0x01, 0x02, 0x03}, "wrapSign must return the signature unchanged")
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
