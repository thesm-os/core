// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package ed25519_test

import (
	"bytes"
	stded25519 "crypto/ed25519"
	"encoding/hex"
	"errors"
	"slices"
	"strconv"
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
	signed25519 "go.thesmos.sh/core/crypto/sign/ed25519"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/rand"
	randcrypto "go.thesmos.sh/core/rand/crypto"
	"go.thesmos.sh/core/rand/seeded"
)

// The goroutines of a concurrent case, and the calls that each makes.
const (
	goroutines = 8
	rounds     = 20
)

// rfc8032Vectors are the known-answer vectors of RFC 8032 section 7.1:
// a seed, its public key, a message and its signature, in hexadecimal.
// They show byte-for-byte interoperability with every other conformant
// implementation.
var rfc8032Vectors = []struct {
	name   string
	seed   string
	public string
	msg    string
	sig    string
}{
	{
		name:   "TEST 1",
		seed:   "9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60",
		public: "d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a",
		msg:    "",
		sig: "e5564300c360ac729086e2cc806e828a84877f1eb8e5d974d873e06522490155" +
			"5fb8821590a33bacc61e39701cf9b46bd25bf5f0595bbe24655141438e7a100b",
	},
	{
		name:   "TEST 2",
		seed:   "4ccd089b28ff96da9db6c346ec114e0f5b8a319f35aba624da8cf6ed4fb8a6fb",
		public: "3d4017c3e843895a92b70aa74d1b7ebc9c982ccf2ec4968cc0cd55f12af4660c",
		msg:    "72",
		sig: "92a009a9f0d4cab8720e820b5f642540a2b27b5416503f8fb3762223ebdb69da085ac1" +
			"e43e15996e458f3613d0f11d8c387b2eaeb4302aeeb00d291612bb0c00",
	},
	{
		name:   "TEST 3",
		seed:   "c5aa8df43f9f837bedb7442f31dcb7b166d38535076f094b85ce3a2e0b4458f7",
		public: "fc51cd8e6218a1a38da47ed00230f0580816ed13ba3303ac5deb911548908025",
		msg:    "af82",
		sig: "6291d657deec24024827e69c3abe01a30ce548a284743a445e3680d7db5ac3ac18ff9b" +
			"538d16f290ae67f760984dc6594a7c15e9716ed28dc027beceea1ec40a",
	},
}

// TestEd25519VerifierContract runs the contract suite of sign.Verifier
// with the Algorithm, the KeyID and the signatures of the standard
// library.
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
			cryptotest.VerifierCrossStdlibAssertion(func(msg []byte) []byte {
				return stded25519.Sign(fix.StdlibPriv, msg)
			}),
		)...,
	)
}

// TestEd25519SignerContract runs the contract suite of sign.Signer with
// the Algorithm, the KeyID, the signatures and the verification of the
// standard library, and the AppendSigner capability.
func TestEd25519SignerContract(t *testing.T) {
	t.Parallel()
	fix := cryptotest.NewEd25519Sample()
	signer := mustSigner(t, fix)

	cryptotest.AssertSignerContract(t,
		func() sign.Signer { return signer },
		append(cryptotest.SignerContractAssertions(),
			cryptotest.SignerAlgorithmAssertion(crypto.AlgEd25519),
			cryptotest.SignerKeyIDAssertion(fix.KeyID),
			cryptotest.SignerCrossStdlibVerifyAssertion(func(pub, msg, sig []byte) bool {
				return stded25519.Verify(stded25519.PublicKey(pub), msg, sig)
			}),
			cryptotest.SignerCrossStdlibSignAssertion(func(msg []byte) []byte {
				return stded25519.Sign(fix.StdlibPriv, msg)
			}),
			cryptotest.AppendSignerAssertion(),
			cryptotest.AppendSignerContextAssertion(),
		)...,
	)
}

