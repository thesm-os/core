// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package tsp

import (
	"bytes"

	"go.thesmos.sh/core/internal/der"
)

// token is the parts of a TimeStampToken that verification reads, each a
// slice of the token: a ContentInfo of SignedData, RFC 5652 sections 3
// and 5, whose encapsulated content is a TSTInfo and which has one
// SignerInfo, as RFC 3161 section 2.4.2 requires.
type token struct {
	// content is the content of eContent, the DER of the TSTInfo.
	content []byte

	// certificates is the content of the certificates field, a SET OF
	// CertificateChoices, and nil when the token has none.
	certificates []byte

	// signer is the one SignerInfo.
	signer signerInfo
}

// signerInfo is the parts of a SignerInfo, RFC 5652 section 5.3, each a
// slice of the token.
type signerInfo struct {
	// issuer is the DER of the issuer Name of an IssuerAndSerialNumber,
	// and nil for a subjectKeyIdentifier. serial is the content of its
	// serialNumber.
	issuer, serial []byte

	// keyID is the content of a subjectKeyIdentifier, and nil for an
	// IssuerAndSerialNumber.
	keyID []byte

	// signedAttrs is the whole signedAttrs element, its [0] tag and its
	// length included, and attrs its content: the attributes that the
	// signature covers.
	signedAttrs, attrs []byte

	// value is the content of the signature OCTET STRING.
	value []byte

	// digest is the digestAlgorithm, and signature the
	// signatureAlgorithm.
	digest, signature algorithmIdentifier
}

// algorithmIdentifier is an AlgorithmIdentifier, RFC 5280 section
// 4.1.1.2: the content of its OID, and the whole element of its
// parameters, nil when they are absent.
type algorithmIdentifier struct {
	oid, params []byte
}

// parseToken returns the parts of b, a TimeStampToken in DER, and reports
// false when b is not one: a ContentInfo whose content type is
// id-signedData, a SignedData whose eContentType is id-ct-TSTInfo with an
// eContent, at most one certificates field, and exactly one SignerInfo
// with signed attributes. It checks the structure only, and verifies no
// field's value. Unsigned attributes and revocation information are read
// past and not checked.
func parseToken(b []byte) (token, bool) {
	r := der.NewReader(b)

	ci, ok := r.Read(der.TagSequence)
	//dokimi:mutate-skip lcr-right: a failed Read leaves r unchanged, and the nil content of an empty r fails the read of its OID
	if !ok || !r.Empty() {
		return token{}, false
	}

	cr := der.NewReader(ci)

	ct, ok := cr.Read(der.TagOID)
	//dokimi:mutate-skip lcr-right: a failed Read returns nil, which differs from oidSignedData
	if !ok || !bytes.Equal(ct, oidSignedData) {
		return token{}, false
	}

	explicit, ok := cr.Read(der.ContextConstructed(0))
	//dokimi:mutate-skip lcr-right: a failed Read leaves cr unchanged, and the nil content of an empty cr fails the read of its SignedData
	if !ok || !cr.Empty() {
		return token{}, false
	}

	er := der.NewReader(explicit)

	sd, ok := er.Read(der.TagSequence)
	//dokimi:mutate-skip lcr-right: a failed Read leaves er unchanged, and parseSignedData refuses the nil content of an empty er
	if !ok || !er.Empty() {
		return token{}, false
	}

	return parseSignedData(sd)
}

// parseSignedData returns the parts of sd, the content of a SignedData
// whose encapsulated content is a TSTInfo, and reports false for any
// other content.
func parseSignedData(sd []byte) (token, bool) {
	var t token

	r := der.NewReader(sd)
	//dokimi:mutate-skip lcr-right: a failed Read returns nil, which der.Integer refuses
	if version, ok := r.Read(der.TagInteger); !ok || !der.Integer(version) {
		return token{}, false
	}

	if _, ok := r.Read(der.TagSet); !ok {
		return token{}, false
	}

	encap, ok := r.Read(der.TagSequence)
	//dokimi:mutate-skip sbr-delete: a failed Read returns nil, which parseEncapsulated refuses
	if !ok {
		return token{}, false
	}

	if t.content, ok = parseEncapsulated(encap); !ok {
		return token{}, false
	}

	if t.certificates, _, ok = r.Optional(der.ContextConstructed(0)); !ok {
		return token{}, false
	}

	if _, _, ok = r.Optional(der.ContextConstructed(1)); !ok {
		return token{}, false
	}

	signers, ok := r.Read(der.TagSet)
	//dokimi:mutate-skip lcr-right: a failed Read leaves r unchanged, and the nil content of an empty r fails the read of its SignerInfo
	if !ok || !r.Empty() {
		return token{}, false
	}

	sr := der.NewReader(signers)

	si, ok := sr.Read(der.TagSequence)
	//dokimi:mutate-skip lcr-right: a failed Read leaves sr unchanged, and parseSignerInfo refuses the nil content of an empty sr
	if !ok || !sr.Empty() {
		return token{}, false
	}

	if t.signer, ok = parseSignerInfo(si); !ok {
		return token{}, false
	}

	return t, true
}

