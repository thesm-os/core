// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package tsptest

import (
	"bytes"
	"crypto/sha1" //nolint:gosec // G505: a SigningCertificate identifies a certificate by its SHA-1 digest
	"slices"

	"go.thesmos.sh/core/internal/der"
)

// Spec is a token that [Authority.Token] builds. TSTInfo is required. Each
// other field replaces the part of the token that the Authority writes,
// and a nil field keeps that part, so the zero value of a field is the
// token of [Authority.Respond].
type Spec struct {
	// TSTInfo is the DER of the TSTInfo, the encapsulated content, such as
	// [Authority.TSTInfo] returns. Token signs it as it is.
	TSTInfo []byte

	// ContentType is the content of the eContentType OID. The default is
	// id-ct-TSTInfo.
	ContentType []byte

	// Attributes are the DER of the signed attributes, in any order. The
	// default is [Authority.Attributes] of TSTInfo.
	Attributes [][]byte

	// DigestAlgorithm and SignatureAlgorithm are the DER of the
	// AlgorithmIdentifiers of the SignerInfo. The defaults are those of
	// the Config's Key and Digest.
	DigestAlgorithm, SignatureAlgorithm []byte

	// SignerIdentifier is the DER of the sid of the SignerInfo. The
	// default is the IssuerAndSerialNumber of the authority's certificate,
	// or its subjectKeyIdentifier when the Config sets SubjectKeyID.
	SignerIdentifier []byte

	// Signature is the signature value. The default is the Authority's
	// signature over the signed attributes.
	Signature []byte

	// Certificates are the DER of the elements of the certificates field.
	// The default is the authority's certificate and the intermediate.
	Certificates [][]byte

	// SignerInfos are the DER of more SignerInfos after the Authority's.
	SignerInfos [][]byte

	// NoCertificates leaves the certificates field out.
	NoCertificates bool
}

// Attribute returns the DER of an Attribute, RFC 5652 section 5.3, of the
// type whose OID has the content typ, with values, each the DER of an
// element, as its attrValues in the order given. DER sorts the elements of
// a SET OF, so a caller passes more than one value in the order of their
// encodings unless the test wants a SET that is not DER. A test builds the
// signed attributes of a [Spec] with it.
func Attribute(typ []byte, values ...[]byte) []byte {
	b := der.NewBuilder(nil)
	attr := b.Open(der.TagSequence)
	b.Add(der.TagOID, typ)
	set := b.Open(der.TagSet)

	for _, v := range values {
		b.AddElement(v)
	}

	b.Close(set)
	b.Close(attr)

	return b.Bytes()
}

// setOf returns the DER of the signedAttrs of attrs: the attributes in the
// order of their encodings, as DER sorts a SET OF, under the tag [0].
func setOf(attrs [][]byte) []byte {
	sorted := slices.Clone(attrs)
	slices.SortFunc(sorted, bytes.Compare)

	b := der.NewBuilder(nil)
	at := b.Open(der.ContextConstructed(0))

	for _, attr := range sorted {
		b.AddElement(attr)
	}

	b.Close(at)

	return b.Bytes()
}

// algorithm returns the DER of an AlgorithmIdentifier of the OID with the
// content oid and the element params, none when params is nil.
func algorithm(oid, params []byte) []byte {
	b := der.NewBuilder(nil)
	seq := b.Open(der.TagSequence)
	b.Add(der.TagOID, oid)
	b.AddElement(params)
	b.Close(seq)

	return b.Bytes()
}

// element returns the DER of the element of tag and content.
func element(tag der.Tag, content []byte) []byte {
	b := der.NewBuilder(nil)
	b.Add(tag, content)

	return b.Bytes()
}

// integer returns the content of the INTEGER v in DER.
func integer(v uint64) []byte {
	b := der.NewBuilder(nil)
	b.AddUint64(v)

	r := der.NewReader(b.Bytes())
	content, _ := r.Read(der.TagInteger)

	return content
}

// orDefault returns v, or def when v is nil.
func orDefault(v, def []byte) []byte {
	if v == nil {
		return def
	}

	return v
}

// orDefaults returns v, or def when v is nil.
func orDefaults(v, def [][]byte) [][]byte {
	if v == nil {
		return def
	}

	return v
}

