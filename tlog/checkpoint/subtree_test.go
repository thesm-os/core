// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package checkpoint_test

import (
	stdmldsa "crypto/mldsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"math"
	"strings"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

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

// subtreeLabel is the label of a cosigned_message, which the tests write
// from tlog-cosignature.
const subtreeLabel = "subtree/v1\n\x00"

// pqName is the key name of the ML-DSA-44 cosigner of the tests.
const pqName = "example.com/pq"

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

// opaqueSigner is a decorator without Unwrap, which hides the context of
// the signer that it wraps.
type opaqueSigner struct {
	sign.Signer
}

func TestSubtree(t *testing.T) {
	t.Parallel()

	entry := checkpoint.SubtreeV1(mldsa.Resolver(mldsa.MLDSA44, ""))

	t.Run("SubtreeV1", func(t *testing.T) {
		t.Parallel()

		t.Run(
			"returns a Verifier of the log signature of the litewitness test of torchwood v0.10.0",
			func(t *testing.T) {
				t.Parallel()
				k := mustParseKey(t, strings.TrimSuffix(string(readFile(t, litewitnessKeyFile)), "\n"))
				n := mustParse(t, readFile(t, litewitnessNoteFile))
				v, err := entry(k)
				testkit.NoError(t, err, "SubtreeV1 must build the Verifier of the log")
				s, ok := n.Find(k)
				testkit.True(t, ok, "the checkpoint must have the line of the log")
				testkit.True(t, v.Verify(n.Text, s.Value), "Verify must accept the log signature")
				stamp, err := checkpoint.Timestamp(s.Value)
				testkit.NoError(t, err, "Timestamp must read the value")
				testkit.True(t, stamp.IsZero(), "the log signs with the timestamp 0")
			},
		)

		t.Run("returns a Verifier of the cosignatures that torchwood v0.10.0 made", func(t *testing.T) {
			t.Parallel()
			k := vectorKey(t, vectorPQ)
			v, err := entry(k)
			testkit.NoError(t, err, "SubtreeV1 must build the Verifier of PQ")
			for _, file := range []string{cosignedFile, emptyFile} {
				n := mustParse(t, readFile(t, file))
				s, ok := n.Find(k)
				testkit.True(t, ok, file+" must have the line of PQ")
				testkit.True(t, v.Verify(n.Text, s.Value), "Verify must accept the line of PQ in "+file)
			}
		})

		t.Run("returns a Verifier of a signature over the message that tlog-cosignature specifies", func(t *testing.T) {
			t.Parallel()
			v, err := entry(pqKey(t, pqName))
			testkit.NoError(t, err, "SubtreeV1 must build the Verifier")
			value := subtreeValue(t, pqName, exampleTimestamp, "example.com/log", 5, exampleDigest(t).Bytes())
			testkit.True(
				t,
				v.Verify([]byte(cosignedText), value),
				"Verify must accept the message of the specification",
			)
		})

		t.Run("returns a Verifier that reports true for an origin and a key name of 255 bytes", func(t *testing.T) {
			t.Parallel()
			name, origin := strings.Repeat("n", 255), strings.Repeat("o", 255)
			v, err := entry(pqKey(t, note.Name(name)))
			testkit.NoError(t, err, "SubtreeV1 must build the Verifier")
			value := subtreeValue(t, name, 1, origin, 5, exampleDigest(t).Bytes())
			testkit.True(t, v.Verify([]byte(origin+"\n5\n"+exampleRoot+"\n"), value),
				"Verify must accept an origin and a key name of 255 bytes")
		})

		t.Run("returns a Verifier that reports false for a body with extension lines", func(t *testing.T) {
			t.Parallel()
			v, err := entry(pqKey(t, pqName))
			testkit.NoError(t, err, "SubtreeV1 must build the Verifier")
			value := subtreeValue(t, pqName, 1, "example.com/log", 5, exampleDigest(t).Bytes())
			testkit.True(t, v.Verify([]byte(cosignedText), value), "Verify must accept the first three lines")
			testkit.False(t, v.Verify([]byte(cosignedText+"ext\n"), value),
				"Verify must refuse the signature of the three lines over a body with an extension line")
		})

		t.Run("returns a Verifier that reports false for an empty tree whose root is not SHA-256 of the empty string",
			func(t *testing.T) {
				t.Parallel()
				v, err := entry(pqKey(t, pqName))
				testkit.NoError(t, err, "SubtreeV1 must build the Verifier")
				empty := sha256.Sum256(nil)
				valid := subtreeValue(t, pqName, 1, "a", 0, empty[:])
				testkit.True(t, v.Verify([]byte("a\n0\n"+base64.StdEncoding.EncodeToString(empty[:])+"\n"), valid),
					"Verify must accept the root of the empty tree")
				other := subtreeValue(t, pqName, 1, "a", 0, exampleDigest(t).Bytes())
				testkit.False(t, v.Verify([]byte("a\n0\n"+exampleRoot+"\n"), other),
					"Verify must refuse another root for the empty tree")
			})

		t.Run("returns a Verifier that reports false for a text that is not a body", func(t *testing.T) {
			t.Parallel()
			v, err := entry(pqKey(t, pqName))
			testkit.NoError(t, err, "SubtreeV1 must build the Verifier")
			value := subtreeValue(t, pqName, 1, "example.com/log", 5, exampleDigest(t).Bytes())
			testkit.False(t, v.Verify([]byte("example.com/log\n5\n"), value), "Verify must refuse a text of two lines")
		})

		t.Run("returns a Verifier that reports false for a root of 48 bytes", func(t *testing.T) {
			t.Parallel()
			v, err := entry(pqKey(t, pqName))
			testkit.NoError(t, err, "SubtreeV1 must build the Verifier")
			value := subtreeValue(t, pqName, 1, "a", 5, root(t, 48).Bytes()[:32])
			testkit.False(t, v.Verify([]byte("a\n5\n"+encode(root(t, 48))+"\n"), value),
				"Verify must refuse a root of 48 bytes")
		})

		t.Run("returns a Verifier that reports false for an origin of 256 bytes", func(t *testing.T) {
			t.Parallel()
			v, err := entry(pqKey(t, pqName))
			testkit.NoError(t, err, "SubtreeV1 must build the Verifier")
			origin := strings.Repeat("o", 256)
			value := subtreeValue(t, pqName, 1, origin[:255], 5, exampleDigest(t).Bytes())
			testkit.False(t, v.Verify([]byte(origin+"\n5\n"+exampleRoot+"\n"), value),
				"Verify must refuse an origin of 256 bytes")
		})

		t.Run("returns a Verifier that reports false for a key name of 256 bytes", func(t *testing.T) {
			t.Parallel()
			name := strings.Repeat("n", 256)
			v, err := entry(pqKey(t, note.Name(name)))
			testkit.NoError(t, err, "SubtreeV1 must build the Verifier")
			value := subtreeValue(t, name[:255], 1, "example.com/log", 5, exampleDigest(t).Bytes())
			testkit.False(t, v.Verify([]byte(cosignedText), value), "Verify must refuse a key name of 256 bytes")
		})

		t.Run("returns a Verifier that reports false for a timestamp above 2^63 − 1", func(t *testing.T) {
			t.Parallel()
			v, err := entry(pqKey(t, pqName))
			testkit.NoError(t, err, "SubtreeV1 must build the Verifier")
			value := subtreeValue(t, pqName, math.MaxInt64+1, "example.com/log", 5, exampleDigest(t).Bytes())
			testkit.False(t, v.Verify([]byte(cosignedText), value), "Verify must refuse the timestamp 2^63")
		})
	})

	t.Run("NewSubtreeV1Signer", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a Signer whose lines verify under SubtreeV1", func(t *testing.T) {
			t.Parallel()
			s := pqSigner(t, fake.New(clockTime))
			value, err := s.Sign([]byte(cosignedText))
			testkit.NoError(t, err, "Sign must sign the checkpoint")
			v, err := entry(s.Key())
			testkit.NoError(t, err, "SubtreeV1 must build the Verifier of the key")
			testkit.True(t, v.Verify([]byte(cosignedText), value), "the Verifier must accept the line")
			stamp, err := checkpoint.Timestamp(value)
			testkit.NoError(t, err, "Timestamp must read the value")
			testkit.True(t, stamp.Equal(clockTime), "the value must hold the time of the clock, not "+stamp.String())
		})

		t.Run("returns a Signer that signs the message that tlog-cosignature specifies", func(t *testing.T) {
			t.Parallel()
			s := pqSigner(t, fake.New(clockTime))
			value, err := s.Sign([]byte(cosignedText))
			testkit.NoError(t, err, "Sign must sign the checkpoint")
			stamp := uint64(clockTime.Unix())
			msg := cosignedMessage(pqName, stamp, "example.com/log", 5, exampleDigest(t).Bytes())
			testkit.True(t, mldsaSigner(t, mldsa.MLDSA44, pqName, "").Verify(msg, value[8:]),
				"the signature must cover the message of the specification")
		})

		t.Run("returns a Signer that writes the timestamp 0 without a UTC source", func(t *testing.T) {
			t.Parallel()
			value, err := pqSigner(t, nil).Sign([]byte(cosignedText))
			testkit.NoError(t, err, "Sign must sign without a UTC source")
			stamp, err := checkpoint.Timestamp(value)
			testkit.NoError(t, err, "Timestamp must read the value")
			testkit.True(t, stamp.IsZero(), "a Signer without a UTC source writes the timestamp 0")
		})

		t.Run("accepts a name of 255 bytes", func(t *testing.T) {
			t.Parallel()
			_, err := checkpoint.NewSubtreeV1Signer(note.Name(strings.Repeat("n", 255)),
				checkpoint.TypeMLDSA44Cosignature, mldsaSigner(t, mldsa.MLDSA44, pqName, ""), nil, 0)
			testkit.NoError(t, err, "NewSubtreeV1Signer must accept a name of 255 bytes")
		})

		t.Run("accepts an ML-DSA-44 signer with a context for a type without an assigned byte", func(t *testing.T) {
			t.Parallel()
			typ, err := note.NewType("example.com/subtree-ml-dsa-44")
			testkit.NoError(t, err, "NewType must accept the identifier")
			_, err = checkpoint.NewSubtreeV1Signer(pqName, typ, mldsaSigner(t, mldsa.MLDSA44, pqName, "x"), nil, 0)
			testkit.NoError(t, err, "NewSubtreeV1Signer must accept a context for a type without an assigned byte")
		})

		t.Run("accepts a signer of type 0x06 that does not report its context", func(t *testing.T) {
			t.Parallel()
			_, err := checkpoint.NewSubtreeV1Signer(pqName, checkpoint.TypeMLDSA44Cosignature,
				opaqueSigner{mldsaSigner(t, mldsa.MLDSA44, pqName, "x")}, nil, 0)
			testkit.NoError(t, err, "NewSubtreeV1Signer must accept a signer that does not report its context")
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
				name: "returns ErrKey for type 0x06 and an Ed25519 signer", giveName: pqName,
				typ: checkpoint.TypeMLDSA44Cosignature, signer: ed25519Signer(t, pqName), want: note.ErrKey,
			},
			{
				name: "returns ErrKey for type 0x06 and an ML-DSA-65 signer", giveName: pqName,
				typ: checkpoint.TypeMLDSA44Cosignature, signer: mldsaSigner(t, mldsa.MLDSA65, pqName, ""),
				want: note.ErrKey,
			},
			{
				name: "returns ErrKey for type 0x06 and a signer with a context", giveName: pqName,
				typ: checkpoint.TypeMLDSA44Cosignature, signer: mldsaSigner(t, mldsa.MLDSA44, pqName, "x"),
				want: note.ErrKey,
			},
			{
				name:     "returns ErrKey for type 0x06 and a context behind Unwrap of a Signer",
				giveName: pqName,
				typ:      checkpoint.TypeMLDSA44Cosignature,
				signer:   unwrapSigner{mldsaSigner(t, mldsa.MLDSA44, pqName, "x")},
				want:     note.ErrKey,
			},
			{
				name: "returns ErrKey for type 0x06 and a context behind Unwrap of a Verifier", giveName: pqName,
				typ:    checkpoint.TypeMLDSA44Cosignature,
				signer: unwrapVerifier{mldsaSigner(t, mldsa.MLDSA44, pqName, "x")}, want: note.ErrKey,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				s, err := checkpoint.NewSubtreeV1Signer(tt.giveName, tt.typ, tt.signer, nil, tt.maxError)
				testkit.ErrorIs(t, err, tt.want, "NewSubtreeV1Signer must refuse the signer")
				testkit.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				testkit.True(t, s == nil, "NewSubtreeV1Signer must return a nil Signer with an error")
			})
		}
	})

	t.Run("SubtreeV1Signer.Reset", func(t *testing.T) {
		t.Parallel()

		t.Run("sets a SubtreeV1Signer of the caller to the key of the signer", func(t *testing.T) {
			t.Parallel()
			var s checkpoint.SubtreeV1Signer
			inner := mldsaSigner(t, mldsa.MLDSA44, pqName, "")
			testkit.NoError(t, s.Reset(pqName, checkpoint.TypeMLDSA44Cosignature, inner, nil, 0),
				"Reset must accept the ML-DSA-44 signer")
			testkit.Equal(t, s.Key(), pqKey(t, pqName), "Reset must set the key")
			value, err := s.Sign([]byte(cosignedText))
			testkit.NoError(t, err, "Sign must sign the checkpoint")
			v, err := entry(pqKey(t, pqName))
			testkit.NoError(t, err, "SubtreeV1 must build the Verifier of the key")
			testkit.True(t, v.Verify([]byte(cosignedText), value), "the Verifier must accept the line")
		})

		t.Run("leaves the SubtreeV1Signer unchanged with an error", func(t *testing.T) {
			t.Parallel()
			s, err := checkpoint.NewSubtreeV1Signer(pqName, checkpoint.TypeMLDSA44Cosignature,
				mldsaSigner(t, mldsa.MLDSA44, pqName, ""), nil, 0)
			testkit.NoError(t, err, "NewSubtreeV1Signer must accept the signer")
			err = s.Reset(pqName, checkpoint.TypeMLDSA44Cosignature, mldsaSigner(t, mldsa.MLDSA44, pqName, "x"), nil, 0)
			testkit.ErrorIs(t, err, note.ErrKey, "Reset must refuse a signer with a context")
			testkit.Equal(t, s.Key(), pqKey(t, pqName), "Reset must leave the key")
		})
	})

	t.Run("Sign", func(t *testing.T) {
		t.Parallel()

		t.Run("returns ErrKey for the zero SubtreeV1Signer", func(t *testing.T) {
			t.Parallel()
			var zero checkpoint.SubtreeV1Signer
			value, err := zero.Sign([]byte(cosignedText))
			testkit.ErrorIs(t, err, note.ErrKey, "the zero SubtreeV1Signer must sign nothing")
			testkit.True(t, value == nil, "Sign must return a nil value with an error")
		})

		tests := []struct {
			name string
			give string
		}{
			{name: "returns ErrBody for a text that is not a body", give: "example.com/log\n5\n"},
			{name: "returns ErrBody for a body with extension lines", give: cosignedText + "ext\n"},
			{name: "returns ErrBody for a root of 48 bytes", give: "a\n5\n" + encode(root(t, 48)) + "\n"},
			{
				name: "returns ErrBody for an origin of 256 bytes",
				give: strings.Repeat("o", 256) + "\n5\n" + exampleRoot + "\n",
			},
			{
				name: "returns ErrBody for an empty tree whose root is not SHA-256 of the empty string",
				give: "a\n0\n" + exampleRoot + "\n",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				value, err := pqSigner(t, nil).Sign([]byte(tt.give))
				testkit.ErrorIs(t, err, checkpoint.ErrBody, "Sign must refuse a body that SubtreeV1 cannot cover")
				testkit.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				testkit.True(t, value == nil, "Sign must return a nil value with an error")
			})
		}

		t.Run("returns ErrBody from a signer that is not an AppendSigner", func(t *testing.T) {
			t.Parallel()
			s, err := checkpoint.NewSubtreeV1Signer(pqName, checkpoint.TypeMLDSA44Cosignature,
				opaqueSigner{mldsaSigner(t, mldsa.MLDSA44, pqName, "")}, nil, 0)
			testkit.NoError(t, err, "NewSubtreeV1Signer must accept the signer")
			value, err := s.Sign([]byte(cosignedText + "ext\n"))
			testkit.ErrorIs(t, err, checkpoint.ErrBody, "Sign must refuse a body that SubtreeV1 cannot cover")
			testkit.True(t, value == nil, "Sign must return a nil value with an error")
		})
	})

	t.Run("AppendSign", func(t *testing.T) {
		t.Parallel()

		t.Run("appends a value that verifies under SubtreeV1", func(t *testing.T) {
			t.Parallel()
			s := pqSigner(t, fake.New(clockTime))
			got, err := s.AppendSign(t.Context(), []byte(prefix), []byte(cosignedText))
			testkit.NoError(t, err, "AppendSign must sign the checkpoint")
			testkit.Equal(t, string(got[:len(prefix)]), prefix, "AppendSign must keep dst")
			testkit.True(t, s.Verify([]byte(cosignedText), got[len(prefix):]), "the appended value must verify")
		})

		t.Run("returns dst unchanged with ErrBody", func(t *testing.T) {
			t.Parallel()
			got, err := pqSigner(t, nil).AppendSign(t.Context(), []byte(prefix), []byte(cosignedText+"ext\n"))
			testkit.ErrorIs(t, err, checkpoint.ErrBody, "AppendSign must refuse a body that SubtreeV1 cannot cover")
			testkit.Equal(t, string(got), prefix, "AppendSign must return dst unchanged")
		})
	})
}

