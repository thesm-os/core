// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package uuidv4_test

import (
	"errors"
	"strings"
	"testing"

	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/uuidv4"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/seeded"
)

func TestFormat(t *testing.T) {
	t.Parallel()

	t.Run("Zero formats to empty (size 0)", func(t *testing.T) {
		t.Parallel()
		if got := uuidv4.Format(id.Zero); got != "" {
			t.Fatalf("Format(Zero): got %q, want empty", got)
		}
	})

	t.Run("all-zero 128-bit ID formats with the canonical layout", func(t *testing.T) {
		t.Parallel()
		const want = "00000000-0000-0000-0000-000000000000"
		if got := uuidv4.Format(id.New128([id.Size128]byte{})); got != want {
			t.Fatalf("Format: got %q, want %q", got, want)
		}
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
		const want = "12345678-9abc-4def-8012-3456789abcde"
		if got := uuidv4.Format(u); got != want {
			t.Fatalf("Format: got %q, want %q", got, want)
		}
	})

	t.Run("output is exactly 36 chars with hyphens at fixed positions", func(t *testing.T) {
		t.Parallel()
		rng := seeded.New(rand.Seed(7))
		g := uuidv4.New(rng)
		got := uuidv4.Format(g.Generate())
		if len(got) != 36 {
			t.Fatalf("len(Format): got %d, want 36", len(got))
		}
		hyphenAt := []int{8, 13, 18, 23}
		for _, i := range hyphenAt {
			if got[i] != '-' {
				t.Fatalf("Format[%d]: got %c, want -", i, got[i])
			}
		}
		if strings.Count(got, "-") != 4 {
			t.Fatalf("hyphen count: got %d, want 4", strings.Count(got, "-"))
		}
	})
}

func TestParse(t *testing.T) {
	t.Parallel()

	t.Run("round-trips Format", func(t *testing.T) {
		t.Parallel()
		g := uuidv4.New(seeded.New(rand.Seed(99)))
		want := g.Generate()
		got, err := uuidv4.Parse(uuidv4.Format(want))
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if got != want {
			t.Fatalf("round-trip:\n got=%v\nwant=%v", got, want)
		}
	})

	t.Run("decodes the canonical zero encoding", func(t *testing.T) {
		t.Parallel()
		got, err := uuidv4.Parse("00000000-0000-0000-0000-000000000000")
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		want := id.New128([id.Size128]byte{})
		if got != want {
			t.Fatalf("zero parse: got %v, want %v", got, want)
		}
	})

	t.Run("decodes a known UUID into the right bytes", func(t *testing.T) {
		t.Parallel()
		got, err := uuidv4.Parse("12345678-9abc-4def-8012-3456789abcde")
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		want := idFromBytes(
			0x12, 0x34, 0x56, 0x78,
			0x9a, 0xbc,
			0x4d, 0xef,
			0x80, 0x12,
			0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde,
		)
		if got != want {
			t.Fatalf("decode: got %x, want %x", got.Bytes(), want.Bytes())
		}
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
			if len(s) != 36 {
				if !errors.Is(err, uuidv4.ErrInvalidLength) {
					t.Fatalf("len %d: got %v, want ErrInvalidLength", len(s), err)
				}
			} else {
				if !errors.Is(err, uuidv4.ErrInvalidFormat) {
					t.Fatalf("len 36 no hyphens: got %v, want ErrInvalidFormat", err)
				}
			}
		}
	})

	t.Run("rejects misplaced hyphens", func(t *testing.T) {
		t.Parallel()
		// 36 chars but a non-hyphen at position 8.
		s := "12345678X9abc-4def-8012-3456789abcde"
		if len(s) != 36 {
			t.Fatalf("test fixture length: got %d, want 36", len(s))
		}
		_, err := uuidv4.Parse(s)
		if !errors.Is(err, uuidv4.ErrInvalidFormat) {
			t.Fatalf("got %v, want ErrInvalidFormat", err)
		}
	})

	t.Run("rejects non-hex character in hex segment", func(t *testing.T) {
		t.Parallel()
		// 'g' at position 0 is not a hex digit.
		s := "g2345678-9abc-4def-8012-3456789abcde"
		_, err := uuidv4.Parse(s)
		if !errors.Is(err, uuidv4.ErrInvalidChar) {
			t.Fatalf("first segment: got %v, want ErrInvalidChar", err)
		}

		// Bad char in second segment.
		s = "12345678-9zbc-4def-8012-3456789abcde"
		_, err = uuidv4.Parse(s)
		if !errors.Is(err, uuidv4.ErrInvalidChar) {
			t.Fatalf("second segment: got %v, want ErrInvalidChar", err)
		}

		// Third segment.
		s = "12345678-9abc-4zef-8012-3456789abcde"
		_, err = uuidv4.Parse(s)
		if !errors.Is(err, uuidv4.ErrInvalidChar) {
			t.Fatalf("third segment: got %v, want ErrInvalidChar", err)
		}

		// Fourth segment.
		s = "12345678-9abc-4def-8z12-3456789abcde"
		_, err = uuidv4.Parse(s)
		if !errors.Is(err, uuidv4.ErrInvalidChar) {
			t.Fatalf("fourth segment: got %v, want ErrInvalidChar", err)
		}

		// Fifth segment.
		s = "12345678-9abc-4def-8012-3456789abczz"
		_, err = uuidv4.Parse(s)
		if !errors.Is(err, uuidv4.ErrInvalidChar) {
			t.Fatalf("fifth segment: got %v, want ErrInvalidChar", err)
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
		if !strings.EqualFold(formatted, s) {
			t.Fatalf("round-trip:\n  in  = %q\n  out = %q", s, formatted)
		}
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
		if err != nil {
			t.Fatalf("Format(%x)=%q; Parse: %v", raw, formatted, err)
		}
		if parsed != u {
			t.Fatalf("round-trip failed:\n  in   = %x\n  fmt  = %q\n  out  = %x",
				raw, formatted, parsed.Bytes())
		}
	})
}