// parseEncapsulated returns the content of the eContent of encap, the
// content of an EncapsulatedContentInfo, when its eContentType is
// id-ct-TSTInfo and its eContent is a primitive OCTET STRING.
func parseEncapsulated(encap []byte) ([]byte, bool) {
	r := der.NewReader(encap)
	//dokimi:mutate-skip lcr-right: a failed Read returns nil, which differs from oidTSTInfo
	if ct, ok := r.Read(der.TagOID); !ok || !bytes.Equal(ct, oidTSTInfo) {
		return nil, false
	}

	explicit, ok := r.Read(der.ContextConstructed(0))
	//dokimi:mutate-skip lcr-right: a failed Read leaves r unchanged, and the nil content of an empty r fails the read of its eContent
	if !ok || !r.Empty() {
		return nil, false
	}

	er := der.NewReader(explicit)

	content, ok := er.Read(der.TagOctetString)
	//dokimi:mutate-skip lcr-right: a failed Read leaves er unchanged, and parseTSTInfo refuses the nil content of an empty er
	if !ok || !er.Empty() {
		return nil, false
	}

	return content, true
}

// parseSignerInfo returns the parts of si, the content of a SignerInfo,
// and reports false when si is not one, or has no signed attributes,
// which RFC 5652 requires for a content type other than id-data.
func parseSignerInfo(si []byte) (signerInfo, bool) {
	var s signerInfo

	r := der.NewReader(si)
	//dokimi:mutate-skip lcr-right: a failed Read returns nil, which der.Integer refuses
	if version, ok := r.Read(der.TagInteger); !ok || !der.Integer(version) {
		return signerInfo{}, false
	}

	if sid, ok := r.Read(der.TagSequence); ok {
		if s.issuer, s.serial, ok = parseIssuerAndSerial(sid); !ok {
			return signerInfo{}, false
		}
	} else if s.keyID, ok = r.Read(der.Context(0)); !ok {
		return signerInfo{}, false
	}

	var ok bool
	if s.digest, ok = readAlgorithm(&r); !ok {
		return signerInfo{}, false
	}

	if s.signedAttrs, s.attrs, ok = r.ReadElement(der.ContextConstructed(0)); !ok {
		return signerInfo{}, false
	}

	if s.signature, ok = readAlgorithm(&r); !ok {
		return signerInfo{}, false
	}

	if s.value, ok = r.Read(der.TagOctetString); !ok {
		return signerInfo{}, false
	}

	//dokimi:mutate-skip lcr-right: a malformed element stays in r, which the check that r is empty refuses
	if _, _, ok = r.Optional(der.ContextConstructed(1)); !ok || !r.Empty() {
		return signerInfo{}, false
	}

	return s, true
}

// parseIssuerAndSerial returns the DER of the issuer Name and the content
// of the serialNumber of ias, the content of an IssuerAndSerialNumber, RFC
// 5652 section 10.2.4.
func parseIssuerAndSerial(ias []byte) (issuer, serial []byte, ok bool) {
	r := der.NewReader(ias)
	if issuer, _, ok = r.ReadElement(der.TagSequence); !ok {
		return nil, nil, false
	}

	serial, ok = r.Read(der.TagInteger)
	//dokimi:mutate-skip lcr-right: a failed Read returns nil, which der.Integer refuses
	if !ok || !der.Integer(serial) {
		return nil, nil, false
	}

	if !r.Empty() {
		return nil, nil, false
	}

	return issuer, serial, true
}

// readAlgorithm reads an AlgorithmIdentifier from r: a SEQUENCE of an OID
// and, optionally, one element of parameters.
func readAlgorithm(r *der.Reader) (algorithmIdentifier, bool) {
	seq, ok := r.Read(der.TagSequence)
	//dokimi:mutate-skip sbr-delete: a failed Read returns nil, which parseAlgorithm refuses
	if !ok {
		return algorithmIdentifier{}, false
	}

	return parseAlgorithm(seq)
}

// parseAlgorithm returns the AlgorithmIdentifier whose content is seq.
func parseAlgorithm(seq []byte) (algorithmIdentifier, bool) {
	var a algorithmIdentifier

	r := der.NewReader(seq)

	var ok bool
	//dokimi:mutate-skip lcr-right: a failed Read returns nil, which der.ObjectIdentifier refuses
	if a.oid, ok = r.Read(der.TagOID); !ok || !der.ObjectIdentifier(a.oid) {
		return algorithmIdentifier{}, false
	}

	if !r.Empty() {
		//dokimi:mutate-skip lcr-right: a failed Next reads nothing, so r is not empty
		if _, a.params, _, ok = r.Next(); !ok || !r.Empty() {
			return algorithmIdentifier{}, false
		}
	}

	return a, true
}

// noParams reports whether a has no parameters or NULL parameters, the two
// forms that RFC 5754 and RFC 3279 allow for a hash and for an RSA
// signature algorithm.
func (a algorithmIdentifier) noParams() bool {
	return a.params == nil || bytes.Equal(a.params, nullParams)
}

// absentParams reports whether a has no parameters, the one form that RFC
// 5758, RFC 8419 and RFC 9882 allow for ECDSA, Ed25519 and ML-DSA.
func (a algorithmIdentifier) absentParams() bool {
	return a.params == nil
}

// nullParams is the DER of NULL, the parameters that an AlgorithmIdentifier
// of a hash or an RSA signature may have.
var nullParams = []byte{byte(der.TagNull), 0x00}
