// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package checkpoint_test

import (
	stdmldsa "crypto/mldsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"math"
	"runtime"
	"strings"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/crypto/sign"
	"go.thesmos.sh/core/crypto/sign/mldsa"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/note"
	"go.thesmos.sh/core/tlog/checkpoint"
)

// The verifier key and the signed checkpoint of the ML-DSA-44 log of
// filippo.io/torchwood v0.10.0, cmd/litewitness/testdata/mldsa.txt. The
// log signs with type 0x06 and the timestamp 0.
const (
	litewitnessKeyFile  = "litewitness.vkey"
	litewitnessNoteFile = "litewitness.txt"
)

// The fixture values of the cases of the SubtreeV1 signatures.
const (
	// subtreeLabel is the label of a cosigned_message, which the tests
	// write from tlog-cosignature.
	subtreeLabel = "subtree/v1\n\x00"

	// pqName is the key name of the ML-DSA-44 cosigner of the tests.
	pqName = "example.com/pq"
)

// unwrapSigner is a decorator that exposes the signer it wraps through
// Unwrap() sign.Signer, and hides every other method of it.
type unwrapSigner struct {
	sign.Signer
}

// Unwrap returns the wrapped signer.
func (u unwrapSigner) Unwrap() sign.Signer { return u.Signer }

// unwrapVerifier is a decorator that exposes the signer it wraps through
// Unwrap() sign.Verifier.
type unwrapVerifier struct {
	sign.Signer
}

// Unwrap returns the wrapped signer.
func (u unwrapVerifier) Unwrap() sign.Verifier { return u.Signer }

// opaqueSigner is a decorator without Unwrap, which hides the context and
// the AppendSign of the signer that it wraps.
type opaqueSigner struct {
	sign.Signer
}

