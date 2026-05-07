// Copyright Thesmos 2026
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
	"errors"
	"io"
	"math/big"
	"testing"

	"go.thesmos.sh/core/coretest/cryptotest"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sign"
	signecdsa "go.thesmos.sh/core/crypto/sign/ecdsap384"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/seeded"
)

// stdlibSign returns the stdlib ECDSA P-384 + SHA-384 signature
// over msg under priv. Reference for cross-stdlib equivalence
// assertions; the consumer closes over the fixture priv at the
// call site. tb.Fatalf is wired in so the (essentially
// unreachable in Go 1.26) error path produces a clean test
// failure rather than a panic.
func stdlibSign(tb testing.TB, priv *ecdsa.PrivateKey) func([]byte) []byte {
	tb.Helper()
	return func(msg []byte) []byte {
		digest := sha512.Sum384(msg)
		sig, err := ecdsa.SignASN1(stdrand.Reader, priv, digest[:])
		if err != nil {
			tb.Fatalf("stdlib ecdsa.SignASN1: %v", err)
		}
		return sig
	}
}

// stdlibVerify reports whether sig is a valid ECDSA P-384 +
// SHA-384 signature over msg under pub (PKIX-encoded). Stateless
// reference — usable across fixtures without a closure.
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
	if err != nil {
		tb.Fatalf("ecdsap384.New from fixture: %v", err)
	}
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

// TestStreamingImplemented locks the streaming-asymmetry
// invariant: ECDSA P-384 (hash-then-sign) MUST satisfy
// [sign.StreamingSigner] / [sign.StreamingVerifier].
func TestStreamingImplemented(t *testing.T) {
	t.Parallel()
	signer := mustSigner(t, cryptotest.NewECDSAP384Sample())

	if _, ok := any(signer).(sign.StreamingSigner); !ok {
		t.Fatal("ECDSA P-384 Signer must implement sign.StreamingSigner")
	}
	if _, ok := any(signer.Verifier).(sign.StreamingVerifier); !ok {
		t.Fatal("ECDSA P-384 Verifier must implement sign.StreamingVerifier")
	}
}

func TestNewVerifier(t *testing.T) {
	t.Parallel()

	t.Run("rejects nil public key", func(t *testing.T) {
		t.Parallel()
		_, err := signecdsa.NewVerifier(nil)
		if !errors.Is(err, signecdsa.ErrNilKey) {
			t.Fatalf("got %v, want ErrNilKey", err)
		}
	})

	t.Run("rejects non-P-384 curve", func(t *testing.T) {
		t.Parallel()
		priv, err := ecdsa.GenerateKey(elliptic.P256(), randReader{r: seeded.New(rand.Seed(1))})
		if err != nil {
			t.Fatalf("GenerateKey(P-256): %v", err)
		}
		_, verr := signecdsa.NewVerifier(&priv.PublicKey)
		if !errors.Is(verr, signecdsa.ErrWrongCurve) {
			t.Fatalf("got %v, want ErrWrongCurve", verr)
		}
	})

	t.Run("rejects off-curve point (failure surfaces from KeyIDFromPub)", func(t *testing.T) {
		t.Parallel()
		// X=1, Y=2 is on Curve=P384 nominally but off the curve
		// mathematically. NewVerifier reaches KeyIDFromPub →
		// pub.Bytes(), which rejects.
		pub := &ecdsa.PublicKey{
			Curve: elliptic.P384(),
			X:     big.NewInt(1),
			Y:     big.NewInt(2),
		}
		_, err := signecdsa.NewVerifier(pub)
		if !errors.Is(err, signecdsa.ErrOffCurve) {
			t.Fatalf("got %v, want ErrOffCurve", err)
		}
	})
}

