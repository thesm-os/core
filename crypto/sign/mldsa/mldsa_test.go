// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package mldsa_test

import (
	"bytes"
	stdmldsa "crypto/mldsa"
	"crypto/sha256"
	"io"
	"strings"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/coretest/cryptotest"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sign"
	"go.thesmos.sh/core/crypto/sign/mldsa"
	randcrypto "go.thesmos.sh/core/rand/crypto"
)

const testContext = "core/test/v1"

// paramSets are the three FIPS 204 parameter sets with the values each
// must report.
var paramSets = []struct {
	std       func() stdmldsa.Parameters
	algorithm crypto.Algorithm
	name      string
	p         mldsa.Params
}{
	{stdmldsa.MLDSA44, crypto.AlgMLDSA44, "ML-DSA-44", mldsa.MLDSA44},
	{stdmldsa.MLDSA65, crypto.AlgMLDSA65, "ML-DSA-65", mldsa.MLDSA65},
	{stdmldsa.MLDSA87, crypto.AlgMLDSA87, "ML-DSA-87", mldsa.MLDSA87},
}

// seed returns a fixed 32-byte seed: byte i is i.
func seed() []byte {
	s := make([]byte, mldsa.SeedSize)
	for i := range s {
		s[i] = byte(i)
	}

	return s
}

func mustSigner(tb testing.TB, p mldsa.Params, context string) *mldsa.Signer {
	tb.Helper()

	s, err := mldsa.New(p, seed(), context)
	testkit.NoError(tb, err, "New must accept a 32-byte seed")

	return s
}

// stdlibKeyID returns SHA-256 of the public key that crypto/mldsa
// derives from the fixed seed, truncated to [sign.KeyIDSize] bytes.
func stdlibKeyID(tb testing.TB, params stdmldsa.Parameters) sign.KeyID {
	tb.Helper()

	sk, err := stdmldsa.NewPrivateKey(params, seed())
	testkit.NoError(tb, err, "the stdlib must accept the seed")

	h := sha256.Sum256(sk.PublicKey().Bytes())

	var id sign.KeyID
	copy(id[:], h[:sign.KeyIDSize])

	return id
}

// stdlibVerify verifies with crypto/mldsa directly, under testContext.
func stdlibVerify(params stdmldsa.Parameters) func(pub, msg, sig []byte) bool {
	return func(pub, msg, sig []byte) bool {
		pk, err := stdmldsa.NewPublicKey(params, pub)
		if err != nil {
			return false
		}

		return stdmldsa.Verify(pk, msg, sig, &stdmldsa.Options{Context: testContext}) == nil
	}
}

// stdlibSign signs with crypto/mldsa directly, from the fixed seed and
// under testContext.
func stdlibSign(tb testing.TB, params stdmldsa.Parameters) func(msg []byte) []byte {
	tb.Helper()

	sk, err := stdmldsa.NewPrivateKey(params, seed())
	testkit.NoError(tb, err, "the stdlib must accept the seed")

	return func(msg []byte) []byte {
		sig, err := sk.Sign(nil, msg, &stdmldsa.Options{Context: testContext})
		testkit.NoError(tb, err, "the stdlib must sign")

		return sig
	}
}

// --- testkit-driven contract layer ---

func TestSignerContract(t *testing.T) {
	t.Parallel()

	for _, tt := range paramSets {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := mustSigner(t, tt.p, testContext)

			cryptotest.AssertSignerContract(t,
				func() sign.Signer { return s },
				append(cryptotest.SignerContractAssertions(),
					cryptotest.SignerAlgorithmAssertion(tt.algorithm),
					cryptotest.SignerKeyIDAssertion(stdlibKeyID(t, tt.std())),
					cryptotest.SignerCrossStdlibVerifyAssertion(stdlibVerify(tt.std())),
					cryptotest.SignerCrossStdlibSignAssertion(stdlibSign(t, tt.std())),
				)...,
			)
		})
	}
}

