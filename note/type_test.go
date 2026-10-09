// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package note_test

import (
	"fmt"
	"strings"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/note"
)

// The bytes of a signature type that the tests pin.
const (
	// typeOther is the type byte that signed-note reserves for the types
	// that it assigns no byte.
	typeOther = 0xff

	// maxIdentifier is the length of the longest identifier of a type
	// without an assigned byte, the largest value of its length byte.
	maxIdentifier = 255
)

// pqIdentifier is the identifier of the type without an assigned byte of
// the tests, and pqType the type: 0xff, the length 21, and the identifier.
const (
	pqIdentifier = "example.com/ml-dsa-87"
	pqType       = note.Type("\xff\x15" + pqIdentifier)
)

// The generators of the properties of Type.
var (
	// identifiers generates the identifiers of 1 to 255 bytes that NewType
	// accepts.
	identifiers = prop.Bytes(prop.MinSize(1), prop.MaxSize(maxIdentifier))

	// types generates valid types: an assigned byte from 0x00 to 0xfe, or
	// the type that NewType returns for an identifier of identifiers.
	types = prop.OneOf(
		prop.Integer[byte](0, typeOther-1).Map(func(b byte) note.Type { return note.Type([]byte{b}) }),
		prop.Composite(func(c *prop.Case) note.Type {
			typ, err := note.NewType(string(c.Draw(identifiers, "identifier")))
			assert.NoError(c, err, "NewType must accept an identifier of 1 to 255 bytes")

			return typ
		}),
	)
)

func TestType(t *testing.T) {
	t.Parallel()

	t.Run("NewType", func(t *testing.T) {
		t.Parallel()

		accepted := []struct {
			name string
			give string
			want note.Type
		}{
			{name: "returns the type of the identifier of the tests", give: pqIdentifier, want: pqType},
			{name: "returns the type of an identifier of 1 byte", give: "x", want: "\xff\x01x"},
			{
				name: "returns the type of an identifier of 255 bytes",
				give: strings.Repeat("x", maxIdentifier),
				want: note.Type("\xff\xff" + strings.Repeat("x", maxIdentifier)),
			},
		}
		for _, tt := range accepted {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := note.NewType(tt.give)
				assert.NoError(t, err, "NewType must accept the identifier")
				assert.Equal(t, got, tt.want, "NewType must return 0xff, the length byte and the identifier")
			})
		}

		t.Run("returns 0xff ‖ len(identifier) ‖ identifier", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "NewType must return 0xff, the length byte and the identifier", func(c *prop.Case) {
				id := c.Draw(identifiers, "identifier")
				got, err := note.NewType(string(id))
				assert.NoError(c, err, "NewType must accept an identifier of 1 to 255 bytes")
				assert.Equal(c, got, note.Type(append([]byte{typeOther, byte(len(id))}, id...)),
					"NewType must return 0xff, the length byte and the identifier")
			})
		})

		refused := []struct {
			name string
			give string
		}{
			{name: "returns ErrType for an empty identifier", give: ""},
			{name: "returns ErrType for an identifier of 256 bytes", give: strings.Repeat("x", maxIdentifier+1)},
		}
		for _, tt := range refused {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := note.NewType(tt.give)
				expect.ErrorIs(t, err, note.ErrType, "NewType must refuse the identifier")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				expect.Equal(t, got, note.Type(""), "NewType must return the empty Type with an error")
			})
		}
	})

	t.Run("Valid", func(t *testing.T) {
		t.Parallel()

		t.Run("reports whether a type of one byte is not 0xff", func(t *testing.T) {
			t.Parallel()
			for b := range 256 {
				expect.Equal(t, note.Type([]byte{byte(b)}).Valid(), b != typeOther,
					fmt.Sprintf("Valid must report whether the byte 0x%02x is a type", b))
			}
		})

		t.Run("reports true for every type that NewType returns", func(t *testing.T) {
			t.Parallel()
			prop.True(t, note.Type.Valid, "Valid must report true for every type that NewType returns",
				prop.Using(types))
		})

		tests := []struct {
			name string
			give note.Type
			want bool
		}{
			{name: "reports true for an identifier of 1 byte", give: "\xff\x01x", want: true},
			{name: "reports false for the empty type", give: "", want: false},
			{name: "reports false for an empty identifier", give: "\xff\x00", want: false},
			{name: "reports false for an identifier shorter than its length", give: "\xff\x02x", want: false},
			{name: "reports false for an identifier longer than its length", give: "\xff\x01xy", want: false},
			{name: "reports false for two bytes without 0xff", give: "\x01\x02", want: false},
			{name: "reports false for three bytes without 0xff", give: "\x01\x01x", want: false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, tt.give.Valid(), tt.want, "Valid must report whether the type is well formed")
			})
		}
	})

	t.Run("String", func(t *testing.T) {
		t.Parallel()

		t.Run("returns 0x followed by two lowercase hexadecimal digits for a type of one byte", func(t *testing.T) {
			t.Parallel()
			for b := range 256 {
				expect.Equal(t, note.Type([]byte{byte(b)}).String(), fmt.Sprintf("0x%02x", b),
					"String must spell the byte of the type")
			}
		})

		t.Run("returns the identifier of a type that NewType returns", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "String must return the identifier of a type that NewType returns", func(c *prop.Case) {
				id := c.Draw(identifiers, "identifier")
				typ, err := note.NewType(string(id))
				assert.NoError(c, err, "NewType must accept an identifier of 1 to 255 bytes")
				assert.Equal(c, typ.String(), string(id), "String must return the identifier")
			})
		})

		tests := []struct {
			name string
			give note.Type
			want string
		}{
			{name: "returns the identifier of the type of the tests", give: pqType, want: pqIdentifier},
			{name: "returns the bytes in hexadecimal for an empty identifier", give: "\xff\x00", want: "0xff00"},
			{name: "returns the bytes in hexadecimal for two bytes without 0xff", give: "\x01\x02", want: "0x0102"},
			{name: "returns 0x for the empty type", give: "", want: "0x"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, tt.give.String(), tt.want, "String must return the diagnostic form")
			})
		}
	})
}

