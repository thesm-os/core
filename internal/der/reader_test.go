// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package der_test

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/internal/der"
)

// highTagNumber is the tag number 31 in the low bits of an identifier
// octet, which announces a tag number in further octets. The Reader
// refuses such a tag.
const highTagNumber der.Tag = 0x1f

// nextIsCanonical is the contract of FuzzReader and of the property that
// runs its body.
const nextIsCanonical = "Next must read nothing or return an element that Add encodes to the same octets"

// oneOctetTags generates every tag whose number fits its identifier octet.
var oneOctetTags = prop.Integer[der.Tag](0, 0xff).Filter(func(tag der.Tag) bool {
	return tag&highTagNumber != highTagNumber
})

func TestReader(t *testing.T) {
	t.Parallel()

	t.Run("Empty", func(t *testing.T) {
		t.Parallel()

		t.Run("reports true for the zero Reader", func(t *testing.T) {
			t.Parallel()
			var r der.Reader
			assert.True(t, r.Empty(), "the zero Reader must have no elements")
		})

		t.Run("reports false for a Reader with an element left", func(t *testing.T) {
			t.Parallel()
			r := der.NewReader([]byte{0x05, 0x00})
			assert.False(t, r.Empty(), "a Reader with an element left must not be empty")
		})
	})

	t.Run("Next", func(t *testing.T) {
		t.Parallel()

		t.Run("reads an element without content", func(t *testing.T) {
			t.Parallel()
			r := der.NewReader([]byte{0x05, 0x00})

			tag, element, content, ok := r.Next()
			assert.True(t, ok, "Next must read a NULL")
			expect.Equal(t, tag, der.TagNull, "Next must return the tag")
			expect.Equal(t, element, []byte{0x05, 0x00}, "Next must return the whole element")
			expect.Empty(t, content, "a NULL must have no content")
			expect.True(t, r.Empty(), "Next must consume the element")
		})

		t.Run("returns the parts of a short-form element", func(t *testing.T) {
			t.Parallel()
			r := der.NewReader([]byte{0x04, 0x02, 0xaa, 0xbb, 0x05, 0x00})

			tag, element, content, ok := r.Next()
			assert.True(t, ok, "Next must read the element")
			expect.Equal(t, tag, der.TagOctetString, "Next must return the tag")
			expect.Equal(t, element, []byte{0x04, 0x02, 0xaa, 0xbb}, "Next must return the whole element")
			expect.Equal(t, content, []byte{0xaa, 0xbb}, "Next must return the content")
			expect.Equal(t, r, der.NewReader([]byte{0x05, 0x00}), "the second element must remain")
		})

		t.Run("reads back an element that a Builder appends", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Next must return the tag and the content that Add appends", func(c *prop.Case) {
				tag := c.Draw(oneOctetTags, "tag")
				content := c.Draw(prop.Bytes(prop.MaxSize(300)), "content")

				b := der.NewBuilder(nil)
				b.Add(tag, content)
				r := der.NewReader(b.Bytes())

				gotTag, element, gotContent, ok := r.Next()
				assert.True(c, ok, "Next must read the element")
				assert.Equal(c, gotTag, tag, "Next must return the tag")
				assert.Equal(c, element, b.Bytes(), "Next must return the whole element")
				assert.Equal(c, gotContent, content, "Next must return the content", assert.EquateEmpty())
				assert.True(c, r.Empty(), "Next must consume the element")
			})
		})

		t.Run("returns only an element that Add encodes to the same octets", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, nextIsCanonical, readsCanonically)
		})

		tests := []struct {
			name string
			give int
		}{
			{name: "reads a length of 128 in one long-form octet", give: 128},
			{name: "reads a length of 255 in one long-form octet", give: 255},
			{name: "reads a length of 256 in two long-form octets", give: 256},
			{name: "reads a length of 64 KiB in three long-form octets", give: 1 << 16},
			{name: "reads a length of 16 MiB in four long-form octets", give: 1 << 24},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				b := der.NewBuilder(nil)
				b.Add(der.TagOctetString, make([]byte, tt.give))
				r := der.NewReader(b.Bytes())

				_, _, content, ok := r.Next()
				assert.True(t, ok, "Next must read a long-form length")
				expect.Length(t, content, tt.give, "Next must return the whole content")
				expect.True(t, r.Empty(), "Next must consume the element")
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
			{
				name: "reports false for a length in five octets",
				give: []byte{0x04, 0x85, 0x01, 0x00, 0x00, 0x00, 0x00},
			},
			{
				name: "reports false for a nine-octet length that wraps to 128",
				give: append(
					[]byte{0x04, 0x89, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x80},
					make([]byte, 128)...,
				),
			},
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
			{
				name: "reports false for a four-octet length of 4 GiB minus 1 past the input",
				give: []byte{0x04, 0x84, 0xff, 0xff, 0xff, 0xff},
			},
		}
		for _, tt := range malformed {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				r := der.NewReader(tt.give)

				var ok bool
				expect.Pure(t, func() der.Reader { return r }, func() { _, _, _, ok = r.Next() },
					"Next must read nothing")
				expect.False(t, ok, "Next must refuse the element")
			})
		}
	})

	t.Run("Read", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the content of an element of the tag", func(t *testing.T) {
			t.Parallel()
			r := der.NewReader([]byte{0x02, 0x01, 0x05})

			content, ok := r.Read(der.TagInteger)
			assert.True(t, ok, "Read must read the INTEGER")
			expect.Equal(t, content, []byte{0x05}, "Read must return the content")
			expect.True(t, r.Empty(), "Read must consume the element")
		})

		t.Run("reports false for an element of another tag", func(t *testing.T) {
			t.Parallel()
			r := der.NewReader([]byte{0x02, 0x01, 0x05})

			_, ok := r.Read(der.TagOctetString)
			assert.False(t, ok, "Read must refuse an element of another tag")
		})

		t.Run("reads nothing for an element of another tag", func(t *testing.T) {
			t.Parallel()
			r := der.NewReader([]byte{0x02, 0x01, 0x05})

			assert.Pure(t, func() der.Reader { return r }, func() { _, _ = r.Read(der.TagOctetString) },
				"the refused element must remain unread")
		})

		t.Run("reports false for a malformed element", func(t *testing.T) {
			t.Parallel()
			r := der.NewReader([]byte{0x02, 0x02, 0x05})

			_, ok := r.Read(der.TagInteger)
			assert.False(t, ok, "Read must refuse a content past the input")
		})
	})

	t.Run("ReadElement", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the parts of an element of the tag", func(t *testing.T) {
			t.Parallel()
			r := der.NewReader([]byte{0x30, 0x03, 0x02, 0x01, 0x05, 0xff})

			element, content, ok := r.ReadElement(der.TagSequence)
			assert.True(t, ok, "ReadElement must read the SEQUENCE")
			expect.Equal(t, element, []byte{0x30, 0x03, 0x02, 0x01, 0x05}, "ReadElement must return the element")
			expect.Equal(t, content, []byte{0x02, 0x01, 0x05}, "ReadElement must return the content")
			expect.Equal(t, r, der.NewReader([]byte{0xff}), "ReadElement must consume the element alone")
		})

		t.Run("reports false for a malformed element of tag 0", func(t *testing.T) {
			t.Parallel()
			r := der.NewReader([]byte{0x00})

			_, _, ok := r.ReadElement(0)
			assert.False(t, ok, "ReadElement must refuse a tag without a length")
		})
	})

	t.Run("Optional", func(t *testing.T) {
		t.Parallel()

		t.Run("returns an element of the tag", func(t *testing.T) {
			t.Parallel()
			r := der.NewReader([]byte{0x80, 0x01, 0x07})

			content, present, ok := r.Optional(der.Context(0))
			assert.True(t, ok, "Optional must read the element")
			expect.True(t, present, "the element must be present")
			expect.Equal(t, content, []byte{0x07}, "Optional must return the content")
			expect.True(t, r.Empty(), "Optional must consume the element")
		})

		t.Run("reads nothing for an element of another tag", func(t *testing.T) {
			t.Parallel()
			r := der.NewReader([]byte{0x81, 0x01, 0x07})

			var present, ok bool
			expect.Pure(t, func() der.Reader { return r }, func() { _, present, ok = r.Optional(der.Context(0)) },
				"the element of another tag must remain unread")
			expect.True(t, ok, "another tag must not be an error")
			expect.False(t, present, "another tag must not be present")
		})

		t.Run("reads nothing from an empty input", func(t *testing.T) {
			t.Parallel()
			var r der.Reader

			_, present, ok := r.Optional(der.Context(0))
			expect.True(t, ok, "an empty input must not be an error")
			expect.False(t, present, "an empty input must have no element")
		})

		t.Run("reports false for a malformed element of the tag", func(t *testing.T) {
			t.Parallel()
			r := der.NewReader([]byte{0x80, 0x02, 0x07})

			var present, ok bool
			expect.Pure(t, func() der.Reader { return r }, func() { _, present, ok = r.Optional(der.Context(0)) },
				"the malformed element must remain unread")
			expect.False(t, ok, "a malformed element of the tag must be an error")
			expect.False(t, present, "a malformed element must not be present")
		})
	})
}