func TestVerifierContract(t *testing.T) {
	t.Parallel()

	for _, tt := range paramSets {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := mustSigner(t, tt.p, testContext)
			msg := []byte("a message to verify")
			sig, err := s.Sign(msg)
			testkit.NoError(t, err, "Sign must succeed")
			sample := cryptotest.VerifierSample{Message: msg, Signature: sig}

			cryptotest.AssertVerifierContract(t,
				func() sign.Verifier { return s.Verifier },
				append(cryptotest.VerifierContractAssertions(sample),
					cryptotest.VerifierAlgorithmAssertion(tt.algorithm),
					cryptotest.VerifierAcceptsAssertion(sample),
					cryptotest.VerifierCrossStdlibAssertion(stdlibSign(t, tt.std())),
				)...,
			)
		})
	}
}

// --- impl-specific ---

func TestParams(t *testing.T) {
	t.Parallel()

	for _, tt := range paramSets {
		t.Run(tt.name+" reports its algorithm", func(t *testing.T) {
			t.Parallel()
			testkit.Equal(t, tt.p.Algorithm(), tt.algorithm, "Algorithm must name the parameter set")
		})
	}

	t.Run("the zero value names no algorithm", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, mldsa.Params(0).Algorithm(), crypto.Algorithm(""),
			"the zero Params must not name an algorithm")
	})
}

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("rejects an unknown parameter set", func(t *testing.T) {
		t.Parallel()
		for _, p := range []mldsa.Params{0, 4, 255} {
			_, err := mldsa.New(p, seed(), testContext)
			testkit.ErrorIs(t, err, mldsa.ErrParams, "an unknown parameter set must be refused")
		}
	})

	t.Run("rejects a seed of the wrong length", func(t *testing.T) {
		t.Parallel()
		for _, n := range []int{0, 31, 33, 64} {
			_, err := mldsa.New(mldsa.MLDSA65, make([]byte, n), testContext)
			testkit.ErrorIs(t, err, mldsa.ErrSeed, "only a 32-byte seed is valid")
		}
	})

	t.Run("accepts a context of 255 bytes and refuses 256", func(t *testing.T) {
		t.Parallel()
		_, err := mldsa.New(mldsa.MLDSA44, seed(), strings.Repeat("c", 255))
		testkit.NoError(t, err, "a 255-byte context must be accepted")

		_, err = mldsa.New(mldsa.MLDSA44, seed(), strings.Repeat("c", 256))
		testkit.ErrorIs(t, err, mldsa.ErrContext, "a 256-byte context must be refused")
	})

	t.Run("round-trips the seed", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, mustSigner(t, mldsa.MLDSA87, testContext).Seed(), seed(),
			"Seed must return the seed the Signer was built from")
	})

	t.Run("copies the seed", func(t *testing.T) {
		t.Parallel()
		s := seed()
		signer, err := mldsa.New(mldsa.MLDSA44, s, testContext)
		testkit.NoError(t, err, "New must succeed")
		clear(s)
		testkit.Equal(t, signer.Seed(), seed(), "zeroing the caller's seed must not change the key")
	})
}

