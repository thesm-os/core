// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tsp

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
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/internal/der"
)

// signedMessage is the message that the cases of verify sign.
var signedMessage = []byte("the signed attributes")

// nullElement is the DER of NULL, the parameters that a hash may state.
var nullElement = []byte{0x05, 0x00}

func TestSignature(t *testing.T) {
	t.Parallel()

	t.Run("digestOf", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give algorithmIdentifier
			want digest
			ok   bool
		}{
			{
				name: "returns SHA-256 for id-sha256",
				give: algorithmIdentifier{oid: oidSHA256},
				want: digestSHA256,
				ok:   true,
			},
			{
				name: "returns SHA-384 for id-sha384",
				give: algorithmIdentifier{oid: oidSHA384},
				want: digestSHA384,
				ok:   true,
			},
			{
				name: "returns SHA-512 for id-sha512",
				give: algorithmIdentifier{oid: oidSHA512},
				want: digestSHA512,
				ok:   true,
			},
			{
				name: "returns SHA-256 for id-sha256 with NULL parameters",
				give: algorithmIdentifier{oid: oidSHA256, params: nullElement},
				want: digestSHA256,
				ok:   true,
			},
			{
				name: "reports false for parameters other than NULL",
				give: algorithmIdentifier{oid: oidSHA256, params: el(der.TagSequence)},
			},
			{name: "reports false for another algorithm", give: algorithmIdentifier{oid: oidUnknown}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, ok := digestOf(tt.give)
				testkit.Equal(t, got, tt.want, "digestOf must return the digest of the OID")
				testkit.Equal(t, ok, tt.ok, "digestOf must report whether it knows the algorithm")
			})
		}
	})

	t.Run("sum", func(t *testing.T) {
		t.Parallel()

		s256, s384, s512 := sha256.Sum256(signedMessage), sha512.Sum384(signedMessage), sha512.Sum512(signedMessage)
		tests := []struct {
			name string
			give digest
			want []byte
		}{
			{name: "returns the SHA-256 of m", give: digestSHA256, want: s256[:]},
			{name: "returns the SHA-384 of m", give: digestSHA384, want: s384[:]},
			{name: "returns the SHA-512 of m", give: digestSHA512, want: s512[:]},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				var buf [sha512.Size]byte
				testkit.Equal(t, tt.give.sum(signedMessage, &buf), tt.want, "sum must return the digest of m")
			})
		}
	})

	t.Run("size", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give digest
			want int
		}{
			{name: "returns 32 for SHA-256", give: digestSHA256, want: sha256.Size},
			{name: "returns 48 for SHA-384", give: digestSHA384, want: sha512.Size384},
			{name: "returns 64 for SHA-512", give: digestSHA512, want: sha512.Size},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(t, tt.give.size(), tt.want, "size must return the size of the digest")
			})
		}
	})

	t.Run("std", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give digest
			want stdcrypto.Hash
		}{
			{name: "returns crypto.SHA256 for SHA-256", give: digestSHA256, want: stdcrypto.SHA256},
			{name: "returns crypto.SHA384 for SHA-384", give: digestSHA384, want: stdcrypto.SHA384},
			{name: "returns crypto.SHA512 for SHA-512", give: digestSHA512, want: stdcrypto.SHA512},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(t, tt.give.std(), tt.want, "std must return the hash of the standard library")
			})
		}
	})

	t.Run("signatureOf", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			oid    []byte
			params []byte
			d      digest
			want   scheme
			ok     bool
		}{
			{
				name: "returns PKCS #1 v1.5 for rsaEncryption",
				oid:  oidRSAEncryption,
				d:    digestSHA384,
				want: schemePKCS1v15,
				ok:   true,
			},
			{
				name: "returns PKCS #1 v1.5 for rsaEncryption with NULL parameters",
				oid:  oidRSAEncryption, params: nullElement, d: digestSHA256, want: schemePKCS1v15, ok: true,
			},
			{
				name:   "reports false for rsaEncryption with other parameters",
				oid:    oidRSAEncryption,
				params: el(der.TagSequence),
				d:      digestSHA256,
			},
			{
				name:   "returns PKCS #1 v1.5 for sha256WithRSAEncryption and SHA-256",
				oid:    oidSHA256WithRSA,
				params: nullElement,
				d:      digestSHA256,
				want:   schemePKCS1v15,
				ok:     true,
			},
			{
				name:   "reports false for sha256WithRSAEncryption and SHA-384",
				oid:    oidSHA256WithRSA,
				params: nullElement,
				d:      digestSHA384,
			},
			{
				name:   "reports false for sha256WithRSAEncryption with other parameters",
				oid:    oidSHA256WithRSA,
				params: el(der.TagSequence),
				d:      digestSHA256,
			},
			{
				name: "returns PKCS #1 v1.5 for sha384WithRSAEncryption and SHA-384",
				oid:  oidSHA384WithRSA,
				d:    digestSHA384,
				want: schemePKCS1v15,
				ok:   true,
			},
			{name: "reports false for sha384WithRSAEncryption and SHA-256", oid: oidSHA384WithRSA, d: digestSHA256},
			{
				name: "returns PKCS #1 v1.5 for sha512WithRSAEncryption and SHA-512",
				oid:  oidSHA512WithRSA,
				d:    digestSHA512,
				want: schemePKCS1v15,
				ok:   true,
			},
			{name: "reports false for sha512WithRSAEncryption and SHA-256", oid: oidSHA512WithRSA, d: digestSHA256},
			{
				name: "returns ECDSA for ecdsa-with-SHA256 and SHA-256",
				oid:  oidECDSAWithSHA256,
				d:    digestSHA256,
				want: schemeECDSA,
				ok:   true,
			},
			{name: "reports false for ecdsa-with-SHA256 and SHA-384", oid: oidECDSAWithSHA256, d: digestSHA384},
			{
				name:   "reports false for ecdsa-with-SHA256 with NULL parameters",
				oid:    oidECDSAWithSHA256,
				params: nullElement,
				d:      digestSHA256,
			},
			{
				name: "returns ECDSA for ecdsa-with-SHA384 and SHA-384",
				oid:  oidECDSAWithSHA384,
				d:    digestSHA384,
				want: schemeECDSA,
				ok:   true,
			},
			{name: "reports false for ecdsa-with-SHA384 and SHA-512", oid: oidECDSAWithSHA384, d: digestSHA512},
			{
				name: "returns ECDSA for ecdsa-with-SHA512 and SHA-512",
				oid:  oidECDSAWithSHA512,
				d:    digestSHA512,
				want: schemeECDSA,
				ok:   true,
			},
			{name: "reports false for ecdsa-with-SHA512 and SHA-384", oid: oidECDSAWithSHA512, d: digestSHA384},
			{
				name: "returns Ed25519 for id-Ed25519 and SHA-512",
				oid:  oidEd25519,
				d:    digestSHA512,
				want: schemeEd25519,
				ok:   true,
			},
			{name: "reports false for id-Ed25519 and SHA-256", oid: oidEd25519, d: digestSHA256},
			{
				name:   "reports false for id-Ed25519 with NULL parameters",
				oid:    oidEd25519,
				params: nullElement,
				d:      digestSHA512,
			},
			{
				name: "returns ML-DSA for id-ml-dsa-44 and SHA-256",
				oid:  oidMLDSA44,
				d:    digestSHA256,
				want: schemeMLDSA,
				ok:   true,
			},
			{
				name: "returns ML-DSA for id-ml-dsa-44 and SHA-512",
				oid:  oidMLDSA44,
				d:    digestSHA512,
				want: schemeMLDSA,
				ok:   true,
			},
			{
				name:   "reports false for id-ml-dsa-44 with NULL parameters",
				oid:    oidMLDSA44,
				params: nullElement,
				d:      digestSHA256,
			},
			{
				name: "returns ML-DSA for id-ml-dsa-65 and SHA-384",
				oid:  oidMLDSA65,
				d:    digestSHA384,
				want: schemeMLDSA,
				ok:   true,
			},
			{name: "reports false for id-ml-dsa-65 and SHA-256", oid: oidMLDSA65, d: digestSHA256},
			{
				name: "returns ML-DSA for id-ml-dsa-87 and SHA-512",
				oid:  oidMLDSA87,
				d:    digestSHA512,
				want: schemeMLDSA,
				ok:   true,
			},
			{name: "reports false for id-ml-dsa-87 and SHA-384", oid: oidMLDSA87, d: digestSHA384},
			{name: "reports false for another algorithm", oid: oidUnknown, d: digestSHA256},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, ok := signatureOf(algorithmIdentifier{oid: tt.oid, params: tt.params}, tt.d)
				testkit.Equal(t, ok, tt.ok, "signatureOf must report whether it verifies the algorithm")
				if tt.ok {
					testkit.Equal(t, got.scheme, tt.want, "signatureOf must return the scheme of the OID")
				}
			})
		}

		t.Run("returns the hash of the digest for a scheme over a digest", func(t *testing.T) {
			t.Parallel()
			got, _ := signatureOf(algorithmIdentifier{oid: oidRSAEncryption}, digestSHA512)
			testkit.Equal(t, got.hash, digestSHA512, "a PKCS #1 v1.5 signature must sign with the digest")
		})

		params := []struct {
			name string
			oid  []byte
			want mldsa.Parameters
		}{
			{name: "returns ML-DSA-44 for id-ml-dsa-44", oid: oidMLDSA44, want: mldsa.MLDSA44()},
			{name: "returns ML-DSA-65 for id-ml-dsa-65", oid: oidMLDSA65, want: mldsa.MLDSA65()},
			{name: "returns ML-DSA-87 for id-ml-dsa-87", oid: oidMLDSA87, want: mldsa.MLDSA87()},
		}
		for _, tt := range params {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, _ := signatureOf(algorithmIdentifier{oid: tt.oid}, digestSHA512)
				testkit.True(t, got.mldsa == tt.want, "signatureOf must return the parameter set of the OID")
			})
		}
	})

	t.Run("pssOf", func(t *testing.T) {
		t.Parallel()

		hash := explicit(0, algorithm(oidSHA256))
		mgf := explicit(1, algorithm(oidMGF1, algorithm(oidSHA256)))
		accepted := []struct {
			name   string
			params []byte
			want   int
		}{
			{
				name:   "returns the salt length that the parameters state",
				params: pss(hash, mgf, explicit(2, integer(32))),
				want:   32,
			},
			{
				name:   "returns a salt length of 20 when the parameters omit it",
				params: pss(hash, mgf),
				want:   defaultSaltLength,
			},
			{
				name:   "returns the largest salt length",
				params: pss(hash, mgf, explicit(2, integer(64))),
				want:   maxSaltLength,
			},
			{
				name:   "returns the salt length of parameters with the trailer field 1",
				params: pss(hash, mgf, explicit(2, integer(32)), explicit(3, integer(1))),
				want:   32,
			},
			{
				name:   "returns the salt length of a hash with NULL parameters",
				params: pss(explicit(0, algorithm(oidSHA256, nullElement)), mgf, explicit(2, integer(32))),
				want:   32,
			},
		}
		for _, tt := range accepted {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, ok := pssOf(tt.params, digestSHA256)
				testkit.True(t, ok, "pssOf must accept the parameters")
				testkit.Equal(t, got.scheme, schemePSS, "pssOf must return RSASSA-PSS")
				testkit.Equal(t, got.hash, digestSHA256, "pssOf must return the hash of the parameters")
				testkit.Equal(t, got.salt, tt.want, "pssOf must return the salt length")
			})
		}

		refused := []struct {
			name   string
			params []byte
		}{
			{name: "reports false for absent parameters", params: nil},
			{name: "reports false for NULL parameters", params: nullElement},
			{name: "reports false for an element after the parameters", params: cat(pss(hash, mgf), el(der.TagNull))},
			{name: "reports false for parameters without a hash", params: pss(mgf)},
			{
				name:   "reports false for a hash that is not DER",
				params: pss(cat([]byte{byte(der.ContextConstructed(0))}, notDER), mgf),
			},
			{
				name:   "reports false for a hash that is not an AlgorithmIdentifier",
				params: pss(explicit(0, el(der.TagNull)), mgf),
			},
			{
				name:   "reports false for an element after the hash",
				params: pss(explicit(0, cat(algorithm(oidSHA256), el(der.TagNull))), mgf),
			},
			{
				name:   "reports false for a hash that this package does not compute",
				params: pss(explicit(0, algorithm(oidUnknown)), mgf),
			},
			{
				name:   "reports false for a hash other than the digest",
				params: pss(explicit(0, algorithm(oidSHA384)), mgf),
			},
			{name: "reports false for parameters without a mask generation", params: pss(hash)},
			{
				name:   "reports false for a mask generation other than MGF1",
				params: pss(hash, explicit(1, algorithm(oidUnknown, algorithm(oidSHA256)))),
			},
			{
				name:   "reports false for an MGF1 of another hash",
				params: pss(hash, explicit(1, algorithm(oidMGF1, algorithm(oidSHA384)))),
			},
			{name: "reports false for an MGF1 without parameters", params: pss(hash, explicit(1, algorithm(oidMGF1)))},
			{
				name:   "reports false for an MGF1 whose parameters are not a SEQUENCE",
				params: pss(hash, explicit(1, algorithm(oidMGF1, nullElement))),
			},
			{
				name:   "reports false for an MGF1 of a hash that this package does not compute",
				params: pss(hash, explicit(1, algorithm(oidMGF1, algorithm(oidUnknown)))),
			},
			{
				name:   "reports false for an MGF1 of a hash without an OID",
				params: pss(hash, explicit(1, algorithm(oidMGF1, el(der.TagSequence, el(der.TagNull))))),
			},
			{name: "reports false for a salt length above 64", params: pss(hash, mgf, explicit(2, integer(65)))},
			{
				name:   "reports false for a salt length that is not DER",
				params: pss(hash, mgf, cat([]byte{byte(der.ContextConstructed(2))}, notDER)),
			},
			{
				name:   "reports false for a salt length that is not an INTEGER",
				params: pss(hash, mgf, explicit(2, el(der.TagOctetString, []byte{32}))),
			},
			{
				name:   "reports false for an element after the salt length",
				params: pss(hash, mgf, explicit(2, cat(integer(32), el(der.TagNull)))),
			},
			{
				name:   "reports false for a negative salt length",
				params: pss(hash, mgf, explicit(2, el(der.TagInteger, []byte{0xff}))),
			},
			{
				name:   "reports false for a trailer field other than 1",
				params: pss(hash, mgf, explicit(2, integer(32)), explicit(3, integer(2))),
			},
			{
				name:   "reports false for an element after the trailer field",
				params: pss(hash, mgf, explicit(3, integer(1)), el(der.TagNull)),
			},
		}
		for _, tt := range refused {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, ok := pssOf(tt.params, digestSHA256)
				testkit.False(t, ok, "pssOf must refuse the parameters")
			})
		}

		t.Run("reports false for an MGF1 whose parameters have an element after them", func(t *testing.T) {
			t.Parallel()
			testkit.False(t, mgfHash(cat(algorithm(oidSHA256), el(der.TagNull)), digestSHA256),
				"mgfHash must refuse an element after the AlgorithmIdentifier")
		})
	})

	t.Run("verify", func(t *testing.T) {
		t.Parallel()

		_, edKey, err := ed25519.GenerateKey(rand.Reader)
		testkit.NoError(t, err, "ed25519.GenerateKey must succeed")
		ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		testkit.NoError(t, err, "ecdsa.GenerateKey must succeed")
		mlKey, err := mldsa.GenerateKey(mldsa.MLDSA44())
		testkit.NoError(t, err, "mldsa.GenerateKey must succeed")
		rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
		testkit.NoError(t, err, "rsa.GenerateKey must succeed")

		s256, s384 := sha256.Sum256(signedMessage), sha512.Sum384(signedMessage)
		edSig := ed25519.Sign(edKey, signedMessage)
		ecSig, err := ecdsa.SignASN1(rand.Reader, ecKey, s256[:])
		testkit.NoError(t, err, "ecdsa.SignASN1 must succeed")
		mlSig, err := mlKey.Sign(nil, signedMessage, &mldsa.Options{})
		testkit.NoError(t, err, "mldsa Sign must succeed")
		pkcsSig, err := rsa.SignPKCS1v15(nil, rsaKey, stdcrypto.SHA384, s384[:])
		testkit.NoError(t, err, "rsa.SignPKCS1v15 must succeed")
		pssSig, err := rsa.SignPSS(rand.Reader, rsaKey, stdcrypto.SHA256, s256[:], &rsa.PSSOptions{SaltLength: 32})
		testkit.NoError(t, err, "rsa.SignPSS must succeed")

		ed := signatureAlgorithm{scheme: schemeEd25519}
		ml := signatureAlgorithm{scheme: schemeMLDSA, mldsa: mldsa.MLDSA44()}
		ec := signatureAlgorithm{scheme: schemeECDSA, hash: digestSHA256}
		pkcs := signatureAlgorithm{scheme: schemePKCS1v15, hash: digestSHA384}
		ps := signatureAlgorithm{scheme: schemePSS, hash: digestSHA256, salt: 32}

		tests := []struct {
			name  string
			alg   signatureAlgorithm
			key   stdcrypto.PublicKey
			value []byte
			want  bool
		}{
			{name: "reports true for an Ed25519 signature", alg: ed, key: edKey.Public(), value: edSig, want: true},
			{
				name:  "reports false for an Ed25519 signature that does not verify",
				alg:   ed,
				key:   edKey.Public(),
				value: mutated(edSig),
			},
			{
				name:  "reports false for an Ed25519 algorithm and another key type",
				alg:   ed,
				key:   ecKey.Public(),
				value: edSig,
			},
			{name: "reports true for an ML-DSA signature", alg: ml, key: mlKey.PublicKey(), value: mlSig, want: true},
			{
				name:  "reports false for an ML-DSA signature that does not verify",
				alg:   ml,
				key:   mlKey.PublicKey(),
				value: mutated(mlSig),
			},
			{
				name:  "reports false for an ML-DSA key of another parameter set",
				alg:   signatureAlgorithm{scheme: schemeMLDSA, mldsa: mldsa.MLDSA65()},
				key:   mlKey.PublicKey(),
				value: mlSig,
			},
			{
				name:  "reports false for an ML-DSA algorithm and another key type",
				alg:   ml,
				key:   edKey.Public(),
				value: mlSig,
			},
			{name: "reports true for an ECDSA signature", alg: ec, key: ecKey.Public(), value: ecSig, want: true},
			{
				name:  "reports false for an ECDSA signature that does not verify",
				alg:   ec,
				key:   ecKey.Public(),
				value: mutated(ecSig),
			},
			{
				name:  "reports false for an ECDSA algorithm and another key type",
				alg:   ec,
				key:   rsaKey.Public(),
				value: ecSig,
			},
			{
				name:  "reports true for a PKCS #1 v1.5 signature",
				alg:   pkcs,
				key:   rsaKey.Public(),
				value: pkcsSig,
				want:  true,
			},
			{
				name:  "reports false for a PKCS #1 v1.5 signature that does not verify",
				alg:   pkcs,
				key:   rsaKey.Public(),
				value: mutated(pkcsSig),
			},
			{
				name:  "reports false for a PKCS #1 v1.5 algorithm and another key type",
				alg:   pkcs,
				key:   ecKey.Public(),
				value: pkcsSig,
			},
			{
				name:  "reports true for an RSASSA-PSS signature",
				alg:   ps,
				key:   rsaKey.Public(),
				value: pssSig,
				want:  true,
			},
			{
				name:  "reports false for an RSASSA-PSS signature of another salt length",
				alg:   signatureAlgorithm{scheme: schemePSS, hash: digestSHA256, salt: 20},
				key:   rsaKey.Public(),
				value: pssSig,
			},
			{
				name:  "reports false for an RSASSA-PSS signature that does not verify",
				alg:   ps,
				key:   rsaKey.Public(),
				value: mutated(pssSig),
			},
			{
				name:  "reports false for an RSASSA-PSS algorithm and another key type",
				alg:   ps,
				key:   edKey.Public(),
				value: pssSig,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(t, tt.alg.verify(tt.key, signedMessage, tt.value), tt.want,
					"verify must report whether the signature verifies")
			})
		}
	})
}

// pss returns the DER of RSASSA-PSS-params of the elements fields.
func pss(fields ...[]byte) []byte {
	return el(der.TagSequence, fields...)
}

// explicit returns the DER of the [n] EXPLICIT element of e.
func explicit(n uint8, e []byte) []byte {
	return el(der.ContextConstructed(n), e)
}

// algorithm returns the DER of an AlgorithmIdentifier of the OID with the
// content oid and the parameter elements params.
func algorithm(oid []byte, params ...[]byte) []byte {
	return el(der.TagSequence, el(der.TagOID, oid), cat(params...))
}

// integer returns the DER of the INTEGER v.
func integer(v uint64) []byte {
	b := der.NewBuilder(nil)
	b.AddUint64(v)

	return b.Bytes()
}

// mutated returns a copy of b with its last octet flipped.
func mutated(b []byte) []byte {
	c := append([]byte(nil), b...)
	c[len(c)-1] ^= 0x01

	return c
}
