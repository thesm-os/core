// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package tsptest

import (
	stdcrypto "crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/mldsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"fmt"

	"go.thesmos.sh/core/internal/der"
)

// rsaBits is the size of the RSA keys of an Authority.
const rsaBits = 2048

// nullParams is the DER of NULL, the parameters of an RSA signature
// algorithm of PKCS #1 v1.5.
var nullParams = []byte{byte(der.TagNull), 0x00}

// Digest names the digest algorithm of an authority's SignerInfo: the
// digest of the TSTInfo in the message-digest attribute, the digest of the
// signed attributes that RSA and ECDSA sign, and the hash of the
// certificate in a SigningCertificateV2. The zero Digest is not valid.
type Digest uint8

// The digests of an Authority.
const (
	// DigestSHA256 is SHA-256.
	DigestSHA256 Digest = 1

	// DigestSHA384 is SHA-384.
	DigestSHA384 Digest = 2

	// DigestSHA512 is SHA-512.
	DigestSHA512 Digest = 3
)

// Valid reports whether d is one of the Digests of this package.
func (d Digest) Valid() bool {
	return d >= DigestSHA256 && d <= DigestSHA512
}

// choose returns sha256, sha384 or sha512 by d, a valid Digest.
func (d Digest) choose(sha256, sha384, sha512 []byte) []byte {
	switch d {
	case DigestSHA384:
		return sha384
	case DigestSHA512:
		return sha512
	default:
		return sha256
	}
}

// oid returns the content of the OID of d in DER.
func (d Digest) oid() []byte {
	return d.choose(oidSHA256, oidSHA384, oidSHA512)
}

// sum returns the digest d of m.
func (d Digest) sum(m []byte) []byte {
	switch d {
	case DigestSHA384:
		s := sha512.Sum384(m)

		return s[:]
	case DigestSHA512:
		s := sha512.Sum512(m)

		return s[:]
	default:
		s := sha256.Sum256(m)

		return s[:]
	}
}

// hash returns d as a hash of the standard library.
func (d Digest) hash() stdcrypto.Hash {
	switch d {
	case DigestSHA384:
		return stdcrypto.SHA384
	case DigestSHA512:
		return stdcrypto.SHA512
	default:
		return stdcrypto.SHA256
	}
}

// pssParams returns the DER of RSASSA-PSS-params for d: the hash d, MGF1
// of d, and a salt of the size of d.
func (d Digest) pssParams() []byte {
	b := der.NewBuilder(nil)
	params := b.Open(der.TagSequence)
	hash := b.Open(der.ContextConstructed(0))
	b.AddElement(algorithm(d.oid(), nil))
	b.Close(hash)
	mgf := b.Open(der.ContextConstructed(1))
	b.AddElement(algorithm(oidMGF1, algorithm(d.oid(), nil)))
	b.Close(mgf)
	salt := b.Open(der.ContextConstructed(2))
	b.AddUint64(uint64(d.hash().Size())) //nolint:gosec // G115: a digest's size is at most 64
	b.Close(salt)
	b.Close(params)

	return b.Bytes()
}

// Key names the key of an authority's certificate, and so the signature
// algorithm of its tokens. The zero Key is not valid.
type Key uint8

// The keys of an Authority.
const (
	// KeyEd25519 signs with Ed25519, RFC 8419, which requires the Digest
	// SHA-512.
	KeyEd25519 Key = 1

	// KeyECDSAP256 signs with ECDSA on P-256 under ecdsa-with-SHA256 or
	// its sibling of the Digest.
	KeyECDSAP256 Key = 2

	// KeyECDSAP384 signs with ECDSA on P-384 under ecdsa-with-SHA384 or
	// its sibling of the Digest.
	KeyECDSAP384 Key = 3

	// KeyRSAPKCS1 signs with RSASSA-PKCS1-v1_5 and a 2048-bit key, under
	// sha256WithRSAEncryption or its sibling of the Digest.
	KeyRSAPKCS1 Key = 4

	// KeyRSAPSS signs with RSASSA-PSS, a 2048-bit key, MGF1 of the Digest
	// and a salt of the Digest's size.
	KeyRSAPSS Key = 5

	// KeyMLDSA44 signs with ML-DSA-44, RFC 9882.
	KeyMLDSA44 Key = 6

	// KeyMLDSA65 signs with ML-DSA-65, RFC 9882.
	KeyMLDSA65 Key = 7

	// KeyMLDSA87 signs with ML-DSA-87, RFC 9882.
	KeyMLDSA87 Key = 8
)