func TestSubtree(t *testing.T) {
	t.Parallel()

	entry := checkpoint.SubtreeV1(mldsa.Resolver(mldsa.MLDSA44, ""))

	// uncovered are the texts that a SubtreeV1 signature cannot cover, which
	// Sign and CheckText refuse alike, with the detail of each error.
	uncovered := []struct {
		name   string
		give   string
		detail string
	}{
		{
			name:   "returns ErrBody for a text that is not a body",
			give:   exampleLog + "\n5\n",
			detail: "a body has three lines or more, and ends in a newline",
		},
		{
			name:   "returns ErrBody for a body with extension lines",
			give:   cosignedText + "ext\n",
			detail: "a SubtreeV1 signature covers no extension lines",
		},
		{
			name:   "returns ErrBody for a root of 48 bytes",
			give:   "a\n5\n" + base64.StdEncoding.EncodeToString(root(t, 48).Bytes()) + "\n",
			detail: "a SubtreeV1 signature covers a root of 32 bytes, not 48",
		},
		{
			name:   "returns ErrBody for an origin of 256 bytes",
			give:   strings.Repeat("o", 256) + "\n5\n" + exampleRoot + "\n",
			detail: "a SubtreeV1 signature covers an origin and a key name of at most 255 bytes",
		},
		{
			name:   "returns ErrBody for an empty tree whose root is not SHA-256 of the empty string",
			give:   "a\n0\n" + exampleRoot + "\n",
			detail: "the root of an empty tree is SHA-256 of the empty string",
		},
	}

	t.Run("SubtreeV1", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a Verifier of the log signature of the litewitness test of torchwood v0.10.0",
			func(t *testing.T) {
				t.Parallel()
				k := mustParseKey(t, strings.TrimSuffix(string(readFile(t, litewitnessKeyFile)), "\n"))
				n := mustParse(t, readFile(t, litewitnessNoteFile))
				v, err := entry(k)
				assert.NoError(t, err, "SubtreeV1 must build the Verifier of the log")
				s, ok := n.Find(k)
				assert.True(t, ok, "the checkpoint must have the line of the log")
				assert.True(t, v.Verify(n.Text, s.Value), "Verify must accept the log signature")
			})

		t.Run("returns a Verifier of a log signature with the timestamp 0", func(t *testing.T) {
			t.Parallel()
			k := mustParseKey(t, strings.TrimSuffix(string(readFile(t, litewitnessKeyFile)), "\n"))
			s, ok := mustParse(t, readFile(t, litewitnessNoteFile)).Find(k)
			assert.True(t, ok, "the checkpoint must have the line of the log")
			stamp, err := checkpoint.Timestamp(s.Value)
			assert.NoError(t, err, "Timestamp must read the value")
			assert.Equal(t, stamp, time.Time{}, "the log must sign with the timestamp 0")
		})

		t.Run("returns a Verifier of the cosignatures that torchwood v0.10.0 made", func(t *testing.T) {
			t.Parallel()
			k := vectorKey(t, vectorPQ)
			v, err := entry(k)
			assert.NoError(t, err, "SubtreeV1 must build the Verifier of PQ")
			for _, file := range []string{cosignedFile, emptyFile} {
				n := mustParse(t, readFile(t, file))
				s, ok := n.Find(k)
				assert.True(t, ok, file+" must have the line of PQ")
				expect.True(t, v.Verify(n.Text, s.Value), "Verify must accept the line of PQ in "+file)
			}
		})

		t.Run("returns a Verifier of a signature over the message that tlog-cosignature specifies", func(t *testing.T) {
			t.Parallel()
			v, err := entry(pqKey(t, pqName))
			assert.NoError(t, err, "SubtreeV1 must build the Verifier")
			value := subtreeValue(t, pqName, exampleTimestamp, exampleLog, cosignedSize, exampleDigest(t).Bytes())
			assert.True(t, v.Verify([]byte(cosignedText), value), "Verify must accept the message of the specification")
		})

		t.Run("returns a Verifier that reports true for an origin of 255 bytes", func(t *testing.T) {
			t.Parallel()
			origin := strings.Repeat("o", 255)
			v, err := entry(pqKey(t, pqName))
			assert.NoError(t, err, "SubtreeV1 must build the Verifier")
			value := subtreeValue(t, pqName, 1, origin, cosignedSize, exampleDigest(t).Bytes())
			assert.True(t, v.Verify([]byte(origin+"\n5\n"+exampleRoot+"\n"), value),
				"Verify must accept an origin of 255 bytes")
		})

		t.Run("returns a Verifier that reports true for a key name of 255 bytes", func(t *testing.T) {
			t.Parallel()
			name := strings.Repeat("n", 255)
			v, err := entry(pqKey(t, note.Name(name)))
			assert.NoError(t, err, "SubtreeV1 must build the Verifier")
			value := subtreeValue(t, name, 1, exampleLog, cosignedSize, exampleDigest(t).Bytes())
			assert.True(t, v.Verify([]byte(cosignedText), value), "Verify must accept a key name of 255 bytes")
		})

		t.Run("returns a Verifier that reports true for the empty tree with the root of the empty string",
			func(t *testing.T) {
				t.Parallel()
				v, err := entry(pqKey(t, pqName))
				assert.NoError(t, err, "SubtreeV1 must build the Verifier")
				empty := sha256.Sum256(nil)
				value := subtreeValue(t, pqName, 1, "a", 0, empty[:])
				assert.True(t, v.Verify([]byte("a\n0\n"+base64.StdEncoding.EncodeToString(empty[:])+"\n"), value),
					"Verify must accept the root of the empty tree")
			})

		t.Run("returns a Verifier that reports false for an empty tree whose root is not SHA-256 of the empty string",
			func(t *testing.T) {
				t.Parallel()
				v, err := entry(pqKey(t, pqName))
				assert.NoError(t, err, "SubtreeV1 must build the Verifier")
				value := subtreeValue(t, pqName, 1, "a", 0, exampleDigest(t).Bytes())
				assert.False(t, v.Verify([]byte("a\n0\n"+exampleRoot+"\n"), value),
					"Verify must refuse another root for the empty tree")
			})

		t.Run("returns a Verifier that reports false for a body with extension lines", func(t *testing.T) {
			t.Parallel()
			v, err := entry(pqKey(t, pqName))
			assert.NoError(t, err, "SubtreeV1 must build the Verifier")
			value := subtreeValue(t, pqName, 1, exampleLog, cosignedSize, exampleDigest(t).Bytes())
			assert.True(t, v.Verify([]byte(cosignedText), value), "Verify must accept the first three lines")
			assert.False(t, v.Verify([]byte(cosignedText+"ext\n"), value),
				"Verify must refuse the signature of the three lines over a body with an extension line")
		})

		t.Run("returns a Verifier that reports false for a text that is not a body", func(t *testing.T) {
			t.Parallel()
			v, err := entry(pqKey(t, pqName))
			assert.NoError(t, err, "SubtreeV1 must build the Verifier")
			value := subtreeValue(t, pqName, 1, exampleLog, cosignedSize, exampleDigest(t).Bytes())
			assert.False(t, v.Verify([]byte(exampleLog+"\n5\n"), value), "Verify must refuse a text of two lines")
		})

		t.Run("returns a Verifier that reports false for a signature of the empty message over a text of two lines",
			func(t *testing.T) {
				t.Parallel()
				v, err := entry(pqKey(t, pqName))
				assert.NoError(t, err, "SubtreeV1 must build the Verifier")
				sig, err := mldsaSigner(t, mldsa.MLDSA44, pqName, "").Sign(nil)
				assert.NoError(t, err, "ML-DSA-44 must sign the empty message")
				value := append(binary.BigEndian.AppendUint64(nil, 1), sig...)
				assert.False(t, v.Verify([]byte(exampleLog+"\n5\n"), value),
					"Verify must refuse a text without a message")
			})

		t.Run("returns a Verifier that reports false for a root of 48 bytes", func(t *testing.T) {
			t.Parallel()
			v, err := entry(pqKey(t, pqName))
			assert.NoError(t, err, "SubtreeV1 must build the Verifier")
			value := subtreeValue(t, pqName, 1, "a", cosignedSize, root(t, 48).Bytes()[:32])
			text := "a\n5\n" + base64.StdEncoding.EncodeToString(root(t, 48).Bytes()) + "\n"
			assert.False(t, v.Verify([]byte(text), value), "Verify must refuse a root of 48 bytes")
		})

		t.Run("returns a Verifier that reports false for an origin of 256 bytes", func(t *testing.T) {
			t.Parallel()
			origin := strings.Repeat("o", 256)
			v, err := entry(pqKey(t, pqName))
			assert.NoError(t, err, "SubtreeV1 must build the Verifier")
			value := subtreeValue(t, pqName, 1, origin, cosignedSize, exampleDigest(t).Bytes())
			assert.False(t, v.Verify([]byte(origin+"\n5\n"+exampleRoot+"\n"), value),
				"Verify must refuse an origin whose length byte cannot state its length")
		})

		t.Run("returns a Verifier that reports false for a key name of 256 bytes", func(t *testing.T) {
			t.Parallel()
			name := strings.Repeat("n", 256)
			v, err := entry(pqKey(t, note.Name(name)))
			assert.NoError(t, err, "SubtreeV1 must build the Verifier")
			value := subtreeValue(t, name, 1, exampleLog, cosignedSize, exampleDigest(t).Bytes())
			assert.False(t, v.Verify([]byte(cosignedText), value),
				"Verify must refuse a key name whose length byte cannot state its length")
		})

		t.Run("returns a Verifier that reports false for a timestamp above 2^63 − 1", func(t *testing.T) {
			t.Parallel()
			v, err := entry(pqKey(t, pqName))
			assert.NoError(t, err, "SubtreeV1 must build the Verifier")
			value := subtreeValue(t, pqName, math.MaxInt64+1, exampleLog, cosignedSize, exampleDigest(t).Bytes())
			assert.False(t, v.Verify([]byte(cosignedText), value), "Verify must refuse the timestamp 2^63")
		})
	})

	t.Run("NewSubtreeV1Signer", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a Signer whose lines verify under SubtreeV1", func(t *testing.T) {
			t.Parallel()
			s := pqSigner(t, fake.New(clockTime))
			value, err := s.Sign([]byte(cosignedText))
			assert.NoError(t, err, "Sign must sign the checkpoint")
			v, err := entry(s.Key())
			assert.NoError(t, err, "SubtreeV1 must build the Verifier of the key")
			assert.True(t, v.Verify([]byte(cosignedText), value), "the Verifier must accept the line")
		})

		t.Run("returns a Signer whose values start with the time of the UTC source", func(t *testing.T) {
			t.Parallel()
			value, err := pqSigner(t, fake.New(clockTime)).Sign([]byte(cosignedText))
			assert.NoError(t, err, "Sign must sign the checkpoint")
			stamp, err := checkpoint.Timestamp(value)
			assert.NoError(t, err, "Timestamp must read the value")
			assert.Equal(t, stamp, clockTime, "the value must contain the time of the clock")
		})

		t.Run("returns a Signer that signs the message that tlog-cosignature specifies", func(t *testing.T) {
			t.Parallel()
			value, err := pqSigner(t, fake.New(clockTime)).Sign([]byte(cosignedText))
			assert.NoError(t, err, "Sign must sign the checkpoint")
			msg := cosignedMessage(pqName, uint64(clockTime.Unix()), exampleLog, cosignedSize, exampleDigest(t).Bytes())
			assert.True(t, mldsaSigner(t, mldsa.MLDSA44, pqName, "").Verify(msg, value[timestampBytes:]),
				"the signature must cover the message of the specification")
		})

		t.Run("returns a Signer that writes the timestamp 0 without a UTC source", func(t *testing.T) {
			t.Parallel()
			value, err := pqSigner(t, nil).Sign([]byte(cosignedText))
			assert.NoError(t, err, "Sign must sign without a UTC source")
			stamp, err := checkpoint.Timestamp(value)
			assert.NoError(t, err, "Timestamp must read the value")
			assert.Equal(t, stamp, time.Time{}, "a Signer without a UTC source must write the timestamp 0")
		})

		t.Run("returns a Signer of a name of 255 bytes", func(t *testing.T) {
			t.Parallel()
			_, err := checkpoint.NewSubtreeV1Signer(note.Name(strings.Repeat("n", 255)),
				checkpoint.TypeMLDSA44Cosignature, mldsaSigner(t, mldsa.MLDSA44, pqName, ""), nil, 0)
			assert.NoError(t, err, "NewSubtreeV1Signer must accept a name of 255 bytes")
		})

		t.Run("returns a Signer of an ML-DSA-44 signer with a context for a type without an assigned byte",
			func(t *testing.T) {
				t.Parallel()
				typ, err := note.NewType("example.com/subtree-ml-dsa-44")
				assert.NoError(t, err, "NewType must accept the identifier")
				_, err = checkpoint.NewSubtreeV1Signer(pqName, typ, mldsaSigner(t, mldsa.MLDSA44, pqName, "x"), nil, 0)
				assert.NoError(t, err, "NewSubtreeV1Signer must accept a context for a type without an assigned byte")
			})

		t.Run("returns a Signer of type 0x06 over a signer that does not report its context", func(t *testing.T) {
			t.Parallel()
			_, err := checkpoint.NewSubtreeV1Signer(pqName, checkpoint.TypeMLDSA44Cosignature,
				opaqueSigner{mldsaSigner(t, mldsa.MLDSA44, pqName, "x")}, nil, 0)
			assert.NoError(t, err, "NewSubtreeV1Signer must accept a signer that does not report its context")
		})

		tests := []struct {
			signer   sign.Signer
			want     error
			name     string
			giveName note.Name
			typ      note.Type
			maxError time.Duration
		}{
			{
				name: "returns ErrKey for a name of 256 bytes", giveName: note.Name(strings.Repeat("n", 256)),
				typ: checkpoint.TypeMLDSA44Cosignature, signer: mldsaSigner(t, mldsa.MLDSA44, pqName, ""),
				want: note.ErrKey,
			},
			{
				name: "returns ErrType for an invalid type", giveName: pqName, typ: "",
				signer: mldsaSigner(t, mldsa.MLDSA44, pqName, ""), want: note.ErrType,
			},
			{
				name: "returns ErrTimestamp for a negative error bound", giveName: pqName,
				typ: checkpoint.TypeMLDSA44Cosignature, signer: mldsaSigner(t, mldsa.MLDSA44, pqName, ""),
				maxError: -time.Second, want: checkpoint.ErrTimestamp,
			},
			{
				name: "returns ErrKey for type 0x06 with an Ed25519 signer", giveName: pqName,
				typ: checkpoint.TypeMLDSA44Cosignature, signer: ed25519Signer(t, pqName), want: note.ErrKey,
			},
			{
				name: "returns ErrKey for type 0x06 with an ML-DSA-65 signer", giveName: pqName,
				typ: checkpoint.TypeMLDSA44Cosignature, signer: mldsaSigner(t, mldsa.MLDSA65, pqName, ""),
				want: note.ErrKey,
			},
			{
				name: "returns ErrKey for type 0x06 with a signer of a context", giveName: pqName,
				typ: checkpoint.TypeMLDSA44Cosignature, signer: mldsaSigner(t, mldsa.MLDSA44, pqName, "x"),
				want: note.ErrKey,
			},
			{
				name: "returns ErrKey for type 0x06 with a context behind Unwrap of a Signer", giveName: pqName,
				typ:    checkpoint.TypeMLDSA44Cosignature,
				signer: unwrapSigner{mldsaSigner(t, mldsa.MLDSA44, pqName, "x")}, want: note.ErrKey,
			},
			{
				name: "returns ErrKey for type 0x06 with a context behind Unwrap of a Verifier", giveName: pqName,
				typ:    checkpoint.TypeMLDSA44Cosignature,
				signer: unwrapVerifier{mldsaSigner(t, mldsa.MLDSA44, pqName, "x")}, want: note.ErrKey,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				s, err := checkpoint.NewSubtreeV1Signer(tt.giveName, tt.typ, tt.signer, nil, tt.maxError)
				expect.ErrorIs(t, err, tt.want, "NewSubtreeV1Signer must refuse the signer")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				expect.Nil(t, s, "NewSubtreeV1Signer must return a nil Signer with an error")
			})
		}
	})

	t.Run("SubtreeV1Signer", func(t *testing.T) {
		t.Parallel()

		t.Run("Reset", func(t *testing.T) {
			t.Parallel()

			t.Run("sets a SubtreeV1Signer of the caller to the key of the signer", func(t *testing.T) {
				t.Parallel()
				var s checkpoint.SubtreeV1Signer
				assert.NoError(t, s.Reset(pqName, checkpoint.TypeMLDSA44Cosignature,
					mldsaSigner(t, mldsa.MLDSA44, pqName, ""), nil, 0), "Reset must accept the ML-DSA-44 signer")
				assert.Equal(t, s.Key(), pqKey(t, pqName), "Reset must set the key")
			})

			t.Run("sets a SubtreeV1Signer of the caller to the signatures of the signer", func(t *testing.T) {
				t.Parallel()
				var s checkpoint.SubtreeV1Signer
				assert.NoError(t, s.Reset(pqName, checkpoint.TypeMLDSA44Cosignature,
					mldsaSigner(t, mldsa.MLDSA44, pqName, ""), nil, 0), "Reset must accept the ML-DSA-44 signer")
				value, err := s.Sign([]byte(cosignedText))
				assert.NoError(t, err, "Sign must sign the checkpoint")
				v, err := entry(pqKey(t, pqName))
				assert.NoError(t, err, "SubtreeV1 must build the Verifier of the key")
				assert.True(t, v.Verify([]byte(cosignedText), value), "the Verifier must accept the line")
			})

			t.Run("leaves the SubtreeV1Signer unchanged with an error", func(t *testing.T) {
				t.Parallel()
				s := pqSigner(t, nil)
				err := s.Reset(pqName, checkpoint.TypeMLDSA44Cosignature, mldsaSigner(t, mldsa.MLDSA44, pqName, "x"),
					nil, 0)
				expect.ErrorIs(t, err, note.ErrKey, "Reset must refuse a signer with a context")
				expect.Equal(t, s.Key(), pqKey(t, pqName), "Reset must leave the key")
			})
		})

		t.Run("Sign", func(t *testing.T) {
			t.Parallel()

			t.Run("returns ErrKey for the zero SubtreeV1Signer", func(t *testing.T) {
				t.Parallel()
				var zero checkpoint.SubtreeV1Signer
				value, err := zero.Sign([]byte(cosignedText))
				expect.ErrorIs(t, err, note.ErrKey, "the zero SubtreeV1Signer must sign nothing")
				expect.Nil(t, value, "Sign must return a nil value with an error")
			})

			for _, tt := range uncovered {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					value, err := pqSigner(t, nil).Sign([]byte(tt.give))
					assert.ErrorIs(t, err, checkpoint.ErrBody, "Sign must refuse a body that SubtreeV1 cannot cover")
					expect.Equal(t, err.Error(), checkpoint.ErrBody.Error()+": "+tt.detail,
						"the error must state the rule")
					expect.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
					expect.Nil(t, value, "Sign must return a nil value with an error")
				})
			}

			t.Run("returns ErrBody for a body with extension lines from a signer that is not an AppendSigner",
				func(t *testing.T) {
					t.Parallel()
					s, err := checkpoint.NewSubtreeV1Signer(pqName, checkpoint.TypeMLDSA44Cosignature,
						opaqueSigner{mldsaSigner(t, mldsa.MLDSA44, pqName, "")}, nil, 0)
					assert.NoError(t, err, "NewSubtreeV1Signer must accept the signer")
					value, err := s.Sign([]byte(cosignedText + "ext\n"))
					expect.ErrorIs(t, err, checkpoint.ErrBody, "Sign must refuse a body that SubtreeV1 cannot cover")
					expect.Nil(t, value, "Sign must return a nil value with an error")
				})

			// The goroutines share the pooled buffers of the messages, and
			// ML-DSA signs with fresh randomness, so each value must verify.
			t.Run("returns a value that verifies to goroutines that sign at once", func(t *testing.T) {
				t.Parallel()
				s := pqSigner(t, nil)
				outcomes := history.Concurrently(goroutines, 10*time.Second, func(int) (any, error) {
					values := make([][]byte, 0, rounds)
					for range rounds {
						value, err := s.Sign([]byte(cosignedText))
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
					assert.Length(t, values, rounds, "every goroutine must sign each round")
					for _, value := range values {
						expect.True(t, s.Verify([]byte(cosignedText), value), "every value must verify under the key")
					}
				}
			})
		})

		t.Run("AppendSign", func(t *testing.T) {
			t.Parallel()

			t.Run("appends a value that verifies under SubtreeV1", func(t *testing.T) {
				t.Parallel()
				s := pqSigner(t, fake.New(clockTime))
				got, err := s.AppendSign(t.Context(), []byte(prefix), []byte(cosignedText))
				assert.NoError(t, err, "AppendSign must sign the checkpoint")
				expect.Equal(t, string(got[:len(prefix)]), prefix, "AppendSign must keep dst")
				expect.True(t, s.Verify([]byte(cosignedText), got[len(prefix):]), "the appended value must verify")
			})

			t.Run("returns dst unchanged with ErrBody", func(t *testing.T) {
				t.Parallel()
				got, err := pqSigner(t, nil).AppendSign(t.Context(), []byte(prefix), []byte(cosignedText+"ext\n"))
				expect.ErrorIs(t, err, checkpoint.ErrBody, "AppendSign must refuse a body that SubtreeV1 cannot cover")
				expect.Equal(t, string(got), prefix, "AppendSign must return dst unchanged")
			})
		})

		t.Run("AppendSignAt", func(t *testing.T) {
			t.Parallel()

			at := clockTime.Add(time.Hour)

			t.Run("appends the time of the caller from a SubtreeV1Signer without a UTC source", func(t *testing.T) {
				t.Parallel()
				s := pqSigner(t, nil)
				got, err := s.AppendSignAt(t.Context(), []byte(prefix), []byte(cosignedText), at)
				assert.NoError(t, err, "AppendSignAt must sign the checkpoint")
				stamp, err := checkpoint.Timestamp(got[len(prefix):])
				assert.NoError(t, err, "Timestamp must read the value")
				expect.Equal(t, stamp, at, "the value must contain the time of the caller")
				expect.Equal(t, string(got[:len(prefix)]), prefix, "AppendSignAt must keep dst")
				expect.True(t, s.Verify([]byte(cosignedText), got[len(prefix):]), "the appended value must verify")
			})

			t.Run("appends a signature over the message that tlog-cosignature specifies", func(t *testing.T) {
				t.Parallel()
				got, err := pqSigner(t, nil).AppendSignAt(t.Context(), nil, []byte(cosignedText), at)
				assert.NoError(t, err, "AppendSignAt must sign the checkpoint")
				msg := cosignedMessage(pqName, uint64(at.Unix()), exampleLog, cosignedSize, exampleDigest(t).Bytes())
				assert.True(t, mldsaSigner(t, mldsa.MLDSA44, pqName, "").Verify(msg, got[timestampBytes:]),
					"the signature must cover the message of the specification at the time of the caller")
			})

			t.Run("returns dst unchanged with ErrBody", func(t *testing.T) {
				t.Parallel()
				got, err := pqSigner(t, nil).AppendSignAt(t.Context(), []byte(prefix), []byte(cosignedText+"ext\n"), at)
				expect.ErrorIs(t, err, checkpoint.ErrBody,
					"AppendSignAt must refuse a body that SubtreeV1 cannot cover")
				expect.Equal(t, string(got), prefix, "AppendSignAt must return dst unchanged")
			})
		})

		t.Run("CheckText", func(t *testing.T) {
			t.Parallel()

			t.Run("returns nil for a body that a SubtreeV1 signature covers", func(t *testing.T) {
				t.Parallel()
				assert.NoError(t, pqSigner(t, nil).CheckText([]byte(cosignedText)),
					"CheckText must accept a body of three lines")
			})

			for _, tt := range uncovered {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					err := pqSigner(t, nil).CheckText([]byte(tt.give))
					assert.ErrorIs(t, err, checkpoint.ErrBody,
						"CheckText must refuse a body that SubtreeV1 cannot cover")
					expect.Equal(t, err.Error(), checkpoint.ErrBody.Error()+": "+tt.detail,
						"the error must state the rule")
					expect.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				})
			}
		})
	})
}

