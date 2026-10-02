// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tsp

import (
	"bytes"
	"crypto/sha1" //nolint:gosec // G505: SigningCertificate identifies a certificate by its SHA-1 digest

	"go.thesmos.sh/core/internal/der"
)

// directoryName is the tag of the directoryName choice of a GeneralName,
// [4] EXPLICIT Name, RFC 5280 section 4.2.1.6.
const directoryName = 4

// attributes is the signed attributes of a SignerInfo that verification
// reads, each a slice of the token: RFC 5652 section 11 and the
// signing-certificate attributes of RFC 5035.
type attributes struct {
	// contentType is the content of the OID of the content-type
	// attribute, and messageDigest the content of the OCTET STRING of the
	// message-digest attribute.
	contentType, messageDigest []byte

	// certHashV2 is the certHash of the first ESSCertIDv2 of a
	// SigningCertificateV2, and nil when the attributes have none.
	// issuerSerialV2 is the content of its issuerSerial, and nil when it
	// has none.
	certHashV2, issuerSerialV2 []byte

	// certHashV1 and issuerSerialV1 are those of the first ESSCertID of a
	// SigningCertificate, whose certHash is a SHA-1 digest.
	certHashV1, issuerSerialV1 []byte

	// hashV2 is the hashAlgorithm of the first ESSCertIDv2, SHA-256 when
	// the ESSCertIDv2 omits it, and zero for a hash that this package does
	// not compute.
	hashV2 digest
}

// parseAttributes returns the attributes of attrs, the content of the
// signedAttrs of a SignerInfo, and reports false when attrs is not a
// sequence of Attribute elements, when it lacks the content-type or the
// message-digest attribute, or when one of the four attributes that it
// reads appears twice or has other than one value. An attribute of
// another type is read past.
func parseAttributes(attrs []byte) (attributes, bool) {
	var a attributes

	r := der.NewReader(attrs)
	for !r.Empty() {
		attr, ok := r.Read(der.TagSequence)
		if !ok {
			return attributes{}, false
		}

		ar := der.NewReader(attr)

		typ, ok := ar.Read(der.TagOID)
		if !ok || !der.ObjectIdentifier(typ) {
			return attributes{}, false
		}

		values, ok := ar.Read(der.TagSet)
		if !ok || !ar.Empty() || !a.add(typ, values) {
			return attributes{}, false
		}
	}

	return a, a.contentType != nil && a.messageDigest != nil
}

// add records the attribute of type typ and the content values of its
// attrValues, and reports false when the attribute is one that it records
// and already has, or whose value it cannot read.
func (a *attributes) add(typ, values []byte) bool {
	var ok bool

	switch {
	case bytes.Equal(typ, oidContentType):
		if a.contentType != nil {
			return false
		}

		a.contentType, ok = single(values, der.TagOID)
	case bytes.Equal(typ, oidMessageDigest):
		if a.messageDigest != nil {
			return false
		}

		a.messageDigest, ok = single(values, der.TagOctetString)
	case bytes.Equal(typ, oidSigningCertificateV2):
		if a.certHashV2 != nil {
			return false
		}

		a.hashV2, a.certHashV2, a.issuerSerialV2, ok = parseSigningCertificate(values, true)
	case bytes.Equal(typ, oidSigningCertificate):
		if a.certHashV1 != nil {
			return false
		}

		_, a.certHashV1, a.issuerSerialV1, ok = parseSigningCertificate(values, false)
	default:
		ok = true
	}

	return ok
}

// single returns the content of the one value of values, the content of
// an attrValues SET, when the value has tag.
func single(values []byte, tag der.Tag) ([]byte, bool) {
	r := der.NewReader(values)
	v, ok := r.Read(tag)

	return v, ok && r.Empty()
}

// parseSigningCertificate returns the hash, the certHash and the content
// of the issuerSerial of the first certificate identifier of values, the
// attrValues of a SigningCertificateV2 when v2 is set, and of a
// SigningCertificate otherwise. A SigningCertificateV2 identifier names
// its hashAlgorithm, SHA-256 when it omits it, and a SigningCertificate
// identifier is a SHA-1 digest of 20 octets. The certHash of a hash that
// this package computes has the size of the hash. The later identifiers
// and the policies restrict the certificates of a chain further, and are
// read past.
func parseSigningCertificate(values []byte, v2 bool) (hash digest, certHash, issuerSerial []byte, ok bool) {
	sc, ok := single(values, der.TagSequence)
	if !ok {
		return 0, nil, nil, false
	}

	r := der.NewReader(sc)

	certs, ok := r.Read(der.TagSequence)
	if !ok {
		return 0, nil, nil, false
	}

	if _, _, ok = r.Optional(der.TagSequence); !ok || !r.Empty() {
		return 0, nil, nil, false
	}

	cr := der.NewReader(certs)

	first, ok := cr.Read(der.TagSequence)
	if !ok {
		return 0, nil, nil, false
	}

	fr := der.NewReader(first)

	size := sha1.Size
	if v2 {
		hash, size, ok = essHash(&fr)
		if !ok {
			return 0, nil, nil, false
		}
	}

	if certHash, ok = fr.Read(der.TagOctetString); !ok || size > 0 && len(certHash) != size {
		return 0, nil, nil, false
	}

	if issuerSerial, _, ok = fr.Optional(der.TagSequence); !ok || !fr.Empty() {
		return 0, nil, nil, false
	}

	return hash, certHash, issuerSerial, true
}

// essHash reads the optional hashAlgorithm of an ESSCertIDv2 from r, and
// returns its digest and the size of its certHash. An omitted algorithm is
// SHA-256, its DEFAULT. A hash that this package does not compute returns
// the zero digest and a size of 0, which leaves the certHash unchecked.
func essHash(r *der.Reader) (digest, int, bool) {
	alg, present, ok := r.Optional(der.TagSequence)
	if !ok {
		return 0, 0, false
	}

	if !present {
		return digestSHA256, digestSHA256.size(), true
	}

	a, ok := parseAlgorithm(alg)
	if !ok {
		return 0, 0, false
	}

	d, known := digestOf(a)
	if !known {
		return 0, 0, true
	}

	return d, d.size(), true
}

// parseIssuerSerial returns the DER of the issuer Name and the content of
// the serialNumber of is, the content of an IssuerSerial, RFC 5035 section
// 5.4.1.1, whose issuer is exactly one directoryName, as RFC 5035 requires
// for a public-key certificate.
func parseIssuerSerial(is []byte) (issuer, serial []byte, ok bool) {
	r := der.NewReader(is)

	names, ok := r.Read(der.TagSequence)
	if !ok {
		return nil, nil, false
	}

	if serial, ok = r.Read(der.TagInteger); !ok || !der.Integer(serial) || !r.Empty() {
		return nil, nil, false
	}

	nr := der.NewReader(names)

	explicit, ok := nr.Read(der.ContextConstructed(directoryName))
	if !ok || !nr.Empty() {
		return nil, nil, false
	}

	er := der.NewReader(explicit)
	if issuer, _, ok = er.ReadElement(der.TagSequence); !ok || !er.Empty() {
		return nil, nil, false
	}

	return issuer, serial, true
}
