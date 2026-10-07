// Copyright Thesmos 2026
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

// oidUnknown is the content of the OID 1.2.3, which names nothing that this
// package reads.
var oidUnknown = []byte{0x2a, 0x03}

// notDER is an element whose length is in the long form below 128, which
// DER forbids, under a tag that a test prefixes.
var notDER = []byte{0x81, 0x01, 0x00}

// The parts of a token that parseToken accepts. Each case of TestToken
// replaces one of them.
var (
	// tokenTSTInfo is the eContent of the token. parseToken does not parse
	// it.
	tokenTSTInfo = el(der.TagSequence, el(der.TagInteger, []byte{1}))

	// tokenEContent is the [0] EXPLICIT element of the eContent.
	tokenEContent = el(der.ContextConstructed(0), el(der.TagOctetString, tokenTSTInfo))

	// tokenIssuer is the issuer Name of the signer identifier, an empty
	// RDNSequence, and tokenSerial the content of its serialNumber.
	tokenIssuer = el(der.TagSequence)
	tokenSerial = []byte{0x05}

	// tokenAttrs is the content of the signedAttrs, and tokenValue the
	// content of the signature.
	tokenAttrs = el(der.TagSequence, el(der.TagOID, oidContentType))
	tokenValue = []byte{0x0a, 0x0b}

	// tokenDigest and tokenSignature are the digestAlgorithm and the
	// signatureAlgorithm of the SignerInfo.
	tokenDigest    = el(der.TagSequence, el(der.TagOID, oidSHA512))
	tokenSignature = el(der.TagSequence, el(der.TagOID, oidEd25519))

	// tokenCertificates is the certificates field, which contains one empty
	// SEQUENCE.
	tokenCertificates = el(der.ContextConstructed(0), el(der.TagSequence))
)

