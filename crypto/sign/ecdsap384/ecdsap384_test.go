// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package ecdsap384_test

import (
	"bytes"
	"crypto/ecdsa"
	stded25519 "crypto/ed25519"
	"crypto/elliptic"
	"crypto/sha512"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"io"
	"math/big"
	"testing"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sign"
	signecdsa "go.thesmos.sh/core/crypto/sign/ecdsap384"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/seeded"
)

func TestStreamingImplemented(t *testing.T) {
	t.Parallel()

	g := mustGenerate(t)

	if _, ok := any(g).(sign.StreamingSigner); !ok {
		t.Fatal("ECDSA P-384 Signer must implement sign.StreamingSigner")
	}
	if _, ok := any(g.Verifier).(sign.StreamingVerifier); !ok {
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
		// P-256 key, not P-384.
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
		if _, err := signecdsa.NewVerifier(pub); err == nil {
			t.Fatal("NewVerifier accepted an off-curve point")
		}
	})

	t.Run("accepts a valid P-384 public key (round-trip from Signer)", func(t *testing.T) {
		t.Parallel()
		s := mustGenerate(t)
		// Recover the *ecdsa.PublicKey from the signer's PKIX
		// bytes — the only way to get a true round-trip
		// without exposing the private key directly.
		parsed, err := x509.ParsePKIXPublicKey(s.PublicKey())
		if err != nil {
			t.Fatalf("ParsePKIX: %v", err)
		}
		pub, ok := parsed.(*ecdsa.PublicKey)
		if !ok {
			t.Fatal("ParsePKIX did not yield *ecdsa.PublicKey")
		}
		v, err := signecdsa.NewVerifier(pub)
		if err != nil {
			t.Fatalf("NewVerifier: %v", err)
		}
		if v.Algorithm() != crypto.AlgECDSAP384 {
			t.Fatalf("Algorithm: got %q, want %q", v.Algorithm(), crypto.AlgECDSAP384)
		}
		if v.KeyID() != s.KeyID() {
			t.Fatal("Verifier KeyID != Signer KeyID for the same public key")
		}
		// Sanity check: signature produced by the Signer
		// verifies under the freshly-constructed Verifier.
		msg := []byte("round-trip test")
		sig, _ := s.Sign(msg)
		if !v.Verify(msg, sig) {
			t.Fatal("Verifier built from Signer's PKIX did not verify the Signer's signature")
		}
	})
}

