// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package der_test

import (
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/internal/der"
)

// benchRuns is the number of calls over which a benchmark averages the
// allocations that it checks.
const benchRuns = 100

// Sinks keep the results of the benchmarks alive.
var (
	sinkBytes []byte
	sinkOK    bool
)

// long returns an OCTET STRING of n zero octets, whose length takes the
// long form for n of 128 or more.
func long(n int) []byte {
	b := der.NewBuilder(nil)
	b.Add(der.TagOctetString, make([]byte, n))

	return b.Bytes()
}

func TestReader(t *testing.T) {
	t.Parallel()

	t.Run("Next", func(t *testing.T) {
		t.Parallel()

		t.Run("reads an element without content", func(t *testing.T) {
			t.Parallel()
			r := der.NewReader([]byte{0x05, 0x00})

			tag, element, content, ok := r.Next()
			testkit.True(t, ok, "Next must read a NULL")
			testkit.Equal(t, tag, der.TagNull, "Next must return the tag")
			testkit.Equal(t, element, []byte{0x05, 0x00}, "Next must return the whole element")
			testkit.Len(t, content, 0, "a NULL must have no content")
			testkit.True(t, r.Empty(), "Next must consume the element")
		})

		t.Run("returns the tag, the element and the content of a short-form element", func(t *testing.T) {
			t.Parallel()
			r := der.NewReader([]byte{0x04, 0x02, 0xaa, 0xbb, 0x05, 0x00})

			tag, element, content, ok := r.Next()
			testkit.True(t, ok, "Next must read the element")
			testkit.Equal(t, tag, der.TagOctetString, "Next must return the tag")
			testkit.Equal(t, element, []byte{0x04, 0x02, 0xaa, 0xbb}, "Next must return the whole element")
			testkit.Equal(t, content, []byte{0xaa, 0xbb}, "Next must return the content")
			testkit.False(t, r.Empty(), "the second element must remain")
		})

		tests := []struct {
			name string
			give int
		}{
			{name: "reads a length of 128 in one long-form octet", give: 128},
			{name: "reads a length of 255 in one long-form octet", give: 255},
			{name: "reads a length of 256 in two long-form octets", give: 256},
			{name: "reads a length of 65,536 in three long-form octets", give: 65_536},
			{name: "reads a length of 16,777,216 in four long-form octets", give: 1 << 24},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				r := der.NewReader(long(tt.give))

				_, _, content, ok := r.Next()
				testkit.True(t, ok, "Next must read a long-form length")
				testkit.Len(t, content, tt.give, "Next must return the whole content")
				testkit.True(t, r.Empty(), "Next must consume the element")
			})
		}

		malformed := []struct {
			name string
			give []byte
		}{
			{name: "reports false for an empty input", give: nil},
			{name: "reports false for a tag without a length", give: []byte{0x04}},
			{name: "reports false for a tag in more than one octet", give: []byte{0x1f, 0x01, 0x00}},
			{name: "reports false for an indefinite length", give: []byte{0x30, 0x80, 0x00, 0x00}},
			{
				name: "reports false for an indefinite length before 128 octets",
				give: append([]byte{0x30, 0x80}, make([]byte, 128)...),
			},
			{name: "reports false for a length in five octets", give: []byte{0x04, 0x85, 0x01, 0x00, 0x00, 0x00, 0x00}},
			{name: "reports false for a long-form length past the input", give: []byte{0x04, 0x82, 0x01}},
			{
				name: "reports false for a long-form length before 127 octets of content",
				give: append([]byte{0x04, 0x81, 0x80}, make([]byte, 127)...),
			},
			{
				name: "reports false for a long-form length with a leading zero",
				give: append([]byte{0x04, 0x82, 0x00, 0x80}, make([]byte, 128)...),
			},
			{
				name: "reports false for a long-form length below 128",
				give: append([]byte{0x04, 0x81, 0x7f}, make([]byte, 128)...),
			},
			{name: "reports false for a content past the input", give: []byte{0x04, 0x02, 0xaa}},
		}
		for _, tt := range malformed {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				r := der.NewReader(tt.give)

				_, _, _, ok := r.Next()
				testkit.False(t, ok, "Next must refuse the element")
				testkit.Equal(t, r.Empty(), len(tt.give) == 0, "a refused element must stay unread")
			})
		}

		t.Run("reports false for a four-octet length of 4 GiB minus 1 past the input", func(t *testing.T) {
			t.Parallel()
			r := der.NewReader([]byte{0x04, 0x84, 0xff, 0xff, 0xff, 0xff})

			_, _, _, ok := r.Next()
			testkit.False(t, ok, "a length past the input must be refused, not wrapped")
		})
	})

	t.Run("Read", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the content of an element of the tag", func(t *testing.T) {
			t.Parallel()
			r := der.NewReader([]byte{0x02, 0x01, 0x05})

			content, ok := r.Read(der.TagInteger)
			testkit.True(t, ok, "Read must read the INTEGER")
			testkit.Equal(t, content, []byte{0x05}, "Read must return the content")
			testkit.True(t, r.Empty(), "Read must consume the element")
		})

		t.Run("reports false for an element of another tag and reads nothing", func(t *testing.T) {
			t.Parallel()
			r := der.NewReader([]byte{0x02, 0x01, 0x05})

			_, ok := r.Read(der.TagOctetString)
			testkit.False(t, ok, "Read must refuse an element of another tag")

			_, ok = r.Read(der.TagInteger)
			testkit.True(t, ok, "the refused element must stay readable")
		})

		t.Run("reports false for a malformed element", func(t *testing.T) {
			t.Parallel()
			r := der.NewReader([]byte{0x02, 0x02, 0x05})

			_, ok := r.Read(der.TagInteger)
			testkit.False(t, ok, "Read must refuse a content past the input")
		})
	})

	t.Run("ReadElement", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the whole element and its content", func(t *testing.T) {
			t.Parallel()
			r := der.NewReader([]byte{0x30, 0x03, 0x02, 0x01, 0x05, 0xff})

			element, content, ok := r.ReadElement(der.TagSequence)
			testkit.True(t, ok, "ReadElement must read the SEQUENCE")
			testkit.Equal(t, element, []byte{0x30, 0x03, 0x02, 0x01, 0x05}, "ReadElement must return the element")
			testkit.Equal(t, content, []byte{0x02, 0x01, 0x05}, "ReadElement must return the content")
		})
	})

	t.Run("Optional", func(t *testing.T) {
		t.Parallel()

		t.Run("reads an element of the tag", func(t *testing.T) {
			t.Parallel()
			r := der.NewReader([]byte{0x80, 0x01, 0x07})

			content, present, ok := r.Optional(der.Context(0))
			testkit.True(t, ok && present, "Optional must read the element")
			testkit.Equal(t, content, []byte{0x07}, "Optional must return the content")
			testkit.True(t, r.Empty(), "Optional must consume the element")
		})

		t.Run("reads nothing for an element of another tag", func(t *testing.T) {
			t.Parallel()
			r := der.NewReader([]byte{0x81, 0x01, 0x07})

			_, present, ok := r.Optional(der.Context(0))
			testkit.True(t, ok, "another tag must not be an error")
			testkit.False(t, present, "another tag must not be present")
			testkit.False(t, r.Empty(), "the element of another tag must stay unread")
		})

		t.Run("reads nothing from an empty input", func(t *testing.T) {
			t.Parallel()
			var r der.Reader

			_, present, ok := r.Optional(der.Context(0))
			testkit.True(t, ok, "an empty input must not be an error")
			testkit.False(t, present, "an empty input must have no element")
		})

		t.Run("reports false for a malformed element of the tag", func(t *testing.T) {
			t.Parallel()
			r := der.NewReader([]byte{0x80, 0x02, 0x07})

			_, present, ok := r.Optional(der.Context(0))
			testkit.False(t, ok, "a malformed element of the tag must be an error")
			testkit.False(t, present, "a malformed element must not be present")
		})
	})
}

func BenchmarkReader(b *testing.B) {
	element := []byte{0x30, 0x03, 0x02, 0x01, 0x05}

	b.Run("ReadElement", func(b *testing.B) {
		derAllocs(b, func() {
			r := der.NewReader(element)
			sinkBytes, _, sinkOK = r.ReadElement(der.TagSequence)
		})
		testkit.True(b, sinkOK, "the benchmark must measure a read")
	})
}

// derAllocs fails b when call allocates, averaged over benchRuns calls, and
// then reports the time and the allocations of call per iteration.
func derAllocs(b *testing.B, call func()) {
	b.Helper()

	if allocs := testing.AllocsPerRun(benchRuns, call); allocs != 0 {
		b.Fatalf("allocates %v times per call, want 0", allocs)
	}

	b.ReportAllocs()
	for b.Loop() {
		call()
	}
}
