// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package note_test

import (
	"context"
	stded25519 "crypto/ed25519"
	"encoding/base64"
	"slices"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sign"
	"go.thesmos.sh/core/crypto/sign/ed25519"
	"go.thesmos.sh/core/crypto/sign/mldsa"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/note"
)

const (
	// peterPrivateKey is the base64 of type 0x01 and the Ed25519 seed of
	// the signer key of golang.org/x/mod's tests,
	// "PRIVATE+KEY+PeterNeumann+c74f20a3+AYEKFALVFGyNhPJEMzD1QIDr+Y7hfZx09iUvxdXHKDFz".
	peterPrivateKey = "AYEKFALVFGyNhPJEMzD1QIDr+Y7hfZx09iUvxdXHKDFz"

	// peterValue is the base64 of the key ID and the signature of
	// PeterNeumann over peterText, in golang.org/x/mod's tests.
	peterValue = "x08go/ZJkuBS9UG/SffcvIAQxVBtiFupLLr8pAcElZInNIuGUgYN1FFYC2pZSNXgKvqfqdngotpRZb6KE6RyyBwJnAM="

	// prefix is the content of dst before an AppendSign of the tests
	// appends to it.
	prefix = "prefix:"
)

// The goroutines of a concurrent case, and the calls that each makes.
const (
	goroutines = 8
	rounds     = 20
)

// fakeSigner is a sign.Signer that reports the algorithm and the public
// key that it is given, and returns the signature and the error that it is
// given.
type fakeSigner struct {
	err error
	alg crypto.Algorithm
	pub []byte
	sig []byte
}

// KeyID returns the zero KeyID.
func (fakeSigner) KeyID() sign.KeyID { return sign.KeyID{} }

// PublicKey returns the public key of f.
func (f fakeSigner) PublicKey() []byte { return f.pub }

// Algorithm returns the algorithm of f.
func (f fakeSigner) Algorithm() crypto.Algorithm { return f.alg }

// Verify reports false.
func (fakeSigner) Verify(_, _ []byte) bool { return false }

// Sign returns the signature and the error of f.
func (f fakeSigner) Sign([]byte) ([]byte, error) { return f.sig, f.err }

