// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package der

// The octets of a BOOLEAN's content in DER.
const (
	// falseOctet is the content of FALSE.
	falseOctet = 0x00

	// trueOctet is the content of TRUE.
	trueOctet = 0xff
)

// signBit is the most significant bit of an octet: the sign of the first
// octet of an INTEGER, and the continuation bit of a base-128 octet of an
// OBJECT IDENTIFIER.
const signBit = 0x80

// Integer reports whether content is the content of an INTEGER in DER: at
// least one octet, and no first octet that the sign of the second makes
// redundant, as 0x00 before an octet below 0x80 and 0xff before an octet of
// 0x80 or more are.
func Integer(content []byte) bool {
	if len(content) == 0 {
		return false
	}

	if len(content) == 1 {
		return true
	}

	redundantZero := content[0] == 0x00 && content[1]&signBit == 0
	redundantOne := content[0] == 0xff && content[1]&signBit != 0

	return !redundantZero && !redundantOne
}

// Uint64 returns the value of content, the content of an INTEGER in DER
// that is not negative and fits a uint64. It reports false for any other
// content.
func Uint64(content []byte) (uint64, bool) {
	if !Integer(content) || content[0]&signBit != 0 {
		return 0, false
	}

	if content[0] == 0x00 {
		content = content[1:]
	}

	if len(content) > 8 {
		return 0, false
	}

	var v uint64
	for _, c := range content {
		v = v<<8 | uint64(c)
	}

	return v, true
}

// Boolean returns the value of content, the content of a BOOLEAN in DER:
// 0xff for true and 0x00 for false. It reports false for any other
// content.
func Boolean(content []byte) (value, ok bool) {
	if len(content) != 1 {
		return false, false
	}

	switch content[0] {
	case trueOctet:
		return true, true
	case falseOctet:
		return false, true
	default:
		return false, false
	}
}

// ObjectIdentifier reports whether content is the content of an OBJECT
// IDENTIFIER in DER: at least one subidentifier, each in base 128 without
// a leading octet of 0x80, and a last octet without the continuation bit.
// Two identifiers are equal exactly when their contents are equal.
func ObjectIdentifier(content []byte) bool {
	if len(content) == 0 || content[len(content)-1]&signBit != 0 {
		return false
	}

	first := true
	for _, c := range content {
		if first && c == signBit {
			return false
		}

		first = c&signBit == 0
	}

	return true
}

// BitString returns the octets of the bits of content, the content of a
// BIT STRING in DER, and the number of unused bits in the last octet. DER
// requires fewer than eight unused bits, none for a BIT STRING without
// octets, and unused bits of zero. It reports false for any other content.
func BitString(content []byte) (bits []byte, unused int, ok bool) {
	if len(content) == 0 || content[0] > 7 {
		return nil, 0, false
	}

	unused, bits = int(content[0]), content[1:]
	if len(bits) == 0 {
		return nil, 0, unused == 0
	}

	if bits[len(bits)-1]&(1<<unused-1) != 0 {
		return nil, 0, false
	}

	return bits, unused, true
}

// Bit reports whether bit n of the octets of a BIT STRING is set. Bit 0 is
// the most significant bit of the first octet, as in a named bit list. A
// bit past the end is not set.
func Bit(bits []byte, n int) bool {
	if n < 0 || n >= 8*len(bits) {
		return false
	}

	return bits[n/8]&(signBit>>(n%8)) != 0
}
