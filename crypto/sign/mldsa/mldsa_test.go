// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package mldsa_test

import (
	"bytes"
	stdmldsa "crypto/mldsa"
	"crypto/sha256"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/coretest/cryptotest"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sign"
	"go.thesmos.sh/core/crypto/sign/mldsa"
	"go.thesmos.sh/core/errs"
	randcrypto "go.thesmos.sh/core/rand/crypto"
)

// testContext is the context string of the keys of the tests.
const testContext = "core/test/v1"

// The allocation ceilings. crypto/mldsa.PrivateKey.Sign returns a new
// slice, which is the only allocation of Sign and of AppendSign into a
// buffer with room. crypto/mldsa.NewPublicKey allocates the key that it
// parses, and NewVerifier allocates the Verifier and its copy of the
// encoding.
const (
	signAllocs        = 1
	newVerifierAllocs = 3
)

// The goroutines of a concurrent case, and the calls that each makes.
const (
	goroutines = 8
	rounds     = 10
)

// paramSets are the three FIPS 204 parameter sets with the values each
// must report. keyID is the KeyID of the public key of the fixed seed,
// which is a persisted encoding.
var paramSets = []struct {
	std       func() stdmldsa.Parameters
	algorithm crypto.Algorithm
	name      string
	keyID     string
	p         mldsa.Params
}{
	{stdmldsa.MLDSA44, crypto.AlgMLDSA44, "ML-DSA-44", "9f107644c1084526af3bc8098680b054", mldsa.MLDSA44},
	{stdmldsa.MLDSA65, crypto.AlgMLDSA65, "ML-DSA-65", "d666806e11cee19a7c989f7445f90dd4", mldsa.MLDSA65},
	{stdmldsa.MLDSA87, crypto.AlgMLDSA87, "ML-DSA-87", "91dc389cfaa01470b7f66eee45a4ae90", mldsa.MLDSA87},
}

// invalidParams generates the Params values outside the three parameter
// sets.
var invalidParams = prop.Integer[mldsa.Params](0, 255).Filter(func(p mldsa.Params) bool {
	return p < mldsa.MLDSA44 || p > mldsa.MLDSA87
})

// keepingSource writes 0x01 into every byte of p and keeps p, so a test
// can read what the caller left in the buffer after the call. It is not
// safe for concurrent use.
type keepingSource struct{ seen []byte }

// Read fills p, keeps it in s.seen, and reports len(p).
func (s *keepingSource) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0x01
	}
	s.seen = p

	return len(p), nil
}

// TestMLDSASignerContract runs the contract suite of sign.Signer for each
// parameter set, with the Algorithm, the KeyID, the signatures and the
// verification of the standard library, and the AppendSigner capability.
func TestMLDSASignerContract(t *testing.T) {
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
					cryptotest.AppendSignerAssertion(),
					cryptotest.AppendSignerContextAssertion(),
				)...,
			)
		})
	}
}

