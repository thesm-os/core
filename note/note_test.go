// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package note_test

import (
	"bytes"
	"context"
	stded25519 "crypto/ed25519"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sign"
	"go.thesmos.sh/core/crypto/sign/ed25519"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/note"
)

// Notes of other implementations.
const (
	// exampleText, exampleLine and exampleNote are the text, the signature
	// line and the note of the example of signed-note, which exampleKey
	// signs.
	exampleText = "This is an example message.\n"
	exampleLine = "— example.com/foo " +
		"Uw2QOkn8srV1yJGh2VYRlL1Tnagv1YEq6TfXppzi2ONncAlTgK7Ztg1ERYNZXsYjOBH3mFXmRKuwHjG1Yu72IneyaQM=\n"
	exampleNote = exampleText + "\n" + exampleLine

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

// The details that the errors of Parse and TextOf add to the text of
// ErrNote, for the rules that several notes break.
const (
	// badBytes is the detail of a note that is not valid UTF-8 or that
	// contains a character below U+0020 other than newline.
	badBytes = "not valid UTF-8, or a character below U+0020 other than newline"

	// noBlankLine is the detail of a note without a blank line.
	noBlankLine = "no blank line before the signature lines"

	// noLine is the detail of a note without a signature line or whose last
	// line does not end in a newline.
	noLine = "no signature line, or a last line without a newline"

	// noPrefix is the detail of a signature line that does not start with
	// an em dash and a space.
	noPrefix = `a signature line does not start with "— "`

	// peterEncoding and shortEncoding are the details of a line of
	// PeterNeumann and of a line of the name a that are not the padded
	// standard base64 of a key ID and a signature.
	peterEncoding = "the line of PeterNeumann is not padded standard base64 of a key ID and a signature"
	shortEncoding = "the line of a is not padded standard base64 of a key ID and a signature"
)

// The fixture values of the cases of Note.
const (
	// textAlphabet are the characters of the lines of the texts that texts
	// generates: the characters of signature lines, a space, and characters
	// outside ASCII and from U+007F to U+009F.
	textAlphabet = "ab —+=/例λ\u007f\u0080\u009f"

	// repeats is the number of copies of one line in the case that counts
	// the verifications of Check.
	repeats = 1000

	// agreesWithParse is the contract of the property and of the fuzz
	// target of TextOf.
	agreesWithParse = "TextOf must return the text and the error of Parse"

	// parsedNote is the label under which the property of the canonical
	// form counts a changed note that Parse accepts. About one changed
	// note in eight parses, so 1,000 cases check about 120 of them.
	parsedNote = "a changed note that Parse accepts"
)

// The generators of the properties of Note.
var (
	// texts generates note texts of one to five lines of up to 39
	// characters of textAlphabet, empty lines included.
	texts = prop.List(prop.String(prop.Alphabet(textAlphabet), prop.MaxSize(39)), prop.MinSize(1), prop.MaxSize(5)).
		Map(func(lines []string) []byte { return []byte(strings.Join(lines, "\n") + "\n") })

	// notes generates valid notes whose lines take their names from names.
	notes = notesOf(names)

	// pooledNotes generates valid notes whose lines take their names from
	// two names, so that one note often repeats the name of the line at the
	// same position of another.
	pooledNotes = notesOf(prop.SampledFrom[note.Name]("PeterNeumann", "EnochRoot"))

	// noteTexts generates the signed notes that AppendText writes for
	// notes.
	noteTexts = prop.Composite(func(c *prop.Case) []byte {
		n := c.Draw(notes, "note")
		msg, err := n.AppendText(nil)
		assert.NoError(c, err, "AppendText must accept a valid note")

		return msg
	})

	// changedNotes generates the signed notes of noteTexts with one byte
	// replaced by any byte.
	changedNotes = prop.Composite(func(c *prop.Case) []byte {
		msg := c.Draw(noteTexts, "note")
		msg[c.Draw(prop.Integer(0, len(msg)-1), "position")] = c.Draw(prop.Of[byte](), "byte")

		return msg
	})

	// messages generates the input of TextOf and Parse: any bytes, a signed
	// note, or a signed note with one byte changed.
	messages = prop.OneOf(prop.Bytes(), noteTexts, changedNotes)
)

// refusedNotes are the notes that Parse, UnmarshalText and TextOf refuse,
// each named by the case of the check that refuses it, with the detail that
// the error adds to the text of ErrNote.
var refusedNotes = []struct {
	name   string
	give   string
	detail string
}{
	{name: "returns ErrNote for a note that is not valid UTF-8", give: "a\xff\n\n" + peterLine, detail: badBytes},
	{name: "returns ErrNote for a note with a NUL", give: "a\x00\n\n" + peterLine, detail: badBytes},
	{name: "returns ErrNote for a note with a carriage return", give: "a\r\n\r\n" + peterLine, detail: badBytes},
	{name: "returns ErrNote for a note with U+001F", give: "a\x1f\n\n" + peterLine, detail: badBytes},
	{name: "returns ErrNote for a note without a blank line", give: peterText + peterLine, detail: noBlankLine},
	{name: "returns ErrNote for a signature line after a lone newline", give: "\n" + peterLine, detail: noBlankLine},
	{name: "returns ErrNote for a note without a signature line", give: peterText + "\n", detail: noLine},
	{
		name:   "returns ErrNote for a last line without a newline",
		give:   strings.TrimSuffix(peterNote, "\n"),
		detail: noLine,
	},
	{
		name:   "returns ErrNote for a line without the em dash",
		give:   peterText + "\n- PeterNeumann " + peterValue + "\n",
		detail: noPrefix,
	},
	{
		name:   "returns ErrNote for a line without the space after the em dash",
		give:   peterText + "\n—PeterNeumann " + peterValue + "\n",
		detail: noPrefix,
	},
	{
		name:   "returns ErrNote for a line with an invalid name",
		give:   peterText + "\n— Peter+Neumann " + peterValue + "\n",
		detail: `a signature line with the invalid name "Peter+Neumann"`,
	},
	{
		name:   "returns ErrNote for a line without a space after the name",
		give:   peterText + "\n— PeterNeumann\n",
		detail: "a signature line without a space after the name",
	},
	{
		name:   "returns ErrNote for a line that is not base64",
		give:   peterText + "\n— PeterNeumann !!!!\n",
		detail: peterEncoding,
	},
	{
		name:   "returns ErrNote for a line whose base64 has spare bits",
		give:   peterText + "\n— PeterNeumann " + strings.Replace(peterValue, "AM=", "AN=", 1) + "\n",
		detail: peterEncoding,
	},
	{name: "returns ErrNote for a line of 4 bytes", give: "a\n\n— a AAAAAA==\n", detail: shortEncoding},
	{
		name:   "returns ErrNote for a line of 4 bytes after a valid line",
		give:   peterNote + "— a AAAAAA==\n",
		detail: shortEncoding,
	},
	{
		name:   "returns ErrNote for a line with an empty name",
		give:   "a\n\n—  AAAAAAA=\n",
		detail: `a signature line with the invalid name ""`,
	},
	{
		name:   "returns ErrNote for padding in the first eight characters of a longer line",
		give:   "a\n\n— a AAAAAAA=AAAA\n",
		detail: shortEncoding,
	},
	{
		name:   "returns ErrNote for a line of base64 whose length is not a multiple of four",
		give:   "a\n\n— a AAAAAAAAAAAAAA=\n",
		detail: shortEncoding,
	},
	{
		name:   "returns ErrNote for padding inside a chunk before the last of a long line",
		give:   "a\n\n— a AAAAAAAA" + strings.Repeat("A", 506) + "==" + strings.Repeat("A", 8) + "\n",
		detail: shortEncoding,
	},
	{
		name:   "returns ErrNote for padding at the end of a chunk before the last of a long line",
		give:   "a\n\n— a AAAAAAAA" + strings.Repeat("A", 510) + "==" + strings.Repeat("A", 4) + "\n",
		detail: shortEncoding,
	},
	{
		name:   "returns ErrNote for spare bits in the last chunk of a long line",
		give:   "a\n\n— a AAAAAAAA" + strings.Repeat("A", 1020) + "AB==\n",
		detail: shortEncoding,
	},
	{
		name:   "returns ErrNote for a long line that is not base64 in its first chunk",
		give:   "a\n\n— a AAAAAAAA!" + strings.Repeat("A", 1023) + "\n",
		detail: shortEncoding,
	},
	{
		name:   "returns ErrNote for the first line without the em dash after a line that is not base64",
		give:   peterText + "\n— PeterNeumann !!!!\n- PeterNeumann " + peterValue + "\n",
		detail: noPrefix,
	},
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

// renamedSigner is a note.Signer whose key has the name that it is given.
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

func TestNote(t *testing.T) {
	t.Parallel()

	t.Run("Parse", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the note of signed-note's example", func(t *testing.T) {
			t.Parallel()
			n := mustParse(t, exampleNote)
			assert.Length(t, n.Signatures, 1, "the example must have one signature line")
			expect.Equal(t, string(n.Text), exampleText, "Parse must return the text with its newline")
			expect.Equal(t, n.Signatures[0].Name, note.Name(exampleName), "Parse must return the key name")
			expect.Equal(t, n.Signatures[0].ID, uint32(exampleKeyID), "Parse must return the key ID")
			expect.Length(t, n.Signatures[0].Value, stded25519.SignatureSize,
				"Parse must return the bytes of the Ed25519 signature")
		})

		t.Run("splits a note at its last blank line", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, string(mustParse(t, "a\n\nb\n\n"+peterLine).Text), "a\n\nb\n",
				"the blank line of the text must remain in the text")
		})

		t.Run("returns a text of one newline", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, string(mustParse(t, "\n\n"+peterLine).Text), "\n", "Parse must return the newline")
		})

		t.Run("keeps every line in the order of the note", func(t *testing.T) {
			t.Parallel()
			n := mustParse(t, peterNote+peterLine+enochLine)
			assert.Length(t, n.Signatures, 3, "Parse must keep every line")
			expect.Equal(t, n.Signatures[1], n.Signatures[0], "Parse must keep a repeated line")
			expect.Equal(t, n.Signatures[2].Name, note.Name("EnochRoot"), "Parse must keep the order of the lines")
		})

		t.Run("returns a line of a key ID with one byte of signature", func(t *testing.T) {
			t.Parallel()
			n := mustParse(t, "a\n\n— a AAAAAAA=\n")
			assert.Length(t, n.Signatures, 1, "the note must have one signature line")
			assert.Length(t, n.Signatures[0].Value, 1, "Parse must return the byte of the signature")
		})

		t.Run("returns a name of the characters from U+007F to U+009F", func(t *testing.T) {
			t.Parallel()
			n := mustParse(t, "a\u007f\u0080\u009f\n\n— a\u007f\u0080\u009f AAAAAAA=\n")
			assert.Length(t, n.Signatures, 1, "the note must have one signature line")
			assert.Equal(t, n.Signatures[0].Name, note.Name("a\u007f\u0080\u009f"), "Parse must return the name")
		})

		t.Run("returns values that do not alias msg", func(t *testing.T) {
			t.Parallel()
			msg := []byte(peterNote)
			n, err := note.Parse(msg)
			assert.NoError(t, err, "Parse must accept the note")
			want := bytes.Clone(n.Signatures[0].Value)
			clear(msg)
			assert.Equal(t, n.Signatures[0].Value, want, "a change to msg must not change a value")
		})

		t.Run("returns a value whose append leaves the next value unchanged", func(t *testing.T) {
			t.Parallel()
			n := mustParse(t, peterNote+enochLine)
			want := bytes.Clone(n.Signatures[1].Value)
			_ = append(n.Signatures[0].Value, 0xff)
			assert.Equal(t, n.Signatures[1].Value, want, "an append to a value must copy it")
		})

		t.Run("returns the note that AppendText writes", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, func(n note.Note) ([]byte, error) { return n.AppendText(nil) }, note.Parse,
				"Parse must return the note that AppendText writes", prop.Using(notes))
		})

		t.Run("returns only notes that AppendText writes back byte for byte", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Parse must return only notes that AppendText writes back byte for byte",
				func(c *prop.Case) {
					msg := c.Draw(changedNotes, "msg")
					n, err := note.Parse(msg)
					if err != nil {
						return
					}
					c.Classify(parsedNote)

					again, err := n.AppendText(nil)
					assert.NoError(c, err, "AppendText must accept a note that Parse returns")
					assert.Equal(c, again, msg, "AppendText must write back the note that Parse returned")
				}, prop.Cases(1_000), prop.Require(parsedNote, 0.05))
		})

		for _, tt := range refusedNotes {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				n, err := note.Parse([]byte(tt.give))
				assert.ErrorIs(t, err, note.ErrNote, "Parse must refuse the note")
				expect.Equal(t, err.Error(), note.ErrNote.Error()+": "+tt.detail, "the error must name the rule")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				expect.Equal(t, n, note.Note{}, "Parse must return the zero Note with an error")
			})
		}
	})

	t.Run("Open", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the note whose lines satisfy the policy", func(t *testing.T) {
			t.Parallel()
			n, err := note.Open([]byte(exampleNote), policyOf(t, textVerifier(t, exampleKey)))
			assert.NoError(t, err, "Open must accept the example of signed-note")
			assert.Equal(t, string(n.Text), exampleText, "Open must return the text")
		})

		t.Run("returns the error of Parse", func(t *testing.T) {
			t.Parallel()
			_, err := note.Open([]byte(peterText), policyOf(t, textVerifier(t, peterKey)))
			assert.ErrorIs(t, err, note.ErrNote, "Open must return the error of Parse")
		})

		t.Run("returns the error of Check", func(t *testing.T) {
			t.Parallel()
			n, err := note.Open([]byte(peterNote), policyOf(t, textVerifier(t, enochKey)))
			expect.ErrorIs(t, err, sign.ErrThreshold, "Open must return the error of Check")
			expect.Equal(t, n, note.Note{}, "Open must return the zero Note with an error")
		})
	})

	t.Run("Sign", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the note that golang.org/x/mod's tests record", func(t *testing.T) {
			t.Parallel()
			n, err := note.Sign(t.Context(), []byte(peterText), peterSigner(t))
			assert.NoError(t, err, "Sign must sign the text")
			msg, err := n.AppendText(nil)
			assert.NoError(t, err, "AppendText must write the note")
			assert.Equal(t, string(msg), peterNote, "Sign must return the note of x/mod")
		})

		t.Run("signs with each signer in order", func(t *testing.T) {
			t.Parallel()
			pq, err := note.NewTextSigner("example.com/pq", pqType, mldsaSigner(t))
			assert.NoError(t, err, "NewTextSigner must accept the ML-DSA signer")
			n, err := note.Sign(t.Context(), []byte(peterText), peterSigner(t), pq)
			assert.NoError(t, err, "Sign must sign with both signers")
			assert.Length(t, n.Signatures, 2, "Sign must write one line per signer")
			expect.Equal(t, n.Signatures[1].Name, note.Name("example.com/pq"), "the lines must follow the signers")
			expect.Equal(t, n.Signatures[1].ID, pq.Key().ID(), "a line must name the key ID of its signer")
			expect.True(t, pq.Verify(n.Text, n.Signatures[1].Value), "the second line must verify")
		})

		t.Run("returns the error of a signer that fails", func(t *testing.T) {
			t.Parallel()
			failure := errors.New("the signer fails")
			s, err := note.NewTextSigner("a", pqType,
				fakeSigner{alg: crypto.AlgMLDSA44, pub: publicKey(), err: failure})
			assert.NoError(t, err, "NewTextSigner must accept the signer")
			_, err = note.Sign(t.Context(), []byte(peterText), s)
			assert.ErrorIs(t, err, failure, "Sign must return the error of the signer")
		})

		t.Run("returns the cause of a context that the caller cancelled", func(t *testing.T) {
			t.Parallel()
			s := peterSigner(t)
			assert.HonoursCancellation(t, func(ctx context.Context) error {
				_, err := note.Sign(ctx, []byte(peterText), s)

				return err
			}, "Sign must return the cause of a cancelled context")
		})

		t.Run("returns the cause of a context whose deadline passed", func(t *testing.T) {
			t.Parallel()
			s := peterSigner(t)
			assert.HonoursDeadline(t, func(ctx context.Context) error {
				_, err := note.Sign(ctx, []byte(peterText), s)

				return err
			}, "Sign must return the cause of a context past its deadline")
		})

		emptySigner, err := note.NewTextSigner("a", pqType, fakeSigner{alg: crypto.AlgMLDSA44, pub: publicKey()})
		assert.NoError(t, err, "NewTextSigner must accept the signer")

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
				expect.ErrorIs(t, err, tt.want, "Sign must refuse the call")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				expect.Equal(t, n, note.Note{}, "Sign must return the zero Note with an error")
			})
		}
	})

	t.Run("Note", func(t *testing.T) {
		t.Parallel()

		t.Run("Sign", func(t *testing.T) {
			t.Parallel()

			t.Run("sets n to the note that golang.org/x/mod's tests record", func(t *testing.T) {
				t.Parallel()
				var n note.Note
				assert.NoError(t, n.Sign(t.Context(), []byte(peterText), peterSigner(t)), "Sign must sign the text")
				msg, err := n.AppendText(nil)
				assert.NoError(t, err, "AppendText must write the note")
				assert.Equal(t, string(msg), peterNote, "Sign must set the note of x/mod")
			})

			t.Run("reuses the slice of lines", func(t *testing.T) {
				t.Parallel()
				s := peterSigner(t)
				var n note.Note
				assert.NoError(t, n.Sign(t.Context(), []byte(peterText), s), "Sign must sign the text")
				line := &n.Signatures[0]
				assert.NoError(t, n.Sign(t.Context(), []byte(exampleText), s), "Sign must sign the second text")
				assert.Equal(t, &n.Signatures[0], line, "Sign must reuse the slice of lines", assert.ByIdentity())
			})

			t.Run("appends each signature to the value of its line", func(t *testing.T) {
				t.Parallel()
				s := peterSigner(t)
				var n note.Note
				assert.NoError(t, n.Sign(t.Context(), []byte(peterText), s), "Sign must sign the text")
				value := &n.Signatures[0].Value[0]
				assert.NoError(t, n.Sign(t.Context(), []byte(exampleText), s), "Sign must sign the second text")
				expect.Equal(t, &n.Signatures[0].Value[0], value, "Sign must append the signature to the value",
					expect.ByIdentity())
				expect.True(t, s.Verify([]byte(exampleText), n.Signatures[0].Value), "the line must verify")
			})

			t.Run("sets n to the lines of each list of signers in turn", func(t *testing.T) {
				t.Parallel()
				peter := peterSigner(t)
				pq, err := note.NewTextSigner("example.com/pq", pqType, mldsaSigner(t))
				assert.NoError(t, err, "NewTextSigner must accept the ML-DSA signer")
				var n note.Note
				for _, signers := range [][]note.Signer{{peter}, {pq, peter}, {peter}, {peter, pq}} {
					assert.NoError(t, n.Sign(t.Context(), []byte(peterText), signers...), "Sign must sign the text")
					assert.Length(t, n.Signatures, len(signers), "Sign must write one line per signer")
					for i, s := range signers {
						expect.Equal(t, n.Signatures[i].Name, s.Key().Name, "the lines must follow the signers")
						expect.True(t, s.Verify(n.Text, n.Signatures[i].Value), "each line must verify")
					}
				}
			})

			t.Run("empties n with the error of a signer", func(t *testing.T) {
				t.Parallel()
				failure := errors.New("the signer fails")
				s, err := note.NewTextSigner("a", pqType,
					fakeSigner{alg: crypto.AlgMLDSA44, pub: publicKey(), err: failure})
				assert.NoError(t, err, "NewTextSigner must accept the signer")
				n := mustParse(t, peterNote)
				assert.ErrorIs(t, n.Sign(t.Context(), []byte(peterText), peterSigner(t), s), failure,
					"Sign must return the error of the signer")
				assertEmpty(t, n)
			})

			t.Run("empties n for a text without its last newline", func(t *testing.T) {
				t.Parallel()
				n := mustParse(t, peterNote)
				assert.ErrorIs(t, n.Sign(t.Context(), []byte("a"), peterSigner(t)), note.ErrNote,
					"Sign must refuse the text")
				assertEmpty(t, n)
			})

			t.Run("signs into the memory of the note that an error emptied", func(t *testing.T) {
				t.Parallel()
				s := peterSigner(t)
				var n note.Note
				assert.NoError(t, n.Sign(t.Context(), []byte(peterText), s), "Sign must sign the text")
				value := &n.Signatures[0].Value[0]
				assert.ErrorIs(t, n.Sign(t.Context(), []byte("a"), s), note.ErrNote, "Sign must refuse the text")
				assert.NoError(t, n.Sign(t.Context(), []byte(exampleText), s), "Sign must sign the second text")
				assert.Equal(t, &n.Signatures[0].Value[0], value, "Sign must reuse the value that the error kept",
					assert.ByIdentity())
			})
		})
	})

	t.Run("Check", func(t *testing.T) {
		t.Parallel()

		t.Run("returns nil for golang.org/x/mod's note under the key of PeterNeumann", func(t *testing.T) {
			t.Parallel()
			assert.NoError(t, mustParse(t, peterNote).Check(policyOf(t, textVerifier(t, peterKey))),
				"Check must accept the signature of x/mod")
		})

		t.Run("returns nil for the note of two keys under both keys", func(t *testing.T) {
			t.Parallel()
			p := policyOf(t, textVerifier(t, peterKey), textVerifier(t, enochKey))
			assert.NoError(t, mustParse(t, peterNote+enochLine).Check(p), "Check must accept both signatures")
		})

		t.Run("returns ErrThreshold for a note that the key did not sign", func(t *testing.T) {
			t.Parallel()
			err := mustParse(t, peterNote).Check(policyOf(t, textVerifier(t, enochKey)))
			expect.ErrorIs(t, err, sign.ErrThreshold, "Check must refuse a note without the line of the key")
			expect.Equal(t, errs.Classify(err), errs.Integrity, "the error must classify as Integrity")
		})

		t.Run("returns ErrThreshold for a signature over another text", func(t *testing.T) {
			t.Parallel()
			err := mustParse(t, "another text\n\n"+peterLine).Check(policyOf(t, textVerifier(t, peterKey)))
			assert.ErrorIs(t, err, sign.ErrThreshold, "Check must refuse a signature over another text")
		})

		t.Run("returns ErrPolicy for the zero Policy", func(t *testing.T) {
			t.Parallel()
			assert.ErrorIs(t, mustParse(t, peterNote).Check(sign.Policy{}), sign.ErrPolicy,
				"Check must return the error of the zero Policy")
		})

		t.Run("verifies one line of a key whose line repeats a thousand times", func(t *testing.T) {
			t.Parallel()
			calls := &atomic.Int64{}
			v := countingVerifier{Verifier: textVerifier(t, peterKey), calls: calls}
			n := mustParse(t, peterNote+strings.Repeat(peterLine, repeats-1))
			assert.NoError(t, n.Check(policyOf(t, v)), "Check must accept the note")
			assert.Equal(t, calls.Load(), int64(1), "Check must verify one line of the key")
		})

		t.Run("ignores a line of the name of a key under another key ID", func(t *testing.T) {
			t.Parallel()
			calls := &atomic.Int64{}
			v := countingVerifier{Verifier: textVerifier(t, peterKey), calls: calls}
			n := mustParse(t, peterNote)
			n.Signatures[0].ID++
			expect.ErrorIs(t, n.Check(policyOf(t, v)), sign.ErrThreshold, "Check must not count the line")
			expect.Equal(t, calls.Load(), int64(0), "Check must not verify the line of another key")
		})

		t.Run("ignores a line of the key ID of a key under another name", func(t *testing.T) {
			t.Parallel()
			calls := &atomic.Int64{}
			v := countingVerifier{Verifier: textVerifier(t, peterKey), calls: calls}
			n := mustParse(t, peterNote)
			n.Signatures[0].Name = "PeterNeumann2"
			expect.ErrorIs(t, n.Check(policyOf(t, v)), sign.ErrThreshold, "Check must not count the line")
			expect.Equal(t, calls.Load(), int64(0), "Check must not verify the line of another key")
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
			assert.True(t, ok, "Find must find the line of the key")
			assert.Equal(t, got, n.Signatures[0], "Find must return the first line of the key")
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
				expect.False(t, ok, "Find must not find a line of the key")
				expect.Equal(t, got, note.Signature{}, "Find must return the zero Signature")
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
				assert.Equal(t, tt.give.Valid(), tt.want, "Valid must report whether the note is well formed")
			})
		}
	})

	t.Run("AppendText", func(t *testing.T) {
		t.Parallel()

		t.Run("appends golang.org/x/mod's note", func(t *testing.T) {
			t.Parallel()
			got, err := mustParse(t, peterNote+enochLine).AppendText([]byte("note:"))
			assert.NoError(t, err, "AppendText must accept a valid note")
			assert.Equal(t, string(got), "note:"+peterNote+enochLine, "AppendText must write the note back")
		})

		t.Run("returns ErrNote with b unchanged for a Note that is not Valid", func(t *testing.T) {
			t.Parallel()
			got, err := (&note.Note{Text: []byte("a\n")}).AppendText([]byte("note:"))
			expect.ErrorIs(t, err, note.ErrNote, "AppendText must refuse a note without a line")
			expect.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
			expect.Equal(t, string(got), "note:", "AppendText must return b unchanged")
		})
	})

	t.Run("MarshalText", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the note that AppendText appends", func(t *testing.T) {
			t.Parallel()
			got, err := mustParse(t, peterNote+enochLine).MarshalText()
			assert.NoError(t, err, "MarshalText must accept a valid note")
			assert.Equal(t, string(got), peterNote+enochLine, "MarshalText must return the note")
		})

		t.Run("returns a note whose capacity is its length", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "MarshalText must return a note whose capacity is its length", func(c *prop.Case) {
				n := c.Draw(notes, "note")
				got, err := n.MarshalText()
				assert.NoError(c, err, "MarshalText must accept a valid note")
				assert.Equal(c, cap(got), len(got), "MarshalText must allocate the note at its length")
			})
		})

		t.Run("returns nil with ErrNote for a Note that is not Valid", func(t *testing.T) {
			t.Parallel()
			got, err := (&note.Note{Text: []byte("a\n")}).MarshalText()
			expect.ErrorIs(t, err, note.ErrNote, "MarshalText must refuse a note without a line")
			expect.Nil(t, got, "MarshalText must return nil with an error")
		})
	})

	t.Run("UnmarshalText", func(t *testing.T) {
		t.Parallel()

		t.Run("sets n to the note that Parse returns", func(t *testing.T) {
			t.Parallel()
			var n note.Note
			assert.NoError(t, n.UnmarshalText([]byte(peterNote+enochLine)), "UnmarshalText must accept the note")
			assert.Equal(t, &n, mustParse(t, peterNote+enochLine), "UnmarshalText must set the note of Parse")
		})

		t.Run("reuses the slice of lines", func(t *testing.T) {
			t.Parallel()
			msg := []byte(peterNote + enochLine)
			var n note.Note
			assert.NoError(t, n.UnmarshalText(msg), "UnmarshalText must accept the note")
			line := &n.Signatures[1]
			assert.NoError(t, n.UnmarshalText(msg), "UnmarshalText must accept the note again")
			assert.Equal(t, &n.Signatures[1], line, "UnmarshalText must reuse the slice of lines", assert.ByIdentity())
		})

		t.Run("decodes each signature into the value of its line", func(t *testing.T) {
			t.Parallel()
			msg := []byte(peterNote + enochLine)
			var n note.Note
			assert.NoError(t, n.UnmarshalText(msg), "UnmarshalText must accept the note")
			value := &n.Signatures[1].Value[0]
			assert.NoError(t, n.UnmarshalText(msg), "UnmarshalText must accept the note again")
			assert.Equal(t, &n.Signatures[1].Value[0], value, "UnmarshalText must decode into the value of the line",
				assert.ByIdentity())
		})

		t.Run("sets the name of a line after a line whose name it reuses", func(t *testing.T) {
			t.Parallel()
			n := mustParse(t, peterNote+enochLine)
			assert.NoError(t, n.UnmarshalText([]byte(peterNote+exampleLine)), "UnmarshalText must accept the note")
			assert.Equal(t, n, mustParse(t, peterNote+exampleLine), "UnmarshalText must set the name of each line")
		})

		t.Run("sets n to each note in turn", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "UnmarshalText must set n to each note in turn", func(c *prop.Case) {
				var n note.Note
				for _, want := range c.Draw(prop.List(pooledNotes, prop.MinSize(1), prop.MaxSize(4)), "notes") {
					msg, err := want.AppendText(nil)
					assert.NoError(c, err, "AppendText must accept a valid note")
					assert.NoError(c, n.UnmarshalText(msg), "UnmarshalText must accept the note that AppendText writes")
					assert.Equal(c, n, want, "UnmarshalText must set the note that AppendText wrote")
				}
			})
		})

		t.Run("decodes into the memory of the note that an error emptied", func(t *testing.T) {
			t.Parallel()
			msg := []byte(peterNote + enochLine)
			var n note.Note
			assert.NoError(t, n.UnmarshalText(msg), "UnmarshalText must accept the note")
			value := &n.Signatures[1].Value[0]
			assert.ErrorIs(t, n.UnmarshalText([]byte(peterText)), note.ErrNote, "UnmarshalText must refuse the text")
			assert.NoError(t, n.UnmarshalText(msg), "UnmarshalText must accept the note again")
			assert.Equal(t, &n.Signatures[1].Value[0], value, "UnmarshalText must reuse the value that the error kept",
				assert.ByIdentity())
		})

		t.Run("decodes a line whose signature outgrows its value", func(t *testing.T) {
			t.Parallel()
			n := mustParse(t, "a\n\n— a AAAAAAA=\n")
			assert.NoError(t, n.UnmarshalText([]byte(peterNote)), "UnmarshalText must accept the note")
			assert.Equal(t, n, mustParse(t, peterNote), "UnmarshalText must set the longer signature")
		})

		for _, tt := range refusedNotes {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				n := mustParse(t, peterNote)
				err := n.UnmarshalText([]byte(tt.give))
				assert.ErrorIs(t, err, note.ErrNote, "UnmarshalText must refuse the note")
				expect.Equal(t, err.Error(), note.ErrNote.Error()+": "+tt.detail, "the error must name the rule")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				assertEmpty(t, n)
			})
		}
	})

	t.Run("TextOf", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the text of signed-note's example", func(t *testing.T) {
			t.Parallel()
			text, err := note.TextOf([]byte(exampleNote))
			assert.NoError(t, err, "TextOf must accept the example of signed-note")
			assert.Equal(t, string(text), exampleText, "TextOf must return the text with its newline")
		})

		t.Run("returns a subslice of msg", func(t *testing.T) {
			t.Parallel()
			msg := []byte(peterNote)
			text, err := note.TextOf(msg)
			assert.NoError(t, err, "TextOf must accept the note")
			assert.Equal(t, &text[0], &msg[0], "the text must alias msg", assert.ByIdentity())
		})

		values := []struct {
			name string
			size int
		}{
			{name: "returns the text of a note whose line is longer than a chunk", size: 1000},
			{name: "returns the text of a note whose line ends a chunk in padding", size: 385},
			{name: "returns the text of a note whose line fills two chunks without padding", size: 770},
		}
		for _, tt := range values {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				n := note.Note{
					Text:       []byte(peterText),
					Signatures: []note.Signature{{Name: "a", Value: bytes.Repeat([]byte{0xa5}, tt.size), ID: 7}},
				}
				msg, err := n.AppendText(nil)
				assert.NoError(t, err, "AppendText must accept the note")

				text, err := note.TextOf(msg)
				assert.NoError(t, err, "TextOf must accept a line of "+strconv.Itoa(tt.size)+" bytes")
				assert.Equal(t, string(text), peterText, "TextOf must return the text")
			})
		}

		t.Run("returns the result of Parse for every message", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, agreesWithParse, textOfAgrees)
		})

		for _, tt := range refusedNotes {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				text, err := note.TextOf([]byte(tt.give))
				assert.ErrorIs(t, err, note.ErrNote, "TextOf must refuse the note")
				expect.Equal(t, err.Error(), note.ErrNote.Error()+": "+tt.detail, "the error must name the rule")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				expect.Nil(t, text, "TextOf must return no text with an error")
			})
		}
	})
}