// Valid reports whether k is one of the Keys of this package.
func (k Key) Valid() bool {
	return k >= KeyEd25519 && k <= KeyMLDSA87
}

// generate returns a new private key of k, a valid Key. An RSA key of 2048
// bits takes tens of milliseconds, and the other keys microseconds.
//
// Error modes: the error of the generator, wrapped.
func (k Key) generate() (stdcrypto.Signer, error) {
	var (
		key stdcrypto.Signer
		err error
	)

	switch k {
	case KeyEd25519:
		_, key, err = ed25519.GenerateKey(rand.Reader)
	case KeyECDSAP256:
		key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case KeyECDSAP384:
		key, err = ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	case KeyRSAPKCS1, KeyRSAPSS:
		key, err = rsa.GenerateKey(rand.Reader, rsaBits)
	case KeyMLDSA44:
		key, err = mldsa.GenerateKey(mldsa.MLDSA44())
	case KeyMLDSA65:
		key, err = mldsa.GenerateKey(mldsa.MLDSA65())
	default:
		key, err = mldsa.GenerateKey(mldsa.MLDSA87())
	}

	if err != nil {
		return nil, fmt.Errorf("tsptest: generate a key: %w", err)
	}

	return key, nil
}

// algorithm returns the DER of the signatureAlgorithm of a SignerInfo that
// k, a valid Key, signs with the digest d.
func (k Key) algorithm(d Digest) []byte {
	switch k {
	case KeyEd25519:
		return algorithm(oidEd25519, nil)
	case KeyECDSAP256, KeyECDSAP384:
		return algorithm(d.choose(oidECDSAWithSHA256, oidECDSAWithSHA384, oidECDSAWithSHA512), nil)
	case KeyRSAPKCS1:
		return algorithm(d.choose(oidSHA256WithRSA, oidSHA384WithRSA, oidSHA512WithRSA), nullParams)
	case KeyRSAPSS:
		return algorithm(oidRSAPSS, d.pssParams())
	case KeyMLDSA44:
		return algorithm(oidMLDSA44, nil)
	case KeyMLDSA65:
		return algorithm(oidMLDSA65, nil)
	default:
		return algorithm(oidMLDSA87, nil)
	}
}

// sign returns the signature of signer, a private key of k, over m, the
// signed attributes with the tag of a SET. Ed25519 and ML-DSA sign m with
// the empty context, and RSA and ECDSA sign the digest d of m.
//
// Error modes: the error of the signer, wrapped.
func (k Key) sign(signer stdcrypto.Signer, m []byte, d Digest) ([]byte, error) {
	var (
		message = m
		opts    stdcrypto.SignerOpts
	)

	switch k {
	case KeyEd25519:
		opts = stdcrypto.Hash(0)
	case KeyMLDSA44, KeyMLDSA65, KeyMLDSA87:
		opts = &mldsa.Options{}
	case KeyRSAPSS:
		message, opts = d.sum(m), &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash, Hash: d.hash()}
	default:
		message, opts = d.sum(m), d.hash()
	}

	signature, err := signer.Sign(rand.Reader, message, opts)
	if err != nil {
		return nil, fmt.Errorf("tsptest: sign: %w", err)
	}

	return signature, nil
}