// TestMLDSAVerifierContract runs the contract suite of sign.Verifier for
// each parameter set, with the Algorithm and the signatures of the
// standard library.
func TestMLDSAVerifierContract(t *testing.T) {
	t.Parallel()

	for _, tt := range paramSets {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := mustSigner(t, tt.p, testContext)
			msg := []byte("a message to verify")
			sig, err := s.Sign(msg)
			assert.NoError(t, err, "Sign must succeed")
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

func TestMLDSA(t *testing.T) {
	t.Parallel()

	t.Run("Params", func(t *testing.T) {
		t.Parallel()

		t.Run("Algorithm", func(t *testing.T) {
			t.Parallel()

			for _, tt := range paramSets {
				t.Run("returns the name of "+tt.name, func(t *testing.T) {
					t.Parallel()
					assert.Equal(t, tt.p.Algorithm(), tt.algorithm, "Algorithm must name the parameter set")
				})
			}

			t.Run("returns the empty name for a value outside the parameter sets", func(t *testing.T) {
				t.Parallel()
				prop.Equal(t, mldsa.Params.Algorithm, func(mldsa.Params) crypto.Algorithm { return "" },
					"a value outside the parameter sets must name no algorithm",
					prop.Using(invalidParams), prop.Example(mldsa.Params(0)), prop.Example(mldsa.Params(4)))
			})
		})
	})

	t.Run("Verifier", func(t *testing.T) {
		t.Parallel()

		t.Run("Context", func(t *testing.T) {
			t.Parallel()

			t.Run("returns the context of NewVerifier", func(t *testing.T) {
				t.Parallel()
				pub := mustSigner(t, mldsa.MLDSA44, testContext).PublicKey()
				v, err := mldsa.NewVerifier(mldsa.MLDSA44, pub, testContext)
				assert.NoError(t, err, "NewVerifier must accept the public key")
				assert.Equal(t, v.Context(), testContext, "Context must return the context of NewVerifier")
			})
		})

		t.Run("Verify", func(t *testing.T) {
			t.Parallel()

			t.Run("reports false for a signature under another context", func(t *testing.T) {
				t.Parallel()
				checkpoints := mustSigner(t, mldsa.MLDSA87, "core/checkpoint/v1")
				cosignatures := mustSigner(t, mldsa.MLDSA87, "core/cosignature/v1")
				assert.Equal(t, checkpoints.KeyID(), cosignatures.KeyID(), "the two Signers must share the key")
				sig, err := checkpoints.Sign([]byte("payload"))
				assert.NoError(t, err, "Sign must succeed")
				assert.True(t, checkpoints.Verify([]byte("payload"), sig), "the signing context must verify")
				assert.False(t, cosignatures.Verify([]byte("payload"), sig), "another context must not verify")
			})

			t.Run("reports false under the empty context for a signature under another", func(t *testing.T) {
				t.Parallel()
				sig, err := mustSigner(t, mldsa.MLDSA44, testContext).Sign([]byte("payload"))
				assert.NoError(t, err, "Sign must succeed")
				assert.False(t, mustSigner(t, mldsa.MLDSA44, "").Verify([]byte("payload"), sig),
					"the empty context must not verify a signature made under another")
			})

			t.Run("reports true for a valid signature to goroutines that verify at once", func(t *testing.T) {
				t.Parallel()
				s := mustSigner(t, mldsa.MLDSA44, testContext)
				sig, err := s.Sign([]byte("payload"))
				assert.NoError(t, err, "Sign must succeed")
				outcomes := history.Concurrently(goroutines, 10*time.Second, func(int) (any, error) {
					verified := make([]bool, 0, rounds)
					for range rounds {
						verified = append(verified, s.Verify([]byte("payload"), sig))
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

		t.Run("returns the Verifier of the public key of a Signer", func(t *testing.T) {
			t.Parallel()
			s := mustSigner(t, mldsa.MLDSA65, testContext)
			sig, err := s.Sign([]byte("payload"))
			assert.NoError(t, err, "Sign must succeed")
			v, err := mldsa.NewVerifier(mldsa.MLDSA65, s.PublicKey(), testContext)
			assert.NoError(t, err, "NewVerifier must accept the public key")
			expect.Equal(t, v.KeyID(), s.KeyID(), "the Verifier must have the KeyID of the Signer")
			expect.True(t, v.Verify([]byte("payload"), sig), "the Verifier must accept the signature of the Signer")
		})

		t.Run("returns a Verifier that a later write to pub leaves unchanged", func(t *testing.T) {
			t.Parallel()
			want := mustSigner(t, mldsa.MLDSA44, testContext).PublicKey()
			pub := bytes.Clone(want)
			v, err := mldsa.NewVerifier(mldsa.MLDSA44, pub, testContext)
			assert.NoError(t, err, "NewVerifier must accept the public key")
			clear(pub)
			assert.Equal(t, v.PublicKey(), want,
				"zeroing the key of the caller must not change the key of the Verifier")
		})

		t.Run("returns ErrParams for a value outside the parameter sets", func(t *testing.T) {
			t.Parallel()
			pub := make([]byte, stdmldsa.MLDSA44PublicKeySize)
			prop.ErrorIs(t, func(p mldsa.Params) error {
				_, err := mldsa.NewVerifier(p, pub, testContext)

				return err
			}, mldsa.ErrParams, "a value outside the parameter sets must be refused",
				prop.Using(invalidParams), prop.Example(mldsa.Params(0)), prop.Example(mldsa.Params(4)))
		})

		t.Run("returns ErrPublicKey for a public key of another size", func(t *testing.T) {
			t.Parallel()
			size := stdmldsa.MLDSA44PublicKeySize
			prop.ErrorIs(t, func(n int) error {
				_, err := mldsa.NewVerifier(mldsa.MLDSA44, make([]byte, n), testContext)

				return err
			}, mldsa.ErrPublicKey, "a public key that is not 1,312 bytes must be refused",
				prop.Using(prop.Integer(0, 4096).Filter(func(n int) bool { return n != size })),
				prop.Example(0), prop.Example(size-1), prop.Example(size+1),
				prop.Example(stdmldsa.MLDSA65PublicKeySize), prop.Example(stdmldsa.MLDSA87PublicKeySize))
		})

		t.Run("returns ErrContext for a context longer than 255 bytes", func(t *testing.T) {
			t.Parallel()
			pub := mustSigner(t, mldsa.MLDSA44, testContext).PublicKey()
			prop.ErrorIs(t, func(n int) error {
				_, err := mldsa.NewVerifier(mldsa.MLDSA44, pub, strings.Repeat("c", n))

				return err
			}, mldsa.ErrContext, "a context longer than 255 bytes must be refused",
				prop.Using(prop.Integer(256, 1024)), prop.Example(256))
		})

		t.Run("returns a Verifier for a context of at most 255 bytes", func(t *testing.T) {
			t.Parallel()
			pub := mustSigner(t, mldsa.MLDSA44, testContext).PublicKey()
			prop.NoError(t, func(n int) error {
				_, err := mldsa.NewVerifier(mldsa.MLDSA44, pub, strings.Repeat("c", n))

				return err
			}, "a context of at most 255 bytes must be accepted",
				prop.Using(prop.Integer(0, 255)), prop.Example(255))
		})

		classes := []struct {
			name    string
			p       mldsa.Params
			pub     []byte
			context string
		}{
			{
				name:    "returns an error of class Invalid for the zero Params",
				pub:     make([]byte, stdmldsa.MLDSA44PublicKeySize),
				context: testContext,
			},
			{
				name:    "returns an error of class Invalid for a public key of another size",
				p:       mldsa.MLDSA87,
				pub:     make([]byte, stdmldsa.MLDSA44PublicKeySize),
				context: testContext,
			},
			{
				name:    "returns an error of class Invalid for a context longer than 255 bytes",
				p:       mldsa.MLDSA44,
				pub:     make([]byte, stdmldsa.MLDSA44PublicKeySize),
				context: strings.Repeat("c", 256),
			},
		}
		for _, tt := range classes {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, err := mldsa.NewVerifier(tt.p, tt.pub, tt.context)
				assert.Equal(t, errs.Classify(err), errs.Invalid, "the refusal must classify as Invalid")
			})
		}
	})

	t.Run("Resolver", func(t *testing.T) {
		t.Parallel()

		t.Run("returns an entry whose Verifier has the bound parameter set", func(t *testing.T) {
			t.Parallel()
			v, err := mldsa.Resolver(mldsa.MLDSA65, testContext)(mustSigner(t, mldsa.MLDSA65, testContext).PublicKey())
			assert.NoError(t, err, "the entry must accept the public key")
			assert.Equal(t, v.Algorithm(), crypto.AlgMLDSA65, "the Verifier must have the bound parameter set")
		})

		t.Run("returns an entry whose Verifier verifies under the bound context", func(t *testing.T) {
			t.Parallel()
			s := mustSigner(t, mldsa.MLDSA65, testContext)
			sig, err := s.Sign([]byte("payload"))
			assert.NoError(t, err, "Sign must succeed")
			v, err := mldsa.Resolver(mldsa.MLDSA65, testContext)(s.PublicKey())
			assert.NoError(t, err, "the entry must accept the public key")
			assert.True(t, v.Verify([]byte("payload"), sig),
				"the Verifier must accept a signature under the bound context")
		})

		t.Run("returns an entry that returns ErrParams for an unknown parameter set", func(t *testing.T) {
			t.Parallel()
			_, err := mldsa.Resolver(0, testContext)(make([]byte, stdmldsa.MLDSA44PublicKeySize))
			assert.ErrorIs(t, err, mldsa.ErrParams, "an unknown parameter set must be refused")
		})

		t.Run("returns an entry that returns a nil Verifier for an error", func(t *testing.T) {
			t.Parallel()
			v, err := mldsa.Resolver(0, testContext)(make([]byte, stdmldsa.MLDSA44PublicKeySize))
			assert.HasError(t, err, "the test must resolve a key that is refused")
			assert.Nil(t, v, "the Verifier must be a nil interface")
		})
	})

	t.Run("Signer", func(t *testing.T) {
		t.Parallel()

		t.Run("Seed", func(t *testing.T) {
			t.Parallel()

			t.Run("returns the seed of New", func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, mustSigner(t, mldsa.MLDSA87, testContext).Seed(), seed(),
					"Seed must return the seed of New")
			})
		})

		// FIPS 204 hedges each signature with fresh randomness, so two
		// signatures of one message differ and only the Verifier checks
		// them.
		t.Run("Sign", func(t *testing.T) {
			t.Parallel()

			t.Run("returns signatures that the Verifier accepts to goroutines that sign at once", func(t *testing.T) {
				t.Parallel()
				s := mustSigner(t, mldsa.MLDSA44, testContext)
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

		t.Run("returns a Signer under the context", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, mustSigner(t, mldsa.MLDSA87, testContext).Context(), testContext,
				"the Signer must sign under the context of New")
		})

		t.Run("returns a Signer that a later write to the seed leaves unchanged", func(t *testing.T) {
			t.Parallel()
			s := seed()
			signer, err := mldsa.New(mldsa.MLDSA44, s, testContext)
			assert.NoError(t, err, "New must accept the seed")
			clear(s)
			assert.Equal(t, signer.Seed(), seed(), "zeroing the seed of the caller must not change the key")
		})

		t.Run("returns ErrParams for a value outside the parameter sets", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(p mldsa.Params) error {
				_, err := mldsa.New(p, seed(), testContext)

				return err
			}, mldsa.ErrParams, "a value outside the parameter sets must be refused",
				prop.Using(invalidParams), prop.Example(mldsa.Params(0)), prop.Example(mldsa.Params(4)))
		})

		t.Run("returns ErrSeed for a seed of another size", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(n int) error {
				_, err := mldsa.New(mldsa.MLDSA65, make([]byte, n), testContext)

				return err
			}, mldsa.ErrSeed, "a seed that is not 32 bytes must be refused",
				prop.Using(prop.Integer(0, 128).Filter(func(n int) bool { return n != mldsa.SeedSize })),
				prop.Example(0), prop.Example(31), prop.Example(33), prop.Example(64))
		})

		t.Run("returns an error of class Invalid for a seed of another size", func(t *testing.T) {
			t.Parallel()
			_, err := mldsa.New(mldsa.MLDSA65, make([]byte, 31), testContext)
			assert.Equal(t, errs.Classify(err), errs.Invalid, "ErrSeed must classify as Invalid")
		})

		t.Run("returns ErrContext for a context longer than 255 bytes", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(n int) error {
				_, err := mldsa.New(mldsa.MLDSA44, seed(), strings.Repeat("c", n))

				return err
			}, mldsa.ErrContext, "a context longer than 255 bytes must be refused",
				prop.Using(prop.Integer(256, 1024)), prop.Example(256))
		})

		t.Run("returns a Signer for a context of at most 255 bytes", func(t *testing.T) {
			t.Parallel()
			prop.NoError(t, func(n int) error {
				_, err := mldsa.New(mldsa.MLDSA44, seed(), strings.Repeat("c", n))

				return err
			}, "a context of at most 255 bytes must be accepted",
				prop.Using(prop.Integer(0, 255)), prop.Example(255))
		})
	})

	t.Run("Generate", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the Signer of the seed that the source supplies", func(t *testing.T) {
			t.Parallel()
			s, err := mldsa.Generate(mldsa.MLDSA65, randcrypto.NewWithReader(bytes.NewReader(seed())), testContext)
			assert.NoError(t, err, "Generate must succeed")
			assert.Equal(t, s.Seed(), seed(), "the seed must be the bytes of the source")
		})

		t.Run("returns the error of the source", func(t *testing.T) {
			t.Parallel()
			errSource := errors.New("entropy source failed")
			_, err := mldsa.Generate(mldsa.MLDSA65, randcrypto.NewWithReader(iotest.ErrReader(errSource)), testContext)
			assert.ErrorIs(t, err, errSource, "Generate must return the failure of the source")
		})

		t.Run("zeroes the seed that it read from the source", func(t *testing.T) {
			t.Parallel()
			source := &keepingSource{}
			_, err := mldsa.Generate(mldsa.MLDSA65, randcrypto.NewWithReader(source), testContext)
			assert.NoError(t, err, "Generate must succeed")
			assert.Equal(t, source.seen, make([]byte, mldsa.SeedSize),
				"the seed must be zeroed before Generate returns")
		})

		t.Run("returns ErrParams for an unknown parameter set", func(t *testing.T) {
			t.Parallel()
			_, err := mldsa.Generate(0, randcrypto.New(), testContext)
			assert.ErrorIs(t, err, mldsa.ErrParams, "an unknown parameter set must be refused")
		})
	})

	// The KeyID derivation, SHA-256(pub)[:16], for the public key of the
	// fixed seed in each parameter set.
	t.Run("KeyIDFromPub", func(t *testing.T) {
		t.Parallel()

		for _, tt := range paramSets {
			t.Run("returns the recorded KeyID of the "+tt.name+" key of the seed", func(t *testing.T) {
				t.Parallel()
				id := mldsa.KeyIDFromPub(mustSigner(t, tt.p, testContext).PublicKey())
				assert.Equal(t, id.String(), tt.keyID, "the KeyID must match its recorded value")
			})
		}
	})
}

