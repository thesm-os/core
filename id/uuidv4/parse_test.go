// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package uuidv4_test

import (
	"strings"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/uuidv4"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/seeded"
)

func TestFormat(t *testing.T) {
	t.Parallel()

	t.Run("Zero formats to empty (size 0)", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, uuidv4.Format(id.Zero), "", "Format(Zero) must be empty")
	})

	t.Run("all-zero 128-bit ID formats with the canonical layout", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, uuidv4.Format(id.New128([id.Size128]byte{})),
			"00000000-0000-0000-0000-000000000000",
			"all-zero ID must format with canonical hyphen layout")
	})

	t.Run("specific bytes format with hyphens", func(t *testing.T) {
		t.Parallel()
		u := idFromBytes(
			0x12, 0x34, 0x56, 0x78,
			0x9a, 0xbc,
			0x4d, 0xef,
			0x80, 0x12,
			0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde,
		)
		testkit.Equal(t, uuidv4.Format(u),
			"12345678-9abc-4def-8012-3456789abcde",
			"specific bytes must format with hyphens at canonical positions")
	})

	t.Run("output is exactly 36 chars with hyphens at fixed positions", func(t *testing.T) {
		t.Parallel()
		rng := seeded.New(rand.Seed(7))
		g := uuidv4.New(rng)
		got := uuidv4.Format(g.Generate())
		testkit.Equal(t, len(got), 36, "Format output must be 36 characters")
		hyphenAt := []int{8, 13, 18, 23}
		for _, i := range hyphenAt {
			testkit.Equal(t, got[i], byte('-'), "hyphen position must contain '-'")
		}
		testkit.Equal(t, strings.Count(got, "-"), 4, "Format output must contain exactly 4 hyphens")
	})
}

func TestParse(t *testing.T) {
	t.Parallel()

	t.Run("round-trips Format", func(t *testing.T) {
		t.Parallel()
		g := uuidv4.New(seeded.New(rand.Seed(99)))
		want := g.Generate()
		got, err := uuidv4.Parse(uuidv4.Format(want))
		testkit.NoError(t, err, "Parse")
		testkit.Equal(t, got, want, "Parse(Format(x)) must round-trip")
	})

	t.Run("decodes the canonical zero encoding", func(t *testing.T) {
		t.Parallel()
		got, err := uuidv4.Parse("00000000-0000-0000-0000-000000000000")
		testkit.NoError(t, err, "Parse")
		testkit.Equal(t, got, id.New128([id.Size128]byte{}),
			"all-zero parse must decode to all-zero ID")
	})

	t.Run("decodes a known UUID into the right bytes", func(t *testing.T) {
		t.Parallel()
		got, err := uuidv4.Parse("12345678-9abc-4def-8012-3456789abcde")
		testkit.NoError(t, err, "Parse")
		want := idFromBytes(
			0x12, 0x34, 0x56, 0x78,
			0x9a, 0xbc,
			0x4d, 0xef,
			0x80, 0x12,
			0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde,
		)
		testkit.Equal(t, got, want, "Parse must decode to the expected bytes")
	})

	t.Run("rejects wrong length", func(t *testing.T) {
		t.Parallel()
		cases := []string{
			"",
			"12345678-9abc-4def-8012-3456789abcd",   // 35
			"12345678-9abc-4def-8012-3456789abcdef", // 37
			strings.Repeat("0", 36),                 // 36 chars but no hyphens
		}
		for _, s := range cases {
			_, err := uuidv4.Parse(s)
			if len(s) == 36 {
				testkit.ErrorIs(t, err, uuidv4.ErrInvalidFormat,
					"36 chars without hyphens must return ErrInvalidFormat")
			} else {
				testkit.ErrorIs(t, err, uuidv4.ErrInvalidLength,
					"wrong-length input must return ErrInvalidLength")
			}
		}
	})

	t.Run("rejects misplaced hyphens", func(t *testing.T) {
		t.Parallel()
		// 36 chars but a non-hyphen at position 8.
		s := "12345678X9abc-4def-8012-3456789abcde"
		testkit.Equal(t, len(s), 36, "test fixture must be 36 chars")
		_, err := uuidv4.Parse(s)
		testkit.ErrorIs(t, err, uuidv4.ErrInvalidFormat,
			"misplaced hyphen must return ErrInvalidFormat")
	})

	t.Run("rejects non-hex character in hex segment", func(t *testing.T) {
		t.Parallel()
		segments := []struct {
			input string
			label string
		}{
			{"g2345678-9abc-4def-8012-3456789abcde", "first"},
			{"12345678-9zbc-4def-8012-3456789abcde", "second"},
			{"12345678-9abc-4zef-8012-3456789abcde", "third"},
			{"12345678-9abc-4def-8z12-3456789abcde", "fourth"},
			{"12345678-9abc-4def-8012-3456789abczz", "fifth"},
		}
		for _, tc := range segments {
			_, err := uuidv4.Parse(tc.input)
			testkit.ErrorIs(t, err, uuidv4.ErrInvalidChar,
				tc.label+" segment with non-hex char must return ErrInvalidChar")
		}
	})
}

// FuzzParse asserts [uuidv4.Parse] never panics on arbitrary
// input, and that successful parses round-trip case-insensitively
// through Format. The stdlib hex decoder accepts both upper and
// lower case; Format always emits lowercase. The case-insensitive
// equality is the right round-trip shape.
func FuzzParse(f *testing.F) {
	f.Add("12345678-9abc-4def-8012-3456789abcde")
	f.Add("00000000-0000-0000-0000-000000000000")
	f.Add("12345678-9ABC-4DEF-8012-3456789ABCDE")

	f.Fuzz(func(t *testing.T, s string) {
		got, err := uuidv4.Parse(s)
		if err != nil {
			return
		}
		formatted := uuidv4.Format(got)
		testkit.True(t, strings.EqualFold(formatted, s),
			"Format(Parse(s)) must equal s case-insensitively")
	})
}

// FuzzRoundTrip asserts the Format → Parse round-trip on
// arbitrary 128-bit payloads: any [id.ID] produced from 16
// bytes must Format to a string that Parse decodes back to the
// original ID.
func FuzzRoundTrip(f *testing.F) {
	f.Add(make([]byte, id.Size128))
	f.Add([]byte{
		0x12, 0x34, 0x56, 0x78,
		0x9a, 0xbc,
		0x4d, 0xef,
		0x80, 0x12,
		0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde,
	})

	f.Fuzz(func(t *testing.T, data []byte) {
		var raw [id.Size128]byte
		copy(raw[:], data)
		u := id.New128(raw)

		formatted := uuidv4.Format(u)
		parsed, err := uuidv4.Parse(formatted)
		testkit.NoError(t, err, "Parse(Format(x))")
		testkit.Equal(t, parsed, u, "Format → Parse round-trip must preserve the ID")
	})
}

func BenchmarkFormat(b *testing.B) {
	u := id.New128([id.Size128]byte{
		0x55, 0x0e, 0x84, 0x00, 0xe2, 0x9b, 0x41, 0xd4,
		0xa7, 0x16, 0x44, 0x66, 0x55, 0x44, 0x00, 0x00,
	})
	b.ReportAllocs()
	for b.Loop() {
		_ = uuidv4.Format(u)
	}
}

func BenchmarkParse(b *testing.B) {
	encoded := "550e8400-e29b-41d4-a716-446655440000"
	b.ReportAllocs()
	for b.Loop() {
		_, _ = uuidv4.Parse(encoded)
	}
}
