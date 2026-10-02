// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tsptest

import (
	"bytes"
	stdcrypto "crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"fmt"
	"math/big"
	"sync"
	"time"

	"go.thesmos.sh/core/internal/der"
)

// The values of RFC 3161 that an Authority writes, and the validity of its
// certificates.
const (
	// statusGranted is the PKIStatus granted.
	statusGranted = 0

	// statusRejection is the PKIStatus rejection.
	statusRejection = 2

	// failUnacceptedPolicy and failUnacceptedExtension are the bits of
	// PKIFailureInfo of the requests that an Authority rejects.
	failUnacceptedPolicy    = 15
	failUnacceptedExtension = 16

	// directoryName is the tag number of the directoryName choice of a
	// GeneralName.
	directoryName = 4

	// defaultValidity is the validity of an authority's certificate when
	// the Config sets none: 365 days.
	defaultValidity = 8760 * time.Hour

	// caValidity is the validity of the root and the intermediate: 3650
	// days.
	caValidity = 87600 * time.Hour
)

// request is the parts of a TimeStampReq that an Authority reads.
type request struct {
	// imprint and nonce are the whole elements of the messageImprint and
	// the nonce, and policy is the content of the reqPolicy. nonce and
	// policy are nil when the request has none.
	imprint, nonce, policy []byte

	// certReq is the certReq field, and extensions reports whether the
	// request has extensions.
	certReq, extensions bool
}

// parseRequest returns the parts of req, the DER of a TimeStampReq, RFC
// 3161 section 2.4.1. It checks the structure, and leaves the version and
// the content of the imprint to the verifier of the token.
//
// Error modes: a request that is not a TimeStampReq returns [ErrRequest].
func parseRequest(req []byte) (request, error) {
	var r request

	rd := der.NewReader(req)

	seq, ok := rd.Read(der.TagSequence)
	if !ok || !rd.Empty() {
		return request{}, ErrRequest
	}

	s := der.NewReader(seq)
	if _, ok = s.Read(der.TagInteger); !ok {
		return request{}, ErrRequest
	}

	if r.imprint, _, ok = s.ReadElement(der.TagSequence); !ok {
		return request{}, ErrRequest
	}

	if r.policy, _, ok = s.Optional(der.TagOID); !ok {
		return request{}, ErrRequest
	}

	nonce, present, ok := s.Optional(der.TagInteger)
	if !ok {
		return request{}, ErrRequest
	}

	if present {
		r.nonce = element(der.TagInteger, nonce)
	}

	certReq, present, ok := s.Optional(der.TagBoolean)
	if !ok {
		return request{}, ErrRequest
	}

	if present {
		r.certReq, _ = der.Boolean(certReq)
	}

	_, r.extensions, ok = s.Optional(der.ContextConstructed(0))
	if !ok || !s.Empty() {
		return request{}, ErrRequest
	}

	return r, nil
}

// Authority is an RFC 3161 time-stamp authority for tests. New creates its
// certificate chain in memory: a root, an intermediate, and the
// authority's certificate, each with a key of its own, so a verifier
// trusts [Authority.Roots] and verifies a token without a network.
//
// [Authority.Respond] responds to a request as the HTTP endpoint of an
// authority does. [Authority.Token] builds the token of a [Spec], whose
// fields replace the parts of a token that a malformed or forged token
// changes.
//
// # Concurrency
//
// Safe for concurrent use. A mutex guards the serial number of the tokens,
// and the certificates and keys do not change after New.
//
// # Allocation contract
//
// Test support: each method allocates its result, and New allocates the
// keys and the certificates.
type Authority struct {
	root, intermediate, leaf *x509.Certificate
	roots                    *x509.CertPool
	signer                   stdcrypto.Signer

	// policy is the content of the Config's Policy in DER, and serial the
	// content of the serialNumber of the authority's certificate.
	policy, serial []byte

	cfg Config

	mu sync.Mutex

	// next is the serial number of the last TSTInfo.
	next uint64
}

