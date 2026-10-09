// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package tsp

import (
	"bytes"
	"slices"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

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
	attrIssuerSerial = slices.Concat(el(der.TagSequence, el(der.ContextConstructed(directoryName), attrName)),
		el(der.TagInteger, attrSerial))

	// attrContentType and attrMessageDigest are the two attributes that
	// every SignerInfo has.
	attrContentType = el(der.TagSequence, el(der.TagOID, oidContentType),
		el(der.TagSet, el(der.TagOID, oidTSTInfo)))
	attrMessageDigest = el(der.TagSequence, el(der.TagOID, oidMessageDigest),
		el(der.TagSet, el(der.TagOctetString, attrDigest)))

	// attrV2 is a SigningCertificateV2 of one ESSCertIDv2 of attrDigest, and
	// attrV1 a SigningCertificate of one ESSCertID of attrSHA1.
	attrV2 = el(der.TagSequence, el(der.TagOID, oidSigningCertificateV2), el(der.TagSet, el(der.TagSequence,
		el(der.TagSequence, el(der.TagSequence, el(der.TagOctetString, attrDigest))))))
	attrV1 = el(der.TagSequence, el(der.TagOID, oidSigningCertificate), el(der.TagSet, el(der.TagSequence,
		el(der.TagSequence, el(der.TagSequence, el(der.TagOctetString, attrSHA1))))))
)

