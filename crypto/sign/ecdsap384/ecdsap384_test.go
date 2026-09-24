// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package ecdsap384_test

import (
	"crypto/ecdsa"
	stded25519 "crypto/ed25519"
	"crypto/elliptic"
	stdrand "crypto/rand"
	"crypto/sha512"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"io"
	"math/big"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/coretest/cryptotest"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sign"
	signecdsa "go.thesmos.sh/core/crypto/sign/ecdsap384"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/seeded"
)

// stdlibSign returns a function that signs msg with the stdlib's
// ECDSA P-384 + SHA-384 under priv. The cross-stdlib assertions use
// it as the reference. A signing error fails tb.
func stdlibSign(tb testing.TB, priv *ecdsa.PrivateKey) func([]byte) []byte {
	tb.Helper()
	return func(msg []byte) []byte {
		digest := sha512.Sum384(msg)
		sig, err := ecdsa.SignASN1(stdrand.Reader, priv, digest[:])
		testkit.NoError(tb, err, "stdlib ecdsa.SignASN1")
		return sig
	}
}

// stdlibVerify reports whether sig is a valid ECDSA P-384 +
// SHA-384 signature over msg under the PKIX-encoded pub, verified by
// the stdlib.
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

// mustSigner wraps a fixture priv in a [signecdsa.Signer]. Caller
// supplies the fixture so the same instance is reused across the
// rest of the test (Sample / KeyID / etc.) without a second
// [cryptotest.NewECDSAP384Sample] call.
func mustSigner(tb testing.TB, fix cryptotest.ECDSAP384Fixture) *signecdsa.Signer {
	tb.Helper()
	s, err := signecdsa.New(fix.StdlibPriv)
	testkit.NoError(tb, err, "ecdsap384.New from fixture")
	return s
}

// --- testkit-driven contract layer ---

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
		)...,
	)
}

func TestECDSAP384SignStreamContract(t *testing.T) {
	t.Parallel()
	signer := mustSigner(t, cryptotest.NewECDSAP384Sample())
	verify := func(msg, sig []byte) bool { return signer.Verify(msg, sig) }

	cryptotest.AssertSignStreamContract(t,
		func() sign.SignStream { return signer.NewSignStream() },
		cryptotest.SignStreamContractAssertions(verify)...,
	)
}

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

func BenchmarkECDSAP384Verifier(b *testing.B) {
	signer := mustSigner(b, cryptotest.NewECDSAP384Sample())
	cryptotest.BenchmarkVerifierContract(b, func() sign.Verifier { return signer.Verifier })
}

func BenchmarkECDSAP384Signer(b *testing.B) {
	signer := mustSigner(b, cryptotest.NewECDSAP384Sample())
	cryptotest.BenchmarkSignerContract(b, func() sign.Signer { return signer })
}

func BenchmarkECDSAP384SignStream(b *testing.B) {
	signer := mustSigner(b, cryptotest.NewECDSAP384Sample())
	cryptotest.BenchmarkSignStreamContract(b,
		func() sign.SignStream { return signer.NewSignStream() },
	)
}

func BenchmarkECDSAP384VerifyStream(b *testing.B) {
	signer := mustSigner(b, cryptotest.NewECDSAP384Sample())
	cryptotest.BenchmarkVerifyStreamContract(b,
		func() sign.VerifyStream { return signer.NewVerifyStream() },
	)
}

// --- impl-specific tests ---

// TestStreamingImplemented checks that ECDSA P-384, a hash-then-sign
// algorithm, implements [sign.StreamingSigner] and
// [sign.StreamingVerifier].
func TestStreamingImplemented(t *testing.T) {
	t.Parallel()

	t.Run("the Signer is a StreamingSigner", func(t *testing.T) {
		t.Parallel()
		_, ok := any(mustSigner(t, cryptotest.NewECDSAP384Sample())).(sign.StreamingSigner)
		testkit.True(t, ok, "ECDSA P-384 Signer must implement sign.StreamingSigner")
	})

	t.Run("the Verifier is a StreamingVerifier", func(t *testing.T) {
		t.Parallel()
		_, ok := any(mustSigner(t, cryptotest.NewECDSAP384Sample()).Verifier).(sign.StreamingVerifier)
		testkit.True(t, ok, "ECDSA P-384 Verifier must implement sign.StreamingVerifier")
	})
}

