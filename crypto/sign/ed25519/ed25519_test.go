// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package ed25519_test

import (
	stded25519 "crypto/ed25519"
	"encoding/hex"
	"fmt"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/coretest/cryptotest"
	"go.thesmos.sh/core/coretest/randtest"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sign"
	signed25519 "go.thesmos.sh/core/crypto/sign/ed25519"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/seeded"
)

// stdlibSign returns a function that signs msg with the stdlib under
// priv. The cross-stdlib assertions use it as the reference.
func stdlibSign(priv stded25519.PrivateKey) func([]byte) []byte {
	return func(msg []byte) []byte { return stded25519.Sign(priv, msg) }
}

// stdlibVerify reports whether sig is a valid Ed25519 signature
// over msg under pub, verified by the stdlib.
func stdlibVerify(pub, msg, sig []byte) bool {
	return stded25519.Verify(stded25519.PublicKey(pub), msg, sig)
}

// mustSigner wraps a fixture priv in a [signed25519.Signer].
// Caller supplies the fixture so the same instance is reused
// across the rest of the test (Sample / KeyID / etc.) without a
// second [cryptotest.NewEd25519Sample] call.
func mustSigner(tb testing.TB, fix cryptotest.Ed25519Fixture) *signed25519.Signer {
	tb.Helper()
	s, err := signed25519.New(fix.StdlibPriv)
	testkit.NoError(tb, err, "ed25519.New from fixture")
	return s
}

// --- testkit-driven contract layer ---

func TestEd25519VerifierContract(t *testing.T) {
	t.Parallel()
	fix := cryptotest.NewEd25519Sample()
	signer := mustSigner(t, fix)
	sample := cryptotest.VerifierSample{Message: fix.Message, Signature: fix.Signature}

	cryptotest.AssertVerifierContract(t,
		func() sign.Verifier { return signer.Verifier },
		append(cryptotest.VerifierContractAssertions(sample),
			cryptotest.VerifierAlgorithmAssertion(crypto.AlgEd25519),
			cryptotest.VerifierKeyIDAssertion(fix.KeyID),
			cryptotest.VerifierAcceptsAssertion(sample),
			cryptotest.VerifierCrossStdlibAssertion(stdlibSign(fix.StdlibPriv)),
		)...,
	)
}

func TestEd25519SignerContract(t *testing.T) {
	t.Parallel()
	fix := cryptotest.NewEd25519Sample()
	signer := mustSigner(t, fix)

	cryptotest.AssertSignerContract(t,
		func() sign.Signer { return signer },
		append(cryptotest.SignerContractAssertions(),
			cryptotest.SignerAlgorithmAssertion(crypto.AlgEd25519),
			cryptotest.SignerKeyIDAssertion(fix.KeyID),
			cryptotest.SignerCrossStdlibVerifyAssertion(stdlibVerify),
			cryptotest.SignerCrossStdlibSignAssertion(stdlibSign(fix.StdlibPriv)),
		)...,
	)
}

func BenchmarkEd25519Verifier(b *testing.B) {
	signer := mustSigner(b, cryptotest.NewEd25519Sample())
	cryptotest.BenchmarkVerifierContract(b, func() sign.Verifier { return signer.Verifier })
}

func BenchmarkEd25519Signer(b *testing.B) {
	signer := mustSigner(b, cryptotest.NewEd25519Sample())
	cryptotest.BenchmarkSignerContract(b, func() sign.Signer { return signer })
}

// --- impl-specific tests ---

// TestStreamingNotImplemented checks that Ed25519 PureEdDSA
// implements neither [sign.StreamingSigner] nor
// [sign.StreamingVerifier]. RFC 8032 §5.1.6 needs the message in two
// SHA-512 computations, and the second depends on the first, so a
// streaming API would have to buffer the message.
func TestStreamingNotImplemented(t *testing.T) {
	t.Parallel()

	t.Run("the Signer is not a StreamingSigner", func(t *testing.T) {
		t.Parallel()
		_, ok := any(mustSigner(t, cryptotest.NewEd25519Sample())).(sign.StreamingSigner)
		testkit.False(t, ok, "Ed25519 Signer must not implement sign.StreamingSigner")
	})

	t.Run("the Verifier is not a StreamingVerifier", func(t *testing.T) {
		t.Parallel()
		_, ok := any(mustSigner(t, cryptotest.NewEd25519Sample()).Verifier).(sign.StreamingVerifier)
		testkit.False(t, ok, "Ed25519 Verifier must not implement sign.StreamingVerifier")
	})
}

