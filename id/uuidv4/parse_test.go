// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package uuidv4_test

import (
	"strings"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/uuidv4"
)

// The contracts of parsesText and roundTrips, which the tests and the
// fuzz targets check.
const (
	parseContract     = "Format of the ID that Parse returns must equal the text apart from case"
	roundTripContract = "Parse must return the ID whose text form Format returns"
)

// exampleText is the text form of exampleID.
const exampleText = "12345678-9abc-4def-8012-3456789abcde"

// exampleID is a UUIDv4 whose bytes each differ.
var exampleID = id.New128([id.Size128]byte{
	0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0x4d, 0xef, 0x80, 0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde,
})

// uuids generates IDs of 128 bits.
var uuids = prop.Bytes(prop.MinSize(id.Size128), prop.MaxSize(id.Size128)).Map(func(b []byte) id.ID {
	return id.New128([id.Size128]byte(b))
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
			{name: "returns the empty string for the zero ID", give: id.Zero, want: ""},
			{name: "returns the empty string for a 160-bit ID", give: id.New160([id.Size160]byte{1}), want: ""},
			{name: "returns the empty string for a 256-bit ID", give: id.New256([id.Size256]byte{1}), want: ""},
			{
				name: "returns the text form of an ID of zero bytes",
				give: id.New128([id.Size128]byte{}),
				want: "00000000-0000-0000-0000-000000000000",
			},
			{name: "returns the hyphenated form of the bytes", give: exampleID, want: exampleText},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, uuidv4.Format(tt.give), tt.want, "Format must return the text form")
			})
		}

		t.Run("returns the lowercase text form of every 128-bit ID", func(t *testing.T) {
			t.Parallel()
			form := `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`
			prop.ForAll(t, "Format must return the lowercase text form", func(c *prop.Case) {
				assert.Matches(c, uuidv4.Format(c.Draw(uuids, "id")), form, "Format must return the text form")
			})
		})
	})

	t.Run("Parse", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the ID that Format encodes", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, roundTripContract, roundTrips)
		})

		tests := []struct {
			name string
			give string
			want id.ID
		}{
			{
				name: "returns the ID of zero bytes for the text form of zero bytes",
				give: "00000000-0000-0000-0000-000000000000",
				want: id.New128([id.Size128]byte{}),
			},
			{name: "returns the bytes that the text form encodes", give: exampleText, want: exampleID},
			{
				name: "returns the bytes that an uppercase text form encodes",
				give: strings.ToUpper(exampleText),
				want: exampleID,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := uuidv4.Parse(tt.give)
				assert.NoError(t, err, "Parse must accept the text form")
				assert.Equal(t, got, tt.want, "Parse must return the encoded bytes")
			})
		}

		t.Run("returns an ID whose text form is the text for any text that it accepts", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, parseContract, parsesText)
		})

		refused := []struct {
			name string
			give string
			want error
		}{
			{
				name: "returns ErrInvalidLength for a text of 35 bytes",
				give: exampleText[:35],
				want: uuidv4.ErrInvalidLength,
			},
			{
				name: "returns ErrInvalidLength for a text of 37 bytes",
				give: exampleText + "f",
				want: uuidv4.ErrInvalidLength,
			},
			{
				name: "returns ErrInvalidFormat for a text without the hyphen at byte 8",
				give: "12345678X9abc-4def-8012-3456789abcde",
				want: uuidv4.ErrInvalidFormat,
			},
			{
				name: "returns ErrInvalidChar for a byte other than a hexadecimal digit",
				give: "12345678-9abc-4def-8z12-3456789abcde",
				want: uuidv4.ErrInvalidChar,
			},
		}
		for _, tt := range refused {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := uuidv4.Parse(tt.give)
				expect.ErrorIs(t, err, tt.want, "Parse must return the sentinel of the text")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "the sentinel must classify as Invalid")
				expect.Equal(t, got, id.Zero, "Parse must return the zero ID with an error")
			})
		}
	})
}

// TestParseAllocs checks the allocation contracts of Format and Parse.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
//
//nolint:paralleltest // see above
func TestParseAllocs(t *testing.T) {
	t.Run("Format", func(t *testing.T) {
		var s string
		expect.MaxAllocs(t, func() { s = uuidv4.Format(exampleID) }, 1, "Format must allocate only the returned string")
		assert.Equal(t, s, exampleText, "the test must measure the text form of the ID")
	})

	t.Run("Parse", func(t *testing.T) {
		var (
			got id.ID
			err error
		)
		expect.MaxAllocs(t, func() { got, err = uuidv4.Parse(exampleText) }, 0, "Parse must not allocate")
		assert.NoError(t, err, "the test must measure a text that Parse accepts")
		assert.Equal(t, got, exampleID, "the test must measure the ID of the text")
	})
}

// BenchmarkParse reports the cost of Format and Parse, and fails when one
// allocates more than TestParseAllocs allows.
func BenchmarkParse(b *testing.B) {
	b.Run("Format", func(b *testing.B) {
		var s string

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			s = uuidv4.Format(exampleID)
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
			got, err = uuidv4.Parse(exampleText)
		}

		assert.NoError(b, err, "the benchmark must measure a text that Parse accepts")
		assert.Equal(b, got, exampleID, "the benchmark must measure the ID of the text")
	})
}

// FuzzParse checks the contract of parsesText on the texts that a fuzzer
// finds. Each seed is a text after the octet of its length that the bridge
// reads first.
func FuzzParse(f *testing.F) {
	for _, s := range []string{exampleText, "00000000-0000-0000-0000-000000000000", strings.ToUpper(exampleText)} {
		f.Add(append([]byte{byte(len(s))}, s...))
	}

	prop.Fuzz(f, parseContract, parsesText)
}

// FuzzRoundTrip checks the contract of roundTrips on the IDs that a fuzzer
// finds.
func FuzzRoundTrip(f *testing.F) {
	f.Add(make([]byte, id.Size128))
	f.Add(exampleID.Bytes())

	prop.Fuzz(f, roundTripContract, roundTrips)
}

// parsesText checks that Parse refuses a drawn text, or returns an ID
// whose text form equals the text apart from case.
func parsesText(c *prop.Case) {
	s := string(c.Draw(prop.Bytes(prop.MaxSize(72)), "text"))

	got, err := uuidv4.Parse(s)
	if err == nil {
		assert.True(c, strings.EqualFold(uuidv4.Format(got), s), "Format(Parse(s)) must equal s apart from case")
	}
}

// roundTrips checks that Parse returns a drawn 128-bit ID from the text
// form that Format returns for it.
func roundTrips(c *prop.Case) {
	want := c.Draw(uuids, "id")

	got, err := uuidv4.Parse(uuidv4.Format(want))
	assert.NoError(c, err, "Parse must accept the text form that Format returns")
	assert.Equal(c, got, want, "Parse must return the ID that Format encoded")
}
