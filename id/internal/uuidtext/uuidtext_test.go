// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package uuidtext_test

import (
	"errors"
	"strings"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/internal/uuidtext"
)

// exampleText is the text form of the example UUID in section 4 of
// RFC 9562.
const exampleText = "f81d4fae-7dec-11d0-a765-00a0c91e6bf6"

// The contracts of parsesText and roundTrips, which the tests and the
// fuzz targets check.
const (
	parseContract     = "Format of the ID that Parse returns must equal the text apart from case"
	roundTripContract = "Parse must return the ID whose text form Format returns"
)

// exampleBytes are the bytes of the UUID whose text form is exampleText.
var exampleBytes = [id.Size128]byte{
	0xf8, 0x1d, 0x4f, 0xae, 0x7d, 0xec, 0x11, 0xd0,
	0xa7, 0x65, 0x00, 0xa0, 0xc9, 0x1e, 0x6b, 0xf6,
}

var (
	errLength = errors.New("the text is not 36 bytes")
	errFormat = errors.New("a hyphen is missing")
	errChar   = errors.New("a group has a byte other than a hex digit")

	// sentinels are the errors that the tests pass to Parse.
	sentinels = uuidtext.Errors{Length: errLength, Format: errFormat, Char: errChar}
)

// texts generates text forms of UUIDs, with digits in either case.
var texts = prop.StringMatching(
	`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`,
)

// uuids generates IDs of 128 bits.
var uuids = prop.Bytes(prop.MinSize(id.Size128), prop.MaxSize(id.Size128)).Map(func(b []byte) id.ID {
	return id.New128([id.Size128]byte(b))
})

func TestUUIDText(t *testing.T) {
	t.Parallel()

	t.Run("Format", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the lowercase text form of a 128-bit ID", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, uuidtext.Format(id.New128(exampleBytes)), exampleText, "Format must return the text form")
		})

		tests := []struct {
			name string
			give id.ID
		}{
			{name: "returns the empty string for the zero ID", give: id.Zero},
			{name: "returns the empty string for a 160-bit ID", give: id.New160([id.Size160]byte{1})},
			{name: "returns the empty string for a 256-bit ID", give: id.New256([id.Size256]byte{1})},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, uuidtext.Format(tt.give), "", "Format must return the empty string")
			})
		}
	})

	t.Run("Parse", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give string
		}{
			{name: "returns the ID of a lowercase text form", give: exampleText},
			{name: "returns the ID of an uppercase text form", give: strings.ToUpper(exampleText)},
			{name: "returns the ID of a mixed-case text form", give: "F81D4FAE-7dec-11D0-a765-00A0c91e6BF6"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := uuidtext.Parse(tt.give, sentinels)
				assert.NoError(t, err, "Parse must accept the text form")
				assert.Equal(t, got, id.New128(exampleBytes), "Parse must return the encoded bytes")
			})
		}

		t.Run("returns the ID that Format encodes", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, roundTripContract, roundTrips)
		})

		t.Run("returns an ID whose text form is a text form in either case", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Parse must accept every text form of a UUID", func(c *prop.Case) {
				s := c.Draw(texts, "text")

				got, err := uuidtext.Parse(s, sentinels)
				assert.NoError(c, err, "Parse must accept the text form")
				assert.True(c, strings.EqualFold(uuidtext.Format(got), s),
					"Format(Parse(s)) must equal s apart from case")
			})
		})

		t.Run("returns an ID whose text form is the text for any text that it accepts", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, parseContract, parsesText)
		})

		t.Run("returns Errors.Length for a text that is not 36 bytes", func(t *testing.T) {
			t.Parallel()
			lengths := prop.Integer(0, 2*uuidtext.Len).Filter(func(n int) bool { return n != uuidtext.Len })
			prop.ForAll(t, "Parse must refuse a text of another length", func(c *prop.Case) {
				n := c.Draw(lengths, "length")

				got, err := uuidtext.Parse(strings.Repeat("0", n), sentinels)
				assert.ErrorIs(c, err, errLength, "Parse must return Errors.Length")
				assert.True(c, got.IsZero(), "Parse must return the zero ID with an error")
			})
		})

		t.Run("returns Errors.Format for a text without one of its hyphens", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Parse must refuse a text without a hyphen", func(c *prop.Case) {
				at := c.Draw(prop.SampledFrom(8, 13, 18, 23), "hyphen")

				got, err := uuidtext.Parse(exampleText[:at]+"0"+exampleText[at+1:], sentinels)
				assert.ErrorIs(c, err, errFormat, "Parse must return Errors.Format")
				assert.True(c, got.IsZero(), "Parse must return the zero ID with an error")
			})
		})

		t.Run("returns Errors.Char for a byte other than a hexadecimal digit", func(t *testing.T) {
			t.Parallel()
			digits := prop.Integer(0, uuidtext.Len-1).Filter(func(i int) bool {
				return i != 8 && i != 13 && i != 18 && i != 23
			})
			prop.ForAll(t, "Parse must refuse a group with another byte", func(c *prop.Case) {
				at := c.Draw(digits, "digit")

				got, err := uuidtext.Parse(exampleText[:at]+"g"+exampleText[at+1:], sentinels)
				assert.ErrorIs(c, err, errChar, "Parse must return Errors.Char")
				assert.True(c, got.IsZero(), "Parse must return the zero ID with an error")
			}, prop.Cases(200))
		})
	})
}