func TestNewVerifier(t *testing.T) {
	t.Parallel()

	t.Run("accepts the public key a Signer reports", func(t *testing.T) {
		t.Parallel()
		s := mustSigner(t, mldsa.MLDSA65, testContext)
		v, err := mldsa.NewVerifier(mldsa.MLDSA65, s.PublicKey(), testContext)
		testkit.NoError(t, err, "NewVerifier must accept the public key")
		testkit.Equal(t, v.KeyID(), s.KeyID(), "the Verifier must have the Signer's KeyID")

		sig, err := s.Sign([]byte("payload"))
		testkit.NoError(t, err, "Sign must succeed")
		testkit.True(t, v.Verify([]byte("payload"), sig), "the Verifier must accept the Signer's signature")
	})

	t.Run("copies the public key", func(t *testing.T) {
		t.Parallel()
		pub := bytes.Clone(mustSigner(t, mldsa.MLDSA44, testContext).PublicKey())
		v, err := mldsa.NewVerifier(mldsa.MLDSA44, pub, testContext)
		testkit.NoError(t, err, "NewVerifier must succeed")
		want := bytes.Clone(pub)
		clear(pub)
		testkit.Equal(t, v.PublicKey(), want, "zeroing the caller's buffer must not change the key")
	})

	t.Run("rejects an unknown parameter set", func(t *testing.T) {
		t.Parallel()
		_, err := mldsa.NewVerifier(0, make([]byte, stdmldsa.MLDSA44PublicKeySize), testContext)
		testkit.ErrorIs(t, err, mldsa.ErrParams, "an unknown parameter set must be refused")
	})

	t.Run("rejects a public key of another parameter set", func(t *testing.T) {
		t.Parallel()
		pub := mustSigner(t, mldsa.MLDSA44, testContext).PublicKey()
		_, err := mldsa.NewVerifier(mldsa.MLDSA87, pub, testContext)
		testkit.ErrorIs(t, err, mldsa.ErrPublicKey, "a 44 key must not parse as an 87 key")
	})

	t.Run("accepts a context of 255 bytes and refuses 256", func(t *testing.T) {
		t.Parallel()
		pub := mustSigner(t, mldsa.MLDSA44, testContext).PublicKey()
		_, err := mldsa.NewVerifier(mldsa.MLDSA44, pub, strings.Repeat("c", 255))
		testkit.NoError(t, err, "a 255-byte context must be accepted")

		_, err = mldsa.NewVerifier(mldsa.MLDSA44, pub, strings.Repeat("c", 256))
		testkit.ErrorIs(t, err, mldsa.ErrContext, "a 256-byte context must be refused")
	})
}

func TestResolver(t *testing.T) {
	t.Parallel()

	t.Run("builds a Verifier under the bound parameter set and context", func(t *testing.T) {
		t.Parallel()
		s := mustSigner(t, mldsa.MLDSA65, testContext)
		sig, err := s.Sign([]byte("payload"))
		testkit.NoError(t, err, "Sign must succeed")

		v, err := mldsa.Resolver(mldsa.MLDSA65, testContext)(s.PublicKey())
		testkit.NoError(t, err, "the entry must accept the public key")
		testkit.Equal(t, v.Algorithm(), crypto.AlgMLDSA65, "the Verifier must report the bound parameter set")
		testkit.True(t, v.Verify([]byte("payload"), sig), "the Verifier must accept the signature")
	})

	t.Run("returns NewVerifier's error and a nil Verifier", func(t *testing.T) {
		t.Parallel()
		v, err := mldsa.Resolver(0, testContext)(make([]byte, stdmldsa.MLDSA44PublicKeySize))
		testkit.ErrorIs(t, err, mldsa.ErrParams, "an unknown parameter set must be refused")
		testkit.True(t, v == nil, "the Verifier must be a nil interface")
	})
}

func TestContextSeparation(t *testing.T) {
	t.Parallel()

	t.Run("a signature does not verify under another context", func(t *testing.T) {
		t.Parallel()
		checkpoints := mustSigner(t, mldsa.MLDSA87, "core/checkpoint/v1")
		cosignatures := mustSigner(t, mldsa.MLDSA87, "core/cosignature/v1")
		testkit.Equal(t, checkpoints.KeyID(), cosignatures.KeyID(), "one seed gives one KeyID")

		sig, err := checkpoints.Sign([]byte("payload"))
		testkit.NoError(t, err, "Sign must succeed")
		testkit.True(t, checkpoints.Verify([]byte("payload"), sig), "the signing context must verify")
		testkit.False(t, cosignatures.Verify([]byte("payload"), sig), "another context must not verify")
	})

	t.Run("a signature does not verify under the empty context", func(t *testing.T) {
		t.Parallel()
		sig, err := mustSigner(t, mldsa.MLDSA44, testContext).Sign([]byte("payload"))
		testkit.NoError(t, err, "Sign must succeed")
		testkit.False(t, mustSigner(t, mldsa.MLDSA44, "").Verify([]byte("payload"), sig),
			"the empty context must not verify a signature made under another")
	})
}

