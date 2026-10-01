// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package note

//go:generate go tool kanon -type=Signature -canonical

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"slices"
)

const (
	// linePrefix starts every signature line: an em dash, U+2014, and a
	// space.
	linePrefix = "— "

	// lineSeparator separates the key name of a signature line from its
	// encoded signature. No key name contains it.
	lineSeparator = ' '

	// headText is the number of base64 characters that decodeSignature
	// decodes first: the key ID and the first two bytes of the signature,
	// or the whole value of a line of five or six bytes.
	headText = 8

	// headBytes is the number of bytes that headText characters decode to
	// at most.
	headBytes = 6

	// base64Quantum is the number of characters that encode one group of
	// bytes. The length of padded base64 is a multiple of it.
	base64Quantum = 4

	// maxPadding is the largest number of '=' that end padded base64.
	maxPadding = 2
)

// Signature is one signature line of a note:
//
//	— <name> <base64(key ID ‖ value)>
//
// The key name and the key ID name the key, and Value is the signature of
// that key over the note text.
//
// The zero Signature is not valid.
//
// # Encoding
//
// kanon generates the canonical codec of Signature, the form of a line in
// a kanon record, with the field numbers Name 1, Value 2 and ID 3. ID is
// written at its fixed width of four bytes, because a key ID is a hash and
// a varint of it is longer. The codec follows the rules of the codec of
// [Key].
type Signature struct {
	// Name is the key name.
	Name Name

	// Value is the signature: the decoded bytes after the key ID.
	Value []byte

	// ID is the key ID.
	ID uint32 `kanon:",fixed"`
}

// Valid reports whether s has a valid name and a value.
//
// # Allocation contract
//
// Zero-alloc.
func (s Signature) Valid() bool {
	return s.Name.Valid() && len(s.Value) > 0
}

// AppendText appends the signature line of s, newline included, to b and
// returns the extended slice. It implements [encoding.TextAppender].
//
// Returns b unchanged and [ErrKey], classified [errs.Invalid], for an
// invalid name, and [ErrNote], classified errs.Invalid, for an empty Value.
//
// # Allocation contract
//
// Zero-alloc when b has room for the line.
func (s Signature) AppendText(b []byte) ([]byte, error) {
	if !s.Name.Valid() {
		return b, fmt.Errorf("%w: invalid name %q", ErrKey, s.Name)
	}

	if len(s.Value) == 0 {
		return b, fmt.Errorf("%w: the line of %s has no signature", ErrNote, s.Name)
	}

	return s.appendLine(b), nil
}

// appendLine appends the signature line of s, a Valid Signature, to b.
func (s Signature) appendLine(b []byte) []byte {
	var id [keyIDSize]byte
	binary.BigEndian.PutUint32(id[:], s.ID)

	b = append(b, linePrefix...)
	b = append(b, s.Name...)
	b = append(b, lineSeparator)
	b = appendBase64(b, string(id[:]), s.Value)

	return append(b, '\n')
}

// textLen returns the length of the signature line of s: the prefix, the
// name, a space, the base64 of the key ID and the value, and a newline.
func (s Signature) textLen() int {
	return len(linePrefix) + len(s.Name) + 2 + base64.StdEncoding.EncodedLen(keyIDSize+len(s.Value))
}

// splitLine splits a signature line, with its newline, into the bytes of
// its key name and the base64 of its key ID and signature. It checks the
// prefix and the space after the name, and leaves the name and the base64
// to [parseSignature].
func splitLine(line []byte) (name, encoded []byte, err error) {
	rest, ok := bytes.CutPrefix(line[:len(line)-1], []byte(linePrefix))
	if !ok {
		return nil, nil, fmt.Errorf("%w: a signature line does not start with %q", ErrNote, linePrefix)
	}

	name, encoded, ok = bytes.Cut(rest, []byte{lineSeparator})
	if !ok {
		return nil, nil, fmt.Errorf("%w: a signature line without a space after the name", ErrNote)
	}

	return name, encoded, nil
}

// parseSignature returns the signature line of name and encoded, the
// base64 of its key ID and signature, with the signature decoded into the
// end of dst, and the extended dst. The Value of the line aliases dst, and
// its capacity ends with it, so that an append to it copies.
//
// Returns [ErrNote] for an invalid name, and for an encoding that is not
// the padded standard base64 of a key ID and at least one byte of
// signature.
func parseSignature(name Name, encoded, dst []byte) (Signature, []byte, error) {
	if !name.Valid() {
		return Signature{}, dst, fmt.Errorf("%w: a signature line with the invalid name %q", ErrNote, name)
	}

	start := len(dst)

	id, out, ok := decodeSignature(dst, encoded)
	if !ok {
		return Signature{}, dst, fmt.Errorf(
			"%w: the line of %s is not padded standard base64 of a key ID and a signature", ErrNote, name)
	}

	return Signature{Name: name, Value: out[start:len(out):len(out)], ID: id}, out, nil
}

// decodeSignature decodes encoded, the padded standard base64 of a key ID
// and a signature, appends the signature to dst, and returns the key ID
// and the extended dst. It reports false for an encoding that the strict
// decoder refuses, and for one of fewer than five bytes. encoded contains
// no carriage return and no newline, which the decoder skips.
//
// It decodes the first eight characters, the key ID and the first two
// bytes of the signature, on the stack, and the rest straight into dst.
// Those characters end in padding only when nothing follows them. It grows
// dst by the exact length of the rest, so that a dst with room for the
// signature decodes without an allocation.
func decodeSignature(dst, encoded []byte) (uint32, []byte, bool) {
	if len(encoded) < headText || len(encoded)%base64Quantum != 0 {
		return 0, dst, false
	}

	var head [headBytes]byte

	n, err := strictBase64.Decode(head[:], encoded[:headText])
	tail := encoded[headText:]

	if err != nil || n <= keyIDSize || n < headBytes && len(tail) > 0 {
		return 0, dst, false
	}

	dst = append(dst, head[keyIDSize:n]...)
	start := len(dst)
	size := base64.StdEncoding.DecodedLen(len(tail)) - min(len(tail)-len(bytes.TrimRight(tail, "=")), maxPadding)
	dst = slices.Grow(dst, size)

	m, err := strictBase64.Decode(dst[start:start+size], tail)
	if err != nil {
		return 0, dst[:start], false
	}

	return binary.BigEndian.Uint32(head[:]), dst[:start+m], true
}