// TestNoteAllocs checks the allocation contract of each function and
// method. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
//
//nolint:paralleltest // see above
func TestNoteAllocs(t *testing.T) {
	msg := []byte(peterNote)
	three := []byte(peterNote + peterLine + enochLine)
	text := []byte(peterText)
	n := mustParse(t, peterNote)
	p := policyOf(t, textVerifier(t, peterKey))
	s := peterSigner(t)
	k := mustParseKey(t, peterKey)

	t.Run("Parse", func(t *testing.T) {
		t.Run("of a note of one line", func(t *testing.T) {
			var err error
			expect.MaxAllocs(t, func() { _, err = note.Parse(msg) }, 3,
				"Parse must allocate the lines, the values and the names alone")
			assert.NoError(t, err, "the test must measure a note that Parse accepts")
		})

		t.Run("of a note of three lines", func(t *testing.T) {
			var err error
			expect.MaxAllocs(t, func() { _, err = note.Parse(three) }, 3,
				"Parse must allocate the lines, the values and the names alone")
			assert.NoError(t, err, "the test must measure a note that Parse accepts")
		})
	})

	t.Run("Open", func(t *testing.T) {
		var err error
		expect.MaxAllocs(t, func() { _, err = note.Open(msg, p) }, 3, "Open must allocate what Parse allocates alone")
		assert.NoError(t, err, "the test must measure a note that Open accepts")
	})

	t.Run("Sign", func(t *testing.T) {
		ctx := t.Context()

		var err error
		expect.MaxAllocs(t, func() { _, err = note.Sign(ctx, text, s) }, 2,
			"Sign must allocate the lines and the value alone")
		assert.NoError(t, err, "the test must measure a text that Sign signs")
	})

	t.Run("Note", func(t *testing.T) {
		t.Run("Sign", func(t *testing.T) {
			ctx := t.Context()

			var reused note.Note
			assert.NoError(t, reused.Sign(ctx, text, s), "Sign must sign the text")

			var err error
			expect.MaxAllocs(t, func() { err = reused.Sign(ctx, text, s) }, 0,
				"Sign must not allocate into a Note with room")
			assert.NoError(t, err, "the test must measure a text that Sign signs")
		})
	})

	t.Run("Check", func(t *testing.T) {
		var err error
		expect.MaxAllocs(t, func() { err = n.Check(p) }, 0, "Check must not allocate")
		assert.NoError(t, err, "the test must measure a note that the policy accepts")
	})

	t.Run("Find", func(t *testing.T) {
		var ok bool
		expect.MaxAllocs(t, func() { _, ok = n.Find(k) }, 0, "Find must not allocate")
		assert.True(t, ok, "the test must measure a key that the note has a line of")
	})

	t.Run("Valid", func(t *testing.T) {
		var got bool
		expect.MaxAllocs(t, func() { got = n.Valid() }, 0, "Valid must not allocate")
		assert.True(t, got, "the test must measure a valid note")
	})

	t.Run("AppendText", func(t *testing.T) {
		buf := make([]byte, 0, len(peterNote))

		var err error
		expect.MaxAllocs(t, func() { _, err = n.AppendText(buf[:0]) }, 0,
			"AppendText must not allocate into a buffer with room")
		assert.NoError(t, err, "the test must measure a valid note")
	})

	t.Run("MarshalText", func(t *testing.T) {
		var err error
		expect.MaxAllocs(t, func() { _, err = n.MarshalText() }, 1, "MarshalText must allocate the note alone")
		assert.NoError(t, err, "the test must measure a valid note")
	})

	t.Run("UnmarshalText", func(t *testing.T) {
		var reused note.Note
		assert.NoError(t, reused.UnmarshalText(three), "UnmarshalText must accept the note")

		var err error
		expect.MaxAllocs(t, func() { err = reused.UnmarshalText(three) }, 0,
			"UnmarshalText must not allocate into a Note of the same lines")
		assert.NoError(t, err, "the test must measure a note that UnmarshalText accepts")
	})

	t.Run("TextOf", func(t *testing.T) {
		t.Run("of notes of other key names", func(t *testing.T) {
			others := [][]byte{msg, three, []byte(exampleNote)}
			i := 0

			var err error
			expect.MaxAllocs(t, func() {
				_, err = note.TextOf(others[i%len(others)])
				i++
			}, 0, "TextOf must not allocate")
			assert.NoError(t, err, "the test must measure notes that TextOf accepts")
		})

		t.Run("of a line of 4627 bytes", func(t *testing.T) {
			long, err := (&note.Note{
				Text:       text,
				Signatures: []note.Signature{{Name: "a", Value: make([]byte, 4627), ID: 7}},
			}).AppendText(nil)
			assert.NoError(t, err, "AppendText must accept the note")

			expect.MaxAllocs(t, func() { _, err = note.TextOf(long) }, 0, "TextOf must not allocate")
			assert.NoError(t, err, "the test must measure a note that TextOf accepts")
		})
	})
}

