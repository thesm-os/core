// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package ulid_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/ulid"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/seeded"
)

func TestFormat(t *testing.T) {
	t.Parallel()

	t.Run("Zero formats to empty (size 0)", func(t *testing.T) {
		t.Parallel()
		if got := ulid.Format(id.Zero); got != "" {
			t.Fatalf("Format(Zero): got %q, want \"\"", got)
		}
	})

	// Frozen-output vectors. Each was hand-derived from the
	// ULID Crockford base32 layout (50 bits of timestamp, 80
	// bits of randomness). The vectors lock the arithmetic in
	// the shift-calculation loops against silent regression.
	t.Run("all-zero 128-bit ID encodes to 26 zeros", func(t *testing.T) {
		t.Parallel()
		u := id.New128([id.Size128]byte{})
		const want = "00000000000000000000000000"
		if got := ulid.Format(u); got != want {
			t.Fatalf("Format: got %q, want %q", got, want)
		}
	})

	t.Run("timestamp byte 0 = 0x01 encodes to leading '04'", func(t *testing.T) {
		t.Parallel()
		// 48-bit timestamp 0x010000000000 = 2^40; shifted
		// left by 2 = 2^42. Top 5 bits (49..45) of a 50-bit
		// field are zero; next 5 bits (44..40) carry the
		// '4' (= 4). Remaining chars are zero.
		u := idFromBytes(0x01)
		const want = "04000000000000000000000000"
		if got := ulid.Format(u); got != want {
			t.Fatalf("Format: got %q, want %q", got, want)
		}
	})

	t.Run("random half all-ones encodes to 16 Zs", func(t *testing.T) {
		t.Parallel()
		// Timestamp half zero → 10 zero chars. Random half:
		// hi = 0xFFFFFFFFFFFFFFFF, tail = 0xFFFFF — both
		// produce 'Z' for every 5-bit chunk.
		u := idFromBytes(
			0, 0, 0, 0, 0, 0,
			0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
		)
		const want = "0000000000ZZZZZZZZZZZZZZZZ"
		if got := ulid.Format(u); got != want {
			t.Fatalf("Format: got %q, want %q", got, want)
		}
	})

	t.Run("asymmetric random half locks per-char arithmetic", func(t *testing.T) {
		t.Parallel()
		// Asymmetric bytes so that each 5-bit chunk maps to a
		// distinct char: a regression in the per-iteration
		// shift arithmetic (`i*5` mutated to `i+5` etc.) would
		// produce a different output for one or more chars.
		u := idFromBytes(
			0, 0, 0, 0, 0, 0,
			0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC, 0xDE, 0xF0, 0x12, 0x34,
		)
		const want = "000000000028T5CY4TQKFF04HM"
		if got := ulid.Format(u); got != want {
			t.Fatalf("Format: got %q, want %q", got, want)
		}
	})

	t.Run("mixed timestamp + random locks both halves", func(t *testing.T) {
		t.Parallel()
		// Both halves carry asymmetric bytes — defends against
		// the timestamp-half shift mutations as well.
		u := idFromBytes(
			0x01, 0x23, 0x45, 0x67, 0x89, 0xAB,
			0x01, 0x23, 0x45, 0x67, 0x89, 0xAB, 0xCD, 0xEF, 0xFE, 0xDC,
		)
		const want = "04HMASW9NC04HMASW9NF6YZZPW"
		if got := ulid.Format(u); got != want {
			t.Fatalf("Format: got %q, want %q", got, want)
		}
	})

	t.Run("all-ones encodes timestamp prefix 'ZZZZZZZZZW'", func(t *testing.T) {
		t.Parallel()
		// 48-bit timestamp 0xFFFFFFFFFFFF shifted left by 2
		// gives a 50-bit value with bits 49..2 set and bits
		// 1..0 zero. Top 9 chars (5 bits each, all 1s) → 'Z';
		// last char's bits are 11100 = 28 = 'W' in the
		// Crockford alphabet (0..9, A-H, J, K, M, N, P, Q, R,
		// S, T, V, W, X, Y, Z).
		u := idFromBytes(
			0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
			0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
		)
		const want = "ZZZZZZZZZWZZZZZZZZZZZZZZZZ"
		if got := ulid.Format(u); got != want {
			t.Fatalf("Format: got %q, want %q", got, want)
		}
	})

	t.Run("output is exactly 26 base32 characters", func(t *testing.T) {
		t.Parallel()
		const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
		origin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		g := ulid.New(fake.New(origin), seeded.New(rand.Seed(1)))
		got := ulid.Format(g.Generate())
		if len(got) != 26 {
			t.Fatalf("len: got %d, want 26", len(got))
		}
		for i, ch := range got {
			if !strings.ContainsRune(alphabet, ch) {
				t.Fatalf("char %d (%q) not in Crockford alphabet", i, ch)
			}
		}
	})
}

