// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package note

//go:generate go tool kanon -type=Type -validate=valid

import (
	"encoding/hex"
	"fmt"
)

const (
	// typeOther is the type byte that signed-note reserves for the types
	// it assigns no byte.
	typeOther = 0xff

	// maxIdentifier is the length of the longest identifier of a type
	// without an assigned byte, the largest value of its length byte.
	maxIdentifier = 255

	// hexPrefix starts the diagnostic form of a type of one byte and of a
	// value that is not a type.
	hexPrefix = "0x"

	// byteName is the length of the diagnostic form of a type of one
	// byte: "0x" and two hexadecimal digits.
	byteName = 4

	// byteNamesSize is the length of byteNames: byteName characters for
	// each of the 256 bytes.
	byteNamesSize = 1024
)

// byteNames contains the diagnostic form of every type of one byte, from
// "0x00" to "0xff", byteName characters each, so that [Type.String]
// returns a substring of it.
var byteNames = func() string {
	names := make([]byte, 0, byteNamesSize)
	for b := range 256 {
		names = append(names, hexPrefix...)
		names = hex.AppendEncode(names, []byte{byte(b)})
	}

	return string(names)
}()

// Type is a signed-note signature type: the bytes that a key ID and a
// verifier key encode between the key name and the public key. An
// assigned type is one byte other than 0xff. A type without an assigned
// byte is 0xff, a length byte n from 1 to 255, and an identifier of n
// bytes, which [NewType] builds.
//
// signed-note does not define where the identifier after 0xff ends, and a
// parser of a verifier key needs that end, because the type and the public
// key are one base64 string. The length byte encodes the end, as
// tlog-cosignature encodes the name of a cosigner. The encoding does not
// change a key ID, which hashes the type and the public key as one string.
// A verifier key whose 0xff type follows another convention parses into
// another Type, which the caller's [Resolver] does not resolve.
//
// kanon encodes a Type as a string. The kanon codec of a struct with a Type
// field returns an error that wraps [ErrType] for a Type that is not Valid,
// on encode and on decode.
type Type string

// TypeEd25519 is type 0x01, an Ed25519 signature over the note text. Its
// [Resolver] entry is [Text] of the [sign.Resolver] entry of Ed25519.
const TypeEd25519 Type = "\x01"

// NewType returns the type 0xff ‖ len(identifier) ‖ identifier, for a
// signature type that signed-note assigns no byte. Use a schema-less URL
// under a domain that the owner of the type controls as the identifier,
// such as "example.com/ml-dsa-87", so that two owners do not choose one
// identifier.
//
// Returns [ErrType], classified [errs.Invalid], for an identifier that is
// empty or longer than 255 bytes.
//
// # Allocation contract
//
// Allocates the returned Type. A const declaration of the bytes that
// NewType returns, as a string literal of type Type, allocates nothing.
func NewType(identifier string) (Type, error) {
	if identifier == "" || len(identifier) > maxIdentifier {
		return "", fmt.Errorf("%w: an identifier of %d bytes, want 1 to %d",
			ErrType, len(identifier), maxIdentifier)
	}

	prefix := []byte{typeOther, byte(len(identifier))} //nolint:gosec // the length is at most 255, checked above

	return Type(string(prefix) + identifier), nil
}

// Valid reports whether t is one byte other than 0xff, or 0xff followed
// by a length byte n of at least 1 and by n more bytes.
//
// # Allocation contract
//
// Zero-alloc.
func (t Type) Valid() bool {
	if len(t) == 1 {
		return t[0] != typeOther
	}

	return len(t) > 2 && t[0] == typeOther && int(t[1]) == len(t)-2
}

// valid returns nil for a Valid type, and an error that wraps [ErrType]
// for any other. The generated ValidateKanon calls it.
func (t Type) valid() error {
	if !t.Valid() {
		return fmt.Errorf("%w: %s", ErrType, t)
	}

	return nil
}

// String returns the type for diagnostics: "0x01" for the type of the byte
// 0x01, the identifier for a valid type without an assigned byte, such as
// "example.com/ml-dsa-87", and "0x" followed by the bytes of t in
// hexadecimal for any other value.
//
// # Allocation contract
//
// Zero-alloc for every Valid type: the form of a type of one byte is a
// substring of a table, and the identifier a substring of t. Allocates the
// form of a value that is not a type.
func (t Type) String() string {
	if len(t) == 1 {
		start := int(t[0]) * byteName

		return byteNames[start : start+byteName]
	}

	if t.Valid() {
		return string(t[2:])
	}

	var buf [stringBuffer]byte

	return string(hex.AppendEncode(append(buf[:0], hexPrefix...), []byte(t)))
}