// TestUUIDTextAllocs checks the allocation contracts of Format and Parse.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
//
//nolint:paralleltest // see above
func TestUUIDTextAllocs(t *testing.T) {
	u := id.New128(exampleBytes)

	t.Run("Format", func(t *testing.T) {
		var s string
		expect.MaxAllocs(t, func() { s = uuidtext.Format(u) }, 1, "Format must allocate only the returned string")
		assert.Equal(t, s, exampleText, "the test must measure the text form of the ID")
	})

	t.Run("Parse", func(t *testing.T) {
		var (
			got id.ID
			err error
		)
		expect.MaxAllocs(t, func() { got, err = uuidtext.Parse(exampleText, sentinels) }, 0, "Parse must not allocate")
		assert.NoError(t, err, "the test must measure a text that Parse accepts")
		assert.Equal(t, got, u, "the test must measure the ID of the text")
	})
}

// BenchmarkUUIDText reports the cost of Format and Parse, and fails when
// one allocates more than TestUUIDTextAllocs allows.
func BenchmarkUUIDText(b *testing.B) {
	u := id.New128(exampleBytes)

	b.Run("Format", func(b *testing.B) {
		var s string

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			s = uuidtext.Format(u)
		}

		assert.Equal(b, s, exampleText, "the benchmark must measure the text form of the ID")
	})

	b.Run("Parse", func(b *testing.B) {
		var (
			got id.ID
			err error
		)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, err = uuidtext.Parse(exampleText, sentinels)
		}

		assert.NoError(b, err, "the benchmark must measure a text that Parse accepts")
		assert.Equal(b, got, u, "the benchmark must measure the ID of the text")
	})
}

// FuzzParse checks the contract of parsesText on the texts that a fuzzer
// finds. Each seed is a text after the octet of its length that the bridge
// reads first.
func FuzzParse(f *testing.F) {
	for _, s := range []string{exampleText, strings.ToUpper(exampleText), "00000000-0000-0000-0000-00000000000g"} {
		f.Add(append([]byte{byte(len(s))}, s...))
	}

	prop.Fuzz(f, parseContract, parsesText)
}

// FuzzRoundTrip checks the contract of roundTrips on the IDs that a fuzzer
// finds.
func FuzzRoundTrip(f *testing.F) {
	f.Add(exampleBytes[:])
	prop.Fuzz(f, roundTripContract, roundTrips)
}

// parsesText checks that Parse refuses a drawn text, or returns an ID
// whose text form equals the text apart from case.
func parsesText(c *prop.Case) {
	s := string(c.Draw(prop.Bytes(prop.MaxSize(2*uuidtext.Len)), "text"))

	got, err := uuidtext.Parse(s, sentinels)
	if err == nil {
		assert.True(c, strings.EqualFold(uuidtext.Format(got), s), "Format(Parse(s)) must equal s apart from case")
	}
}

// roundTrips checks that Parse returns a drawn 128-bit ID from the text
// form that Format returns for it.
func roundTrips(c *prop.Case) {
	want := c.Draw(uuids, "id")
	assert.RoundTrip(c, func(i id.ID) (string, error) { return uuidtext.Format(i), nil },
		func(s string) (id.ID, error) { return uuidtext.Parse(s, sentinels) }, want,
		"Parse must return the ID that Format encoded")
}
