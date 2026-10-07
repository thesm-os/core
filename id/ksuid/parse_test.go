// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package ksuid_test

import (
	"strings"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/ksuid"
)

// The contracts of parsesText and roundTrips, which the tests and the
// fuzz targets check.
const (
	parseContract     = "Format of the ID that Parse returns must equal the text"
	roundTripContract = "Parse must return the ID whose encoding Format returns"
)

// The reference KSUID of segmentio/ksuid: its bytes, a timestamp of
// 2017-10-09T21:46:47Z and 16 random bytes, and its encoding.
var (
	referenceBytes = [id.Size160]byte{
		0x06, 0x69, 0xf7, 0xef,
		0xb5, 0xa1, 0xcd, 0x34, 0xb5, 0xf9, 0x9d, 0x12,
		0x14, 0xe5, 0xb9, 0x16, 0x9d, 0xa6, 0x9c, 0x32,
	}
	referenceText = "0ujtsYcgvSTl8PAuR7PHXnl95SE"
)

// largestText is the encoding of the largest KSUID, whose 160 bits are
// all set. An encoding above it in the order of the alphabet overflows.
var largestText = ksuid.Format(id.New160([id.Size160]byte{
	0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
	0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
}))

// ksuids generates IDs of 160 bits.
var ksuids = prop.Bytes(prop.MinSize(id.Size160), prop.MaxSize(id.Size160)).Map(func(b []byte) id.ID {
	return id.New160([id.Size160]byte(b))
})

func TestParse(t *testing.T) {
	t.Parallel()

	t.Run("Format", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give id.ID
			want string
		}{
			{name: "returns the empty string for Zero", give: id.Zero, want: ""},
			{
				name: "returns the empty string for an ID shorter than 160 bits",
				give: id.New128([id.Size128]byte{1}),
				want: "",
			},
			{
				name: "returns 27 zeros for an ID of zero bytes",
				give: id.New160([id.Size160]byte{}),
				want: strings.Repeat("0", 27),
			},
			{
				name: "returns the reference encoding of segmentio/ksuid",
				give: id.New160(referenceBytes),
				want: referenceText,
			},
			{
				name: "returns the encoding of the first 160 bits of a 256-bit ID",
				give: id.New256([id.Size256]byte(append(referenceBytes[:], make([]byte, 12)...))),
				want: referenceText,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, ksuid.Format(tt.give), tt.want, "Format must return the base62 encoding")
			})
		}
	})

	t.Run("Parse", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the ID that Format encodes", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, roundTripContract, roundTrips)
		})

		t.Run("returns the ID of the reference encoding of segmentio/ksuid", func(t *testing.T) {
			t.Parallel()
			got, err := ksuid.Parse(referenceText)
			assert.NoError(t, err, "Parse must accept the reference encoding")
			assert.Equal(t, got, id.New160(referenceBytes), "Parse must decode the reference bytes")
		})

		t.Run("returns the ID of zero bytes for 27 zeros", func(t *testing.T) {
			t.Parallel()
			got, err := ksuid.Parse(strings.Repeat("0", 27))
			assert.NoError(t, err, "Parse must accept 27 zeros")
			assert.Equal(t, got, id.New160([id.Size160]byte{}), "Parse must decode the ID of zero bytes")
		})

		t.Run("returns an ID whose encoding is the text for every text it accepts", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, parseContract, parsesText)
		})

		t.Run("returns an ID for every encoding up to the largest KSUID", func(t *testing.T) {
			t.Parallel()
			below := prop.StringMatching(`[0-9A-Za][0-9A-Za-z]{26}`).Filter(func(s string) bool {
				return s <= largestText
			})
			prop.RoundTrip(t, ksuid.Parse, func(i id.ID) (string, error) { return ksuid.Format(i), nil },
				"Parse must decode every encoding that does not overflow", prop.Using(below))
		})

		t.Run("returns ErrOverflow for every encoding above the largest KSUID", func(t *testing.T) {
			t.Parallel()
			above := prop.StringMatching(`[a-z][0-9A-Za-z]{26}`).Filter(func(s string) bool { return s > largestText })
			prop.ErrorIs(t, func(s string) error {
				_, err := ksuid.Parse(s)

				return err
			}, ksuid.ErrOverflow, "an encoding above 2^160 must overflow",
				prop.Using(above), prop.Example(strings.Repeat("z", 27)))
		})

		t.Run("returns ErrInvalidLength for a text that is not 27 characters", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(n int) error {
				_, err := ksuid.Parse(strings.Repeat("0", n))

				return err
			}, ksuid.ErrInvalidLength, "a text of another length must be refused",
				prop.Using(prop.Integer(0, 100).Filter(func(n int) bool { return n != 27 })),
				prop.Example(0), prop.Example(1), prop.Example(26), prop.Example(28), prop.Example(100))
		})

		t.Run("returns ErrInvalidChar for a character outside the base62 alphabet", func(t *testing.T) {
			t.Parallel()
			outside := prop.Integer(0, 255).Filter(func(b int) bool {
				return !strings.ContainsRune("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz", rune(b))
			})
			prop.ForAll(t, "Parse must refuse a character outside the alphabet", func(c *prop.Case) {
				at := c.Draw(prop.Integer(0, 26), "position")
				b := c.Draw(outside, "character")

				text := []byte(strings.Repeat("0", 27))
				text[at] = byte(b)
				_, err := ksuid.Parse(string(text))
				assert.ErrorIs(c, err, ksuid.ErrInvalidChar, "Parse must return ErrInvalidChar")
			})
		})

		classes := []struct {
			name string
			give string
		}{
			{name: "returns an error of class Invalid for a text that is not 27 characters", give: "0"},
			{
				name: "returns an error of class Invalid for a character outside the alphabet",
				give: "!" + strings.Repeat("0", 26),
			},
			{name: "returns an error of class Invalid for an encoding above 2^160", give: strings.Repeat("z", 27)},
		}
		for _, tt := range classes {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, err := ksuid.Parse(tt.give)
				assert.Equal(t, errs.Classify(err), errs.Invalid, "the refusal must classify as Invalid")
			})
		}

		t.Run("returns an ID for every KSUID of a real-world corpus", func(t *testing.T) {
			t.Parallel()
			corpus := []string{
				"3DMNSJaqdg8XxB3ebyUCpTsfLua", "3DMNSD2051qXOhPcHPVbTFKTz4C", "3DMNSFiMJNt5TsQQ5MNFhqKJ4Gp",
				"3DMNSJjvkmc4nEWQL9vVdeXZlTL", "3DMNSHCOLcZxIO8wibbUGZCw0KP", "3DMNSEDrad9BA3jFMjhGmJxXXTy",
				"3DMNSJ5wZHLRbxza4VKLzm2iflc", "3DMNSKiQLBh4sDALxEiQR5zS9yS", "3DMNSGziNSXhSdsWDEYW6y3PD1I",
				"3DMNSD4nrmkBBDAdfOjlcEy0hlS", "3DMNSDp2ZPdf8VJuKaziSckFXga", "3DMNSHMmRXGequYgSilf9WPvxUY",
				"3DMNSKGZEskZA16SLP34v87f3Zj", "3DMNSFt1TrtUHtqiA60h4DleULA", "3DMNSG7R6P4YNlH2TwteNRuQxoT",
				"3DMNSDgBY1BCzO9h948yViUSt7G", "3DMNSGBkGhe2JIKZ4zfXi34WCoM", "3DMNSDGIxVHwPGoQSQRsuVgaHuR",
				"3DMNSK8j7CpI1vYudpsF7cnhixa", "3DMNSECLNkKXAzGQIoFjswYCU8T", "3DMNSGFZxAFDnrccaxRAqZfB9bE",
				"3DMNSF2ZMLQYcEJRXepTknRKLdb", "3DMNSFUge6Cu6lo3FkaYSAFgnF4", "3DMNSDOFP5RkewlijSxs7FBmF3R",
				"3DMNSGjRdUN8gJyehGQk5IflM9x",
			}
			assert.Total(t, func(s string) error {
				_, err := ksuid.Parse(s)

				return err
			}, corpus, "Parse must accept every KSUID of the corpus")
		})
	})
}