// New returns an Authority of cfg with a new certificate chain: a root and
// an intermediate that are valid for 3650 days, and the authority's
// certificate, valid for cfg.Validity, each from an hour before the
// clock's time. The root and the intermediate sign with ECDSA on P-256.
// The authority's certificate has the key of cfg.Key, the serial number 3,
// the subject key identifier 01020304, and the extended key usage of
// cfg.Usage.
//
// Error modes: a Config without a Clock or a Policy, or with a Key, a
// Digest, an ESS or a Usage that is not Valid, returns [ErrConfig],
// classified Invalid. A failure to generate a key or to create a
// certificate returns its error, wrapped.
func New(cfg Config) (*Authority, error) {
	policy, err := cfg.Policy.AppendBinary(nil)
	if err != nil || cfg.Clock == nil || len(policy) == 0 {
		return nil, ErrConfig
	}

	if !cfg.Key.Valid() || !cfg.Digest.Valid() || !cfg.ESS.Valid() || !cfg.Usage.Valid() {
		return nil, ErrConfig
	}

	signer, err := cfg.Key.generate()
	if err != nil {
		return nil, err
	}

	a := &Authority{cfg: cfg, signer: signer, policy: policy}

	err = a.issue()
	if err != nil {
		return nil, err
	}

	a.roots = x509.NewCertPool()
	a.roots.AddCert(a.root)
	a.serial = serialOf(a.leaf.RawTBSCertificate)

	return a, nil
}

// Roots returns a pool of the Authority's root: the Roots of a verifier of
// its tokens. The caller does not change the pool.
func (a *Authority) Roots() *x509.CertPool {
	return a.roots
}

// Leaf returns the authority's certificate, whose key signs the tokens.
func (a *Authority) Leaf() *x509.Certificate {
	return a.leaf
}

// Intermediate returns the certificate between the root and the
// authority's certificate, which a token contains unless its Spec replaces
// the certificates.
func (a *Authority) Intermediate() *x509.Certificate {
	return a.intermediate
}

// Respond returns the DER of a TimeStampResp for req, the DER of a
// TimeStampReq, as the HTTP endpoint of an authority returns it:
//
//   - A request of the Config's policy, or of none, gets a token that
//     repeats its message imprint and nonce, with the next serial number
//     and the clock's time. The token contains the authority's certificate
//     and the intermediate when certReq is TRUE.
//   - A request of another policy gets the rejection unacceptedPolicy.
//   - A request with extensions gets the rejection unacceptedExtension.
//
// Error modes: a request that is not a TimeStampReq returns [ErrRequest],
// classified Invalid, and a failure to sign returns its error, wrapped.
func (a *Authority) Respond(req []byte) ([]byte, error) {
	r, err := parseRequest(req)
	if err != nil {
		return nil, err
	}

	if r.extensions {
		return StatusResponse(statusRejection, []int{failUnacceptedExtension}, "extensions are not supported"), nil
	}

	if r.policy != nil && !bytes.Equal(r.policy, a.policy) {
		return StatusResponse(statusRejection, []int{failUnacceptedPolicy}, "the policy is not supported"), nil
	}

	tok, err := a.Token(Spec{TSTInfo: a.tstInfo(r), NoCertificates: !r.certReq})
	if err != nil {
		return nil, err
	}

	return grantedResponse(tok), nil
}

// TSTInfo returns the DER of the TSTInfo that Respond signs for req, with
// the next serial number, so a test builds a token of its own from it with
// [Authority.Token].
//
// Error modes: a request that is not a TimeStampReq returns [ErrRequest],
// classified Invalid.
func (a *Authority) TSTInfo(req []byte) ([]byte, error) {
	r, err := parseRequest(req)
	if err != nil {
		return nil, err
	}

	return a.tstInfo(r), nil
}

// tstInfo returns the DER of the TSTInfo of r: version 1, the Config's
// policy, the request's imprint, the next serial number, the clock's time,
// the optional fields of the Config, and the request's nonce.
func (a *Authority) tstInfo(r request) []byte {
	a.mu.Lock()
	a.next++
	serial := a.next
	a.mu.Unlock()

	b := der.NewBuilder(nil)
	seq := b.Open(der.TagSequence)
	b.AddUint64(1)
	b.Add(der.TagOID, a.policy)
	b.AddElement(r.imprint)
	b.AddUint64(serial)
	b.AddGeneralizedTime(a.cfg.Clock.Time())

	if a.cfg.Accuracy > 0 {
		addAccuracy(&b, a.cfg.Accuracy)
	}

	if a.cfg.Ordering {
		b.AddBoolean(true)
	}

	b.AddElement(r.nonce)

	if a.cfg.TSA {
		tsa := b.Open(der.ContextConstructed(0))
		name := b.Open(der.ContextConstructed(directoryName))
		b.AddElement(a.leaf.RawSubject)
		b.Close(name)
		b.Close(tsa)
	}

	if len(a.cfg.Extensions) > 0 {
		exts := b.Open(der.ContextConstructed(1))
		for _, e := range a.cfg.Extensions {
			ext := b.Open(der.TagSequence)
			b.Add(der.TagOID, oidContent(e.Id))

			if e.Critical {
				b.AddBoolean(true)
			}

			b.Add(der.TagOctetString, e.Value)
			b.Close(ext)
		}

		b.Close(exts)
	}

	b.Close(seq)

	return b.Bytes()
}