func TestText(t *testing.T) {
	t.Parallel()

	t.Run("Text", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a Verifier of the key", func(t *testing.T) {
			t.Parallel()
			k := mustParseKey(t, exampleKey)
			v, err := note.Text(ed25519.Resolve)(k)
			assert.NoError(t, err, "Text must build the Verifier")
			assertVerifier(t, v, k)
		})

		t.Run("returns a Verifier that reports true for the signature of signed-note's example", func(t *testing.T) {
			t.Parallel()
			n := mustParse(t, exampleNote)
			assert.Length(t, n.Signatures, 1, "the example must have one signature line")
			assert.True(t, textVerifier(t, exampleKey).Verify(n.Text, n.Signatures[0].Value),
				"Verify must report true for the signature of the example")
		})

		t.Run("returns a Verifier that reports false for a signature over another text", func(t *testing.T) {
			t.Parallel()
			n := mustParse(t, exampleNote)
			assert.Length(t, n.Signatures, 1, "the example must have one signature line")
			assert.False(t, textVerifier(t, exampleKey).Verify([]byte("another text\n"), n.Signatures[0].Value),
				"Verify must report false for the signature over another text")
		})

		t.Run("returns a Verifier that keeps a copy of the public key", func(t *testing.T) {
			t.Parallel()
			k := mustParseKey(t, exampleKey)
			v, err := note.Text(ed25519.Resolve)(k)
			assert.NoError(t, err, "Text must build the Verifier")
			clear(k.PublicKey)
			assert.Equal(t, v.PublicKey(), mustParseKey(t, exampleKey).PublicKey,
				"a change to the key of the caller must not change the Verifier")
		})

		t.Run("returns ErrKey for a key that is not Valid", func(t *testing.T) {
			t.Parallel()
			_, err := note.Text(ed25519.Resolve)(note.Key{Name: "a", Type: note.TypeEd25519})
			expect.ErrorIs(t, err, note.ErrKey, "Text must refuse a key without a public key")
			expect.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
		})

		t.Run("returns the error of resolve", func(t *testing.T) {
			t.Parallel()
			_, err := note.Text(ed25519.Resolve)(note.Key{Name: "a", Type: note.TypeEd25519, PublicKey: []byte{1}})
			assert.ErrorIs(t, err, ed25519.ErrInvalidPublicKeySize, "Text must return the error of resolve")
		})

		t.Run("returns ErrUnknownType when resolve returns no Verifier with no error", func(t *testing.T) {
			t.Parallel()
			none := func([]byte) (sign.Verifier, error) {
				return nil, nil //nolint:nilnil // the case is a resolve that returns neither
			}
			_, err := note.Text(none)(mustParseKey(t, exampleKey))
			expect.ErrorIs(t, err, note.ErrUnknownType, "Text must refuse a resolve without a Verifier")
			expect.Equal(t, errs.Classify(err), errs.Unsupported, "the error must classify as Unsupported")
		})
	})

	t.Run("NewTextSigner", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a Signer whose key is the key of PeterNeumann", func(t *testing.T) {
			t.Parallel()
			assertVerifier(t, peterSigner(t), mustParseKey(t, peterKey))
		})

		t.Run("returns a Signer of the signatures that golang.org/x/mod's tests record", func(t *testing.T) {
			t.Parallel()
			value, err := peterSigner(t).Sign([]byte(peterText))
			assert.NoError(t, err, "Sign must sign the text")
			assert.Equal(t, value, peterSignature(t).Value, "Ed25519 must return the signature that x/mod records")
		})

		t.Run("returns a Signer of an ML-DSA key of a type without an assigned byte", func(t *testing.T) {
			t.Parallel()
			s, err := note.NewTextSigner("example.com/pq", pqType, mldsaSigner(t))
			assert.NoError(t, err, "NewTextSigner must accept ML-DSA for a type without an assigned byte")
			value, err := s.Sign([]byte(peterText))
			assert.NoError(t, err, "Sign must sign the text")
			v, err := note.Text(mldsa.Resolver(mldsa.MLDSA44, ""))(s.Key())
			assert.NoError(t, err, "Text must build the Verifier")
			assert.True(t, v.Verify([]byte(peterText), value), "the Verifier of the key must accept the signature")
		})

		t.Run("returns a Signer of the public key of the signer without a copy", func(t *testing.T) {
			t.Parallel()
			pub := publicKey()
			s, err := note.NewTextSigner("a", pqType, fakeSigner{alg: crypto.AlgMLDSA44, pub: pub})
			assert.NoError(t, err, "NewTextSigner must accept the signer")
			expect.Equal(t, s.PublicKey(), pub, "the Signer must use the immutable key of the signer",
				expect.ByIdentity())
			expect.Equal(t, s.Key().PublicKey, pub, "the key of the Signer must use the same key", expect.ByIdentity())
		})

		tests := []struct {
			signer sign.Signer
			want   error
			name   string
			typ    note.Type
			give   note.Name
		}{
			{
				name: "returns ErrKey for a nil signer", give: "a", typ: note.TypeEd25519,
				signer: nil, want: note.ErrKey,
			},
			{
				name: "returns ErrKey for an invalid name", give: "a b", typ: note.TypeEd25519,
				signer: peterEd25519(t), want: note.ErrKey,
			},
			{
				name: "returns ErrType for an invalid type", give: "a", typ: "\xff",
				signer: peterEd25519(t), want: note.ErrType,
			},
			{
				name: "returns ErrKey for a signer without a public key", give: "a", typ: pqType,
				signer: fakeSigner{alg: crypto.AlgMLDSA44}, want: note.ErrKey,
			},
			{
				name:   "returns ErrKey for type 0x01 with a signer of another algorithm",
				give:   "a",
				typ:    note.TypeEd25519,
				signer: mldsaSigner(t),
				want:   note.ErrKey,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				s, err := note.NewTextSigner(tt.give, tt.typ, tt.signer)
				expect.ErrorIs(t, err, tt.want, "NewTextSigner must refuse the signer")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				expect.Nil(t, s, "NewTextSigner must return a nil Signer with an error")
			})
		}
	})

	t.Run("TextSigner", func(t *testing.T) {
		t.Parallel()

		t.Run("Reset", func(t *testing.T) {
			t.Parallel()

			t.Run("sets a TextSigner of the caller to the key of the signer", func(t *testing.T) {
				t.Parallel()
				var s note.TextSigner
				assert.NoError(t, s.Reset("PeterNeumann", note.TypeEd25519, peterEd25519(t)),
					"Reset must accept the signer")
				assertVerifier(t, &s, mustParseKey(t, peterKey))
			})

			t.Run("sets a TextSigner to the signatures of the signer", func(t *testing.T) {
				t.Parallel()
				var s note.TextSigner
				assert.NoError(t, s.Reset("PeterNeumann", note.TypeEd25519, peterEd25519(t)),
					"Reset must accept the signer")
				value, err := s.Sign([]byte(peterText))
				assert.NoError(t, err, "Sign must sign the text")
				assert.Equal(t, value, peterSignature(t).Value,
					"the TextSigner must return the signature that x/mod records")
			})

			t.Run("sets a TextSigner to another key", func(t *testing.T) {
				t.Parallel()
				s := peterSigner(t)
				assert.NoError(t, s.Reset("example.com/pq", pqType, mldsaSigner(t)), "Reset must accept the signer")
				value, err := s.Sign([]byte(peterText))
				assert.NoError(t, err, "Sign must sign the text")
				expect.Equal(t, s.Key().Name, note.Name("example.com/pq"), "Reset must set the name")
				expect.True(t, s.Verify([]byte(peterText), value), "the TextSigner must sign with the new signer")
			})

			t.Run("leaves the TextSigner unchanged with an error", func(t *testing.T) {
				t.Parallel()
				s := peterSigner(t)
				assert.ErrorIs(t, s.Reset("a b", note.TypeEd25519, peterEd25519(t)), note.ErrKey,
					"Reset must refuse an invalid name")
				assertVerifier(t, s, mustParseKey(t, peterKey))
			})
		})

		t.Run("Key", func(t *testing.T) {
			t.Parallel()

			t.Run("returns the zero Key for the zero TextSigner", func(t *testing.T) {
				t.Parallel()
				var zero note.TextSigner
				assert.Equal(t, zero.Key(), note.Key{}, "the zero TextSigner must have no key")
			})
		})

		t.Run("Verify", func(t *testing.T) {
			t.Parallel()

			t.Run("reports true for a signature of the key over the text", func(t *testing.T) {
				t.Parallel()
				assert.True(t, peterSigner(t).Verify([]byte(peterText), peterSignature(t).Value),
					"Verify must report true for the signature over the text")
			})

			t.Run("reports false for a signature of the key over another text", func(t *testing.T) {
				t.Parallel()
				assert.False(t, peterSigner(t).Verify([]byte(peterText+"x\n"), peterSignature(t).Value),
					"Verify must report false for the signature over another text")
			})

			t.Run("reports false for the zero TextSigner", func(t *testing.T) {
				t.Parallel()
				var zero note.TextSigner
				assert.False(t, zero.Verify([]byte(peterText), []byte{1}), "the zero TextSigner must verify nothing")
			})

			t.Run("reports true for the recorded signature to goroutines that verify at once", func(t *testing.T) {
				t.Parallel()
				s := peterSigner(t)
				value := peterSignature(t).Value
				outcomes := history.Concurrently(goroutines, 10*time.Second, func(int) (any, error) {
					verified := make([]bool, 0, rounds)
					for range rounds {
						verified = append(verified, s.Verify([]byte(peterText), value))
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

		t.Run("Sign", func(t *testing.T) {
			t.Parallel()

			t.Run("returns ErrKey for the zero TextSigner", func(t *testing.T) {
				t.Parallel()
				var zero note.TextSigner
				value, err := zero.Sign([]byte(peterText))
				expect.ErrorIs(t, err, note.ErrKey, "the zero TextSigner must sign nothing")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				expect.Nil(t, value, "Sign must return no value with an error")
			})

			t.Run("returns the recorded signature to goroutines that sign at once", func(t *testing.T) {
				t.Parallel()
				s := peterSigner(t)
				outcomes := history.Concurrently(goroutines, 10*time.Second, func(int) (any, error) {
					values := make([][]byte, 0, rounds)
					for range rounds {
						value, err := s.Sign([]byte(peterText))
						if err != nil {
							return values, err
						}
						values = append(values, value)
					}

					return values, nil
				})
				for _, o := range outcomes {
					assert.True(t, o.Finished, "every goroutine must finish")
					assert.NoError(t, o.Error, "Sign must sign on every goroutine")
					values, _ := o.Output.([][]byte)
					assert.Equal(t, values, slices.Repeat([][]byte{peterSignature(t).Value}, rounds),
						"every signature must equal the signature that x/mod records")
				}
			})
		})

		t.Run("SignContext", func(t *testing.T) {
			t.Parallel()

			t.Run("returns the signature of Sign under a context that has not ended", func(t *testing.T) {
				t.Parallel()
				value, err := peterSigner(t).SignContext(t.Context(), []byte(peterText))
				assert.NoError(t, err, "SignContext must sign under a context that has not ended")
				assert.Equal(t, value, peterSignature(t).Value, "SignContext must return the signature of Sign")
			})

			t.Run("returns the cause of a context that the caller cancelled", func(t *testing.T) {
				t.Parallel()
				s := peterSigner(t)
				assert.HonoursCancellation(t, func(ctx context.Context) error {
					_, err := s.SignContext(ctx, []byte(peterText))

					return err
				}, "SignContext must return the cause of a cancelled context")
			})

			t.Run("returns the cause of a context whose deadline passed", func(t *testing.T) {
				t.Parallel()
				s := peterSigner(t)
				assert.HonoursDeadline(t, func(ctx context.Context) error {
					_, err := s.SignContext(ctx, []byte(peterText))

					return err
				}, "SignContext must return the cause of a context past its deadline")
			})

			t.Run("returns ErrKey for the zero TextSigner", func(t *testing.T) {
				t.Parallel()
				var zero note.TextSigner
				_, err := zero.SignContext(t.Context(), []byte(peterText))
				assert.ErrorIs(t, err, note.ErrKey, "the zero TextSigner must sign nothing")
			})
		})

		t.Run("AppendSign", func(t *testing.T) {
			t.Parallel()

			t.Run("appends the signature that golang.org/x/mod's tests record", func(t *testing.T) {
				t.Parallel()
				got, err := peterSigner(t).AppendSign(t.Context(), []byte(prefix), []byte(peterText))
				assert.NoError(t, err, "AppendSign must sign the text")
				assert.Equal(t, string(got), prefix+string(peterSignature(t).Value),
					"AppendSign must append the signature to dst")
			})

			t.Run("appends the signature of a signer that is not an AppendSigner", func(t *testing.T) {
				t.Parallel()
				want := []byte("a signature")
				s, err := note.NewTextSigner("a", pqType,
					fakeSigner{alg: crypto.AlgMLDSA44, pub: publicKey(), sig: want})
				assert.NoError(t, err, "NewTextSigner must accept the signer")
				got, err := s.AppendSign(t.Context(), []byte(prefix), []byte(peterText))
				assert.NoError(t, err, "AppendSign must sign the text")
				assert.Equal(t, string(got), prefix+string(want), "AppendSign must append the signature to dst")
			})

			t.Run("returns dst unchanged with the cause of a context that ended", func(t *testing.T) {
				t.Parallel()
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				got, err := peterSigner(t).AppendSign(ctx, []byte(prefix), []byte(peterText))
				expect.ErrorIs(t, err, context.Canceled, "AppendSign must return the cause of the context")
				expect.Equal(t, string(got), prefix, "AppendSign must return dst unchanged")
			})

			t.Run("returns dst unchanged with ErrKey for the zero TextSigner", func(t *testing.T) {
				t.Parallel()
				var zero note.TextSigner
				got, err := zero.AppendSign(t.Context(), []byte(prefix), []byte(peterText))
				expect.ErrorIs(t, err, note.ErrKey, "the zero TextSigner must sign nothing")
				expect.Equal(t, string(got), prefix, "AppendSign must return dst unchanged")
			})
		})
	})
}

// TestTextAllocs checks the allocation contract of each function and
// method. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
//
//nolint:paralleltest // see above
func TestTextAllocs(t *testing.T) {
	k := mustParseKey(t, peterKey)
	ed := peterEd25519(t)
	s := peterSigner(t)
	text := []byte(peterText)
	value := peterSignature(t).Value

	t.Run("Text", func(t *testing.T) {
		entry := note.Text(ed25519.Resolve)

		var err error
		expect.MaxAllocs(t, func() { _, err = entry(k) }, 2,
			"the entry must allocate the Verifier and the Ed25519 key alone")
		assert.NoError(t, err, "the test must measure a key that the entry accepts")
	})

	t.Run("NewTextSigner", func(t *testing.T) {
		var err error
		expect.MaxAllocs(t, func() { _, err = note.NewTextSigner("PeterNeumann", note.TypeEd25519, ed) }, 1,
			"NewTextSigner must allocate the TextSigner alone")
		assert.NoError(t, err, "the test must measure a signer that NewTextSigner accepts")
	})

	t.Run("TextSigner", func(t *testing.T) {
		t.Run("Reset", func(t *testing.T) {
			var reused note.TextSigner

			var err error
			expect.MaxAllocs(t, func() { err = reused.Reset("PeterNeumann", note.TypeEd25519, ed) }, 0,
				"Reset must not allocate")
			assert.NoError(t, err, "the test must measure a signer that Reset accepts")
		})

		t.Run("Key", func(t *testing.T) {
			var got note.Key
			expect.MaxAllocs(t, func() { got = s.Key() }, 0, "Key must not allocate")
			assert.Equal(t, got, k, "the test must measure the key of PeterNeumann")
		})

		t.Run("KeyID", func(t *testing.T) {
			var got sign.KeyID
			expect.MaxAllocs(t, func() { got = s.KeyID() }, 0, "KeyID must not allocate")
			assert.Equal(t, got, note.KeyID(k.Name, k.ID()), "the test must measure the KeyID of PeterNeumann")
		})

		t.Run("PublicKey", func(t *testing.T) {
			var got []byte
			expect.MaxAllocs(t, func() { got = s.PublicKey() }, 0, "PublicKey must not allocate")
			assert.Equal(t, got, k.PublicKey, "the test must measure the public key of PeterNeumann")
		})

		t.Run("Algorithm", func(t *testing.T) {
			var got crypto.Algorithm
			expect.MaxAllocs(t, func() { got = s.Algorithm() }, 0, "Algorithm must not allocate")
			assert.Equal(t, got, note.Algorithm, "the test must measure the algorithm of every note key")
		})

		t.Run("Verify", func(t *testing.T) {
			var got bool
			expect.MaxAllocs(t, func() { got = s.Verify(text, value) }, 0, "Verify must not allocate")
			assert.True(t, got, "the test must measure a signature that Verify accepts")
		})

		t.Run("Sign", func(t *testing.T) {
			var err error
			expect.MaxAllocs(t, func() { _, err = s.Sign(text) }, 1, "Sign must allocate the signature alone")
			assert.NoError(t, err, "the test must measure a text that Sign signs")
		})

		t.Run("SignContext", func(t *testing.T) {
			ctx := t.Context()

			var err error
			expect.MaxAllocs(t, func() { _, err = s.SignContext(ctx, text) }, 1,
				"SignContext must allocate the signature alone")
			assert.NoError(t, err, "the test must measure a text that SignContext signs")
		})

		t.Run("AppendSign", func(t *testing.T) {
			ctx := t.Context()
			buf := make([]byte, 0, len(value))

			var err error
			expect.MaxAllocs(t, func() { _, err = s.AppendSign(ctx, buf[:0], text) }, 0,
				"AppendSign must not allocate into a buffer with room")
			assert.NoError(t, err, "the test must measure a text that AppendSign signs")
		})
	})
}

// BenchmarkText reports the cost of each function and method, and fails
// above the allocations that their contracts state.
func BenchmarkText(b *testing.B) {
	k := mustParseKey(b, peterKey)
	ed := peterEd25519(b)
	s := peterSigner(b)
	text := []byte(peterText)
	value := peterSignature(b).Value

	b.Run("Text", func(b *testing.B) {
		entry := note.Text(ed25519.Resolve)

		var err error

		c := bench.Start(b).MaxAllocs(2)
		defer c.End()

		for c.Loop() {
			_, err = entry(k)
		}

		assert.NoError(b, err, "the benchmark must measure a key that the entry accepts")
	})

	b.Run("NewTextSigner", func(b *testing.B) {
		var err error

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			_, err = note.NewTextSigner("PeterNeumann", note.TypeEd25519, ed)
		}

		assert.NoError(b, err, "the benchmark must measure a signer that NewTextSigner accepts")
	})

	b.Run("TextSigner", func(b *testing.B) {
		b.Run("Reset", func(b *testing.B) {
			var reused note.TextSigner

			var err error

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				err = reused.Reset("PeterNeumann", note.TypeEd25519, ed)
			}

			assert.NoError(b, err, "the benchmark must measure a signer that Reset accepts")
		})

		b.Run("Key", func(b *testing.B) {
			var got note.Key

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got = s.Key()
			}

			assert.Equal(b, got, k, "the benchmark must measure the key of PeterNeumann")
		})

		b.Run("KeyID", func(b *testing.B) {
			var got sign.KeyID

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got = s.KeyID()
			}

			assert.Equal(b, got, note.KeyID(k.Name, k.ID()), "the benchmark must measure the KeyID of PeterNeumann")
		})

		b.Run("PublicKey", func(b *testing.B) {
			var got []byte

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got = s.PublicKey()
			}

			assert.Equal(b, got, k.PublicKey, "the benchmark must measure the public key of PeterNeumann")
		})

		b.Run("Algorithm", func(b *testing.B) {
			var got crypto.Algorithm

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got = s.Algorithm()
			}

			assert.Equal(b, got, note.Algorithm, "the benchmark must measure the algorithm of every note key")
		})

		b.Run("Verify", func(b *testing.B) {
			var got bool

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got = s.Verify(text, value)
			}

			assert.True(b, got, "the benchmark must measure a signature that Verify accepts")
		})

		b.Run("Sign", func(b *testing.B) {
			var err error

			c := bench.Start(b).MaxAllocs(1)
			defer c.End()

			for c.Loop() {
				_, err = s.Sign(text)
			}

			assert.NoError(b, err, "the benchmark must measure a text that Sign signs")
		})

		b.Run("SignContext", func(b *testing.B) {
			ctx := b.Context()

			var err error

			c := bench.Start(b).MaxAllocs(1)
			defer c.End()

			for c.Loop() {
				_, err = s.SignContext(ctx, text)
			}

			assert.NoError(b, err, "the benchmark must measure a text that SignContext signs")
		})

		b.Run("AppendSign", func(b *testing.B) {
			ctx := b.Context()
			buf := make([]byte, 0, len(value))

			var err error

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				_, err = s.AppendSign(ctx, buf[:0], text)
			}

			assert.NoError(b, err, "the benchmark must measure a text that AppendSign signs")
		})
	})
}

