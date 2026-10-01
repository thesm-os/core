// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package note_test

import (
	"context"
	stded25519 "crypto/ed25519"
	"encoding/base64"
	"testing"

	"go.thesmos.sh/testkit"

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

// peterEd25519 returns the Ed25519 signer of PeterNeumann.
func peterEd25519(tb testing.TB) *ed25519.Signer {
	tb.Helper()

	raw, err := base64.StdEncoding.DecodeString(peterPrivateKey)
	testkit.NoError(tb, err, "the fixture must decode")

	s, err := ed25519.New(stded25519.NewKeyFromSeed(raw[1:]))
	testkit.NoError(tb, err, "the seed must give a key")

	return s
}

// peterSigner returns the note Signer of PeterNeumann.
func peterSigner(tb testing.TB) note.Signer {
	tb.Helper()

	s, err := note.NewTextSigner("PeterNeumann", note.TypeEd25519, peterEd25519(tb))
	testkit.NoError(tb, err, "NewTextSigner must accept the Ed25519 signer")

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
	testkit.NoError(tb, err, "New must accept the seed")

	return s
}

// fakeSigner is a sign.Signer that reports the algorithm and the public
// key it holds, and returns the signature and the error it holds.
type fakeSigner struct {
	err error
	alg crypto.Algorithm
	pub []byte
	sig []byte
}

// KeyID returns the zero KeyID.
func (fakeSigner) KeyID() sign.KeyID { return sign.KeyID{} }

// PublicKey returns the public key that f holds.
func (f fakeSigner) PublicKey() []byte { return f.pub }

// Algorithm returns the algorithm that f holds.
func (f fakeSigner) Algorithm() crypto.Algorithm { return f.alg }

// Verify reports false.
func (fakeSigner) Verify(_, _ []byte) bool { return false }

// Sign returns the signature and the error that f holds.
func (f fakeSigner) Sign([]byte) ([]byte, error) { return f.sig, f.err }

func TestText(t *testing.T) {
	t.Parallel()

	t.Run("Text", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a Verifier of the key", func(t *testing.T) {
			t.Parallel()
			k := mustParseKey(t, exampleKey)
			v, err := note.Text(ed25519.Resolve)(k)
			testkit.NoError(t, err, "Text must build the Verifier")
			assertVerifier(t, v, k)
		})

		t.Run("returns a Verifier that accepts the signature of signed-note's example", func(t *testing.T) {
			t.Parallel()
			v, err := note.Text(ed25519.Resolve)(mustParseKey(t, exampleKey))
			testkit.NoError(t, err, "Text must build the Verifier")
			n, err := note.Parse([]byte(exampleNote))
			testkit.NoError(t, err, "Parse must accept the example")
			testkit.True(t, v.Verify(n.Text, n.Signatures[0].Value), "Verify must accept the example's signature")
		})

		t.Run("returns a Verifier that refuses a signature over another text", func(t *testing.T) {
			t.Parallel()
			v, err := note.Text(ed25519.Resolve)(mustParseKey(t, exampleKey))
			testkit.NoError(t, err, "Text must build the Verifier")
			n, err := note.Parse([]byte(exampleNote))
			testkit.NoError(t, err, "Parse must accept the example")
			testkit.False(t, v.Verify([]byte("another text\n"), n.Signatures[0].Value),
				"Verify must refuse the signature over another text")
		})

		t.Run("keeps a copy of the public key", func(t *testing.T) {
			t.Parallel()
			k := mustParseKey(t, exampleKey)
			v, err := note.Text(ed25519.Resolve)(k)
			testkit.NoError(t, err, "Text must build the Verifier")
			want := mustParseKey(t, exampleKey).PublicKey
			clear(k.PublicKey)
			testkit.Equal(t, v.PublicKey(), want, "a change to the caller's key must not change the Verifier")
		})

		t.Run("returns ErrKey for a key that is not Valid", func(t *testing.T) {
			t.Parallel()
			_, err := note.Text(ed25519.Resolve)(note.Key{Name: "a", Type: note.TypeEd25519})
			testkit.ErrorIs(t, err, note.ErrKey, "Text must refuse a key without a public key")
			testkit.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
		})

		t.Run("returns the error of resolve", func(t *testing.T) {
			t.Parallel()
			_, err := note.Text(ed25519.Resolve)(note.Key{Name: "a", Type: note.TypeEd25519, PublicKey: []byte{1}})
			testkit.ErrorIs(t, err, ed25519.ErrInvalidPublicKeySize, "Text must return the error of resolve")
		})

		t.Run("returns ErrUnknownType when resolve returns neither a Verifier nor an error", func(t *testing.T) {
			t.Parallel()
			none := func([]byte) (sign.Verifier, error) {
				return nil, nil //nolint:nilnil // the case is a resolve that returns neither
			}
			_, err := note.Text(none)(mustParseKey(t, exampleKey))
			testkit.ErrorIs(t, err, note.ErrUnknownType, "Text must refuse a resolve without a Verifier")
			testkit.Equal(t, errs.Classify(err), errs.Unsupported, "the error must classify as Unsupported")
		})
	})

	t.Run("NewTextSigner", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a Signer of the key of its name, type and public key", func(t *testing.T) {
			t.Parallel()
			assertVerifier(t, peterSigner(t), mustParseKey(t, peterKey))
		})

		t.Run("returns a Signer whose signatures golang.org/x/mod's tests record", func(t *testing.T) {
			t.Parallel()
			value, err := peterSigner(t).Sign([]byte(peterText))
			testkit.NoError(t, err, "Sign must succeed")
			want, err := base64.StdEncoding.DecodeString(peterValue)
			testkit.NoError(t, err, "the fixture must decode")
			testkit.Equal(t, value, want[4:], "Ed25519 must give the signature that x/mod records")
		})

		t.Run("returns a Signer of an ML-DSA key of a type without an assigned byte", func(t *testing.T) {
			t.Parallel()
			s, err := note.NewTextSigner("example.com/pq", pqType, mldsaSigner(t))
			testkit.NoError(t, err, "NewTextSigner must accept ML-DSA for a type without an assigned byte")
			value, err := s.Sign([]byte(peterText))
			testkit.NoError(t, err, "Sign must succeed")
			v, err := note.Text(mldsa.Resolver(mldsa.MLDSA44, ""))(s.Key())
			testkit.NoError(t, err, "Text must build the Verifier")
			testkit.True(t, v.Verify([]byte(peterText), value), "the Verifier of the key must accept the signature")
		})

		t.Run("returns a Signer that uses the public key of the signer without a copy", func(t *testing.T) {
			t.Parallel()
			pub := publicKey()
			s, err := note.NewTextSigner("a", pqType, fakeSigner{alg: crypto.AlgMLDSA44, pub: pub})
			testkit.NoError(t, err, "NewTextSigner must accept the signer")
			testkit.True(t, &s.PublicKey()[0] == &pub[0], "the Signer must use the immutable key of the signer")
			testkit.True(t, &s.Key().PublicKey[0] == &pub[0], "the key of the Signer must use it too")
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
				name:   "returns ErrKey for type 0x01 and a signer of another algorithm",
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
				testkit.ErrorIs(t, err, tt.want, "NewTextSigner must refuse the signer")
				testkit.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				testkit.True(t, s == nil, "NewTextSigner must return a nil Signer with an error")
			})
		}
	})

	t.Run("TextSigner.Reset", func(t *testing.T) {
		t.Parallel()

		t.Run("sets a TextSigner of the caller to the key of the signer", func(t *testing.T) {
			t.Parallel()
			var s note.TextSigner
			testkit.NoError(t, s.Reset("PeterNeumann", note.TypeEd25519, peterEd25519(t)),
				"Reset must accept the signer")
			assertVerifier(t, &s, mustParseKey(t, peterKey))
			value, err := s.Sign([]byte(peterText))
			testkit.NoError(t, err, "Sign must succeed")
			want, err := base64.StdEncoding.DecodeString(peterValue)
			testkit.NoError(t, err, "the fixture must decode")
			testkit.Equal(t, value, want[4:], "the TextSigner must give the signature that x/mod records")
		})

		t.Run("sets a TextSigner to another key", func(t *testing.T) {
			t.Parallel()
			s := peterSigner(t)
			ts, ok := s.(*note.TextSigner)
			testkit.True(t, ok, "NewTextSigner must return a *TextSigner")
			testkit.NoError(t, ts.Reset("example.com/pq", pqType, mldsaSigner(t)), "Reset must accept the signer")
			testkit.Equal(t, ts.Key().Name, note.Name("example.com/pq"), "Reset must set the name")
			value, err := ts.Sign([]byte(peterText))
			testkit.NoError(t, err, "Sign must succeed")
			testkit.True(t, ts.Verify([]byte(peterText), value), "the TextSigner must sign with the new signer")
		})

		t.Run("leaves the TextSigner unchanged with an error", func(t *testing.T) {
			t.Parallel()
			s := peterSigner(t)
			ts, _ := s.(*note.TextSigner)
			testkit.ErrorIs(t, ts.Reset("a b", note.TypeEd25519, peterEd25519(t)), note.ErrKey,
				"Reset must refuse an invalid name")
			assertVerifier(t, ts, mustParseKey(t, peterKey))
		})
	})

	t.Run("Key", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the zero Key for the zero TextSigner", func(t *testing.T) {
			t.Parallel()
			var zero note.TextSigner
			testkit.Equal(t, zero.Key(), note.Key{}, "the zero TextSigner must have no key")
		})
	})

	t.Run("Verify", func(t *testing.T) {
		t.Parallel()

		t.Run("reports whether the value is a signature of the key over the text", func(t *testing.T) {
			t.Parallel()
			s := peterSigner(t)
			value, err := s.Sign([]byte(peterText))
			testkit.NoError(t, err, "Sign must succeed")
			testkit.True(t, s.Verify([]byte(peterText), value), "Verify must accept the signature over the text")
			testkit.False(t, s.Verify([]byte(peterText+"x\n"), value), "Verify must refuse it over another text")
		})

		t.Run("reports false for the zero TextSigner", func(t *testing.T) {
			t.Parallel()
			var zero note.TextSigner
			testkit.False(t, zero.Verify([]byte(peterText), []byte{1}), "the zero TextSigner must verify nothing")
		})
	})

	t.Run("Sign", func(t *testing.T) {
		t.Parallel()

		t.Run("returns ErrKey for the zero TextSigner", func(t *testing.T) {
			t.Parallel()
			var zero note.TextSigner
			value, err := zero.Sign([]byte(peterText))
			testkit.ErrorIs(t, err, note.ErrKey, "the zero TextSigner must sign nothing")
			testkit.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
			testkit.True(t, value == nil, "Sign must return no value with an error")
		})
	})

	t.Run("SignContext", func(t *testing.T) {
		t.Parallel()

		t.Run("returns ErrKey for the zero TextSigner", func(t *testing.T) {
			t.Parallel()
			var zero note.TextSigner
			_, err := zero.SignContext(t.Context(), []byte(peterText))
			testkit.ErrorIs(t, err, note.ErrKey, "the zero TextSigner must sign nothing")
		})

		t.Run("returns the signature under a live context", func(t *testing.T) {
			t.Parallel()
			value, err := sign.SignContext(t.Context(), peterSigner(t), []byte(peterText))
			testkit.NoError(t, err, "SignContext must sign under a live context")
			want, err := base64.StdEncoding.DecodeString(peterValue)
			testkit.NoError(t, err, "the fixture must decode")
			testkit.Equal(t, value, want[4:], "SignContext must give the signature of Sign")
		})

		t.Run("returns the cause of a context that ended", func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			_, err := sign.SignContext(ctx, peterSigner(t), []byte(peterText))
			testkit.ErrorIs(t, err, context.Canceled, "SignContext must return the cause of the context")
		})
	})

	t.Run("AppendSign", func(t *testing.T) {
		t.Parallel()

		want, err := base64.StdEncoding.DecodeString(peterValue)
		testkit.NoError(t, err, "the fixture must decode")

		t.Run("appends the signature that golang.org/x/mod's tests record", func(t *testing.T) {
			t.Parallel()
			got, err := peterSigner(t).AppendSign(t.Context(), []byte(prefix), []byte(peterText))
			testkit.NoError(t, err, "AppendSign must sign the text")
			testkit.Equal(t, string(got), prefix+string(want[4:]), "AppendSign must append the signature to dst")
		})

		t.Run("appends the signature of a signer that is not an AppendSigner", func(t *testing.T) {
			t.Parallel()
			s, err := note.NewTextSigner("a", pqType, fakeSigner{alg: crypto.AlgMLDSA44, pub: publicKey(), sig: want})
			testkit.NoError(t, err, "NewTextSigner must accept the signer")
			got, err := s.AppendSign(t.Context(), []byte(prefix), []byte(peterText))
			testkit.NoError(t, err, "AppendSign must sign the text")
			testkit.Equal(t, string(got), prefix+string(want), "AppendSign must append the signature to dst")
		})

		t.Run("returns dst unchanged with the cause of a context that ended", func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			got, err := peterSigner(t).AppendSign(ctx, []byte(prefix), []byte(peterText))
			testkit.ErrorIs(t, err, context.Canceled, "AppendSign must return the cause of the context")
			testkit.Equal(t, string(got), prefix, "AppendSign must return dst unchanged")
		})

		t.Run("returns dst unchanged and ErrKey for the zero TextSigner", func(t *testing.T) {
			t.Parallel()
			var zero note.TextSigner
			got, err := zero.AppendSign(t.Context(), []byte(prefix), []byte(peterText))
			testkit.ErrorIs(t, err, note.ErrKey, "the zero TextSigner must sign nothing")
			testkit.Equal(t, string(got), prefix, "AppendSign must return dst unchanged")
		})
	})
}

