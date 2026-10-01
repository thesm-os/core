// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package note_test

import (
	"encoding/base64"
	"encoding/binary"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/note"
)

// peterLine is the signature line of PeterNeumann over peterText in
// golang.org/x/mod's tests.
const peterLine = "— PeterNeumann " + peterValue + "\n"

// peterSignature returns the Signature of peterLine.
func peterSignature(tb testing.TB) note.Signature {
	tb.Helper()

	raw, err := base64.StdEncoding.DecodeString(peterValue)
	testkit.NoError(tb, err, "the fixture must decode")

	return note.Signature{Name: "PeterNeumann", Value: raw[4:], ID: binary.BigEndian.Uint32(raw)}
}

func TestSignature(t *testing.T) {
	t.Parallel()

	t.Run("Valid", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give note.Signature
			want bool
		}{
			{name: "reports true for a line with a name and a value", give: peterSignature(t), want: true},
			{
				name: "reports false for an invalid name",
				give: note.Signature{Name: "a b", Value: []byte{1}},
				want: false,
			},
			{name: "reports false for an empty value", give: note.Signature{Name: "a", Value: []byte{}}, want: false},
			{name: "reports false for the zero Signature", give: note.Signature{}, want: false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(t, tt.give.Valid(), tt.want, "Valid must report whether the line is complete")
			})
		}
	})

	t.Run("AppendText", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the line that golang.org/x/mod's tests record", func(t *testing.T) {
			t.Parallel()
			got, err := peterSignature(t).AppendText([]byte("text\n\n"))
			testkit.NoError(t, err, "AppendText must accept a valid line")
			testkit.Equal(t, string(got), "text\n\n"+peterLine, "AppendText must append the signature line")
		})

		t.Run("returns b unchanged and ErrKey for an invalid name", func(t *testing.T) {
			t.Parallel()
			got, err := note.Signature{Name: "a b", Value: []byte{1}}.AppendText([]byte("text"))
			testkit.ErrorIs(t, err, note.ErrKey, "AppendText must refuse an invalid name")
			testkit.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
			testkit.Equal(t, string(got), "text", "AppendText must return b unchanged")
		})

		t.Run("returns b unchanged and ErrNote for an empty value", func(t *testing.T) {
			t.Parallel()
			got, err := note.Signature{Name: "a"}.AppendText([]byte("text"))
			testkit.ErrorIs(t, err, note.ErrNote, "AppendText must refuse a line without a signature")
			testkit.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
			testkit.Equal(t, string(got), "text", "AppendText must return b unchanged")
		})
	})
}

func BenchmarkSignature(b *testing.B) {
	s := peterSignature(b)

	b.Run("Valid", func(b *testing.B) {
		benchZeroAlloc(b, func() { sinkBool = s.Valid() })
	})

	b.Run("AppendText", func(b *testing.B) {
		buf := make([]byte, 0, len(peterLine))
		benchZeroAlloc(b, func() { sinkBytes, errSink = s.AppendText(buf[:0]) })
	})
}