func TestToken(t *testing.T) {
	t.Parallel()

	valid := signedDataOf(el(der.TagSequence, signerInfoOf()))
	signedData := el(der.TagSequence, valid...)

	t.Run("parseToken", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the parts of a token", func(t *testing.T) {
			t.Parallel()
			tok, ok := parseToken(el(der.TagSequence, el(der.TagOID, oidSignedData),
				el(der.ContextConstructed(0), signedData)))
			assert.True(t, ok, "parseToken must accept the token")
			expect.Equal(t, tok.content, tokenTSTInfo, "content must be the eContent")
			expect.Equal(t, tok.certificates, el(der.TagSequence), "certificates must be the content of the field")
			expect.Equal(t, tok.signer.issuer, tokenIssuer, "issuer must be the DER of the Name")
			expect.Equal(t, tok.signer.serial, tokenSerial, "serial must be the content of the serialNumber")
			expect.Nil(t, tok.signer.keyID, "keyID must be nil for an IssuerAndSerialNumber")
			expect.Equal(t, tok.signer.signedAttrs, el(der.ContextConstructed(0), tokenAttrs),
				"signedAttrs must be the whole element")
			expect.Equal(t, tok.signer.attrs, tokenAttrs, "attrs must be the content of signedAttrs")
			expect.Equal(t, tok.signer.value, tokenValue, "value must be the content of the signature")
			expect.Equal(t, tok.signer.digest.oid, oidSHA512, "digest must be the digestAlgorithm")
			expect.Equal(t, tok.signer.signature.oid, oidEd25519, "signature must be the signatureAlgorithm")
		})

		tests := []struct {
			name string
			give []byte
		}{
			{name: "reports false for a ContentInfo that is not a SEQUENCE", give: el(der.TagSet)},
			{
				name: "reports false for octets after the ContentInfo",
				give: slices.Concat(el(der.TagSequence, el(der.TagOID, oidSignedData),
					el(der.ContextConstructed(0), signedData)), el(der.TagNull)),
			},
			{
				name: "reports false for a content type other than id-signedData",
				give: el(der.TagSequence, el(der.TagOID, oidTSTInfo), el(der.ContextConstructed(0), signedData)),
			},
			{
				name: "reports false for a ContentInfo without content",
				give: el(der.TagSequence, el(der.TagOID, oidSignedData)),
			},
			{
				name: "reports false for an element after the content",
				give: el(der.TagSequence, el(der.TagOID, oidSignedData), el(der.ContextConstructed(0), signedData),
					el(der.TagNull)),
			},
			{
				name: "reports false for content that is not a SEQUENCE",
				give: el(der.TagSequence, el(der.TagOID, oidSignedData),
					el(der.ContextConstructed(0), el(der.TagSet, valid...))),
			},
			{
				name: "reports false for an element after the SignedData",
				give: el(der.TagSequence, el(der.TagOID, oidSignedData),
					el(der.ContextConstructed(0), signedData, el(der.TagNull))),
			},
			{
				name: "reports false for a SignedData that parseSignedData refuses",
				give: el(der.TagSequence, el(der.TagOID, oidSignedData),
					el(der.ContextConstructed(0), el(der.TagSequence))),
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, ok := parseToken(tt.give)
				assert.False(t, ok, "parseToken must refuse the token")
			})
		}
	})

	t.Run("parseSignedData", func(t *testing.T) {
		t.Parallel()

		t.Run("returns nil certificates for a SignedData without them", func(t *testing.T) {
			t.Parallel()
			tok, ok := parseSignedData(slices.Concat(valid[0], valid[1], valid[2], valid[4]))
			assert.True(t, ok, "parseSignedData must accept a SignedData without certificates")
			assert.Nil(t, tok.certificates, "certificates must be nil")
		})

		t.Run("reads past the crls", func(t *testing.T) {
			t.Parallel()
			_, ok := parseSignedData(slices.Concat(valid[0], valid[1], valid[2], valid[3],
				el(der.ContextConstructed(1)), valid[4]))
			assert.True(t, ok, "parseSignedData must accept crls")
		})

		tests := []struct {
			name  string
			parts [][]byte
		}{
			{name: "reports false for a SignedData without a version", parts: valid[1:]},
			{
				name:  "reports false for a version that is not an INTEGER in DER",
				parts: [][]byte{el(der.TagInteger, []byte{0x00, 0x03}), valid[1], valid[2], valid[3], valid[4]},
			},
			{
				name:  "reports false for a SignedData without digestAlgorithms",
				parts: [][]byte{valid[0], valid[2], valid[3], valid[4]},
			},
			{
				name:  "reports false for a SignedData without encapContentInfo",
				parts: [][]byte{valid[0], valid[1], valid[3], valid[4]},
			},
			{
				name: "reports false for an eContentType other than id-ct-TSTInfo",
				parts: [][]byte{
					valid[0], valid[1], el(der.TagSequence, el(der.TagOID, oidUnknown), tokenEContent),
					valid[3], valid[4],
				},
			},
			{
				name: "reports false for an encapContentInfo without eContent",
				parts: [][]byte{
					valid[0], valid[1], el(der.TagSequence, el(der.TagOID, oidTSTInfo)), valid[3], valid[4],
				},
			},
			{
				name: "reports false for an element after eContent",
				parts: [][]byte{
					valid[0], valid[1],
					el(der.TagSequence, el(der.TagOID, oidTSTInfo), tokenEContent, el(der.TagNull)), valid[3], valid[4],
				},
			},
			{
				name: "reports false for eContent that is not an OCTET STRING",
				parts: [][]byte{
					valid[0], valid[1],
					el(der.TagSequence, el(der.TagOID, oidTSTInfo), el(der.ContextConstructed(0), tokenTSTInfo)),
					valid[3], valid[4],
				},
			},
			{
				name: "reports false for an element after the OCTET STRING of eContent",
				parts: [][]byte{
					valid[0], valid[1], el(der.TagSequence, el(der.TagOID, oidTSTInfo),
						el(der.ContextConstructed(0), el(der.TagOctetString, tokenTSTInfo), el(der.TagNull))),
					valid[3], valid[4],
				},
			},
			{
				name: "reports false for certificates that are not DER",
				parts: [][]byte{
					valid[0], valid[1], valid[2],
					slices.Concat([]byte{byte(der.ContextConstructed(0))}, notDER), valid[4],
				},
			},
			{
				name: "reports false for crls that are not DER",
				parts: [][]byte{
					valid[0], valid[1], valid[2], valid[3],
					slices.Concat([]byte{byte(der.ContextConstructed(1))}, notDER), valid[4],
				},
			},
			{
				name:  "reports false for a SignedData without signerInfos",
				parts: [][]byte{valid[0], valid[1], valid[2], valid[3]},
			},
			{
				name:  "reports false for an element after signerInfos",
				parts: [][]byte{valid[0], valid[1], valid[2], valid[3], valid[4], el(der.TagNull)},
			},
			{
				name:  "reports false for signerInfos without a SignerInfo",
				parts: [][]byte{valid[0], valid[1], valid[2], valid[3], el(der.TagSet)},
			},
			{
				name: "reports false for two SignerInfos",
				parts: [][]byte{
					valid[0], valid[1], valid[2], valid[3],
					el(der.TagSet, el(der.TagSequence, signerInfoOf()), el(der.TagSequence, signerInfoOf())),
				},
			},
			{
				name:  "reports false for a SignerInfo that is not a SEQUENCE",
				parts: [][]byte{valid[0], valid[1], valid[2], valid[3], el(der.TagSet, el(der.TagSet))},
			},
			{
				name: "reports false for a SignerInfo that parseSignerInfo refuses",
				parts: [][]byte{
					valid[0], valid[1], valid[2], valid[3],
					el(der.TagSet, el(der.TagSequence, signerInfoOf(func(p *signerParts) { p.version = nil }))),
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, ok := parseSignedData(slices.Concat(tt.parts...))
				assert.False(t, ok, "parseSignedData must refuse the SignedData")
			})
		}
	})

	t.Run("parseSignerInfo", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the subjectKeyIdentifier of a signer", func(t *testing.T) {
			t.Parallel()
			s, ok := parseSignerInfo(signerInfoOf(func(p *signerParts) { p.sid = el(der.Context(0), []byte{1, 2, 3}) }))
			assert.True(t, ok, "parseSignerInfo must accept a subjectKeyIdentifier")
			expect.Equal(t, s.keyID, []byte{1, 2, 3}, "keyID must be the content of the identifier")
			expect.Nil(t, s.issuer, "issuer must be nil for a subjectKeyIdentifier")
		})

		t.Run("reads past the unsigned attributes", func(t *testing.T) {
			t.Parallel()
			_, ok := parseSignerInfo(signerInfoOf(func(p *signerParts) { p.unsigned = el(der.ContextConstructed(1)) }))
			assert.True(t, ok, "parseSignerInfo must accept unsigned attributes")
		})

		tests := []struct {
			name string
			edit func(*signerParts)
		}{
			{name: "reports false for a SignerInfo without a version", edit: func(p *signerParts) { p.version = nil }},
			{
				name: "reports false for a version that is not an INTEGER in DER",
				edit: func(p *signerParts) { p.version = el(der.TagInteger) },
			},
			{
				name: "reports false for an IssuerAndSerialNumber without an issuer",
				edit: func(p *signerParts) { p.sid = el(der.TagSequence, el(der.TagInteger, tokenSerial)) },
			},
			{
				name: "reports false for an IssuerAndSerialNumber without a serialNumber",
				edit: func(p *signerParts) { p.sid = el(der.TagSequence, tokenIssuer) },
			},
			{
				name: "reports false for a serialNumber that is not an INTEGER in DER",
				edit: func(p *signerParts) {
					p.sid = el(der.TagSequence, tokenIssuer, el(der.TagInteger, []byte{0x00, 0x05}))
				},
			},
			{
				name: "reports false for an element after the serialNumber",
				edit: func(p *signerParts) {
					p.sid = el(der.TagSequence, tokenIssuer, el(der.TagInteger, tokenSerial), el(der.TagNull))
				},
			},
			{
				name: "reports false for a signer identifier of another tag",
				edit: func(p *signerParts) { p.sid = el(der.TagOctetString, []byte{1}) },
			},
			{
				name: "reports false for a SignerInfo without a digestAlgorithm",
				edit: func(p *signerParts) { p.digest = nil },
			},
			{name: "reports false for a SignerInfo without signedAttrs", edit: func(p *signerParts) { p.signed = nil }},
			{
				name: "reports false for a SignerInfo without a signatureAlgorithm",
				edit: func(p *signerParts) { p.signature = nil },
			},
			{name: "reports false for a SignerInfo without a signature", edit: func(p *signerParts) { p.value = nil }},
			{
				name: "reports false for unsignedAttrs that are not DER",
				edit: func(p *signerParts) {
					p.unsigned = slices.Concat([]byte{byte(der.ContextConstructed(1))}, notDER)
				},
			},
			{
				name: "reports false for an element after the unsignedAttrs",
				edit: func(p *signerParts) { p.unsigned = slices.Concat(el(der.ContextConstructed(1)), el(der.TagNull)) },
			},
			{
				name: "reports false for a digestAlgorithm that is not a SEQUENCE",
				edit: func(p *signerParts) { p.digest = el(der.TagSet, el(der.TagOID, oidSHA512)) },
			},
			{
				name: "reports false for an AlgorithmIdentifier without an OID",
				edit: func(p *signerParts) { p.digest = el(der.TagSequence, el(der.TagNull)) },
			},
			{
				name: "reports false for an AlgorithmIdentifier whose OID is not DER",
				edit: func(p *signerParts) { p.digest = el(der.TagSequence, el(der.TagOID, []byte{0x80, 0x01})) },
			},
			{
				name: "reports false for parameters that are not DER",
				edit: func(p *signerParts) {
					p.digest = el(der.TagSequence, el(der.TagOID, oidSHA512),
						slices.Concat([]byte{byte(der.TagNull)}, notDER))
				},
			},
			{
				name: "reports false for two elements of parameters",
				edit: func(p *signerParts) {
					p.digest = el(der.TagSequence, el(der.TagOID, oidSHA512), el(der.TagNull), el(der.TagNull))
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, ok := parseSignerInfo(signerInfoOf(tt.edit))
				assert.False(t, ok, "parseSignerInfo must refuse the SignerInfo")
			})
		}
	})

	t.Run("parseAlgorithm", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the OID of an identifier without parameters", func(t *testing.T) {
			t.Parallel()
			a, ok := parseAlgorithm(el(der.TagOID, oidSHA256))
			assert.True(t, ok, "parseAlgorithm must accept an OID without parameters")
			expect.Equal(t, a.oid, oidSHA256, "oid must be the content of the OID")
			expect.Nil(t, a.params, "params must be nil when they are absent")
		})

		t.Run("returns the whole element of the parameters", func(t *testing.T) {
			t.Parallel()
			a, ok := parseAlgorithm(slices.Concat(el(der.TagOID, oidSHA256), el(der.TagSequence, el(der.TagNull))))
			assert.True(t, ok, "parseAlgorithm must accept one element of parameters")
			assert.Equal(t, a.params, el(der.TagSequence, el(der.TagNull)), "params must be the whole element")
		})
	})

	t.Run("noParams", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			params []byte
			want   bool
		}{
			{name: "reports true for absent parameters", params: nil, want: true},
			{name: "reports true for NULL parameters", params: []byte{0x05, 0x00}, want: true},
			{name: "reports false for other parameters", params: el(der.TagSequence), want: false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				a := algorithmIdentifier{oid: oidSHA256, params: tt.params}
				assert.Equal(t, a.noParams(), tt.want, "noParams must report the form of the parameters")
			})
		}
	})

	t.Run("absentParams", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			params []byte
			want   bool
		}{
			{name: "reports true for absent parameters", params: nil, want: true},
			{name: "reports false for NULL parameters", params: []byte{0x05, 0x00}, want: false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				a := algorithmIdentifier{oid: oidEd25519, params: tt.params}
				assert.Equal(t, a.absentParams(), tt.want, "absentParams must report absent parameters")
			})
		}
	})
}

