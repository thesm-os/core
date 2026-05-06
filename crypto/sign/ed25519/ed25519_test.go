// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package ed25519_test

import (
	"bytes"
	stded25519 "crypto/ed25519"
	"encoding/hex"
	"errors"
	"testing"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sign"
	signed25519 "go.thesmos.sh/core/crypto/sign/ed25519"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/seeded"
)

// signEd25519 is the streaming-asymmetry guard: Ed25519
// PureEdDSA must NOT satisfy [sign.StreamingSigner] /
// [sign.StreamingVerifier]. RFC 8032 §5.1.6 needs the message
// in two SHA-512 computations, the second depending on the
// first; a streaming API would force internal buffering.
func TestStreamingNotImplemented(t *testing.T) {
	t.Parallel()

	g := mustGenerate(t)

	if _, ok := any(g).(sign.StreamingSigner); ok {
		t.Fatal("Ed25519 Signer must NOT implement sign.StreamingSigner")
	}
	if _, ok := any(g.Verifier).(sign.StreamingVerifier); ok {
		t.Fatal("Ed25519 Verifier must NOT implement sign.StreamingVerifier")
	}
}

func TestNewVerifier(t *testing.T) {
	t.Parallel()

	t.Run("accepts a valid 32-byte public key", func(t *testing.T) {
		t.Parallel()
		pub := make(stded25519.PublicKey, stded25519.PublicKeySize)
		v, err := signed25519.NewVerifier(pub)
		if err != nil {
			t.Fatalf("NewVerifier: %v", err)
		}
		if got := v.Algorithm(); got != crypto.AlgEd25519 {
			t.Fatalf("Algorithm: got %q, want %q", got, crypto.AlgEd25519)
		}
		if !bytes.Equal(v.PublicKey(), pub) {
			t.Fatalf("PublicKey: got %x, want %x", v.PublicKey(), pub)
		}
	})

	t.Run("rejects wrong-size public key", func(t *testing.T) {
		t.Parallel()
		cases := [][]byte{
			nil,
			make([]byte, 0),
			make([]byte, 16),
			make([]byte, 31),
			make([]byte, 33),
			make([]byte, 64),
		}
		for _, c := range cases {
			_, err := signed25519.NewVerifier(stded25519.PublicKey(c))
			if !errors.Is(err, signed25519.ErrInvalidPublicKeySize) {
				t.Fatalf("len %d: got %v, want ErrInvalidPublicKeySize", len(c), err)
			}
		}
	})
}

func TestNewVerifierFromBytes(t *testing.T) {
	t.Parallel()

	t.Run("copies the source buffer", func(t *testing.T) {
		t.Parallel()
		src := make([]byte, stded25519.PublicKeySize)
		for i := range src {
			src[i] = byte(i + 1)
		}
		v, err := signed25519.NewVerifierFromBytes(src)
		if err != nil {
			t.Fatalf("NewVerifierFromBytes: %v", err)
		}
		// Mutate source — internal copy must be unaffected.
		want := append([]byte(nil), src...)
		for i := range src {
			src[i] = 0
		}
		if !bytes.Equal(v.PublicKey(), want) {
			t.Fatal("Verifier aliases the caller's buffer")
		}
	})

	t.Run("rejects wrong-size byte slice", func(t *testing.T) {
		t.Parallel()
		_, err := signed25519.NewVerifierFromBytes(make([]byte, 16))
		if !errors.Is(err, signed25519.ErrInvalidPublicKeySize) {
			t.Fatalf("got %v, want ErrInvalidPublicKeySize", err)
		}
	})
}

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("accepts a valid 64-byte private key", func(t *testing.T) {
		t.Parallel()
		_, priv, err := stded25519.GenerateKey(randReader{r: seeded.New(rand.Seed(1))})
		if err != nil {
			t.Fatalf("GenerateKey: %v", err)
		}
		s, err := signed25519.New(priv)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if got := s.Algorithm(); got != crypto.AlgEd25519 {
			t.Fatalf("Algorithm: got %q, want %q", got, crypto.AlgEd25519)
		}
		if got := len(s.PublicKey()); got != stded25519.PublicKeySize {
			t.Fatalf("PublicKey size: got %d, want %d", got, stded25519.PublicKeySize)
		}
	})

	t.Run("copies the source private key", func(t *testing.T) {
		t.Parallel()
		_, priv, err := stded25519.GenerateKey(randReader{r: seeded.New(rand.Seed(1))})
		if err != nil {
			t.Fatalf("GenerateKey: %v", err)
		}
		s, err := signed25519.New(priv)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		msg := []byte("test message")
		want, _ := s.Sign(msg)

		// Zero the caller's buffer — secure-coding practice.
		// The Signer must continue producing valid signatures
		// from its internal copy.
		for i := range priv {
			priv[i] = 0
		}
		got, _ := s.Sign(msg)
		if !bytes.Equal(got, want) {
			t.Fatal("Signer's signature changed after caller zeroed source key — defensive copy missing")
		}
		if !s.Verify(msg, got) {
			t.Fatal("Signer's signature failed to verify after caller zeroed source key")
		}
	})

	t.Run("rejects wrong-size private key", func(t *testing.T) {
		t.Parallel()
		cases := [][]byte{
			nil,
			make([]byte, 0),
			make([]byte, 32),
			make([]byte, 63),
			make([]byte, 65),
			make([]byte, 128),
		}
		for _, c := range cases {
			_, err := signed25519.New(stded25519.PrivateKey(c))
			if !errors.Is(err, signed25519.ErrInvalidPrivateKeySize) {
				t.Fatalf("len %d: got %v, want ErrInvalidPrivateKeySize", len(c), err)
			}
		}
	})
}