// TestSubtreeAllocs checks the allocation contract of each function and
// method. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
//
// Each measurement of a path through the pooled buffers of the package
// starts with two collections, which empty the pool. The warm-up call of
// MaxAllocs then grows a buffer, and the measured calls reuse it only when
// the path keeps the growth of the buffer and returns it to the pool.
//
//nolint:paralleltest // see above
func TestSubtreeAllocs(t *testing.T) {
	entry := checkpoint.SubtreeV1(mldsa.Resolver(mldsa.MLDSA44, ""))
	n := mustParse(t, readFile(t, cosignedFile))
	k := vectorKey(t, vectorPQ)
	inner := mldsaSigner(t, mldsa.MLDSA44, pqName, "")
	text := []byte(cosignedText)
	s := pqSigner(t, fake.New(clockTime))

	v, err := entry(k)
	assert.NoError(t, err, "SubtreeV1 must build the Verifier")
	line, ok := n.Find(k)
	assert.True(t, ok, "the checkpoint must have the line of PQ")

	t.Run("SubtreeV1", func(t *testing.T) {
		expect.MaxAllocs(t, func() { _, err = entry(k) }, 4,
			"the entry must allocate the Verifier and the ML-DSA-44 key alone")
		assert.NoError(t, err, "the test must measure a key that the entry accepts")
	})

	t.Run("Verify", func(t *testing.T) {
		var got bool
		runtime.GC()
		runtime.GC()
		expect.MaxAllocs(t, func() { got = v.Verify(n.Text, line.Value) }, 0, "Verify must not allocate")
		assert.True(t, got, "the test must measure a line that verifies")
	})

	t.Run("NewSubtreeV1Signer", func(t *testing.T) {
		expect.MaxAllocs(t, func() {
			_, err = checkpoint.NewSubtreeV1Signer(pqName, checkpoint.TypeMLDSA44Cosignature, inner, nil, 0)
		}, 1, "NewSubtreeV1Signer must allocate the SubtreeV1Signer alone")
		assert.NoError(t, err, "the test must measure a signer that NewSubtreeV1Signer accepts")
	})

	t.Run("SubtreeV1Signer", func(t *testing.T) {
		t.Run("Reset", func(t *testing.T) {
			var reused checkpoint.SubtreeV1Signer
			expect.MaxAllocs(t, func() {
				err = reused.Reset(pqName, checkpoint.TypeMLDSA44Cosignature, inner, nil, 0)
			}, 0, "Reset must not allocate")
			assert.NoError(t, err, "the test must measure a signer that Reset accepts")
		})

		t.Run("Sign", func(t *testing.T) {
			runtime.GC()
			runtime.GC()
			expect.MaxAllocs(t, func() { _, err = s.Sign(text) }, 2,
				"Sign must allocate the value and the signature of crypto/mldsa alone")
			assert.NoError(t, err, "the test must measure a text that Sign signs")
		})

		t.Run("AppendSign", func(t *testing.T) {
			ctx := t.Context()
			buf := make([]byte, 0, timestampBytes+stdmldsa.MLDSA44().SignatureSize())
			runtime.GC()
			runtime.GC()
			expect.MaxAllocs(t, func() { _, err = s.AppendSign(ctx, buf[:0], text) }, 1,
				"AppendSign must allocate the signature of crypto/mldsa alone")
			assert.NoError(t, err, "the test must measure a text that AppendSign signs")
		})

		t.Run("AppendSignAt", func(t *testing.T) {
			ctx := t.Context()
			buf := make([]byte, 0, timestampBytes+stdmldsa.MLDSA44().SignatureSize())
			runtime.GC()
			runtime.GC()
			expect.MaxAllocs(t, func() { _, err = s.AppendSignAt(ctx, buf[:0], text, clockTime) }, 1,
				"AppendSignAt must allocate the signature of crypto/mldsa alone")
			assert.NoError(t, err, "the test must measure a text that AppendSignAt signs")
		})

		t.Run("CheckText", func(t *testing.T) {
			runtime.GC()
			runtime.GC()
			expect.MaxAllocs(t, func() { err = s.CheckText(text) }, 0, "CheckText must not allocate")
			assert.NoError(t, err, "the test must measure a text that CheckText accepts")
		})
	})
}