// TestParseAllocs checks the allocation contracts of Format and Parse.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
//
//nolint:paralleltest // see above
func TestParseAllocs(t *testing.T) {
	u := id.New160(referenceBytes)

	t.Run("Format", func(t *testing.T) {
		var s string
		expect.MaxAllocs(t, func() { s = ksuid.Format(u) }, 1, "Format must allocate only the returned string")
		assert.Equal(t, s, referenceText, "the test must measure the encoding of the KSUID")
	})

	t.Run("Parse", func(t *testing.T) {
		var (
			got id.ID
			err error
		)
		expect.MaxAllocs(t, func() { got, err = ksuid.Parse(referenceText) }, 0, "Parse must not allocate")
		assert.NoError(t, err, "the test must measure an encoding that Parse accepts")
		assert.Equal(t, got, u, "the test must measure the ID of the encoding")
	})
}

// BenchmarkParse reports the cost of Format and Parse, and fails when one
// allocates more than TestParseAllocs allows.
func BenchmarkParse(b *testing.B) {
	u := id.New160(referenceBytes)

	b.Run("Format", func(b *testing.B) {
		var s string

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			s = ksuid.Format(u)
		}

		assert.Equal(b, s, referenceText, "the benchmark must measure the encoding of the KSUID")
	})

	b.Run("Parse", func(b *testing.B) {
		var (
			got id.ID
			err error
		)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, err = ksuid.Parse(referenceText)
		}

		assert.NoError(b, err, "the benchmark must measure an encoding that Parse accepts")
		assert.Equal(b, got, u, "the benchmark must measure the ID of the encoding")
	})
}

// FuzzParse checks the contract of parsesText on the texts that a fuzzer
// finds. Each seed is a text after the octet of its length that the bridge
// reads first.
func FuzzParse(f *testing.F) {
	for _, s := range []string{referenceText, strings.Repeat("0", 27)} {
		f.Add(append([]byte{byte(len(s))}, s...))
	}

	prop.Fuzz(f, parseContract, parsesText)
}

// FuzzRoundTrip checks the contract of roundTrips on the IDs that a fuzzer
// finds.
func FuzzRoundTrip(f *testing.F) {
	f.Add(make([]byte, id.Size160))
	f.Add(referenceBytes[:])
	f.Add([]byte(strings.Repeat("\xff", id.Size160)))

	prop.Fuzz(f, roundTripContract, roundTrips)
}

// parsesText checks that Parse refuses a drawn text, or returns an ID
// whose encoding is the text. The base62 encoding of a KSUID has one form,
// so the comparison is exact.
func parsesText(c *prop.Case) {
	s := string(c.Draw(prop.Bytes(prop.MaxSize(54)), "text"))

	got, err := ksuid.Parse(s)
	if err == nil {
		assert.Equal(c, ksuid.Format(got), s, "Format(Parse(s)) must equal s")
	}
}

// roundTrips checks that Parse returns a drawn 160-bit ID from the
// encoding that Format returns for it.
func roundTrips(c *prop.Case) {
	want := c.Draw(ksuids, "id")
	assert.RoundTrip(c, func(i id.ID) (string, error) { return ksuid.Format(i), nil }, ksuid.Parse, want,
		"Parse must return the ID that Format encoded")
}
