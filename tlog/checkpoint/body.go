// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package checkpoint

//go:generate go tool kanon -type=Origin,Extension,Body -validate=valid -canonical

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"slices"
	"strconv"
	"unicode/utf8"

	"go.thesmos.sh/core/crypto"
)

const (
	// lastControl is the last character below U+0020. A body contains no
	// character below U+0020 other than newline.
	lastControl = 0x1f

	// newline ends every line of a body.
	newline = '\n'

	// bodyLines is the number of lines that every body has: the origin,
	// the size and the root.
	bodyLines = 3

	// maxRootText is the length of the longest root line: the padded
	// standard base64 of a 64-byte root.
	maxRootText = 88

	// maxRootBytes is the number of bytes that a root line of maxRootText
	// characters decodes to at most.
	maxRootBytes = 66

	// maxSizeText is the length of the decimal of the largest uint64.
	maxSizeText = 20
)

// strictBase64 decodes padded standard base64, and refuses an encoding
// whose spare bits are not zero.
var strictBase64 = base64.StdEncoding.Strict()

// Origin is the origin line of a checkpoint: non-empty valid UTF-8
// without a character below U+0020. It identifies the log that signs the
// checkpoint. tlog-checkpoint recommends a schema-less URL, such as
// example.com/log, and permits spaces and '+'.
//
// Every valid key name of [note.Name] is a valid Origin, so
// Origin(k.Name) converts the key name of a log to the origin of its
// checkpoints. An origin with a space or a '+' is the key name of no log.
//
// kanon encodes an Origin as a string. The kanon codec of a struct with an
// Origin field returns an error that wraps [ErrBody] for an Origin that is
// not Valid, on encode and on decode.
type Origin string

// Valid reports whether o is an origin line.
//
// # Allocation contract
//
// Zero-alloc.
func (o Origin) Valid() bool {
	return validLine(string(o))
}

// valid returns nil for a Valid origin, and an error that wraps [ErrBody]
// for any other. The generated ValidateKanon calls it.
func (o Origin) valid() error {
	if !o.Valid() {
		return fmt.Errorf("%w: the origin %q is not a line", ErrBody, o)
	}

	return nil
}

// Extension is an extension line of a checkpoint, with the rule of an
// [Origin]: non-empty valid UTF-8 without a character below U+0020.
// tlog-checkpoint makes extension lines optional and opaque, and
// recommends against them, because log monitors cannot audit them. kanon
// encodes an Extension as an Origin.
type Extension string

// Valid reports whether e is an extension line.
//
// # Allocation contract
//
// Zero-alloc.
func (e Extension) Valid() bool {
	return validLine(string(e))
}

// valid returns nil for a Valid extension line, and an error that wraps
// [ErrBody] for any other. The generated ValidateKanon calls it.
func (e Extension) valid() error {
	if !e.Valid() {
		return fmt.Errorf("%w: the extension %q is not a line", ErrBody, e)
	}

	return nil
}

// Body is the text of a C2SP tlog-checkpoint: the origin, the tree size,
// the root hash and the extension lines, one per line.
//
// [ParseBody] returns a Valid Body, and [Body.AppendText] writes it back
// byte for byte. The zero Body is not valid.
//
// # Encoding
//
// ParseBody and Body.AppendText read and write the text form that
// tlog-checkpoint specifies and that signatures cover. kanon generates the
// canonical codec of Body, the form of a Body in a kanon record, with the
// field numbers Origin 1, Extensions 2, Size 3 and Root 4. Its decode
// accepts only the encoding that its encode writes. The methods of the
// codec have pointer receivers.
type Body struct {
	// Origin identifies the log.
	Origin Origin

	// Extensions are the extension lines, in the order of the text, and
	// empty for a body without them: nil from ParseBody, and of length 0
	// with its capacity from an UnmarshalText that reuses a Body with
	// extension lines.
	Extensions []Extension

	// Size is the number of leaves of the tree.
	Size uint64

	// Root is the root hash of the tree: 32 bytes for a tree over
	// SHA-256, as RFC 6962 and C2SP specify, and 48 or 64 bytes for a
	// tree that tlog builds over another hasher. It is the Digest that
	// [go.thesmos.sh/core/tlog.Builder.Root] returns.
	Root crypto.Digest
}

