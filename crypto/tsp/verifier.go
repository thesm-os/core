// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tsp

import (
	"bytes"
	"crypto/fips140"
	"crypto/sha1" //nolint:gosec // G505: RFC 3161 identifies a certificate by its SHA-1 digest in SigningCertificate
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/asn1"
	"fmt"
	"time"

	"go.thesmos.sh/core/cache"
	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/clock/hlc"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/internal/der"
	"go.thesmos.sh/core/pool"
)

// verifiedChains is the number of authority certificates whose verified
// chains a Verifier keeps. An authority signs with one certificate at a
// time and changes it every few years, so a verifier of the tokens of a
// few authorities hits the cache for every token.
const verifiedChains = 64

// oidExtKeyUsage is id-ce-extKeyUsage, 2.5.29.37, RFC 5280 section
// 4.2.1.12, in the form of the Extensions of an [x509.Certificate].
var oidExtKeyUsage = asn1.ObjectIdentifier{2, 5, 29, 37}

// oidSubjectAltName is id-ce-subjectAltName, 2.5.29.17, RFC 5280 section
// 4.2.1.6, in the form of the Extensions of an [x509.Certificate].
var oidSubjectAltName = asn1.ObjectIdentifier{2, 5, 29, 17}

// buffers pools the buffers into which Verify copies the signed
// attributes, under the tag of a SET, to verify their signature.
var buffers = pool.NewBufferPool()

// clockForCache is the clock of the caches of verified chains. The chains
// do not expire, so a cache reads it only when a Set evicts, and its time
// changes no result.
var clockForCache clock.Clock = hlc.New(0)

// chainsConfig is the configuration of the cache of verified chains of
// each Verifier. cache.New refuses only a configuration without a Clock or
// with a Capacity below 1, so it accepts chainsConfig, as TestChain
// checks.
var chainsConfig = cache.Config[[sha256.Size]byte, *chain]{Clock: clockForCache, Capacity: verifiedChains}

// Policy is a policy of time-stamp authorities that a [Verifier] accepts,
// with the accuracy of its tokens that state none. RFC 3161 section 2.4.2
// lets an authority's policy state the accuracy of its tokens in place of
// the tokens.
type Policy struct {
	// ID is the TSAPolicyId of the policy.
	ID x509.OID

	// Accuracy is the accuracy of the tokens of the policy that state
	// none. It must be positive.
	Accuracy time.Duration
}

// VerifierConfig configures a [Verifier]. Roots and Policies are
// required, and Intermediates and Check are optional.
type VerifierConfig struct {
	// Roots are the certificates of the roots that the certificate of an
	// authority chains to.
	Roots *x509.CertPool

	// Intermediates are certificates between the roots and the
	// certificate of an authority that tokens do not contain. A token's
	// own certificates other than the authority's serve as intermediates
	// too.
	Intermediates *x509.CertPool

	// Check is called with the Info of each token that verifies, whose
	// Chain is the verified chain of the authority's certificate, before
	// Verify returns, and an error that it returns fails the verification.
	// A caller checks there what this package cannot know: the status of
	// the authority in a trusted list, the revocation of its certificate
	// at the token's time, or an extension that a qualified time-stamp
	// contains. A nil Check checks nothing more.
	Check func(info Info) error

	// Policies are the policies whose tokens the Verifier accepts. There
	// is at least one, and no two have one ID.
	Policies []Policy
}