func TestEd25519(t *testing.T) {
	t.Parallel()

	t.Run("Verifier", func(t *testing.T) {
		t.Parallel()

		// RFC 8032 section 5.1.6 needs the message in two SHA-512
		// computations, and the second depends on the first, so a
		// streaming API would have to buffer the message.
		t.Run("implements no StreamingVerifier", func(t *testing.T) {
			t.Parallel()
			_, ok := any(mustSigner(t, cryptotest.NewEd25519Sample()).Verifier).(sign.StreamingVerifier)
			assert.False(t, ok, "an Ed25519 Verifier must not implement sign.StreamingVerifier")
		})

		t.Run("Verify", func(t *testing.T) {
			t.Parallel()

			for _, tt := range rfc8032Vectors {
				t.Run("reports true for the signature of RFC 8032 "+tt.name, func(t *testing.T) {
					t.Parallel()
					v, err := signed25519.NewVerifierFromBytes(decodeHex(t, tt.public))
					assert.NoError(t, err, "NewVerifierFromBytes must accept the public key")
					assert.True(t, v.Verify(decodeHex(t, tt.msg), decodeHex(t, tt.sig)),
						"Verify must accept the signature of the vector")
				})
			}

			t.Run("reports true for a valid signature to goroutines that verify at once", func(t *testing.T) {
				t.Parallel()
				fix := cryptotest.NewEd25519Sample()
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

		t.Run("returns a Verifier that a later write to pub leaves unchanged", func(t *testing.T) {
			t.Parallel()
			pub := stded25519.PublicKey(bytes.Repeat([]byte{7}, stded25519.PublicKeySize))
			v, err := signed25519.NewVerifier(pub)
			assert.NoError(t, err, "NewVerifier must accept a 32-byte key")
			clear(pub)
			assert.Equal(t, v.PublicKey(), bytes.Repeat([]byte{7}, stded25519.PublicKeySize),
				"zeroing the key of the caller must not change the key of the Verifier")
		})

		t.Run("returns ErrInvalidPublicKeySize for a public key of another size", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(n int) error {
				_, err := signed25519.NewVerifier(make(stded25519.PublicKey, n))

				return err
			}, signed25519.ErrInvalidPublicKeySize, "a public key that is not 32 bytes must be refused",
				prop.Using(prop.Integer(0, 128).Filter(func(n int) bool { return n != stded25519.PublicKeySize })),
				prop.Example(0), prop.Example(31), prop.Example(33), prop.Example(64))
		})
	})

	t.Run("NewVerifierFromBytes", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a Verifier that a later write to the bytes leaves unchanged", func(t *testing.T) {
			t.Parallel()
			src := make([]byte, stded25519.PublicKeySize)
			for i := range src {
				src[i] = byte(i + 1)
			}
			want := bytes.Clone(src)
			v, err := signed25519.NewVerifierFromBytes(src)
			assert.NoError(t, err, "NewVerifierFromBytes must accept a 32-byte key")
			clear(src)
			assert.Equal(t, v.PublicKey(), want,
				"zeroing the buffer of the caller must not change the key of the Verifier")
		})

		t.Run("returns ErrInvalidPublicKeySize for a slice of 16 bytes", func(t *testing.T) {
			t.Parallel()
			_, err := signed25519.NewVerifierFromBytes(make([]byte, 16))
			assert.ErrorIs(t, err, signed25519.ErrInvalidPublicKeySize, "a 16-byte slice must be refused")
		})
	})

	t.Run("Resolve", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a Verifier for the public key", func(t *testing.T) {
			t.Parallel()
			fix := cryptotest.NewEd25519Sample()
			v, err := signed25519.Resolve(mustSigner(t, fix).PublicKey())
			assert.NoError(t, err, "Resolve must accept a 32-byte key")
			expect.Equal(t, v.KeyID(), fix.KeyID, "the Verifier must have the KeyID of the key")
			expect.True(t, v.Verify(fix.Message, fix.Signature), "the Verifier must accept the signature of the key")
		})

		t.Run("returns ErrInvalidPublicKeySize for a key of 31 bytes", func(t *testing.T) {
			t.Parallel()
			_, err := signed25519.Resolve(make([]byte, 31))
			assert.ErrorIs(t, err, signed25519.ErrInvalidPublicKeySize, "a 31-byte key must be refused")
		})

		t.Run("returns an error of class Invalid for a key of 31 bytes", func(t *testing.T) {
			t.Parallel()
			_, err := signed25519.Resolve(make([]byte, 31))
			assert.Equal(t, errs.Classify(err), errs.Invalid, "ErrInvalidPublicKeySize must classify as Invalid")
		})

		t.Run("returns a nil Verifier for a key of 31 bytes", func(t *testing.T) {
			t.Parallel()
			v, err := signed25519.Resolve(make([]byte, 31))
			assert.HasError(t, err, "the test must resolve a key that is refused")
			assert.Nil(t, v, "the Verifier must be a nil interface")
		})
	})

	t.Run("Signer", func(t *testing.T) {
		t.Parallel()

		t.Run("implements no StreamingSigner", func(t *testing.T) {
			t.Parallel()
			_, ok := any(mustSigner(t, cryptotest.NewEd25519Sample())).(sign.StreamingSigner)
			assert.False(t, ok, "an Ed25519 Signer must not implement sign.StreamingSigner")
		})

		t.Run("Sign", func(t *testing.T) {
			t.Parallel()

			for _, tt := range rfc8032Vectors {
				t.Run("returns the signature of RFC 8032 "+tt.name, func(t *testing.T) {
					t.Parallel()
					s, err := signed25519.New(stded25519.NewKeyFromSeed(decodeHex(t, tt.seed)))
					assert.NoError(t, err, "New must accept the key of the seed")
					got, err := s.Sign(decodeHex(t, tt.msg))
					assert.NoError(t, err, "Sign must succeed")
					assert.Equal(t, hex.EncodeToString(got), tt.sig, "Sign must match the signature of the vector")
				})
			}

			t.Run("returns the signature of one call to goroutines that sign at once", func(t *testing.T) {
				t.Parallel()
				s := mustSigner(t, cryptotest.NewEd25519Sample())
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
					want, err := s.Sign([]byte("message " + strconv.Itoa(o.Client)))
					assert.NoError(t, err, "Sign must sign the message")
					assert.Equal(t, sigs, slices.Repeat([][]byte{want}, rounds),
						"every signature must equal the signature of one call")
				}
			})
		})
	})

	t.Run("New", func(t *testing.T) {
		t.Parallel()

		for _, tt := range rfc8032Vectors {
			t.Run("returns a Signer of the public key of RFC 8032 "+tt.name, func(t *testing.T) {
				t.Parallel()
				s, err := signed25519.New(stded25519.NewKeyFromSeed(decodeHex(t, tt.seed)))
				assert.NoError(t, err, "New must accept the key of the seed")
				assert.Equal(t, hex.EncodeToString(s.PublicKey()), tt.public,
					"the public key must match the public key of the vector")
			})
		}

		t.Run("returns ErrInvalidPrivateKeySize for a private key of another size", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(n int) error {
				_, err := signed25519.New(make(stded25519.PrivateKey, n))

				return err
			}, signed25519.ErrInvalidPrivateKeySize, "a private key that is not 64 bytes must be refused",
				prop.Using(prop.Integer(0, 256).Filter(func(n int) bool { return n != stded25519.PrivateKeySize })),
				prop.Example(0), prop.Example(32), prop.Example(63), prop.Example(65), prop.Example(128))
		})

		t.Run("returns an error of class Invalid for a private key of another size", func(t *testing.T) {
			t.Parallel()
			_, err := signed25519.New(make(stded25519.PrivateKey, 32))
			assert.Equal(t, errs.Classify(err), errs.Invalid, "ErrInvalidPrivateKeySize must classify as Invalid")
		})

		t.Run("returns a Signer that a later write to the private key leaves unchanged", func(t *testing.T) {
			t.Parallel()
			fix := cryptotest.NewEd25519Sample()
			priv := bytes.Clone(fix.StdlibPriv)
			s, err := signed25519.New(priv)
			assert.NoError(t, err, "New must accept the private key of the fixture")
			assert.Pure(t, func() []byte {
				sig, err := s.Sign(fix.Message)
				assert.NoError(t, err, "Sign must succeed")

				return sig
			}, func() { clear(priv) }, "zeroing the key of the caller must not change the signatures of the Signer")
		})
	})

	t.Run("Generate", func(t *testing.T) {
		t.Parallel()

		t.Run("returns another key pair for another seed", func(t *testing.T) {
			t.Parallel()
			a, err := signed25519.Generate(seeded.New(rand.Seed(1)))
			assert.NoError(t, err, "Generate must succeed")
			b, err := signed25519.Generate(seeded.New(rand.Seed(2)))
			assert.NoError(t, err, "Generate must succeed")
			assert.NotEqual(t, a.PublicKey(), b.PublicKey(), "distinct seeds must produce distinct key pairs")
		})

		t.Run("returns the same key pair for the same seed", func(t *testing.T) {
			t.Parallel()
			assert.Deterministic(t, func(s rand.Seed) ([]byte, error) {
				k, err := signed25519.Generate(seeded.New(s))
				if err != nil {
					return nil, err
				}

				return k.PublicKey(), nil
			}, rand.Seed(42), "one seed must produce one key pair")
		})

		t.Run("returns the error of the source", func(t *testing.T) {
			t.Parallel()
			errSource := errors.New("entropy source failed")
			_, err := signed25519.Generate(randcrypto.NewWithReader(iotest.ErrReader(errSource)))
			assert.ErrorIs(t, err, errSource, "Generate must return the failure of the source")
		})
	})

	// The KeyID derivation, SHA-256(pub)[:16], for the public key whose
	// bytes are 0x01 to 0x20.
	t.Run("KeyIDFromPub", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the first 16 bytes of the SHA-256 of the key", func(t *testing.T) {
			t.Parallel()
			var raw [stded25519.PublicKeySize]byte
			for i := range raw {
				raw[i] = byte(i + 1)
			}
			got := signed25519.KeyIDFromPub(raw[:])
			assert.Equal(t, hex.EncodeToString(got[:]), "ae216c2ef5247a3782c135efa279a3e4",
				"the KeyID must match SHA-256(pub)[:16]")
		})
	})
}

