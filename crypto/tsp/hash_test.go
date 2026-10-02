// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tsp_test

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/coretest/tsptest"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/tsp"
)

func TestHash(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		give      tsp.Hash
		dotted    string
		algorithm crypto.Algorithm
		size      int
	}{
		{
			name: "SHA256 is id-sha256 with digests of 32 bytes", give: tsp.SHA256,
			dotted: "2.16.840.1.101.3.4.2.1", algorithm: crypto.AlgSHA256, size: crypto.DigestSize256,
		},
		{
			name: "SHA384 is id-sha384 with digests of 48 bytes", give: tsp.SHA384,
			dotted: "2.16.840.1.101.3.4.2.2", algorithm: crypto.AlgSHA384, size: crypto.DigestSize384,
		},
		{
			name: "SHA512 is id-sha512 with digests of 64 bytes", give: tsp.SHA512,
			dotted: "2.16.840.1.101.3.4.2.3", algorithm: crypto.AlgSHA512, size: crypto.DigestSize512,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testkit.True(t, tt.give.OID.Equal(mustOID(tt.dotted)), "the OID must be the one of RFC 5754")
			testkit.Equal(t, tt.give.Algorithm, tt.algorithm, "the Algorithm must name the hash in core")
			testkit.Equal(t, tt.give.Size, tt.size, "the Size must be the size of a digest")
		})
	}

	t.Run("a Hash of another Algorithm verifies the tokens of its OID", func(t *testing.T) {
		t.Parallel()
		a := newAuthority(t, tsptest.Config{Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512})
		v := newVerifier(t, a, nil)

		renamed := tsp.Hash{OID: tsp.SHA256.OID, Algorithm: "sha-256-of-a-suite", Size: tsp.SHA256.Size}
		_, err := v.Verify(stamp(t, a), renamed, imprint)
		testkit.NoError(t, err, "Verify must compare the hash by its OID alone")
	})
}