// Verifier verifies RFC 3161 time-stamp tokens offline, against the roots
// and the policies of its configuration. It verifies each token in the
// steps of RFC 3161 section 2.2 and RFC 5652 section 5.6:
//
//   - The token is a SignedData of a TSTInfo with one SignerInfo.
//   - The message imprint is the caller's, and the policy is one of the
//     Verifier's.
//   - The content-type attribute is id-ct-TSTInfo, and the message-digest
//     attribute is the digest of the TSTInfo.
//   - The SigningCertificateV2 attribute, or the SigningCertificate
//     attribute, names a certificate of the token, which the signer
//     identifier names too.
//   - The certificate has one critical extended key usage,
//     id-kp-timeStamping, and a chain to the roots that is valid at
//     genTime and allows time stamping.
//   - The signature verifies under the certificate's key.
//   - A tsa field names the certificate: a directoryName of its subject, or
//     a name of its subjectAltName extension.
//   - The configuration's Check accepts the token.
//
// The Verifier keeps the verified chains of up to 64 authority
// certificates, by the SHA-256 of the certificate, so a token of a known
// certificate verifies without parsing a certificate. A token whose
// genTime is outside the validity of a kept chain verifies its chain again.
//
// The Verifier does not check revocation. Under RFC 3161 section 4, a
// token from before the revocation of its authority's certificate remains
// valid for some reasons of revocation, so the caller's Check decides.
//
// # Concurrency
//
// Safe for concurrent use. Verify takes no lock for a token of a known
// certificate.
//
// # Allocation contract
//
// Verify allocates nothing for a token of a known certificate signed with
// Ed25519 or ML-DSA, apart from what Check allocates. RSA and ECDSA
// allocate what crypto/rsa and crypto/ecdsa allocate to verify a
// signature. A token of an unknown certificate allocates its parse and the
// verification of its chain.
type Verifier struct {
	roots, intermediates *x509.CertPool
	check                func(Info) error

	// chains keeps the verified chains by the SHA-256 of the leaf.
	chains *cache.Cache[[sha256.Size]byte, *chain]

	policies []policy
}

// policy is a Policy and the content of its ID in DER.
type policy struct {
	id []byte
	Policy
}

// chain is a verified chain of an authority's certificate.
type chain struct {
	notBefore, notAfter time.Time

	// leaf is the authority's certificate, and certs the chain, leaf
	// first.
	leaf  *x509.Certificate
	certs []*x509.Certificate

	// serial is the content of the leaf's serialNumber INTEGER.
	serial []byte
}

// NewVerifier returns a Verifier of cfg.
//
// Error modes: Roots that are nil, no Policies, a Policy without an ID or
// with an Accuracy that is not positive, and two Policies of one ID return
// [ErrConfig], classified [go.thesmos.sh/core/errs.Invalid].
//
// # Allocation contract
//
// The Verifier, its policies and its cache.
func NewVerifier(cfg VerifierConfig) (*Verifier, error) {
	if cfg.Roots == nil || len(cfg.Policies) == 0 {
		return nil, ErrConfig
	}

	policies := make([]policy, 0, len(cfg.Policies))
	for _, p := range cfg.Policies {
		id := appendOID(nil, p.ID)
		if len(id) == 0 || p.Accuracy <= 0 {
			return nil, ErrConfig
		}

		for _, q := range policies {
			if bytes.Equal(q.id, id) {
				return nil, ErrConfig
			}
		}

		policies = append(policies, policy{id: id, Policy: p})
	}

	chains, _ := cache.New(chainsConfig) //nolint:errcheck // cache.New accepts chainsConfig, as TestChain checks

	return &Verifier{
		roots:         cfg.Roots,
		intermediates: cfg.Intermediates,
		check:         cfg.Check,
		chains:        chains,
		policies:      policies,
	}, nil
}