// TestMLDSAAllocs checks the allocation contracts of the Verifier, of
// Sign, AppendSign and NewVerifier, and of KeyIDFromPub, for ML-DSA-44.
// MaxAllocs counts the allocations of the whole process, so the test
// does not run in parallel.
//
//nolint:paralleltest // see above
func TestMLDSAAllocs(t *testing.T) {
	s := mustSigner(t, mldsa.MLDSA44, testContext)
	pub := s.PublicKey()
	want := stdlibKeyID(t, stdmldsa.MLDSA44())
	msg := []byte("payload")
	sig, err := s.Sign(msg)
	assert.NoError(t, err, "Sign must succeed")

	t.Run("Verifier", func(t *testing.T) {
		t.Run("Verify", func(t *testing.T) {
			var ok bool
			expect.MaxAllocs(t, func() { ok = s.Verify(msg, sig) }, 0, "Verify must not allocate")
			assert.True(t, ok, "the test must measure a signature that verifies")
		})

		t.Run("KeyID", func(t *testing.T) {
			var id sign.KeyID
			expect.MaxAllocs(t, func() { id = s.KeyID() }, 0, "KeyID must not allocate")
			assert.Equal(t, id, want, "the test must measure the KeyID of the key")
		})

		t.Run("PublicKey", func(t *testing.T) {
			var got []byte
			expect.MaxAllocs(t, func() { got = s.PublicKey() }, 0, "PublicKey must not allocate")
			assert.Length(t, got, stdmldsa.MLDSA44PublicKeySize, "the test must measure the encoding of the key")
		})

		t.Run("Algorithm", func(t *testing.T) {
			var alg crypto.Algorithm
			expect.MaxAllocs(t, func() { alg = s.Algorithm() }, 0, "Algorithm must not allocate")
			assert.Equal(t, alg, crypto.AlgMLDSA44, "the test must measure the name of the parameter set")
		})

		t.Run("Context", func(t *testing.T) {
			var got string
			expect.MaxAllocs(t, func() { got = s.Context() }, 0, "Context must not allocate")
			assert.Equal(t, got, testContext, "the test must measure the context of the key")
		})
	})

	t.Run("NewVerifier", func(t *testing.T) {
		var v *mldsa.Verifier
		expect.MaxAllocs(t, func() { v, _ = mldsa.NewVerifier(mldsa.MLDSA44, pub, testContext) }, newVerifierAllocs,
			"NewVerifier must allocate only the key, its encoding and the Verifier")
		assert.NotNil(t, v, "the test must measure a Verifier that NewVerifier accepts")
	})

	t.Run("Signer", func(t *testing.T) {
		t.Run("Sign", func(t *testing.T) {
			var got []byte
			expect.MaxAllocs(t, func() { got, _ = s.Sign(msg) }, signAllocs, "Sign must allocate only the signature")
			assert.True(t, s.Verify(msg, got), "the test must measure a signature that verifies")
		})

		t.Run("AppendSign", func(t *testing.T) {
			buf := make([]byte, 0, stdmldsa.MLDSA44SignatureSize)
			expect.MaxAllocs(t, func() { buf, _ = s.AppendSign(t.Context(), buf[:0], msg) }, signAllocs,
				"AppendSign into a buffer with room must allocate only the signature of Sign")
			assert.True(t, s.Verify(msg, buf), "the test must measure a signature that verifies")
		})
	})

	t.Run("KeyIDFromPub", func(t *testing.T) {
		var id sign.KeyID
		expect.MaxAllocs(t, func() { id = mldsa.KeyIDFromPub(pub) }, 0, "KeyIDFromPub must not allocate")
		assert.Equal(t, id, want, "the test must measure the KeyID of the key")
	})
}