// ParseBody parses the text of a checkpoint. The origin and the extension
// lines are substrings of one string of text, so the Body does not alias
// text.
//
// Returns [ErrBody], classified [errs.Invalid], for a text with fewer than
// three lines, a text that does not end in a newline, that is not valid
// UTF-8, or that contains a character below U+0020 other than newline, an
// empty origin or extension line, a size that is not the decimal of a
// uint64 without leading zeros, and a root that is not the padded
// standard base64 of 32, 48 or 64 bytes.
//
// # Allocation contract
//
// Allocates the string of text once, the slice of extension lines once
// for a body that has them, and the error of a text that it refuses.
// [Body.UnmarshalText] into a reused Body is the path without these
// allocations.
func ParseBody(text []byte) (Body, error) {
	var b Body
	if err := b.UnmarshalText(text); err != nil {
		return Body{}, err
	}

	return b, nil
}

// Valid reports whether b has a valid origin, a root, and valid extension
// lines. Every Size is valid.
//
// # Allocation contract
//
// Zero-alloc.
func (b Body) Valid() bool {
	if !b.Origin.Valid() || b.Root.IsZero() {
		return false
	}

	for _, e := range b.Extensions {
		if !e.Valid() {
			return false
		}
	}

	return true
}

// AppendText appends the text of b to dst and returns the extended slice:
// the origin, the size in decimal, the root in padded standard base64,
// and each extension line, each followed by a newline. It implements
// [encoding.TextAppender].
//
// Returns dst unchanged and [ErrBody], classified [errs.Invalid], for a
// Body that is not Valid.
//
// # Allocation contract
//
// Zero-alloc when dst has room for the text.
func (b Body) AppendText(dst []byte) ([]byte, error) {
	if !b.Valid() {
		return dst, fmt.Errorf("%w: a Body needs a valid origin, a root and valid extension lines", ErrBody)
	}

	dst = append(dst, b.Origin...)
	dst = append(dst, newline)
	dst = strconv.AppendUint(dst, b.Size, 10)
	dst = append(dst, newline)
	dst = base64.StdEncoding.AppendEncode(dst, b.Root.Bytes())
	dst = append(dst, newline)

	for _, e := range b.Extensions {
		dst = append(dst, e...)
		dst = append(dst, newline)
	}

	return dst, nil
}

// MarshalText returns the text of b, as [Body.AppendText] writes it. It
// implements [encoding.TextMarshaler].
//
// Returns nil and [ErrBody], classified [errs.Invalid], for a Body that is
// not Valid.
//
// # Allocation contract
//
// Allocates the returned text once, at its exact length.
func (b Body) MarshalText() ([]byte, error) {
	text, err := b.AppendText(make([]byte, 0, b.textLen()))
	if err != nil {
		return nil, err
	}

	return text, nil
}

// UnmarshalText sets b to the body of text, as [ParseBody] parses it, and
// reuses the memory of b: its origin and each extension line that equals
// the line of text, and the capacity of its extension lines. The lines
// that differ are substrings of one string of text. It implements
// [encoding.TextUnmarshaler]. On an error b is unchanged.
//
// Returns the errors of ParseBody.
//
// # Allocation contract
//
// Zero-alloc when the origin and the extension lines of text equal those
// of b, as for the next checkpoint of the log of b. Otherwise allocates
// the string of text once, and the slice of extension lines when b has no
// room for them.
func (b *Body) UnmarshalText(text []byte) error {
	l, err := splitBody(text)
	if err != nil {
		return err
	}

	b.assign(l, text)

	return nil
}

// assign sets b to the body of l, the lines of text, and reuses the memory
// of b as [Body.UnmarshalText] documents. It makes the string of text at
// the first line that differs from the line of b, and takes each line
// that differs from it at the offset of the line in text.
func (b *Body) assign(l lines, text []byte) {
	var str string

	if string(l.origin) != string(b.Origin) {
		str = string(text)
		b.Origin = Origin(str[:len(l.origin)])
	}

	b.Size, b.Root = l.size, l.root
	old := b.Extensions
	exts := slices.Grow(old[:0], bytes.Count(l.extensions, []byte{newline}))
	off := len(text) - len(l.extensions)

	for line := range bytes.Lines(l.extensions) {
		e := line[:len(line)-1]

		if i := len(exts); i < len(old) && string(e) == string(old[i]) {
			exts = append(exts, old[i])
		} else {
			if str == "" {
				str = string(text)
			}

			exts = append(exts, Extension(str[off:off+len(e)]))
		}

		off += len(line)
	}

	b.Extensions = exts
}