// Verify verifies token, a TimeStampToken in DER, for imprint, a digest of
// h, and returns its TSTInfo. The steps are those of the [Verifier]
// documentation. Info.Time.Latest is the time before which the data of the
// imprint existed.
//
// Error modes:
//
//   - A Hash without an OID, and an imprint whose size is not h.Size,
//     return [ErrHash], classified Invalid.
//   - A token that is not DER returns [ErrMalformed].
//   - Another imprint returns [ErrImprint], and a policy that the
//     Verifier does not accept [ErrPolicy].
//   - A digest or a signature algorithm that this package does not
//     verify, and a SHA-1 certificate identifier in FIPS 140-only mode,
//     return [ErrUnsupported], classified Unsupported.
//   - Attributes, a signer identifier, a signature or a tsa field that
//     fail their checks return [ErrSignature].
//   - A certificate that the token lacks, or that fails its checks,
//     returns an error that wraps [ErrCertificate].
//   - An error of Check returns an error that wraps it.
//
// Every error of this package but ErrHash and ErrUnsupported classifies as
// [go.thesmos.sh/core/errs.Integrity].
//
// # Allocation contract
//
// As the [Verifier]'s. The Info refers to token, so the caller keeps token
// while it uses the Info. Its Chain is the chain that the Verifier keeps,
// without a copy.
func (v *Verifier) Verify(token []byte, h Hash, imprint crypto.Digest) (Info, error) {
	if !h.matches(imprint) {
		return Info{}, ErrHash
	}

	t, ok := parseToken(token)
	if !ok {
		return Info{}, ErrMalformed
	}

	ti, ok := parseTSTInfo(t.content)
	if !ok {
		return Info{}, ErrMalformed
	}

	if !h.names(ti.hash) || !bytes.Equal(ti.imprint, imprint.Bytes()) {
		return Info{}, ErrImprint
	}

	p, ok := v.policy(ti.policy)
	if !ok {
		return Info{}, ErrPolicy
	}

	c, err := v.signer(&t, ti.genTime)
	if err != nil {
		return Info{}, err
	}

	if ti.tsa != nil && !c.names(ti.tsa) {
		return Info{}, ErrSignature
	}

	info := Info{
		Policy:     p,
		Serial:     ti.serial,
		Nonce:      ti.nonce,
		TSA:        ti.tsa,
		Chain:      c.certs,
		extensions: ti.extensions,
		Time:       clock.UTCReading{Time: ti.genTime, MaxError: p.Accuracy, Synced: true},
		Ordering:   ti.ordering,
	}
	if ti.stated {
		info.Time.MaxError = ti.accuracy
	}

	if v.check != nil {
		if err := v.check(info); err != nil {
			return Info{}, fmt.Errorf("tsp: check: %w", err)
		}
	}

	return info, nil
}

// policy returns the Policy of the Verifier whose ID has the content id.
func (v *Verifier) policy(id []byte) (Policy, bool) {
	for _, p := range v.policies {
		if bytes.Equal(p.id, id) {
			return p.Policy, true
		}
	}

	return Policy{}, false
}

// signer checks the signer information of t, and returns the verified
// chain of its authority's certificate: the attributes, the certificate
// that they name, its chain at genTime, the signer identifier, and the
// signature.
func (v *Verifier) signer(t *token, genTime time.Time) (*chain, error) {
	d, ok := digestOf(t.signer.digest)
	if !ok {
		return nil, ErrUnsupported
	}

	alg, ok := signatureOf(t.signer.signature, d)
	if !ok {
		return nil, ErrUnsupported
	}

	attrs, ok := parseAttributes(t.signer.attrs)
	if !ok {
		return nil, ErrMalformed
	}

	var sum [sha512.Size]byte
	if !bytes.Equal(attrs.contentType, oidTSTInfo) || !bytes.Equal(attrs.messageDigest, d.sum(t.content, &sum)) {
		return nil, ErrSignature
	}

	leaf, err := certificate(attrs, t.certificates)
	if err != nil {
		return nil, err
	}

	c, err := v.chainOf(leaf, t.certificates, genTime)
	if err != nil {
		return nil, err
	}

	if !c.identifies(t.signer) || !c.issues(attrs) {
		return nil, ErrSignature
	}

	// The signature covers the signed attributes with the tag of a SET
	// in place of their [0] tag, RFC 5652 section 5.4.
	buf := buffers.Get()
	defer buffers.Put(buf)

	_, _ = buf.Write(t.signer.signedAttrs) // bytes.Buffer.Write returns no error
	m := buf.Bytes()
	m[0] = byte(der.TagSet)

	if !alg.verify(c.leaf.PublicKey, m, t.signer.value) {
		return nil, ErrSignature
	}

	return c, nil
}

