// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fixed_test

import (
	"bytes"
	"encoding"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/fixed"
)

// The encoding interfaces this package promises to satisfy. A
// compile-time check rather than a runtime one: a missing method is a
// build failure, which is where it belongs.
var (
	_ encoding.BinaryAppender    = fixed.Zero
	_ encoding.BinaryMarshaler   = fixed.Zero
	_ encoding.BinaryUnmarshaler = (*fixed.Fixed64)(nil)
	_ encoding.TextAppender      = fixed.Zero
	_ encoding.TextMarshaler     = fixed.Zero
	_ encoding.TextUnmarshaler   = (*fixed.Fixed64)(nil)
)

func TestMarshalBinary(t *testing.T) {
	t.Parallel()

	t.Run("is eight bytes big-endian two's complement", func(t *testing.T) {
		t.Parallel()

		cases := map[string]struct {
			in   fixed.Fixed64
			want []byte
		}{
			"zero":     {fixed.Zero, []byte{0, 0, 0, 0, 0, 0, 0, 0}},
			"smallest": {fixed.Smallest, []byte{0, 0, 0, 0, 0, 0, 0, 1}},
			"minus one raw unit": {
				-fixed.Smallest,
				[]byte{255, 255, 255, 255, 255, 255, 255, 255},
			},
			"one": {fixed.One, []byte{0, 0, 0, 0, 5, 245, 225, 0}},
			"max": {
				fixed.Max,
				[]byte{127, 255, 255, 255, 255, 255, 255, 255},
			},
			"min": {
				fixed.Min,
				[]byte{128, 0, 0, 0, 0, 0, 0, 1},
			},
		}
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got, err := tc.in.MarshalBinary()

				testkit.NoError(t, err, "MarshalBinary must succeed")
				testkit.Equal(t, got, tc.want,
					"the layout is a stable wire contract")
			})
		}
	})

	t.Run("refuses the excluded value", func(t *testing.T) {
		t.Parallel()

		_, err := outOfDomain.MarshalBinary()

		testkit.ErrorIs(t, err, fixed.ErrRange,
			"nothing outside the domain may reach the wire")
	})

	t.Run("byte order is not numeric order", func(t *testing.T) {
		t.Parallel()

		// The documented caveat, asserted so it cannot regress into a
		// silent promise: a caller sorting encoded keys does NOT get
		// numeric order without their own sign-bit flip.
		neg, err := (-fixed.One).MarshalBinary()
		testkit.NoError(t, err, "MarshalBinary must succeed")

		pos, err := fixed.One.MarshalBinary()
		testkit.NoError(t, err, "MarshalBinary must succeed")

		testkit.True(t, bytes.Compare(neg, pos) > 0,
			"the encoded negative must sort above the encoded positive")
		testkit.True(t, -fixed.One < fixed.One,
			"while the values themselves order correctly")
	})
}

func TestAppendBinary(t *testing.T) {
	t.Parallel()

	t.Run("appends to an existing buffer", func(t *testing.T) {
		t.Parallel()

		got, err := fixed.Smallest.AppendBinary([]byte{0xAA})

		testkit.NoError(t, err, "AppendBinary must succeed")
		testkit.Equal(t, got, []byte{0xAA, 0, 0, 0, 0, 0, 0, 0, 1},
			"AppendBinary must append rather than replace")
	})

	t.Run("leaves dst untouched when it refuses", func(t *testing.T) {
		t.Parallel()

		got, err := outOfDomain.AppendBinary([]byte{0xAA})

		testkit.ErrorIs(t, err, fixed.ErrRange,
			"AppendBinary must refuse the excluded value")
		testkit.Equal(t, got, []byte{0xAA},
			"a refused append must not modify dst")
	})
}

func TestUnmarshalBinary(t *testing.T) {
	t.Parallel()

	t.Run("decodes what MarshalBinary produced", func(t *testing.T) {
		t.Parallel()

		values := []fixed.Fixed64{
			fixed.Zero, fixed.One, fixed.Smallest, -fixed.Smallest,
			fixed.Max, fixed.Min, 1234567890, -1234567890,
		}
		for _, want := range values {
			b, err := want.MarshalBinary()
			testkit.NoError(t, err, "MarshalBinary must succeed")

			var got fixed.Fixed64

			testkit.NoError(t, got.UnmarshalBinary(b),
				"UnmarshalBinary must accept its own output")
			testkit.Equal(t, got, want, "the round trip must be exact")
		}
	})

	t.Run("rejects any length but Size", func(t *testing.T) {
		t.Parallel()

		cases := map[string][]byte{
			"nil":      nil,
			"empty":    {},
			"short":    {0, 0, 0, 0, 0, 0, 0},
			"long":     {0, 0, 0, 0, 0, 0, 0, 0, 0},
			"one byte": {1},
		}
		for name, data := range cases {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got := fixed.One

				err := got.UnmarshalBinary(data)

				testkit.ErrorIs(t, err, fixed.ErrSize,
					"a wrong-length input must be a decode error")
				testkit.Equal(t, got, fixed.One,
					"a rejected decode must not modify the receiver")
			})
		}
	})

	t.Run("rejects the excluded value on the wire", func(t *testing.T) {
		t.Parallel()

		// The bit pattern of math.MinInt64. No encoder in this package
		// produces it; a hostile or corrupted peer can still send it.
		got := fixed.One

		err := got.UnmarshalBinary([]byte{128, 0, 0, 0, 0, 0, 0, 0})

		testkit.ErrorIs(t, err, fixed.ErrRange,
			"a decoded out-of-domain value must be rejected")
		testkit.Equal(t, got, fixed.One,
			"a rejected decode must not modify the receiver")
	})
}
