// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package uuidv4_test

import (
	"strings"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/uuidv4"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/seeded"
)

func TestFormat(t *testing.T) {
	t.Parallel()

	t.Run("returns the empty string for an ID that is not 128 bits", func(t *testing.T) {
		t.Parallel()
		for name, u := range map[string]id.ID{
			"the zero ID":  id.Zero,
			"a 160-bit ID": id.New160([id.Size160]byte{1}),
			"a 256-bit ID": id.New256([id.Size256]byte{1}),
		} {
			testkit.Equal(t, uuidv4.Format(u), "", "Format must return the empty string for "+name)
		}
	})

	t.Run("returns the canonical layout for an all-zero ID", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, uuidv4.Format(id.New128([id.Size128]byte{})),
			"00000000-0000-0000-0000-000000000000",
			"all-zero ID must format with canonical hyphen layout")
	})

	t.Run("returns the hyphenated form of specific bytes", func(t *testing.T) {
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

	t.Run("returns 36 characters with hyphens at fixed positions", func(t *testing.T) {
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

	t.Run("returns the ID that Format encodes", func(t *testing.T) {
		t.Parallel()
		g := uuidv4.New(seeded.New(rand.Seed(99)))
		want := g.Generate()
		got, err := uuidv4.Parse(uuidv4.Format(want))
		testkit.NoError(t, err, "Parse")
		testkit.Equal(t, got, want, "Parse(Format(x)) must round-trip")
	})

	t.Run("returns the all-zero ID for the all-zero text form", func(t *testing.T) {
		t.Parallel()
		got, err := uuidv4.Parse("00000000-0000-0000-0000-000000000000")
		testkit.NoError(t, err, "Parse")
		testkit.Equal(t, got, id.New128([id.Size128]byte{}),
			"all-zero parse must decode to all-zero ID")
	})

	t.Run("returns the bytes that the text form encodes", func(t *testing.T) {
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

	t.Run("returns ErrInvalidLength for a string of the wrong length", func(t *testing.T) {
		t.Parallel()
		cases := []string{
			"",
			"12345678-9abc-4def-8012-3456789abcd",   // 35
			"12345678-9abc-4def-8012-3456789abcdef", // 37
		}
		for _, s := range cases {
			_, err := uuidv4.Parse(s)
			testkit.ErrorIs(t, err, uuidv4.ErrInvalidLength,
				"wrong-length input must return ErrInvalidLength")
			testkit.Equal(t, errs.Classify(err), errs.Invalid, "ErrInvalidLength must classify as Invalid")
		}
	})

	t.Run("returns ErrInvalidFormat for a string without a hyphen at position 8", func(t *testing.T) {
		t.Parallel()
		for _, s := range []string{
			"12345678X9abc-4def-8012-3456789abcde",
			strings.Repeat("0", 36),
		} {
			testkit.Equal(t, len(s), 36, "test fixture must be 36 chars")
			_, err := uuidv4.Parse(s)
			testkit.ErrorIs(t, err, uuidv4.ErrInvalidFormat,
				"misplaced hyphen must return ErrInvalidFormat")
			testkit.Equal(t, errs.Classify(err), errs.Invalid, "ErrInvalidFormat must classify as Invalid")
		}
	})

	t.Run("returns ErrInvalidChar for a non-hex character in each segment", func(t *testing.T) {
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
			testkit.Equal(t, errs.Classify(err), errs.Invalid, "ErrInvalidChar must classify as Invalid")
		}
	})
}

// FuzzParse checks that Parse does not panic, and that the text form of
// every ID it returns equals its input apart from case.
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

// FuzzRoundTrip checks that Parse returns every 128-bit ID from the text
// form that Format returns for it.
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
