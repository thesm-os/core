// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package ecdsap384_test

import (
	"bytes"
	"crypto/ecdsa"
	stded25519 "crypto/ed25519"
	"crypto/elliptic"
	stdrand "crypto/rand"
	"crypto/sha512"
	"crypto/x509"
	"encoding/hex"
	"math/big"
	"slices"
	"strconv"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/coretest/cryptotest"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sign"
	signecdsa "go.thesmos.sh/core/crypto/sign/ecdsap384"
	"go.thesmos.sh/core/errs"
)

// resolveAllocs is the ceiling of Resolve. crypto/x509 allocates 14
// objects to parse the key, and crypto/ecdsa allocates 6 to encode its
// point for the KeyID. Resolve allocates its copy of the encoding and the
// Verifier.
const resolveAllocs = 22

// The goroutines of a concurrent case, and the calls that each makes.
const (
	goroutines = 8
	rounds     = 20
)

// offCurve is the point X=1, Y=2. It names P-384 as its curve but is not
// on it, so KeyIDFromPub refuses it through ecdsa.PublicKey.Bytes. Only
// the deprecated raw coordinates build such a key. The tests only read
// it.
//
//nolint:staticcheck // an invalid key is the subject of the tests
var offCurve = &ecdsa.PublicKey{Curve: elliptic.P384(), X: big.NewInt(1), Y: big.NewInt(2)}

// TestECDSAP384VerifierContract runs the contract suite of sign.Verifier
// with the Algorithm, the KeyID and the signatures of the standard
// library.
func TestECDSAP384VerifierContract(t *testing.T) {
	t.Parallel()
	fix := cryptotest.NewECDSAP384Sample()
	signer := mustSigner(t, fix)
	sample := cryptotest.VerifierSample{Message: fix.Message, Signature: fix.Signature}

	cryptotest.AssertVerifierContract(t,
		func() sign.Verifier { return signer.Verifier },
		append(cryptotest.VerifierContractAssertions(sample),
			cryptotest.VerifierAlgorithmAssertion(crypto.AlgECDSAP384),
			cryptotest.VerifierKeyIDAssertion(fix.KeyID),
			cryptotest.VerifierAcceptsAssertion(sample),
			cryptotest.VerifierCrossStdlibAssertion(stdlibSign(t, fix.StdlibPriv)),
		)...,
	)
}

// TestECDSAP384SignerContract runs the contract suite of sign.Signer
// with the Algorithm, the KeyID, the signatures and the verification of
// the standard library, and the AppendSigner capability.
func TestECDSAP384SignerContract(t *testing.T) {
	t.Parallel()
	fix := cryptotest.NewECDSAP384Sample()
	signer := mustSigner(t, fix)

	cryptotest.AssertSignerContract(t,
		func() sign.Signer { return signer },
		append(cryptotest.SignerContractAssertions(),
			cryptotest.SignerAlgorithmAssertion(crypto.AlgECDSAP384),
			cryptotest.SignerKeyIDAssertion(fix.KeyID),
			cryptotest.SignerCrossStdlibVerifyAssertion(stdlibVerify),
			cryptotest.SignerCrossStdlibSignAssertion(stdlibSign(t, fix.StdlibPriv)),
			cryptotest.AppendSignerAssertion(),
			cryptotest.AppendSignerContextAssertion(),
		)...,
	)
}

// TestECDSAP384SignStreamContract runs the contract suite of
// sign.SignStream.
func TestECDSAP384SignStreamContract(t *testing.T) {
	t.Parallel()
	signer := mustSigner(t, cryptotest.NewECDSAP384Sample())

	cryptotest.AssertSignStreamContract(t,
		func() sign.SignStream { return signer.NewSignStream() },
		cryptotest.SignStreamContractAssertions(signer.Verify)...,
	)
}

// TestECDSAP384VerifyStreamContract runs the contract suite of
// sign.VerifyStream.
func TestECDSAP384VerifyStreamContract(t *testing.T) {
	t.Parallel()
	fix := cryptotest.NewECDSAP384Sample()
	signer := mustSigner(t, fix)
	streamSample := cryptotest.VerifyStreamSample{Message: fix.Message, Signature: fix.Signature}

	cryptotest.AssertVerifyStreamContract(t,
		func() sign.VerifyStream { return signer.NewVerifyStream() },
		cryptotest.VerifyStreamContractAssertions(streamSample)...,
	)
}