// textLen returns the length of the text of b: each line with its
// newline.
func (b Body) textLen() int {
	var size [maxSizeText]byte

	n := len(b.Origin) + len(strconv.AppendUint(size[:0], b.Size, 10)) +
		base64.StdEncoding.EncodedLen(b.Root.Size()) + bodyLines

	for _, e := range b.Extensions {
		n += len(e) + 1
	}

	return n
}

// lines is a body split into its lines, without a copy of the text.
type lines struct {
	// origin is the origin line, without its newline.
	origin []byte

	// extensions are the extension lines, each with its newline, and
	// empty for a body without them.
	extensions []byte

	// size is the tree size.
	size uint64

	// root is the root hash.
	root crypto.Digest
}

// splitBody splits text into the lines of a body, and checks each line
// as [ParseBody] documents. It returns the error of ParseBody.
func splitBody(text []byte) (lines, error) {
	if bytes.Count(text, []byte{newline}) < bodyLines || text[len(text)-1] != newline {
		return lines{}, fmt.Errorf("%w: a body has three lines or more, and ends in a newline", ErrBody)
	}

	if !validText(text) {
		return lines{}, fmt.Errorf("%w: not valid UTF-8, or a character below U+0020 other than newline", ErrBody)
	}

	origin, rest, _ := bytes.Cut(text, []byte{newline})
	sizeLine, rest, _ := bytes.Cut(rest, []byte{newline})
	rootLine, rest, _ := bytes.Cut(rest, []byte{newline})

	if len(origin) == 0 {
		return lines{}, fmt.Errorf("%w: an empty origin line", ErrBody)
	}

	size, ok := parseSize(sizeLine)
	if !ok {
		return lines{}, fmt.Errorf("%w: the size %q is not the decimal of a uint64 without leading zeros",
			ErrBody, sizeLine)
	}

	root, ok := parseRoot(rootLine)
	if !ok {
		return lines{}, fmt.Errorf("%w: the root %q is not the padded standard base64 of 32, 48 or 64 bytes",
			ErrBody, rootLine)
	}

	for line := range bytes.Lines(rest) {
		if len(line) == 1 {
			return lines{}, fmt.Errorf("%w: an empty extension line", ErrBody)
		}
	}

	return lines{origin: origin, extensions: rest, size: size, root: root}, nil
}

// parseSize returns the size whose decimal is line, and reports false for
// a line that is not the decimal of a uint64 without leading zeros.
// strconv.ParseUint accepts only digits in base 10.
func parseSize(line []byte) (uint64, bool) {
	if len(line) > 1 && line[0] == '0' {
		return 0, false
	}

	size, err := strconv.ParseUint(string(line), 10, 64)

	return size, err == nil
}

// parseRoot returns the root whose padded standard base64 is line, and
// reports false for a line that is not the encoding of 32, 48 or 64
// bytes.
func parseRoot(line []byte) (crypto.Digest, bool) {
	if len(line) > maxRootText {
		return crypto.Digest{}, false
	}

	var raw [maxRootBytes]byte

	n, err := strictBase64.Decode(raw[:], line)
	if err != nil {
		return crypto.Digest{}, false
	}

	root, err := crypto.DigestFromBytes(raw[:n])

	return root, err == nil
}

// validLine reports whether s is an origin or extension line: non-empty
// valid UTF-8 without a character below U+0020. A character below U+0020
// is one byte in UTF-8, and no other character encodes to such a byte.
func validLine(s string) bool {
	if s == "" || !utf8.ValidString(s) {
		return false
	}

	for i := range len(s) {
		if s[i] <= lastControl {
			return false
		}
	}

	return true
}

// validText reports whether text is valid UTF-8 without a character below
// U+0020 other than newline.
func validText(text []byte) bool {
	for _, c := range text {
		if c <= lastControl && c != newline {
			return false
		}
	}

	return utf8.Valid(text)
}