// TestReaderAllocs checks the allocation contract of the methods that read
// an element. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
//
//nolint:paralleltest // see above
func TestReaderAllocs(t *testing.T) {
	sequence := []byte{0x30, 0x03, 0x02, 0x01, 0x05}
	implicit := []byte{0x80, 0x01, 0x07}

	t.Run("Next", func(t *testing.T) {
		var ok bool
		expect.MaxAllocs(t, func() {
			r := der.NewReader(sequence)
			_, _, _, ok = r.Next()
		}, 0, "Next must not allocate")
		assert.True(t, ok, "the test must measure a read")
	})

	t.Run("Read", func(t *testing.T) {
		var ok bool
		expect.MaxAllocs(t, func() {
			r := der.NewReader(sequence)
			_, ok = r.Read(der.TagSequence)
		}, 0, "Read must not allocate")
		assert.True(t, ok, "the test must measure a read")
	})

	t.Run("ReadElement", func(t *testing.T) {
		var ok bool
		expect.MaxAllocs(t, func() {
			r := der.NewReader(sequence)
			_, _, ok = r.ReadElement(der.TagSequence)
		}, 0, "ReadElement must not allocate")
		assert.True(t, ok, "the test must measure a read")
	})

	t.Run("Optional", func(t *testing.T) {
		var present bool
		expect.MaxAllocs(t, func() {
			r := der.NewReader(implicit)
			_, present, _ = r.Optional(der.Context(0))
		}, 0, "Optional must not allocate")
		assert.True(t, present, "the test must measure a read")
	})
}