func TestECDSAP384(t *testing.T) {
	t.Parallel()

	t.Run("Verifier", func(t *testing.T) {
		t.Parallel()

		t.Run("implements StreamingVerifier", func(t *testing.T) {
			t.Parallel()
			_, ok := any(mustSigner(t, cryptotest.NewECDSAP384Sample()).Verifier).(sign.StreamingVerifier)
			assert.True(t, ok, "an ECDSA P-384 Verifier must implement sign.StreamingVerifier")
		})

		t.Run("NewVerifyStream", func(t *testing.T) {
			t.Parallel()

			t.Run("returns a stream whose Write reports the length of p", func(t *testing.T) {
				t.Parallel()
				v := mustSigner(t, cryptotest.NewECDSAP384Sample()).Verifier
				prop.Equal(t, func(p []byte) int {
					n, _ := v.NewVerifyStream().Write(p)

					return n
				}, func(p []byte) int { return len(p) }, "Write must report every byte of p as written",
					prop.Using(prop.Bytes(prop.MaxSize(256))), prop.Example([]byte("payload")))
			})
		})

		t.Run("Verify", func(t *testing.T) {
			t.Parallel()

			t.Run("reports true for a valid signature to goroutines that verify at once", func(t *testing.T) {
				t.Parallel()
				fix := cryptotest.NewECDSAP384Sample()
				v := mustSigner(t, fix).Verifier
				outcomes := history.Concurrently(goroutines, 10*time.Second, func(int) (any, error) {
					verified := make([]bool, 0, rounds)
					for range rounds {
						verified = append(verified, v.Verify(fix.Message, fix.Signature))
					}

					return verified, nil
				})
				for _, o := range outcomes {
					assert.True(t, o.Finished, "every goroutine must finish")
					verified, _ := o.Output.([]bool)
					assert.Equal(t, verified, slices.Repeat([]bool{true}, rounds),
						"every Verify must accept the signature")
				}
			})
		})
	})

	t.Run("NewVerifier", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give *ecdsa.PublicKey
			want error
		}{
			{name: "returns ErrNilKey for a nil public key", give: nil, want: signecdsa.ErrNilKey},
			{
				name: "returns ErrWrongCurve for a P-256 public key",
				give: &p256Key(t).PublicKey,
				want: signecdsa.ErrWrongCurve,
			},
			{name: "returns ErrOffCurve for a point off the curve", give: offCurve, want: signecdsa.ErrOffCurve},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, err := signecdsa.NewVerifier(tt.give)
				expect.ErrorIs(t, err, tt.want, "NewVerifier must return the sentinel of the key")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "the sentinel must classify as Invalid")
			})
		}
	})

	t.Run("NewVerifierFromPKIX", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the Verifier of the encoded key", func(t *testing.T) {
			t.Parallel()
			fix := cryptotest.NewECDSAP384Sample()
			v, err := signecdsa.NewVerifierFromPKIX(fix.PublicKey)
			assert.NoError(t, err, "NewVerifierFromPKIX must accept the encoding")
			expect.Equal(t, v.PublicKey(), fix.PublicKey, "PublicKey must return the encoding")
			expect.Equal(t, v.KeyID(), fix.KeyID, "KeyID must return the KeyID of the key")
		})

		// The PKIX encoding of a P-384 key takes 120 bytes, so no input
		// of the generator encodes one.
		t.Run("returns ErrInvalidPublicKey for bytes that are not PKIX", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(b []byte) error {
				_, err := signecdsa.NewVerifierFromPKIX(b)

				return err
			}, signecdsa.ErrInvalidPublicKey, "bytes that do not parse must be refused",
				prop.Using(prop.Bytes(prop.MaxSize(64))),
				prop.Example([]byte{}), prop.Example([]byte{0x00}), prop.Example([]byte("not asn.1 der at all")))
		})

		t.Run("returns ErrInvalidPublicKey for the PKIX of an Ed25519 key", func(t *testing.T) {
			t.Parallel()
			pkix, err := x509.MarshalPKIXPublicKey(make(stded25519.PublicKey, stded25519.PublicKeySize))
			assert.NoError(t, err, "MarshalPKIXPublicKey must encode the key")
			_, err = signecdsa.NewVerifierFromPKIX(pkix)
			assert.ErrorIs(t, err, signecdsa.ErrInvalidPublicKey, "the PKIX of an Ed25519 key must be refused")
		})

		// crypto/x509 returns a nil *ecdsa.PublicKey with the error of a
		// point off the curve.
		t.Run("returns ErrInvalidPublicKey for the PKIX of a point off the curve", func(t *testing.T) {
			t.Parallel()
			pkix := bytes.Clone(cryptotest.NewECDSAP384Sample().PublicKey)
			pkix[len(pkix)-1] ^= 0x01 // the last byte of the Y coordinate
			_, err := signecdsa.NewVerifierFromPKIX(pkix)
			assert.ErrorIs(t, err, signecdsa.ErrInvalidPublicKey, "the PKIX of a point off the curve must be refused")
		})

		t.Run("returns ErrWrongCurve for the PKIX of a P-256 key", func(t *testing.T) {
			t.Parallel()
			pkix, err := x509.MarshalPKIXPublicKey(&p256Key(t).PublicKey)
			assert.NoError(t, err, "MarshalPKIXPublicKey must encode the key")
			_, err = signecdsa.NewVerifierFromPKIX(pkix)
			assert.ErrorIs(t, err, signecdsa.ErrWrongCurve, "the PKIX of a P-256 key must be refused")
		})

		t.Run("returns a Verifier that a later write to the bytes leaves unchanged", func(t *testing.T) {
			t.Parallel()
			fix := cryptotest.NewECDSAP384Sample()
			src := bytes.Clone(fix.PublicKey)
			v, err := signecdsa.NewVerifierFromPKIX(src)
			assert.NoError(t, err, "NewVerifierFromPKIX must accept the encoding")
			clear(src)
			assert.Equal(t, v.PublicKey(), fix.PublicKey,
				"zeroing the buffer of the caller must not change the key of the Verifier")
		})
	})

	t.Run("Resolve", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a Verifier for the PKIX public key", func(t *testing.T) {
			t.Parallel()
			fix := cryptotest.NewECDSAP384Sample()
			v, err := signecdsa.Resolve(fix.PublicKey)
			assert.NoError(t, err, "Resolve must accept a PKIX P-384 key")
			expect.Equal(t, v.KeyID(), fix.KeyID, "the Verifier must have the KeyID of the key")
			expect.True(t, v.Verify(fix.Message, fix.Signature), "the Verifier must accept the signature of the key")
		})

		t.Run("returns ErrInvalidPublicKey for bytes that are not PKIX", func(t *testing.T) {
			t.Parallel()
			_, err := signecdsa.Resolve([]byte("not a PKIX key"))
			assert.ErrorIs(t, err, signecdsa.ErrInvalidPublicKey, "malformed bytes must be refused")
		})

		t.Run("returns an error of class Invalid for bytes that are not PKIX", func(t *testing.T) {
			t.Parallel()
			_, err := signecdsa.Resolve([]byte("not a PKIX key"))
			assert.Equal(t, errs.Classify(err), errs.Invalid, "ErrInvalidPublicKey must classify as Invalid")
		})

		t.Run("returns a nil Verifier for bytes that are not PKIX", func(t *testing.T) {
			t.Parallel()
			v, err := signecdsa.Resolve([]byte("not a PKIX key"))
			assert.HasError(t, err, "the test must resolve bytes that are refused")
			assert.Nil(t, v, "the Verifier must be a nil interface")
		})
	})

	t.Run("Signer", func(t *testing.T) {
		t.Parallel()

		t.Run("implements StreamingSigner", func(t *testing.T) {
			t.Parallel()
			_, ok := any(mustSigner(t, cryptotest.NewECDSAP384Sample())).(sign.StreamingSigner)
			assert.True(t, ok, "an ECDSA P-384 Signer must implement sign.StreamingSigner")
		})

		t.Run("NewSignStream", func(t *testing.T) {
			t.Parallel()

			t.Run("returns a stream whose Write reports the length of p", func(t *testing.T) {
				t.Parallel()
				s := mustSigner(t, cryptotest.NewECDSAP384Sample())
				prop.Equal(t, func(p []byte) int {
					n, _ := s.NewSignStream().Write(p)

					return n
				}, func(p []byte) int { return len(p) }, "Write must report every byte of p as written",
					prop.Using(prop.Bytes(prop.MaxSize(256))), prop.Example([]byte("payload")))
			})
		})

		// ECDSA draws a fresh nonce for each signature, so two signatures
		// of one message differ and only the Verifier checks them.
		t.Run("Sign", func(t *testing.T) {
			t.Parallel()

			t.Run("returns signatures that the Verifier accepts to goroutines that sign at once", func(t *testing.T) {
				t.Parallel()
				s := mustSigner(t, cryptotest.NewECDSAP384Sample())
				outcomes := history.Concurrently(goroutines, 10*time.Second, func(client int) (any, error) {
					msg := []byte("message " + strconv.Itoa(client))
					sigs := make([][]byte, 0, rounds)
					for range rounds {
						sig, err := s.Sign(msg)
						if err != nil {
							return sigs, err
						}
						sigs = append(sigs, sig)
					}

					return sigs, nil
				})
				for _, o := range outcomes {
					assert.True(t, o.Finished, "every goroutine must finish")
					assert.NoError(t, o.Error, "Sign must sign on every goroutine")
					sigs, _ := o.Output.([][]byte)
					assert.Length(t, sigs, rounds, "every goroutine must return its signatures")
					for _, sig := range sigs {
						expect.True(t, s.Verify([]byte("message "+strconv.Itoa(o.Client)), sig),
							"the Verifier must accept every signature")
					}
				}
			})
		})
	})

	t.Run("New", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give *ecdsa.PrivateKey
			want error
		}{
			{name: "returns ErrNilKey for a nil private key", give: nil, want: signecdsa.ErrNilKey},
			{name: "returns ErrWrongCurve for a P-256 private key", give: p256Key(t), want: signecdsa.ErrWrongCurve},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, err := signecdsa.New(tt.give)
				assert.ErrorIs(t, err, tt.want, "New must return the sentinel of the key")
			})
		}
	})

	// crypto/ecdsa.GenerateKey ignores the supplied reader and uses the
	// secure source of the runtime unless GODEBUG=cryptocustomrand=1 is
	// set, so the package promises no deterministic keys.
	t.Run("Generate", func(t *testing.T) {
		t.Parallel()

		t.Run("returns another key pair on each call", func(t *testing.T) {
			t.Parallel()
			a, err := signecdsa.Generate()
			assert.NoError(t, err, "Generate must succeed")
			b, err := signecdsa.Generate()
			assert.NoError(t, err, "Generate must succeed")
			assert.NotEqual(t, a.PublicKey(), b.PublicKey(), "two calls must produce two key pairs")
		})
	})

	// The KeyID derivation, SHA-256 of the SEC 1 uncompressed point
	// truncated to 16 bytes, for the base point G of FIPS 186-5 and SEC 2.
	t.Run("KeyIDFromPub", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the first 16 bytes of the SHA-256 of the SEC 1 point", func(t *testing.T) {
			t.Parallel()
			params := elliptic.P384().Params()
			//nolint:staticcheck // the published coordinates of G are the fixture
			pub := &ecdsa.PublicKey{Curve: elliptic.P384(), X: params.Gx, Y: params.Gy}
			got, err := signecdsa.KeyIDFromPub(pub)
			assert.NoError(t, err, "KeyIDFromPub must accept the base point")
			assert.Equal(t, hex.EncodeToString(got[:]), "8c2eb3e0b8d6cc2a197a52c92860f7b1",
				"the KeyID must match SHA-256(SEC 1 point)[:16]")
		})

		t.Run("returns ErrOffCurve for a point off the curve", func(t *testing.T) {
			t.Parallel()
			_, err := signecdsa.KeyIDFromPub(offCurve)
			assert.ErrorIs(t, err, signecdsa.ErrOffCurve, "a point off the curve must be refused")
		})
	})
}