// chainOf returns the verified chain of leaf, the DER of an authority's
// certificate, at genTime: the chain that the Verifier keeps when genTime
// is within its validity, and otherwise a chain that it verifies with the
// intermediates of the configuration and the other certificates of the
// token, and keeps.
func (v *Verifier) chainOf(leaf, certificates []byte, genTime time.Time) (*chain, error) {
	key := sha256.Sum256(leaf)
	if c, ok := v.chains.Get(key); ok && c.valid(genTime) {
		return c, nil
	}

	cert, err := x509.ParseCertificate(leaf)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCertificate, err)
	}

	if !timeStampingOnly(cert) {
		return nil, fmt.Errorf("%w: the extended key usage is not one critical id-kp-timeStamping", ErrCertificate)
	}

	chains, err := cert.Verify(x509.VerifyOptions{
		Roots:         v.roots,
		Intermediates: v.pool(certificates, leaf),
		CurrentTime:   genTime,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageTimeStamping},
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCertificate, err)
	}

	c := &chain{leaf: cert, certs: chains[0], serial: serialOf(cert.RawTBSCertificate)}
	c.notBefore, c.notAfter = cert.NotBefore, cert.NotAfter
	for _, ca := range chains[0][1:] {
		c.notBefore, c.notAfter = laterOf(c.notBefore, ca.NotBefore), earlierOf(c.notAfter, ca.NotAfter)
	}

	v.chains.Set(key, c, time.Time{})

	return c, nil
}

// pool returns the intermediates of a chain: those of the configuration,
// and each certificate of certificates other than leaf that parses. A
// token may contain certificates that this package does not parse, such
// as attribute certificates, which serve no chain.
func (v *Verifier) pool(certificates, leaf []byte) *x509.CertPool {
	var p *x509.CertPool
	if v.intermediates != nil {
		p = v.intermediates.Clone()
	} else {
		p = x509.NewCertPool()
	}

	r := der.NewReader(certificates)
	for !r.Empty() {
		tag, element, _, ok := r.Next()
		if !ok {
			break
		}

		if tag != der.TagSequence || bytes.Equal(element, leaf) {
			continue
		}

		if cert, err := x509.ParseCertificate(element); err == nil {
			p.AddCert(cert)
		}
	}

	return p
}

// valid reports whether every certificate of c is valid at t.
func (c *chain) valid(t time.Time) bool {
	return !t.Before(c.notBefore) && !t.After(c.notAfter)
}

// identifies reports whether s, the signer identifier of a SignerInfo,
// names the leaf of c: its issuer and serial number, or its subject key
// identifier.
func (c *chain) identifies(s signerInfo) bool {
	if s.keyID != nil {
		return len(c.leaf.SubjectKeyId) > 0 && bytes.Equal(s.keyID, c.leaf.SubjectKeyId)
	}

	return bytes.Equal(s.issuer, c.leaf.RawIssuer) && bytes.Equal(s.serial, c.serial)
}

// issues reports whether the issuerSerial of each signing-certificate
// attribute of attrs that has one names the leaf of c: one directoryName
// that is the leaf's issuer, and its serial number.
func (c *chain) issues(attrs attributes) bool {
	for _, is := range [...][]byte{attrs.issuerSerialV2, attrs.issuerSerialV1} {
		if is == nil {
			continue
		}

		issuer, serial, ok := parseIssuerSerial(is)
		if !ok || !bytes.Equal(issuer, c.leaf.RawIssuer) || !bytes.Equal(serial, c.serial) {
			return false
		}
	}

	return true
}

// names reports whether tsa, the DER of the GeneralName of a tsa field,
// names the leaf of c, as RFC 3161 section 2.4.2 requires of a tsa field:
// a directoryName whose Name is the leaf's subject, or a GeneralName of the
// leaf's subjectAltName extension. Names compare by their DER, as the
// issuer of a signer identifier does. crypto/x509 refuses a certificate
// with two extensions of one identifier, so the leaf has at most one
// subjectAltName extension.
func (c *chain) names(tsa []byte) bool {
	r := der.NewReader(tsa)
	if subject, ok := r.Read(der.ContextConstructed(directoryName)); ok && bytes.Equal(subject, c.leaf.RawSubject) {
		return true
	}

	for _, ext := range c.leaf.Extensions {
		if ext.Id.Equal(oidSubjectAltName) {
			return altName(ext.Value, tsa)
		}
	}

	return false
}

