// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package note

//go:generate go tool kanon -type=Name -validate=valid

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Name is a key name: non-empty valid UTF-8 without '+', without a
// character below U+0020, and without a character of the Unicode
// White_Space property, which [unicode.IsSpace] reports.
//
// The rule is golang.org/x/mod's rule for key names, with the characters
// below U+0020 refused as well. unicode.IsSpace reports false for U+0000
// to U+0008 and U+000E to U+001F, and no note can contain them, so a Name
// that [Name.Valid] accepts always forms a signature line that [Parse]
// accepts. A '+' separates the parts of a verifier key, and a space
// separates the parts of a signature line.
//
// kanon encodes a Name as a string. The kanon codec of a struct with a Name
// field returns an error that wraps [ErrKey] for a Name that is not Valid,
// on encode and on decode.
type Name string

// Valid reports whether n is a key name.
//
// # Allocation contract
//
// Zero-alloc.
func (n Name) Valid() bool {
	return n != "" && utf8.ValidString(string(n)) && !strings.ContainsFunc(string(n), notNameRune)
}

// valid returns nil for a Valid name, and an error that wraps [ErrKey] for
// any other. The generated ValidateKanon calls it.
func (n Name) valid() error {
	if !n.Valid() {
		return fmt.Errorf("%w: invalid name %q", ErrKey, n)
	}

	return nil
}

// validName reports whether b, bytes of valid UTF-8, are a key name, as
// [Name.Valid] reports it for Name(b), without the conversion, which
// allocates. It does not check the encoding, which [TextOf] checks for the
// whole note before it checks a name.
func validName(b []byte) bool {
	return len(b) > 0 && !bytes.ContainsFunc(b, notNameRune)
}

// notNameRune reports whether a key name excludes r: a character below
// U+0020, '+', or a character of the Unicode White_Space property.
func notNameRune(r rune) bool {
	return r <= lastControl || r == keySeparator || unicode.IsSpace(r)
}