// TestECDSAP384Allocs checks the allocation ceiling of Resolve.
// MaxAllocs counts the allocations of the whole process, so the test
// does not run in parallel.
func TestECDSAP384Allocs(t *testing.T) {
	pub := mustSigner(t, cryptotest.NewECDSAP384Sample()).PublicKey()

	t.Run("Resolve", func(t *testing.T) {
		var v sign.Verifier
		expect.MaxAllocs(t, func() { v, _ = signecdsa.Resolve(pub) }, resolveAllocs,
			"Resolve must allocate only what the parse and the encoding of the key allocate")
		assert.NotNil(t, v, "the test must measure a resolution that succeeds")
	})
}

// BenchmarkECDSAP384Verifier runs the benchmarks of the contract suite
// of sign.Verifier.
func BenchmarkECDSAP384Verifier(b *testing.B) {
	signer := mustSigner(b, cryptotest.NewECDSAP384Sample())
	cryptotest.BenchmarkVerifierContract(b, func() sign.Verifier { return signer.Verifier })
}

// BenchmarkECDSAP384Signer runs the benchmarks of the contract suite of
// sign.Signer.
func BenchmarkECDSAP384Signer(b *testing.B) {
	signer := mustSigner(b, cryptotest.NewECDSAP384Sample())
	cryptotest.BenchmarkSignerContract(b, func() sign.Signer { return signer })
}

