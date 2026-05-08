// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package ksuid_test

import (
	"strings"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/ksuid"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/seeded"
)

func TestFormat(t *testing.T) {
	t.Parallel()

	t.Run("Zero formats to empty (size 0)", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, ksuid.Format(id.Zero), "", "Format(Zero) must be empty")
	})

	t.Run("all-zero 160-bit ID encodes to 27 zeros", func(t *testing.T) {
		t.Parallel()
		u := id.New160([id.Size160]byte{})
		const want = "000000000000000000000000000"
		testkit.Equal(t, ksuid.Format(u), want, "Format of all-zero ID must encode to 27 zeros")
	})

	// Frozen-output vector cross-checked against the segmentio/ksuid
	// reference encoding for the same byte payload.
	t.Run("known KSUID encodes to the canonical form", func(t *testing.T) {
		t.Parallel()
		// Bytes 0..3: timestamp 107608047 (offset from KSUID epoch
		// → 2017-10-09T21:46:47Z absolute).
		// Bytes 4..19: 16-byte random payload from segmentio/ksuid
		// README example.
		raw := [id.Size160]byte{
			0x06, 0x69, 0xf7, 0xef,
			0xb5, 0xa1, 0xcd, 0x34, 0xb5, 0xf9, 0x9d, 0x12,
			0x14, 0xe5, 0xb9, 0x16, 0x9d, 0xa6, 0x9c, 0x32,
		}
		u := id.New160(raw)
		const want = "0ujtsYcgvSTl8PAuR7PHXnl95SE"
		testkit.Equal(t, ksuid.Format(u), want, "Format must match segmentio/ksuid reference")
	})
}

func TestParse(t *testing.T) {
	t.Parallel()

	t.Run("round-trips Format on a generated KSUID", func(t *testing.T) {
		t.Parallel()
		origin := time.Date(2026, 6, 15, 12, 34, 56, 0, time.UTC)
		g := ksuid.New(fake.New(origin), seeded.New(rand.Seed(7)))
		want := g.Generate()

		got, err := ksuid.Parse(ksuid.Format(want))
		testkit.NoError(t, err, "Parse")
		testkit.Equal(t, got, want, "Parse(Format(x)) must round-trip")
	})

	t.Run("decodes the all-zero canonical encoding", func(t *testing.T) {
		t.Parallel()
		got, err := ksuid.Parse("000000000000000000000000000")
		testkit.NoError(t, err, "Parse")
		testkit.Equal(t, got, id.New160([id.Size160]byte{}),
			"all-zero parse must decode to all-zero 160-bit ID")
	})

	t.Run("decodes the known segmentio reference vector", func(t *testing.T) {
		t.Parallel()
		got, err := ksuid.Parse("0ujtsYcgvSTl8PAuR7PHXnl95SE")
		testkit.NoError(t, err, "Parse")
		want := id.New160([id.Size160]byte{
			0x06, 0x69, 0xf7, 0xef,
			0xb5, 0xa1, 0xcd, 0x34, 0xb5, 0xf9, 0x9d, 0x12,
			0x14, 0xe5, 0xb9, 0x16, 0x9d, 0xa6, 0x9c, 0x32,
		})
		testkit.Equal(t, got, want, "Parse must decode to the segmentio reference bytes")
	})

	t.Run("rejects wrong length", func(t *testing.T) {
		t.Parallel()
		cases := []string{
			"",
			"0",
			strings.Repeat("0", 26),
			strings.Repeat("0", 28),
			strings.Repeat("0", 100),
		}
		for _, s := range cases {
			_, err := ksuid.Parse(s)
			testkit.ErrorIs(t, err, ksuid.ErrInvalidLength,
				"wrong-length input must return ErrInvalidLength")
		}
	})

	t.Run("rejects characters outside base62 alphabet", func(t *testing.T) {
		t.Parallel()
		// '!' at position 0 is not alphanumeric.
		_, err := ksuid.Parse("!" + strings.Repeat("0", 26))
		testkit.ErrorIs(t, err, ksuid.ErrInvalidChar,
			"non-alphanumeric input must return ErrInvalidChar")

		// Hyphen mid-string.
		_, err = ksuid.Parse(strings.Repeat("0", 13) + "-" + strings.Repeat("0", 13))
		testkit.ErrorIs(t, err, ksuid.ErrInvalidChar,
			"hyphen mid-string must return ErrInvalidChar")
	})

	t.Run("rejects values exceeding 2^160", func(t *testing.T) {
		t.Parallel()
		// 27 'z' chars = 62^27 - 1, the maximum representable
		// base62 27-character value (~2.66e48), well above 2^160
		// (~1.46e48).
		_, err := ksuid.Parse(strings.Repeat("z", 27))
		testkit.ErrorIs(t, err, ksuid.ErrOverflow,
			"value above 2^160 must return ErrOverflow")
	})

	// Alphabet-coverage corpus. Real-world KSUIDs sampled at a
	// single timestamp prefix ("3DMNS...") with diverse random
	// suffixes. Collectively the suffix bytes exercise every
	// branch of the base62 decoder ([decodeChar]) — digits,
	// uppercase A-Z, lowercase a-z.
	t.Run("real-world corpus parses without error", func(t *testing.T) {
		t.Parallel()
		corpus := []string{
			"3DMNSJaqdg8XxB3ebyUCpTsfLua",
			"3DMNSD2051qXOhPcHPVbTFKTz4C",
			"3DMNSFiMJNt5TsQQ5MNFhqKJ4Gp",
			"3DMNSJjvkmc4nEWQL9vVdeXZlTL",
			"3DMNSHCOLcZxIO8wibbUGZCw0KP",
			"3DMNSEDrad9BA3jFMjhGmJxXXTy",
			"3DMNSJ5wZHLRbxza4VKLzm2iflc",
			"3DMNSKiQLBh4sDALxEiQR5zS9yS",
			"3DMNSGziNSXhSdsWDEYW6y3PD1I",
			"3DMNSD4nrmkBBDAdfOjlcEy0hlS",
			"3DMNSDp2ZPdf8VJuKaziSckFXga",
			"3DMNSHMmRXGequYgSilf9WPvxUY",
			"3DMNSKGZEskZA16SLP34v87f3Zj",
			"3DMNSFt1TrtUHtqiA60h4DleULA",
			"3DMNSG7R6P4YNlH2TwteNRuQxoT",
			"3DMNSDgBY1BCzO9h948yViUSt7G",
			"3DMNSGBkGhe2JIKZ4zfXi34WCoM",
			"3DMNSDGIxVHwPGoQSQRsuVgaHuR",
			"3DMNSK8j7CpI1vYudpsF7cnhixa",
			"3DMNSECLNkKXAzGQIoFjswYCU8T",
			"3DMNSGFZxAFDnrccaxRAqZfB9bE",
			"3DMNSF2ZMLQYcEJRXepTknRKLdb",
			"3DMNSFUge6Cu6lo3FkaYSAFgnF4",
			"3DMNSDOFP5RkewlijSxs7FBmF3R",
			"3DMNSGjRdUN8gJyehGQk5IflM9x",
		}
		for _, s := range corpus {
			got, err := ksuid.Parse(s)
			testkit.NoError(t, err, "Parse "+s)
			testkit.False(t, got.IsZero(), "Parse must not produce Zero for non-zero input")
			// Round-trip: Format(Parse(s)) must equal s.
			testkit.Equal(t, ksuid.Format(got), s, "Format(Parse(s)) must equal s")
		}
	})
}

