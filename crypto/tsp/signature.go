// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package tsp

import (
	"bytes"
	stdcrypto "crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/mldsa"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"

	"go.thesmos.sh/core/internal/der"
	"go.thesmos.sh/core/pool"
)

// maxSaltLength is the largest salt of an RSASSA-PSS signature that a
// token may state: the size of SHA-512, which RFC 4055 section 3.1
// recommends as the salt of the strongest hash.
const maxSaltLength = 64

// defaultSaltLength is the saltLength of RSASSA-PSS-params when the
// parameters omit it, RFC 4055 section 3.1.
const defaultSaltLength = 20

// digests pools the arrays into which verifyDigest computes a digest.
// crypto/rsa and crypto/ecdsa take the digest as a slice that escapes in
// the compiler's escape analysis, so an array on the stack of
// verifyDigest moves to the heap on every call, and a pooled array does
// not. The digest of public data needs no clearing before Put.
var digests = pool.NewPool(func() *[sha512.Size]byte { return new([sha512.Size]byte) })

// digest is a hash that this package computes for CMS: the digest of the
// encapsulated content, of the signed attributes, and of a certificate.
type digest uint8

// The digests of this package.
const (
	// digestSHA256 is SHA-256.
	digestSHA256 digest = 1

	// digestSHA384 is SHA-384.
	digestSHA384 digest = 2

	// digestSHA512 is SHA-512.
	digestSHA512 digest = 3
)

// digestOf returns the digest that a names: SHA-256, SHA-384 or SHA-512,
// with no parameters or NULL parameters. It reports false for any other
// algorithm.
func digestOf(a algorithmIdentifier) (digest, bool) {
	if !a.noParams() {
		return 0, false
	}

	switch {
	case bytes.Equal(a.oid, oidSHA256):
		return digestSHA256, true
	case bytes.Equal(a.oid, oidSHA384):
		return digestSHA384, true
	case bytes.Equal(a.oid, oidSHA512):
		return digestSHA512, true
	default:
		return 0, false
	}
}

// sum computes the digest d of m into dst, and returns the digest: the
// first 32, 48 or 64 bytes of dst.
func (d digest) sum(m []byte, dst *[sha512.Size]byte) []byte {
	switch d {
	case digestSHA256:
		s := sha256.Sum256(m)

		return dst[:copy(dst[:], s[:])]
	case digestSHA384:
		s := sha512.Sum384(m)

		return dst[:copy(dst[:], s[:])]
	default:
		s := sha512.Sum512(m)

		return dst[:copy(dst[:], s[:])]
	}
}

// size returns the size of a digest d in bytes: 32, 48 or 64.
func (d digest) size() int {
	switch d {
	case digestSHA256:
		return sha256.Size
	case digestSHA384:
		return sha512.Size384
	default:
		return sha512.Size
	}
}

// std returns d as a hash of the standard library, which the RSA
// signatures of crypto/rsa take.
func (d digest) std() stdcrypto.Hash {
	switch d {
	case digestSHA256:
		return stdcrypto.SHA256
	case digestSHA384:
		return stdcrypto.SHA384
	default:
		return stdcrypto.SHA512
	}
}

// scheme is a signature scheme that this package verifies.
type scheme uint8

// The signature schemes of this package.
const (
	// schemePKCS1v15 is RSASSA-PKCS1-v1_5 over a digest, RFC 8017.
	schemePKCS1v15 scheme = 1

	// schemePSS is RSASSA-PSS over a digest, RFC 8017 and RFC 4055.
	schemePSS scheme = 2

	// schemeECDSA is ECDSA over a digest, FIPS 186-5.
	schemeECDSA scheme = 3

	// schemeEd25519 is Ed25519 over the message, RFC 8032 and RFC 8419.
	schemeEd25519 scheme = 4

	// schemeMLDSA is ML-DSA over the message with the empty context,
	// FIPS 204 and RFC 9882.
	schemeMLDSA scheme = 5
)

// signatureAlgorithm is the signature algorithm of a SignerInfo, with the
// parameters that its verification needs.
type signatureAlgorithm struct {
	// mldsa is the parameter set of an ML-DSA signature.
	mldsa mldsa.Parameters

	// salt is the salt length of an RSASSA-PSS signature.
	salt int

	scheme scheme

	// hash is the digest that a scheme over a digest signs.
	hash digest
}