// BenchmarkECDSAP384SignStream runs the benchmarks of the contract suite
// of sign.SignStream.
func BenchmarkECDSAP384SignStream(b *testing.B) {
	signer := mustSigner(b, cryptotest.NewECDSAP384Sample())
	cryptotest.BenchmarkSignStreamContract(b,
		func() sign.SignStream { return signer.NewSignStream() },
	)
}

// BenchmarkECDSAP384VerifyStream runs the benchmarks of the contract
// suite of sign.VerifyStream.
func BenchmarkECDSAP384VerifyStream(b *testing.B) {
	signer := mustSigner(b, cryptotest.NewECDSAP384Sample())
	cryptotest.BenchmarkVerifyStreamContract(b,
		func() sign.VerifyStream { return signer.NewVerifyStream() },
	)
}

// BenchmarkECDSAP384 reports the cost of Resolve, and fails when it
// allocates more than TestECDSAP384Allocs allows.
func BenchmarkECDSAP384(b *testing.B) {
	b.Run("Resolve", func(b *testing.B) {
		pub := mustSigner(b, cryptotest.NewECDSAP384Sample()).PublicKey()
		var v sign.Verifier

		c := bench.Start(b).MaxAllocs(resolveAllocs)
		defer c.End()

		for c.Loop() {
			v, _ = signecdsa.Resolve(pub)
		}

		assert.NotNil(b, v, "the benchmark must measure a resolution that succeeds")
	})
}

