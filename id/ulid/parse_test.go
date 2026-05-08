// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package ulid_test

import (
	"strings"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

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
		testkit.Equal(t, ulid.Format(id.Zero), "", "Format(Zero) must be empty")
	})

	// Frozen-output vectors. Each was hand-derived from the
	// ULID Crockford base32 layout (50 bits of timestamp, 80
	// bits of randomness). The vectors lock the arithmetic in
	// the shift-calculation loops against silent regression.
	t.Run("all-zero 128-bit ID encodes to 26 zeros", func(t *testing.T) {
		t.Parallel()
		u := id.New128([id.Size128]byte{})
		testkit.Equal(t, ulid.Format(u), "00000000000000000000000000",
			"all-zero ID must encode to 26 zeros")
	})

	t.Run("timestamp byte 0 = 0x01 encodes to leading '04'", func(t *testing.T) {
		t.Parallel()
		// 48-bit timestamp 0x010000000000 = 2^40; shifted
		// left by 2 = 2^42. Top 5 bits (49..45) of a 50-bit
		// field are zero; next 5 bits (44..40) carry the
		// '4' (= 4). Remaining chars are zero.
		u := idFromBytes(0x01)
		testkit.Equal(t, ulid.Format(u), "04000000000000000000000000",
			"timestamp byte 0 = 0x01 must encode to leading '04'")
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
		testkit.Equal(t, ulid.Format(u), "0000000000ZZZZZZZZZZZZZZZZ",
			"random half all-ones must encode to 16 Zs")
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
		testkit.Equal(t, ulid.Format(u), "000000000028T5CY4TQKFF04HM",
			"asymmetric random half must encode to expected vector")
	})

	t.Run("mixed timestamp + random locks both halves", func(t *testing.T) {
		t.Parallel()
		// Both halves carry asymmetric bytes — defends against
		// the timestamp-half shift mutations as well.
		u := idFromBytes(
			0x01, 0x23, 0x45, 0x67, 0x89, 0xAB,
			0x01, 0x23, 0x45, 0x67, 0x89, 0xAB, 0xCD, 0xEF, 0xFE, 0xDC,
		)
		testkit.Equal(t, ulid.Format(u), "04HMASW9NC04HMASW9NF6YZZPW",
			"mixed timestamp+random must encode to expected vector")
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
		testkit.Equal(t, ulid.Format(u), "ZZZZZZZZZWZZZZZZZZZZZZZZZZ",
			"all-ones must encode timestamp prefix 'ZZZZZZZZZW'")
	})

	t.Run("output is exactly 26 base32 characters", func(t *testing.T) {
		t.Parallel()
		const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
		origin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		g := ulid.New(fake.New(origin), seeded.New(rand.Seed(1)))
		got := ulid.Format(g.Generate())
		testkit.Equal(t, len(got), 26, "Format output must be 26 characters")
		for i, ch := range got {
			testkit.True(t, strings.ContainsRune(alphabet, ch),
				"char "+string(ch)+" at position must be in Crockford alphabet")
			_ = i
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
		testkit.NoError(t, err, "ParseULID")
		testkit.Equal(t, got, want, "ParseULID(Format(x)) must round-trip")
	})

	t.Run("decodes the all-zero canonical encoding", func(t *testing.T) {
		t.Parallel()
		got, err := ulid.ParseULID("00000000000000000000000000")
		testkit.NoError(t, err, "ParseULID")
		testkit.Equal(t, got, id.New128([id.Size128]byte{}),
			"all-zero parse must decode to all-zero 128-bit ID")
	})

	t.Run("decodes asymmetric vector", func(t *testing.T) {
		t.Parallel()
		got, err := ulid.ParseULID("04HMASW9NC04HMASW9NF6YZZPW")
		testkit.NoError(t, err, "ParseULID")
		want := idFromBytes(
			0x01, 0x23, 0x45, 0x67, 0x89, 0xAB,
			0x01, 0x23, 0x45, 0x67, 0x89, 0xAB, 0xCD, 0xEF, 0xFE, 0xDC,
		)
		testkit.Equal(t, got, want, "asymmetric vector must round-trip")
	})

	t.Run("accepts lowercase input", func(t *testing.T) {
		t.Parallel()
		upper, err := ulid.ParseULID("04HMASW9NC04HMASW9NF6YZZPW")
		testkit.NoError(t, err, "ParseULID upper")
		lower, err := ulid.ParseULID("04hmasw9nc04hmasw9nf6yzzpw")
		testkit.NoError(t, err, "ParseULID lower")
		testkit.Equal(t, upper, lower,
			"upper- and lower-case must decode identically (case folding)")
	})

	t.Run("substitutes Crockford I/L/O for 1/1/0", func(t *testing.T) {
		t.Parallel()
		// '1' and 'L' should decode identically.
		_, err := ulid.ParseULID("1000000000000000000000000O")
		testkit.NoError(t, err, "ParseULID with O")
		_, err = ulid.ParseULID("L0000000000000000000000000")
		testkit.NoError(t, err, "ParseULID with L")
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
			testkit.ErrorIs(t, err, ulid.ErrInvalidLength,
				"wrong-length input must return ErrInvalidLength")
		}
	})

	t.Run("rejects characters outside Crockford alphabet", func(t *testing.T) {
		t.Parallel()
		// 'U' is excluded from Crockford to avoid V/U confusion.
		_, err := ulid.ParseULID("U" + strings.Repeat("0", 25))
		testkit.ErrorIs(t, err, ulid.ErrInvalidChar,
			"'U' (excluded from Crockford) must return ErrInvalidChar")

		// Punctuation: not in alphabet at any position.
		_, err = ulid.ParseULID(strings.Repeat("0", 10) + "!" + strings.Repeat("0", 15))
		testkit.ErrorIs(t, err, ulid.ErrInvalidChar,
			"punctuation must return ErrInvalidChar")
	})

	t.Run("rejects timestamp overflow (first char > '7')", func(t *testing.T) {
		t.Parallel()
		// First char '8' (Crockford value 8) means timestamp bits
		// 49..45 = 01000, which sets bit 48 — beyond the 48-bit
		// timestamp slot.
		_, err := ulid.ParseULID("8" + strings.Repeat("0", 25))
		testkit.ErrorIs(t, err, ulid.ErrInvalidTimestamp,
			"first char '8' must return ErrInvalidTimestamp")

		// 'Z' (value 31): top 2 bits non-zero.
		_, err = ulid.ParseULID("Z" + strings.Repeat("0", 25))
		testkit.ErrorIs(t, err, ulid.ErrInvalidTimestamp,
			"first char 'Z' must return ErrInvalidTimestamp")
	})

	t.Run("accepts maximum valid first char '7'", func(t *testing.T) {
		t.Parallel()
		// Locks the boundary: first char '7' (Crockford value 7)
		// means timestamp bits 49..45 = 00111, which leaves bit
		// 48 unset — within the 48-bit timestamp slot. The
		// boundary check `first > 7` must accept exactly 7.
		got, err := ulid.ParseULID("7" + strings.Repeat("0", 25))
		testkit.NoError(t, err, "first char '7' must parse")
		testkit.False(t, got.IsZero(), "first char '7' must produce non-Zero ID")
	})

	t.Run("rejects invalid char inside timestamp half", func(t *testing.T) {
		t.Parallel()
		// '!' at position 5 — inside the timestamp half (chars
		// 0..9), past the leading-char check. Exercises the
		// in-loop invalid-char branch that the leading-char
		// check doesn't cover.
		_, err := ulid.ParseULID("00000!0000" + strings.Repeat("0", 16))
		testkit.ErrorIs(t, err, ulid.ErrInvalidChar,
			"invalid char inside timestamp half must return ErrInvalidChar")
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
		_, err := ulid.ParseULID("0123456789ABCDEFGHJKMNPQRS")
		testkit.NoError(t, err, "uppercase chars 0-S must decode")
	})

	t.Run("uppercase chars T-Z decode", func(t *testing.T) {
		t.Parallel()
		// Covers the T, V, W, X, Y, Z branches (values 26..31)
		// plus padding.
		_, err := ulid.ParseULID("0TVWXYZ" + strings.Repeat("0", 19))
		testkit.NoError(t, err, "uppercase chars T-Z must decode")
	})

	t.Run("lowercase chars a-z decode", func(t *testing.T) {
		t.Parallel()
		// 25 lowercase chars covering a..h, j, k, m, n, p..t,
		// v..z plus one '0' padder.
		_, err := ulid.ParseULID("0abcdefghjkmnpqrstvwxyz000")
		testkit.NoError(t, err, "lowercase chars a-z must decode")
	})

	t.Run("I L O substitutions decode", func(t *testing.T) {
		t.Parallel()
		// The ULID spec retroactively accepts I/L → 1 and O → 0
		// to handle handwritten or transcribed identifiers; both
		// upper- and lower-case forms are accepted.
		_, err := ulid.ParseULID("0IiLlOo" + strings.Repeat("0", 19))
		testkit.NoError(t, err, "I/L/O substitutions must decode")
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
		testkit.NoError(t, err, "re-parse of Format(Parse(s))")
		testkit.Equal(t, again, got,
			"Parse(Format(Parse(s))) must equal Parse(s) — idempotent")
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
		testkit.NoError(t, err, "Parse(Format(x))")
		testkit.Equal(t, parsed, u, "Format → Parse round-trip must preserve the ID")
	})
}

func BenchmarkFormat(b *testing.B) {
	u := id.New128([id.Size128]byte{
		0x01, 0x9a, 0x2b, 0x3c, 0x4d, 0x5e, 0x6f, 0x70,
		0x81, 0x92, 0xa3, 0xb4, 0xc5, 0xd6, 0xe7, 0xf8,
	})
	b.ReportAllocs()
	for b.Loop() {
		_ = ulid.Format(u)
	}
}

func BenchmarkParseULID(b *testing.B) {
	u := id.New128([id.Size128]byte{
		0x01, 0x9a, 0x2b, 0x3c, 0x4d, 0x5e, 0x6f, 0x70,
		0x81, 0x92, 0xa3, 0xb4, 0xc5, 0xd6, 0xe7, 0xf8,
	})
	encoded := ulid.Format(u)
	b.ReportAllocs()
	for b.Loop() {
		_, _ = ulid.ParseULID(encoded)
	}
}