func TestNewVerifierFromPKIX(t *testing.T) {
	t.Parallel()

	t.Run("round-trips PublicKey() bytes", func(t *testing.T) {
		t.Parallel()
		fix := cryptotest.NewECDSAP384Sample()
		v, err := signecdsa.NewVerifierFromPKIX(fix.PublicKey)
		if err != nil {
			t.Fatalf("NewVerifierFromPKIX: %v", err)
		}
		if !bytes.Equal(v.PublicKey(), fix.PublicKey) {
			t.Fatal("PublicKey round-trip mismatch")
		}
		if v.KeyID() != fix.KeyID {
			t.Fatal("KeyID round-trip mismatch")
		}
	})

	t.Run("rejects malformed bytes", func(t *testing.T) {
		t.Parallel()
		cases := [][]byte{nil, {}, {0x00}, []byte("not asn.1 der at all")}
		for _, c := range cases {
			_, err := signecdsa.NewVerifierFromPKIX(c)
			if !errors.Is(err, signecdsa.ErrInvalidPublicKey) {
				t.Fatalf("len %d: got %v, want ErrInvalidPublicKey", len(c), err)
			}
		}
	})

	t.Run("rejects PKIX of a non-ECDSA key (Ed25519)", func(t *testing.T) {
		t.Parallel()
		pubBytes := buildEd25519PKIX(t)
		_, verr := signecdsa.NewVerifierFromPKIX(pubBytes)
		if !errors.Is(verr, signecdsa.ErrInvalidPublicKey) {
			t.Fatalf("got %v, want ErrInvalidPublicKey", verr)
		}
	})

	t.Run("rejects PKIX of a non-P-384 key", func(t *testing.T) {
		t.Parallel()
		priv, err := ecdsa.GenerateKey(elliptic.P256(), randReader{r: seeded.New(rand.Seed(1))})
		if err != nil {
			t.Fatalf("GenerateKey(P-256): %v", err)
		}
		pkix, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
		if err != nil {
			t.Fatalf("MarshalPKIX: %v", err)
		}
		_, verr := signecdsa.NewVerifierFromPKIX(pkix)
		if !errors.Is(verr, signecdsa.ErrWrongCurve) {
			t.Fatalf("got %v, want ErrWrongCurve", verr)
		}
	})

	t.Run("defensive copy: caller may mutate source after construction", func(t *testing.T) {
		t.Parallel()
		fix := cryptotest.NewECDSAP384Sample()
		src := append([]byte(nil), fix.PublicKey...)
		v, err := signecdsa.NewVerifierFromPKIX(src)
		if err != nil {
			t.Fatalf("NewVerifierFromPKIX: %v", err)
		}
		want := append([]byte(nil), src...)
		for i := range src {
			src[i] = 0
		}
		if !bytes.Equal(v.PublicKey(), want) {
			t.Fatal("Verifier aliases the caller's PKIX buffer")
		}
	})
}

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("rejects nil private key", func(t *testing.T) {
		t.Parallel()
		_, err := signecdsa.New(nil)
		if !errors.Is(err, signecdsa.ErrNilKey) {
			t.Fatalf("got %v, want ErrNilKey", err)
		}
	})

	t.Run("rejects non-P-384 private key", func(t *testing.T) {
		t.Parallel()
		priv, err := ecdsa.GenerateKey(elliptic.P256(), randReader{r: seeded.New(rand.Seed(1))})
		if err != nil {
			t.Fatalf("GenerateKey(P-256): %v", err)
		}
		_, nerr := signecdsa.New(priv)
		if !errors.Is(nerr, signecdsa.ErrWrongCurve) {
			t.Fatalf("got %v, want ErrWrongCurve", nerr)
		}
	})
}

func TestGenerate(t *testing.T) {
	t.Parallel()

	t.Run("successive calls produce different keypairs", func(t *testing.T) {
		t.Parallel()
		a, err := signecdsa.Generate()
		if err != nil {
			t.Fatalf("Generate (a): %v", err)
		}
		b, err := signecdsa.Generate()
		if err != nil {
			t.Fatalf("Generate (b): %v", err)
		}
		if bytes.Equal(a.PublicKey(), b.PublicKey()) {
			t.Fatal("successive Generate calls produced identical keypairs — entropy collision is astronomical")
		}
	})

	// NOTE: Go 1.26's [crypto/ecdsa.GenerateKey] ignores the
	// supplied reader and draws from the runtime's secure RNG
	// unless `GODEBUG=cryptocustomrand=1` is set. Deterministic
	// key generation for ECDSA P-384 is therefore not promised
	// at this seam.
}

func TestKeyIDStability(t *testing.T) {
	t.Parallel()

	t.Run("hardcoded vector locks SEC 1 + SHA-256[:16] derivation", func(t *testing.T) {
		t.Parallel()
		// Vector: the P-384 base point G (FIPS 186-5 / SEC 2).
		// Well-known on-curve coordinates that survive
		// [crypto/ecdsa.PublicKey.Bytes]'s curve-membership
		// check. The KeyID frozen below locks the exact
		// SEC 1 + SHA-256 + truncate pipeline.
		params := elliptic.P384().Params()
		pub := &ecdsa.PublicKey{
			Curve: elliptic.P384(),
			X:     params.Gx,
			Y:     params.Gy,
		}
		const wantHex = "8c2eb3e0b8d6cc2a197a52c92860f7b1"
		got, err := signecdsa.KeyIDFromPub(pub)
		if err != nil {
			t.Fatalf("KeyIDFromPub(G): %v", err)
		}
		gotHex := hex.EncodeToString(got[:])
		if gotHex != wantHex {
			t.Fatalf("KeyID encoding drift:\n got=%s\nwant=%s", gotHex, wantHex)
		}
	})

	t.Run("rejects off-curve point", func(t *testing.T) {
		t.Parallel()
		pub := &ecdsa.PublicKey{
			Curve: elliptic.P384(),
			X:     big.NewInt(1),
			Y:     big.NewInt(2),
		}
		_, err := signecdsa.KeyIDFromPub(pub)
		if !errors.Is(err, signecdsa.ErrOffCurve) {
			t.Fatalf("got %v, want ErrOffCurve", err)
		}
	})
}

// --- helpers ---

type randReader struct{ r rand.Rand }

func (rr randReader) Read(p []byte) (int, error) { return rr.r.Read(p) }

// Compile-time assertion: io.Reader is satisfied by randReader,
// so the adapter pattern works with any stdlib API expecting a
// reader (like ecdsa.GenerateKey).
var _ io.Reader = randReader{}

// buildEd25519PKIX returns PKIX-encoded bytes of a fresh Ed25519
// public key. Used to drive the NewVerifierFromPKIX-non-ECDSA
// failure path.
func buildEd25519PKIX(t *testing.T) []byte {
	t.Helper()
	pub, _, err := stded25519.GenerateKey(randReader{r: seeded.New(rand.Seed(1))})
	if err != nil {
		t.Fatalf("ed25519 GenerateKey: %v", err)
	}
	out, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("MarshalPKIX: %v", err)
	}
	return out
}