// signatureOf returns the signature algorithm of s, the
// signatureAlgorithm of a SignerInfo whose digestAlgorithm is d. It
// reports false for an algorithm that this package does not verify, for
// parameters that the algorithm's RFC does not allow, and for a d that
// does not fit the algorithm:
//
//   - rsaEncryption signs with d, RFC 5754 section 3.2, and
//     sha256WithRSAEncryption and its siblings with their own hash, which
//     is d.
//   - RSASSA-PSS states its hash, which is d, an MGF1 of the same hash, a
//     salt of at most 64 octets, and the trailer 1.
//   - ecdsa-with-SHA256 and its siblings sign with their own hash, which is
//     d.
//   - Ed25519 requires d to be SHA-512, RFC 8419 section 3.
//   - ML-DSA-44 accepts SHA-256, SHA-384 and SHA-512 as d, ML-DSA-65
//     SHA-384 and SHA-512, and ML-DSA-87 SHA-512, RFC 9882 section 3.3.
func signatureOf(s algorithmIdentifier, d digest) (signatureAlgorithm, bool) {
	switch {
	case bytes.Equal(s.oid, oidRSAEncryption):
		return signatureAlgorithm{scheme: schemePKCS1v15, hash: d}, s.noParams()
	case bytes.Equal(s.oid, oidSHA256WithRSA):
		return signatureAlgorithm{scheme: schemePKCS1v15, hash: d}, s.noParams() && d == digestSHA256
	case bytes.Equal(s.oid, oidSHA384WithRSA):
		return signatureAlgorithm{scheme: schemePKCS1v15, hash: d}, s.noParams() && d == digestSHA384
	case bytes.Equal(s.oid, oidSHA512WithRSA):
		return signatureAlgorithm{scheme: schemePKCS1v15, hash: d}, s.noParams() && d == digestSHA512
	case bytes.Equal(s.oid, oidRSAPSS):
		return pssOf(s.params, d)
	case bytes.Equal(s.oid, oidECDSAWithSHA256):
		return signatureAlgorithm{scheme: schemeECDSA, hash: d}, s.absentParams() && d == digestSHA256
	case bytes.Equal(s.oid, oidECDSAWithSHA384):
		return signatureAlgorithm{scheme: schemeECDSA, hash: d}, s.absentParams() && d == digestSHA384
	case bytes.Equal(s.oid, oidECDSAWithSHA512):
		return signatureAlgorithm{scheme: schemeECDSA, hash: d}, s.absentParams() && d == digestSHA512
	case bytes.Equal(s.oid, oidEd25519):
		return signatureAlgorithm{scheme: schemeEd25519}, s.absentParams() && d == digestSHA512
	case bytes.Equal(s.oid, oidMLDSA44):
		return signatureAlgorithm{scheme: schemeMLDSA, mldsa: mldsa.MLDSA44()}, s.absentParams()
	case bytes.Equal(s.oid, oidMLDSA65):
		return signatureAlgorithm{scheme: schemeMLDSA, mldsa: mldsa.MLDSA65()}, s.absentParams() && d != digestSHA256
	case bytes.Equal(s.oid, oidMLDSA87):
		return signatureAlgorithm{scheme: schemeMLDSA, mldsa: mldsa.MLDSA87()}, s.absentParams() && d == digestSHA512
	default:
		return signatureAlgorithm{}, false
	}
}

// pssOf returns the RSASSA-PSS algorithm of params, the whole element of
// RSASSA-PSS-params, RFC 4055 section 3.1, for the digest d. The
// parameters state the hash d, an MGF1 of d, an optional salt length of at
// most maxSaltLength that defaults to 20, and an optional trailer field
// of 1. The hash and the mask generation have defaults of SHA-1, which
// this package does not accept, so the parameters state both.
func pssOf(params []byte, d digest) (signatureAlgorithm, bool) {
	r := der.NewReader(params)

	seq, ok := r.Read(der.TagSequence)
	//dokimi:mutate-skip lcr-right: a failed Read leaves r unchanged, and the nil content of an empty r has no hash
	if !ok || !r.Empty() {
		return signatureAlgorithm{}, false
	}

	pr := der.NewReader(seq)

	hash, ok := explicitAlgorithm(&pr, 0)
	h, known := digestOf(hash)
	//dokimi:mutate-skip lcr-left: digestOf returns the zero digest for an unknown algorithm, which differs from d
	if !ok || !known {
		return signatureAlgorithm{}, false
	}

	if h != d {
		return signatureAlgorithm{}, false
	}

	mgf, ok := explicitAlgorithm(&pr, 1)
	//dokimi:mutate-skip lcr-right: a failed explicitAlgorithm returns no OID, which differs from oidMGF1
	if !ok || !bytes.Equal(mgf.oid, oidMGF1) {
		return signatureAlgorithm{}, false
	}

	if !mgfHash(mgf.params, d) {
		return signatureAlgorithm{}, false
	}

	salt, ok := explicitUint(&pr, 2, defaultSaltLength)
	if !ok || salt > maxSaltLength {
		return signatureAlgorithm{}, false
	}

	trailer, ok := explicitUint(&pr, 3, 1)
	//dokimi:mutate-skip lcr-right: explicitUint returns 0 when it reports false, which is not the trailer 1
	if !ok || trailer != 1 {
		return signatureAlgorithm{}, false
	}

	if !pr.Empty() {
		return signatureAlgorithm{}, false
	}

	return signatureAlgorithm{scheme: schemePSS, hash: d, salt: int(salt)}, true
}