// signerParts is the elements of a SignerInfo that signerInfoOf writes, in
// their order. A nil element is left out.
type signerParts struct {
	version, sid, digest, signed, signature, value, unsigned []byte
}

// signerInfoOf returns the content of a SignerInfo that parseSignerInfo
// accepts, after edits change its parts.
func signerInfoOf(edits ...func(*signerParts)) []byte {
	p := signerParts{
		version:   el(der.TagInteger, []byte{1}),
		sid:       el(der.TagSequence, tokenIssuer, el(der.TagInteger, tokenSerial)),
		digest:    tokenDigest,
		signed:    el(der.ContextConstructed(0), tokenAttrs),
		signature: tokenSignature,
		value:     el(der.TagOctetString, tokenValue),
	}
	for _, edit := range edits {
		edit(&p)
	}

	return slices.Concat(p.version, p.sid, p.digest, p.signed, p.signature, p.value, p.unsigned)
}

// signedDataOf returns the five elements of a SignedData that
// parseSignedData accepts, with si as its one SignerInfo element: version,
// digestAlgorithms, encapContentInfo, certificates and signerInfos.
func signedDataOf(si []byte) [][]byte {
	return [][]byte{
		el(der.TagInteger, []byte{3}),
		el(der.TagSet, tokenDigest),
		el(der.TagSequence, el(der.TagOID, oidTSTInfo), tokenEContent),
		tokenCertificates,
		el(der.TagSet, si),
	}
}

// el returns the DER of the element of tag whose content is the
// concatenation of contents.
func el(tag der.Tag, contents ...[]byte) []byte {
	b := der.NewBuilder(nil)
	b.Add(tag, bytes.Join(contents, nil))

	return b.Bytes()
}
