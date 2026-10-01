// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package note_test

import (
	"bytes"
	"context"
	"math/rand/v2"
	"strings"
	"sync/atomic"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sign"
	"go.thesmos.sh/core/crypto/sign/ed25519"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/note"
)

// Notes of other implementations.
const (
	// exampleText and exampleNote are the text and the note of the example
	// of signed-note, which exampleKey signs.
	exampleText = "This is an example message.\n"
	exampleNote = exampleText + "\n— example.com/foo " +
		"Uw2QOkn8srV1yJGh2VYRlL1Tnagv1YEq6TfXppzi2ONncAlTgK7Ztg1ERYNZXsYjOBH3mFXmRKuwHjG1Yu72IneyaQM=\n"

	// peterText and peterNote are the text and the note of
	// golang.org/x/mod's tests, which peterKey signs.
	peterText = "If you think cryptography is the answer to your problem,\n" +
		"then you don't know what your problem is.\n"
	peterNote = peterText + "\n" + peterLine

	// enochLine is the signature line of enochKey over peterText in
	// golang.org/x/mod's tests.
	enochLine = "— EnochRoot " +
		"rwz+eBzmZa0SO3NbfRGzPCpDckykFXSdeX+MNtCOXm2/5n2tiOHp+vAF1aGrQ5ovTG01oOTGwnWLox33WWd1RvMc+QQ=\n"
)

const (
	// repeats is the number of copies of one line in the case that
	// counts the verifications of Check.
	repeats = 1000

	// benchRuns is the number of calls over which a benchmark averages
	// the allocations that it checks.
	benchRuns = 100
)

// Sinks receive the results of the benchmarks, so that the compiler
// keeps every call that they measure.
var (
	sinkBool      bool
	sinkString    string
	sinkBytes     []byte
	sinkType      note.Type
	sinkKey       note.Key
	sinkID        uint32
	sinkKeyID     sign.KeyID
	sinkVerifier  note.Verifier
	sinkSigner    note.Signer
	sinkNote      note.Note
	sinkSignature note.Signature
	errSink       error
)

// benchZeroAlloc reports the cost of call, and fails when call allocates.
func benchZeroAlloc(b *testing.B, call func()) {
	b.Helper()
	benchAllocs(b, 0, call)
}

// benchAllocs reports the cost of call, and fails when call does not
// allocate want times per call.
func benchAllocs(b *testing.B, want float64, call func()) {
	b.Helper()

	if allocs := testing.AllocsPerRun(benchRuns, call); allocs != want {
		b.Fatalf("allocates %v times per call, want %v", allocs, want)
	}

	b.ReportAllocs()
	for b.Loop() {
		call()
	}
}

// countingVerifier is a note.Verifier that counts its calls to Verify.
type countingVerifier struct {
	note.Verifier

	calls *atomic.Int64
}

// Verify counts the call and returns the result of the embedded Verifier.
func (v countingVerifier) Verify(text, value []byte) bool {
	v.calls.Add(1)

	return v.Verifier.Verify(text, value)
}

// renamedSigner is a note.Signer whose key has the name it sets.
type renamedSigner struct {
	note.Signer

	name note.Name
}

// Key returns the key of the embedded Signer under the name of s.
func (s renamedSigner) Key() note.Key {
	k := s.Signer.Key()
	k.Name = s.name

	return k
}

// textVerifier returns the Ed25519 text Verifier of vkey.
func textVerifier(tb testing.TB, vkey string) note.Verifier {
	tb.Helper()

	v, err := note.Text(ed25519.Resolve)(mustParseKey(tb, vkey))
	testkit.NoError(tb, err, "Text must build the Verifier of "+vkey)

	return v
}

// policyOf returns the policy that every one of verifiers signs.
func policyOf(tb testing.TB, verifiers ...sign.Verifier) sign.Policy {
	tb.Helper()

	p, err := sign.NewPolicyTree(sign.AllOf("keys", verifiers...))
	testkit.NoError(tb, err, "NewPolicyTree must accept the keys")

	return p
}

// mustParse returns the note of msg, and fails the test when Parse
// refuses it.
func mustParse(tb testing.TB, msg string) *note.Note {
	tb.Helper()

	n, err := note.Parse([]byte(msg))
	testkit.NoError(tb, err, "Parse must accept the note")

	return &n
}