// TestNewVerifier covers the constructor's refusals. The point X=1,
// Y=2 names P-384 as its curve but is not on it, so KeyIDFromPub
// refuses it through [ecdsa.PublicKey.Bytes].
func TestNewVerifier(t *testing.T) {
	t.Parallel()

	t.Run("rejects nil public key", func(t *testing.T) {
		t.Parallel()
		_, err := signecdsa.NewVerifier(nil)
		testkit.ErrorIs(t, err, signecdsa.ErrNilKey, "nil pub must return ErrNilKey")
	})

	t.Run("rejects non-P-384 curve", func(t *testing.T) {
		t.Parallel()
		priv, err := ecdsa.GenerateKey(elliptic.P256(), randReader{r: seeded.New(rand.Seed(1))})
		testkit.NoError(t, err, "GenerateKey(P-256)")
		_, verr := signecdsa.NewVerifier(&priv.PublicKey)
		testkit.ErrorIs(t, verr, signecdsa.ErrWrongCurve, "P-256 pub must return ErrWrongCurve")
	})

	t.Run("rejects an off-curve point through KeyIDFromPub", func(t *testing.T) {
		t.Parallel()
		//nolint:staticcheck // raw coordinates are deprecated because they
		// can build an invalid key. An invalid key is the subject here.
		pub := &ecdsa.PublicKey{
			Curve: elliptic.P384(),
			X:     big.NewInt(1),
			Y:     big.NewInt(2),
		}
		_, err := signecdsa.NewVerifier(pub)
		testkit.ErrorIs(t, err, signecdsa.ErrOffCurve, "off-curve point must return ErrOffCurve")
	})
}

func TestNewVerifierFromPKIX(t *testing.T) {
	t.Parallel()

	t.Run("round-trips PublicKey() bytes", func(t *testing.T) {
		t.Parallel()
		fix := cryptotest.NewECDSAP384Sample()
		v, err := signecdsa.NewVerifierFromPKIX(fix.PublicKey)
		testkit.NoError(t, err, "NewVerifierFromPKIX")
		testkit.Equal(t, v.PublicKey(), fix.PublicKey, "PublicKey round-trip must preserve bytes")
		testkit.Equal(t, v.KeyID(), fix.KeyID, "KeyID round-trip must preserve identity")
	})

	t.Run("rejects malformed bytes", func(t *testing.T) {
		t.Parallel()
		cases := [][]byte{nil, {}, {0x00}, []byte("not asn.1 der at all")}
		for _, c := range cases {
			_, err := signecdsa.NewVerifierFromPKIX(c)
			testkit.ErrorIs(t, err, signecdsa.ErrInvalidPublicKey,
				fmt.Sprintf("len %d malformed bytes must return ErrInvalidPublicKey", len(c)))
		}
	})

	t.Run("rejects PKIX of a non-ECDSA key (Ed25519)", func(t *testing.T) {
		t.Parallel()
		pubBytes := buildEd25519PKIX(t)
		_, verr := signecdsa.NewVerifierFromPKIX(pubBytes)
		testkit.ErrorIs(t, verr, signecdsa.ErrInvalidPublicKey,
			"Ed25519 PKIX must return ErrInvalidPublicKey")
	})

	t.Run("rejects PKIX of a non-P-384 key", func(t *testing.T) {
		t.Parallel()
		priv, err := ecdsa.GenerateKey(elliptic.P256(), randReader{r: seeded.New(rand.Seed(1))})
		testkit.NoError(t, err, "GenerateKey(P-256)")
		pkix, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
		testkit.NoError(t, err, "MarshalPKIX")
		_, verr := signecdsa.NewVerifierFromPKIX(pkix)
		testkit.ErrorIs(t, verr, signecdsa.ErrWrongCurve,
			"P-256 PKIX must return ErrWrongCurve")
	})

	t.Run("copies the source buffer", func(t *testing.T) {
		t.Parallel()
		fix := cryptotest.NewECDSAP384Sample()
		src := append([]byte(nil), fix.PublicKey...)
		v, err := signecdsa.NewVerifierFromPKIX(src)
		testkit.NoError(t, err, "NewVerifierFromPKIX")
		want := append([]byte(nil), src...)
		clear(src)
		testkit.Equal(t, v.PublicKey(), want,
			"zeroing the caller's buffer must not change the Verifier's key")
	})
}

