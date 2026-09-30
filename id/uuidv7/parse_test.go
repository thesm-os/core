// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package uuidv7_test

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/id/uuidv7"
)

// sinkID receives the results of Parse in BenchmarkParse, so that the
// compiler keeps every call that it measures.
var sinkID id.ID

func TestFormat(t *testing.T) {
	t.Parallel()

	t.Run("returns the lowercase text form of a 128-bit ID", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, uuidv7.Format(exampleID), exampleText, "Format must return the text form")
	})

	t.Run("returns the empty string for an ID that is not 128 bits", func(t *testing.T) {
		t.Parallel()
		for name, u := range notUUIDs() {
			testkit.Equal(t, uuidv7.Format(u), "", "Format must return the empty string for "+name)
		}
	})
}

func TestParse(t *testing.T) {
	t.Parallel()

	t.Run("returns the ID of a text form", func(t *testing.T) {
		t.Parallel()
		got, err := uuidv7.Parse(exampleText)
		testkit.NoError(t, err, "Parse must accept the text form")
		testkit.Equal(t, got, exampleID, "Parse must return the encoded bytes")
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
			name: "returns ErrInvalidChar for a byte other than a hex digit",
			give: "g" + exampleText[1:],
			want: uuidv7.ErrInvalidChar,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := uuidv7.Parse(tt.give)
			testkit.ErrorIs(t, err, tt.want, "Parse must return the sentinel error for "+tt.give)
			testkit.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
			testkit.Equal(t, got, id.Zero, "Parse must return the zero ID with an error")
		})
	}
}

// BenchmarkParse reports the cost of Parse, and fails when it allocates.
func BenchmarkParse(b *testing.B) {
	benchZeroAlloc(b, func() { sinkID, _ = uuidv7.Parse(exampleText) })
}
