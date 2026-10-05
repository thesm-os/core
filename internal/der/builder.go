// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package der

import (
	"encoding/binary"
	"math/bits"
)

// zeros is the room that Close appends before it moves the content of an
// element whose length needs the long form: at most four octets.
var zeros [maxLengthOctets]byte

// Builder appends DER elements to a byte slice. Add appends an element
// whose content the caller has, and Open and Close bracket a constructed
// element whose content the calls in between append. Close writes the
// length once it is known, and moves the content when the length needs
// more than one octet, so the caller computes no length.
//
// The caller closes every element that it opens, innermost first, and
// passes Close the position that Open returned. A Builder does not check
// the nesting, because the formats of this module open a fixed structure.
//
// # Concurrency
//
// Not safe for concurrent use.
//
// # Allocation contract
//
// No method allocates while the slice has room. A slice without room
// grows through append.
type Builder struct {
	b []byte
}

// NewBuilder returns a Builder that appends to dst.
func NewBuilder(dst []byte) Builder {
	return Builder{b: dst}
}

// Bytes returns the slice that the Builder appended to.
func (b *Builder) Bytes() []byte {
	return b.b
}

// Add appends the element of tag and content.
func (b *Builder) Add(tag Tag, content []byte) {
	b.b = appendLength(append(b.b, byte(tag)), len(content))
	b.b = append(b.b, content...)
}

// AddElement appends element, a whole DER element, as it is.
func (b *Builder) AddElement(element []byte) {
	b.b = append(b.b, element...)
}

// AddUint64 appends an INTEGER of v: its octets without leading zeros,
// after a zero octet when the first of them has the sign bit set, so the
// INTEGER is not negative. Zero is the one octet 0x00.
func (b *Builder) AddUint64(v uint64) {
	var octets [9]byte
	binary.BigEndian.PutUint64(octets[1:], v)

	i := 1
	for i < len(octets)-1 && octets[i] == 0 {
		i++
	}

	if octets[i]&signBit != 0 {
		i--
	}

	b.Add(TagInteger, octets[i:])
}

// AddBoolean appends a BOOLEAN of v.
func (b *Builder) AddBoolean(v bool) {
	content := byte(falseOctet)
	if v {
		content = trueOctet
	}

	b.b = append(b.b, byte(TagBoolean), 1, content)
}

// Open appends the tag of a constructed element and a placeholder for its
// length, and returns the position of the placeholder, which the caller
// passes to Close.
func (b *Builder) Open(tag Tag) int {
	b.b = append(b.b, byte(tag), 0)

	return len(b.b) - 1
}

// Close writes the length of the element whose placeholder is at at, the
// octets appended since Open. A length of 128 or more takes the long form:
// Close inserts its octets after the placeholder and moves the content
// after them.
func (b *Builder) Close(at int) {
	n := len(b.b) - at - 1
	if n < longForm {
		b.b[at] = octet(n)

		return
	}

	octets := lengthOctets(n)
	b.b = append(b.b, zeros[:octets]...)
	copy(b.b[at+1+octets:], b.b[at+1:at+1+n])

	b.b[at] = longForm | octet(octets)
	for i := octets; i > 0; i-- {
		b.b[at+i] = octet(n)
		n >>= 8
	}
}

// appendLength appends the DER length of n: one octet below 128, and the
// long form otherwise.
func appendLength(dst []byte, n int) []byte {
	if n < longForm {
		return append(dst, octet(n))
	}

	octets := lengthOctets(n)

	dst = append(dst, longForm|octet(octets))
	for i := range octets {
		dst = append(dst, octet(n>>(8*(octets-1-i))))
	}

	return dst
}

// lengthOctets returns the number of octets of n in base 256, for a length
// of 128 or more: 1 to 4.
func lengthOctets(n int) int {
	//nolint:gosec // G115: n is a length, so it is not negative
	return (bits.Len64(uint64(n)) + 7) / 8
}

// octet returns the low eight bits of n: a length below 128, a count of
// length octets, or one octet of a long-form length.
func octet(n int) byte {
	//nolint:gosec // G115: the conversion keeps the low eight bits by design
	return byte(n)
}
