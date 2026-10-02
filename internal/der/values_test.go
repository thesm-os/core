// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package der_test

import (
	"math"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/internal/der"
)

func TestValues(t *testing.T) {
	t.Parallel()

	t.Run("Integer", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give []byte
			want bool
		}{
			{name: "reports false for no octets", give: nil, want: false},
			{name: "reports true for one octet", give: []byte{0x80}, want: true},
			{name: "reports true for 0x00 before an octet with the sign bit", give: []byte{0x00, 0x80}, want: true},
			{
				name: "reports false for 0x00 before an octet without the sign bit",
				give: []byte{0x00, 0x7f}, want: false,
			},
			{
				name: "reports true for 0xff before an octet without the sign bit",
				give: []byte{0xff, 0x7f}, want: true,
			},
			{
				name: "reports false for 0xff before an octet with the sign bit",
				give: []byte{0xff, 0x80}, want: false,
			},
			{
				name: "reports true for 0x01 before an octet without the sign bit",
				give: []byte{0x01, 0x00}, want: true,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(t, der.Integer(tt.give), tt.want, "Integer must apply the DER rule")
			})
		}
	})

	t.Run("Uint64", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			give   []byte
			want   uint64
			wantOK bool
		}{
			{name: "returns 0 for the octet 0x00", give: []byte{0x00}, want: 0, wantOK: true},
			{name: "returns 127 for the octet 0x7f", give: []byte{0x7f}, want: 127, wantOK: true},
			{name: "returns 128 after a sign octet", give: []byte{0x00, 0x80}, want: 128, wantOK: true},
			{name: "returns 258 for two octets", give: []byte{0x01, 0x02}, want: 258, wantOK: true},
			{
				name: "returns the largest uint64 after a sign octet",
				give: []byte{0x00, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
				want: math.MaxUint64, wantOK: true,
			},
			{name: "reports false for a negative value", give: []byte{0x80}, wantOK: false},
			{name: "reports false for a redundant sign octet", give: []byte{0x00, 0x01}, wantOK: false},
			{name: "reports false for no octets", give: nil, wantOK: false},
			{
				name:   "reports false for a value of nine octets",
				give:   []byte{0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
				wantOK: false,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, ok := der.Uint64(tt.give)
				testkit.Equal(t, ok, tt.wantOK, "Uint64 must accept exactly the DER values that fit")
				testkit.Equal(t, got, tt.want, "Uint64 must return the value")
			})
		}
	})

	t.Run("Boolean", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name      string
			give      []byte
			want      bool
			wantValid bool
		}{
			{name: "returns true for 0xff", give: []byte{0xff}, want: true, wantValid: true},
			{name: "returns false for 0x00", give: []byte{0x00}, want: false, wantValid: true},
			{name: "reports false for 0x01", give: []byte{0x01}, wantValid: false},
			{name: "reports false for two octets", give: []byte{0xff, 0xff}, wantValid: false},
			{name: "reports false for no octets", give: nil, wantValid: false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, ok := der.Boolean(tt.give)
				testkit.Equal(t, ok, tt.wantValid, "Boolean must accept exactly 0x00 and 0xff")
				testkit.Equal(t, got, tt.want, "Boolean must return the value")
			})
		}
	})

	t.Run("ObjectIdentifier", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give []byte
			want bool
		}{
			{
				name: "reports true for SHA-256",
				give: []byte{0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x01}, want: true,
			},
			{name: "reports false for no octets", give: nil, want: false},
			{name: "reports false for a last octet with the continuation bit", give: []byte{0x2a, 0x86}, want: false},
			{
				name: "reports false for a leading 0x80 of the first subidentifier",
				give: []byte{0x80, 0x01}, want: false,
			},
			{
				name: "reports false for a leading 0x80 of a later subidentifier",
				give: []byte{0x2a, 0x80, 0x01}, want: false,
			},
			{name: "reports true for a 0x80 inside a subidentifier", give: []byte{0x2a, 0x81, 0x80, 0x01}, want: true},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(t, der.ObjectIdentifier(tt.give), tt.want, "ObjectIdentifier must apply the DER rule")
			})
		}
	})

	t.Run("BitString", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the octets and the unused bits", func(t *testing.T) {
			t.Parallel()
			bits, unused, ok := der.BitString([]byte{0x06, 0x80, 0x40})
			testkit.True(t, ok, "BitString must accept unused bits of zero")
			testkit.Equal(t, bits, []byte{0x80, 0x40}, "BitString must return the octets")
			testkit.Equal(t, unused, 6, "BitString must return the unused bits")
		})

		t.Run("returns no octets for an empty BIT STRING", func(t *testing.T) {
			t.Parallel()
			bits, unused, ok := der.BitString([]byte{0x00})
			testkit.True(t, ok, "an empty BIT STRING must be valid")
			testkit.Len(t, bits, 0, "an empty BIT STRING must have no octets")
			testkit.Equal(t, unused, 0, "an empty BIT STRING must have no unused bits")
		})

		malformed := []struct {
			name string
			give []byte
		}{
			{name: "reports false for no octets", give: nil},
			{name: "reports false for eight unused bits", give: []byte{0x08, 0x00}},
			{name: "reports false for unused bits without octets", give: []byte{0x01}},
			{name: "reports false for a set unused bit", give: []byte{0x01, 0x01}},
		}
		for _, tt := range malformed {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, _, ok := der.BitString(tt.give)
				testkit.False(t, ok, "BitString must refuse the content")
			})
		}

		t.Run("accepts seven unused bits of zero", func(t *testing.T) {
			t.Parallel()
			_, unused, ok := der.BitString([]byte{0x07, 0x80})
			testkit.True(t, ok, "seven unused bits of zero must be valid")
			testkit.Equal(t, unused, 7, "BitString must return the unused bits")
		})
	})

	t.Run("Bit", func(t *testing.T) {
		t.Parallel()

		bits := []byte{0x80, 0x01}
		tests := []struct {
			name string
			give int
			want bool
		}{
			{name: "reports true for bit 0, the top bit of the first octet", give: 0, want: true},
			{name: "reports false for bit 1", give: 1, want: false},
			{name: "reports false for bit 14", give: 14, want: false},
			{name: "reports true for bit 15, the low bit of the second octet", give: 15, want: true},
			{name: "reports false for bit 16, past the end", give: 16, want: false},
			{name: "reports false for a negative bit", give: -1, want: false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(t, der.Bit(bits, tt.give), tt.want, "Bit must number from the top bit")
			})
		}
	})
}