func TestNewVerifier(t *testing.T) {
	t.Parallel()

	t.Run("rejects wrong-size public key", func(t *testing.T) {
		t.Parallel()
		cases := [][]byte{nil, {}, make([]byte, 16), make([]byte, 31), make([]byte, 33), make([]byte, 64)}
		for _, c := range cases {
			_, err := signed25519.NewVerifier(stded25519.PublicKey(c))
			testkit.ErrorIs(t, err, signed25519.ErrInvalidPublicKeySize,
				fmt.Sprintf("len %d must be rejected as wrong-size", len(c)))
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
		testkit.NoError(t, err, "NewVerifierFromBytes")
		want := append([]byte(nil), src...)
		for i := range src {
			src[i] = 0
		}
		testkit.Equal(t, v.PublicKey(), want,
			"zeroing the caller's buffer must not change the Verifier's key")
	})

	t.Run("rejects wrong-size byte slice", func(t *testing.T) {
		t.Parallel()
		_, err := signed25519.NewVerifierFromBytes(make([]byte, 16))
		testkit.ErrorIs(t, err, signed25519.ErrInvalidPublicKeySize,
			"16-byte slice must be rejected")
	})
}

func TestResolve(t *testing.T) {
	t.Parallel()

	t.Run("returns a Verifier for the public key", func(t *testing.T) {
		t.Parallel()
		fix := cryptotest.NewEd25519Sample()
		v, err := signed25519.Resolve(mustSigner(t, fix).PublicKey())
		testkit.NoError(t, err, "Resolve must accept a 32-byte key")
		testkit.Equal(t, v.KeyID(), fix.KeyID, "the Verifier must have the key's KeyID")
		testkit.True(t, v.Verify(fix.Message, fix.Signature), "the Verifier must accept the key's signature")
	})

	t.Run("returns ErrInvalidPublicKeySize and a nil Verifier", func(t *testing.T) {
		t.Parallel()
		v, err := signed25519.Resolve(make([]byte, 31))
		testkit.ErrorIs(t, err, signed25519.ErrInvalidPublicKeySize, "a 31-byte key must be refused")
		testkit.True(t, v == nil, "the Verifier must be a nil interface")
	})
}

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("rejects wrong-size private key", func(t *testing.T) {
		t.Parallel()
		cases := [][]byte{nil, {}, make([]byte, 32), make([]byte, 63), make([]byte, 65), make([]byte, 128)}
		for _, c := range cases {
			_, err := signed25519.New(stded25519.PrivateKey(c))
			testkit.ErrorIs(t, err, signed25519.ErrInvalidPrivateKeySize,
				fmt.Sprintf("len %d must be rejected as wrong-size", len(c)))
		}
	})

	t.Run("copies the source private key", func(t *testing.T) {
		t.Parallel()
		fix := cryptotest.NewEd25519Sample()
		priv := append(stded25519.PrivateKey(nil), fix.StdlibPriv...)

		s, err := signed25519.New(priv)
		testkit.NoError(t, err, "New from fixture priv copy")
		want, err := s.Sign(fix.Message)
		testkit.NoError(t, err, "Sign")

		clear(priv)
		got, err := s.Sign(fix.Message)
		testkit.NoError(t, err, "Sign after zero")
		testkit.Equal(t, got, want,
			"zeroing the caller's key must not change the Signer's signatures")
	})
}

func TestGenerate(t *testing.T) {
	t.Parallel()

	t.Run("two seeds produce different keypairs", func(t *testing.T) {
		t.Parallel()
		a, err := signed25519.Generate(seeded.New(rand.Seed(1)))
		testkit.NoError(t, err, "Generate(seed=1)")
		b, err := signed25519.Generate(seeded.New(rand.Seed(2)))
		testkit.NoError(t, err, "Generate(seed=2)")
		testkit.NotEqual(t, a.PublicKey(), b.PublicKey(),
			"distinct seeds must produce distinct keypairs")
	})

	t.Run("propagates entropy-source failure", func(t *testing.T) {
		t.Parallel()
		failingRand := randtest.NewRandStub(t)
		failingRand.OnRead.Returns(0, testkit.TestError("entropy source failed"))
		_, err := signed25519.Generate(failingRand)
		testkit.Error(t, err, "Generate must reject a failing entropy source")
	})

	t.Run("returns the same keypair for the same seed", func(t *testing.T) {
		t.Parallel()
		a, err := signed25519.Generate(seeded.New(rand.Seed(42)))
		testkit.NoError(t, err, "Generate(seed=42) #1")
		b, err := signed25519.Generate(seeded.New(rand.Seed(42)))
		testkit.NoError(t, err, "Generate(seed=42) #2")
		testkit.Equal(t, a.PublicKey(), b.PublicKey(),
			"same seed must produce same public key")
		testkit.Equal(t, a.KeyID(), b.KeyID(),
			"same seed must produce same KeyID")
	})
}

