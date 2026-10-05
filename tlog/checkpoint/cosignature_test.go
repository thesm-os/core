// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package checkpoint_test

import (
	"bytes"
	"context"
	stded25519 "crypto/ed25519"
	"crypto/sha256"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.dokimi.dev/assert/bench"
	"go.thesmos.sh/testkit"

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

// cosignedText is the text that the cosigners of the tests sign.
const cosignedText = "example.com/log\n5\n" + exampleRoot + "\n"

// cosignatureHeader is the start of the message of a CosignatureV1
// signature, before the timestamp in decimal, which the tests write from
// tlog-cosignature.
const cosignatureHeader = "cosignature/v1\ntime "

// prefix is the content of dst before an AppendSign of the tests appends
// to it.
const prefix = "prefix:"

// clockTime is the time of the fake clock of the cosigners of the tests.
var clockTime = time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)

// failingUTC is a clock.UTCSource that returns its error.
type failingUTC struct {
	err error
}

// ReadUTC returns the zero reading and the error of f.
func (f failingUTC) ReadUTC() (clock.UTCReading, error) {
	return clock.UTCReading{}, f.err
}

// fakeSigner is a sign.Signer that reports its algorithm and its public
// key, and returns its signature and its error.
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
				testkit.NoError(t, err, "CosignatureV1 must build the Verifier of "+name)
				s, ok := n.Find(k)
				testkit.True(t, ok, "the checkpoint must have the line of "+name)
				testkit.True(t, v.Verify(n.Text, s.Value), "Verify must accept the line of "+name)
			}
		})

		t.Run("returns a Verifier of a cosignature over a checkpoint with an extension line", func(t *testing.T) {
			t.Parallel()
			n := mustParse(t, readFile(t, extensionFile))
			k := vectorKey(t, vectorW1)
			v, err := entry(k)
			testkit.NoError(t, err, "CosignatureV1 must build the Verifier")
			s, ok := n.Find(k)
			testkit.True(t, ok, "the checkpoint must have the line of W1")
			testkit.True(t, v.Verify(n.Text, s.Value), "Verify must accept the line over the extension line")
		})

		t.Run("returns a Verifier of a copy of the key", func(t *testing.T) {
			t.Parallel()
			k := vectorKey(t, vectorW1)
			v, err := entry(k)
			testkit.NoError(t, err, "CosignatureV1 must build the Verifier")
			testkit.Equal(t, v.Key(), k, "Key must return the key")
			testkit.Equal(t, v.KeyID(), note.KeyID(k.Name, k.ID()), "KeyID must derive from the name and the key ID")
			testkit.Equal(t, v.PublicKey(), k.PublicKey, "PublicKey must return the public key")
			testkit.Equal(t, v.Algorithm(), note.Algorithm, "Algorithm must return note.Algorithm")
			clear(k.PublicKey)
			testkit.Equal(t, v.PublicKey(), vectorKey(t, vectorW1).PublicKey,
				"a change to the caller's key must not change the Verifier")
		})

		t.Run("returns a Verifier that reports false for the line over another text", func(t *testing.T) {
			t.Parallel()
			n := mustParse(t, readFile(t, cosignedFile))
			k := vectorKey(t, vectorW1)
			v, err := entry(k)
			testkit.NoError(t, err, "CosignatureV1 must build the Verifier")
			s, _ := n.Find(k)
			testkit.False(t, v.Verify([]byte(cosignedText), s.Value), "Verify must refuse the line over another text")
		})

		t.Run("returns a Verifier that reports false for the line with another timestamp", func(t *testing.T) {
			t.Parallel()
			n := mustParse(t, readFile(t, cosignedFile))
			k := vectorKey(t, vectorW1)
			v, err := entry(k)
			testkit.NoError(t, err, "CosignatureV1 must build the Verifier")
			s, _ := n.Find(k)
			changed := bytes.Clone(s.Value)
			changed[7]++
			testkit.False(t, v.Verify(n.Text, changed), "Verify must refuse the line with another timestamp")
		})

		t.Run("returns a Verifier that reports false for a value of 7 bytes", func(t *testing.T) {
			t.Parallel()
			v, err := entry(vectorKey(t, vectorW1))
			testkit.NoError(t, err, "CosignatureV1 must build the Verifier")
			testkit.False(t, v.Verify([]byte(cosignedText), make([]byte, 7)), "Verify must refuse a value of 7 bytes")
		})

		t.Run("returns ErrKey for a key that is not Valid", func(t *testing.T) {
			t.Parallel()
			v, err := entry(note.Key{Name: "a", Type: checkpoint.TypeEd25519Cosignature})
			testkit.ErrorIs(t, err, note.ErrKey, "CosignatureV1 must refuse a key without a public key")
			testkit.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
			testkit.True(t, v == nil, "CosignatureV1 must return a nil Verifier with an error")
		})

		t.Run("returns the error of resolve", func(t *testing.T) {
			t.Parallel()
			_, err := entry(note.Key{Name: "a", Type: checkpoint.TypeEd25519Cosignature, PublicKey: []byte{1}})
			testkit.ErrorIs(t, err, ed25519.ErrInvalidPublicKeySize, "CosignatureV1 must return the error of resolve")
		})

		t.Run("returns ErrUnknownType when resolve returns neither a Verifier nor an error", func(t *testing.T) {
			t.Parallel()
			none := func([]byte) (sign.Verifier, error) {
				return nil, nil //nolint:nilnil // the case is a resolve that returns neither
			}
			_, err := checkpoint.CosignatureV1(none)(vectorKey(t, vectorW1))
			testkit.ErrorIs(t, err, note.ErrUnknownType, "CosignatureV1 must refuse a resolve without a Verifier")
			testkit.Equal(t, errs.Classify(err), errs.Unsupported, "the error must classify as Unsupported")
		})
	})

	t.Run("NewCosignatureV1Signer", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a Signer whose lines verify under CosignatureV1", func(t *testing.T) {
			t.Parallel()
			s := witness(t, "example.com/w")
			value, err := s.Sign([]byte(cosignedText))
			testkit.NoError(t, err, "Sign must sign the text")
			v, err := checkpoint.CosignatureV1(ed25519.Resolve)(s.Key())
			testkit.NoError(t, err, "CosignatureV1 must build the Verifier of the key")
			testkit.True(t, v.Verify([]byte(cosignedText), value), "the Verifier must accept the line")
			testkit.True(t, s.Verify([]byte(cosignedText), value), "the Signer must accept its own line")
		})

		t.Run("returns a Signer of the key of its name, type and public key", func(t *testing.T) {
			t.Parallel()
			ed := ed25519Signer(t, "example.com/w")
			s, err := checkpoint.NewCosignatureV1Signer("example.com/w", checkpoint.TypeEd25519Cosignature, ed,
				fake.New(clockTime), time.Second)
			testkit.NoError(t, err, "NewCosignatureV1Signer must accept the Ed25519 signer")
			want := note.Key{Name: "example.com/w", Type: checkpoint.TypeEd25519Cosignature, PublicKey: ed.PublicKey()}
			testkit.Equal(t, s.Key(), want, "Key must return the name, the type and the public key")
			testkit.Equal(t, s.KeyID(), note.KeyID(want.Name, want.ID()), "KeyID must derive from the key")
		})

		t.Run("returns a Signer that uses the public key of the signer without a copy", func(t *testing.T) {
			t.Parallel()
			pub := make([]byte, 32)
			s, err := checkpoint.NewCosignatureV1Signer("a", checkpoint.TypeEd25519Cosignature,
				fakeSigner{alg: crypto.AlgEd25519, pub: pub}, fake.New(clockTime), time.Second)
			testkit.NoError(t, err, "NewCosignatureV1Signer must accept the signer")
			testkit.True(t, &s.PublicKey()[0] == &pub[0], "the Signer must use the immutable key of the signer")
			testkit.True(t, &s.Key().PublicKey[0] == &pub[0], "the key of the Signer must use it too")
		})

		t.Run("accepts an ML-DSA signer for a type without an assigned byte", func(t *testing.T) {
			t.Parallel()
			typ, err := note.NewType("example.com/cosignature-ml-dsa-44")
			testkit.NoError(t, err, "NewType must accept the identifier")
			_, err = checkpoint.NewCosignatureV1Signer("a", typ, mldsaSigner(t, mldsa.MLDSA44, "a", ""),
				fake.New(clockTime), time.Second)
			testkit.NoError(t, err, "NewCosignatureV1Signer must accept ML-DSA for a type without an assigned byte")
		})

		t.Run("accepts the error bound 0", func(t *testing.T) {
			t.Parallel()
			s, err := checkpoint.NewCosignatureV1Signer("a", checkpoint.TypeEd25519Cosignature,
				ed25519Signer(t, "a"), fake.New(clockTime), 0)
			testkit.NoError(t, err, "NewCosignatureV1Signer must accept the error bound 0")
			_, err = s.Sign([]byte(cosignedText))
			testkit.NoError(t, err, "Sign must sign with a reading without error")
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
				name: "returns ErrKey for type 0x04 and a signer of another algorithm", giveName: "a",
				typ: checkpoint.TypeEd25519Cosignature, signer: mldsaSigner(t, mldsa.MLDSA44, "a", ""),
				utc: fake.New(clockTime), want: note.ErrKey,
			},
			{
				name: "returns ErrKey for a signer without a public key", giveName: "a", typ: "\x05",
				signer: fakeSigner{alg: crypto.AlgEd25519}, utc: fake.New(clockTime), want: note.ErrKey,
			},
			{
				name:     "returns ErrTimestamp for a nil UTC source",
				giveName: "a",
				typ:      checkpoint.TypeEd25519Cosignature,
				signer:   ed25519Signer(t, "a"),
				utc:      nil,
				want:     checkpoint.ErrTimestamp,
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
				testkit.ErrorIs(t, err, tt.want, "NewCosignatureV1Signer must refuse the signer")
				testkit.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				testkit.True(t, s == nil, "NewCosignatureV1Signer must return a nil Signer with an error")
			})
		}
	})

	t.Run("CosignatureV1Signer.Reset", func(t *testing.T) {
		t.Parallel()

		t.Run("sets a CosignatureV1Signer of the caller to the key of the signer", func(t *testing.T) {
			t.Parallel()
			var s checkpoint.CosignatureV1Signer
			ed := ed25519Signer(t, "example.com/w")
			testkit.NoError(t, s.Reset("example.com/w", checkpoint.TypeEd25519Cosignature, ed, fake.New(clockTime),
				time.Second), "Reset must accept the signer")
			testkit.Equal(t, s.Key(), witness(t, "example.com/w").Key(), "Reset must set the key")
			value, err := s.Sign([]byte(cosignedText))
			testkit.NoError(t, err, "Sign must sign the text")
			testkit.True(t, witness(t, "example.com/w").Verify([]byte(cosignedText), value),
				"the line must verify under the key")
		})

		t.Run("sets a CosignatureV1Signer to another key", func(t *testing.T) {
			t.Parallel()
			s, err := checkpoint.NewCosignatureV1Signer("a", checkpoint.TypeEd25519Cosignature,
				ed25519Signer(t, "a"), fake.New(clockTime), time.Second)
			testkit.NoError(t, err, "NewCosignatureV1Signer must accept the signer")
			testkit.NoError(t, s.Reset("b", checkpoint.TypeEd25519Cosignature, ed25519Signer(t, "b"),
				fake.New(clockTime), time.Second), "Reset must accept the signer")
			testkit.Equal(t, s.Key(), witness(t, "b").Key(), "Reset must set the key of b")
		})

		t.Run("leaves the CosignatureV1Signer unchanged with an error", func(t *testing.T) {
			t.Parallel()
			s, err := checkpoint.NewCosignatureV1Signer("a", checkpoint.TypeEd25519Cosignature,
				ed25519Signer(t, "a"), fake.New(clockTime), time.Second)
			testkit.NoError(t, err, "NewCosignatureV1Signer must accept the signer")
			err = s.Reset("b", checkpoint.TypeEd25519Cosignature, ed25519Signer(t, "b"), nil, time.Second)
			testkit.ErrorIs(t, err, checkpoint.ErrTimestamp, "Reset must refuse a nil UTC source")
			testkit.Equal(t, s.Key(), witness(t, "a").Key(), "Reset must leave the key of a")
		})
	})

	t.Run("Key", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the zero Key for the zero CosignatureV1Signer", func(t *testing.T) {
			t.Parallel()
			var zero checkpoint.CosignatureV1Signer
			testkit.Equal(t, zero.Key(), note.Key{}, "the zero CosignatureV1Signer must have no key")
		})
	})

	t.Run("Verify", func(t *testing.T) {
		t.Parallel()

		t.Run("reports false for the zero CosignatureV1Signer", func(t *testing.T) {
			t.Parallel()
			var zero checkpoint.CosignatureV1Signer
			testkit.False(t, zero.Verify([]byte(cosignedText), make([]byte, 8+64)),
				"the zero CosignatureV1Signer must verify nothing")
		})
	})

	t.Run("Sign", func(t *testing.T) {
		t.Parallel()

		t.Run("returns ErrKey for the zero CosignatureV1Signer", func(t *testing.T) {
			t.Parallel()
			var zero checkpoint.CosignatureV1Signer
			value, err := zero.Sign([]byte(cosignedText))
			testkit.ErrorIs(t, err, note.ErrKey, "the zero CosignatureV1Signer must sign nothing")
			testkit.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
			testkit.True(t, value == nil, "Sign must return a nil value with an error")
		})

		t.Run("returns a value that starts with the time of the UTC source", func(t *testing.T) {
			t.Parallel()
			value, err := witness(t, "a").Sign([]byte(cosignedText))
			testkit.NoError(t, err, "Sign must sign the text")
			got, err := checkpoint.Timestamp(value)
			testkit.NoError(t, err, "Timestamp must read the value")
			testkit.True(t, got.Equal(clockTime), "the value must contain the time of the clock, not "+got.String())
			testkit.Len(t, value, 8+64, "the value must contain the timestamp and the Ed25519 signature")
		})

		t.Run("returns a value that starts with the whole seconds of the reading", func(t *testing.T) {
			t.Parallel()
			s, err := checkpoint.NewCosignatureV1Signer("a", checkpoint.TypeEd25519Cosignature,
				ed25519Signer(t, "a"), fake.New(clockTime.Add(999*time.Millisecond)), time.Second)
			testkit.NoError(t, err, "NewCosignatureV1Signer must accept the signer")
			value, err := s.Sign([]byte(cosignedText))
			testkit.NoError(t, err, "Sign must sign the text")
			got, err := checkpoint.Timestamp(value)
			testkit.NoError(t, err, "Timestamp must read the value")
			testkit.True(t, got.Equal(clockTime), "the value must contain the whole seconds, not "+got.String())
		})

		t.Run("signs with a reading whose error bound equals the bound of the Signer", func(t *testing.T) {
			t.Parallel()
			c := fake.New(clockTime)
			c.SetUTCError(time.Second, true)
			s, err := checkpoint.NewCosignatureV1Signer("a", checkpoint.TypeEd25519Cosignature,
				ed25519Signer(t, "a"), c, time.Second)
			testkit.NoError(t, err, "NewCosignatureV1Signer must accept the signer")
			_, err = s.Sign([]byte(cosignedText))
			testkit.NoError(t, err, "Sign must sign with a reading within the bound")
		})

		t.Run("returns ErrClock with the error of a UTC source that fails", func(t *testing.T) {
			t.Parallel()
			failure := testkit.TestError("the UTC source fails")
			s, err := checkpoint.NewCosignatureV1Signer("a", checkpoint.TypeEd25519Cosignature,
				ed25519Signer(t, "a"), failingUTC{err: failure}, time.Second)
			testkit.NoError(t, err, "NewCosignatureV1Signer must accept the signer")
			_, err = s.Sign([]byte(cosignedText))
			testkit.ErrorIs(t, err, checkpoint.ErrClock, "Sign must refuse to sign without a reading")
			testkit.ErrorIs(t, err, failure, "Sign must return the error of the UTC source")
			testkit.Equal(t, errs.Classify(err), errs.Transient, "the error must classify as Transient")
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
				testkit.NoError(t, err, "NewCosignatureV1Signer must accept the signer")
				value, err := s.Sign([]byte(cosignedText))
				testkit.ErrorIs(t, err, checkpoint.ErrClock, "Sign must refuse to sign with the reading")
				testkit.Equal(t, errs.Classify(err), errs.Transient, "the error must classify as Transient")
				testkit.True(t, value == nil, "Sign must return a nil value with an error")
			})
		}

		t.Run("returns the error of the signer", func(t *testing.T) {
			t.Parallel()
			failure := testkit.TestError("the signer fails")
			s, err := checkpoint.NewCosignatureV1Signer(
				"a",
				checkpoint.TypeEd25519Cosignature,
				fakeSigner{
					alg: crypto.AlgEd25519,
					pub: make([]byte, 32),
					err: failure,
				},
				fake.New(clockTime),
				time.Second,
			)
			testkit.NoError(t, err, "NewCosignatureV1Signer must accept the signer")
			_, err = s.Sign([]byte(cosignedText))
			testkit.ErrorIs(t, err, failure, "Sign must return the error of the signer")
		})
	})

	t.Run("SignContext", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a value that verifies under a live context", func(t *testing.T) {
			t.Parallel()
			s := witness(t, "a")
			value, err := sign.SignContext(t.Context(), s, []byte(cosignedText))
			testkit.NoError(t, err, "SignContext must sign under a live context")
			testkit.True(t, s.Verify([]byte(cosignedText), value), "the value must verify")
		})

		t.Run("returns the cause of a context that ended", func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			_, err := sign.SignContext(ctx, witness(t, "a"), []byte(cosignedText))
			testkit.ErrorIs(t, err, context.Canceled, "SignContext must return the cause of the context")
		})
	})

	t.Run("AppendSign", func(t *testing.T) {
		t.Parallel()

		t.Run("appends a value that verifies under CosignatureV1", func(t *testing.T) {
			t.Parallel()
			s := witness(t, "a")
			got, err := s.AppendSign(t.Context(), []byte(prefix), []byte(cosignedText))
			testkit.NoError(t, err, "AppendSign must sign the text")
			testkit.Equal(t, string(got[:len(prefix)]), prefix, "AppendSign must keep dst")
			testkit.True(t, s.Verify([]byte(cosignedText), got[len(prefix):]), "the appended value must verify")
		})

		t.Run("appends the value of a signer that is not an AppendSigner", func(t *testing.T) {
			t.Parallel()
			var messages [][]byte
			s := retainingWitness(t, &messages)
			got, err := s.AppendSign(t.Context(), []byte(prefix), []byte(cosignedText))
			testkit.NoError(t, err, "AppendSign must sign the text")
			testkit.Equal(t, string(got[:len(prefix)]), prefix, "AppendSign must keep dst")
			testkit.True(t, s.Verify([]byte(cosignedText), got[len(prefix):]), "the appended value must verify")
		})

		t.Run("builds each message of a signer that is not an AppendSigner in a buffer of its own", func(t *testing.T) {
			t.Parallel()
			var messages [][]byte
			s := retainingWitness(t, &messages)
			for _, text := range []string{cosignedText, "another text\n"} {
				_, err := s.AppendSign(t.Context(), nil, []byte(text))
				testkit.NoError(t, err, "AppendSign must sign the text")
			}
			want := cosignatureHeader + strconv.FormatInt(clockTime.Unix(), 10) + "\n" + cosignedText
			testkit.Equal(t, string(messages[0]), want, "a later call must not change a message that the signer kept")
		})

		t.Run("returns dst unchanged with ErrClock", func(t *testing.T) {
			t.Parallel()
			s, err := checkpoint.NewCosignatureV1Signer("a", checkpoint.TypeEd25519Cosignature,
				ed25519Signer(t, "a"), failingUTC{err: testkit.TestError("the UTC source fails")}, time.Second)
			testkit.NoError(t, err, "NewCosignatureV1Signer must accept the signer")
			got, err := s.AppendSign(t.Context(), []byte(prefix), []byte(cosignedText))
			testkit.ErrorIs(t, err, checkpoint.ErrClock, "AppendSign must refuse to sign without a reading")
			testkit.Equal(t, string(got), prefix, "AppendSign must return dst unchanged")
		})

		t.Run("returns dst unchanged with the error of a signer that is not an AppendSigner", func(t *testing.T) {
			t.Parallel()
			failure := testkit.TestError("the signer fails")
			s, err := checkpoint.NewCosignatureV1Signer(
				"a",
				checkpoint.TypeEd25519Cosignature,
				fakeSigner{
					alg: crypto.AlgEd25519,
					pub: make([]byte, 32),
					err: failure,
				},
				fake.New(clockTime),
				time.Second,
			)
			testkit.NoError(t, err, "NewCosignatureV1Signer must accept the signer")
			got, err := s.AppendSign(t.Context(), []byte(prefix), []byte(cosignedText))
			testkit.ErrorIs(t, err, failure, "AppendSign must return the error of the signer")
			testkit.Equal(t, string(got), prefix, "AppendSign must return dst unchanged")
		})

		t.Run("returns dst unchanged with the cause of a context that ended", func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			got, err := witness(t, "a").AppendSign(ctx, []byte(prefix), []byte(cosignedText))
			testkit.ErrorIs(t, err, context.Canceled, "AppendSign must return the cause of the context")
			testkit.Equal(t, string(got), prefix, "AppendSign must return dst unchanged")
		})

		t.Run("returns dst unchanged and ErrKey for the zero CosignatureV1Signer", func(t *testing.T) {
			t.Parallel()
			var zero checkpoint.CosignatureV1Signer
			got, err := zero.AppendSign(t.Context(), []byte(prefix), []byte(cosignedText))
			testkit.ErrorIs(t, err, note.ErrKey, "the zero CosignatureV1Signer must sign nothing")
			testkit.Equal(t, string(got), prefix, "AppendSign must return dst unchanged")
		})
	})

	t.Run("AppendSignAt", func(t *testing.T) {
		t.Parallel()

		at := clockTime.Add(time.Hour)

		t.Run("appends a value that verifies under CosignatureV1", func(t *testing.T) {
			t.Parallel()
			s := witness(t, "a").(checkpoint.Cosigner)
			got, err := s.AppendSignAt(t.Context(), []byte(prefix), []byte(cosignedText), at)
			testkit.NoError(t, err, "AppendSignAt must sign the text")
			testkit.Equal(t, string(got[:len(prefix)]), prefix, "AppendSignAt must keep dst")
			testkit.True(t, s.Verify([]byte(cosignedText), got[len(prefix):]), "the appended value must verify")
		})

		t.Run("appends the whole seconds of the time of the caller", func(t *testing.T) {
			t.Parallel()
			s := witness(t, "a").(checkpoint.Cosigner)
			got, err := s.AppendSignAt(t.Context(), nil, []byte(cosignedText), at.Add(999*time.Millisecond))
			testkit.NoError(t, err, "AppendSignAt must sign the text")
			stamp, err := checkpoint.Timestamp(got)
			testkit.NoError(t, err, "Timestamp must read the value")
			testkit.True(t, stamp.Equal(at),
				"the value must contain the whole seconds of the time, not "+stamp.String())
		})

		t.Run("signs without a reading of the UTC source", func(t *testing.T) {
			t.Parallel()
			s, err := checkpoint.NewCosignatureV1Signer("a", checkpoint.TypeEd25519Cosignature,
				ed25519Signer(t, "a"), failingUTC{err: testkit.TestError("the UTC source fails")}, time.Second)
			testkit.NoError(t, err, "NewCosignatureV1Signer must accept the signer")
			got, err := s.AppendSignAt(t.Context(), nil, []byte(cosignedText), at)
			testkit.NoError(t, err, "AppendSignAt must sign without a reading")
			testkit.True(t, s.Verify([]byte(cosignedText), got), "the value must verify")
		})

		t.Run("signs at the first second after the Unix epoch", func(t *testing.T) {
			t.Parallel()
			first := time.Unix(1, 0)
			s := witness(t, "a").(checkpoint.Cosigner)
			got, err := s.AppendSignAt(t.Context(), nil, []byte(cosignedText), first)
			testkit.NoError(t, err, "AppendSignAt must sign at the timestamp 1")
			stamp, err := checkpoint.Timestamp(got)
			testkit.NoError(t, err, "Timestamp must read the value")
			testkit.True(t, stamp.Equal(first), "the value must contain the timestamp 1, not "+stamp.String())
		})

		t.Run("builds the message of a signer that is not an AppendSigner in a buffer of its own", func(t *testing.T) {
			t.Parallel()
			var messages [][]byte
			s := retainingWitness(t, &messages).(checkpoint.Cosigner)
			got, err := s.AppendSignAt(t.Context(), nil, []byte(cosignedText), at)
			testkit.NoError(t, err, "AppendSignAt must sign the text")
			testkit.True(t, s.Verify([]byte(cosignedText), got), "the value must verify")
			want := cosignatureHeader + strconv.FormatInt(at.Unix(), 10) + "\n" + cosignedText
			testkit.Equal(t, string(messages[0]), want, "the signer must receive the message of the time")
		})

		timeTests := []struct {
			give time.Time
			name string
		}{
			{
				name: "returns dst unchanged and ErrTimestamp for a time in the first second of the Unix epoch",
				give: time.Unix(0, 999_999_999),
			},
			{name: "returns dst unchanged and ErrTimestamp for a time before the Unix epoch", give: time.Unix(-1, 0)},
			{name: "returns dst unchanged and ErrTimestamp for the zero Time", give: time.Time{}},
		}
		for _, tt := range timeTests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				s := witness(t, "a").(checkpoint.Cosigner)
				got, err := s.AppendSignAt(t.Context(), []byte(prefix), []byte(cosignedText), tt.give)
				testkit.ErrorIs(t, err, checkpoint.ErrTimestamp,
					"AppendSignAt must refuse a timestamp that is not positive")
				testkit.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				testkit.Equal(t, string(got), prefix, "AppendSignAt must return dst unchanged")
			})
		}

		t.Run("returns dst unchanged with the error of the signer", func(t *testing.T) {
			t.Parallel()
			failure := testkit.TestError("the signer fails")
			signer := fakeSigner{alg: crypto.AlgEd25519, pub: make([]byte, 32), err: failure}
			s, err := checkpoint.NewCosignatureV1Signer("a", checkpoint.TypeEd25519Cosignature, signer,
				fake.New(clockTime), time.Second)
			testkit.NoError(t, err, "NewCosignatureV1Signer must accept the signer")
			got, err := s.AppendSignAt(t.Context(), []byte(prefix), []byte(cosignedText), at)
			testkit.ErrorIs(t, err, failure, "AppendSignAt must return the error of the signer")
			testkit.Equal(t, string(got), prefix, "AppendSignAt must return dst unchanged")
		})

		t.Run("returns dst unchanged with the cause of a context that ended", func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			s := witness(t, "a").(checkpoint.Cosigner)
			got, err := s.AppendSignAt(ctx, []byte(prefix), []byte(cosignedText), at)
			testkit.ErrorIs(t, err, context.Canceled, "AppendSignAt must return the cause of the context")
			testkit.Equal(t, string(got), prefix, "AppendSignAt must return dst unchanged")
		})

		t.Run("returns dst unchanged and ErrKey for the zero CosignatureV1Signer", func(t *testing.T) {
			t.Parallel()
			var zero checkpoint.CosignatureV1Signer
			got, err := zero.AppendSignAt(t.Context(), []byte(prefix), []byte(cosignedText), at)
			testkit.ErrorIs(t, err, note.ErrKey, "the zero CosignatureV1Signer must sign nothing")
			testkit.Equal(t, string(got), prefix, "AppendSignAt must return dst unchanged")
		})
	})

	t.Run("CheckText", func(t *testing.T) {
		t.Parallel()

		t.Run("returns nil for a text that is not a checkpoint", func(t *testing.T) {
			t.Parallel()
			testkit.NoError(t, witness(t, "a").(checkpoint.Cosigner).CheckText([]byte("another text\n")),
				"CheckText must accept every text of CosignatureV1")
		})

		t.Run("returns nil without a reading of the UTC source", func(t *testing.T) {
			t.Parallel()
			s, err := checkpoint.NewCosignatureV1Signer("a", checkpoint.TypeEd25519Cosignature,
				ed25519Signer(t, "a"), failingUTC{err: testkit.TestError("the UTC source fails")}, time.Second)
			testkit.NoError(t, err, "NewCosignatureV1Signer must accept the signer")
			testkit.NoError(t, s.CheckText([]byte(cosignedText)), "CheckText must not read the UTC source")
		})

		t.Run("returns ErrKey for the zero CosignatureV1Signer", func(t *testing.T) {
			t.Parallel()
			var zero checkpoint.CosignatureV1Signer
			err := zero.CheckText([]byte(cosignedText))
			testkit.ErrorIs(t, err, note.ErrKey, "the zero CosignatureV1Signer must sign nothing")
			testkit.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
		})
	})
}