func BenchmarkText(b *testing.B) {
	k := mustParseKey(b, peterKey)
	entry := note.Text(ed25519.Resolve)
	ed := peterEd25519(b)
	s := peterSigner(b)
	text := []byte(peterText)

	value, err := s.Sign(text)
	testkit.NoError(b, err, "Sign must succeed")

	b.Run("Text", func(b *testing.B) {
		benchAllocs(b, 2, func() { sinkVerifier, errSink = entry(k) })
	})

	b.Run("NewTextSigner", func(b *testing.B) {
		benchAllocs(b, 1, func() { sinkSigner, errSink = note.NewTextSigner("PeterNeumann", note.TypeEd25519, ed) })
	})

	b.Run("TextSigner.Reset", func(b *testing.B) {
		var ts note.TextSigner
		benchZeroAlloc(b, func() { errSink = ts.Reset("PeterNeumann", note.TypeEd25519, ed) })
	})

	b.Run("Verify", func(b *testing.B) {
		benchZeroAlloc(b, func() { sinkBool = s.Verify(text, value) })
	})

	b.Run("Sign", func(b *testing.B) {
		benchAllocs(b, 1, func() { sinkBytes, errSink = s.Sign(text) })
	})

	b.Run("SignContext", func(b *testing.B) {
		ctx := b.Context()
		benchAllocs(b, 1, func() { sinkBytes, errSink = sign.SignContext(ctx, s, text) })
	})

	b.Run("AppendSign", func(b *testing.B) {
		ctx := b.Context()
		buf := make([]byte, 0, len(value))
		benchZeroAlloc(b, func() { sinkBytes, errSink = s.AppendSign(ctx, buf[:0], text) })
	})
}
