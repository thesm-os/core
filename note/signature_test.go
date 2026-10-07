// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package note_test

import (
	"encoding/base64"
	"encoding/binary"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/note"
)

// peterLine is the signature line of PeterNeumann over peterText in
// golang.org/x/mod's tests.
const peterLine = "— PeterNeumann " + peterValue + "\n"

func TestSignature(t *testing.T) {
	t.Parallel()

	t.Run("Valid", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give note.Signature
			want bool
		}{
			{name: "reports true for the line of golang.org/x/mod's tests", give: peterSignature(t), want: true},
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
				assert.Equal(t, tt.give.Valid(), tt.want, "Valid must report whether the line is complete")
			})
		}
	})

	t.Run("AppendText", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the line that golang.org/x/mod's tests record", func(t *testing.T) {
			t.Parallel()
			got, err := peterSignature(t).AppendText([]byte("text\n\n"))
			assert.NoError(t, err, "AppendText must accept a valid line")
			assert.Equal(t, string(got), "text\n\n"+peterLine, "AppendText must append the signature line")
		})

		t.Run("returns ErrKey with b unchanged for an invalid name", func(t *testing.T) {
			t.Parallel()
			got, err := note.Signature{Name: "a b", Value: []byte{1}}.AppendText([]byte("text"))
			expect.ErrorIs(t, err, note.ErrKey, "AppendText must refuse an invalid name")
			expect.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
			expect.Equal(t, string(got), "text", "AppendText must return b unchanged")
		})

		t.Run("returns ErrNote with b unchanged for an empty value", func(t *testing.T) {
			t.Parallel()
			got, err := note.Signature{Name: "a"}.AppendText([]byte("text"))
			expect.ErrorIs(t, err, note.ErrNote, "AppendText must refuse a line without a signature")
			expect.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
			expect.Equal(t, string(got), "text", "AppendText must return b unchanged")
		})
	})
}

// TestSignatureAllocs checks the allocation contract of each method.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
//
//nolint:paralleltest // see above
func TestSignatureAllocs(t *testing.T) {
	s := peterSignature(t)

	t.Run("Valid", func(t *testing.T) {
		var got bool
		expect.MaxAllocs(t, func() { got = s.Valid() }, 0, "Valid must not allocate")
		assert.True(t, got, "the test must measure a valid line")
	})

	t.Run("AppendText", func(t *testing.T) {
		buf := make([]byte, 0, len(peterLine))

		var got []byte
		expect.MaxAllocs(t, func() { got, _ = s.AppendText(buf[:0]) }, 0,
			"AppendText must not allocate into a buffer with room")
		assert.Equal(t, string(got), peterLine, "the test must measure the line of golang.org/x/mod's tests")
	})
}

// BenchmarkSignature reports the cost of each method, and fails above the
// allocations that their contracts state.
func BenchmarkSignature(b *testing.B) {
	s := peterSignature(b)

	b.Run("Valid", func(b *testing.B) {
		var got bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = s.Valid()
		}

		assert.True(b, got, "the benchmark must measure a valid line")
	})

	b.Run("AppendText", func(b *testing.B) {
		buf := make([]byte, 0, len(peterLine))

		var got []byte

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, _ = s.AppendText(buf[:0])
		}

		assert.Equal(b, string(got), peterLine, "the benchmark must measure the line of golang.org/x/mod's tests")
	})
}

// peterSignature returns the Signature of peterLine.
func peterSignature(tb testing.TB) note.Signature {
	tb.Helper()

	raw, err := base64.StdEncoding.DecodeString(peterValue)
	assert.NoError(tb, err, "the fixture must decode")

	return note.Signature{Name: "PeterNeumann", Value: raw[keyIDBytes:], ID: binary.BigEndian.Uint32(raw)}
}