func TestGenerate(t *testing.T) {
	t.Parallel()

	t.Run("two seeds produce different keypairs", func(t *testing.T) {
		t.Parallel()
		a, err := signed25519.Generate(seeded.New(rand.Seed(1)))
		if err != nil {
			t.Fatalf("Generate(seed=1): %v", err)
		}
		b, err := signed25519.Generate(seeded.New(rand.Seed(2)))
		if err != nil {
			t.Fatalf("Generate(seed=2): %v", err)
		}
		if bytes.Equal(a.PublicKey(), b.PublicKey()) {
			t.Fatal("distinct seeds produced identical keypairs")
		}
	})

	t.Run("propagates entropy-source failure", func(t *testing.T) {
		t.Parallel()
		// errRand returns an error from every Read; the
		// stdlib's [ed25519.GenerateKey] surfaces it, and our
		// wrapper wraps it for context.
		_, err := signed25519.Generate(errRand{})
		if err == nil {
			t.Fatal("Generate accepted a failing entropy source")
		}
	})

	t.Run("deterministic across runs (same seed → same keypair)", func(t *testing.T) {
		t.Parallel()
		a, err := signed25519.Generate(seeded.New(rand.Seed(42)))
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		b, err := signed25519.Generate(seeded.New(rand.Seed(42)))
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if !bytes.Equal(a.PublicKey(), b.PublicKey()) {
			t.Fatal("same seed produced different public keys")
		}
		if a.KeyID() != b.KeyID() {
			t.Fatal("same seed produced different KeyIDs")
		}
	})
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
		if got := len(sig); got != stded25519.SignatureSize {
			t.Fatalf("signature size: got %d, want %d", got, stded25519.SignatureSize)
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

	t.Run("Verify rejects wrong-length signature", func(t *testing.T) {
		t.Parallel()
		// Below, exact-1, exact+1, far above.
		cases := [][]byte{nil, make([]byte, 32), make([]byte, 63), make([]byte, 65), make([]byte, 128)}
		for _, c := range cases {
			if s.Verify(msg, c) {
				t.Fatalf("Verify accepted a signature of length %d", len(c))
			}
		}
	})

	t.Run("different key rejects the signature", func(t *testing.T) {
		t.Parallel()
		other, err := signed25519.Generate(seeded.New(rand.Seed(999)))
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
		v, err := signed25519.NewVerifier(stded25519.PublicKey(s.PublicKey()))
		if err != nil {
			t.Fatalf("NewVerifier: %v", err)
		}
		if !v.Verify(msg, sig) {
			t.Fatal("Verifier-only path rejected a valid signature")
		}
		if v.KeyID() != s.KeyID() {
			t.Fatal("Verifier KeyID != Signer KeyID for the same public key")
		}
	})

	t.Run("Sign over empty message is well-defined", func(t *testing.T) {
		t.Parallel()
		sig, err := s.Sign(nil)
		if err != nil {
			t.Fatalf("Sign(nil): %v", err)
		}
		if !s.Verify(nil, sig) {
			t.Fatal("Verify rejected a signature over nil message")
		}
		if !s.Verify([]byte{}, sig) {
			t.Fatal("nil and empty-slice messages must produce identical signatures")
		}
	})
}

func TestKeyIDStability(t *testing.T) {
	t.Parallel()

	t.Run("hardcoded vector locks SHA-256(pub)[:16] derivation", func(t *testing.T) {
		t.Parallel()
		// Vector: pub = bytes 0x01, 0x02, ..., 0x20.
		// Expected KeyID = SHA-256(pub)[:16].
		var raw [stded25519.PublicKeySize]byte
		for i := range raw {
			raw[i] = byte(i + 1)
		}
		pub := stded25519.PublicKey(raw[:])
		const wantHex = "ae216c2ef5247a3782c135efa279a3e4"
		got := signed25519.KeyIDFromPub(pub)
		gotHex := hex.EncodeToString(got[:])
		if gotHex != wantHex {
			t.Fatalf("KeyID encoding drift:\n got=%s\nwant=%s", gotHex, wantHex)
		}
	})

	t.Run("different pub produces different KeyID", func(t *testing.T) {
		t.Parallel()
		a := mustGenerate(t).KeyID()
		other, err := signed25519.Generate(seeded.New(rand.Seed(8888)))
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
	msg := []byte("payload that round-trips between our seam and stdlib ed25519")

	t.Run("our Sign verifies under stdlib", func(t *testing.T) {
		t.Parallel()
		sig, _ := s.Sign(msg)
		if !stded25519.Verify(stded25519.PublicKey(s.PublicKey()), msg, sig) {
			t.Fatal("stdlib rejected our signature")
		}
	})

	t.Run("stdlib signature verifies under our Verifier", func(t *testing.T) {
		t.Parallel()
		_, priv, err := stded25519.GenerateKey(randReader{r: seeded.New(rand.Seed(13))})
		if err != nil {
			t.Fatalf("GenerateKey: %v", err)
		}
		sig := stded25519.Sign(priv, msg)
		v, err := signed25519.NewVerifier(stded25519.PublicKey(priv[stded25519.PublicKeySize:]))
		if err != nil {
			t.Fatalf("NewVerifier: %v", err)
		}
		if !v.Verify(msg, sig) {
			t.Fatal("our Verifier rejected a stdlib-produced signature")
		}
	})
}

func TestZeroAlloc(t *testing.T) {
	s := mustGenerate(t)
	msg := []byte("zero-alloc-message")
	sig, err := s.Sign(msg)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	cases := []struct {
		name string
		fn   func()
	}{
		{"KeyID", func() { _ = s.KeyID() }},
		{"PublicKey", func() { _ = s.PublicKey() }},
		{"Algorithm", func() { _ = s.Algorithm() }},
		{"Verify", func() { _ = s.Verify(msg, sig) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := testing.AllocsPerRun(100, tc.fn); got != 0 {
				t.Fatalf("%s: %v allocs/op, want 0", tc.name, got)
			}
		})
	}
}

// FuzzVerifyNeverPanics asserts Verify never panics on
// arbitrary (msg, sig) pairs against a stable public key.
func FuzzVerifyNeverPanics(f *testing.F) {
	s, err := signed25519.Generate(seeded.New(rand.Seed(1)))
	if err != nil {
		f.Fatalf("Generate: %v", err)
	}
	f.Add([]byte("msg"), make([]byte, 64))
	f.Add([]byte{}, []byte{})
	f.Add([]byte("a"), []byte{0xff})

	f.Fuzz(func(_ *testing.T, msg, sig []byte) {
		_ = s.Verify(msg, sig)
	})
}

// FuzzSignVerifyRoundTrip asserts: for any msg, Sign(msg) →
// Verify(msg, sig) returns true; and tampering returns false.
func FuzzSignVerifyRoundTrip(f *testing.F) {
	s, err := signed25519.Generate(seeded.New(rand.Seed(2)))
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
		if len(sig) > 0 {
			tampered := append([]byte(nil), sig...)
			tampered[0] ^= 0x01
			if s.Verify(msg, tampered) {
				t.Fatal("Verify accepted a tampered signature")
			}
		}
	})
}

func BenchmarkSign(b *testing.B) {
	s, err := signed25519.Generate(seeded.New(rand.Seed(1)))
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
			b.ReportAllocs()
			b.SetBytes(int64(sz.n))
			for b.Loop() {
				_, _ = s.Sign(data)
			}
		})
	}
}

func BenchmarkVerify(b *testing.B) {
	s, err := signed25519.Generate(seeded.New(rand.Seed(1)))
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

func mustGenerate(t *testing.T) *signed25519.Signer {
	t.Helper()
	s, err := signed25519.Generate(seeded.New(rand.Seed(1)))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return s
}

// randReader wraps a rand.Rand as io.Reader for stdlib paths
// that the test fixture needs (mirroring the production
// adapter, but visible at test scope so we can build stdlib-
// generated keypairs deterministically for round-trip tests).
type randReader struct{ r rand.Rand }

func (rr randReader) Read(p []byte) (int, error) { return rr.r.Read(p) }

// errRand is a rand.Rand that always errors on Read — exercises
// the entropy-source-failure path in [signed25519.Generate].
type errRand struct{}

func (errRand) Uint64() uint64 { return 0 }

func (errRand) Read(_ []byte) (int, error) {
	return 0, errors.New("entropy source failed")
}
