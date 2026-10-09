// Copyright ThesmOS B.V. 2026
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
	"slices"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

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
		}{
			{name: "returns SHA-256 for id-sha256", give: algorithmIdentifier{oid: oidSHA256}, want: digestSHA256},
			{name: "returns SHA-384 for id-sha384", give: algorithmIdentifier{oid: oidSHA384}, want: digestSHA384},
			{name: "returns SHA-512 for id-sha512", give: algorithmIdentifier{oid: oidSHA512}, want: digestSHA512},
			{
				name: "returns SHA-256 for id-sha256 with NULL parameters",
				give: algorithmIdentifier{oid: oidSHA256, params: nullElement},
				want: digestSHA256,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, ok := digestOf(tt.give)
				assert.True(t, ok, "digestOf must know the algorithm")
				assert.Equal(t, got, tt.want, "digestOf must return the digest of the OID")
			})
		}

		refused := []struct {
			name string
			give algorithmIdentifier
		}{
			{
				name: "reports false for parameters other than NULL",
				give: algorithmIdentifier{oid: oidSHA256, params: el(der.TagSequence)},
			},
			{name: "reports false for another algorithm", give: algorithmIdentifier{oid: oidUnknown}},
		}
		for _, tt := range refused {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, ok := digestOf(tt.give)
				assert.False(t, ok, "digestOf must refuse the algorithm")
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
				assert.Equal(t, tt.give.sum(signedMessage, &buf), tt.want, "sum must return the digest of m")
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
				assert.Equal(t, tt.give.size(), tt.want, "size must return the size of the digest")
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
				assert.Equal(t, tt.give.std(), tt.want, "std must return the hash of the standard library")
			})
		}
	})

	t.Run("signatureOf", func(t *testing.T) {
		t.Parallel()

		accepted := []struct {
			name   string
			oid    []byte
			params []byte
			d      digest
			want   scheme
		}{
			{
				name: "returns PKCS #1 v1.5 for rsaEncryption",
				oid:  oidRSAEncryption,
				d:    digestSHA384,
				want: schemePKCS1v15,
			},
			{
				name:   "returns PKCS #1 v1.5 for rsaEncryption with NULL parameters",
				oid:    oidRSAEncryption,
				params: nullElement,
				d:      digestSHA256,
				want:   schemePKCS1v15,
			},
			{
				name:   "returns PKCS #1 v1.5 for sha256WithRSAEncryption with SHA-256",
				oid:    oidSHA256WithRSA,
				params: nullElement,
				d:      digestSHA256,
				want:   schemePKCS1v15,
			},
			{
				name: "returns PKCS #1 v1.5 for sha384WithRSAEncryption with SHA-384",
				oid:  oidSHA384WithRSA,
				d:    digestSHA384,
				want: schemePKCS1v15,
			},
			{
				name: "returns PKCS #1 v1.5 for sha512WithRSAEncryption with SHA-512",
				oid:  oidSHA512WithRSA,
				d:    digestSHA512,
				want: schemePKCS1v15,
			},
			{
				name: "returns ECDSA for ecdsa-with-SHA256 with SHA-256",
				oid:  oidECDSAWithSHA256,
				d:    digestSHA256,
				want: schemeECDSA,
			},
			{
				name: "returns ECDSA for ecdsa-with-SHA384 with SHA-384",
				oid:  oidECDSAWithSHA384,
				d:    digestSHA384,
				want: schemeECDSA,
			},
			{
				name: "returns ECDSA for ecdsa-with-SHA512 with SHA-512",
				oid:  oidECDSAWithSHA512,
				d:    digestSHA512,
				want: schemeECDSA,
			},
			{
				name: "returns Ed25519 for id-Ed25519 with SHA-512",
				oid:  oidEd25519,
				d:    digestSHA512,
				want: schemeEd25519,
			},
			{name: "returns ML-DSA for id-ml-dsa-44 with SHA-256", oid: oidMLDSA44, d: digestSHA256, want: schemeMLDSA},
			{name: "returns ML-DSA for id-ml-dsa-44 with SHA-512", oid: oidMLDSA44, d: digestSHA512, want: schemeMLDSA},
			{name: "returns ML-DSA for id-ml-dsa-65 with SHA-384", oid: oidMLDSA65, d: digestSHA384, want: schemeMLDSA},
			{name: "returns ML-DSA for id-ml-dsa-87 with SHA-512", oid: oidMLDSA87, d: digestSHA512, want: schemeMLDSA},
		}
		for _, tt := range accepted {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, ok := signatureOf(algorithmIdentifier{oid: tt.oid, params: tt.params}, tt.d)
				assert.True(t, ok, "signatureOf must verify the algorithm")
				assert.Equal(t, got.scheme, tt.want, "signatureOf must return the scheme of the OID")
			})
		}

		refused := []struct {
			name   string
			oid    []byte
			params []byte
			d      digest
		}{
			{
				name:   "reports false for rsaEncryption with other parameters",
				oid:    oidRSAEncryption,
				params: el(der.TagSequence),
				d:      digestSHA256,
			},
			{
				name:   "reports false for sha256WithRSAEncryption with SHA-384",
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
			{name: "reports false for sha384WithRSAEncryption with SHA-256", oid: oidSHA384WithRSA, d: digestSHA256},
			{
				name:   "reports false for sha384WithRSAEncryption with other parameters",
				oid:    oidSHA384WithRSA,
				params: el(der.TagSequence),
				d:      digestSHA384,
			},
			{name: "reports false for sha512WithRSAEncryption with SHA-256", oid: oidSHA512WithRSA, d: digestSHA256},
			{
				name:   "reports false for sha512WithRSAEncryption with other parameters",
				oid:    oidSHA512WithRSA,
				params: el(der.TagSequence),
				d:      digestSHA512,
			},
			{name: "reports false for ecdsa-with-SHA256 with SHA-384", oid: oidECDSAWithSHA256, d: digestSHA384},
			{
				name:   "reports false for ecdsa-with-SHA256 with NULL parameters",
				oid:    oidECDSAWithSHA256,
				params: nullElement,
				d:      digestSHA256,
			},
			{name: "reports false for ecdsa-with-SHA384 with SHA-512", oid: oidECDSAWithSHA384, d: digestSHA512},
			{
				name:   "reports false for ecdsa-with-SHA384 with NULL parameters",
				oid:    oidECDSAWithSHA384,
				params: nullElement,
				d:      digestSHA384,
			},
			{name: "reports false for ecdsa-with-SHA512 with SHA-384", oid: oidECDSAWithSHA512, d: digestSHA384},
			{
				name:   "reports false for ecdsa-with-SHA512 with NULL parameters",
				oid:    oidECDSAWithSHA512,
				params: nullElement,
				d:      digestSHA512,
			},
			{name: "reports false for id-Ed25519 with SHA-256", oid: oidEd25519, d: digestSHA256},
			{
				name:   "reports false for id-Ed25519 with NULL parameters",
				oid:    oidEd25519,
				params: nullElement,
				d:      digestSHA512,
			},
			{
				name:   "reports false for id-ml-dsa-44 with NULL parameters",
				oid:    oidMLDSA44,
				params: nullElement,
				d:      digestSHA256,
			},
			{name: "reports false for id-ml-dsa-65 with SHA-256", oid: oidMLDSA65, d: digestSHA256},
			{
				name:   "reports false for id-ml-dsa-65 with NULL parameters",
				oid:    oidMLDSA65,
				params: nullElement,
				d:      digestSHA384,
			},
			{name: "reports false for id-ml-dsa-87 with SHA-384", oid: oidMLDSA87, d: digestSHA384},
			{
				name:   "reports false for id-ml-dsa-87 with NULL parameters",
				oid:    oidMLDSA87,
				params: nullElement,
				d:      digestSHA512,
			},
			{name: "reports false for another algorithm", oid: oidUnknown, d: digestSHA256},
		}
		for _, tt := range refused {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, ok := signatureOf(algorithmIdentifier{oid: tt.oid, params: tt.params}, tt.d)
				assert.False(t, ok, "signatureOf must refuse the algorithm")
			})
		}

		t.Run("returns the hash of the digest for a scheme over a digest", func(t *testing.T) {
			t.Parallel()
			got, _ := signatureOf(algorithmIdentifier{oid: oidRSAEncryption}, digestSHA512)
			assert.Equal(t, got.hash, digestSHA512, "a PKCS #1 v1.5 signature must sign with the digest")
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
				assert.Equal(t, got.mldsa, tt.want, "signatureOf must return the parameter set of the OID")
			})
		}
	})

	t.Run("pssOf", func(t *testing.T) {
		t.Parallel()

		sha256Algorithm := el(der.TagSequence, el(der.TagOID, oidSHA256))
		hash := el(der.ContextConstructed(0), sha256Algorithm)
		mgf := el(der.ContextConstructed(1), el(der.TagSequence, el(der.TagOID, oidMGF1), sha256Algorithm))
		salt32 := el(der.ContextConstructed(2), integer(32))

		accepted := []struct {
			name   string
			fields [][]byte
			want   int
		}{
			{name: "returns the salt length that the parameters state", fields: [][]byte{hash, mgf, salt32}, want: 32},
			{
				name:   "returns a salt length of 20 for parameters that omit it",
				fields: [][]byte{hash, mgf},
				want:   defaultSaltLength,
			},
			{
				name:   "returns the largest salt length",
				fields: [][]byte{hash, mgf, el(der.ContextConstructed(2), integer(64))},
				want:   maxSaltLength,
			},
			{
				name:   "returns the salt length of parameters with the trailer field 1",
				fields: [][]byte{hash, mgf, salt32, el(der.ContextConstructed(3), integer(1))},
				want:   32,
			},
			{
				name: "returns the salt length of a hash with NULL parameters",
				fields: [][]byte{
					el(der.ContextConstructed(0), el(der.TagSequence, el(der.TagOID, oidSHA256), nullElement)),
					mgf, salt32,
				},
				want: 32,
			},
		}
		for _, tt := range accepted {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, ok := pssOf(el(der.TagSequence, tt.fields...), digestSHA256)
				assert.True(t, ok, "pssOf must accept the parameters")
				expect.Equal(t, got.scheme, schemePSS, "pssOf must return RSASSA-PSS")
				expect.Equal(t, got.hash, digestSHA256, "pssOf must return the hash of the parameters")
				expect.Equal(t, got.salt, tt.want, "pssOf must return the salt length")
			})
		}

		refusedParams := []struct {
			name   string
			params []byte
		}{
			{name: "reports false for absent parameters", params: nil},
			{name: "reports false for NULL parameters", params: nullElement},
			{
				name:   "reports false for an element after the parameters",
				params: slices.Concat(el(der.TagSequence, hash, mgf), el(der.TagNull)),
			},
		}
		for _, tt := range refusedParams {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, ok := pssOf(tt.params, digestSHA256)
				assert.False(t, ok, "pssOf must refuse the parameters")
			})
		}

		refused := []struct {
			name   string
			fields [][]byte
		}{
			{name: "reports false for parameters without a hash", fields: [][]byte{mgf}},
			{
				name:   "reports false for a hash that is not DER",
				fields: [][]byte{slices.Concat([]byte{byte(der.ContextConstructed(0))}, notDER), mgf},
			},
			{
				name:   "reports false for a hash that is not an AlgorithmIdentifier",
				fields: [][]byte{el(der.ContextConstructed(0), el(der.TagNull)), mgf},
			},
			{
				name:   "reports false for an element after the hash",
				fields: [][]byte{el(der.ContextConstructed(0), sha256Algorithm, el(der.TagNull)), mgf},
			},
			{
				name:   "reports false for a hash that this package does not compute",
				fields: [][]byte{el(der.ContextConstructed(0), el(der.TagSequence, el(der.TagOID, oidUnknown))), mgf},
			},
			{
				name:   "reports false for a hash other than the digest",
				fields: [][]byte{el(der.ContextConstructed(0), el(der.TagSequence, el(der.TagOID, oidSHA384))), mgf},
			},
			{name: "reports false for parameters without a mask generation", fields: [][]byte{hash}},
			{
				name: "reports false for a mask generation other than MGF1",
				fields: [][]byte{
					hash,
					el(der.ContextConstructed(1), el(der.TagSequence, el(der.TagOID, oidUnknown), sha256Algorithm)),
				},
			},
			{
				name: "reports false for an MGF1 of another hash",
				fields: [][]byte{hash, el(der.ContextConstructed(1), el(der.TagSequence, el(der.TagOID, oidMGF1),
					el(der.TagSequence, el(der.TagOID, oidSHA384))))},
			},
			{
				name:   "reports false for an MGF1 without parameters",
				fields: [][]byte{hash, el(der.ContextConstructed(1), el(der.TagSequence, el(der.TagOID, oidMGF1)))},
			},
			{
				name: "reports false for an MGF1 whose parameters are not a SEQUENCE",
				fields: [][]byte{
					hash,
					el(der.ContextConstructed(1), el(der.TagSequence, el(der.TagOID, oidMGF1), nullElement)),
				},
			},
			{
				name: "reports false for an MGF1 of a hash that this package does not compute",
				fields: [][]byte{hash, el(der.ContextConstructed(1), el(der.TagSequence, el(der.TagOID, oidMGF1),
					el(der.TagSequence, el(der.TagOID, oidUnknown))))},
			},
			{
				name: "reports false for an MGF1 of a hash without an OID",
				fields: [][]byte{hash, el(der.ContextConstructed(1), el(der.TagSequence, el(der.TagOID, oidMGF1),
					el(der.TagSequence, el(der.TagNull))))},
			},
			{
				name:   "reports false for a salt length above 64",
				fields: [][]byte{hash, mgf, el(der.ContextConstructed(2), integer(65))},
			},
			{
				name:   "reports false for a salt length that is not DER",
				fields: [][]byte{hash, mgf, slices.Concat([]byte{byte(der.ContextConstructed(2))}, notDER)},
			},
			{
				name:   "reports false for a salt length that is not an INTEGER",
				fields: [][]byte{hash, mgf, el(der.ContextConstructed(2), el(der.TagOctetString, []byte{32}))},
			},
			{
				name:   "reports false for an element after the salt length",
				fields: [][]byte{hash, mgf, el(der.ContextConstructed(2), integer(32), el(der.TagNull))},
			},
			{
				name:   "reports false for a negative salt length",
				fields: [][]byte{hash, mgf, el(der.ContextConstructed(2), el(der.TagInteger, []byte{0xff}))},
			},
			{
				name:   "reports false for a trailer field other than 1",
				fields: [][]byte{hash, mgf, salt32, el(der.ContextConstructed(3), integer(2))},
			},
			{
				name:   "reports false for an element after the trailer field",
				fields: [][]byte{hash, mgf, el(der.ContextConstructed(3), integer(1)), el(der.TagNull)},
			},
		}
		for _, tt := range refused {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, ok := pssOf(el(der.TagSequence, tt.fields...), digestSHA256)
				assert.False(t, ok, "pssOf must refuse the parameters")
			})
		}
	})

	t.Run("mgfHash", func(t *testing.T) {
		t.Parallel()

		t.Run("reports false for parameters with an element after them", func(t *testing.T) {
			t.Parallel()
			params := slices.Concat(el(der.TagSequence, el(der.TagOID, oidSHA256)), el(der.TagNull))
			assert.False(t, mgfHash(params, digestSHA256),
				"mgfHash must refuse an element after the AlgorithmIdentifier")
		})
	})

	t.Run("verify", func(t *testing.T) {
		t.Parallel()

		_, edKey, err := ed25519.GenerateKey(rand.Reader)
		assert.NoError(t, err, "ed25519.GenerateKey must succeed")
		ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		assert.NoError(t, err, "ecdsa.GenerateKey must succeed")
		mlKey, err := mldsa.GenerateKey(mldsa.MLDSA44())
		assert.NoError(t, err, "mldsa.GenerateKey must succeed")
		rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
		assert.NoError(t, err, "rsa.GenerateKey must succeed")

		s256, s384 := sha256.Sum256(signedMessage), sha512.Sum384(signedMessage)
		edSig := ed25519.Sign(edKey, signedMessage)
		ecSig, err := ecdsa.SignASN1(rand.Reader, ecKey, s256[:])
		assert.NoError(t, err, "ecdsa.SignASN1 must succeed")
		mlSig, err := mlKey.Sign(nil, signedMessage, &mldsa.Options{})
		assert.NoError(t, err, "the ML-DSA signature must succeed")
		pkcsSig, err := rsa.SignPKCS1v15(nil, rsaKey, stdcrypto.SHA384, s384[:])
		assert.NoError(t, err, "rsa.SignPKCS1v15 must succeed")
		pssSig, err := rsa.SignPSS(rand.Reader, rsaKey, stdcrypto.SHA256, s256[:], &rsa.PSSOptions{SaltLength: 32})
		assert.NoError(t, err, "rsa.SignPSS must succeed")

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
				name:  "reports false for an Ed25519 algorithm with another key type",
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
				name:  "reports false for an ML-DSA algorithm with another key type",
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
				name:  "reports false for an ECDSA algorithm with another key type",
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
				name:  "reports false for a PKCS #1 v1.5 algorithm with another key type",
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
				name:  "reports false for an RSASSA-PSS algorithm with another key type",
				alg:   ps,
				key:   edKey.Public(),
				value: pssSig,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, tt.alg.verify(tt.key, signedMessage, tt.value), tt.want,
					"verify must report whether the signature verifies")
			})
		}
	})
}

// integer returns the DER of the INTEGER v.
func integer(v uint64) []byte {
	b := der.NewBuilder(nil)
	b.AddUint64(v)

	return b.Bytes()
}

// mutated returns a copy of b with its last octet flipped.
func mutated(b []byte) []byte {
	c := slices.Clone(b)
	c[len(c)-1] ^= 0x01

	return c
}