// issue creates the root, the intermediate and the authority's certificate
// of the Authority, as New describes them.
//
// Error modes: the error of a key generation or of crypto/x509, wrapped.
func (a *Authority) issue() error {
	now := a.cfg.Clock.Time()
	from := now.Add(-time.Hour)

	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("tsptest: generate the root's key: %w", err)
	}

	root := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "tsptest root"},
		NotBefore:             from,
		NotAfter:              now.Add(caValidity),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	if a.root, err = create(root, root, rootKey.Public(), rootKey); err != nil {
		return err
	}

	interKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("tsptest: generate the intermediate's key: %w", err)
	}

	inter := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "tsptest intermediate"},
		NotBefore:             from,
		NotAfter:              now.Add(caValidity),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	if a.intermediate, err = create(inter, a.root, interKey.Public(), rootKey); err != nil {
		return err
	}

	validity := a.cfg.Validity
	if validity == 0 {
		validity = defaultValidity
	}

	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "tsptest authority"},
		NotBefore:    from,
		NotAfter:     now.Add(validity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		SubjectKeyId: []byte{1, 2, 3, 4},
	}

	err = a.cfg.Usage.apply(leaf)
	if err != nil {
		return err
	}

	a.leaf, err = create(leaf, a.intermediate, a.signer.Public(), interKey)

	return err
}

// addAccuracy appends the Accuracy of d: its whole seconds, milliseconds
// and microseconds, each when it is not zero. A part of d below a
// microsecond is left out.
func addAccuracy(b *der.Builder, d time.Duration) {
	acc := b.Open(der.TagSequence)
	if s := d / time.Second; s > 0 {
		b.AddUint64(uint64(s))
	}

	if ms := d % time.Second / time.Millisecond; ms > 0 {
		b.Add(der.Context(0), integer(uint64(ms)))
	}

	if us := d % time.Millisecond / time.Microsecond; us > 0 {
		b.Add(der.Context(1), integer(uint64(us)))
	}

	b.Close(acc)
}

// create returns the certificate of template, issued by parent with key,
// the issuer's private key, for the public key pub.
//
// Error modes: the error of crypto/x509, wrapped.
func create(
	template, parent *x509.Certificate,
	pub stdcrypto.PublicKey,
	key stdcrypto.Signer,
) (*x509.Certificate, error) {
	raw, err := x509.CreateCertificate(rand.Reader, template, parent, pub, key)
	if err != nil {
		return nil, fmt.Errorf("tsptest: create a certificate: %w", err)
	}

	cert, err := x509.ParseCertificate(raw)
	if err != nil {
		return nil, fmt.Errorf("tsptest: parse a certificate: %w", err)
	}

	return cert, nil
}

// serialOf returns the content of the serialNumber of tbs, the DER of a
// TBSCertificate that crypto/x509 created: the INTEGER after the optional
// version.
func serialOf(tbs []byte) []byte {
	r := der.NewReader(tbs)
	seq, _ := r.Read(der.TagSequence)

	sr := der.NewReader(seq)
	_, _, _ = sr.Optional(der.ContextConstructed(0))
	serial, _ := sr.Read(der.TagInteger)

	return serial
}

// oidContent returns the content of id in DER, and nil for an identifier
// that encoding/asn1 does not encode, which a test does not pass.
func oidContent(id asn1.ObjectIdentifier) []byte {
	raw, _ := asn1.Marshal(id) //nolint:errcheck // the content of an identifier that does not encode is nil

	r := der.NewReader(raw)
	content, _ := r.Read(der.TagOID)

	return content
}
