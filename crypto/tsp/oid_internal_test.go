// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tsp

import (
	"crypto/x509"
	"testing"

	"go.thesmos.sh/testkit"
)

func TestOIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		got    []byte
		dotted string
	}{
		{name: "oidSignedData is id-signedData", got: oidSignedData, dotted: "1.2.840.113549.1.7.2"},
		{name: "oidTSTInfo is id-ct-TSTInfo", got: oidTSTInfo, dotted: "1.2.840.113549.1.9.16.1.4"},
		{name: "oidContentType is id-contentType", got: oidContentType, dotted: "1.2.840.113549.1.9.3"},
		{name: "oidMessageDigest is id-messageDigest", got: oidMessageDigest, dotted: "1.2.840.113549.1.9.4"},
		{
			name:   "oidSigningCertificate is id-aa-signingCertificate",
			got:    oidSigningCertificate,
			dotted: "1.2.840.113549.1.9.16.2.12",
		},
		{
			name:   "oidSigningCertificateV2 is id-aa-signingCertificateV2",
			got:    oidSigningCertificateV2,
			dotted: "1.2.840.113549.1.9.16.2.47",
		},
		{name: "oidSHA256 is id-sha256", got: oidSHA256, dotted: "2.16.840.1.101.3.4.2.1"},
		{name: "oidSHA384 is id-sha384", got: oidSHA384, dotted: "2.16.840.1.101.3.4.2.2"},
		{name: "oidSHA512 is id-sha512", got: oidSHA512, dotted: "2.16.840.1.101.3.4.2.3"},
		{name: "oidRSAEncryption is rsaEncryption", got: oidRSAEncryption, dotted: "1.2.840.113549.1.1.1"},
		{name: "oidRSAPSS is id-RSASSA-PSS", got: oidRSAPSS, dotted: "1.2.840.113549.1.1.10"},
		{name: "oidMGF1 is id-mgf1", got: oidMGF1, dotted: "1.2.840.113549.1.1.8"},
		{name: "oidSHA256WithRSA is sha256WithRSAEncryption", got: oidSHA256WithRSA, dotted: "1.2.840.113549.1.1.11"},
		{name: "oidSHA384WithRSA is sha384WithRSAEncryption", got: oidSHA384WithRSA, dotted: "1.2.840.113549.1.1.12"},
		{name: "oidSHA512WithRSA is sha512WithRSAEncryption", got: oidSHA512WithRSA, dotted: "1.2.840.113549.1.1.13"},
		{name: "oidECDSAWithSHA256 is ecdsa-with-SHA256", got: oidECDSAWithSHA256, dotted: "1.2.840.10045.4.3.2"},
		{name: "oidECDSAWithSHA384 is ecdsa-with-SHA384", got: oidECDSAWithSHA384, dotted: "1.2.840.10045.4.3.3"},
		{name: "oidECDSAWithSHA512 is ecdsa-with-SHA512", got: oidECDSAWithSHA512, dotted: "1.2.840.10045.4.3.4"},
		{name: "oidEd25519 is id-Ed25519", got: oidEd25519, dotted: "1.3.101.112"},
		{name: "oidMLDSA44 is id-ml-dsa-44", got: oidMLDSA44, dotted: "2.16.840.1.101.3.4.3.17"},
		{name: "oidMLDSA65 is id-ml-dsa-65", got: oidMLDSA65, dotted: "2.16.840.1.101.3.4.3.18"},
		{name: "oidMLDSA87 is id-ml-dsa-87", got: oidMLDSA87, dotted: "2.16.840.1.101.3.4.3.19"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			oid, err := x509.ParseOID(tt.dotted)
			testkit.NoError(t, err, "the dotted form must parse")
			want, err := oid.AppendBinary(nil)
			testkit.NoError(t, err, "the OID must encode")
			testkit.Equal(t, tt.got, want, "the content must be the DER of the dotted form")
		})
	}
}
