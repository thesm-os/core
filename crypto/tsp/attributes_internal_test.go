// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tsp

import (
	"bytes"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/internal/der"
)

// The values of the attributes of the cases.
var (
	// attrDigest is the message digest, and the certHash of SHA-256.
	attrDigest = bytes.Repeat([]byte{0x11}, 32)

	// attrSHA1 is a certHash of SHA-1, and attrSHA384 one of SHA-384.
	attrSHA1   = bytes.Repeat([]byte{0x22}, 20)
	attrSHA384 = bytes.Repeat([]byte{0x33}, 48)

	// attrName is the DER of an issuer Name, and attrSerial the content of
	// a serialNumber.
	attrName = el(der.TagSequence, el(der.TagSet, el(der.TagSequence, el(der.TagOID, []byte{0x55, 0x04, 0x03}),
		el(der.TagUTF8String, []byte("tsp issuer")))))
	attrSerial = []byte{0x09}

	// attrIssuerSerial is the content of an IssuerSerial of attrName and
	// attrSerial.
	attrIssuerSerial = cat(el(der.TagSequence, el(der.ContextConstructed(directoryName), attrName)),
		el(der.TagInteger, attrSerial))

	// attrContentType and attrMessageDigest are the two attributes that
	// every SignerInfo has.
	attrContentType   = attribute(oidContentType, el(der.TagOID, oidTSTInfo))
	attrMessageDigest = attribute(oidMessageDigest, el(der.TagOctetString, attrDigest))
)