// altName reports whether san, the extnValue of a subjectAltName extension,
// has the GeneralName name, by its DER. It reports false for a san whose
// names are not DER.
func altName(san, name []byte) bool {
	r := der.NewReader(san)
	names, _ := r.Read(der.TagSequence)

	nr := der.NewReader(names)
	for !nr.Empty() {
		_, element, _, ok := nr.Next()
		if !ok {
			return false
		}

		if bytes.Equal(element, name) {
			return true
		}
	}

	return false
}

// certificate returns the DER of the certificate of certificates that the
// signing-certificate attributes of attrs name: the first ESSCertIDv2 of
// a SigningCertificateV2, and the first ESSCertID of a SigningCertificate,
// RFC 5035 and RFC 5816. When the token has both, each names the
// certificate. In FIPS 140-only mode, which refuses SHA-1, the SHA-1
// identifier of a SigningCertificate is not checked beside a
// SigningCertificateV2, and a token with only a SigningCertificate returns
// [ErrUnsupported].
func certificate(attrs attributes, certificates []byte) ([]byte, error) {
	sha1Allowed := !fips140.Enforced()

	var leaf []byte

	if attrs.certHashV2 != nil {
		if attrs.hashV2 == 0 {
			return nil, ErrUnsupported
		}

		leaf = find(certificates, attrs.hashV2, attrs.certHashV2)
	} else {
		if attrs.certHashV1 == nil {
			return nil, ErrSignature
		}

		if !sha1Allowed {
			return nil, ErrUnsupported
		}

		leaf = findSHA1(certificates, attrs.certHashV1)
	}

	if leaf == nil {
		return nil, fmt.Errorf("%w: the token does not contain the certificate that it names", ErrCertificate)
	}

	if attrs.certHashV1 != nil && sha1Allowed {
		if sum := sha1.Sum(leaf); !bytes.Equal(sum[:], attrs.certHashV1) { //nolint:gosec // G401: see the import
			return nil, ErrSignature
		}
	}

	return leaf, nil
}

// find returns the DER of the certificate of certificates whose digest d
// is hash, and nil when there is none.
func find(certificates []byte, d digest, hash []byte) []byte {
	var sum [sha512.Size]byte

	r := der.NewReader(certificates)
	for !r.Empty() {
		tag, element, _, ok := r.Next()
		if !ok {
			return nil
		}

		if tag == der.TagSequence && bytes.Equal(d.sum(element, &sum), hash) {
			return element
		}
	}

	return nil
}

// findSHA1 returns the DER of the certificate of certificates whose SHA-1
// digest is hash, and nil when there is none.
func findSHA1(certificates, hash []byte) []byte {
	r := der.NewReader(certificates)
	for !r.Empty() {
		tag, element, _, ok := r.Next()
		if !ok {
			return nil
		}

		if tag != der.TagSequence {
			continue
		}

		if sum := sha1.Sum(element); bytes.Equal(sum[:], hash) { //nolint:gosec // G401: see the import
			return element
		}
	}

	return nil
}

// timeStampingOnly reports whether cert has the one extended key usage
// that RFC 3161 section 2.3 requires of an authority: an extension that
// lists only id-kp-timeStamping and is critical.
func timeStampingOnly(cert *x509.Certificate) bool {
	if len(cert.ExtKeyUsage) != 1 || cert.ExtKeyUsage[0] != x509.ExtKeyUsageTimeStamping ||
		len(cert.UnknownExtKeyUsage) != 0 {

		return false
	}

	for _, ext := range cert.Extensions {
		if ext.Id.Equal(oidExtKeyUsage) {
			return ext.Critical
		}
	}

	return false
}

// serialOf returns the content of the serialNumber INTEGER of tbs, the
// DER of a TBSCertificate that crypto/x509 parsed: the field after the
// optional version.
func serialOf(tbs []byte) []byte {
	r := der.NewReader(tbs)
	seq, _ := r.Read(der.TagSequence)

	sr := der.NewReader(seq)
	_, _, _ = sr.Optional(der.ContextConstructed(0))
	serial, _ := sr.Read(der.TagInteger)

	return serial
}

// laterOf returns the later of a and b.
func laterOf(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}

	return a
}

// earlierOf returns the earlier of a and b.
func earlierOf(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}

	return a
}