// TestEd25519Allocs checks the allocation contracts of Verify,
// KeyIDFromPub, Sign, AppendSign and Resolve. MaxAllocs counts the
// allocations of the whole process, so the test does not run in
// parallel.
//
//nolint:paralleltest // see above
func TestEd25519Allocs(t *testing.T) {
	fix := cryptotest.NewEd25519Sample()
	s := mustSigner(t, fix)
	msg := []byte("payload")
	buf := make([]byte, 0, stded25519.SignatureSize)
	pub := s.PublicKey()

	t.Run("Verifier", func(t *testing.T) {
		t.Run("Verify", func(t *testing.T) {
			var ok bool
			expect.MaxAllocs(t, func() { ok = s.Verify(fix.Message, fix.Signature) }, 0, "Verify must not allocate")
			assert.True(t, ok, "the test must measure a signature that verifies")
		})
	})

	t.Run("Resolve", func(t *testing.T) {
		var v sign.Verifier
		expect.MaxAllocs(t, func() { v, _ = signed25519.Resolve(pub) }, 1, "Resolve must allocate only the Verifier")
		assert.NotNil(t, v, "the test must measure a resolution that succeeds")
	})

	t.Run("Signer", func(t *testing.T) {
		t.Run("Sign", func(t *testing.T) {
			var sig []byte
			expect.MaxAllocs(t, func() { sig, _ = s.Sign(msg) }, 1, "Sign must allocate only the signature")
			assert.True(t, s.Verify(msg, sig), "the test must measure a signature that verifies")
		})

		t.Run("AppendSign", func(t *testing.T) {
			expect.MaxAllocs(t, func() { buf, _ = s.AppendSign(t.Context(), buf[:0], msg) }, 0,
				"AppendSign into a buffer with room must not allocate")
			assert.True(t, s.Verify(msg, buf), "the test must measure a signature that verifies")
		})
	})

	t.Run("KeyIDFromPub", func(t *testing.T) {
		var id sign.KeyID
		expect.MaxAllocs(t, func() { id = signed25519.KeyIDFromPub(pub) }, 0, "KeyIDFromPub must not allocate")
		assert.Equal(t, id, fix.KeyID, "the test must measure the KeyID of the key")
	})
}