func TestGenerate(t *testing.T) {
	t.Parallel()

	t.Run("reads the seed from the source", func(t *testing.T) {
		t.Parallel()
		s, err := mldsa.Generate(mldsa.MLDSA65, randcrypto.NewWithReader(bytes.NewReader(seed())), testContext)
		testkit.NoError(t, err, "Generate must succeed")
		testkit.Equal(t, s.Seed(), seed(), "the seed must be the bytes the source supplied")
	})

	t.Run("returns the source's failure", func(t *testing.T) {
		t.Parallel()
		failing := randcrypto.NewWithReader(&testkit.FailingReader{
			Source: bytes.NewReader(nil), Err: io.ErrUnexpectedEOF,
		})
		_, err := mldsa.Generate(mldsa.MLDSA65, failing, testContext)
		testkit.ErrorIs(t, err, io.ErrUnexpectedEOF, "the entropy failure must be returned")
	})

	t.Run("returns New's refusal", func(t *testing.T) {
		t.Parallel()
		_, err := mldsa.Generate(0, randcrypto.New(), testContext)
		testkit.ErrorIs(t, err, mldsa.ErrParams, "an unknown parameter set must be refused")
	})
}

// TestKeyIDStability pins the KeyID of the fixed seed's public key for
// each parameter set. The derivation is a persisted encoding.
func TestKeyIDStability(t *testing.T) {
	t.Parallel()

	want := map[mldsa.Params]string{
		mldsa.MLDSA44: "9f107644c1084526af3bc8098680b054",
		mldsa.MLDSA65: "d666806e11cee19a7c989f7445f90dd4",
		mldsa.MLDSA87: "91dc389cfaa01470b7f66eee45a4ae90",
	}
	for _, tt := range paramSets {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testkit.Equal(t, mustSigner(t, tt.p, testContext).KeyID().String(), want[tt.p],
				"the KeyID of the fixed seed must match its recorded value")
		})
	}
}

// TestZeroAlloc enforces the allocation contract of the Verifier.
// testing.AllocsPerRun reads a process-global malloc counter, so this
// test does not call t.Parallel.
//
//nolint:paralleltest // see comment above
func TestZeroAlloc(t *testing.T) {
	s := mustSigner(t, mldsa.MLDSA44, testContext)
	msg := []byte("payload")
	sig, err := s.Sign(msg)
	testkit.NoError(t, err, "Sign must succeed")

	tests := []struct {
		fn   func()
		name string
	}{
		{func() { _ = s.Verify(msg, sig) }, "Verify"},
		{func() { _ = s.KeyID() }, "KeyID"},
		{func() { _ = s.PublicKey() }, "PublicKey"},
		{func() { _ = s.Algorithm() }, "Algorithm"},
		{func() { _ = mldsa.KeyIDFromPub(s.PublicKey()) }, "KeyIDFromPub"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testkit.Equal(t, testing.AllocsPerRun(20, tt.fn), float64(0), tt.name+" must not allocate")
		})
	}
}

func BenchmarkSign(b *testing.B) {
	for _, tt := range paramSets {
		b.Run(tt.name, func(b *testing.B) {
			s := mustSigner(b, tt.p, testContext)
			msg := make([]byte, 64)
			b.ReportAllocs()
			for b.Loop() {
				_, _ = s.Sign(msg)
			}
		})
	}
}

func BenchmarkVerify(b *testing.B) {
	for _, tt := range paramSets {
		b.Run(tt.name, func(b *testing.B) {
			s := mustSigner(b, tt.p, testContext)
			msg := make([]byte, 64)
			sig, err := s.Sign(msg)
			testkit.NoError(b, err, "Sign must succeed")
			b.ReportAllocs()
			for b.Loop() {
				_ = s.Verify(msg, sig)
			}
		})
	}
}