func TestNewVerifierFromPKIX(t *testing.T) {
	t.Parallel()

	t.Run("round-trips PublicKey() bytes", func(t *testing.T) {
		t.Parallel()
		s := mustGenerate(t)
		v, err := signecdsa.NewVerifierFromPKIX(s.PublicKey())
		if err != nil {
			t.Fatalf("NewVerifierFromPKIX: %v", err)
		}
		if !bytes.Equal(v.PublicKey(), s.PublicKey()) {
			t.Fatal("PublicKey round-trip mismatch")
		}
		if v.KeyID() != s.KeyID() {
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
		// Build an Ed25519 PKIX public key. ParsePKIX yields
		// ed25519.PublicKey, which fails the *ecdsa.PublicKey
		// type assertion.
		pubBytes := buildEd25519PKIX(t)
		_, verr := signecdsa.NewVerifierFromPKIX(pubBytes)
		if !errors.Is(verr, signecdsa.ErrInvalidPublicKey) {
			t.Fatalf("got %v, want ErrInvalidPublicKey", verr)
		}
	})

	t.Run("rejects PKIX of a non-P-384 key", func(t *testing.T) {
		t.Parallel()
		// Build a P-256 PKIX-encoded public key.
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
		s := mustGenerate(t)
		src := append([]byte(nil), s.PublicKey()...)
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

	t.Run("accepts a valid P-384 private key", func(t *testing.T) {
		t.Parallel()
		priv, err := ecdsa.GenerateKey(elliptic.P384(), randReader{r: seeded.New(rand.Seed(1))})
		if err != nil {
			t.Fatalf("GenerateKey(P-384): %v", err)
		}
		s, err := signecdsa.New(priv)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if s.Algorithm() != crypto.AlgECDSAP384 {
			t.Fatalf("Algorithm: got %q, want %q", s.Algorithm(), crypto.AlgECDSAP384)
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
	// at this seam — the Ed25519 sibling test asserts
	// determinism because [crypto/ed25519.GenerateKey] still
	// honours the reader.
}

func TestSignVerify(t *testing.T) {
	t.Parallel()

	s := mustGenerate(t)
	msg := []byte("the quick brown fox jumps over the lazy dog")

	t.Run("Sign then Verify accepts the canonical signature", func(t *testing.T) {
		t.Parallel()
		sig, err := s.Sign(msg)
		if err != nil {
			t.Fatalf("Sign: %v", err)
		}
		if !s.Verify(msg, sig) {
			t.Fatal("Verify rejected the canonical signature")
		}
	})

	t.Run("Verify rejects flipped signature bit", func(t *testing.T) {
		t.Parallel()
		sig, _ := s.Sign(msg)
		sig[0] ^= 0x01
		if s.Verify(msg, sig) {
			t.Fatal("Verify accepted a tampered signature")
		}
	})

	t.Run("Verify rejects flipped message bit", func(t *testing.T) {
		t.Parallel()
		sig, _ := s.Sign(msg)
		tampered := append([]byte(nil), msg...)
		tampered[0] ^= 0x01
		if s.Verify(tampered, sig) {
			t.Fatal("Verify accepted a signature over a tampered message")
		}
	})

	t.Run("Verify rejects malformed (non-DER) signature", func(t *testing.T) {
		t.Parallel()
		cases := [][]byte{
			nil,
			{},
			{0x00},
			make([]byte, 16),
			[]byte("not asn.1 der"),
		}
		for _, c := range cases {
			if s.Verify(msg, c) {
				t.Fatalf("Verify accepted malformed signature of length %d", len(c))
			}
		}
	})

	t.Run("different key rejects the signature", func(t *testing.T) {
		t.Parallel()
		other, err := signecdsa.Generate()
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		sig, _ := s.Sign(msg)
		if other.Verify(msg, sig) {
			t.Fatal("Verifier with a different key accepted the signature")
		}
	})

	t.Run("Verifier-only path verifies signatures from the matching Signer", func(t *testing.T) {
		t.Parallel()
		sig, _ := s.Sign(msg)
		v, err := signecdsa.NewVerifierFromPKIX(s.PublicKey())
		if err != nil {
			t.Fatalf("NewVerifierFromPKIX: %v", err)
		}
		if !v.Verify(msg, sig) {
			t.Fatal("Verifier-only path rejected a valid signature")
		}
		if v.KeyID() != s.KeyID() {
			t.Fatal("Verifier KeyID != Signer KeyID for the same public key")
		}
	})
}

func TestStreamingSign(t *testing.T) {
	t.Parallel()

	s := mustGenerate(t)
	full := []byte("agentic-context-payload-streamed-across-many-writes")

	t.Run("streamed sign verifies under whole-message Verify", func(t *testing.T) {
		t.Parallel()
		ss := s.NewSignStream()
		_, _ = ss.Write(full[:10])
		_, _ = ss.Write(full[10:])
		sig, err := ss.SignAndReset()
		if err != nil {
			t.Fatalf("SignAndReset: %v", err)
		}
		if !s.Verify(full, sig) {
			t.Fatal("Verify rejected a streamed signature")
		}
	})

	t.Run("byte-by-byte writes equal whole-message Sign", func(t *testing.T) {
		t.Parallel()
		ss := s.NewSignStream()
		for i := range full {
			_, _ = ss.Write(full[i : i+1])
		}
		sig, err := ss.SignAndReset()
		if err != nil {
			t.Fatalf("SignAndReset: %v", err)
		}
		// Note: ECDSA sigs are non-deterministic (per Go 1.26's
		// internal RNG). Equality of the *signatures* would
		// flake; instead assert the streamed sig verifies.
		if !s.Verify(full, sig) {
			t.Fatal("byte-by-byte streamed sig did not verify")
		}
	})

	t.Run("SignAndReset resets state for reuse", func(t *testing.T) {
		t.Parallel()
		ss := s.NewSignStream()
		_, _ = ss.Write([]byte("first message"))
		_, _ = ss.SignAndReset()

		// After reset, writing a different message and signing
		// must yield a signature valid for THAT message — not
		// for the concatenation of both.
		_, _ = ss.Write([]byte("second"))
		sig, err := ss.SignAndReset()
		if err != nil {
			t.Fatalf("SignAndReset: %v", err)
		}
		if !s.Verify([]byte("second"), sig) {
			t.Fatal("post-reset signature did not verify against the new message")
		}
		if s.Verify([]byte("first messagesecond"), sig) {
			t.Fatal("post-reset signature verified against the concatenated messages — stream did not reset")
		}
	})
}

func TestStreamingVerify(t *testing.T) {
	t.Parallel()

	s := mustGenerate(t)
	full := []byte("agentic-context-payload-streamed-across-many-writes")
	sig, err := s.Sign(full)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	t.Run("streamed verify accepts a valid whole-message signature", func(t *testing.T) {
		t.Parallel()
		vs := s.NewVerifyStream()
		_, _ = vs.Write(full)
		if !vs.Verify(sig) {
			t.Fatal("streaming Verify rejected a valid signature")
		}
	})

	t.Run("split writes equal whole-message verify", func(t *testing.T) {
		t.Parallel()
		vs := s.NewVerifyStream()
		_, _ = vs.Write(full[:10])
		_, _ = vs.Write(full[10:30])
		_, _ = vs.Write(full[30:])
		if !vs.Verify(sig) {
			t.Fatal("split-write Verify rejected a valid signature")
		}
	})

	t.Run("byte-by-byte writes equal whole-message verify", func(t *testing.T) {
		t.Parallel()
		vs := s.NewVerifyStream()
		for i := range full {
			_, _ = vs.Write(full[i : i+1])
		}
		if !vs.Verify(sig) {
			t.Fatal("byte-by-byte Verify rejected a valid signature")
		}
	})

	t.Run("rejects a signature over different bytes", func(t *testing.T) {
		t.Parallel()
		vs := s.NewVerifyStream()
		_, _ = vs.Write([]byte("not the message that was signed"))
		if vs.Verify(sig) {
			t.Fatal("streaming Verify accepted a signature over different bytes")
		}
	})
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
		// X=1, Y=2 is not on P-384. pub.Bytes() rejects;
		// KeyIDFromPub propagates the error.
		pub := &ecdsa.PublicKey{
			Curve: elliptic.P384(),
			X:     big.NewInt(1),
			Y:     big.NewInt(2),
		}
		if _, err := signecdsa.KeyIDFromPub(pub); err == nil {
			t.Fatal("KeyIDFromPub accepted an off-curve point")
		}
	})

	t.Run("different pub produces different KeyID", func(t *testing.T) {
		t.Parallel()
		a := mustGenerate(t).KeyID()
		other, err := signecdsa.Generate()
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if a == other.KeyID() {
			t.Fatal("distinct pubs collided to the same KeyID — astronomical, check derivation")
		}
	})
}

func TestCrossCheckStdlib(t *testing.T) {
	t.Parallel()

	s := mustGenerate(t)
	msg := []byte("payload that round-trips between our seam and stdlib ECDSA P-384")

	t.Run("our Sign verifies under stdlib", func(t *testing.T) {
		t.Parallel()
		sig, err := s.Sign(msg)
		if err != nil {
			t.Fatalf("Sign: %v", err)
		}
		// Reconstruct an *ecdsa.PublicKey from our PKIX bytes.
		parsed, err := x509.ParsePKIXPublicKey(s.PublicKey())
		if err != nil {
			t.Fatalf("ParsePKIX: %v", err)
		}
		pub, ok := parsed.(*ecdsa.PublicKey)
		if !ok {
			t.Fatal("ParsePKIX did not yield *ecdsa.PublicKey")
		}
		digest := sha512.Sum384(msg)
		if !ecdsa.VerifyASN1(pub, digest[:], sig) {
			t.Fatal("stdlib rejected our signature")
		}
	})
}

func TestZeroAlloc(t *testing.T) {
	s := mustGenerate(t)

	// Verify, Sign, SignAndReset, and stream finalisation are
	// intentionally absent: ECDSA P-384 verification does
	// big.Int arithmetic ([ecdsa.VerifyASN1]) which allocates,
	// and signing returns a freshly-allocated DER slice
	// ([ecdsa.SignASN1]) — both stdlib constraints. The
	// package doc documents this honestly. The cases below
	// cover every method this package documents as zero-alloc:
	// the cheap getters and the streaming Write paths (which
	// are absorb-only into a SHA-384 [hash.Hash] state on the
	// receiver).
	signStream := s.NewSignStream()
	verifyStream := s.NewVerifyStream()
	buf := make([]byte, 256)

	cases := []struct {
		name string
		fn   func()
	}{
		{"KeyID", func() { _ = s.KeyID() }},
		{"PublicKey", func() { _ = s.PublicKey() }},
		{"Algorithm", func() { _ = s.Algorithm() }},
		{"SignStream.Write", func() { _, _ = signStream.Write(buf) }},
		{"VerifyStream.Write", func() { _, _ = verifyStream.Write(buf) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := testing.AllocsPerRun(100, tc.fn); got != 0 {
				t.Fatalf("%s: %v allocs/op, want 0", tc.name, got)
			}
		})
	}
}

func FuzzVerifyNeverPanics(f *testing.F) {
	s, err := signecdsa.Generate()
	if err != nil {
		f.Fatalf("Generate: %v", err)
	}
	f.Add([]byte("msg"), make([]byte, 72))
	f.Add([]byte{}, []byte{})
	f.Add([]byte("a"), []byte{0xff})

	f.Fuzz(func(_ *testing.T, msg, sig []byte) {
		_ = s.Verify(msg, sig)
	})
}

func FuzzSignVerifyRoundTrip(f *testing.F) {
	s, err := signecdsa.Generate()
	if err != nil {
		f.Fatalf("Generate: %v", err)
	}
	f.Add([]byte("hi"))
	f.Add([]byte{})
	f.Add(make([]byte, 1024))

	f.Fuzz(func(t *testing.T, msg []byte) {
		sig, err := s.Sign(msg)
		if err != nil {
			t.Fatalf("Sign: %v", err)
		}
		if !s.Verify(msg, sig) {
			t.Fatal("Verify rejected canonical signature")
		}
		if len(sig) > 4 {
			tampered := append([]byte(nil), sig...)
			// Flip a bit in the body (skip leading DER tag/length).
			tampered[len(tampered)/2] ^= 0x01
			if s.Verify(msg, tampered) {
				t.Fatal("Verify accepted a tampered signature")
			}
		}
	})
}

func BenchmarkSign(b *testing.B) {
	s, err := signecdsa.Generate()
	if err != nil {
		b.Fatalf("Generate: %v", err)
	}
	for _, sz := range []struct {
		name string
		n    int
	}{
		{"64B", 64},
		{"1K", 1024},
		{"64K", 65536},
	} {
		b.Run(sz.name, func(b *testing.B) {
			// sink read past the loop forces the signature
			// slice to escape; matches production reality
			// where the DER bytes are persisted or sent.
			var sink []byte
			data := make([]byte, sz.n)
			b.ReportAllocs()
			b.SetBytes(int64(sz.n))
			for b.Loop() {
				sink, _ = s.Sign(data)
			}
			if len(sink) == 0 {
				b.Fatal("sink unexpectedly empty after loop")
			}
		})
	}
}

func BenchmarkVerify(b *testing.B) {
	s, err := signecdsa.Generate()
	if err != nil {
		b.Fatalf("Generate: %v", err)
	}
	for _, sz := range []struct {
		name string
		n    int
	}{
		{"64B", 64},
		{"1K", 1024},
		{"64K", 65536},
	} {
		b.Run(sz.name, func(b *testing.B) {
			data := make([]byte, sz.n)
			sig, _ := s.Sign(data)
			b.ReportAllocs()
			b.SetBytes(int64(sz.n))
			for b.Loop() {
				_ = s.Verify(data, sig)
			}
		})
	}
}

// BenchmarkGenerate measures keypair-generation cost. Setup-
// path benchmark — every consumer pays this once per Signer
// before any signing happens. ECDSA P-384 generation is
// substantially heavier than [crypto/sign/ed25519.Generate]
// because the P-384 base-point operation is expensive.
func BenchmarkGenerate(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		s, err := signecdsa.Generate()
		if err != nil {
			b.Fatalf("Generate: %v", err)
		}
		_ = s
	}
}

// BenchmarkKeyIDFromPub measures the KeyID-derivation cost over
// the SEC 1 uncompressed point encoding. Hot for verifier
// services that route receipts through a trust store keyed by
// [sign.KeyID].
func BenchmarkKeyIDFromPub(b *testing.B) {
	s, err := signecdsa.Generate()
	if err != nil {
		b.Fatalf("Generate: %v", err)
	}
	parsed, err := x509.ParsePKIXPublicKey(s.PublicKey())
	if err != nil {
		b.Fatalf("ParsePKIXPublicKey: %v", err)
	}
	pub, ok := parsed.(*ecdsa.PublicKey)
	if !ok {
		b.Fatalf("parsed key is %T, want *ecdsa.PublicKey", parsed)
	}
	b.ReportAllocs()
	for b.Loop() {
		_, err := signecdsa.KeyIDFromPub(pub)
		if err != nil {
			b.Fatalf("KeyIDFromPub: %v", err)
		}
	}
}

func mustGenerate(t *testing.T) *signecdsa.Signer {
	t.Helper()
	s, err := signecdsa.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return s
}

type randReader struct{ r rand.Rand }

func (rr randReader) Read(p []byte) (int, error) { return rr.r.Read(p) }

// Compile-time assertion: io.Reader is satisfied by randReader,
// so the adapter pattern works with any stdlib API expecting a
// reader (like ecdsa.GenerateKey).
var _ io.Reader = randReader{}

// buildEd25519PKIX returns PKIX-encoded bytes of a fresh
// Ed25519 public key. Used to drive the
// NewVerifierFromPKIX-non-ECDSA failure path.
func buildEd25519PKIX(t *testing.T) []byte {
	t.Helper()
	pub, _, err := stded25519.GenerateKey(randReader{r: seeded.New(rand.Seed(1))})
	if err != nil {
		t.Fatalf("ed25519 GenerateKey: %v", err)
	}
	bytes, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("MarshalPKIX: %v", err)
	}
	return bytes
}