// BenchmarkReader reports the cost of each method that reads an element,
// and fails when one allocates.
func BenchmarkReader(b *testing.B) {
	sequence := []byte{0x30, 0x03, 0x02, 0x01, 0x05}
	implicit := []byte{0x80, 0x01, 0x07}

	b.Run("Next", func(b *testing.B) {
		var ok bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			r := der.NewReader(sequence)
			_, _, _, ok = r.Next()
		}

		assert.True(b, ok, "the benchmark must measure a read")
	})

	b.Run("Read", func(b *testing.B) {
		var ok bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			r := der.NewReader(sequence)
			_, ok = r.Read(der.TagSequence)
		}

		assert.True(b, ok, "the benchmark must measure a read")
	})

	b.Run("ReadElement", func(b *testing.B) {
		var ok bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			r := der.NewReader(sequence)
			_, _, ok = r.ReadElement(der.TagSequence)
		}

		assert.True(b, ok, "the benchmark must measure a read")
	})

	b.Run("Optional", func(b *testing.B) {
		var present bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			r := der.NewReader(implicit)
			_, present, _ = r.Optional(der.Context(0))
		}

		assert.True(b, present, "the benchmark must measure a read")
	})
}

// FuzzReader checks [nextIsCanonical] on the inputs of the fuzzer. Each
// seed is the choices of one case: a two-octet little-endian length, then
// the octets of the input.
func FuzzReader(f *testing.F) {
	for _, input := range [][]byte{
		{0x05, 0x00},
		{0x04, 0x02, 0xaa, 0xbb, 0x05, 0x00},
		{0x30, 0x03, 0x02, 0x01, 0x05},
		{0x30, 0x80, 0x00, 0x00},
		{0x04, 0x81, 0x7f},
		{0x04, 0x84, 0xff, 0xff, 0xff, 0xff},
	} {
		f.Add(append([]byte{byte(len(input)), 0}, input...))
	}

	prop.Fuzz(f, nextIsCanonical, readsCanonically)
}

// readsCanonically checks [nextIsCanonical] on an input that the case
// draws. A refused input leaves the Reader as NewReader returned it. An
// element that Next returns is the start of the input, Add encodes its tag
// and content to its octets, and the Reader continues after it.
func readsCanonically(c *prop.Case) {
	input := c.Draw(prop.Bytes(), "input")
	r := der.NewReader(input)

	tag, element, content, ok := r.Next()
	if !ok {
		assert.Equal(c, r, der.NewReader(input), "a refused element must remain unread")

		return
	}

	b := der.NewBuilder(nil)
	b.Add(tag, content)
	assert.Equal(c, b.Bytes(), element, "Add must encode the tag and the content to the element")
	assert.Equal(c, element, input[:len(element)], "Next must return the element at the start of the input")
	assert.Equal(c, r, der.NewReader(input[len(element):]), "Next must consume the element alone")
}