func BenchmarkCosignature(b *testing.B) {
	n := mustParse(b, readFile(b, cosignedFile))
	k := vectorKey(b, vectorW1)
	entry := checkpoint.CosignatureV1(ed25519.Resolve)
	ed := ed25519Signer(b, "a")
	utc := fake.New(clockTime)
	text := []byte(cosignedText)

	v, err := entry(k)
	testkit.NoError(b, err, "CosignatureV1 must build the Verifier")

	line, _ := n.Find(k)
	s := witness(b, "a")

	b.Run("CosignatureV1", func(b *testing.B) {
		benchAllocs(b, 2, func() { sinkVerifier, errSink = entry(k) })
	})

	b.Run("Verify", func(b *testing.B) {
		benchZeroAlloc(b, func() { sinkBool = v.Verify(n.Text, line.Value) })
	})

	b.Run("NewCosignatureV1Signer", func(b *testing.B) {
		benchAllocs(b, 1, func() {
			sinkSigner, errSink = checkpoint.NewCosignatureV1Signer("a", checkpoint.TypeEd25519Cosignature, ed, utc,
				time.Second)
		})
	})

	b.Run("CosignatureV1Signer.Reset", func(b *testing.B) {
		var reused checkpoint.CosignatureV1Signer
		benchZeroAlloc(b, func() {
			errSink = reused.Reset("a", checkpoint.TypeEd25519Cosignature, ed, utc, time.Second)
		})
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
		buf := make([]byte, 0, 8+64)
		benchZeroAlloc(b, func() { sinkBytes, errSink = s.AppendSign(ctx, buf[:0], text) })
	})

	b.Run("AppendSignAt", func(b *testing.B) {
		ctx := b.Context()
		cs := s.(checkpoint.Cosigner)
		buf := make([]byte, 0, 8+64)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		var (
			got []byte
			err error
		)

		for c.Loop() {
			got, err = cs.AppendSignAt(ctx, buf[:0], text, clockTime)
		}

		testkit.NoError(b, err, "AppendSignAt must sign the text")
		testkit.True(b, cs.Verify(text, got), "the benchmark must measure a value that verifies")
	})

	b.Run("CheckText", func(b *testing.B) {
		cs := s.(checkpoint.Cosigner)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		var err error
		for c.Loop() {
			err = cs.CheckText(text)
		}

		testkit.NoError(b, err, "the benchmark must measure a text that CheckText accepts")
	})
}

