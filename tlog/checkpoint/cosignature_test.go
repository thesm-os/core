// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package checkpoint_test

import (
	"bytes"
	"context"
	stded25519 "crypto/ed25519"
	"crypto/sha256"
	"errors"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sign"
	"go.thesmos.sh/core/crypto/sign/ed25519"
	"go.thesmos.sh/core/crypto/sign/mldsa"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/note"
	"go.thesmos.sh/core/tlog/checkpoint"
)

// The files of testdata that torchwood v0.10.0 and golang.org/x/mod v0.40.0
// made: a policy of the log example.com/log and the witnesses W1, W2 and
// W3 of type 0x04 and PQ of type 0x06, a checkpoint that the log and every
// witness signed, a checkpoint with an extension line that the log and W1
// signed, and a checkpoint of the empty tree that the log and PQ signed.
const (
	policyFile    = "policy.txt"
	cosignedFile  = "cosigned.txt"
	extensionFile = "extension.txt"
	emptyFile     = "empty.txt"
)

// The names of the witnesses of policyFile, and the name that vectorKey
// reads as the log.
const (
	vectorLog = "log"
	vectorW1  = "W1"
	vectorW2  = "W2"
	vectorW3  = "W3"
	vectorPQ  = "PQ"
)

// The fixture values of the cases of the cosignatures.
const (
	// cosignedText is the text that the cosigners of the tests sign: a
	// checkpoint of exampleLog of size cosignedSize.
	cosignedText = exampleLog + "\n5\n" + exampleRoot + "\n"

	// cosignedSize is the size of cosignedText.
	cosignedSize = 5

	// cosignatureHeader is the start of the message of a CosignatureV1
	// signature, before the timestamp in decimal, which the tests write from
	// tlog-cosignature.
	cosignatureHeader = "cosignature/v1\ntime "

	// prefix is the content of dst before an AppendSign of the tests
	// appends to it.
	prefix = "prefix:"

	// timestampBytes is the length of the timestamp that starts the value
	// of a timestamped signature.
	timestampBytes = 8
)

// goroutines is the number of goroutines of the cases that sign or verify
// at once, and rounds the number of calls of each.
const (
	goroutines = 8
	rounds     = 20
)

// clockTime is the time of the fake clock of the cosigners of the tests.
var clockTime = time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)

// resolver is the note.Resolver of the three assigned types: type 0x01 over
// Ed25519, and the two cosignature types of tlog-cosignature.
var resolver = note.Resolver{
	note.TypeEd25519:                  note.Text(ed25519.Resolve),
	checkpoint.TypeEd25519Cosignature: checkpoint.CosignatureV1(ed25519.Resolve),
	checkpoint.TypeMLDSA44Cosignature: checkpoint.SubtreeV1(mldsa.Resolver(mldsa.MLDSA44, "")),
}

// failingUTC is a clock.UTCSource that returns its error.
type failingUTC struct {
	err error
}

// ReadUTC returns the zero reading and the error of f.
func (f failingUTC) ReadUTC() (clock.UTCReading, error) {
	return clock.UTCReading{}, f.err
}

// fakeSigner is a sign.Signer that reports its algorithm and its public
// key, returns its signature and its error, and reports its valid for
// every signature that it verifies.
type fakeSigner struct {
	err   error
	alg   crypto.Algorithm
	pub   []byte
	sig   []byte
	valid bool
}

// KeyID returns the zero KeyID.
func (fakeSigner) KeyID() sign.KeyID { return sign.KeyID{} }

// PublicKey returns the public key of f.
func (f fakeSigner) PublicKey() []byte { return f.pub }

// Algorithm returns the algorithm of f.
func (f fakeSigner) Algorithm() crypto.Algorithm { return f.alg }

// Verify reports the valid of f.
func (f fakeSigner) Verify(_, _ []byte) bool { return f.valid }

// Sign returns the signature and the error of f.
func (f fakeSigner) Sign([]byte) ([]byte, error) { return f.sig, f.err }

// retainingSigner is a sign.ContextSigner that is not a sign.AppendSigner.
// It keeps every message that it signs, as a signer behind a process
// boundary may read a message after SignContext returns.
type retainingSigner struct {
	sign.Signer

	// messages are the messages that SignContext signed, in order.
	messages *[][]byte
}

// SignContext keeps message, and returns the signature of the wrapped
// signer over it.
func (r retainingSigner) SignContext(_ context.Context, message []byte) ([]byte, error) {
	*r.messages = append(*r.messages, message)

	return r.Sign(message)
}

