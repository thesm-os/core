// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tsp

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/cache"
	"go.thesmos.sh/core/internal/der"
)

// The bounds of the validity of the chain of the cases.
var (
	chainFrom  = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	chainUntil = time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
)

func TestChain(t *testing.T) {
	t.Parallel()

	t.Run("chainsConfig", func(t *testing.T) {
		t.Parallel()
		_, err := cache.New(chainsConfig)
		testkit.NoError(t, err, "cache.New must accept the configuration of every Verifier")
	})

	t.Run("valid", func(t *testing.T) {
		t.Parallel()

		c := &chain{notBefore: chainFrom, notAfter: chainUntil}
		tests := []struct {
			name string
			give time.Time
			want bool
		}{
			{name: "reports true at the start of the validity", give: chainFrom, want: true},
			{name: "reports true at the end of the validity", give: chainUntil, want: true},
			{name: "reports false before the validity", give: chainFrom.Add(-time.Nanosecond)},
			{name: "reports false after the validity", give: chainUntil.Add(time.Nanosecond)},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(t, c.valid(tt.give), tt.want, "valid must report whether t is within the validity")
			})
		}
	})

	t.Run("identifies", func(t *testing.T) {
		t.Parallel()

		issuer := el(der.TagSequence)
		tests := []struct {
			name string
			leaf *x509.Certificate
			give signerInfo
			want bool
		}{
			{
				name: "reports true for the issuer and the serial number of the leaf",
				leaf: &x509.Certificate{RawIssuer: issuer},
				give: signerInfo{issuer: issuer, serial: []byte{0x05}},
				want: true,
			},
			{
				name: "reports false for another serial number",
				leaf: &x509.Certificate{RawIssuer: issuer},
				give: signerInfo{issuer: issuer, serial: []byte{0x06}},
			},
			{
				name: "reports false for another issuer",
				leaf: &x509.Certificate{RawIssuer: issuer},
				give: signerInfo{issuer: el(der.TagSet), serial: []byte{0x05}},
			},
			{
				name: "reports true for the subject key identifier of the leaf",
				leaf: &x509.Certificate{SubjectKeyId: []byte{1, 2}},
				give: signerInfo{keyID: []byte{1, 2}},
				want: true,
			},
			{
				name: "reports false for another subject key identifier",
				leaf: &x509.Certificate{SubjectKeyId: []byte{1, 2}},
				give: signerInfo{keyID: []byte{1, 3}},
			},
			{
				name: "reports false for an empty identifier of a leaf without one",
				leaf: &x509.Certificate{},
				give: signerInfo{keyID: []byte{}},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				c := &chain{leaf: tt.leaf, serial: []byte{0x05}}
				testkit.Equal(t, c.identifies(tt.give), tt.want, "identifies must report whether s names the leaf")
			})
		}
	})

	t.Run("issues", func(t *testing.T) {
		t.Parallel()

		c := &chain{leaf: &x509.Certificate{RawIssuer: attrName}, serial: attrSerial}
		other := cat(
			el(der.TagSequence, el(der.ContextConstructed(directoryName), attrName)),
			el(der.TagInteger, []byte{0x0a}),
		)
		tests := []struct {
			name string
			give attributes
			want bool
		}{
			{name: "reports true for attributes without an issuerSerial", want: true},
			{
				name: "reports true for an issuerSerial of the leaf",
				give: attributes{issuerSerialV2: attrIssuerSerial},
				want: true,
			},
			{
				name: "reports true for both issuerSerials of the leaf",
				give: attributes{issuerSerialV2: attrIssuerSerial, issuerSerialV1: attrIssuerSerial},
				want: true,
			},
			{
				name: "reports false for an issuerSerial of another serial number",
				give: attributes{issuerSerialV2: other},
			},
			{
				name: "reports false for an issuerSerial of another issuer",
				give: attributes{issuerSerialV1: cat(el(der.TagSequence, el(der.ContextConstructed(directoryName),
					el(der.TagSequence))), el(der.TagInteger, attrSerial))},
			},
			{
				name: "reports false for an issuerSerial that does not parse",
				give: attributes{issuerSerialV1: el(der.TagNull)},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(
					t,
					c.issues(tt.give),
					tt.want,
					"issues must report whether each issuerSerial names the leaf",
				)
			})
		}
	})

	t.Run("names", func(t *testing.T) {
		t.Parallel()

		subject := el(der.TagSequence, el(der.TagSet, el(der.TagSequence,
			el(der.TagOID, []byte{0x55, 0x04, 0x03}), el(der.TagUTF8String, []byte("tsa")))))
		email := el(der.Context(1), []byte("tsa@example.com"))
		dns := el(der.Context(2), []byte("tsa.example.com"))
		san := func(names ...[]byte) pkix.Extension {
			return pkix.Extension{Id: oidSubjectAltName, Value: el(der.TagSequence, names...)}
		}
		usage := pkix.Extension{Id: oidExtKeyUsage, Critical: true}

		tests := []struct {
			name string
			exts []pkix.Extension
			give []byte
			want bool
		}{
			{
				name: "reports true for a directoryName of the leaf's subject",
				give: el(der.ContextConstructed(directoryName), subject),
				want: true,
			},
			{
				name: "reports false for a directoryName of another subject",
				give: el(der.ContextConstructed(directoryName), el(der.TagSequence)),
			},
			{
				name: "reports false for the subject in a name other than a directoryName",
				give: el(der.ContextConstructed(1), subject),
			},
			{
				name: "reports true for a name of the subjectAltName extension",
				exts: []pkix.Extension{usage, san(email, dns)},
				give: dns,
				want: true,
			},
			{
				name: "reports false for a name that the subjectAltName extension does not have",
				exts: []pkix.Extension{san(email)},
				give: dns,
			},
			{
				name: "reports false for a subjectAltName extension whose names are not DER",
				exts: []pkix.Extension{san([]byte{0x82, 0x81, 0x01, 'x'})},
				give: dns,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				c := &chain{leaf: &x509.Certificate{RawSubject: subject, Extensions: tt.exts}}
				testkit.Equal(t, c.names(tt.give), tt.want, "names must report whether the tsa field names the leaf")
			})
		}
	})

	t.Run("timeStampingOnly", func(t *testing.T) {
		t.Parallel()

		critical := []pkix.Extension{{Id: oidExtKeyUsage, Critical: true}}
		tests := []struct {
			name string
			give *x509.Certificate
			want bool
		}{
			{
				name: "reports true for one critical id-kp-timeStamping",
				give: &x509.Certificate{
					ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageTimeStamping},
					Extensions:  critical,
				},
				want: true,
			},
			{
				name: "reports false for an extension that is not critical",
				give: &x509.Certificate{
					ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageTimeStamping},
					Extensions:  []pkix.Extension{{Id: oidExtKeyUsage}},
				},
			},
			{
				name: "reports false for one usage other than id-kp-timeStamping",
				give: &x509.Certificate{
					ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
					Extensions:  critical,
				},
			},
			{
				name: "reports false for a second usage",
				give: &x509.Certificate{
					ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageTimeStamping, x509.ExtKeyUsageServerAuth},
					Extensions:  critical,
				},
			},
			{
				name: "reports false for a usage that crypto/x509 does not know",
				give: &x509.Certificate{
					ExtKeyUsage:        []x509.ExtKeyUsage{x509.ExtKeyUsageTimeStamping},
					UnknownExtKeyUsage: []asn1.ObjectIdentifier{{1, 2, 3}},
					Extensions:         critical,
				},
			},
			{
				name: "reports false for a certificate without the extension",
				give: &x509.Certificate{ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageTimeStamping}},
			},
			{name: "reports false for a certificate without an extended key usage", give: &x509.Certificate{}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(t, timeStampingOnly(tt.give), tt.want,
					"timeStampingOnly must report whether the certificate has one critical id-kp-timeStamping")
			})
		}
	})

	t.Run("serialOf", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give []byte
		}{
			{
				name: "returns the serialNumber after the version",
				give: el(der.TagSequence, el(der.ContextConstructed(0), el(der.TagInteger, []byte{2})),
					el(der.TagInteger, []byte{0x00, 0x80}), el(der.TagSequence)),
			},
			{
				name: "returns the serialNumber of a certificate without a version",
				give: el(der.TagSequence, el(der.TagInteger, []byte{0x00, 0x80}), el(der.TagSequence)),
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(
					t,
					serialOf(tt.give),
					[]byte{0x00, 0x80},
					"serialOf must return the content of the INTEGER",
				)
			})
		}
	})

	t.Run("laterOf", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the later time in either order", func(t *testing.T) {
			t.Parallel()
			testkit.Equal(t, laterOf(chainFrom, chainUntil), chainUntil, "laterOf must return b when b is later")
			testkit.Equal(t, laterOf(chainUntil, chainFrom), chainUntil, "laterOf must return a when a is later")
		})
	})

	t.Run("earlierOf", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the earlier time in either order", func(t *testing.T) {
			t.Parallel()
			testkit.Equal(t, earlierOf(chainFrom, chainUntil), chainFrom, "earlierOf must return a when a is earlier")
			testkit.Equal(t, earlierOf(chainUntil, chainFrom), chainFrom, "earlierOf must return b when b is earlier")
		})
	})
}