// stdlibSign returns a function that signs msg with the ECDSA P-384 and
// SHA-384 of the standard library under priv. The cross-stdlib
// assertions use it as the reference. A signing error fails tb.
func stdlibSign(tb testing.TB, priv *ecdsa.PrivateKey) func([]byte) []byte {
	tb.Helper()

	return func(msg []byte) []byte {
		digest := sha512.Sum384(msg)
		sig, err := ecdsa.SignASN1(stdrand.Reader, priv, digest[:])
		assert.NoError(tb, err, "ecdsa.SignASN1 must sign")

		return sig
	}
}

// stdlibVerify reports whether sig is a valid ECDSA P-384 and SHA-384
// signature over msg under the PKIX-encoded pub, verified by the
// standard library.
func stdlibVerify(pub, msg, sig []byte) bool {
	parsed, err := x509.ParsePKIXPublicKey(pub)
	if err != nil {
		return false
	}
	pk, ok := parsed.(*ecdsa.PublicKey)
	if !ok {
		return false
	}
	digest := sha512.Sum384(msg)

	return ecdsa.VerifyASN1(pk, digest[:], sig)
}

// mustSigner wraps the private key of fix in a Signer. It fails tb when
// New refuses the key.
func mustSigner(tb testing.TB, fix cryptotest.ECDSAP384Fixture) *signecdsa.Signer {
	tb.Helper()

	s, err := signecdsa.New(fix.StdlibPriv)
	assert.NoError(tb, err, "New must accept the private key of the fixture")

	return s
}

// p256Key returns the P-256 private key whose scalar is 32 bytes of
// 0x01. It fails tb when ParseRawPrivateKey refuses the scalar.
func p256Key(tb testing.TB) *ecdsa.PrivateKey {
	tb.Helper()

	priv, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), bytes.Repeat([]byte{0x01}, 32))
	assert.NoError(tb, err, "ParseRawPrivateKey must accept the scalar")

	return priv
}
