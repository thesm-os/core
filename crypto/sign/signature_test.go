// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package sign_test

import (
	"encoding/hex"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.thesmos.sh/kanon"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sign"
)

// The recorded encodings of recorded.
const (
	// signatureHex pins the kanon encoding of recorded, which a consumer
	// persists, so it never changes: field 1 is the algorithm ed25519,
	// field 2 the value 01 02 03, and field 3 the key ID 01 to 10.
	signatureHex = "0a07" + "65643235353139" +
		"1203" + "010203" +
		"1a10" + "0102030405060708090a0b0c0d0e0f10"

	// swappedHex is signatureHex with field 2 before field 1, which the
	// canonical decode rejects.
	swappedHex = "1203" + "010203" +
		"0a07" + "65643235353139" +
		"1a10" + "0102030405060708090a0b0c0d0e0f10"
)

// recorded is the signature whose encoding signatureHex pins. A test
// that changes it changes a copy.
var recorded = sign.Signature{
	Algorithm: crypto.AlgEd25519,
	Value:     []byte{1, 2, 3},
	KeyID:     sign.KeyID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
}

func TestSignature(t *testing.T) {
	t.Parallel()

	t.Run("Complete", func(t *testing.T) {
		t.Parallel()

		complete := recorded
		tests := []struct {
			name string
			give *sign.Signature
			want bool
		}{
			{name: "reports true for a signature with every field", give: &complete, want: true},
			{
				name: "reports false for a signature without an algorithm",
				give: recordedWith(func(s *sign.Signature) { s.Algorithm = "" }),
				want: false,
			},
			{
				name: "reports false for a signature with a nil value",
				give: recordedWith(func(s *sign.Signature) { s.Value = nil }),
				want: false,
			},
			{
				name: "reports false for a signature with an empty value",
				give: recordedWith(func(s *sign.Signature) { s.Value = []byte{} }),
				want: false,
			},
			{
				name: "reports false for a signature with the zero key ID",
				give: recordedWith(func(s *sign.Signature) { s.KeyID = sign.KeyID{} }),
				want: false,
			},
			{name: "reports false for the zero Signature", give: &sign.Signature{}, want: false},
			{name: "reports false for a nil Signature", give: nil, want: false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, tt.give.Complete(), tt.want, "Complete must report whether every field is set")
			})
		}
	})

	t.Run("AppendBinary", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the recorded encoding of a signature", func(t *testing.T) {
			t.Parallel()
			s := recorded
			got, err := s.AppendBinary(nil)
			assert.NoError(t, err, "AppendBinary must encode the signature")
			assert.Equal(t, hex.EncodeToString(got), signatureHex, "AppendBinary must write field 1 to 3 as recorded")
		})
	})

	t.Run("UnmarshalBinary", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the signature of the recorded encoding", func(t *testing.T) {
			t.Parallel()
			data, err := hex.DecodeString(signatureHex)
			assert.NoError(t, err, "the recorded encoding must be hexadecimal")
			var got sign.Signature
			assert.NoError(t, got.UnmarshalBinary(data), "UnmarshalBinary must decode the recorded encoding")
			assert.Equal(t, got, recorded, "UnmarshalBinary must return the recorded signature")
		})

		t.Run("returns ErrNotCanonical for the recorded fields in another order", func(t *testing.T) {
			t.Parallel()
			data, err := hex.DecodeString(swappedHex)
			assert.NoError(t, err, "the swapped encoding must be hexadecimal")
			var got sign.Signature
			assert.ErrorIs(t, got.UnmarshalBinary(data), kanon.ErrNotCanonical,
				"UnmarshalBinary must reject an encoding that the encode does not write")
		})
	})
}

// TestSignatureAllocs checks that Complete allocates nothing. MaxAllocs
// counts the allocations of the whole process, so the test does not run
// in parallel.
func TestSignatureAllocs(t *testing.T) {
	s := recorded

	t.Run("Complete", func(t *testing.T) {
		var ok bool
		expect.MaxAllocs(t, func() { ok = s.Complete() }, 0, "Complete must not allocate")
		assert.True(t, ok, "the test must measure a complete signature")
	})
}

// BenchmarkSignature reports the cost of Complete, and fails when it
// allocates.
func BenchmarkSignature(b *testing.B) {
	b.Run("Complete", func(b *testing.B) {
		s := recorded
		var ok bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			ok = s.Complete()
		}

		assert.True(b, ok, "the benchmark must measure a complete signature")
	})
}

// recordedWith returns a copy of recorded after change has changed it.
func recordedWith(change func(*sign.Signature)) *sign.Signature {
	s := recorded
	change(&s)

	return &s
}