// BenchmarkNote reports the cost of each function and method, and fails
// above the allocations that their contracts state.
func BenchmarkNote(b *testing.B) {
	msg := []byte(peterNote)
	three := []byte(peterNote + peterLine + enochLine)
	text := []byte(peterText)
	n := mustParse(b, peterNote)
	p := policyOf(b, textVerifier(b, peterKey))
	s := peterSigner(b)
	k := mustParseKey(b, peterKey)

	b.Run("Parse", func(b *testing.B) {
		b.Run("of a note of one line", func(b *testing.B) {
			var err error

			c := bench.Start(b).MaxAllocs(3)
			defer c.End()

			for c.Loop() {
				_, err = note.Parse(msg)
			}

			assert.NoError(b, err, "the benchmark must measure a note that Parse accepts")
		})

		b.Run("of a note of three lines", func(b *testing.B) {
			var err error

			c := bench.Start(b).MaxAllocs(3)
			defer c.End()

			for c.Loop() {
				_, err = note.Parse(three)
			}

			assert.NoError(b, err, "the benchmark must measure a note that Parse accepts")
		})
	})

	b.Run("Open", func(b *testing.B) {
		var err error

		c := bench.Start(b).MaxAllocs(3)
		defer c.End()

		for c.Loop() {
			_, err = note.Open(msg, p)
		}

		assert.NoError(b, err, "the benchmark must measure a note that Open accepts")
	})

	b.Run("Sign", func(b *testing.B) {
		ctx := b.Context()

		var err error

		c := bench.Start(b).MaxAllocs(2)
		defer c.End()

		for c.Loop() {
			_, err = note.Sign(ctx, text, s)
		}

		assert.NoError(b, err, "the benchmark must measure a text that Sign signs")
	})

	b.Run("Note", func(b *testing.B) {
		b.Run("Sign", func(b *testing.B) {
			ctx := b.Context()

			var reused note.Note
			assert.NoError(b, reused.Sign(ctx, text, s), "Sign must sign the text")

			var err error

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				err = reused.Sign(ctx, text, s)
			}

			assert.NoError(b, err, "the benchmark must measure a text that Sign signs")
		})
	})

	b.Run("Check", func(b *testing.B) {
		var err error

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			err = n.Check(p)
		}

		assert.NoError(b, err, "the benchmark must measure a note that the policy accepts")
	})

	b.Run("Find", func(b *testing.B) {
		var ok bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			_, ok = n.Find(k)
		}

		assert.True(b, ok, "the benchmark must measure a key that the note has a line of")
	})

	b.Run("Valid", func(b *testing.B) {
		var got bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = n.Valid()
		}

		assert.True(b, got, "the benchmark must measure a valid note")
	})

	b.Run("AppendText", func(b *testing.B) {
		buf := make([]byte, 0, len(peterNote))

		var err error

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			_, err = n.AppendText(buf[:0])
		}

		assert.NoError(b, err, "the benchmark must measure a valid note")
	})

	b.Run("MarshalText", func(b *testing.B) {
		var err error

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			_, err = n.MarshalText()
		}

		assert.NoError(b, err, "the benchmark must measure a valid note")
	})

	b.Run("UnmarshalText", func(b *testing.B) {
		var reused note.Note
		assert.NoError(b, reused.UnmarshalText(three), "UnmarshalText must accept the note")

		var err error

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			err = reused.UnmarshalText(three)
		}

		assert.NoError(b, err, "the benchmark must measure a note that UnmarshalText accepts")
	})

	b.Run("TextOf", func(b *testing.B) {
		b.Run("of notes of other key names", func(b *testing.B) {
			others := [][]byte{msg, three, []byte(exampleNote)}
			i := 0

			var err error

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				_, err = note.TextOf(others[i%len(others)])
				i++
			}

			assert.NoError(b, err, "the benchmark must measure notes that TextOf accepts")
		})

		b.Run("of a line of 4627 bytes", func(b *testing.B) {
			long, err := (&note.Note{
				Text:       text,
				Signatures: []note.Signature{{Name: "a", Value: make([]byte, 4627), ID: 7}},
			}).AppendText(nil)
			assert.NoError(b, err, "AppendText must accept the note")

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				_, err = note.TextOf(long)
			}

			assert.NoError(b, err, "the benchmark must measure a note that TextOf accepts")
		})
	})
}