// retainingWitness returns a cosigner of type 0x04 named "a", over a
// retainingSigner of the Ed25519 key of "a" that keeps each message in
// messages, with the time of a fake clock at clockTime.
func retainingWitness(tb testing.TB, messages *[][]byte) note.Signer {
	tb.Helper()

	s, err := checkpoint.NewCosignatureV1Signer("a", checkpoint.TypeEd25519Cosignature,
		retainingSigner{Signer: ed25519Signer(tb, "a"), messages: messages}, fake.New(clockTime), time.Second)
	testkit.NoError(tb, err, "NewCosignatureV1Signer must accept the signer")

	return s
}

// resolver returns the note.Resolver of the three assigned types: type 0x01
// over Ed25519, and the two cosignature types of tlog-cosignature.
func resolver() note.Resolver {
	return note.Resolver{
		note.TypeEd25519:                  note.Text(ed25519.Resolve),
		checkpoint.TypeEd25519Cosignature: checkpoint.CosignatureV1(ed25519.Resolve),
		checkpoint.TypeMLDSA44Cosignature: checkpoint.SubtreeV1(mldsa.Resolver(mldsa.MLDSA44, "")),
	}
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
	testkit.NoError(tb, err, "ParseKey must accept "+vkey)

	return k
}

// mustParse returns the note of msg, and fails the test when note.Parse
// refuses it.
func mustParse(tb testing.TB, msg []byte) *note.Note {
	tb.Helper()

	n, err := note.Parse(msg)
	testkit.NoError(tb, err, "note.Parse must accept the note")

	return &n
}

