// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package der

// maxLengthOctets is the largest number of octets of a long-form length
// that the Reader accepts. Four octets encode lengths up to 4 GiB.
const maxLengthOctets = 4

// longForm marks the first length octet of a long-form length, whose low
// bits count the octets that follow.
const longForm = 0x80

// Reader reads the DER elements of a byte slice in sequence. Each method
// that reads an element returns slices of the input, and copies nothing.
//
// A method that reports a malformed element, or an element of another
// tag, leaves the Reader where it was, so the caller can read the element
// with another method or stop. The zero Reader has no elements.
//
// # Concurrency
//
// A Reader is a value over a slice that it does not change, so two
// goroutines read one input with a Reader each. One Reader is not safe for
// concurrent use.
//
// # Allocation contract
//
// No method allocates.
type Reader struct {
	b []byte
}

// NewReader returns a Reader of the elements of b.
func NewReader(b []byte) Reader {
	return Reader{b: b}
}

// Empty reports whether the Reader has read every octet of its input.
func (r *Reader) Empty() bool {
	return len(r.b) == 0
}

// Next reads the next element, and returns its tag, its whole encoding and
// its content. It reports false, and reads nothing, when the input is
// empty or the next element is not DER: a tag in more than one octet, an
// indefinite length, a length in more octets than its value needs or in
// more than four octets, or a length past the end of the input.
func (r *Reader) Next() (tag Tag, element, content []byte, ok bool) {
	tag, header, n, ok := head(r.b)
	if !ok {
		return 0, nil, nil, false
	}

	element, content = r.b[:header+n], r.b[header:header+n]
	r.b = r.b[header+n:]

	return tag, element, content, true
}

// Read reads the next element when it is DER and has tag, and returns its
// content. It reports false, and reads nothing, otherwise.
func (r *Reader) Read(tag Tag) (content []byte, ok bool) {
	_, content, ok = r.ReadElement(tag)

	return content, ok
}

// ReadElement reads the next element when it is DER and has tag, and
// returns its whole encoding and its content. It reports false, and reads
// nothing, otherwise.
func (r *Reader) ReadElement(tag Tag) (element, content []byte, ok bool) {
	if got, header, n, ok := head(r.b); ok && got == tag {
		element, content = r.b[:header+n], r.b[header:header+n]
		r.b = r.b[header+n:]

		return element, content, true
	}

	return nil, nil, false
}

// Optional reads the next element when it has tag, and returns its content
// with present true. When the input is empty or its next element has
// another tag, Optional reads nothing and returns present false and ok
// true. It reports ok false, and reads nothing, when the next element has
// tag and is not DER.
func (r *Reader) Optional(tag Tag) (content []byte, present, ok bool) {
	if len(r.b) == 0 || Tag(r.b[0]) != tag {
		return nil, false, true
	}

	content, ok = r.Read(tag)

	return content, ok, ok
}

// head returns the tag of the element at the start of b, the number of
// octets of its tag and length, and the length of its content. It reports
// false when the element is not DER or does not fit b.
func head(b []byte) (tag Tag, header, n int, ok bool) {
	ident, rest, ok := take(b, 2)
	if !ok || Tag(ident[0])&numberMask == numberMask {
		return 0, 0, 0, false
	}

	tag, n = Tag(ident[0]), int(ident[1])
	if n >= longForm {
		octets := n &^ longForm
		if octets == 0 || octets > maxLengthOctets {
			return 0, 0, 0, false
		}

		var length []byte
		if length, rest, ok = take(rest, octets); !ok || length[0] == 0 {
			return 0, 0, 0, false
		}

		n = 0
		for _, c := range length {
			n = n<<8 | int(c)
		}

		if n < longForm {
			return 0, 0, 0, false
		}
	}

	if _, _, ok = take(rest, n); !ok {
		return 0, 0, 0, false
	}

	return tag, len(b) - len(rest), n, true
}

// take splits the first n octets off s. It returns no slices, and reports
// false, when s has fewer than n octets.
func take(s []byte, n int) (first, rest []byte, ok bool) {
	if len(s) < n {
		return nil, nil, false
	}

	return s[:n], s[n:], true
}
