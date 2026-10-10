// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"math"
	"slices"
	"strings"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/note"
)

// The proof lines and the note of the example request of tlog-witness,
// which the tests parse.
const (
	exampleProof1 = "PlRNCrwHpqhGrupue0L7gxbjbMiKA9temvuZZDDpkaw="
	exampleProof2 = "jrJZDmY8Y7SyJE0MWLpLozkIVMSMZcD5kvuKxPC3swk="
	exampleNote   = "example.com/behind-the-sofa\n20852163\nCsUYapGGPo4dkMgIAUqom/Xajj7h2fB2MPA3j2jxq2I=\n\n" +
		"— example.com/behind-the-sofa Az3grlgtzPICa5OS8npVmf1Myq/5IZniMp+ZJurmRDeOoRDe4URYN7u5/Zhcyv2q1gGzGku9nTo+" +
		"zyWE+xeMcTOAYQ8=\n"
	exampleRequest = "old 20852014\n" + exampleProof1 + "\n" + exampleProof2 + "\n\n" + exampleNote
)

// unknownLine is a signature line of a key that no log has: a valid name,
// and the base64 of a key ID and a signature of four bytes each.
const unknownLine = "— unknown.example AAAAAAAAAAA=\n"

func TestProtocolInternal(t *testing.T) {
	t.Parallel()

	t.Run("request", func(t *testing.T) {
		t.Parallel()

		t.Run("parse", func(t *testing.T) {
			t.Parallel()

			t.Run("sets the old size, the proof and the note of the example of tlog-witness", func(t *testing.T) {
				t.Parallel()
				var r request
				assert.NoError(t, r.parse([]byte(exampleRequest)), "parse must accept the example")
				expect.Equal(t, r.oldSize, uint64(20852014), "parse must set the old size")
				expect.Length(t, r.proof, 2, "parse must set both proof lines")
				expect.Equal(t, string(r.note), exampleNote, "parse must set the note after the blank line")
			})

			t.Run("reuses the capacity of the proof", func(t *testing.T) {
				t.Parallel()
				r := request{proof: make([]crypto.Digest, 0, 2)}
				first := &r.proof[:1][0]
				assert.NoError(t, r.parse([]byte(exampleRequest)), "parse must accept the example")
				assert.Equal(t, &r.proof[0], first, "parse must append into the proof of r", assert.ByIdentity())
			})

			t.Run("leaves the fields unchanged for a body that ParseRequest refuses", func(t *testing.T) {
				t.Parallel()
				r := request{note: []byte("a note"), oldSize: 7}
				assert.ErrorIs(t, r.parse([]byte("old 5")), ErrRequest, "parse must return the error of ParseRequest")
				expect.Equal(t, r.oldSize, uint64(7), "parse must keep the old size")
				expect.Equal(t, string(r.note), "a note", "parse must keep the note")
			})
		})
	})

	t.Run("appendSize", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the decimal and a newline", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, string(appendSize([]byte("a:"), 20852163)), "a:20852163\n",
				"appendSize must write the size and a newline")
		})

		t.Run("appends maxSizeBody bytes for the largest size", func(t *testing.T) {
			t.Parallel()
			assert.Length(t, appendSize(nil, math.MaxUint64), maxSizeBody,
				"the body of a 409 must fit an array of maxSizeBody bytes")
		})
	})

	t.Run("SizeError", func(t *testing.T) {
		t.Parallel()

		t.Run("Error", func(t *testing.T) {
			t.Parallel()

			t.Run("returns a text of maxSizeError bytes for the largest size", func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, len((&SizeError{Size: math.MaxUint64}).Error()), maxSizeError,
					"the text of a SizeError must fit an array of maxSizeError bytes")
			})
		})
	})

	t.Run("parseSizeBody", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the size of a body that appendSize writes", func(t *testing.T) {
			t.Parallel()
			size, ok := parseSizeBody(appendSize(nil, 18446744073709551615))
			assert.True(t, ok, "parseSizeBody must accept the body")
			assert.Equal(t, size, uint64(18446744073709551615), "parseSizeBody must read the size")
		})

		tests := []struct {
			name string
			give string
		}{
			{name: "reports false for a body without a newline", give: "5"},
			{name: "reports false for a size with a leading zero", give: "05\n"},
			{name: "reports false for an empty size", give: "\n"},
			{name: "reports false for two newlines", give: "5\n\n"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, ok := parseSizeBody([]byte(tt.give))
				assert.False(t, ok, "parseSizeBody must refuse the body")
			})
		}
	})

	t.Run("checkLines", func(t *testing.T) {
		t.Parallel()

		accepted := []struct {
			name string
			give string
		}{
			{
				name: "returns nil for a note of 64 signature lines",
				give: string(withLines(t, []byte(exampleNote), maxLines)),
			},
			{
				name: "returns nil for a note of 64 signature lines whose text contains a blank line",
				give: "a\n\n" + string(withLines(t, []byte(exampleNote), maxLines)),
			},
			{name: "returns nil for a msg without a blank line", give: "a\n" + strings.Repeat(unknownLine, maxLines+1)},
		}
		for _, tt := range accepted {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.NoError(t, checkLines([]byte(tt.give)), "checkLines must accept the msg")
			})
		}

		refused := []struct {
			name string
			give string
		}{
			{
				name: "returns ErrRequest for a note of 65 signature lines",
				give: string(withLines(t, []byte(exampleNote), maxLines+1)),
			},
			{
				name: "returns ErrRequest for a note of 65 signature lines whose blank line starts the note",
				give: "\n\n" + strings.Repeat(unknownLine, maxLines+1),
			},
		}
		for _, tt := range refused {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.ErrorIs(t, checkLines([]byte(tt.give)), ErrRequest, "checkLines must refuse the note")
			})
		}

		t.Run("returns ErrRequest for exactly the notes of more than 64 signature lines", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "checkLines must count the signature lines that note.Parse parses", func(c *prop.Case) {
				text := c.Draw(prop.List(prop.SampledFrom("a", "", "b c"), prop.MaxSize(4)), "text")
				lines := c.Draw(prop.Integer(0, maxLines+4), "lines")
				msg := []byte(strings.Join(text, "\n") + "\n\n" + strings.Repeat(unknownLine, lines))

				n, err := note.Parse(msg)
				c.Assume(err == nil)
				assert.Equal(c, checkLines(msg) != nil, len(n.Signatures) > maxLines,
					"checkLines must refuse a note exactly when note.Parse returns more than 64 signature lines")
			})
		})
	})
}

// withLines returns a copy of msg, a signed note, with lines of a key that
// no log has appended until the note has n signature lines.
func withLines(tb assert.TB, msg []byte, n int) []byte {
	tb.Helper()
	parsed, err := note.Parse(msg)
	assert.NoError(tb, err, "the note must parse")

	return append(slices.Clone(msg), strings.Repeat(unknownLine, n-len(parsed.Signatures))...)
}
