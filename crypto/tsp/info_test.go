// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package tsp_test

import (
	"crypto/x509/pkix"
	"encoding/asn1"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"

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
		assert.NoError(t, err, "Verify must accept the token")

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
				expect.Equal(t, value, tt.value, "Extension must return the extnValue")
				expect.Equal(t, critical, tt.critical, "Extension must report the critical flag")
				expect.Equal(t, ok, tt.ok, "Extension must report whether the token has the extension")
			})
		}

		t.Run("reports false for a token without extensions", func(t *testing.T) {
			t.Parallel()
			plain := newAuthority(t, tsptest.Config{Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512})
			info, err := newVerifier(t, plain, nil).Verify(stamp(t, plain), tsp.SHA256, imprint)
			assert.NoError(t, err, "Verify must accept the token")

			_, _, ok := info.Extension(x509OID(t, plainExtension.Id))
			assert.False(t, ok, "a token without extensions has none")
		})
	})
}

// TestInfoAllocs checks the allocation contract of Extension. MaxAllocs
// counts the allocations of the whole process, so the test does not run
// in parallel.
func TestInfoAllocs(t *testing.T) {
	a := newAuthority(t, tsptest.Config{
		Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512,
		Extensions: []pkix.Extension{criticalExtension, plainExtension},
	})
	info, err := newVerifier(t, a, nil).Verify(stamp(t, a), tsp.SHA256, imprint)
	assert.NoError(t, err, "Verify must accept the token")
	id := x509OID(t, plainExtension.Id)

	t.Run("Extension", func(t *testing.T) {
		var ok bool
		expect.MaxAllocs(t, func() { _, _, ok = info.Extension(id) }, 0, "Extension must not allocate")
		assert.True(t, ok, "the test must measure an extension that the token has")
	})
}

// BenchmarkInfo reports the cost of Extension, and fails when it
// allocates.
func BenchmarkInfo(b *testing.B) {
	b.Run("Extension", func(b *testing.B) {
		a := newAuthority(b, tsptest.Config{
			Key: tsptest.KeyEd25519, Digest: tsptest.DigestSHA512,
			Extensions: []pkix.Extension{criticalExtension, plainExtension},
		})
		info, err := newVerifier(b, a, nil).Verify(stamp(b, a), tsp.SHA256, imprint)
		assert.NoError(b, err, "Verify must accept the token")
		id := x509OID(b, plainExtension.Id)

		var ok bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			_, _, ok = info.Extension(id)
		}

		assert.True(b, ok, "the benchmark must measure an extension that the token has")
	})
}
