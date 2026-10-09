// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package uuidv7_test

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/uuidv7"
)

func TestParse(t *testing.T) {
	t.Parallel()

	t.Run("Format", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the lowercase text form of a 128-bit ID", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, uuidv7.Format(exampleID), exampleText, "Format must return the text form")
		})

		for _, tt := range notUUIDs {
			t.Run("returns the empty string for "+tt.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, uuidv7.Format(tt.id), "", "Format must return the empty string")
			})
		}
	})

	t.Run("Parse", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the ID of a text form", func(t *testing.T) {
			t.Parallel()
			got, err := uuidv7.Parse(exampleText)
			assert.NoError(t, err, "Parse must accept the text form")
			assert.Equal(t, got, exampleID, "Parse must return the encoded bytes")
		})

		t.Run("returns the ID that Format encodes", func(t *testing.T) {
			t.Parallel()
			uuids := prop.Bytes(prop.MinSize(id.Size128), prop.MaxSize(id.Size128)).Map(func(b []byte) id.ID {
				return id.New128([id.Size128]byte(b))
			})
			prop.RoundTrip(t, func(u id.ID) (string, error) { return uuidv7.Format(u), nil }, uuidv7.Parse,
				"Parse must return the ID whose text form Format returns", prop.Using(uuids))
		})

		tests := []struct {
			name string
			give string
			want error
		}{
			{
				name: "returns ErrInvalidLength for a text that is not 36 bytes",
				give: exampleText[:35],
				want: uuidv7.ErrInvalidLength,
			},
			{
				name: "returns ErrInvalidFormat for a text without one of its hyphens",
				give: exampleText[:8] + "0" + exampleText[9:],
				want: uuidv7.ErrInvalidFormat,
			},
			{
				name: "returns ErrInvalidChar for a byte other than a hexadecimal digit",
				give: "g" + exampleText[1:],
				want: uuidv7.ErrInvalidChar,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := uuidv7.Parse(tt.give)
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
func TestParseAllocs(t *testing.T) {
	t.Run("Format", func(t *testing.T) {
		var s string
		expect.MaxAllocs(t, func() { s = uuidv7.Format(exampleID) }, 1, "Format must allocate only the returned string")
		assert.Equal(t, s, exampleText, "the test must measure the text form of the UUIDv7")
	})

	t.Run("Parse", func(t *testing.T) {
		var (
			got id.ID
			err error
		)
		expect.MaxAllocs(t, func() { got, err = uuidv7.Parse(exampleText) }, 0, "Parse must not allocate")
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
			s = uuidv7.Format(exampleID)
		}

		assert.Equal(b, s, exampleText, "the benchmark must measure the text form of the UUIDv7")
	})

	b.Run("Parse", func(b *testing.B) {
		var (
			got id.ID
			err error
		)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, err = uuidv7.Parse(exampleText)
		}

		assert.NoError(b, err, "the benchmark must measure a text that Parse accepts")
		assert.Equal(b, got, exampleID, "the benchmark must measure the ID of the text")
	})
}
