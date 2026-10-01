// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package note_test

import (
	"fmt"
	"strings"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/note"
)

// pqIdentifier is the identifier of the 0xff type of the tests, and
// pqType the type: 0xff, the length 21, and the identifier.
const (
	pqIdentifier = "example.com/ml-dsa-87"
	pqType       = note.Type("\xff\x15" + pqIdentifier)
)

func TestType(t *testing.T) {
	t.Parallel()

	t.Run("NewType", func(t *testing.T) {
		t.Parallel()

		t.Run("returns 0xff, the length and the identifier", func(t *testing.T) {
			t.Parallel()
			got, err := note.NewType(pqIdentifier)
			testkit.NoError(t, err, "NewType must accept the identifier")
			testkit.Equal(t, got, pqType, "NewType must write 0xff, the length byte and the identifier")
		})

		t.Run("accepts an identifier of 1 byte", func(t *testing.T) {
			t.Parallel()
			got, err := note.NewType("x")
			testkit.NoError(t, err, "NewType must accept one byte")
			testkit.Equal(t, got, note.Type("\xff\x01x"), "NewType must write the length 1")
		})

		t.Run("accepts an identifier of 255 bytes", func(t *testing.T) {
			t.Parallel()
			got, err := note.NewType(strings.Repeat("x", 255))
			testkit.NoError(t, err, "NewType must accept 255 bytes")
			testkit.Equal(t, got, note.Type("\xff\xff"+strings.Repeat("x", 255)), "NewType must write the length 255")
		})

		tests := []struct {
			name string
			give string
		}{
			{name: "returns ErrType for an empty identifier", give: ""},
			{name: "returns ErrType for an identifier of 256 bytes", give: strings.Repeat("x", 256)},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := note.NewType(tt.give)
				testkit.ErrorIs(t, err, note.ErrType, "NewType must refuse the identifier")
				testkit.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				testkit.Equal(t, got, note.Type(""), "NewType must return the empty Type with an error")
			})
		}
	})

	t.Run("Valid", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give note.Type
			want bool
		}{
			{name: "reports true for type 0x01", give: note.TypeEd25519, want: true},
			{name: "reports true for any other single byte", give: "\x00", want: true},
			{name: "reports true for a type without an assigned byte", give: pqType, want: true},
			{name: "reports true for an identifier of 1 byte", give: "\xff\x01x", want: true},
			{name: "reports false for the empty type", give: "", want: false},
			{name: "reports false for a lone 0xff", give: "\xff", want: false},
			{name: "reports false for an empty identifier", give: "\xff\x00", want: false},
			{name: "reports false for an identifier shorter than its length", give: "\xff\x02x", want: false},
			{name: "reports false for an identifier longer than its length", give: "\xff\x01xy", want: false},
			{name: "reports false for two bytes without 0xff", give: "\x01\x02", want: false},
			{name: "reports false for three bytes without 0xff", give: "\x01\x01x", want: false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(t, tt.give.Valid(), tt.want, "Valid must report whether the type is well formed")
			})
		}
	})

	t.Run("String", func(t *testing.T) {
		t.Parallel()

		t.Run("returns 0x and two lowercase hexadecimal digits for each type of one byte", func(t *testing.T) {
			t.Parallel()
			for b := range 256 {
				testkit.Equal(t, note.Type([]byte{byte(b)}).String(), fmt.Sprintf("0x%02x", b),
					"String must spell the byte of the type")
			}
		})

		tests := []struct {
			name string
			give note.Type
			want string
		}{
			{name: "returns 0x01 for type 0x01", give: note.TypeEd25519, want: "0x01"},
			{name: "returns the identifier for a type without an assigned byte", give: pqType, want: pqIdentifier},
			{name: "returns the identifier of one byte", give: "\xff\x01x", want: "x"},
			{name: "returns 0xff for a lone 0xff", give: "\xff", want: "0xff"},
			{name: "returns the bytes in hexadecimal for an empty identifier", give: "\xff\x00", want: "0xff00"},
			{name: "returns the bytes in hexadecimal for two bytes without 0xff", give: "\x01\x02", want: "0x0102"},
			{name: "returns 0x for the empty type", give: "", want: "0x"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(t, tt.give.String(), tt.want, "String must return the diagnostic form")
			})
		}
	})
}

func BenchmarkType(b *testing.B) {
	b.Run("NewType", func(b *testing.B) {
		benchAllocs(b, 1, func() { sinkType, errSink = note.NewType(pqIdentifier) })
	})

	b.Run("Valid", func(b *testing.B) {
		benchZeroAlloc(b, func() { sinkBool = pqType.Valid() })
	})

	b.Run("String", func(b *testing.B) {
		benchZeroAlloc(b, func() { sinkString = note.TypeEd25519.String() })
	})

	b.Run("String of a type without an assigned byte", func(b *testing.B) {
		benchZeroAlloc(b, func() { sinkString = pqType.String() })
	})
}