// BenchmarkSubtree reports the cost of each function and method, and fails
// above the allocations that their contracts state.
func BenchmarkSubtree(b *testing.B) {
	entry := checkpoint.SubtreeV1(mldsa.Resolver(mldsa.MLDSA44, ""))
	n := mustParse(b, readFile(b, cosignedFile))
	k := vectorKey(b, vectorPQ)
	inner := mldsaSigner(b, mldsa.MLDSA44, pqName, "")
	text := []byte(cosignedText)
	s := pqSigner(b, fake.New(clockTime))

	v, err := entry(k)
	assert.NoError(b, err, "SubtreeV1 must build the Verifier")
	line, ok := n.Find(k)
	assert.True(b, ok, "the checkpoint must have the line of PQ")

	b.Run("SubtreeV1", func(b *testing.B) {
		var err error

		c := bench.Start(b).MaxAllocs(4)
		defer c.End()

		for c.Loop() {
			_, err = entry(k)
		}

		assert.NoError(b, err, "the benchmark must measure a key that the entry accepts")
	})

	b.Run("Verify", func(b *testing.B) {
		var got bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = v.Verify(n.Text, line.Value)
		}

		assert.True(b, got, "the benchmark must measure a line that verifies")
	})

	b.Run("NewSubtreeV1Signer", func(b *testing.B) {
		var err error

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			_, err = checkpoint.NewSubtreeV1Signer(pqName, checkpoint.TypeMLDSA44Cosignature, inner, nil, 0)
		}

		assert.NoError(b, err, "the benchmark must measure a signer that NewSubtreeV1Signer accepts")
	})

	b.Run("SubtreeV1Signer", func(b *testing.B) {
		b.Run("Reset", func(b *testing.B) {
			var reused checkpoint.SubtreeV1Signer

			var err error

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				err = reused.Reset(pqName, checkpoint.TypeMLDSA44Cosignature, inner, nil, 0)
			}

			assert.NoError(b, err, "the benchmark must measure a signer that Reset accepts")
		})

		b.Run("Sign", func(b *testing.B) {
			var err error

			c := bench.Start(b).MaxAllocs(2)
			defer c.End()

			for c.Loop() {
				_, err = s.Sign(text)
			}

			assert.NoError(b, err, "the benchmark must measure a text that Sign signs")
		})

		b.Run("AppendSign", func(b *testing.B) {
			ctx := b.Context()
			buf := make([]byte, 0, timestampBytes+stdmldsa.MLDSA44().SignatureSize())

			var err error

			c := bench.Start(b).MaxAllocs(1)
			defer c.End()

			for c.Loop() {
				_, err = s.AppendSign(ctx, buf[:0], text)
			}

			assert.NoError(b, err, "the benchmark must measure a text that AppendSign signs")
		})

		b.Run("AppendSignAt", func(b *testing.B) {
			ctx := b.Context()
			buf := make([]byte, 0, timestampBytes+stdmldsa.MLDSA44().SignatureSize())

			var err error

			c := bench.Start(b).MaxAllocs(1)
			defer c.End()

			for c.Loop() {
				_, err = s.AppendSignAt(ctx, buf[:0], text, clockTime)
			}

			assert.NoError(b, err, "the benchmark must measure a text that AppendSignAt signs")
		})

		b.Run("CheckText", func(b *testing.B) {
			var err error

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				err = s.CheckText(text)
			}

			assert.NoError(b, err, "the benchmark must measure a text that CheckText accepts")
		})
	})
}