func BenchmarkSubtree(b *testing.B) {
	entry := checkpoint.SubtreeV1(mldsa.Resolver(mldsa.MLDSA44, ""))
	n := mustParse(b, readFile(b, cosignedFile))
	k := vectorKey(b, vectorPQ)
	inner := mldsaSigner(b, mldsa.MLDSA44, pqName, "")
	text := []byte(cosignedText)

	v, err := entry(k)
	testkit.NoError(b, err, "SubtreeV1 must build the Verifier")

	line, _ := n.Find(k)
	s := pqSigner(b, fake.New(clockTime))

	b.Run("SubtreeV1", func(b *testing.B) {
		benchAllocs(b, 4, func() { sinkVerifier, errSink = entry(k) })
	})

	b.Run("Verify", func(b *testing.B) {
		benchZeroAlloc(b, func() { sinkBool = v.Verify(n.Text, line.Value) })
	})

	b.Run("NewSubtreeV1Signer", func(b *testing.B) {
		benchAllocs(b, 1, func() {
			sinkSigner, errSink = checkpoint.NewSubtreeV1Signer(pqName, checkpoint.TypeMLDSA44Cosignature, inner,
				nil, 0)
		})
	})

	b.Run("SubtreeV1Signer.Reset", func(b *testing.B) {
		var reused checkpoint.SubtreeV1Signer
		benchZeroAlloc(b, func() {
			errSink = reused.Reset(pqName, checkpoint.TypeMLDSA44Cosignature, inner, nil, 0)
		})
	})

	b.Run("Sign", func(b *testing.B) {
		benchAllocs(b, 2, func() { sinkBytes, errSink = s.Sign(text) })
	})

	b.Run("AppendSign", func(b *testing.B) {
		ctx := b.Context()
		buf := make([]byte, 0, 8+stdmldsa.MLDSA44().SignatureSize())
		benchAllocs(b, 1, func() { sinkBytes, errSink = s.AppendSign(ctx, buf[:0], text) })
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
// ML-DSA-44 key of pqName, with the time of utc.
func pqSigner(tb testing.TB, utc clock.UTCSource) note.Signer {
	tb.Helper()

	s, err := checkpoint.NewSubtreeV1Signer(pqName, checkpoint.TypeMLDSA44Cosignature,
		mldsaSigner(tb, mldsa.MLDSA44, pqName, ""), utc, time.Second)
	testkit.NoError(tb, err, "NewSubtreeV1Signer must accept the ML-DSA-44 signer")

	return s
}

// subtreeValue returns the value of a timestamped signature with timestamp
// t, by the ML-DSA-44 key of pqName, over the cosignedMessage of name,
// origin, size and hash.
func subtreeValue(tb testing.TB, name string, t uint64, origin string, size uint64, hash []byte) []byte {
	tb.Helper()

	sig, err := mldsaSigner(tb, mldsa.MLDSA44, pqName, "").Sign(cosignedMessage(name, t, origin, size, hash))
	testkit.NoError(tb, err, "ML-DSA-44 must sign the message")

	return value(t, sig...)
}

// cosignedMessage returns the cosigned_message of tlog-cosignature for the
// checkpoint of origin, size and root hash, by the cosigner name with
// timestamp t: the label, the name after its length byte, the timestamp,
// the origin after its length byte, the start 0, the size as the end, and
// the hash.
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