// assertEmpty fails the test when n has a text or a line, as a Note that
// an error emptied has neither.
func assertEmpty(tb testing.TB, n *note.Note) {
	tb.Helper()

	testkit.True(tb, n.Text == nil, "the Note must have no text")
	testkit.Len(tb, n.Signatures, 0, "the Note must have no line")
}

// textRunes are the characters of the texts that randomNote draws: the
// characters of signature lines, a space, and characters outside ASCII
// and from U+007F to U+009F.
var textRunes = []rune("ab —+=/例λ\u007f\u0080\u009f")

// randomNote returns a valid note: one to five lines of text, empty lines
// included, and one to five signature lines.
func randomNote(r *rand.Rand) *note.Note {
	var text strings.Builder
	for range 1 + r.IntN(5) {
		for range r.IntN(40) {
			text.WriteRune(textRunes[r.IntN(len(textRunes))])
		}

		text.WriteByte('\n')
	}

	sigs := make([]note.Signature, 1+r.IntN(5))
	for i := range sigs {
		sigs[i] = note.Signature{Name: randomName(r), Value: randomBytes(r, 1, 80), ID: r.Uint32()}
	}

	return &note.Note{Text: []byte(text.String()), Signatures: sigs}
}

func TestNote(t *testing.T) {
	t.Parallel()

	t.Run("Parse", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the text and the lines of signed-note's example", func(t *testing.T) {
			t.Parallel()
			n := mustParse(t, exampleNote)
			testkit.Equal(t, string(n.Text), exampleText, "Parse must return the text with its newline")
			testkit.Len(t, n.Signatures, 1, "the example has one signature line")
			testkit.Equal(t, n.Signatures[0].Name, note.Name(exampleName), "Parse must return the key name")
			testkit.Equal(t, n.Signatures[0].ID, uint32(0x530d903a), "Parse must return the key ID")
			testkit.Len(t, n.Signatures[0].Value, 64, "Parse must return the 64 bytes of the Ed25519 signature")
		})

		t.Run("splits a note at its last blank line", func(t *testing.T) {
			t.Parallel()
			n := mustParse(t, "a\n\nb\n\n"+peterLine)
			testkit.Equal(t, string(n.Text), "a\n\nb\n", "the blank line of the text must stay in the text")
		})

		t.Run("accepts a text of one newline", func(t *testing.T) {
			t.Parallel()
			testkit.Equal(t, string(mustParse(t, "\n\n"+peterLine).Text), "\n", "Parse must return the newline")
		})

		t.Run("keeps repeated lines and the lines of unknown keys", func(t *testing.T) {
			t.Parallel()
			n := mustParse(t, peterNote+peterLine+enochLine)
			testkit.Len(t, n.Signatures, 3, "Parse must keep every line")
			testkit.Equal(t, n.Signatures[2].Name, note.Name("EnochRoot"), "Parse must keep the order of the lines")
		})

		t.Run("accepts a line of a key ID and one byte of signature", func(t *testing.T) {
			t.Parallel()
			testkit.Len(t, mustParse(t, "a\n\n— a AAAAAAA=\n").Signatures[0].Value, 1, "a line may hold one byte")
		})

		t.Run("accepts U+007F and U+0080 to U+009F", func(t *testing.T) {
			t.Parallel()
			msg := "a\u007f\u0080\u009f\n\n— a\u007f\u0080\u009f AAAAAAA=\n"
			n := mustParse(t, msg)
			testkit.Equal(t, n.Signatures[0].Name, note.Name("a\u007f\u0080\u009f"), "the name may hold them")
		})

		t.Run("returns values that do not alias msg", func(t *testing.T) {
			t.Parallel()
			msg := []byte(peterNote)
			n, err := note.Parse(msg)
			testkit.NoError(t, err, "Parse must accept the note")
			want := bytes.Clone(n.Signatures[0].Value)
			clear(msg)
			testkit.Equal(t, n.Signatures[0].Value, want, "a change to msg must not change a value")
		})

		t.Run("returns values whose growth does not reach the next value", func(t *testing.T) {
			t.Parallel()
			n := mustParse(t, peterNote+enochLine)
			want := bytes.Clone(n.Signatures[1].Value)
			_ = append(n.Signatures[0].Value, 0xff)
			testkit.Equal(t, n.Signatures[1].Value, want, "an append to a value must copy it")
		})

		t.Run("accepts exactly the notes that AppendText writes", func(t *testing.T) {
			t.Parallel()
			r := testkit.SeededRand(t)
			for range 2000 {
				n := randomNote(r)
				msg, err := n.AppendText(nil)
				testkit.NoError(t, err, "AppendText must accept a valid note")
				got, err := note.Parse(msg)
				testkit.NoError(t, err, "Parse must accept "+string(msg))
				testkit.Equal(t, got, *n, "Parse must return the note that AppendText wrote")

				msg[r.IntN(len(msg))] = byte(r.Uint32())
				if changed, err := note.Parse(msg); err == nil {
					again, err := changed.AppendText(nil)
					testkit.NoError(t, err, "AppendText must accept a note that Parse returns")
					testkit.Equal(t, string(again), string(msg), "Parse must accept only what AppendText writes")
				}
			}
		})

		tests := []struct {
			name string
			give string
		}{
			{name: "returns ErrNote for a note that is not valid UTF-8", give: "a\xff\n\n" + peterLine},
			{name: "returns ErrNote for a note with a NUL", give: "a\x00\n\n" + peterLine},
			{name: "returns ErrNote for a note with a carriage return", give: "a\r\n\r\n" + peterLine},
			{name: "returns ErrNote for a note with U+001F", give: "a\x1f\n\n" + peterLine},
			{name: "returns ErrNote for a note without a blank line", give: peterText + peterLine},
			{name: "returns ErrNote for a note without a signature line", give: peterText + "\n"},
			{name: "returns ErrNote for a last line without a newline", give: strings.TrimSuffix(peterNote, "\n")},
			{
				name: "returns ErrNote for a line without the em dash",
				give: peterText + "\n- PeterNeumann " + peterValue + "\n",
			},
			{
				name: "returns ErrNote for a line without the space after the em dash",
				give: peterText + "\n—PeterNeumann " + peterValue + "\n",
			},
			{
				name: "returns ErrNote for a line with an invalid name",
				give: peterText + "\n— Peter+Neumann " + peterValue + "\n",
			},
			{name: "returns ErrNote for a line without a space after the name", give: peterText + "\n— PeterNeumann\n"},
			{name: "returns ErrNote for a line that is not base64", give: peterText + "\n— PeterNeumann !!!!\n"},
			{
				name: "returns ErrNote for a line whose base64 has spare bits",
				give: peterText + "\n— PeterNeumann " + strings.Replace(peterValue, "AM=", "AN=", 1) + "\n",
			},
			{name: "returns ErrNote for a line of 4 bytes", give: "a\n\n— a AAAAAA==\n"},
			{name: "returns ErrNote for a line of 4 bytes after a valid line", give: peterNote + "— a AAAAAA==\n"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				n, err := note.Parse([]byte(tt.give))
				testkit.ErrorIs(t, err, note.ErrNote, "Parse must refuse the note")
				testkit.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				testkit.Equal(t, n, note.Note{}, "Parse must return the zero Note with an error")
			})
		}
	})

	t.Run("Open", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the note when its lines satisfy the policy", func(t *testing.T) {
			t.Parallel()
			n, err := note.Open([]byte(exampleNote), policyOf(t, textVerifier(t, exampleKey)))
			testkit.NoError(t, err, "Open must accept the example of signed-note")
			testkit.Equal(t, string(n.Text), exampleText, "Open must return the text")
		})

		t.Run("returns the error of Parse", func(t *testing.T) {
			t.Parallel()
			_, err := note.Open([]byte(peterText), policyOf(t, textVerifier(t, peterKey)))
			testkit.ErrorIs(t, err, note.ErrNote, "Open must return the error of Parse")
		})

		t.Run("returns the error of Check", func(t *testing.T) {
			t.Parallel()
			n, err := note.Open([]byte(peterNote), policyOf(t, textVerifier(t, enochKey)))
			testkit.ErrorIs(t, err, sign.ErrThreshold, "Open must return the error of Check")
			testkit.Equal(t, n, note.Note{}, "Open must return the zero Note with an error")
		})
	})

	t.Run("Sign", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the note that golang.org/x/mod's tests record", func(t *testing.T) {
			t.Parallel()
			n, err := note.Sign(t.Context(), []byte(peterText), peterSigner(t))
			testkit.NoError(t, err, "Sign must sign the text")
			msg, err := n.AppendText(nil)
			testkit.NoError(t, err, "AppendText must write the note")
			testkit.Equal(t, string(msg), peterNote, "Sign must give x/mod's note")
		})

		t.Run("signs with each signer in order", func(t *testing.T) {
			t.Parallel()
			pq, err := note.NewTextSigner("example.com/pq", pqType, mldsaSigner(t))
			testkit.NoError(t, err, "NewTextSigner must accept the ML-DSA signer")
			n, err := note.Sign(t.Context(), []byte(peterText), peterSigner(t), pq)
			testkit.NoError(t, err, "Sign must sign with both signers")
			testkit.Len(t, n.Signatures, 2, "Sign must write one line per signer")
			testkit.Equal(t, n.Signatures[1].Name, note.Name("example.com/pq"), "the lines must follow the signers")
			testkit.Equal(t, n.Signatures[1].ID, pq.Key().ID(), "a line must carry the key ID of its signer")
			testkit.True(t, pq.Verify(n.Text, n.Signatures[1].Value), "the second line must verify")
		})

		t.Run("returns the error of a signer that fails", func(t *testing.T) {
			t.Parallel()
			failure := testkit.TestError("the signer fails")
			s, err := note.NewTextSigner(
				"a",
				pqType,
				fakeSigner{alg: crypto.AlgMLDSA44, pub: publicKey(), err: failure},
			)
			testkit.NoError(t, err, "NewTextSigner must accept the signer")
			_, err = note.Sign(t.Context(), []byte(peterText), s)
			testkit.ErrorIs(t, err, failure, "Sign must return the error of the signer")
		})

		t.Run("returns the cause of a context that ended", func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			_, err := note.Sign(ctx, []byte(peterText), peterSigner(t))
			testkit.ErrorIs(t, err, context.Canceled, "Sign must stop at a context that ended")
		})

		emptySigner, err := note.NewTextSigner("a", pqType, fakeSigner{alg: crypto.AlgMLDSA44, pub: publicKey()})
		testkit.NoError(t, err, "NewTextSigner must accept the signer")

		tests := []struct {
			want    error
			name    string
			text    string
			signers []note.Signer
		}{
			{
				name: "returns ErrNote for a text without its last newline", text: "a",
				signers: []note.Signer{peterSigner(t)}, want: note.ErrNote,
			},
			{
				name: "returns ErrNote for a text that is not valid UTF-8", text: "a\xff\n",
				signers: []note.Signer{peterSigner(t)}, want: note.ErrNote,
			},
			{
				name: "returns ErrNote for a text with a character below U+0020", text: "a\x01\n",
				signers: []note.Signer{peterSigner(t)}, want: note.ErrNote,
			},
			{name: "returns ErrNote for no signers", text: peterText, signers: nil, want: note.ErrNote},
			{
				name: "returns ErrKey for a nil signer", text: peterText,
				signers: []note.Signer{peterSigner(t), nil}, want: note.ErrKey,
			},
			{
				name: "returns ErrKey for a signer with an invalid name", text: peterText,
				signers: []note.Signer{renamedSigner{Signer: peterSigner(t), name: "a b"}}, want: note.ErrKey,
			},
			{
				name: "returns ErrNote for a signer that returns an empty signature", text: peterText,
				signers: []note.Signer{emptySigner}, want: note.ErrNote,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				n, err := note.Sign(t.Context(), []byte(tt.text), tt.signers...)
				testkit.ErrorIs(t, err, tt.want, "Sign must refuse the call")
				testkit.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				testkit.Equal(t, n, note.Note{}, "Sign must return the zero Note with an error")
			})
		}
	})

	t.Run("Note.Sign", func(t *testing.T) {
		t.Parallel()

		t.Run("sets n to the note that golang.org/x/mod's tests record", func(t *testing.T) {
			t.Parallel()
			var n note.Note
			testkit.NoError(t, n.Sign(t.Context(), []byte(peterText), peterSigner(t)), "Sign must sign the text")
			msg, err := n.AppendText(nil)
			testkit.NoError(t, err, "AppendText must write the note")
			testkit.Equal(t, string(msg), peterNote, "Sign must give x/mod's note")
		})

		t.Run("reuses the slice of lines and the value of each line", func(t *testing.T) {
			t.Parallel()
			s := peterSigner(t)
			var n note.Note
			testkit.NoError(t, n.Sign(t.Context(), []byte(peterText), s), "Sign must sign the text")
			line, value := &n.Signatures[0], &n.Signatures[0].Value[0]
			testkit.NoError(t, n.Sign(t.Context(), []byte(exampleText), s), "Sign must sign the second text")
			testkit.True(t, &n.Signatures[0] == line, "Sign must reuse the slice of lines")
			testkit.True(
				t,
				&n.Signatures[0].Value[0] == value,
				"Sign must append the signature to the value of the line",
			)
			testkit.True(t, s.Verify([]byte(exampleText), n.Signatures[0].Value), "the line must verify over the text")
		})

		t.Run("sets n to the lines of each list of signers in turn", func(t *testing.T) {
			t.Parallel()
			peter := peterSigner(t)
			pq, err := note.NewTextSigner("example.com/pq", pqType, mldsaSigner(t))
			testkit.NoError(t, err, "NewTextSigner must accept the ML-DSA signer")
			var n note.Note
			for _, signers := range [][]note.Signer{{peter}, {pq, peter}, {peter}, {peter, pq}} {
				testkit.NoError(t, n.Sign(t.Context(), []byte(peterText), signers...), "Sign must sign the text")
				testkit.Len(t, n.Signatures, len(signers), "Sign must write one line per signer")
				for i, s := range signers {
					testkit.Equal(t, n.Signatures[i].Name, s.Key().Name, "the lines must follow the signers")
					testkit.True(t, s.Verify(n.Text, n.Signatures[i].Value), "each line must verify")
				}
			}
		})

		t.Run("empties n with the error of a signer", func(t *testing.T) {
			t.Parallel()
			failure := testkit.TestError("the signer fails")
			s, err := note.NewTextSigner(
				"a",
				pqType,
				fakeSigner{alg: crypto.AlgMLDSA44, pub: publicKey(), err: failure},
			)
			testkit.NoError(t, err, "NewTextSigner must accept the signer")
			n := mustParse(t, peterNote)
			err = n.Sign(t.Context(), []byte(peterText), peterSigner(t), s)
			testkit.ErrorIs(t, err, failure, "Sign must return the error of the signer")
			assertEmpty(t, n)
		})

		t.Run("empties n for a text without its last newline", func(t *testing.T) {
			t.Parallel()
			n := mustParse(t, peterNote)
			err := n.Sign(t.Context(), []byte("a"), peterSigner(t))
			testkit.ErrorIs(t, err, note.ErrNote, "Sign must refuse the text")
			assertEmpty(t, n)
		})

		t.Run("signs into the memory of the note that an error emptied", func(t *testing.T) {
			t.Parallel()
			s := peterSigner(t)
			var n note.Note
			testkit.NoError(t, n.Sign(t.Context(), []byte(peterText), s), "Sign must sign the text")
			value := &n.Signatures[0].Value[0]
			testkit.ErrorIs(t, n.Sign(t.Context(), []byte("a"), s), note.ErrNote, "Sign must refuse the text")
			testkit.NoError(t, n.Sign(t.Context(), []byte(exampleText), s), "Sign must sign the second text")
			testkit.True(t, &n.Signatures[0].Value[0] == value, "Sign must reuse the value that the error kept")
		})
	})

	t.Run("Check", func(t *testing.T) {
		t.Parallel()

		t.Run("returns nil for golang.org/x/mod's note under the key of PeterNeumann", func(t *testing.T) {
			t.Parallel()
			testkit.NoError(t, mustParse(t, peterNote).Check(policyOf(t, textVerifier(t, peterKey))),
				"Check must accept x/mod's signature")
		})

		t.Run("returns nil for the note of two keys under both keys", func(t *testing.T) {
			t.Parallel()
			p := policyOf(t, textVerifier(t, peterKey), textVerifier(t, enochKey))
			testkit.NoError(t, mustParse(t, peterNote+enochLine).Check(p), "Check must accept both signatures")
		})

		t.Run("returns ErrThreshold for a note that the key did not sign", func(t *testing.T) {
			t.Parallel()
			err := mustParse(t, peterNote).Check(policyOf(t, textVerifier(t, enochKey)))
			testkit.ErrorIs(t, err, sign.ErrThreshold, "Check must refuse a note without the key's line")
			testkit.Equal(t, errs.Classify(err), errs.Integrity, "the error must classify as Integrity")
		})

		t.Run("returns ErrThreshold for a signature over another text", func(t *testing.T) {
			t.Parallel()
			err := mustParse(t, "another text\n\n"+peterLine).Check(policyOf(t, textVerifier(t, peterKey)))
			testkit.ErrorIs(t, err, sign.ErrThreshold, "Check must refuse a signature over another text")
		})

		t.Run("returns ErrPolicy for the zero Policy", func(t *testing.T) {
			t.Parallel()
			testkit.ErrorIs(t, mustParse(t, peterNote).Check(sign.Policy{}), sign.ErrPolicy,
				"Check must return the error of the zero Policy")
		})

		t.Run("verifies one line of a key whose line repeats 1,000 times", func(t *testing.T) {
			t.Parallel()
			calls := &atomic.Int64{}
			v := countingVerifier{Verifier: textVerifier(t, peterKey), calls: calls}
			n := mustParse(t, peterNote+strings.Repeat(peterLine, repeats-1))
			testkit.NoError(t, n.Check(policyOf(t, v)), "Check must accept the note")
			testkit.Equal(t, calls.Load(), int64(1), "Check must verify one line of the key")
		})

		t.Run("ignores a line of the name of a key under another key ID", func(t *testing.T) {
			t.Parallel()
			calls := &atomic.Int64{}
			v := countingVerifier{Verifier: textVerifier(t, peterKey), calls: calls}
			n := mustParse(t, peterNote)
			n.Signatures[0].ID++
			testkit.ErrorIs(t, n.Check(policyOf(t, v)), sign.ErrThreshold, "Check must not count the line")
			testkit.Equal(t, calls.Load(), int64(0), "Check must not verify the line of another key")
		})

		t.Run("ignores a line of the key ID of a key under another name", func(t *testing.T) {
			t.Parallel()
			calls := &atomic.Int64{}
			v := countingVerifier{Verifier: textVerifier(t, peterKey), calls: calls}
			n := mustParse(t, peterNote)
			n.Signatures[0].Name = "PeterNeumann2"
			testkit.ErrorIs(t, n.Check(policyOf(t, v)), sign.ErrThreshold, "Check must not count the line")
			testkit.Equal(t, calls.Load(), int64(0), "Check must not verify the line of another key")
		})
	})

	t.Run("Find", func(t *testing.T) {
		t.Parallel()

		peter := mustParseKey(t, peterKey)

		t.Run("returns the first line of the key", func(t *testing.T) {
			t.Parallel()
			n := mustParse(t, peterNote+enochLine+peterLine)
			n.Signatures[2].Value = []byte{1}
			got, ok := n.Find(peter)
			testkit.True(t, ok, "Find must find the line of the key")
			testkit.Equal(t, got, n.Signatures[0], "Find must return the first line of the key")
		})

		tests := []struct {
			change func(*note.Note)
			name   string
		}{
			{
				name:   "reports false for a key without a line",
				change: func(n *note.Note) { n.Signatures = n.Signatures[1:] },
			},
			{name: "reports false for a line of the key ID under another name", change: func(n *note.Note) {
				n.Signatures[0].Name = "Peter"
			}},
			{name: "reports false for a line of the name under another key ID", change: func(n *note.Note) {
				n.Signatures[0].ID--
			}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				n := mustParse(t, peterNote+enochLine)
				tt.change(n)
				got, ok := n.Find(peter)
				testkit.False(t, ok, "Find must not find a line of the key")
				testkit.Equal(t, got, note.Signature{}, "Find must return the zero Signature")
			})
		}
	})

	t.Run("Valid", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			give *note.Note
			name string
			want bool
		}{
			{name: "reports true for golang.org/x/mod's note", give: mustParse(t, peterNote), want: true},
			{
				name: "reports false for an empty text",
				give: &note.Note{Signatures: []note.Signature{peterSignature(t)}}, want: false,
			},
			{
				name: "reports false for a text without its last newline",
				give: &note.Note{Text: []byte("a"), Signatures: []note.Signature{peterSignature(t)}}, want: false,
			},
			{
				name: "reports false for a text with a character below U+0020",
				give: &note.Note{Text: []byte("a\x02\n"), Signatures: []note.Signature{peterSignature(t)}}, want: false,
			},
			{
				name: "reports false for a text that is not valid UTF-8",
				give: &note.Note{Text: []byte("a\xff\n"), Signatures: []note.Signature{peterSignature(t)}}, want: false,
			},
			{name: "reports false for a note without a line", give: &note.Note{Text: []byte("a\n")}, want: false},
			{name: "reports false for a line that is not Valid", give: &note.Note{
				Text: []byte("a\n"), Signatures: []note.Signature{peterSignature(t), {Name: "a"}},
			}, want: false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(t, tt.give.Valid(), tt.want, "Valid must report whether the note is well formed")
			})
		}
	})

	t.Run("AppendText", func(t *testing.T) {
		t.Parallel()

		t.Run("appends golang.org/x/mod's note", func(t *testing.T) {
			t.Parallel()
			got, err := mustParse(t, peterNote+enochLine).AppendText([]byte("note:"))
			testkit.NoError(t, err, "AppendText must accept a valid note")
			testkit.Equal(t, string(got), "note:"+peterNote+enochLine, "AppendText must write the note back")
		})

		t.Run("returns b unchanged and ErrNote for a Note that is not Valid", func(t *testing.T) {
			t.Parallel()
			got, err := (&note.Note{Text: []byte("a\n")}).AppendText([]byte("note:"))
			testkit.ErrorIs(t, err, note.ErrNote, "AppendText must refuse a note without a line")
			testkit.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
			testkit.Equal(t, string(got), "note:", "AppendText must return b unchanged")
		})
	})

	t.Run("MarshalText", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the note that AppendText appends", func(t *testing.T) {
			t.Parallel()
			got, err := mustParse(t, peterNote+enochLine).MarshalText()
			testkit.NoError(t, err, "MarshalText must accept a valid note")
			testkit.Equal(t, string(got), peterNote+enochLine, "MarshalText must return the note")
		})

		t.Run("returns a note whose capacity is its length", func(t *testing.T) {
			t.Parallel()
			r := testkit.SeededRand(t)
			for range 2000 {
				got, err := randomNote(r).MarshalText()
				testkit.NoError(t, err, "MarshalText must accept a valid note")
				testkit.Equal(t, cap(got), len(got), "MarshalText must allocate the note at its length")
			}
		})

		t.Run("returns nil and ErrNote for a Note that is not Valid", func(t *testing.T) {
			t.Parallel()
			got, err := (&note.Note{Text: []byte("a\n")}).MarshalText()
			testkit.ErrorIs(t, err, note.ErrNote, "MarshalText must refuse a note without a line")
			testkit.True(t, got == nil, "MarshalText must return nil with an error")
		})
	})

	t.Run("UnmarshalText", func(t *testing.T) {
		t.Parallel()

		t.Run("sets n to the note that Parse returns", func(t *testing.T) {
			t.Parallel()
			var n note.Note
			testkit.NoError(t, n.UnmarshalText([]byte(peterNote+enochLine)), "UnmarshalText must accept the note")
			testkit.Equal(t, &n, mustParse(t, peterNote+enochLine), "UnmarshalText must set the note of Parse")
		})

		t.Run("reuses the slice of lines and the value of each line", func(t *testing.T) {
			t.Parallel()
			msg := []byte(peterNote + enochLine)
			var n note.Note
			testkit.NoError(t, n.UnmarshalText(msg), "UnmarshalText must accept the note")
			line, value := &n.Signatures[1], &n.Signatures[1].Value[0]
			testkit.NoError(t, n.UnmarshalText(msg), "UnmarshalText must accept the note again")
			testkit.True(t, &n.Signatures[1] == line, "UnmarshalText must reuse the slice of lines")
			testkit.True(t, &n.Signatures[1].Value[0] == value, "UnmarshalText must decode into the value of the line")
		})

		t.Run("sets n to each note in turn", func(t *testing.T) {
			t.Parallel()
			r := testkit.SeededRand(t)
			var n note.Note
			for range 2000 {
				want := randomNote(r)
				msg, err := want.AppendText(nil)
				testkit.NoError(t, err, "AppendText must accept a valid note")
				testkit.NoError(t, n.UnmarshalText(msg), "UnmarshalText must accept "+string(msg))
				testkit.Equal(t, &n, want, "UnmarshalText must set the note that AppendText wrote")
			}
		})

		t.Run("decodes into the memory of the note that an error emptied", func(t *testing.T) {
			t.Parallel()
			msg := []byte(peterNote + enochLine)
			var n note.Note
			testkit.NoError(t, n.UnmarshalText(msg), "UnmarshalText must accept the note")
			value := &n.Signatures[1].Value[0]
			testkit.ErrorIs(t, n.UnmarshalText([]byte(peterText)), note.ErrNote, "UnmarshalText must refuse the text")
			testkit.NoError(t, n.UnmarshalText(msg), "UnmarshalText must accept the note again")
			testkit.True(t, &n.Signatures[1].Value[0] == value,
				"UnmarshalText must reuse the value that the error kept")
		})

		t.Run("decodes a line whose signature outgrows its value", func(t *testing.T) {
			t.Parallel()
			n := mustParse(t, "a\n\n— a AAAAAAA=\n")
			testkit.NoError(t, n.UnmarshalText([]byte(peterNote)), "UnmarshalText must accept the note")
			testkit.Equal(t, n, mustParse(t, peterNote), "UnmarshalText must set the longer signature")
		})

		tests := []struct {
			name string
			give string
		}{
			{name: "empties n for a note without a blank line", give: peterText + peterLine},
			{
				name: "empties n for a line without the em dash",
				give: peterNote + "- PeterNeumann " + peterValue + "\n",
			},
			{name: "empties n for a line that is not base64", give: peterNote + "— PeterNeumann !!!!\n"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				n := mustParse(t, peterNote)
				err := n.UnmarshalText([]byte(tt.give))
				testkit.ErrorIs(t, err, note.ErrNote, "UnmarshalText must refuse the note")
				testkit.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				assertEmpty(t, n)
			})
		}
	})
}