func TestCosignature(t *testing.T) {
	t.Parallel()

	t.Run("CosignatureV1", func(t *testing.T) {
		t.Parallel()

		entry := checkpoint.CosignatureV1(ed25519.Resolve)

		t.Run("returns a Verifier of the cosignatures that torchwood v0.10.0 made", func(t *testing.T) {
			t.Parallel()
			n := mustParse(t, readFile(t, cosignedFile))
			for _, name := range []string{vectorW1, vectorW2, vectorW3} {
				k := vectorKey(t, name)
				v, err := entry(k)
				assert.NoError(t, err, "CosignatureV1 must build the Verifier of "+name)
				s, ok := n.Find(k)
				assert.True(t, ok, "the checkpoint must have the line of "+name)
				expect.True(t, v.Verify(n.Text, s.Value), "Verify must accept the line of "+name)
			}
		})

		t.Run("returns a Verifier of a cosignature over a checkpoint with an extension line", func(t *testing.T) {
			t.Parallel()
			n := mustParse(t, readFile(t, extensionFile))
			k := vectorKey(t, vectorW1)
			v, err := entry(k)
			assert.NoError(t, err, "CosignatureV1 must build the Verifier")
			s, ok := n.Find(k)
			assert.True(t, ok, "the checkpoint must have the line of W1")
			assert.True(t, v.Verify(n.Text, s.Value), "Verify must accept the line over the extension line")
		})

		t.Run("returns a Verifier of the key", func(t *testing.T) {
			t.Parallel()
			k := vectorKey(t, vectorW1)
			v, err := entry(k)
			assert.NoError(t, err, "CosignatureV1 must build the Verifier")
			expect.Equal(t, v.Key(), k, "Key must return the key")
			expect.Equal(t, v.KeyID(), note.KeyID(k.Name, k.ID()), "KeyID must derive from the name and the key ID")
			expect.Equal(t, v.PublicKey(), k.PublicKey, "PublicKey must return the public key")
			expect.Equal(t, v.Algorithm(), note.Algorithm, "Algorithm must return note.Algorithm")
		})

		t.Run("returns a Verifier that keeps a copy of the public key", func(t *testing.T) {
			t.Parallel()
			k := vectorKey(t, vectorW1)
			v, err := entry(k)
			assert.NoError(t, err, "CosignatureV1 must build the Verifier")
			clear(k.PublicKey)
			assert.Equal(t, v.PublicKey(), vectorKey(t, vectorW1).PublicKey,
				"a change to the key of the caller must not change the Verifier")
		})

		t.Run("returns a Verifier that reports false for the line over another text", func(t *testing.T) {
			t.Parallel()
			n := mustParse(t, readFile(t, cosignedFile))
			k := vectorKey(t, vectorW1)
			v, err := entry(k)
			assert.NoError(t, err, "CosignatureV1 must build the Verifier")
			s, ok := n.Find(k)
			assert.True(t, ok, "the checkpoint must have the line of W1")
			assert.False(t, v.Verify([]byte(cosignedText), s.Value), "Verify must refuse the line over another text")
		})

		t.Run("returns a Verifier that reports false for the line with another timestamp", func(t *testing.T) {
			t.Parallel()
			n := mustParse(t, readFile(t, cosignedFile))
			k := vectorKey(t, vectorW1)
			v, err := entry(k)
			assert.NoError(t, err, "CosignatureV1 must build the Verifier")
			s, ok := n.Find(k)
			assert.True(t, ok, "the checkpoint must have the line of W1")
			changed := bytes.Clone(s.Value)
			changed[timestampBytes-1]++
			assert.False(t, v.Verify(n.Text, changed), "Verify must refuse the line with another timestamp")
		})

		t.Run("returns a Verifier that reports false for a value of 7 bytes over an algorithm that accepts it",
			func(t *testing.T) {
				t.Parallel()
				accepting := func(pub []byte) (sign.Verifier, error) {
					return fakeSigner{alg: crypto.AlgEd25519, pub: pub, valid: true}, nil
				}
				v, err := checkpoint.CosignatureV1(accepting)(vectorKey(t, vectorW1))
				assert.NoError(t, err, "CosignatureV1 must build the Verifier")
				assert.False(t, v.Verify([]byte(cosignedText), make([]byte, timestampBytes-1)),
					"Verify must refuse a value without a timestamp")
			})

		t.Run("returns ErrKey for a key that is not Valid", func(t *testing.T) {
			t.Parallel()
			v, err := entry(note.Key{Name: "a", Type: checkpoint.TypeEd25519Cosignature})
			expect.ErrorIs(t, err, note.ErrKey, "CosignatureV1 must refuse a key without a public key")
			expect.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
			expect.Nil(t, v, "CosignatureV1 must return a nil Verifier with an error")
		})

		t.Run("returns the error of resolve", func(t *testing.T) {
			t.Parallel()
			_, err := entry(note.Key{Name: "a", Type: checkpoint.TypeEd25519Cosignature, PublicKey: []byte{1}})
			assert.ErrorIs(t, err, ed25519.ErrInvalidPublicKeySize, "CosignatureV1 must return the error of resolve")
		})

		t.Run("returns ErrUnknownType when resolve returns no Verifier with no error", func(t *testing.T) {
			t.Parallel()
			none := func([]byte) (sign.Verifier, error) {
				return nil, nil //nolint:nilnil // the case is a resolve that returns neither
			}
			_, err := checkpoint.CosignatureV1(none)(vectorKey(t, vectorW1))
			expect.ErrorIs(t, err, note.ErrUnknownType, "CosignatureV1 must refuse a resolve without a Verifier")
			expect.Equal(t, errs.Classify(err), errs.Unsupported, "the error must classify as Unsupported")
		})
	})

	t.Run("NewCosignatureV1Signer", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a Signer whose lines verify under CosignatureV1", func(t *testing.T) {
			t.Parallel()
			s := witness(t, "example.com/w")
			value, err := s.Sign([]byte(cosignedText))
			assert.NoError(t, err, "Sign must sign the text")
			v, err := checkpoint.CosignatureV1(ed25519.Resolve)(s.Key())
			assert.NoError(t, err, "CosignatureV1 must build the Verifier of the key")
			assert.True(t, v.Verify([]byte(cosignedText), value), "the Verifier must accept the line")
		})

		t.Run("returns a Signer of the key of the signer", func(t *testing.T) {
			t.Parallel()
			ed := ed25519Signer(t, "example.com/w")
			s, err := checkpoint.NewCosignatureV1Signer("example.com/w", checkpoint.TypeEd25519Cosignature, ed,
				fake.New(clockTime), time.Second)
			assert.NoError(t, err, "NewCosignatureV1Signer must accept the Ed25519 signer")
			want := note.Key{Name: "example.com/w", Type: checkpoint.TypeEd25519Cosignature, PublicKey: ed.PublicKey()}
			expect.Equal(t, s.Key(), want, "Key must return the name, the type and the public key")
			expect.Equal(t, s.KeyID(), note.KeyID(want.Name, want.ID()), "KeyID must derive from the key")
		})

		t.Run("returns a Signer of the public key of the signer without a copy", func(t *testing.T) {
			t.Parallel()
			pub := make([]byte, 32)
			s, err := checkpoint.NewCosignatureV1Signer("a", checkpoint.TypeEd25519Cosignature,
				fakeSigner{alg: crypto.AlgEd25519, pub: pub}, fake.New(clockTime), time.Second)
			assert.NoError(t, err, "NewCosignatureV1Signer must accept the signer")
			expect.Equal(t, s.PublicKey(), pub, "the Signer must use the immutable key of the signer",
				expect.ByIdentity())
			expect.Equal(t, s.Key().PublicKey, pub, "the key of the Signer must use the same key", expect.ByIdentity())
		})

		t.Run("returns a Signer of an ML-DSA key of a type without an assigned byte", func(t *testing.T) {
			t.Parallel()
			typ, err := note.NewType("example.com/cosignature-ml-dsa-44")
			assert.NoError(t, err, "NewType must accept the identifier")
			_, err = checkpoint.NewCosignatureV1Signer("a", typ, mldsaSigner(t, mldsa.MLDSA44, "a", ""),
				fake.New(clockTime), time.Second)
			assert.NoError(t, err, "NewCosignatureV1Signer must accept ML-DSA for a type without an assigned byte")
		})

		t.Run("returns a Signer of the error bound 0", func(t *testing.T) {
			t.Parallel()
			s, err := checkpoint.NewCosignatureV1Signer("a", checkpoint.TypeEd25519Cosignature,
				ed25519Signer(t, "a"), fake.New(clockTime), 0)
			assert.NoError(t, err, "NewCosignatureV1Signer must accept the error bound 0")
			_, err = s.Sign([]byte(cosignedText))
			assert.NoError(t, err, "Sign must sign with a reading without error")
		})

		tests := []struct {
			signer   sign.Signer
			utc      clock.UTCSource
			want     error
			name     string
			giveName note.Name
			typ      note.Type
			maxError time.Duration
		}{
			{
				name: "returns ErrKey for a nil signer", giveName: "a", typ: checkpoint.TypeEd25519Cosignature,
				signer: nil, utc: fake.New(clockTime), want: note.ErrKey,
			},
			{
				name: "returns ErrKey for an invalid name", giveName: "a b", typ: checkpoint.TypeEd25519Cosignature,
				signer: ed25519Signer(t, "a"), utc: fake.New(clockTime), want: note.ErrKey,
			},
			{
				name: "returns ErrType for an invalid type", giveName: "a", typ: "\xff",
				signer: ed25519Signer(t, "a"), utc: fake.New(clockTime), want: note.ErrType,
			},
			{
				name: "returns ErrKey for type 0x04 with a signer of another algorithm", giveName: "a",
				typ: checkpoint.TypeEd25519Cosignature, signer: mldsaSigner(t, mldsa.MLDSA44, "a", ""),
				utc: fake.New(clockTime), want: note.ErrKey,
			},
			{
				name: "returns ErrKey for a signer without a public key", giveName: "a", typ: "\x05",
				signer: fakeSigner{alg: crypto.AlgEd25519}, utc: fake.New(clockTime), want: note.ErrKey,
			},
			{
				name: "returns ErrTimestamp for a nil UTC source", giveName: "a",
				typ: checkpoint.TypeEd25519Cosignature, signer: ed25519Signer(t, "a"), utc: nil,
				want: checkpoint.ErrTimestamp,
			},
			{
				name: "returns ErrTimestamp for a negative error bound", giveName: "a",
				typ: checkpoint.TypeEd25519Cosignature, signer: ed25519Signer(t, "a"), utc: fake.New(clockTime),
				maxError: -time.Nanosecond, want: checkpoint.ErrTimestamp,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				s, err := checkpoint.NewCosignatureV1Signer(tt.giveName, tt.typ, tt.signer, tt.utc, tt.maxError)
				expect.ErrorIs(t, err, tt.want, "NewCosignatureV1Signer must refuse the signer")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				expect.Nil(t, s, "NewCosignatureV1Signer must return a nil Signer with an error")
			})
		}
	})

	t.Run("CosignatureV1Signer", func(t *testing.T) {
		t.Parallel()

		t.Run("Reset", func(t *testing.T) {
			t.Parallel()

			t.Run("sets a CosignatureV1Signer of the caller to the key of the signer", func(t *testing.T) {
				t.Parallel()
				var s checkpoint.CosignatureV1Signer
				err := s.Reset("example.com/w", checkpoint.TypeEd25519Cosignature, ed25519Signer(t, "example.com/w"),
					fake.New(clockTime), time.Second)
				assert.NoError(t, err, "Reset must accept the signer")
				assert.Equal(t, s.Key(), witness(t, "example.com/w").Key(), "Reset must set the key")
			})

			t.Run("sets a CosignatureV1Signer to the signatures of the signer", func(t *testing.T) {
				t.Parallel()
				var s checkpoint.CosignatureV1Signer
				err := s.Reset("example.com/w", checkpoint.TypeEd25519Cosignature, ed25519Signer(t, "example.com/w"),
					fake.New(clockTime), time.Second)
				assert.NoError(t, err, "Reset must accept the signer")
				value, err := s.Sign([]byte(cosignedText))
				assert.NoError(t, err, "Sign must sign the text")
				assert.True(t, witness(t, "example.com/w").Verify([]byte(cosignedText), value),
					"the line must verify under the key")
			})

			t.Run("sets a CosignatureV1Signer to another key", func(t *testing.T) {
				t.Parallel()
				s, err := checkpoint.NewCosignatureV1Signer("a", checkpoint.TypeEd25519Cosignature,
					ed25519Signer(t, "a"), fake.New(clockTime), time.Second)
				assert.NoError(t, err, "NewCosignatureV1Signer must accept the signer")
				assert.NoError(t, s.Reset("b", checkpoint.TypeEd25519Cosignature, ed25519Signer(t, "b"),
					fake.New(clockTime), time.Second), "Reset must accept the signer")
				assert.Equal(t, s.Key(), witness(t, "b").Key(), "Reset must set the key of b")
			})

			t.Run("leaves the CosignatureV1Signer unchanged with an error", func(t *testing.T) {
				t.Parallel()
				s, err := checkpoint.NewCosignatureV1Signer("a", checkpoint.TypeEd25519Cosignature,
					ed25519Signer(t, "a"), fake.New(clockTime), time.Second)
				assert.NoError(t, err, "NewCosignatureV1Signer must accept the signer")
				err = s.Reset("b", checkpoint.TypeEd25519Cosignature, ed25519Signer(t, "b"), nil, time.Second)
				expect.ErrorIs(t, err, checkpoint.ErrTimestamp, "Reset must refuse a nil UTC source")
				expect.Equal(t, s.Key(), witness(t, "a").Key(), "Reset must leave the key of a")
			})
		})

		t.Run("Key", func(t *testing.T) {
			t.Parallel()

			t.Run("returns the zero Key for the zero CosignatureV1Signer", func(t *testing.T) {
				t.Parallel()
				var zero checkpoint.CosignatureV1Signer
				assert.Equal(t, zero.Key(), note.Key{}, "the zero CosignatureV1Signer must have no key")
			})
		})

		t.Run("Verify", func(t *testing.T) {
			t.Parallel()

			t.Run("reports true for a line of the Signer", func(t *testing.T) {
				t.Parallel()
				s := witness(t, "a")
				value, err := s.Sign([]byte(cosignedText))
				assert.NoError(t, err, "Sign must sign the text")
				assert.True(t, s.Verify([]byte(cosignedText), value), "the Signer must accept its own line")
			})

			t.Run("reports false for the zero CosignatureV1Signer", func(t *testing.T) {
				t.Parallel()
				var zero checkpoint.CosignatureV1Signer
				value := make([]byte, timestampBytes+stded25519.SignatureSize)
				assert.False(t, zero.Verify([]byte(cosignedText), value),
					"the zero CosignatureV1Signer must verify nothing")
			})
		})

		t.Run("Sign", func(t *testing.T) {
			t.Parallel()

			t.Run("returns ErrKey for the zero CosignatureV1Signer", func(t *testing.T) {
				t.Parallel()
				var zero checkpoint.CosignatureV1Signer
				value, err := zero.Sign([]byte(cosignedText))
				expect.ErrorIs(t, err, note.ErrKey, "the zero CosignatureV1Signer must sign nothing")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				expect.Nil(t, value, "Sign must return a nil value with an error")
			})

			t.Run("returns a value that starts with the time of the UTC source", func(t *testing.T) {
				t.Parallel()
				value, err := witness(t, "a").Sign([]byte(cosignedText))
				assert.NoError(t, err, "Sign must sign the text")
				got, err := checkpoint.Timestamp(value)
				assert.NoError(t, err, "Timestamp must read the value")
				assert.Equal(t, got, clockTime, "the value must contain the time of the clock")
			})

			t.Run("returns a value of the timestamp and the Ed25519 signature", func(t *testing.T) {
				t.Parallel()
				value, err := witness(t, "a").Sign([]byte(cosignedText))
				assert.NoError(t, err, "Sign must sign the text")
				assert.Length(t, value, timestampBytes+stded25519.SignatureSize,
					"the value must contain the timestamp and the Ed25519 signature")
			})

			// The goroutines share the pooled buffers of the messages. The
			// time of the fake clock does not move and Ed25519 signs
			// deterministically, so every value equals the value of one call.
			t.Run("returns the value of one call to goroutines that sign at once", func(t *testing.T) {
				t.Parallel()
				s := witness(t, "a")
				want, err := s.Sign([]byte(cosignedText))
				assert.NoError(t, err, "Sign must sign the text")
				outcomes := history.Concurrently(goroutines, 10*time.Second, func(int) (any, error) {
					values := make([][]byte, 0, rounds)
					for range rounds {
						value, signErr := s.Sign([]byte(cosignedText))
						if signErr != nil {
							return values, signErr
						}
						values = append(values, value)
					}

					return values, nil
				})
				for _, o := range outcomes {
					assert.True(t, o.Finished, "every goroutine must finish")
					assert.NoError(t, o.Error, "Sign must sign on every goroutine")
					values, _ := o.Output.([][]byte)
					assert.Equal(t, values, slices.Repeat([][]byte{want}, rounds),
						"every value must equal the value of one call")
				}
			})

			t.Run("returns a value that starts with the whole seconds of the reading", func(t *testing.T) {
				t.Parallel()
				s, err := checkpoint.NewCosignatureV1Signer("a", checkpoint.TypeEd25519Cosignature,
					ed25519Signer(t, "a"), fake.New(clockTime.Add(999*time.Millisecond)), time.Second)
				assert.NoError(t, err, "NewCosignatureV1Signer must accept the signer")
				value, err := s.Sign([]byte(cosignedText))
				assert.NoError(t, err, "Sign must sign the text")
				got, err := checkpoint.Timestamp(value)
				assert.NoError(t, err, "Timestamp must read the value")
				assert.Equal(t, got, clockTime, "the value must contain the whole seconds")
			})

			t.Run("signs with a reading whose error bound equals the bound of the Signer", func(t *testing.T) {
				t.Parallel()
				c := fake.New(clockTime)
				c.SetUTCError(time.Second, true)
				s, err := checkpoint.NewCosignatureV1Signer("a", checkpoint.TypeEd25519Cosignature,
					ed25519Signer(t, "a"), c, time.Second)
				assert.NoError(t, err, "NewCosignatureV1Signer must accept the signer")
				_, err = s.Sign([]byte(cosignedText))
				assert.NoError(t, err, "Sign must sign with a reading within the bound")
			})

			t.Run("returns ErrClock with the error of a UTC source that fails", func(t *testing.T) {
				t.Parallel()
				failure := errors.New("the UTC source fails")
				s, err := checkpoint.NewCosignatureV1Signer("a", checkpoint.TypeEd25519Cosignature,
					ed25519Signer(t, "a"), failingUTC{err: failure}, time.Second)
				assert.NoError(t, err, "NewCosignatureV1Signer must accept the signer")
				_, err = s.Sign([]byte(cosignedText))
				expect.ErrorIs(t, err, checkpoint.ErrClock, "Sign must refuse to sign without a reading")
				expect.ErrorIs(t, err, failure, "Sign must return the error of the UTC source")
				expect.Equal(t, errs.Classify(err), errs.Transient, "the error must classify as Transient")
			})

			clockTests := []struct {
				at       time.Time
				name     string
				maxError time.Duration
				synced   bool
			}{
				{name: "returns ErrClock for an unsynchronised reading", at: clockTime, synced: false},
				{
					name: "returns ErrClock for an error bound above the bound of the Signer", at: clockTime,
					maxError: time.Second + time.Nanosecond, synced: true,
				},
				{name: "returns ErrClock for a reading before the Unix epoch", at: time.Unix(-1, 0), synced: true},
			}
			for _, tt := range clockTests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					c := fake.New(tt.at)
					c.SetUTCError(tt.maxError, tt.synced)
					s, err := checkpoint.NewCosignatureV1Signer("a", checkpoint.TypeEd25519Cosignature,
						ed25519Signer(t, "a"), c, time.Second)
					assert.NoError(t, err, "NewCosignatureV1Signer must accept the signer")
					value, err := s.Sign([]byte(cosignedText))
					expect.ErrorIs(t, err, checkpoint.ErrClock, "Sign must refuse to sign with the reading")
					expect.Equal(t, errs.Classify(err), errs.Transient, "the error must classify as Transient")
					expect.Nil(t, value, "Sign must return a nil value with an error")
				})
			}

			t.Run("returns ErrClock that states the Synced flag of the reading", func(t *testing.T) {
				t.Parallel()
				c := fake.New(clockTime)
				c.SetUTCError(0, false)
				s, err := checkpoint.NewCosignatureV1Signer("a", checkpoint.TypeEd25519Cosignature,
					ed25519Signer(t, "a"), c, time.Second)
				assert.NoError(t, err, "NewCosignatureV1Signer must accept the signer")
				_, err = s.Sign([]byte(cosignedText))
				assert.ErrorIs(t, err, checkpoint.ErrClock, "Sign must refuse to sign with the reading")
				assert.Equal(t, err.Error(),
					checkpoint.ErrClock.Error()+": a reading with Synced false and MaxError 0s, for the bound 1s",
					"the error must state the reading")
			})

			t.Run("returns the error of the signer", func(t *testing.T) {
				t.Parallel()
				failure := errors.New("the signer fails")
				signer := fakeSigner{alg: crypto.AlgEd25519, pub: make([]byte, 32), err: failure}
				s, err := checkpoint.NewCosignatureV1Signer("a", checkpoint.TypeEd25519Cosignature, signer,
					fake.New(clockTime), time.Second)
				assert.NoError(t, err, "NewCosignatureV1Signer must accept the signer")
				_, err = s.Sign([]byte(cosignedText))
				assert.ErrorIs(t, err, failure, "Sign must return the error of the signer")
			})
		})

		t.Run("SignContext", func(t *testing.T) {
			t.Parallel()

			t.Run("returns a value that verifies under a context that has not ended", func(t *testing.T) {
				t.Parallel()
				s := witness(t, "a")
				value, err := sign.SignContext(t.Context(), s, []byte(cosignedText))
				assert.NoError(t, err, "SignContext must sign under a context that has not ended")
				assert.True(t, s.Verify([]byte(cosignedText), value), "the value must verify")
			})

			t.Run("returns the cause of a context that the caller cancelled", func(t *testing.T) {
				t.Parallel()
				s := witness(t, "a")
				assert.HonoursCancellation(t, func(ctx context.Context) error {
					_, err := sign.SignContext(ctx, s, []byte(cosignedText))

					return err
				}, "SignContext must return the cause of a cancelled context")
			})
		})

		t.Run("AppendSign", func(t *testing.T) {
			t.Parallel()

			t.Run("appends a value that verifies under CosignatureV1", func(t *testing.T) {
				t.Parallel()
				s := witness(t, "a")
				got, err := s.AppendSign(t.Context(), []byte(prefix), []byte(cosignedText))
				assert.NoError(t, err, "AppendSign must sign the text")
				expect.Equal(t, string(got[:len(prefix)]), prefix, "AppendSign must keep dst")
				expect.True(t, s.Verify([]byte(cosignedText), got[len(prefix):]), "the appended value must verify")
			})

			t.Run("appends the value of a signer that is not an AppendSigner", func(t *testing.T) {
				t.Parallel()
				var messages [][]byte
				s := retainingWitness(t, &messages)
				got, err := s.AppendSign(t.Context(), []byte(prefix), []byte(cosignedText))
				assert.NoError(t, err, "AppendSign must sign the text")
				expect.Equal(t, string(got[:len(prefix)]), prefix, "AppendSign must keep dst")
				expect.True(t, s.Verify([]byte(cosignedText), got[len(prefix):]), "the appended value must verify")
			})

			t.Run("builds each message of a signer that is not an AppendSigner in a buffer of its own",
				func(t *testing.T) {
					t.Parallel()
					var messages [][]byte
					s := retainingWitness(t, &messages)
					for _, text := range []string{cosignedText, "another text\n"} {
						_, err := s.AppendSign(t.Context(), nil, []byte(text))
						assert.NoError(t, err, "AppendSign must sign the text")
					}
					want := cosignatureHeader + strconv.FormatInt(clockTime.Unix(), 10) + "\n" + cosignedText
					assert.Equal(t, string(messages[0]), want,
						"a later call must not change a message that the signer kept")
				})

			t.Run("returns dst unchanged with ErrClock", func(t *testing.T) {
				t.Parallel()
				s, err := checkpoint.NewCosignatureV1Signer("a", checkpoint.TypeEd25519Cosignature,
					ed25519Signer(t, "a"), failingUTC{err: errors.New("the UTC source fails")}, time.Second)
				assert.NoError(t, err, "NewCosignatureV1Signer must accept the signer")
				got, err := s.AppendSign(t.Context(), []byte(prefix), []byte(cosignedText))
				expect.ErrorIs(t, err, checkpoint.ErrClock, "AppendSign must refuse to sign without a reading")
				expect.Equal(t, string(got), prefix, "AppendSign must return dst unchanged")
			})

			t.Run("returns dst unchanged with the error of a signer that is not an AppendSigner", func(t *testing.T) {
				t.Parallel()
				failure := errors.New("the signer fails")
				signer := fakeSigner{alg: crypto.AlgEd25519, pub: make([]byte, 32), err: failure}
				s, err := checkpoint.NewCosignatureV1Signer("a", checkpoint.TypeEd25519Cosignature, signer,
					fake.New(clockTime), time.Second)
				assert.NoError(t, err, "NewCosignatureV1Signer must accept the signer")
				got, err := s.AppendSign(t.Context(), []byte(prefix), []byte(cosignedText))
				expect.ErrorIs(t, err, failure, "AppendSign must return the error of the signer")
				expect.Equal(t, string(got), prefix, "AppendSign must return dst unchanged")
			})

			t.Run("returns dst unchanged with the cause of a context that ended", func(t *testing.T) {
				t.Parallel()
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				got, err := witness(t, "a").AppendSign(ctx, []byte(prefix), []byte(cosignedText))
				expect.ErrorIs(t, err, context.Canceled, "AppendSign must return the cause of the context")
				expect.Equal(t, string(got), prefix, "AppendSign must return dst unchanged")
			})

			t.Run("returns dst unchanged with ErrKey for the zero CosignatureV1Signer", func(t *testing.T) {
				t.Parallel()
				var zero checkpoint.CosignatureV1Signer
				got, err := zero.AppendSign(t.Context(), []byte(prefix), []byte(cosignedText))
				expect.ErrorIs(t, err, note.ErrKey, "the zero CosignatureV1Signer must sign nothing")
				expect.Equal(t, string(got), prefix, "AppendSign must return dst unchanged")
			})
		})

		t.Run("AppendSignAt", func(t *testing.T) {
			t.Parallel()

			at := clockTime.Add(time.Hour)

			t.Run("appends a value that verifies under CosignatureV1", func(t *testing.T) {
				t.Parallel()
				s := witness(t, "a")
				got, err := s.AppendSignAt(t.Context(), []byte(prefix), []byte(cosignedText), at)
				assert.NoError(t, err, "AppendSignAt must sign the text")
				expect.Equal(t, string(got[:len(prefix)]), prefix, "AppendSignAt must keep dst")
				expect.True(t, s.Verify([]byte(cosignedText), got[len(prefix):]), "the appended value must verify")
			})

			t.Run("appends the whole seconds of the time of the caller", func(t *testing.T) {
				t.Parallel()
				got, err := witness(t, "a").AppendSignAt(t.Context(), nil, []byte(cosignedText),
					at.Add(999*time.Millisecond))
				assert.NoError(t, err, "AppendSignAt must sign the text")
				stamp, err := checkpoint.Timestamp(got)
				assert.NoError(t, err, "Timestamp must read the value")
				assert.Equal(t, stamp, at, "the value must contain the whole seconds of the time")
			})

			t.Run("signs without a reading of the UTC source", func(t *testing.T) {
				t.Parallel()
				s, err := checkpoint.NewCosignatureV1Signer("a", checkpoint.TypeEd25519Cosignature,
					ed25519Signer(t, "a"), failingUTC{err: errors.New("the UTC source fails")}, time.Second)
				assert.NoError(t, err, "NewCosignatureV1Signer must accept the signer")
				got, err := s.AppendSignAt(t.Context(), nil, []byte(cosignedText), at)
				assert.NoError(t, err, "AppendSignAt must sign without a reading")
				assert.True(t, s.Verify([]byte(cosignedText), got), "the value must verify")
			})

			t.Run("signs at the first second after the Unix epoch", func(t *testing.T) {
				t.Parallel()
				first := time.Unix(1, 0).UTC()
				got, err := witness(t, "a").AppendSignAt(t.Context(), nil, []byte(cosignedText), first)
				assert.NoError(t, err, "AppendSignAt must sign at the timestamp 1")
				stamp, err := checkpoint.Timestamp(got)
				assert.NoError(t, err, "Timestamp must read the value")
				assert.Equal(t, stamp, first, "the value must contain the timestamp 1")
			})

			t.Run("passes the message of the time of the caller to a signer that is not an AppendSigner",
				func(t *testing.T) {
					t.Parallel()
					var messages [][]byte
					s := retainingWitness(t, &messages)
					_, err := s.AppendSignAt(t.Context(), nil, []byte(cosignedText), at)
					assert.NoError(t, err, "AppendSignAt must sign the text")
					want := cosignatureHeader + strconv.FormatInt(at.Unix(), 10) + "\n" + cosignedText
					assert.Equal(t, string(messages[0]), want, "the signer must receive the message of the time")
				})

			timeTests := []struct {
				give time.Time
				name string
			}{
				{
					name: "returns ErrTimestamp with dst unchanged for a time in the first second of the Unix epoch",
					give: time.Unix(0, 999_999_999),
				},
				{
					name: "returns ErrTimestamp with dst unchanged for a time before the Unix epoch",
					give: time.Unix(-1, 0),
				},
				{name: "returns ErrTimestamp with dst unchanged for the zero Time", give: time.Time{}},
			}
			for _, tt := range timeTests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					got, err := witness(t, "a").AppendSignAt(t.Context(), []byte(prefix), []byte(cosignedText), tt.give)
					expect.ErrorIs(t, err, checkpoint.ErrTimestamp,
						"AppendSignAt must refuse a timestamp that is not positive")
					expect.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
					expect.Equal(t, string(got), prefix, "AppendSignAt must return dst unchanged")
				})
			}

			t.Run("returns dst unchanged with the error of the signer", func(t *testing.T) {
				t.Parallel()
				failure := errors.New("the signer fails")
				signer := fakeSigner{alg: crypto.AlgEd25519, pub: make([]byte, 32), err: failure}
				s, err := checkpoint.NewCosignatureV1Signer("a", checkpoint.TypeEd25519Cosignature, signer,
					fake.New(clockTime), time.Second)
				assert.NoError(t, err, "NewCosignatureV1Signer must accept the signer")
				got, err := s.AppendSignAt(t.Context(), []byte(prefix), []byte(cosignedText), at)
				expect.ErrorIs(t, err, failure, "AppendSignAt must return the error of the signer")
				expect.Equal(t, string(got), prefix, "AppendSignAt must return dst unchanged")
			})

			t.Run("returns dst unchanged with the cause of a context that ended", func(t *testing.T) {
				t.Parallel()
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				got, err := witness(t, "a").AppendSignAt(ctx, []byte(prefix), []byte(cosignedText), at)
				expect.ErrorIs(t, err, context.Canceled, "AppendSignAt must return the cause of the context")
				expect.Equal(t, string(got), prefix, "AppendSignAt must return dst unchanged")
			})

			t.Run("returns dst unchanged with ErrKey for the zero CosignatureV1Signer", func(t *testing.T) {
				t.Parallel()
				var zero checkpoint.CosignatureV1Signer
				got, err := zero.AppendSignAt(t.Context(), []byte(prefix), []byte(cosignedText), at)
				expect.ErrorIs(t, err, note.ErrKey, "the zero CosignatureV1Signer must sign nothing")
				expect.Equal(t, string(got), prefix, "AppendSignAt must return dst unchanged")
			})
		})

		t.Run("CheckText", func(t *testing.T) {
			t.Parallel()

			t.Run("returns nil for a text that is not a checkpoint", func(t *testing.T) {
				t.Parallel()
				assert.NoError(t, witness(t, "a").CheckText([]byte("another text\n")),
					"CheckText must accept every text of CosignatureV1")
			})

			t.Run("returns nil without a reading of the UTC source", func(t *testing.T) {
				t.Parallel()
				s, err := checkpoint.NewCosignatureV1Signer("a", checkpoint.TypeEd25519Cosignature,
					ed25519Signer(t, "a"), failingUTC{err: errors.New("the UTC source fails")}, time.Second)
				assert.NoError(t, err, "NewCosignatureV1Signer must accept the signer")
				assert.NoError(t, s.CheckText([]byte(cosignedText)), "CheckText must not read the UTC source")
			})

			t.Run("returns ErrKey for the zero CosignatureV1Signer", func(t *testing.T) {
				t.Parallel()
				var zero checkpoint.CosignatureV1Signer
				err := zero.CheckText([]byte(cosignedText))
				expect.ErrorIs(t, err, note.ErrKey, "the zero CosignatureV1Signer must sign nothing")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
			})
		})
	})
}

