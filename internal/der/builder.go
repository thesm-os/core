// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package der

import (
	"encoding/binary"
	"math/bits"
)

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

// NewBuilder returns a Builder that appends elements after the octets of
// dst, into the spare capacity of dst first. A nil dst starts an empty
// encoding.
func NewBuilder(dst []byte) Builder {
	return Builder{b: dst}
}

// Bytes returns the octets of dst followed by every element appended
// since. The slice shares its array with the Builder, so a later call can
// change it.
func (b *Builder) Bytes() []byte {
	return b.b
}

// Add appends the element of tag and content: tag, the DER length of
// content, and content.
func (b *Builder) Add(tag Tag, content []byte) {
	b.b = appendLength(append(b.b, byte(tag)), len(content))
	b.b = append(b.b, content...)
}

// AddElement appends element, a whole DER element that the caller
// encoded, as it is. AddElement does not check element.
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

// AddBoolean appends a BOOLEAN of v. DER allows the content 0xff for TRUE
// and 0x00 for FALSE alone.
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

// Close writes the length of the element whose placeholder is at at: the
// number of octets appended since Open. A length below 128 replaces the
// placeholder. A length of 128 or more takes the long form, so Close
// appends room for the octets that the long form adds, moves the content
// after them, and writes the length from the placeholder on.
func (b *Builder) Close(at int) {
	// The long form of an int takes at most one octet and eight more.
	var octets [1 + 8]byte

	n := len(b.b) - at - 1
	length := appendLength(octets[:0], n)
	b.b = append(b.b, length[1:]...)
	copy(b.b[at+len(length):], b.b[at+1:at+1+n])
	copy(b.b[at:], length)
}

// appendLength appends the DER length of n, which is not negative: one
// octet below 128, and otherwise the long form, the octet 0x80 plus the
// number of octets of n in base 256, then those octets in big-endian
// order.
func appendLength(dst []byte, n int) []byte {
	if n < longForm {
		return append(dst, byte(n)) //nolint:gosec // G115: n is below 128 and not negative
	}

	octets := (bits.Len64(uint64(n)) + 7) / 8 //nolint:gosec // G115: n is a length, so it is not negative

	dst = append(dst, longForm|byte(octets)) //nolint:gosec // G115: a length takes at most eight octets
	for i := range octets {
		dst = append(dst, byte(n>>(8*(octets-1-i)))) //nolint:gosec // G115: the conversion keeps the low eight bits
	}

	return dst
}
