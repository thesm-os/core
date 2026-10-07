// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package note_test

import (
	"strconv"
	"testing"
	"unicode"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/note"
)

// nameAlphabet are the characters of the names that names generates:
// letters, digits and punctuation of key names, characters of two and three
// bytes outside ASCII, and the characters from U+007F to U+009F that are
// not spaces.
const nameAlphabet = "abz09./:-_λ例\u007f\u0080\u009f"

// names generates valid key names of 1 to 40 characters of nameAlphabet.
var names = prop.String(prop.Alphabet(nameAlphabet), prop.MinSize(1), prop.MaxSize(40)).
	Map(func(s string) note.Name { return note.Name(s) })

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
			{name: "reports false for a name that is not valid UTF-8", give: "a\xffb", want: false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, tt.give.Valid(), tt.want, "Valid must report whether the name is a key name")
			})
		}

		t.Run("reports false for a name with a character below U+0020", func(t *testing.T) {
			t.Parallel()
			for c := range rune(0x20) {
				expect.False(t, note.Name("a"+string(c)+"b").Valid(),
					"Valid must report false for "+strconv.QuoteRune(c))
			}
		})

		t.Run("reports false for a name with a character of the White_Space property", func(t *testing.T) {
			t.Parallel()
			for _, r := range unicode.White_Space.R16 {
				for c := rune(r.Lo); c <= rune(r.Hi); c += rune(r.Stride) {
					expect.False(t, note.Name("a"+string(c)+"b").Valid(),
						"Valid must report false for "+strconv.QuoteRune(c))
				}
			}
		})

		t.Run("reports true for a name of the characters of key names", func(t *testing.T) {
			t.Parallel()
			prop.True(t, note.Name.Valid, "Valid must report true for a name of the characters of key names",
				prop.Using(names))
		})
	})
}

// TestNameAllocs checks the allocation contract of Valid. MaxAllocs counts
// the allocations of the whole process, so the test does not run in
// parallel.
//
//nolint:paralleltest // see above
func TestNameAllocs(t *testing.T) {
	t.Run("Valid", func(t *testing.T) {
		name := note.Name(exampleName)

		var got bool
		expect.MaxAllocs(t, func() { got = name.Valid() }, 0, "Valid must not allocate")
		assert.True(t, got, "the test must measure a valid name")
	})
}

// BenchmarkName reports the cost of Valid, and fails above the allocations
// that its contract states.
func BenchmarkName(b *testing.B) {
	b.Run("Valid", func(b *testing.B) {
		name := note.Name(exampleName)

		var got bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = name.Valid()
		}

		assert.True(b, got, "the benchmark must measure a valid name")
	})
}