// mgfHash reports whether params, the parameters of an MGF1, name the
// digest d.
func mgfHash(params []byte, d digest) bool {
	r := der.NewReader(params)

	seq, ok := r.Read(der.TagSequence)
	//dokimi:mutate-skip lcr-right: a failed Read leaves r unchanged, and parseAlgorithm refuses the nil content of an empty r
	if !ok || !r.Empty() {
		return false
	}

	alg, ok := parseAlgorithm(seq)
	h, known := digestOf(alg)

	//dokimi:mutate-skip lcr-left,lcr-right,sbr-delete: digestOf returns the zero digest for an algorithm that is unknown or does not parse, which differs from d
	if !ok || !known {
		return false
	}

	return h == d
}

// explicitAlgorithm reads the AlgorithmIdentifier of the [n] EXPLICIT
// element from r, and reports false when the element is absent or is not
// one.
func explicitAlgorithm(r *der.Reader, n uint8) (algorithmIdentifier, bool) {
	content, ok := r.Read(der.ContextConstructed(n))
	//dokimi:mutate-skip sbr-delete: a failed Read returns nil, which readAlgorithm refuses
	if !ok {
		return algorithmIdentifier{}, false
	}

	er := der.NewReader(content)

	a, ok := readAlgorithm(&er)

	//dokimi:mutate-skip lcr-right: a failed readAlgorithm returns the zero AlgorithmIdentifier, which every caller refuses
	return a, ok && er.Empty()
}

// explicitUint reads the INTEGER of the optional [n] EXPLICIT element from
// r, and returns def when the element is absent.
func explicitUint(r *der.Reader, n uint8, def uint64) (uint64, bool) {
	content, present, ok := r.Optional(der.ContextConstructed(n))
	//dokimi:mutate-skip sbr-delete: a malformed element stays in r, which the check of pssOf that the parameters end refuses
	if !ok {
		return 0, false
	}

	if !present {
		return def, true
	}

	er := der.NewReader(content)

	v, ok := er.Read(der.TagInteger)
	//dokimi:mutate-skip lcr-right: a failed Read returns nil, which der.Uint64 refuses
	if !ok || !er.Empty() {
		return 0, false
	}

	return der.Uint64(v)
}

// verify reports whether value is a signature of key over m, the signed
// attributes of a SignerInfo with the tag of a SET, RFC 5652 section 5.4,
// under a. A key of another type than the scheme's, or an ML-DSA key of
// another parameter set, does not verify.
//
// # Allocation contract
//
// Ed25519 and ML-DSA allocate nothing. RSA and ECDSA allocate what
// crypto/rsa and crypto/ecdsa allocate to verify.
func (a signatureAlgorithm) verify(key stdcrypto.PublicKey, m, value []byte) bool {
	switch a.scheme {
	case schemeEd25519:
		pub, ok := key.(ed25519.PublicKey)

		return ok && ed25519.Verify(pub, m, value)
	case schemeMLDSA:
		pub, ok := key.(*mldsa.PublicKey)

		return ok && pub.Parameters() == a.mldsa && mldsa.Verify(pub, m, value, nil) == nil
	default:
		return a.verifyDigest(key, m, value)
	}
}

// verifyDigest reports whether value is a signature of key over the digest
// of m under a, whose scheme is RSASSA-PKCS1-v1_5, RSASSA-PSS or ECDSA. It
// computes the digest into an array of digests.
func (a signatureAlgorithm) verifyDigest(key stdcrypto.PublicKey, m, value []byte) bool {
	buf := digests.Get()
	defer digests.Put(buf)

	digest := a.hash.sum(m, buf)

	switch a.scheme {
	case schemePKCS1v15:
		pub, ok := key.(*rsa.PublicKey)

		return ok && rsa.VerifyPKCS1v15(pub, a.hash.std(), digest, value) == nil
	case schemePSS:
		pub, ok := key.(*rsa.PublicKey)
		opts := rsa.PSSOptions{SaltLength: a.salt, Hash: a.hash.std()}

		return ok && rsa.VerifyPSS(pub, a.hash.std(), digest, value, &opts) == nil
	default:
		pub, ok := key.(*ecdsa.PublicKey)

		return ok && ecdsa.VerifyASN1(pub, digest, value)
	}
}
