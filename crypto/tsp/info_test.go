// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tsp_test

import (
	"crypto/x509/pkix"
	"encoding/asn1"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/coretest/tsptest"
	"go.thesmos.sh/core/crypto/tsp"
)

// The extensions of the tokens of TestInfo: one critical, one not, and
// one that no token has.
var (
	criticalExtension = pkix.Extension{
		Id:       asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 99999, 9, 1},
		Critical: true,
		Value:    []byte("critical"),
	}
	plainExtension  = pkix.Extension{Id: asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 99999, 9, 2}, Value: []byte("plain")}
	absentExtension = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 99999, 9, 3}
)

func TestInfo(t *testing.T) {
	t.Parallel()

	t.Run("Extension", func(t *testing.T) {
		t.Parallel()

		a := newAuthority(t, tsptest.Config{
			Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512,
			Extensions: []pkix.Extension{criticalExtension, plainExtension},
		})
		info, err := newVerifier(t, a, nil).Verify(stamp(t, a), tsp.SHA256, imprint)
		testkit.NoError(t, err, "Verify must accept the token")

		tests := []struct {
			name         string
			id           asn1.ObjectIdentifier
			value        []byte
			critical, ok bool
		}{
			{
				name:     "returns the value of a critical extension",
				id:       criticalExtension.Id,
				value:    criticalExtension.Value,
				critical: true,
				ok:       true,
			},
			{
				name:  "returns the value of an extension that is not critical",
				id:    plainExtension.Id,
				value: plainExtension.Value,
				ok:    true,
			},
			{name: "reports false for an extension that the token lacks", id: absentExtension},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				value, critical, ok := info.Extension(x509OID(t, tt.id))
				testkit.Equal(t, value, tt.value, "Extension must return the extnValue")
				testkit.Equal(t, critical, tt.critical, "Extension must report the critical flag")
				testkit.Equal(t, ok, tt.ok, "Extension must report whether the token has the extension")
			})
		}

		t.Run("reports false for a token without extensions", func(t *testing.T) {
			t.Parallel()
			plain := newAuthority(t, tsptest.Config{Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512})
			info, err := newVerifier(t, plain, nil).Verify(stamp(t, plain), tsp.SHA256, imprint)
			testkit.NoError(t, err, "Verify must accept the token")

			_, _, ok := info.Extension(x509OID(t, plainExtension.Id))
			testkit.False(t, ok, "a token without extensions has none")
		})
	})
}

func BenchmarkInfo(b *testing.B) {
	b.Run("Extension", func(b *testing.B) {
		a := newAuthority(b, tsptest.Config{
			Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512,
			Extensions: []pkix.Extension{criticalExtension, plainExtension},
		})
		info, err := newVerifier(b, a, nil).Verify(stamp(b, a), tsp.SHA256, imprint)
		testkit.NoError(b, err, "Verify must accept the token")

		id := x509OID(b, plainExtension.Id)

		var ok bool
		tspAllocs(b, 0, func() { sinkBytes, _, ok = info.Extension(id) })
		testkit.True(b, ok, "the benchmark must measure an extension that the token has")
	})
}