// TestTypeAllocs checks the allocation contract of each function and
// method. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
func TestTypeAllocs(t *testing.T) {
	t.Run("NewType", func(t *testing.T) {
		var got note.Type
		expect.MaxAllocs(t, func() { got, _ = note.NewType(pqIdentifier) }, 1, "NewType must allocate the Type alone")
		assert.Equal(t, got, pqType, "the test must measure a type that NewType returns")
	})

	t.Run("Valid", func(t *testing.T) {
		var got bool
		expect.MaxAllocs(t, func() { got = pqType.Valid() }, 0, "Valid must not allocate")
		assert.True(t, got, "the test must measure a valid type")
	})

	t.Run("String", func(t *testing.T) {
		t.Run("of a type of one byte", func(t *testing.T) {
			var got string
			expect.MaxAllocs(t, func() { got = note.TypeEd25519.String() }, 0, "String must not allocate")
			assert.Equal(t, got, "0x01", "the test must measure the type 0x01")
		})

		t.Run("of a type without an assigned byte", func(t *testing.T) {
			var got string
			expect.MaxAllocs(t, func() { got = pqType.String() }, 0, "String must not allocate")
			assert.Equal(t, got, pqIdentifier, "the test must measure the type of the tests")
		})
	})
}

// BenchmarkType reports the cost of each function and method, and fails
// above the allocations that their contracts state.
func BenchmarkType(b *testing.B) {
	b.Run("NewType", func(b *testing.B) {
		var got note.Type

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			got, _ = note.NewType(pqIdentifier)
		}

		assert.Equal(b, got, pqType, "the benchmark must measure a type that NewType returns")
	})

	b.Run("Valid", func(b *testing.B) {
		var got bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = pqType.Valid()
		}

		assert.True(b, got, "the benchmark must measure a valid type")
	})

	b.Run("String", func(b *testing.B) {
		b.Run("of a type of one byte", func(b *testing.B) {
			var got string

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got = note.TypeEd25519.String()
			}

			assert.Equal(b, got, "0x01", "the benchmark must measure the type 0x01")
		})

		b.Run("of a type without an assigned byte", func(b *testing.B) {
			var got string

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got = pqType.String()
			}

			assert.Equal(b, got, pqIdentifier, "the benchmark must measure the type of the tests")
		})
	})
}