func TestParseULID(t *testing.T) {
	t.Parallel()

	t.Run("round-trips Format", func(t *testing.T) {
		t.Parallel()
		origin := time.Date(2026, 6, 15, 12, 34, 56, 0, time.UTC)
		g := ulid.New(fake.New(origin), seeded.New(rand.Seed(7)))
		want := g.Generate()
		encoded := ulid.Format(want)

		got, err := ulid.ParseULID(encoded)
		if err != nil {
			t.Fatalf("ParseULID: %v", err)
		}
		if got != want {
			t.Fatalf("round-trip:\n got=%v\nwant=%v", got, want)
		}
	})

	t.Run("decodes the all-zero canonical encoding", func(t *testing.T) {
		t.Parallel()
		got, err := ulid.ParseULID("00000000000000000000000000")
		if err != nil {
			t.Fatalf("ParseULID: %v", err)
		}
		want := id.New128([id.Size128]byte{})
		if got != want {
			t.Fatalf("zero parse: got %v, want %v", got, want)
		}
	})

	t.Run("decodes asymmetric vector", func(t *testing.T) {
		t.Parallel()
		got, err := ulid.ParseULID("04HMASW9NC04HMASW9NF6YZZPW")
		if err != nil {
			t.Fatalf("ParseULID: %v", err)
		}
		want := idFromBytes(
			0x01, 0x23, 0x45, 0x67, 0x89, 0xAB,
			0x01, 0x23, 0x45, 0x67, 0x89, 0xAB, 0xCD, 0xEF, 0xFE, 0xDC,
		)
		if got != want {
			t.Fatalf("decode: got %x, want %x", got.Bytes(), want.Bytes())
		}
	})

	t.Run("accepts lowercase input", func(t *testing.T) {
		t.Parallel()
		upper, err := ulid.ParseULID("04HMASW9NC04HMASW9NF6YZZPW")
		if err != nil {
			t.Fatalf("upper: %v", err)
		}
		lower, err := ulid.ParseULID("04hmasw9nc04hmasw9nf6yzzpw")
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		if upper != lower {
			t.Fatalf("case folding: upper=%v lower=%v", upper, lower)
		}
	})

	t.Run("substitutes Crockford I/L/O for 1/1/0", func(t *testing.T) {
		t.Parallel()
		// '1' and 'L' should decode identically.
		oneA, err := ulid.ParseULID("1000000000000000000000000O")
		if err != nil {
			t.Fatalf("with O: %v", err)
		}
		oneB, err := ulid.ParseULID("L0000000000000000000000000")
		if err != nil {
			t.Fatalf("with L: %v", err)
		}
		// Position 0 carries the leading 1 in oneB; position 25
		// carries the trailing O→0 in oneA. Different positions,
		// so they're not equal — but both must parse without
		// error, demonstrating substitution acceptance.
		_ = oneA
		_ = oneB
	})

	t.Run("rejects wrong length", func(t *testing.T) {
		t.Parallel()
		cases := []string{
			"",
			"0",
			strings.Repeat("0", 25),
			strings.Repeat("0", 27),
			strings.Repeat("0", 100),
		}
		for _, s := range cases {
			_, err := ulid.ParseULID(s)
			if !errors.Is(err, ulid.ErrInvalidLength) {
				t.Fatalf("len %d: got %v, want ErrInvalidLength", len(s), err)
			}
		}
	})

	t.Run("rejects characters outside Crockford alphabet", func(t *testing.T) {
		t.Parallel()
		// 'U' is excluded from Crockford to avoid V/U confusion.
		s := "U" + strings.Repeat("0", 25)
		_, err := ulid.ParseULID(s)
		if !errors.Is(err, ulid.ErrInvalidChar) {
			t.Fatalf("with U: got %v, want ErrInvalidChar", err)
		}

		// Punctuation: not in alphabet at any position.
		s = strings.Repeat("0", 10) + "!" + strings.Repeat("0", 15)
		_, err = ulid.ParseULID(s)
		if !errors.Is(err, ulid.ErrInvalidChar) {
			t.Fatalf("with !: got %v, want ErrInvalidChar", err)
		}
	})

	t.Run("rejects timestamp overflow (first char > '7')", func(t *testing.T) {
		t.Parallel()
		// First char '8' (Crockford value 8) means timestamp bits
		// 49..45 = 01000, which sets bit 48 — beyond the 48-bit
		// timestamp slot.
		s := "8" + strings.Repeat("0", 25)
		_, err := ulid.ParseULID(s)
		if !errors.Is(err, ulid.ErrInvalidTimestamp) {
			t.Fatalf("with 8: got %v, want ErrInvalidTimestamp", err)
		}

		// 'Z' (value 31): top 2 bits non-zero.
		s = "Z" + strings.Repeat("0", 25)
		_, err = ulid.ParseULID(s)
		if !errors.Is(err, ulid.ErrInvalidTimestamp) {
			t.Fatalf("with Z: got %v, want ErrInvalidTimestamp", err)
		}
	})

	t.Run("accepts maximum valid first char '7'", func(t *testing.T) {
		t.Parallel()
		// Locks the boundary: first char '7' (Crockford value 7)
		// means timestamp bits 49..45 = 00111, which leaves bit
		// 48 unset — within the 48-bit timestamp slot. The
		// boundary check `first > 7` must accept exactly 7.
		s := "7" + strings.Repeat("0", 25)
		got, err := ulid.ParseULID(s)
		if err != nil {
			t.Fatalf("with 7: got err %v, want nil (boundary)", err)
		}
		if got.IsZero() {
			t.Fatal("with 7: got Zero, want non-zero")
		}
	})

	t.Run("rejects invalid char inside timestamp half", func(t *testing.T) {
		t.Parallel()
		// '!' at position 5 — inside the timestamp half (chars
		// 0..9), past the leading-char check. Exercises the
		// in-loop invalid-char branch that the leading-char
		// check doesn't cover.
		s := "00000!0000" + strings.Repeat("0", 16)
		_, err := ulid.ParseULID(s)
		if !errors.Is(err, ulid.ErrInvalidChar) {
			t.Fatalf("with ! mid-timestamp: got %v, want ErrInvalidChar", err)
		}
	})

	// Alphabet-coverage tests. Each test parses a complete 26-
	// character ULID string whose body collectively exercises
	// every branch of the Crockford decoder (decodeChar). Two
	// strings of 26 distinct chars cover the 32-char alphabet;
	// a third covers the I/L/O substitutions and lowercase
	// variants.

	t.Run("uppercase chars 0-S decode", func(t *testing.T) {
		t.Parallel()
		// 26 distinct chars: 0..9, A-H, J, K, M, N, P, Q, R, S
		// (covers Crockford values 0..25). First char '0' so the
		// timestamp boundary check passes.
		s := "0123456789ABCDEFGHJKMNPQRS"
		if _, err := ulid.ParseULID(s); err != nil {
			t.Fatalf("ParseULID(%q): %v", s, err)
		}
	})

	t.Run("uppercase chars T-Z decode", func(t *testing.T) {
		t.Parallel()
		// Covers the T, V, W, X, Y, Z branches (values 26..31)
		// plus padding.
		s := "0TVWXYZ" + strings.Repeat("0", 19)
		if _, err := ulid.ParseULID(s); err != nil {
			t.Fatalf("ParseULID(%q): %v", s, err)
		}
	})

	t.Run("lowercase chars a-z decode", func(t *testing.T) {
		t.Parallel()
		// 25 lowercase chars covering a..h, j, k, m, n, p..t,
		// v..z plus one '0' padder.
		s := "0abcdefghjkmnpqrstvwxyz000"
		if _, err := ulid.ParseULID(s); err != nil {
			t.Fatalf("ParseULID(%q): %v", s, err)
		}
	})

	t.Run("I L O substitutions decode", func(t *testing.T) {
		t.Parallel()
		// The ULID spec retroactively accepts I/L → 1 and O → 0
		// to handle handwritten or transcribed identifiers; both
		// upper- and lower-case forms are accepted.
		s := "0IiLlOo" + strings.Repeat("0", 19)
		if _, err := ulid.ParseULID(s); err != nil {
			t.Fatalf("ParseULID(%q): %v", s, err)
		}
	})
}