// BenchmarkEd25519Verifier runs the benchmarks of the contract suite of
// sign.Verifier.
func BenchmarkEd25519Verifier(b *testing.B) {
	signer := mustSigner(b, cryptotest.NewEd25519Sample())
	cryptotest.BenchmarkVerifierContract(b, func() sign.Verifier { return signer.Verifier })
}

// BenchmarkEd25519Signer runs the benchmarks of the contract suite of
// sign.Signer.
func BenchmarkEd25519Signer(b *testing.B) {
	signer := mustSigner(b, cryptotest.NewEd25519Sample())
	cryptotest.BenchmarkSignerContract(b, func() sign.Signer { return signer })
}

// BenchmarkEd25519 reports the cost of AppendSign into a buffer with room
// and of Resolve, and fails when either allocates more than
// TestEd25519Allocs allows.
func BenchmarkEd25519(b *testing.B) {
	s := mustSigner(b, cryptotest.NewEd25519Sample())

	b.Run("Signer", func(b *testing.B) {
		b.Run("AppendSign", func(b *testing.B) {
			msg := []byte("payload")
			buf := make([]byte, 0, stded25519.SignatureSize)

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				buf, _ = s.AppendSign(b.Context(), buf[:0], msg)
			}

			assert.True(b, s.Verify(msg, buf), "the benchmark must measure a signature that verifies")
		})
	})

	b.Run("Resolve", func(b *testing.B) {
		pub := s.PublicKey()
		var v sign.Verifier

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			v, _ = signed25519.Resolve(pub)
		}

		assert.NotNil(b, v, "the benchmark must measure a resolution that succeeds")
	})
}

// mustSigner wraps the private key of fix in a Signer. It fails tb when
// New refuses the key.
func mustSigner(tb testing.TB, fix cryptotest.Ed25519Fixture) *signed25519.Signer {
	tb.Helper()

	s, err := signed25519.New(fix.StdlibPriv)
	assert.NoError(tb, err, "New must accept the private key of the fixture")

	return s
}

// decodeHex returns the bytes of the hexadecimal s. It fails tb when s
// is not hexadecimal.
func decodeHex(tb testing.TB, s string) []byte {
	tb.Helper()

	b, err := hex.DecodeString(s)
	assert.NoError(tb, err, "a field of the vector must be hexadecimal")

	return b
}