func TestResolve(t *testing.T) {
	t.Parallel()

	t.Run("returns a Verifier for the PKIX public key", func(t *testing.T) {
		t.Parallel()
		fix := cryptotest.NewECDSAP384Sample()
		v, err := signecdsa.Resolve(fix.PublicKey)
		testkit.NoError(t, err, "Resolve must accept a PKIX P-384 key")
		testkit.Equal(t, v.KeyID(), fix.KeyID, "the Verifier must have the key's KeyID")
		testkit.True(t, v.Verify(fix.Message, fix.Signature), "the Verifier must accept the key's signature")
	})

	t.Run("returns ErrInvalidPublicKey and a nil Verifier", func(t *testing.T) {
		t.Parallel()
		v, err := signecdsa.Resolve([]byte("not a PKIX key"))
		testkit.ErrorIs(t, err, signecdsa.ErrInvalidPublicKey, "malformed bytes must be refused")
		testkit.True(t, v == nil, "the Verifier must be a nil interface")
	})
}

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("rejects nil private key", func(t *testing.T) {
		t.Parallel()
		_, err := signecdsa.New(nil)
		testkit.ErrorIs(t, err, signecdsa.ErrNilKey, "nil priv must return ErrNilKey")
	})

	t.Run("rejects non-P-384 private key", func(t *testing.T) {
		t.Parallel()
		priv, err := ecdsa.GenerateKey(elliptic.P256(), randReader{r: seeded.New(rand.Seed(1))})
		testkit.NoError(t, err, "GenerateKey(P-256)")
		_, nerr := signecdsa.New(priv)
		testkit.ErrorIs(t, nerr, signecdsa.ErrWrongCurve,
			"P-256 priv must return ErrWrongCurve")
	})
}

// TestGenerate covers key generation. [crypto/ecdsa.GenerateKey]
// ignores the supplied reader and uses the runtime's secure RNG unless
// `GODEBUG=cryptocustomrand=1` is set, so the seam does not promise
// deterministic ECDSA P-384 keys and no test asserts them.
func TestGenerate(t *testing.T) {
	t.Parallel()

	t.Run("successive calls produce different keypairs", func(t *testing.T) {
		t.Parallel()
		a, err := signecdsa.Generate()
		testkit.NoError(t, err, "Generate (a)")
		b, err := signecdsa.Generate()
		testkit.NoError(t, err, "Generate (b)")
		testkit.NotEqual(t, a.PublicKey(), b.PublicKey(),
			"successive Generate calls must produce distinct keypairs")
	})
}

// TestKeyIDStability pins the KeyID derivation, SHA-256 of the SEC 1
// uncompressed point truncated to 16 bytes, for the P-384 base point
// G of FIPS 186-5 and SEC 2. G is on the curve, so
// [crypto/ecdsa.PublicKey.Bytes] accepts it.
func TestKeyIDStability(t *testing.T) {
	t.Parallel()

	t.Run("matches SEC 1 + SHA-256[:16] for the base point", func(t *testing.T) {
		t.Parallel()
		params := elliptic.P384().Params()
		//nolint:staticcheck // the published coordinates of G are the
		// fixture; parsing an encoding of them would test the parser.
		pub := &ecdsa.PublicKey{
			Curve: elliptic.P384(),
			X:     params.Gx,
			Y:     params.Gy,
		}
		const wantHex = "8c2eb3e0b8d6cc2a197a52c92860f7b1"
		got, err := signecdsa.KeyIDFromPub(pub)
		testkit.NoError(t, err, "KeyIDFromPub(G)")
		testkit.Equal(t, hex.EncodeToString(got[:]), wantHex,
			"KeyID encoding must match SEC 1 + SHA-256[:16]")
	})

	t.Run("rejects off-curve point", func(t *testing.T) {
		t.Parallel()
		//nolint:staticcheck // raw coordinates are deprecated because they
		// can build an invalid key. An invalid key is the subject here.
		pub := &ecdsa.PublicKey{
			Curve: elliptic.P384(),
			X:     big.NewInt(1),
			Y:     big.NewInt(2),
		}
		_, err := signecdsa.KeyIDFromPub(pub)
		testkit.ErrorIs(t, err, signecdsa.ErrOffCurve, "off-curve point must return ErrOffCurve")
	})
}

// --- helpers ---

type randReader struct{ r rand.Rand }

func (rr randReader) Read(p []byte) (int, error) { return rr.r.Read(p) }

// Compile-time assertion that randReader is an [io.Reader], as
// stdlib functions such as ecdsa.GenerateKey require.
var _ io.Reader = randReader{}

// buildEd25519PKIX returns the PKIX encoding of a fresh Ed25519
// public key, which NewVerifierFromPKIX must refuse.
func buildEd25519PKIX(t *testing.T) []byte {
	t.Helper()
	pub, _, err := stded25519.GenerateKey(randReader{r: seeded.New(rand.Seed(1))})
	testkit.NoError(t, err, "ed25519 GenerateKey")
	out, err := x509.MarshalPKIXPublicKey(pub)
	testkit.NoError(t, err, "MarshalPKIX")
	return out
}
