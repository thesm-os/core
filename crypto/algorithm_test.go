// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package crypto_test

import (
	"testing"

	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/crypto"
)

// TestAlgorithm pins the spelling of every Algorithm. Receipts, envelopes and
// signatures persist the spelling, so a change of one breaks every artefact
// that names it.
func TestAlgorithm(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		give crypto.Algorithm
		want string
	}{
		{name: "spells AlgSHA256 as sha-256", give: crypto.AlgSHA256, want: "sha-256"},
		{name: "spells AlgSHA384 as sha-384", give: crypto.AlgSHA384, want: "sha-384"},
		{name: "spells AlgSHA512 as sha-512", give: crypto.AlgSHA512, want: "sha-512"},
		{name: "spells AlgSHA3_256 as sha3-256", give: crypto.AlgSHA3_256, want: "sha3-256"},
		{name: "spells AlgSHA3_384 as sha3-384", give: crypto.AlgSHA3_384, want: "sha3-384"},
		{name: "spells AlgSHA3_512 as sha3-512", give: crypto.AlgSHA3_512, want: "sha3-512"},
		{name: "spells AlgHMACSHA256 as hmac-sha-256", give: crypto.AlgHMACSHA256, want: "hmac-sha-256"},
		{name: "spells AlgHMACSHA384 as hmac-sha-384", give: crypto.AlgHMACSHA384, want: "hmac-sha-384"},
		{name: "spells AlgHMACSHA512 as hmac-sha-512", give: crypto.AlgHMACSHA512, want: "hmac-sha-512"},
		{name: "spells AlgHMACSHA3_256 as hmac-sha3-256", give: crypto.AlgHMACSHA3_256, want: "hmac-sha3-256"},
		{name: "spells AlgHMACSHA3_384 as hmac-sha3-384", give: crypto.AlgHMACSHA3_384, want: "hmac-sha3-384"},
		{name: "spells AlgHMACSHA3_512 as hmac-sha3-512", give: crypto.AlgHMACSHA3_512, want: "hmac-sha3-512"},
		{name: "spells AlgEd25519 as ed25519", give: crypto.AlgEd25519, want: "ed25519"},
		{name: "spells AlgECDSAP384 as ecdsa-p384", give: crypto.AlgECDSAP384, want: "ecdsa-p384"},
		{name: "spells AlgMLDSA44 as ml-dsa-44", give: crypto.AlgMLDSA44, want: "ml-dsa-44"},
		{name: "spells AlgMLDSA65 as ml-dsa-65", give: crypto.AlgMLDSA65, want: "ml-dsa-65"},
		{name: "spells AlgMLDSA87 as ml-dsa-87", give: crypto.AlgMLDSA87, want: "ml-dsa-87"},
		{name: "spells AlgAES128GCM as aes-128-gcm", give: crypto.AlgAES128GCM, want: "aes-128-gcm"},
		{name: "spells AlgAES256GCM as aes-256-gcm", give: crypto.AlgAES256GCM, want: "aes-256-gcm"},
		{name: "spells AlgXAES256GCM as xaes-256-gcm", give: crypto.AlgXAES256GCM, want: "xaes-256-gcm"},
		{
			name: "spells AlgChaCha20Poly1305 as chacha20-poly1305",
			give: crypto.AlgChaCha20Poly1305,
			want: "chacha20-poly1305",
		},
		{
			name: "spells AlgXChaCha20Poly1305 as xchacha20-poly1305",
			give: crypto.AlgXChaCha20Poly1305,
			want: "xchacha20-poly1305",
		},
		{name: "spells AlgSHAKE128 as shake128", give: crypto.AlgSHAKE128, want: "shake128"},
		{name: "spells AlgSHAKE256 as shake256", give: crypto.AlgSHAKE256, want: "shake256"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, string(tt.give), tt.want, "the spelling of a persisted algorithm must not change")
		})
	}
}