// BenchmarkMLDSA reports the cost of Verify, Sign, AppendSign into a
// buffer with room and NewVerifier for each parameter set, and fails
// when one allocates more than TestMLDSAAllocs allows.
func BenchmarkMLDSA(b *testing.B) {
	msg := make([]byte, 64)

	b.Run("Verifier", func(b *testing.B) {
		b.Run("Verify", func(b *testing.B) {
			for _, tt := range paramSets {
				b.Run(tt.name, func(b *testing.B) {
					s := mustSigner(b, tt.p, testContext)
					sig, err := s.Sign(msg)
					assert.NoError(b, err, "Sign must succeed")
					var ok bool

					c := bench.Start(b).MaxAllocs(0)
					defer c.End()

					for c.Loop() {
						ok = s.Verify(msg, sig)
					}

					assert.True(b, ok, "the benchmark must measure a signature that verifies")
				})
			}
		})
	})

	b.Run("NewVerifier", func(b *testing.B) {
		for _, tt := range paramSets {
			b.Run(tt.name, func(b *testing.B) {
				pub := mustSigner(b, tt.p, testContext).PublicKey()
				var v *mldsa.Verifier

				c := bench.Start(b).MaxAllocs(newVerifierAllocs)
				defer c.End()

				for c.Loop() {
					v, _ = mldsa.NewVerifier(tt.p, pub, testContext)
				}

				assert.NotNil(b, v, "the benchmark must measure a Verifier that NewVerifier accepts")
			})
		}
	})

	b.Run("Signer", func(b *testing.B) {
		b.Run("Sign", func(b *testing.B) {
			for _, tt := range paramSets {
				b.Run(tt.name, func(b *testing.B) {
					s := mustSigner(b, tt.p, testContext)
					var sig []byte

					c := bench.Start(b).MaxAllocs(signAllocs)
					defer c.End()

					for c.Loop() {
						sig, _ = s.Sign(msg)
					}

					assert.True(b, s.Verify(msg, sig), "the benchmark must measure a signature that verifies")
				})
			}
		})

		b.Run("AppendSign", func(b *testing.B) {
			for _, tt := range paramSets {
				b.Run(tt.name, func(b *testing.B) {
					s := mustSigner(b, tt.p, testContext)
					buf := make([]byte, 0, tt.std().SignatureSize())

					c := bench.Start(b).MaxAllocs(signAllocs)
					defer c.End()

					for c.Loop() {
						buf, _ = s.AppendSign(b.Context(), buf[:0], msg)
					}

					assert.True(b, s.Verify(msg, buf), "the benchmark must measure a signature that verifies")
				})
			}
		})
	})
}