// FuzzTextOf checks textOfAgrees for the messages that the fuzzer finds.
// Each seed is a message of the first generator of messages: the index 0,
// the length of the message in two bytes, little-endian, and the message.
func FuzzTextOf(f *testing.F) {
	seeds := make([]string, 0, 2+len(refusedNotes))
	seeds = append(seeds, exampleNote, peterNote+peterLine+enochLine)
	for _, tt := range refusedNotes {
		seeds = append(seeds, tt.give)
	}

	for _, msg := range seeds {
		f.Add(append([]byte{0, byte(len(msg)), byte(len(msg) >> 8)}, msg...))
	}

	prop.Fuzz(f, agreesWithParse, textOfAgrees)
}

// textOfAgrees checks that TextOf returns the text and the error of Parse
// for a message of messages.
func textOfAgrees(c *prop.Case) {
	msg := c.Draw(messages, "msg")
	n, parseErr := note.Parse(msg)
	text, err := note.TextOf(msg)

	assert.Equal(c, err, parseErr, "TextOf must return the error of Parse")
	assert.Equal(c, text, n.Text, "TextOf must return the text of Parse")
}

// notesOf returns a generator of valid notes of a text of texts and of one
// to five signature lines, each of a name of from, a value of 1 to 80 bytes
// and any key ID.
func notesOf(from prop.Generator[note.Name]) prop.Generator[note.Note] {
	lines := prop.Composite(func(c *prop.Case) note.Signature {
		return note.Signature{
			Name:  c.Draw(from, "name"),
			Value: c.Draw(prop.Bytes(prop.MinSize(1), prop.MaxSize(80)), "value"),
			ID:    c.Draw(prop.Of[uint32](), "key ID"),
		}
	})

	return prop.Composite(func(c *prop.Case) note.Note {
		return note.Note{
			Text:       c.Draw(texts, "text"),
			Signatures: c.Draw(prop.List(lines, prop.MinSize(1), prop.MaxSize(5)), "signatures"),
		}
	})
}

// textVerifier returns the Ed25519 text Verifier of the verifier key text.
func textVerifier(tb testing.TB, text string) note.Verifier {
	tb.Helper()

	v, err := note.Text(ed25519.Resolve)(mustParseKey(tb, text))
	assert.NoError(tb, err, "Text must build the Verifier of "+text)

	return v
}

// policyOf returns the policy that every one of verifiers signs.
func policyOf(tb testing.TB, verifiers ...sign.Verifier) sign.Policy {
	tb.Helper()

	p, err := sign.NewPolicyTree(sign.AllOf("keys", verifiers...))
	assert.NoError(tb, err, "NewPolicyTree must accept the keys")

	return p
}

// mustParse returns the note of msg, and fails the test when Parse refuses
// it.
func mustParse(tb testing.TB, msg string) *note.Note {
	tb.Helper()

	n, err := note.Parse([]byte(msg))
	assert.NoError(tb, err, "Parse must accept the note")

	return &n
}

// assertEmpty checks that n has no text and no line, as a Note that an
// error emptied has neither.
func assertEmpty(tb testing.TB, n *note.Note) {
	tb.Helper()

	expect.Nil(tb, n.Text, "the Note must have no text")
	expect.Empty(tb, n.Signatures, "the Note must have no line")
}