// ed25519Signer returns the Ed25519 signer whose seed is SHA-256 of label.
func ed25519Signer(tb testing.TB, label string) *ed25519.Signer {
	tb.Helper()

	seed := sha256.Sum256([]byte(label))

	s, err := ed25519.New(stded25519.NewKeyFromSeed(seed[:]))
	testkit.NoError(tb, err, "the seed must give a key")

	return s
}

// mldsaSigner returns the ML-DSA signer of p under context whose seed is
// SHA-256 of label.
func mldsaSigner(tb testing.TB, p mldsa.Params, label, context string) *mldsa.Signer {
	tb.Helper()

	seed := sha256.Sum256([]byte(label))

	s, err := mldsa.New(p, seed[:], context)
	testkit.NoError(tb, err, "the seed must give a key")

	return s
}

// witness returns a cosigner of type 0x04 named name, over the Ed25519
// key of name, with the time of a fake clock at clockTime.
func witness(tb testing.TB, name note.Name) note.Signer {
	tb.Helper()

	s, err := checkpoint.NewCosignatureV1Signer(name, checkpoint.TypeEd25519Cosignature,
		ed25519Signer(tb, string(name)), fake.New(clockTime), time.Second)
	testkit.NoError(tb, err, "NewCosignatureV1Signer must accept the Ed25519 signer")

	return s
}