// TestCosignatureAllocs checks the allocation contract of each function and
// method. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
//
// Each measurement of a path through the pooled buffers of the package
// starts with two collections, which empty the pool. The warm-up call of
// MaxAllocs then grows a buffer, and the measured calls reuse it only when
// the path keeps the growth of the buffer and returns it to the pool.
//
//nolint:paralleltest // see above
func TestCosignatureAllocs(t *testing.T) {
	n := mustParse(t, readFile(t, cosignedFile))
	k := vectorKey(t, vectorW1)
	entry := checkpoint.CosignatureV1(ed25519.Resolve)
	ed := ed25519Signer(t, "a")
	utc := fake.New(clockTime)
	text := []byte(cosignedText)
	s := witness(t, "a")

	v, err := entry(k)
	assert.NoError(t, err, "CosignatureV1 must build the Verifier")
	line, ok := n.Find(k)
	assert.True(t, ok, "the checkpoint must have the line of W1")

	t.Run("CosignatureV1", func(t *testing.T) {
		expect.MaxAllocs(t, func() { _, err = entry(k) }, 2,
			"the entry must allocate the Verifier and the Ed25519 key alone")
		assert.NoError(t, err, "the test must measure a key that the entry accepts")
	})

	t.Run("Verify", func(t *testing.T) {
		var got bool
		runtime.GC()
		runtime.GC()
		expect.MaxAllocs(t, func() { got = v.Verify(n.Text, line.Value) }, 0, "Verify must not allocate")
		assert.True(t, got, "the test must measure a line that verifies")
	})

	t.Run("NewCosignatureV1Signer", func(t *testing.T) {
		expect.MaxAllocs(t, func() {
			_, err = checkpoint.NewCosignatureV1Signer("a", checkpoint.TypeEd25519Cosignature, ed, utc, time.Second)
		}, 1, "NewCosignatureV1Signer must allocate the CosignatureV1Signer alone")
		assert.NoError(t, err, "the test must measure a signer that NewCosignatureV1Signer accepts")
	})

	t.Run("CosignatureV1Signer", func(t *testing.T) {
		t.Run("Reset", func(t *testing.T) {
			var reused checkpoint.CosignatureV1Signer
			expect.MaxAllocs(t, func() {
				err = reused.Reset("a", checkpoint.TypeEd25519Cosignature, ed, utc, time.Second)
			}, 0, "Reset must not allocate")
			assert.NoError(t, err, "the test must measure a signer that Reset accepts")
		})

		t.Run("Sign", func(t *testing.T) {
			runtime.GC()
			runtime.GC()
			expect.MaxAllocs(t, func() { _, err = s.Sign(text) }, 1, "Sign must allocate the value alone")
			assert.NoError(t, err, "the test must measure a text that Sign signs")
		})

		t.Run("SignContext", func(t *testing.T) {
			ctx := t.Context()
			runtime.GC()
			runtime.GC()
			expect.MaxAllocs(t, func() { _, err = sign.SignContext(ctx, s, text) }, 1,
				"SignContext must allocate the value alone")
			assert.NoError(t, err, "the test must measure a text that SignContext signs")
		})

		t.Run("AppendSign", func(t *testing.T) {
			ctx := t.Context()
			buf := make([]byte, 0, timestampBytes+stded25519.SignatureSize)
			runtime.GC()
			runtime.GC()
			expect.MaxAllocs(t, func() { _, err = s.AppendSign(ctx, buf[:0], text) }, 0,
				"AppendSign must not allocate into a buffer with room")
			assert.NoError(t, err, "the test must measure a text that AppendSign signs")
		})

		t.Run("AppendSignAt", func(t *testing.T) {
			ctx := t.Context()
			buf := make([]byte, 0, timestampBytes+stded25519.SignatureSize)
			runtime.GC()
			runtime.GC()
			expect.MaxAllocs(t, func() { _, err = s.AppendSignAt(ctx, buf[:0], text, clockTime) }, 0,
				"AppendSignAt must not allocate into a buffer with room")
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

// BenchmarkCosignature reports the cost of each function and method, and
// fails above the allocations that their contracts state.
func BenchmarkCosignature(b *testing.B) {
	n := mustParse(b, readFile(b, cosignedFile))
	k := vectorKey(b, vectorW1)
	entry := checkpoint.CosignatureV1(ed25519.Resolve)
	ed := ed25519Signer(b, "a")
	utc := fake.New(clockTime)
	text := []byte(cosignedText)
	s := witness(b, "a")

	v, err := entry(k)
	assert.NoError(b, err, "CosignatureV1 must build the Verifier")
	line, ok := n.Find(k)
	assert.True(b, ok, "the checkpoint must have the line of W1")

	b.Run("CosignatureV1", func(b *testing.B) {
		var err error

		c := bench.Start(b).MaxAllocs(2)
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

	b.Run("NewCosignatureV1Signer", func(b *testing.B) {
		var err error

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			_, err = checkpoint.NewCosignatureV1Signer("a", checkpoint.TypeEd25519Cosignature, ed, utc, time.Second)
		}

		assert.NoError(b, err, "the benchmark must measure a signer that NewCosignatureV1Signer accepts")
	})

	b.Run("CosignatureV1Signer", func(b *testing.B) {
		b.Run("Reset", func(b *testing.B) {
			var reused checkpoint.CosignatureV1Signer

			var err error

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				err = reused.Reset("a", checkpoint.TypeEd25519Cosignature, ed, utc, time.Second)
			}

			assert.NoError(b, err, "the benchmark must measure a signer that Reset accepts")
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
				_, err = sign.SignContext(ctx, s, text)
			}

			assert.NoError(b, err, "the benchmark must measure a text that SignContext signs")
		})

		b.Run("AppendSign", func(b *testing.B) {
			ctx := b.Context()
			buf := make([]byte, 0, timestampBytes+stded25519.SignatureSize)

			var err error

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				_, err = s.AppendSign(ctx, buf[:0], text)
			}

			assert.NoError(b, err, "the benchmark must measure a text that AppendSign signs")
		})

		b.Run("AppendSignAt", func(b *testing.B) {
			ctx := b.Context()
			buf := make([]byte, 0, timestampBytes+stded25519.SignatureSize)

			var err error

			c := bench.Start(b).MaxAllocs(0)
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

// retainingWitness returns a cosigner of type 0x04 named "a", over a
// retainingSigner of the Ed25519 key of "a" that keeps each message in
// messages, with the time of a fake clock at clockTime.
func retainingWitness(tb testing.TB, messages *[][]byte) *checkpoint.CosignatureV1Signer {
	tb.Helper()

	s, err := checkpoint.NewCosignatureV1Signer("a", checkpoint.TypeEd25519Cosignature,
		retainingSigner{Signer: ed25519Signer(tb, "a"), messages: messages}, fake.New(clockTime), time.Second)
	assert.NoError(tb, err, "NewCosignatureV1Signer must accept the signer")

	return s
}

// vectorKey returns the verifier key of the witness name of policyFile, or
// of its log for vectorLog.
func vectorKey(tb testing.TB, name string) note.Key {
	tb.Helper()

	for line := range strings.Lines(string(readFile(tb, policyFile))) {
		f := strings.Fields(line)
		if f[0] == "log" && name == vectorLog {
			return mustParseKey(tb, f[1])
		}

		if f[0] == "witness" && f[1] == name {
			return mustParseKey(tb, f[2])
		}
	}

	tb.Fatalf("%s has no key of %s", policyFile, name)

	return note.Key{}
}

// mustParseKey returns the key of vkey, and fails the test when ParseKey
// refuses it.
func mustParseKey(tb testing.TB, vkey string) note.Key {
	tb.Helper()

	k, err := note.ParseKey(vkey)
	assert.NoError(tb, err, "ParseKey must accept "+vkey)

	return k
}

// mustParse returns the note of msg, and fails the test when note.Parse
// refuses it.
func mustParse(tb testing.TB, msg []byte) *note.Note {
	tb.Helper()

	n, err := note.Parse(msg)
	assert.NoError(tb, err, "note.Parse must accept the note")

	return &n
}

// ed25519Signer returns the Ed25519 signer whose seed is SHA-256 of label.
func ed25519Signer(tb testing.TB, label string) *ed25519.Signer {
	tb.Helper()

	seed := sha256.Sum256([]byte(label))

	s, err := ed25519.New(stded25519.NewKeyFromSeed(seed[:]))
	assert.NoError(tb, err, "the seed must give a key")

	return s
}

// mldsaSigner returns the ML-DSA signer of p under context whose seed is
// SHA-256 of label.
func mldsaSigner(tb testing.TB, p mldsa.Params, label, context string) *mldsa.Signer {
	tb.Helper()

	seed := sha256.Sum256([]byte(label))

	s, err := mldsa.New(p, seed[:], context)
	assert.NoError(tb, err, "the seed must give a key")

	return s
}

// witness returns a cosigner of type 0x04 named name, over the Ed25519
// key of name, with the time of a fake clock at clockTime.
func witness(tb testing.TB, name note.Name) *checkpoint.CosignatureV1Signer {
	tb.Helper()

	s, err := checkpoint.NewCosignatureV1Signer(name, checkpoint.TypeEd25519Cosignature,
		ed25519Signer(tb, string(name)), fake.New(clockTime), time.Second)
	assert.NoError(tb, err, "NewCosignatureV1Signer must accept the Ed25519 signer")

	return s
}