// Token returns the DER of the TimeStampToken of s: a ContentInfo of a
// SignedData of version 3 whose encapsulated content is s.TSTInfo, with
// one SignerInfo of the Authority, and the parts that s replaces. The
// Authority signs the signed attributes with the tag of a SET, RFC 5652
// section 5.4, unless s replaces the signature.
//
// Error modes: a Spec without a TSTInfo returns [ErrConfig], classified
// Invalid, and a failure to sign returns its error, wrapped.
func (a *Authority) Token(s Spec) ([]byte, error) {
	if s.TSTInfo == nil {
		return nil, ErrConfig
	}

	signedAttrs := setOf(orDefaults(s.Attributes, a.Attributes(s.TSTInfo)))

	signature := s.Signature
	if signature == nil {
		m := slices.Clone(signedAttrs)
		m[0] = byte(der.TagSet)

		var err error
		if signature, err = a.cfg.Key.sign(a.signer, m, a.cfg.Digest); err != nil {
			return nil, err
		}
	}

	digestAlgorithm := orDefault(s.DigestAlgorithm, algorithm(a.cfg.Digest.oid(), nil))

	b := der.NewBuilder(nil)
	ci := b.Open(der.TagSequence)
	b.Add(der.TagOID, oidSignedData)
	explicit := b.Open(der.ContextConstructed(0))
	sd := b.Open(der.TagSequence)
	b.AddUint64(3)
	digests := b.Open(der.TagSet)
	b.AddElement(digestAlgorithm)
	b.Close(digests)

	encap := b.Open(der.TagSequence)
	b.Add(der.TagOID, orDefault(s.ContentType, oidTSTInfo))
	content := b.Open(der.ContextConstructed(0))
	b.Add(der.TagOctetString, s.TSTInfo)
	b.Close(content)
	b.Close(encap)

	if !s.NoCertificates {
		at := b.Open(der.ContextConstructed(0))
		for _, c := range orDefaults(s.Certificates, [][]byte{a.leaf.Raw, a.intermediate.Raw}) {
			b.AddElement(c)
		}

		b.Close(at)
	}

	signers := b.Open(der.TagSet)
	a.signerInfo(&b, s, digestAlgorithm, signedAttrs, signature)

	for _, si := range s.SignerInfos {
		b.AddElement(si)
	}

	b.Close(signers)
	b.Close(sd)
	b.Close(explicit)
	b.Close(ci)

	return b.Bytes(), nil
}

// Attributes returns the DER of the signed attributes that the Authority
// writes for a token of tstInfo: the content type id-ct-TSTInfo, the
// message digest of tstInfo under the Config's Digest, and the
// signing-certificate attributes of the Config's ESS, in that order. A
// test changes one of them and passes the result as the Attributes of a
// [Spec].
func (a *Authority) Attributes(tstInfo []byte) [][]byte {
	attrs := [][]byte{
		Attribute(oidContentType, element(der.TagOID, oidTSTInfo)),
		Attribute(oidMessageDigest, element(der.TagOctetString, a.cfg.Digest.sum(tstInfo))),
	}

	if a.cfg.ESS.v2() {
		attrs = append(attrs, Attribute(oidSigningCertificateV2, a.signingCertificate(true)))
	}

	if a.cfg.ESS.v1() {
		attrs = append(attrs, Attribute(oidSigningCertificate, a.signingCertificate(false)))
	}

	return attrs
}

// signerInfo appends the DER of the Authority's SignerInfo to b, with the
// parts that s replaces: version 3 for a subjectKeyIdentifier of the
// Config, and 1 otherwise.
func (a *Authority) signerInfo(b *der.Builder, s Spec, digestAlgorithm, signedAttrs, signature []byte) {
	si := b.Open(der.TagSequence)
	if a.cfg.SubjectKeyID && s.SignerIdentifier == nil {
		b.AddUint64(3)
	} else {
		b.AddUint64(1)
	}

	b.AddElement(orDefault(s.SignerIdentifier, a.signerIdentifier()))
	b.AddElement(digestAlgorithm)
	b.AddElement(signedAttrs)
	b.AddElement(orDefault(s.SignatureAlgorithm, a.cfg.Key.algorithm(a.cfg.Digest)))
	b.Add(der.TagOctetString, signature)
	b.Close(si)
}

// signerIdentifier returns the DER of the Authority's sid: the
// subjectKeyIdentifier of its certificate when the Config sets
// SubjectKeyID, and its IssuerAndSerialNumber otherwise.
func (a *Authority) signerIdentifier() []byte {
	if a.cfg.SubjectKeyID {
		return element(der.Context(0), a.leaf.SubjectKeyId)
	}

	b := der.NewBuilder(nil)
	ias := b.Open(der.TagSequence)
	b.AddElement(a.leaf.RawIssuer)
	b.Add(der.TagInteger, a.serial)
	b.Close(ias)

	return b.Bytes()
}

// signingCertificate returns the DER of a SigningCertificateV2 when v2 is
// set, and of a SigningCertificate otherwise, with one identifier of the
// authority's certificate. The identifier of a SigningCertificateV2 names
// the Config's Digest, unless it is SHA-256, the DEFAULT, and the
// identifier of a SigningCertificate is a SHA-1 digest. Each has the
// issuerSerial of the certificate when the Config sets IssuerSerial.
func (a *Authority) signingCertificate(v2 bool) []byte {
	b := der.NewBuilder(nil)
	sc := b.Open(der.TagSequence)
	certs := b.Open(der.TagSequence)
	id := b.Open(der.TagSequence)

	if v2 {
		if a.cfg.Digest != DigestSHA256 {
			b.AddElement(algorithm(a.cfg.Digest.oid(), nil))
		}

		b.Add(der.TagOctetString, a.cfg.Digest.sum(a.leaf.Raw))
	} else {
		sum := sha1.Sum(a.leaf.Raw) //nolint:gosec // G401: see the import
		b.Add(der.TagOctetString, sum[:])
	}

	if a.cfg.IssuerSerial {
		is := b.Open(der.TagSequence)
		names := b.Open(der.TagSequence)
		name := b.Open(der.ContextConstructed(directoryName))
		b.AddElement(a.leaf.RawIssuer)
		b.Close(name)
		b.Close(names)
		b.Add(der.TagInteger, a.serial)
		b.Close(is)
	}

	b.Close(id)
	b.Close(certs)
	b.Close(sc)

	return b.Bytes()
}