// peterEd25519 returns the Ed25519 signer of PeterNeumann.
func peterEd25519(tb testing.TB) *ed25519.Signer {
	tb.Helper()

	raw, err := base64.StdEncoding.DecodeString(peterPrivateKey)
	assert.NoError(tb, err, "the fixture must decode")

	s, err := ed25519.New(stded25519.NewKeyFromSeed(raw[1:]))
	assert.NoError(tb, err, "the seed must give a key")

	return s
}

// peterSigner returns the note Signer of PeterNeumann.
func peterSigner(tb testing.TB) *note.TextSigner {
	tb.Helper()

	s, err := note.NewTextSigner("PeterNeumann", note.TypeEd25519, peterEd25519(tb))
	assert.NoError(tb, err, "NewTextSigner must accept the Ed25519 signer")

	return s
}

// mldsaSigner returns an ML-DSA-44 signer under the empty context, from
// the seed whose byte i is i.
func mldsaSigner(tb testing.TB) *mldsa.Signer {
	tb.Helper()

	seed := make([]byte, mldsa.SeedSize)
	for i := range seed {
		seed[i] = byte(i)
	}

	s, err := mldsa.New(mldsa.MLDSA44, seed, "")
	assert.NoError(tb, err, "New must accept the seed")

	return s
}
