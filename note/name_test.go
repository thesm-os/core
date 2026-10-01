// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package note_test

import (
	"math/rand/v2"
	"strings"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/note"
)

// nameRunes are the characters of the names that randomName draws:
// letters, digits and punctuation of key names, a character outside
// ASCII, and the characters from U+007F to U+009F that are not spaces.
var nameRunes = []rune("abz09./:-_λ例\u007f\u0080\u009f")

// randomName returns a valid key name of 1 to 40 characters.
func randomName(r *rand.Rand) note.Name {
	var b strings.Builder
	for range 1 + r.IntN(40) {
		b.WriteRune(nameRunes[r.IntN(len(nameRunes))])
	}

	return note.Name(b.String())
}

// randomBytes returns from minLen to maxLen random bytes.
func randomBytes(r *rand.Rand, minLen, maxLen int) []byte {
	b := make([]byte, minLen+r.IntN(maxLen-minLen+1))
	for i := range b {
		b[i] = byte(r.Uint32())
	}

	return b
}

func TestName(t *testing.T) {
	t.Parallel()

	t.Run("Valid", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give note.Name
			want bool
		}{
			{name: "reports true for a schema-less URL", give: "example.com/log42", want: true},
			{name: "reports true for a name of letters outside ASCII", give: "例え.example/λ", want: true},
			{name: "reports true for a name with a colon", give: "transparency.dev/DEV:witness", want: true},
			{name: "reports true for a name with U+007F", give: "a\u007fb", want: true},
			{name: "reports true for a name with U+0080", give: "a\u0080b", want: true},
			{name: "reports true for a name with U+009F", give: "a\u009fb", want: true},
			{name: "reports false for the empty name", give: "", want: false},
			{name: "reports false for a name with a plus", give: "a+b", want: false},
			{name: "reports false for a name with a space", give: "a b", want: false},
			{name: "reports false for a name with a no-break space", give: "a b", want: false},
			{name: "reports false for a name with U+0085", give: "a\u0085b", want: false},
			{name: "reports false for a name with a line separator", give: "a b", want: false},
			{name: "reports false for a name that is not valid UTF-8", give: "a\xffb", want: false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(t, tt.give.Valid(), tt.want, "Valid must report whether the name is a key name")
			})
		}

		t.Run("reports false for a name with a character below U+0020", func(t *testing.T) {
			t.Parallel()
			for c := range rune(0x20) {
				testkit.False(t, note.Name("a"+string(c)+"b").Valid(),
					"Valid must refuse the character "+string(c))
			}
		})

		t.Run("reports true for random names of the characters of key names", func(t *testing.T) {
			t.Parallel()
			r := testkit.SeededRand(t)
			for range 500 {
				name := randomName(r)
				testkit.True(t, name.Valid(), "Valid must accept "+string(name))
			}
		})
	})
}

func BenchmarkName(b *testing.B) {
	b.Run("Valid", func(b *testing.B) {
		name := note.Name("example.com/log42")
		benchZeroAlloc(b, func() { sinkBool = name.Valid() })
	})
}