// seed returns a fixed 32-byte seed whose byte i is i. Each call returns
// a new slice, so a test may zero it.
func seed() []byte {
	s := make([]byte, mldsa.SeedSize)
	for i := range s {
		s[i] = byte(i)
	}

	return s
}

// mustSigner returns the Signer of the fixed seed in parameter set p
// under context. It fails tb when New refuses them.
func mustSigner(tb testing.TB, p mldsa.Params, context string) *mldsa.Signer {
	tb.Helper()

	s, err := mldsa.New(p, seed(), context)
	assert.NoError(tb, err, "New must accept a 32-byte seed")

	return s
}

// stdlibKeyID returns SHA-256 of the public key that crypto/mldsa
// derives from the fixed seed, truncated to sign.KeyIDSize bytes. It
// fails tb when crypto/mldsa refuses the seed.
func stdlibKeyID(tb testing.TB, params stdmldsa.Parameters) sign.KeyID {
	tb.Helper()

	sk, err := stdmldsa.NewPrivateKey(params, seed())
	assert.NoError(tb, err, "crypto/mldsa must accept the seed")

	h := sha256.Sum256(sk.PublicKey().Bytes())

	var id sign.KeyID
	copy(id[:], h[:sign.KeyIDSize])

	return id
}

// stdlibVerify returns a function that verifies with crypto/mldsa
// directly, under testContext.
func stdlibVerify(params stdmldsa.Parameters) func(pub, msg, sig []byte) bool {
	return func(pub, msg, sig []byte) bool {
		pk, err := stdmldsa.NewPublicKey(params, pub)
		if err != nil {
			return false
		}

		return stdmldsa.Verify(pk, msg, sig, &stdmldsa.Options{Context: testContext}) == nil
	}
}

// stdlibSign returns a function that signs with crypto/mldsa directly,
// with the key of the fixed seed and under testContext. A failure of
// crypto/mldsa fails tb.
func stdlibSign(tb testing.TB, params stdmldsa.Parameters) func(msg []byte) []byte {
	tb.Helper()

	sk, err := stdmldsa.NewPrivateKey(params, seed())
	assert.NoError(tb, err, "crypto/mldsa must accept the seed")

	return func(msg []byte) []byte {
		sig, err := sk.Sign(nil, msg, &stdmldsa.Options{Context: testContext})
		assert.NoError(tb, err, "crypto/mldsa must sign")

		return sig
	}
}