func TestAttributes(t *testing.T) {
	t.Parallel()

	t.Run("parseAttributes", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the content type and the message digest", func(t *testing.T) {
			t.Parallel()
			a, ok := parseAttributes(slices.Concat(attrContentType, attrMessageDigest))
			assert.True(t, ok, "parseAttributes must accept the attributes")
			expect.Equal(t, a.contentType, oidTSTInfo, "contentType must be the content of the OID")
			expect.Equal(t, a.messageDigest, attrDigest, "messageDigest must be the content of the OCTET STRING")
			expect.Nil(t, a.certHashV2, "an absent SigningCertificateV2 must leave certHashV2 nil")
			expect.Nil(t, a.certHashV1, "an absent SigningCertificate must leave certHashV1 nil")
		})

		t.Run("returns the identifiers of both signing-certificate attributes", func(t *testing.T) {
			t.Parallel()
			v2 := el(der.TagSequence, el(der.TagOID, oidSigningCertificateV2), el(der.TagSet, el(der.TagSequence,
				el(der.TagSequence, el(der.TagSequence, el(der.TagOctetString, attrDigest),
					el(der.TagSequence, attrIssuerSerial))))))
			a, ok := parseAttributes(slices.Concat(attrContentType, attrMessageDigest, v2, attrV1))
			assert.True(t, ok, "parseAttributes must accept both attributes")
			expect.Equal(t, a.hashV2, digestSHA256, "hashV2 must default to SHA-256")
			expect.Equal(t, a.certHashV2, attrDigest, "certHashV2 must be the certHash of the ESSCertIDv2")
			expect.Equal(t, a.issuerSerialV2, attrIssuerSerial, "issuerSerialV2 must be the content of issuerSerial")
			expect.Equal(t, a.certHashV1, attrSHA1, "certHashV1 must be the certHash of the ESSCertID")
			expect.Nil(t, a.issuerSerialV1, "issuerSerialV1 must be nil when the ESSCertID has none")
		})

		t.Run("reads past an attribute of another type", func(t *testing.T) {
			t.Parallel()
			other := el(der.TagSequence, el(der.TagOID, oidUnknown), el(der.TagSet, el(der.TagNull), el(der.TagNull)))
			_, ok := parseAttributes(slices.Concat(attrContentType, other, attrMessageDigest))
			assert.True(t, ok, "parseAttributes must accept an attribute of another type")
		})

		t.Run("returns the attributes of one SigningCertificateV2 beside one SigningCertificate", func(t *testing.T) {
			t.Parallel()
			_, ok := parseAttributes(slices.Concat(attrContentType, attrMessageDigest, attrV2, attrV1))
			assert.True(t, ok, "parseAttributes must accept one attribute of each version")
		})

		// v1Value is the one value of the SET of attrV1.
		v1Value := el(der.TagSequence, el(der.TagSequence, el(der.TagSequence, el(der.TagOctetString, attrSHA1))))
		tests := []struct {
			name string
			give []byte
		}{
			{
				name: "reports false for an attribute that is not a SEQUENCE",
				give: slices.Concat(attrContentType, attrMessageDigest, el(der.TagSet)),
			},
			{
				name: "reports false for an attribute without a type",
				give: slices.Concat(attrContentType, attrMessageDigest, el(der.TagSequence, el(der.TagSet))),
			},
			{
				name: "reports false for a type that is not an OID in DER",
				give: slices.Concat(attrContentType, attrMessageDigest,
					el(der.TagSequence, el(der.TagOID, []byte{0x80, 0x01}), el(der.TagSet))),
			},
			{
				name: "reports false for an attribute without values",
				give: slices.Concat(attrContentType, attrMessageDigest,
					el(der.TagSequence, el(der.TagOID, oidUnknown))),
			},
			{
				name: "reports false for values that are not a SET",
				give: slices.Concat(attrContentType, attrMessageDigest,
					el(der.TagSequence, el(der.TagOID, oidUnknown), el(der.TagSequence))),
			},
			{
				name: "reports false for an element after the values",
				give: slices.Concat(attrContentType, attrMessageDigest,
					el(der.TagSequence, el(der.TagOID, oidUnknown), el(der.TagSet), el(der.TagNull))),
			},
			{name: "reports false for attributes without a content type", give: attrMessageDigest},
			{name: "reports false for attributes without a message digest", give: attrContentType},
			{
				name: "reports false for two content types",
				give: slices.Concat(attrContentType, attrContentType, attrMessageDigest),
			},
			{
				name: "reports false for two message digests",
				give: slices.Concat(attrContentType, attrMessageDigest, attrMessageDigest),
			},
			{
				name: "reports false for a content type of two values",
				give: slices.Concat(el(der.TagSequence, el(der.TagOID, oidContentType),
					el(der.TagSet, el(der.TagOID, oidTSTInfo), el(der.TagOID, oidTSTInfo))), attrMessageDigest),
			},
			{
				name: "reports false for a content type without a value before a second content type",
				give: slices.Concat(el(der.TagSequence, el(der.TagOID, oidContentType), el(der.TagSet)),
					attrContentType, attrMessageDigest),
			},
			{
				name: "reports false for a SigningCertificate of two values",
				give: slices.Concat(attrContentType, attrMessageDigest,
					el(der.TagSequence, el(der.TagOID, oidSigningCertificate), el(der.TagSet, v1Value, v1Value))),
			},
			{
				name: "reports false for a content type that is not an OID",
				give: slices.Concat(el(der.TagSequence, el(der.TagOID, oidContentType),
					el(der.TagSet, el(der.TagOctetString, oidTSTInfo))), attrMessageDigest),
			},
			{
				name: "reports false for a message digest that is not an OCTET STRING",
				give: slices.Concat(attrContentType, el(der.TagSequence, el(der.TagOID, oidMessageDigest),
					el(der.TagSet, el(der.TagOID, attrDigest)))),
			},
			{
				name: "reports false for two SigningCertificateV2 attributes",
				give: slices.Concat(attrContentType, attrMessageDigest, attrV2, attrV2),
			},
			{
				name: "reports false for two SigningCertificate attributes",
				give: slices.Concat(attrContentType, attrMessageDigest, attrV1, attrV1),
			},
			{
				name: "reports false for a SigningCertificateV2 that does not parse",
				give: slices.Concat(attrContentType, attrMessageDigest,
					el(der.TagSequence, el(der.TagOID, oidSigningCertificateV2), el(der.TagSet, el(der.TagNull)))),
			},
			{
				name: "reports false for a SigningCertificate that does not parse",
				give: slices.Concat(attrContentType, attrMessageDigest,
					el(der.TagSequence, el(der.TagOID, oidSigningCertificate), el(der.TagSet, el(der.TagNull)))),
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, ok := parseAttributes(tt.give)
				assert.False(t, ok, "parseAttributes must refuse the attributes")
			})
		}
	})

	// The cases of a table of identifiers pass the content of the
	// attrValues of a SigningCertificate whose certs are the one identifier
	// id.
	t.Run("parseSigningCertificate", func(t *testing.T) {
		t.Parallel()

		accepted := []struct {
			name         string
			id           []byte
			v2           bool
			hash         digest
			certHash     []byte
			issuerSerial []byte
		}{
			{
				name:     "returns SHA-256 for an ESSCertIDv2 without a hashAlgorithm",
				id:       el(der.TagSequence, el(der.TagOctetString, attrDigest)),
				v2:       true,
				hash:     digestSHA256,
				certHash: attrDigest,
			},
			{
				name: "returns the hashAlgorithm of an ESSCertIDv2",
				id: el(der.TagSequence, el(der.TagSequence, el(der.TagOID, oidSHA384)),
					el(der.TagOctetString, attrSHA384)),
				v2:       true,
				hash:     digestSHA384,
				certHash: attrSHA384,
			},
			{
				name: "returns the zero digest for a hash that this package does not compute",
				id: el(der.TagSequence, el(der.TagSequence, el(der.TagOID, oidUnknown)),
					el(der.TagOctetString, attrSHA1)),
				v2:       true,
				certHash: attrSHA1,
			},
			{
				name: "returns the issuerSerial of an ESSCertIDv2",
				id: el(der.TagSequence, el(der.TagOctetString, attrDigest),
					el(der.TagSequence, attrIssuerSerial)),
				v2:           true,
				hash:         digestSHA256,
				certHash:     attrDigest,
				issuerSerial: attrIssuerSerial,
			},
			{
				name:     "returns the SHA-1 certHash of an ESSCertID",
				id:       el(der.TagSequence, el(der.TagOctetString, attrSHA1)),
				certHash: attrSHA1,
			},
		}
		for _, tt := range accepted {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				hash, certHash, issuerSerial, ok := parseSigningCertificate(
					el(der.TagSequence, el(der.TagSequence, tt.id)), tt.v2,
				)
				assert.True(t, ok, "parseSigningCertificate must accept the attribute value")
				expect.Equal(t, hash, tt.hash, "hash must be the digest of the identifier")
				expect.Equal(t, certHash, tt.certHash, "certHash must be the content of the OCTET STRING")
				expect.Equal(t, issuerSerial, tt.issuerSerial, "issuerSerial must be the content of the IssuerSerial")
			})
		}

		t.Run("returns the first identifier of certs that has later ones", func(t *testing.T) {
			t.Parallel()
			_, certHash, _, ok := parseSigningCertificate(el(der.TagSequence, el(der.TagSequence,
				el(der.TagSequence, el(der.TagOctetString, attrSHA1)),
				el(der.TagSequence, el(der.TagOctetString, bytes.Repeat([]byte{0x44}, 20))))), false)
			assert.True(t, ok, "parseSigningCertificate must read past the later identifiers")
			assert.Equal(t, certHash, attrSHA1, "certHash must be the certHash of the first identifier")
		})

		t.Run("reads past the policies", func(t *testing.T) {
			t.Parallel()
			_, _, _, ok := parseSigningCertificate(el(der.TagSequence, el(der.TagSequence,
				el(der.TagSequence, el(der.TagOctetString, attrSHA1))), el(der.TagSequence)), false)
			assert.True(t, ok, "parseSigningCertificate must read past the policies")
		})

		refusedIDs := []struct {
			name string
			id   []byte
			v2   bool
		}{
			{
				name: "reports false for a hashAlgorithm that is not DER",
				id: el(der.TagSequence, slices.Concat([]byte{byte(der.TagSequence)}, notDER),
					el(der.TagOctetString, attrDigest)),
				v2: true,
			},
			{
				name: "reports false for a hashAlgorithm without an OID",
				id:   el(der.TagSequence, el(der.TagSequence, el(der.TagNull)), el(der.TagOctetString, attrDigest)),
				v2:   true,
			},
			{
				name: "reports false for a certHash of another size than its hash",
				id: el(der.TagSequence, el(der.TagSequence, el(der.TagOID, oidSHA384)),
					el(der.TagOctetString, attrDigest)),
				v2: true,
			},
			{
				name: "reports false for a certHash of another size than SHA-256 without a hashAlgorithm",
				id:   el(der.TagSequence, el(der.TagOctetString, attrSHA1)),
				v2:   true,
			},
			{
				name: "reports false for a SHA-1 certHash of another size",
				id:   el(der.TagSequence, el(der.TagOctetString, attrDigest)),
			},
			{name: "reports false for an identifier without a certHash", id: el(der.TagSequence)},
			{
				name: "reports false for an identifier of an unknown hash without a certHash",
				id:   el(der.TagSequence, el(der.TagSequence, el(der.TagOID, oidUnknown))),
				v2:   true,
			},
			{
				name: "reports false for an issuerSerial that is not DER",
				id: el(der.TagSequence, el(der.TagOctetString, attrSHA1),
					slices.Concat([]byte{byte(der.TagSequence)}, notDER)),
			},
			{
				name: "reports false for an element after the issuerSerial",
				id: el(der.TagSequence, el(der.TagOctetString, attrSHA1), el(der.TagSequence),
					el(der.TagNull)),
			},
		}
		for _, tt := range refusedIDs {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, _, _, ok := parseSigningCertificate(el(der.TagSequence, el(der.TagSequence, tt.id)), tt.v2)
				assert.False(t, ok, "parseSigningCertificate must refuse the attribute value")
			})
		}

		id := el(der.TagSequence, el(der.TagOctetString, attrSHA1))
		refusedValues := []struct {
			name   string
			values []byte
		}{
			{
				name: "reports false for two values",
				values: slices.Concat(el(der.TagSequence, el(der.TagSequence, id)),
					el(der.TagSequence, el(der.TagSequence, id))),
			},
			{name: "reports false for a value that is not a SEQUENCE", values: el(der.TagSet)},
			{name: "reports false for a value without certs", values: el(der.TagSequence)},
			{
				name:   "reports false for certs that are not a SEQUENCE",
				values: el(der.TagSequence, el(der.TagSet, id)),
			},
			{
				name: "reports false for policies that are not DER",
				values: el(der.TagSequence, el(der.TagSequence, id),
					slices.Concat([]byte{byte(der.TagSequence)}, notDER)),
			},
			{
				name:   "reports false for an element after the policies",
				values: el(der.TagSequence, el(der.TagSequence, id), el(der.TagSequence), el(der.TagNull)),
			},
			{
				name:   "reports false for certs without an identifier",
				values: el(der.TagSequence, el(der.TagSequence)),
			},
			{
				name:   "reports false for an identifier that is not a SEQUENCE",
				values: el(der.TagSequence, el(der.TagSequence, el(der.TagSet))),
			},
		}
		for _, tt := range refusedValues {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, _, _, ok := parseSigningCertificate(tt.values, false)
				assert.False(t, ok, "parseSigningCertificate must refuse the attribute value")
			})
		}
	})

	t.Run("parseIssuerSerial", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the Name of the directoryName", func(t *testing.T) {
			t.Parallel()
			issuer, _, ok := parseIssuerSerial(attrIssuerSerial)
			assert.True(t, ok, "parseIssuerSerial must accept the IssuerSerial")
			assert.Equal(t, issuer, attrName, "issuer must be the DER of the Name")
		})

		t.Run("returns the content of the serialNumber", func(t *testing.T) {
			t.Parallel()
			_, serial, ok := parseIssuerSerial(attrIssuerSerial)
			assert.True(t, ok, "parseIssuerSerial must accept the IssuerSerial")
			assert.Equal(t, serial, attrSerial, "serial must be the content of the serialNumber")
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
				give: slices.Concat(names, el(der.TagInteger, []byte{0x00, 0x09})),
			},
			{
				name: "reports false for an element after the serialNumber",
				give: slices.Concat(names, el(der.TagInteger, attrSerial), el(der.TagNull)),
			},
			{
				name: "reports false for two names",
				give: slices.Concat(el(der.TagSequence, el(der.ContextConstructed(directoryName), attrName),
					el(der.ContextConstructed(directoryName), attrName)), el(der.TagInteger, attrSerial)),
			},
			{
				name: "reports false for a name of another choice",
				give: slices.Concat(
					el(der.TagSequence, el(der.Context(1), []byte("tsa@example.com"))),
					el(der.TagInteger, attrSerial),
				),
			},
			{
				name: "reports false for a directoryName without a Name",
				give: slices.Concat(el(der.TagSequence, el(der.ContextConstructed(directoryName))),
					el(der.TagInteger, attrSerial)),
			},
			{
				name: "reports false for a directoryName that is not a Name",
				give: slices.Concat(el(der.TagSequence, el(der.ContextConstructed(directoryName), el(der.TagSet))),
					el(der.TagInteger, attrSerial)),
			},
			{
				name: "reports false for an element after the Name",
				give: slices.Concat(el(der.TagSequence, el(der.ContextConstructed(directoryName), attrName,
					el(der.TagNull))), el(der.TagInteger, attrSerial)),
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, _, ok := parseIssuerSerial(tt.give)
				assert.False(t, ok, "parseIssuerSerial must refuse the IssuerSerial")
			})
		}
	})
}
