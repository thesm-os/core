// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"math"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/crypto"
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
			assert.Equal(t, len(appendSize(nil, math.MaxUint64)), maxSizeBody,
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
}