func TestAttributes(t *testing.T) {
	t.Parallel()

	t.Run("parseAttributes", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the content type and the message digest", func(t *testing.T) {
			t.Parallel()
			a, ok := parseAttributes(cat(attrContentType, attrMessageDigest))
			testkit.True(t, ok, "parseAttributes must accept the attributes")
			testkit.Equal(t, a.contentType, oidTSTInfo, "contentType must be the content of the OID")
			testkit.Equal(t, a.messageDigest, attrDigest, "messageDigest must be the content of the OCTET STRING")
			testkit.True(t, a.certHashV2 == nil && a.certHashV1 == nil, "absent identifiers must be nil")
		})

		t.Run("returns the identifiers of both signing-certificate attributes", func(t *testing.T) {
			t.Parallel()
			a, ok := parseAttributes(cat(attrContentType, attrMessageDigest,
				attribute(oidSigningCertificateV2,
					signingCertificateOf(essCertID(nil, attrDigest, el(der.TagSequence, attrIssuerSerial)))),
				attribute(oidSigningCertificate, signingCertificateOf(essCertID(nil, attrSHA1, nil)))))
			testkit.True(t, ok, "parseAttributes must accept both attributes")
			testkit.Equal(t, a.hashV2, digestSHA256, "hashV2 must default to SHA-256")
			testkit.Equal(t, a.certHashV2, attrDigest, "certHashV2 must be the certHash of the ESSCertIDv2")
			testkit.Equal(t, a.issuerSerialV2, attrIssuerSerial, "issuerSerialV2 must be the content of issuerSerial")
			testkit.Equal(t, a.certHashV1, attrSHA1, "certHashV1 must be the certHash of the ESSCertID")
			testkit.True(t, a.issuerSerialV1 == nil, "issuerSerialV1 must be nil when the ESSCertID has none")
		})

		t.Run("reads past an attribute of another type", func(t *testing.T) {
			t.Parallel()
			_, ok := parseAttributes(cat(attrContentType, attribute(oidUnknown, el(der.TagNull), el(der.TagNull)),
				attrMessageDigest))
			testkit.True(t, ok, "parseAttributes must accept an attribute of another type")
		})

		v2 := attribute(oidSigningCertificateV2, signingCertificateOf(essCertID(nil, attrDigest, nil)))
		v1 := attribute(oidSigningCertificate, signingCertificateOf(essCertID(nil, attrSHA1, nil)))

		t.Run("returns the attributes of one SigningCertificateV2 and one SigningCertificate", func(t *testing.T) {
			t.Parallel()
			_, ok := parseAttributes(cat(attrContentType, attrMessageDigest, v2, v1))
			testkit.True(t, ok, "parseAttributes must accept one attribute of each version")
		})
		tests := []struct {
			name string
			give []byte
		}{
			{
				name: "reports false for an attribute that is not a SEQUENCE",
				give: cat(attrContentType, attrMessageDigest, el(der.TagSet)),
			},
			{
				name: "reports false for an attribute without a type",
				give: cat(attrContentType, attrMessageDigest, el(der.TagSequence, el(der.TagSet))),
			},
			{
				name: "reports false for a type that is not an OID in DER",
				give: cat(attrContentType, attrMessageDigest,
					el(der.TagSequence, el(der.TagOID, []byte{0x80, 0x01}), el(der.TagSet))),
			},
			{
				name: "reports false for an attribute without values",
				give: cat(attrContentType, attrMessageDigest, el(der.TagSequence, el(der.TagOID, oidUnknown))),
			},
			{
				name: "reports false for values that are not a SET",
				give: cat(attrContentType, attrMessageDigest,
					el(der.TagSequence, el(der.TagOID, oidUnknown), el(der.TagSequence))),
			},
			{
				name: "reports false for an element after the values",
				give: cat(attrContentType, attrMessageDigest,
					el(der.TagSequence, el(der.TagOID, oidUnknown), el(der.TagSet), el(der.TagNull))),
			},
			{name: "reports false for attributes without a content type", give: attrMessageDigest},
			{name: "reports false for attributes without a message digest", give: attrContentType},
			{
				name: "reports false for two content types",
				give: cat(attrContentType, attrContentType, attrMessageDigest),
			},
			{
				name: "reports false for two message digests",
				give: cat(attrContentType, attrMessageDigest, attrMessageDigest),
			},
			{
				name: "reports false for a content type of two values",
				give: cat(
					attribute(oidContentType, el(der.TagOID, oidTSTInfo), el(der.TagOID, oidTSTInfo)),
					attrMessageDigest,
				),
			},
			{
				name: "reports false for a content type that is not an OID",
				give: cat(attribute(oidContentType, el(der.TagOctetString, oidTSTInfo)), attrMessageDigest),
			},
			{
				name: "reports false for a message digest that is not an OCTET STRING",
				give: cat(attrContentType, attribute(oidMessageDigest, el(der.TagOID, attrDigest))),
			},
			{
				name: "reports false for two SigningCertificateV2 attributes",
				give: cat(attrContentType, attrMessageDigest, v2, v2),
			},
			{
				name: "reports false for two SigningCertificate attributes",
				give: cat(attrContentType, attrMessageDigest, v1, v1),
			},
			{
				name: "reports false for a SigningCertificateV2 that does not parse",
				give: cat(attrContentType, attrMessageDigest, attribute(oidSigningCertificateV2, el(der.TagNull))),
			},
			{
				name: "reports false for a SigningCertificate that does not parse",
				give: cat(attrContentType, attrMessageDigest, attribute(oidSigningCertificate, el(der.TagNull))),
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, ok := parseAttributes(tt.give)
				testkit.False(t, ok, "parseAttributes must refuse the attributes")
			})
		}
	})

	t.Run("parseSigningCertificate", func(t *testing.T) {
		t.Parallel()

		accepted := []struct {
			name         string
			give         []byte
			v2           bool
			hash         digest
			certHash     []byte
			issuerSerial []byte
		}{
			{
				name:     "returns SHA-256 for an ESSCertIDv2 without a hashAlgorithm",
				give:     signingCertificate(essCertID(nil, attrDigest, nil)),
				v2:       true,
				hash:     digestSHA256,
				certHash: attrDigest,
			},
			{
				name: "returns the hashAlgorithm of an ESSCertIDv2",
				give: signingCertificate(
					essCertID(el(der.TagSequence, el(der.TagOID, oidSHA384)), attrSHA384, nil),
				),
				v2:       true,
				hash:     digestSHA384,
				certHash: attrSHA384,
			},
			{
				name:     "returns the zero digest for a hash that this package does not compute",
				give:     signingCertificate(essCertID(el(der.TagSequence, el(der.TagOID, oidUnknown)), attrSHA1, nil)),
				v2:       true,
				certHash: attrSHA1,
			},
			{
				name:         "returns the issuerSerial of an ESSCertIDv2",
				give:         signingCertificate(essCertID(nil, attrDigest, el(der.TagSequence, attrIssuerSerial))),
				v2:           true,
				hash:         digestSHA256,
				certHash:     attrDigest,
				issuerSerial: attrIssuerSerial,
			},
			{
				name:     "returns the SHA-1 certHash of an ESSCertID",
				give:     signingCertificate(essCertID(nil, attrSHA1, nil)),
				certHash: attrSHA1,
			},
			{
				name: "returns the first identifier and reads past the later ones and the policies",
				give: el(der.TagSet, el(
					der.TagSequence,
					el(
						der.TagSequence,
						essCertID(nil, attrSHA1, nil),
						essCertID(nil, bytes.Repeat([]byte{0x44}, 20), nil),
					),
					el(der.TagSequence),
				)),
				certHash: attrSHA1,
			},
		}
		for _, tt := range accepted {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				hash, certHash, issuerSerial, ok := parseSigningCertificate(content(tt.give), tt.v2)
				testkit.True(t, ok, "parseSigningCertificate must accept the attribute value")
				testkit.Equal(t, hash, tt.hash, "hash must be the digest of the identifier")
				testkit.Equal(t, certHash, tt.certHash, "certHash must be the content of the OCTET STRING")
				testkit.Equal(t, issuerSerial, tt.issuerSerial, "issuerSerial must be the content of the IssuerSerial")
			})
		}

		refused := []struct {
			name string
			give []byte
			v2   bool
		}{
			{
				name: "reports false for two values",
				give: el(der.TagSet, signingCertificateOf(essCertID(nil, attrSHA1, nil)),
					signingCertificateOf(essCertID(nil, attrSHA1, nil))),
			},
			{name: "reports false for a value that is not a SEQUENCE", give: el(der.TagSet, el(der.TagSet))},
			{name: "reports false for a value without certs", give: el(der.TagSet, el(der.TagSequence))},
			{
				name: "reports false for certs that are not a SEQUENCE",
				give: el(der.TagSet, el(der.TagSequence, el(der.TagSet, essCertID(nil, attrSHA1, nil)))),
			},
			{
				name: "reports false for policies that are not DER",
				give: el(der.TagSet, el(der.TagSequence, el(der.TagSequence, essCertID(nil, attrSHA1, nil)),
					cat([]byte{byte(der.TagSequence)}, notDER))),
			},
			{
				name: "reports false for an element after the policies",
				give: el(der.TagSet, el(der.TagSequence, el(der.TagSequence, essCertID(nil, attrSHA1, nil)),
					el(der.TagSequence), el(der.TagNull))),
			},
			{
				name: "reports false for certs without an identifier",
				give: el(der.TagSet, el(der.TagSequence, el(der.TagSequence))),
			},
			{
				name: "reports false for an identifier that is not a SEQUENCE",
				give: el(der.TagSet, el(der.TagSequence, el(der.TagSequence, el(der.TagSet)))),
			},
			{
				name: "reports false for a hashAlgorithm that is not DER",
				give: signingCertificate(el(der.TagSequence, cat([]byte{byte(der.TagSequence)}, notDER),
					el(der.TagOctetString, attrDigest))),
				v2: true,
			},
			{
				name: "reports false for a hashAlgorithm without an OID",
				give: signingCertificate(essCertID(el(der.TagSequence, el(der.TagNull)), attrDigest, nil)),
				v2:   true,
			},
			{
				name: "reports false for a certHash of another size than its hash",
				give: signingCertificate(essCertID(el(der.TagSequence, el(der.TagOID, oidSHA384)), attrDigest, nil)),
				v2:   true,
			},
			{
				name: "reports false for a certHash of another size than SHA-256 without a hashAlgorithm",
				give: signingCertificate(essCertID(nil, attrSHA1, nil)),
				v2:   true,
			},
			{
				name: "reports false for a SHA-1 certHash of another size",
				give: signingCertificate(essCertID(nil, attrDigest, nil)),
			},
			{name: "reports false for an identifier without a certHash", give: signingCertificate(el(der.TagSequence))},
			{
				name: "reports false for an issuerSerial that is not DER",
				give: signingCertificate(el(der.TagSequence, el(der.TagOctetString, attrSHA1),
					cat([]byte{byte(der.TagSequence)}, notDER))),
			},
			{
				name: "reports false for an element after the issuerSerial",
				give: signingCertificate(el(der.TagSequence, el(der.TagOctetString, attrSHA1), el(der.TagSequence),
					el(der.TagNull))),
			},
		}
		for _, tt := range refused {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, _, _, ok := parseSigningCertificate(content(tt.give), tt.v2)
				testkit.False(t, ok, "parseSigningCertificate must refuse the attribute value")
			})
		}
	})

	t.Run("parseIssuerSerial", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the Name of the directoryName and the serialNumber", func(t *testing.T) {
			t.Parallel()
			issuer, serial, ok := parseIssuerSerial(attrIssuerSerial)
			testkit.True(t, ok, "parseIssuerSerial must accept the IssuerSerial")
			testkit.Equal(t, issuer, attrName, "issuer must be the DER of the Name")
			testkit.Equal(t, serial, attrSerial, "serial must be the content of the serialNumber")
		})

		names := el(der.TagSequence, el(der.ContextConstructed(directoryName), attrName))
		tests := []struct {
			name string
			give []byte
		}{
			{name: "reports false for an IssuerSerial without an issuer", give: el(der.TagInteger, attrSerial)},
			{name: "reports false for an IssuerSerial without a serialNumber", give: names},
			{
				name: "reports false for a serialNumber that is not an INTEGER in DER",
				give: cat(names, el(der.TagInteger, []byte{0x00, 0x09})),
			},
			{
				name: "reports false for an element after the serialNumber",
				give: cat(names, el(der.TagInteger, attrSerial), el(der.TagNull)),
			},
			{
				name: "reports false for two names",
				give: cat(el(der.TagSequence, el(der.ContextConstructed(directoryName), attrName),
					el(der.ContextConstructed(directoryName), attrName)), el(der.TagInteger, attrSerial)),
			},
			{
				name: "reports false for a name of another choice",
				give: cat(
					el(der.TagSequence, el(der.Context(1), []byte("tsa@example.com"))),
					el(der.TagInteger, attrSerial),
				),
			},
			{
				name: "reports false for a directoryName that is not a Name",
				give: cat(el(der.TagSequence, el(der.ContextConstructed(directoryName), el(der.TagSet))),
					el(der.TagInteger, attrSerial)),
			},
			{
				name: "reports false for an element after the Name",
				give: cat(el(der.TagSequence, el(der.ContextConstructed(directoryName), attrName, el(der.TagNull))),
					el(der.TagInteger, attrSerial)),
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, _, ok := parseIssuerSerial(tt.give)
				testkit.False(t, ok, "parseIssuerSerial must refuse the IssuerSerial")
			})
		}
	})
}

// attribute returns the DER of an Attribute of the type whose OID has the
// content typ, with the elements values.
func attribute(typ []byte, values ...[]byte) []byte {
	return el(der.TagSequence, el(der.TagOID, typ), el(der.TagSet, values...))
}

// essCertID returns the DER of an ESSCertID or ESSCertIDv2 of the
// hashAlgorithm element alg, none when nil, certHash, and the issuerSerial
// element issuerSerial, none when nil.
func essCertID(alg, certHash, issuerSerial []byte) []byte {
	return el(der.TagSequence, alg, el(der.TagOctetString, certHash), issuerSerial)
}

// signingCertificateOf returns the DER of a SigningCertificate or
// SigningCertificateV2 of the one identifier id.
func signingCertificateOf(id []byte) []byte {
	return el(der.TagSequence, el(der.TagSequence, id))
}

// signingCertificate returns the DER of the attrValues SET of a
// SigningCertificate or SigningCertificateV2 whose one identifier is the
// element id.
func signingCertificate(id []byte) []byte {
	return el(der.TagSet, signingCertificateOf(id))
}

// content returns the content of the element b.
func content(b []byte) []byte {
	r := der.NewReader(b)
	_, _, c, _ := r.Next()

	return c
}
