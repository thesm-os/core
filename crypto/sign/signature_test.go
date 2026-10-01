// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package sign_test

import (
	"encoding/hex"
	"testing"

	"go.thesmos.sh/kanon"
	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sign"
)

// The recorded encodings of recordedSignature.
const (
	// signatureHex pins the kanon encoding of recordedSignature, which a
	// consumer persists, so it never changes: field 1 is the algorithm
	// ed25519, field 2 the value 01 02 03, and field 3 the key ID 01 to 10.
	signatureHex = "0a07" + "65643235353139" +
		"1203" + "010203" +
		"1a10" + "0102030405060708090a0b0c0d0e0f10"

	// swappedHex is signatureHex with field 2 before field 1, which the
	// canonical decode rejects.
	swappedHex = "1203" + "010203" +
		"0a07" + "65643235353139" +
		"1a10" + "0102030405060708090a0b0c0d0e0f10"
)

// recordedSignature is the signature whose encoding signatureHex pins.
func recordedSignature() sign.Signature {
	return sign.Signature{
		Algorithm: crypto.AlgEd25519,
		Value:     []byte{1, 2, 3},
		KeyID:     sign.KeyID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
	}
}

func TestSignature(t *testing.T) {
	t.Parallel()

	t.Run("AppendBinary", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the recorded encoding of a signature", func(t *testing.T) {
			t.Parallel()
			s := recordedSignature()
			got, err := s.AppendBinary(nil)
			testkit.NoError(t, err, "AppendBinary must encode the signature")
			testkit.Equal(t, hex.EncodeToString(got), signatureHex,
				"AppendBinary must write field 1 to 3 as recorded")
		})
	})

	t.Run("UnmarshalBinary", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the signature of the recorded encoding", func(t *testing.T) {
			t.Parallel()
			data, err := hex.DecodeString(signatureHex)
			testkit.NoError(t, err, "the recorded encoding must be hexadecimal")
			var got sign.Signature
			testkit.NoError(t, got.UnmarshalBinary(data), "UnmarshalBinary must decode the recorded encoding")
			testkit.Equal(t, got, recordedSignature(), "UnmarshalBinary must return the recorded signature")
		})

		t.Run("returns ErrNotCanonical for the recorded fields in another order", func(t *testing.T) {
			t.Parallel()
			data, err := hex.DecodeString(swappedHex)
			testkit.NoError(t, err, "the swapped encoding must be hexadecimal")
			var got sign.Signature
			testkit.ErrorIs(t, got.UnmarshalBinary(data), kanon.ErrNotCanonical,
				"UnmarshalBinary must reject an encoding that the encode does not write")
		})
	})
}
