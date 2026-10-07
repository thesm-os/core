// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tsp

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // G505: SigningCertificate names a certificate by its SHA-1 digest
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
	"slices"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

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

		t.Run("is a configuration that cache.New accepts", func(t *testing.T) {
			t.Parallel()
			_, err := cache.New(chainsConfig)
			assert.NoError(t, err, "cache.New must accept the configuration of every Verifier")
		})
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
				assert.Equal(t, c.valid(tt.give), tt.want, "valid must report whether t is within the validity")
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
				assert.Equal(t, c.identifies(tt.give), tt.want, "identifies must report whether s names the leaf")
			})
		}
	})

	t.Run("issues", func(t *testing.T) {
		t.Parallel()

		c := &chain{leaf: &x509.Certificate{RawIssuer: attrName}, serial: attrSerial}
		other := slices.Concat(
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
				give: attributes{issuerSerialV1: slices.Concat(el(der.TagSequence,
					el(der.ContextConstructed(directoryName), el(der.TagSequence))), el(der.TagInteger, attrSerial))},
			},
			{
				name: "reports false for an issuerSerial that does not parse",
				give: attributes{issuerSerialV1: el(der.TagNull)},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, c.issues(tt.give), tt.want,
					"issues must report whether each issuerSerial names the leaf")
			})
		}

		t.Run("reports false for an issuerSerial that does not parse for a leaf without an issuer", func(t *testing.T) {
			t.Parallel()
			bare := &chain{leaf: &x509.Certificate{}}
			assert.False(t, bare.issues(attributes{issuerSerialV2: el(der.TagNull)}),
				"an issuerSerial that does not parse must name no leaf")
		})
	})

	t.Run("names", func(t *testing.T) {
		t.Parallel()

		subject := el(der.TagSequence, el(der.TagSet, el(der.TagSequence,
			el(der.TagOID, []byte{0x55, 0x04, 0x03}), el(der.TagUTF8String, []byte("tsa")))))
		email := el(der.Context(1), []byte("tsa@example.com"))
		dns := el(der.Context(2), []byte("tsa.example.com"))
		usage := pkix.Extension{Id: oidExtKeyUsage, Critical: true}

		tests := []struct {
			name string
			exts []pkix.Extension
			give []byte
			want bool
		}{
			{
				name: "reports true for a directoryName of the subject of the leaf",
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
				exts: []pkix.Extension{usage, {Id: oidSubjectAltName, Value: el(der.TagSequence, email, dns)}},
				give: dns,
				want: true,
			},
			{
				name: "reports false for a name that the subjectAltName extension does not have",
				exts: []pkix.Extension{{Id: oidSubjectAltName, Value: el(der.TagSequence, email)}},
				give: dns,
			},
			{
				name: "reports false for a subjectAltName extension whose names are not DER",
				exts: []pkix.Extension{
					{Id: oidSubjectAltName, Value: el(der.TagSequence, []byte{0x82, 0x81, 0x01, 'x'})},
				},
				give: dns,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				c := &chain{leaf: &x509.Certificate{RawSubject: subject, Extensions: tt.exts}}
				assert.Equal(t, c.names(tt.give), tt.want, "names must report whether the tsa field names the leaf")
			})
		}

		t.Run("reports false for a name other than a directoryName of a leaf without a subject", func(t *testing.T) {
			t.Parallel()
			bare := &chain{leaf: &x509.Certificate{}}
			assert.False(t, bare.names(dns), "a name other than a directoryName must not match an empty subject")
		})
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
				assert.Equal(t, timeStampingOnly(tt.give), tt.want,
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
				assert.Equal(t, serialOf(tt.give), []byte{0x00, 0x80},
					"serialOf must return the content of the INTEGER")
			})
		}
	})

	t.Run("laterOf", func(t *testing.T) {
		t.Parallel()

		t.Run("returns b when b is later", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, laterOf(chainFrom, chainUntil), chainUntil, "laterOf must return the later time")
		})

		t.Run("returns a when a is later", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, laterOf(chainUntil, chainFrom), chainUntil, "laterOf must return the later time")
		})
	})

	t.Run("earlierOf", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a when a is earlier", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, earlierOf(chainFrom, chainUntil), chainFrom, "earlierOf must return the earlier time")
		})

		t.Run("returns b when b is earlier", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, earlierOf(chainUntil, chainFrom), chainFrom, "earlierOf must return the earlier time")
		})
	})

	t.Run("pool", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the certificates of the token other than the leaf", func(t *testing.T) {
			t.Parallel()
			leaf, other := selfSigned(t, "tsp leaf"), selfSigned(t, "tsp other")
			want := x509.NewCertPool()
			want.AddCert(other)
			got := (&Verifier{}).pool(slices.Concat(leaf.Raw, other.Raw), leaf.Raw)
			assert.True(t, got.Equal(want), "pool must leave the leaf out of the intermediates")
		})
	})

	t.Run("certificate", func(t *testing.T) {
		t.Parallel()

		t.Run("returns ErrCertificate for a certHash of no certificate of the token", func(t *testing.T) {
			t.Parallel()
			attrs := attributes{certHashV2: attrDigest, hashV2: digestSHA256}
			leaf, err := certificate(attrs, selfSigned(t, "tsp leaf").Raw)
			assert.ErrorIs(t, err, ErrCertificate, "certificate must refuse a certHash of no certificate")
			expect.Equal(t, err.Error(), ErrCertificate.Error()+
				": the token does not contain the certificate that it names", "the error must state the missing certificate")
			expect.Nil(t, leaf, "certificate must return no leaf with an error")
		})
	})

	// A token may contain certificates of other choices than a
	// Certificate, such as an attribute certificate, [1] IMPLICIT. The
	// element of the cases has the digest that the attribute names.
	t.Run("find", func(t *testing.T) {
		t.Parallel()

		t.Run("returns nil for an element that is not a SEQUENCE", func(t *testing.T) {
			t.Parallel()
			element := el(der.ContextConstructed(1), el(der.TagNull))
			sum := sha256.Sum256(element)
			assert.Nil(t, find(element, digestSHA256, sum[:]), "find must skip an element that is not a Certificate")
		})
	})

	t.Run("findSHA1", func(t *testing.T) {
		t.Parallel()

		t.Run("returns nil for an element that is not a SEQUENCE", func(t *testing.T) {
			t.Parallel()
			element := el(der.ContextConstructed(1), el(der.TagNull))
			sum := sha1.Sum(element) //nolint:gosec // G401: SigningCertificate names a certificate by its SHA-1 digest
			assert.Nil(t, findSHA1(element, sum[:]), "findSHA1 must skip an element that is not a Certificate")
		})
	})
}

// selfSigned returns a self-signed Ed25519 certificate whose subject has
// the common name cn. It fails tb when the certificate does not build.
func selfSigned(tb testing.TB, cn string) *x509.Certificate {
	tb.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	assert.NoError(tb, err, "GenerateKey must make a key")

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    chainFrom,
		NotAfter:     chainUntil,
	}
	raw, err := x509.CreateCertificate(rand.Reader, template, template, pub, priv)
	assert.NoError(tb, err, "CreateCertificate must make the certificate")

	cert, err := x509.ParseCertificate(raw)
	assert.NoError(tb, err, "ParseCertificate must read the certificate")

	return cert
}