// TestKeyIDStability pins the KeyID derivation, SHA-256(pub)[:16], for
// the public key whose bytes are 0x01 to 0x20.
func TestKeyIDStability(t *testing.T) {
	t.Parallel()

	t.Run("matches SHA-256(pub)[:16] for a fixed key", func(t *testing.T) {
		t.Parallel()
		var raw [stded25519.PublicKeySize]byte
		for i := range raw {
			raw[i] = byte(i + 1)
		}
		pub := stded25519.PublicKey(raw[:])
		const wantHex = "ae216c2ef5247a3782c135efa279a3e4"
		got := signed25519.KeyIDFromPub(pub)
		testkit.Equal(t, hex.EncodeToString(got[:]), wantHex,
			"KeyID encoding must match SHA-256(pub)[:16]")
	})
}

// TestRFC8032Vectors checks the implementation against the RFC 8032
// §7.1 known-answer vectors. Round-trip tests show internal
// consistency, and the vectors show byte-for-byte interoperability
// with every other conformant implementation.
func TestRFC8032Vectors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		secretHex string
		publicHex string
		msgHex    string
		sigHex    string
	}{
		{
			name:      "TEST 1 (empty message)",
			secretHex: "9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60",
			publicHex: "d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a",
			msgHex:    "",
			sigHex: "e5564300c360ac729086e2cc806e828a84877f1eb8e5d974d873e06522490155" +
				"5fb8821590a33bacc61e39701cf9b46bd25bf5f0595bbe24655141438e7a100b",
		},
		{
			name:      "TEST 2 (single-byte message)",
			secretHex: "4ccd089b28ff96da9db6c346ec114e0f5b8a319f35aba624da8cf6ed4fb8a6fb",
			publicHex: "3d4017c3e843895a92b70aa74d1b7ebc9c982ccf2ec4968cc0cd55f12af4660c",
			msgHex:    "72",
			sigHex: "92a009a9f0d4cab8720e820b5f642540a2b27b5416503f8fb3762223ebdb69da085ac1" +
				"e43e15996e458f3613d0f11d8c387b2eaeb4302aeeb00d291612bb0c00",
		},
		{
			name:      "TEST 3 (two-byte message)",
			secretHex: "c5aa8df43f9f837bedb7442f31dcb7b166d38535076f094b85ce3a2e0b4458f7",
			publicHex: "fc51cd8e6218a1a38da47ed00230f0580816ed13ba3303ac5deb911548908025",
			msgHex:    "af82",
			sigHex: "6291d657deec24024827e69c3abe01a30ce548a284743a445e3680d7db5ac3ac18ff9b" +
				"538d16f290ae67f760984dc6594a7c15e9716ed28dc027beceea1ec40a",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			seed := testkit.MustDecodeHex(t, tc.secretHex)
			pub := testkit.MustDecodeHex(t, tc.publicHex)
			msg := testkit.MustDecodeHex(t, tc.msgHex)
			wantSig := testkit.MustDecodeHex(t, tc.sigHex)

			priv := stded25519.NewKeyFromSeed(seed)
			s, err := signed25519.New(priv)
			testkit.NoError(t, err, "New")

			testkit.Equal(t, []byte(s.PublicKey()), pub, "derived pubkey must match RFC vector")

			gotSig, err := s.Sign(msg)
			testkit.NoError(t, err, "Sign")
			testkit.Equal(t, gotSig, wantSig, "Sign output must byte-match RFC vector")

			v, err := signed25519.NewVerifier(stded25519.PublicKey(pub))
			testkit.NoError(t, err, "NewVerifier")
			testkit.True(t, v.Verify(msg, wantSig),
				"Verify must accept the canonical RFC signature")
		})
	}
}