func BenchmarkNote(b *testing.B) {
	msg := []byte(peterNote)
	text := []byte(peterText)
	n := mustParse(b, peterNote)
	p := policyOf(b, textVerifier(b, peterKey))
	s := peterSigner(b)
	k := mustParseKey(b, peterKey)

	b.Run("Parse", func(b *testing.B) {
		benchAllocs(b, 3, func() { sinkNote, errSink = note.Parse(msg) })
	})

	b.Run("Parse of 3 lines", func(b *testing.B) {
		three := []byte(peterNote + peterLine + enochLine)
		benchAllocs(b, 3, func() { sinkNote, errSink = note.Parse(three) })
	})

	b.Run("Open", func(b *testing.B) {
		benchAllocs(b, 3, func() { sinkNote, errSink = note.Open(msg, p) })
	})

	b.Run("Sign", func(b *testing.B) {
		ctx := b.Context()
		benchAllocs(b, 2, func() { sinkNote, errSink = note.Sign(ctx, text, s) })
	})

	b.Run("Note.Sign", func(b *testing.B) {
		ctx := b.Context()
		var reused note.Note
		testkit.NoError(b, reused.Sign(ctx, text, s), "Sign must sign the text")
		benchZeroAlloc(b, func() { errSink = reused.Sign(ctx, text, s) })
	})

	b.Run("Check", func(b *testing.B) {
		benchZeroAlloc(b, func() { errSink = n.Check(p) })
	})

	b.Run("Find", func(b *testing.B) {
		benchZeroAlloc(b, func() { sinkSignature, sinkBool = n.Find(k) })
	})

	b.Run("Valid", func(b *testing.B) {
		benchZeroAlloc(b, func() { sinkBool = n.Valid() })
	})

	b.Run("AppendText", func(b *testing.B) {
		buf := make([]byte, 0, len(peterNote))
		benchZeroAlloc(b, func() { sinkBytes, errSink = n.AppendText(buf[:0]) })
	})

	b.Run("MarshalText", func(b *testing.B) {
		benchAllocs(b, 1, func() { sinkBytes, errSink = n.MarshalText() })
	})

	b.Run("UnmarshalText", func(b *testing.B) {
		var reused note.Note
		testkit.NoError(b, reused.UnmarshalText(msg), "UnmarshalText must accept the note")
		benchZeroAlloc(b, func() { errSink = reused.UnmarshalText(msg) })
	})
}
