// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package uuidtext_test

import (
	"strconv"
	"strings"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/internal/uuidtext"
)

// exampleText is the text form of the example UUID in section 4 of
// RFC 9562.
const exampleText = "f81d4fae-7dec-11d0-a765-00a0c91e6bf6"

// benchRuns is the number of calls over which a benchmark averages the
// allocations that it checks.
const benchRuns = 100

// exampleBytes are the bytes of the UUID whose text form is exampleText.
var exampleBytes = [id.Size128]byte{
	0xf8, 0x1d, 0x4f, 0xae, 0x7d, 0xec, 0x11, 0xd0,
	0xa7, 0x65, 0x00, 0xa0, 0xc9, 0x1e, 0x6b, 0xf6,
}

var (
	errLength = testkit.TestError("the text is not 36 bytes")
	errFormat = testkit.TestError("a hyphen is missing")
	errChar   = testkit.TestError("a group has a byte other than a hex digit")

	// sentinels are the errors that the tests pass to Parse.
	sentinels = uuidtext.Errors{Length: errLength, Format: errFormat, Char: errChar}
)

// The benchmarks write each result to a sink, so that the compiler keeps
// every call that they measure.
var (
	sinkText string
	sinkID   id.ID
)

// notUUIDs returns IDs that are not 128 bits, each named by its size.
func notUUIDs() map[string]id.ID {
	return map[string]id.ID{
		"the zero ID":  id.Zero,
		"a 160-bit ID": id.New160([id.Size160]byte{1}),
		"a 256-bit ID": id.New256([id.Size256]byte{1}),
	}
}

func TestFormat(t *testing.T) {
	t.Parallel()

	t.Run("returns the lowercase text form of a 128-bit ID", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, uuidtext.Format(id.New128(exampleBytes)), exampleText,
			"Format must return the text form")
	})

	t.Run("returns the empty string for an ID that is not 128 bits", func(t *testing.T) {
		t.Parallel()
		for name, u := range notUUIDs() {
			testkit.Equal(t, uuidtext.Format(u), "", "Format must return the empty string for "+name)
		}
	})
}

func TestParse(t *testing.T) {
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
			testkit.NoError(t, err, "Parse must accept "+tt.give)
			testkit.Equal(t, got, id.New128(exampleBytes), "Parse must return the encoded bytes")
		})
	}

	t.Run("returns the ID that Format encodes", func(t *testing.T) {
		t.Parallel()
		want := id.New128([id.Size128]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 255})
		got, err := uuidtext.Parse(uuidtext.Format(want), sentinels)
		testkit.NoError(t, err, "Parse must accept the text form that Format returns")
		testkit.Equal(t, got, want, "Parse must return the ID that Format encoded")
	})

	t.Run("returns Errors.Length for a text that is not 36 bytes", func(t *testing.T) {
		t.Parallel()
		for _, s := range []string{"", exampleText[:35], exampleText + "0"} {
			got, err := uuidtext.Parse(s, sentinels)
			testkit.ErrorIs(t, err, errLength,
				"Parse must return Errors.Length for a text of "+strconv.Itoa(len(s))+" bytes")
			testkit.Equal(t, got, id.Zero, "Parse must return the zero ID with an error")
		}
	})

	t.Run("returns Errors.Format for a text without one of its hyphens", func(t *testing.T) {
		t.Parallel()
		for _, at := range []int{8, 13, 18, 23} {
			s := exampleText[:at] + "0" + exampleText[at+1:]
			got, err := uuidtext.Parse(s, sentinels)
			testkit.ErrorIs(t, err, errFormat, "Parse must return Errors.Format for "+s)
			testkit.Equal(t, got, id.Zero, "Parse must return the zero ID with an error")
		}
	})

	t.Run("returns Errors.Char for a byte other than a hex digit in any group", func(t *testing.T) {
		t.Parallel()
		for _, at := range []int{0, 12, 14, 22, 35} {
			s := exampleText[:at] + "g" + exampleText[at+1:]
			got, err := uuidtext.Parse(s, sentinels)
			testkit.ErrorIs(t, err, errChar, "Parse must return Errors.Char for "+s)
			testkit.Equal(t, got, id.Zero, "Parse must return the zero ID with an error")
		}
	})
}

// FuzzParse checks that Parse does not panic, and that the text form of
// every ID it returns equals its input apart from case.
func FuzzParse(f *testing.F) {
	f.Add(exampleText)
	f.Add(strings.ToUpper(exampleText))
	f.Add("00000000-0000-0000-0000-00000000000g")

	f.Fuzz(func(t *testing.T, s string) {
		got, err := uuidtext.Parse(s, sentinels)
		if err != nil {
			return
		}

		testkit.True(t, strings.EqualFold(uuidtext.Format(got), s),
			"Format(Parse(s)) must equal s apart from case")
	})
}

// FuzzRoundTrip checks that Parse returns every 128-bit ID from the text
// form that Format returns for it.
func FuzzRoundTrip(f *testing.F) {
	f.Add(exampleBytes[:])

	f.Fuzz(func(t *testing.T, data []byte) {
		var raw [id.Size128]byte
		copy(raw[:], data)
		want := id.New128(raw)

		got, err := uuidtext.Parse(uuidtext.Format(want), sentinels)
		testkit.NoError(t, err, "Parse must accept the text form that Format returns")
		testkit.Equal(t, got, want, "Parse must return the ID that Format encoded")
	})
}

// BenchmarkFormat reports the cost of Format, and fails when it allocates
// more than the returned string.
func BenchmarkFormat(b *testing.B) {
	u := id.New128(exampleBytes)
	format := func() { sinkText = uuidtext.Format(u) }

	if allocs := testing.AllocsPerRun(benchRuns, format); allocs > 1 {
		b.Fatalf("Format allocates %v times per call, want 1", allocs)
	}

	b.ReportAllocs()
	for b.Loop() {
		format()
	}
}

// BenchmarkParse reports the cost of Parse, and fails when it allocates.
func BenchmarkParse(b *testing.B) {
	parse := func() { sinkID, _ = uuidtext.Parse(exampleText, sentinels) }

	if allocs := testing.AllocsPerRun(benchRuns, parse); allocs != 0 {
		b.Fatalf("Parse allocates %v times per call, want 0", allocs)
	}

	b.ReportAllocs()
	for b.Loop() {
		parse()
	}
}