// FuzzParse asserts [ksuid.Parse] never panics on arbitrary
// input, and that successful parses round-trip exactly through
// Format. KSUID's base62 encoding is bytewise-stable, so
// Format(Parse(s)) == s for every parseable s.
func FuzzParse(f *testing.F) {
	f.Add("0ujtsYcgvSTl8PAuR7PHXnl95SE")
	f.Add("000000000000000000000000000")

	f.Fuzz(func(t *testing.T, s string) {
		got, err := ksuid.Parse(s)
		if err != nil {
			return
		}
		testkit.Equal(t, ksuid.Format(got), s, "Format(Parse(s)) must equal s")
	})
}

// FuzzRoundTrip asserts the Format → Parse round-trip on
// arbitrary 160-bit payloads: any [id.ID] produced from 20
// bytes must Format to a string that Parse decodes back to the
// original ID. This exercises the base62 big-int divide-by-62
// encode loop and the multiply-by-62 decode loop end-to-end.
func FuzzRoundTrip(f *testing.F) {
	f.Add(make([]byte, id.Size160))
	f.Add([]byte{
		0x06, 0x69, 0xf7, 0xef,
		0xb5, 0xa1, 0xcd, 0x34, 0xb5, 0xf9, 0x9d, 0x12,
		0x14, 0xe5, 0xb9, 0x16, 0x9d, 0xa6, 0x9c, 0x32,
	})
	f.Add([]byte{
		0xff, 0xff, 0xff, 0xff,
		0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
		0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
	})

	f.Fuzz(func(t *testing.T, data []byte) {
		var raw [id.Size160]byte
		copy(raw[:], data)
		u := id.New160(raw)

		formatted := ksuid.Format(u)
		parsed, err := ksuid.Parse(formatted)
		testkit.NoError(t, err, "Parse(Format(x))")
		testkit.Equal(t, parsed, u, "Format → Parse round-trip must preserve the ID")
	})
}

func BenchmarkFormat(b *testing.B) {
	u := id.New160([id.Size160]byte{
		0x06, 0x69, 0xf7, 0xef, 0xb5, 0xa1, 0xcd, 0x34,
		0xb5, 0xf9, 0x9d, 0x12, 0x14, 0xe5, 0xb9, 0x16,
		0x9d, 0xa6, 0x9c, 0x32,
	})
	b.ReportAllocs()
	for b.Loop() {
		_ = ksuid.Format(u)
	}
}

func BenchmarkParse(b *testing.B) {
	encoded := "0ujtsYcgvSTl8PAuR7PHXnl95SE"
	b.ReportAllocs()
	for b.Loop() {
		_, _ = ksuid.Parse(encoded)
	}
}
