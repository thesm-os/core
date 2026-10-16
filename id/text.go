// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package id

import (
	"bytes"
	"encoding/hex"
)

// upperHexLetters are the letters that [encoding/hex] decodes and that the
// text of an [ID] does not contain.
const upperHexLetters = "ABCDEF"

// AppendText appends the text of i to dst and returns the extended slice.
// The text is the string that [ID.String] returns: "id:" followed by the
// active prefix in lowercase hexadecimal, and "id:" alone for [Zero]. The
// error is always nil. [encoding/json] writes an ID as a JSON string of this
// text, and the key of a map as the name of an object member. Implements
// [encoding.TextAppender].
//
// # Allocation contract
//
// Zero alloc when dst has room for the text: the 3 bytes of the prefix and
// two digits for each byte of i.
func (i ID) AppendText(dst []byte) ([]byte, error) {
	dst = append(dst, textPrefix...)

	return hex.AppendEncode(dst, i.bytes[:i.size]), nil
}

// MarshalText returns the text that [ID.AppendText] appends, in a new slice
// of the length of the text. The error is always nil. Implements
// [encoding.TextMarshaler].
//
// # Allocation contract
//
// One allocation for the returned slice, also for [Zero].
func (i ID) MarshalText() ([]byte, error) {
	return i.AppendText(make([]byte, 0, len(textPrefix)+2*int(i.size)))
}

// UnmarshalText sets i to the ID whose text is text, the text that
// [ID.AppendText] appends, and to [Zero] for "id:" alone. It decodes the
// digits after the prefix into an array on its stack, and then decodes the
// bytes as [ID.UnmarshalBinary] does. The decoded i equals, under ==, the ID
// of the same bytes, whatever i contained before. On an error i is
// unchanged. i does not alias text. Implements [encoding.TextUnmarshaler].
//
// Returns [ErrText] for a text that does not start with "id:". Returns
// [ErrSize] unless 0, 32, 40 or 64 digits follow the prefix. Returns
// [ErrText] for a digit other than 0 to 9 and the lowercase letters a to f.
// The uppercase letters are an error too, so each ID has exactly one text.
//
// # Allocation contract
//
// Zero alloc. It decodes into the receiver.
func (i *ID) UnmarshalText(text []byte) error {
	digits, ok := bytes.CutPrefix(text, []byte(textPrefix))
	if !ok {
		return ErrText
	}

	if len(digits)%2 != 0 {
		return ErrSize
	}

	size, ok := idSize(len(digits) / 2)
	if !ok {
		return ErrSize
	}

	var buf [MaxSize]byte
	if _, err := hex.Decode(buf[:size], digits); err != nil || bytes.ContainsAny(digits, upperHexLetters) {
		return ErrText
	}

	return i.UnmarshalBinary(buf[:size])
}