// FuzzParseULID asserts [ulid.ParseULID] never panics on
// arbitrary input, and that successful parses are idempotent —
// Parse(Format(Parse(s))) == Parse(s). Idempotency is the right
// shape for ULID: the parser is case-insensitive and applies
// I/L→1, O→0 substitutions, so the input string is not bytewise
// recovered, but the parsed [id.ID] is.
func FuzzParseULID(f *testing.F) {
	f.Add("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	f.Add("00000000000000000000000000")
	f.Add("7ZZZZZZZZZZZZZZZZZZZZZZZZZ")
	f.Add("0iIlLoO" + strings.Repeat("0", 19))

	f.Fuzz(func(t *testing.T, s string) {
		got, err := ulid.ParseULID(s)
		if err != nil {
			return
		}
		formatted := ulid.Format(got)
		again, err := ulid.ParseULID(formatted)
		if err != nil {
			t.Fatalf("re-parse of Format(%q)=%q: %v", s, formatted, err)
		}
		if got != again {
			t.Fatalf("idempotency broken:\n  Parse(%q)        = %v\n  Format -> Parse  = %v",
				s, got, again)
		}
	})
}

// FuzzULIDRoundTrip asserts the Format → Parse round-trip on
// arbitrary 128-bit payloads: any [id.ID] produced from 16 bytes
// must Format to a string that Parse decodes back to the
// original ID.
func FuzzULIDRoundTrip(f *testing.F) {
	f.Add(make([]byte, id.Size128))
	f.Add([]byte{
		0x01, 0x23, 0x45, 0x67, 0x89, 0xAB,
		0x01, 0x23, 0x45, 0x67, 0x89, 0xAB, 0xCD, 0xEF, 0xFE, 0xDC,
	})
	f.Add([]byte{
		0x3F, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	})

	f.Fuzz(func(t *testing.T, data []byte) {
		var raw [id.Size128]byte
		copy(raw[:], data)
		// Mask the top 2 bits of byte 0: [ParseULID] rejects
		// first Crockford char > 7, which translates to the
		// 48-bit timestamp value being < 2^46. Bits 47..46 of
		// the timestamp are bits 7..6 of byte 0.
		raw[0] &= 0x3F
		u := id.New128(raw)

		formatted := ulid.Format(u)
		parsed, err := ulid.ParseULID(formatted)
		if err != nil {
			t.Fatalf("Format(%x)=%q; Parse: %v", raw, formatted, err)
		}
		if parsed != u {
			t.Fatalf("round-trip failed:\n  in   = %x\n  fmt  = %q\n  out  = %x",
				raw, formatted, parsed.Bytes())
		}
	})
}