// pqKey returns the key of type 0x06 named name, whose ML-DSA-44 key is
// that of mldsaSigner for pqName.
func pqKey(tb testing.TB, name note.Name) note.Key {
	tb.Helper()

	pub := mldsaSigner(tb, mldsa.MLDSA44, pqName, "").PublicKey()

	return note.Key{Name: name, Type: checkpoint.TypeMLDSA44Cosignature, PublicKey: pub}
}

// pqSigner returns the cosigner of type 0x06 named pqName, over the
// ML-DSA-44 key of pqName, with the time of utc within a second.
func pqSigner(tb testing.TB, utc clock.UTCSource) *checkpoint.SubtreeV1Signer {
	tb.Helper()

	s, err := checkpoint.NewSubtreeV1Signer(pqName, checkpoint.TypeMLDSA44Cosignature,
		mldsaSigner(tb, mldsa.MLDSA44, pqName, ""), utc, time.Second)
	assert.NoError(tb, err, "NewSubtreeV1Signer must accept the ML-DSA-44 signer")

	return s
}

// subtreeValue returns the value of a timestamped signature with timestamp
// t, by the ML-DSA-44 key of pqName, over the cosignedMessage of name,
// origin, size and hash.
func subtreeValue(tb testing.TB, name string, t uint64, origin string, size uint64, hash []byte) []byte {
	tb.Helper()

	sig, err := mldsaSigner(tb, mldsa.MLDSA44, pqName, "").Sign(cosignedMessage(name, t, origin, size, hash))
	assert.NoError(tb, err, "ML-DSA-44 must sign the message")

	return append(binary.BigEndian.AppendUint64(nil, t), sig...)
}

// cosignedMessage returns the cosigned_message of tlog-cosignature for the
// checkpoint of origin, size and root hash, by the cosigner name with
// timestamp t: the label, the name after its length byte, the timestamp,
// the origin after its length byte, the start 0, the size as the end, and
// the hash. A length byte of a field of 256 bytes or more contains the low
// byte of its length.
func cosignedMessage(name string, t uint64, origin string, size uint64, hash []byte) []byte {
	msg := []byte(subtreeLabel)
	msg = append(msg, byte(len(name)))
	msg = append(msg, name...)
	msg = binary.BigEndian.AppendUint64(msg, t)
	msg = append(msg, byte(len(origin)))
	msg = append(msg, origin...)
	msg = binary.BigEndian.AppendUint64(msg, 0)
	msg = binary.BigEndian.AppendUint64(msg, size)

	return append(msg, hash...)
}
