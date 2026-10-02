// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package note

//go:generate go tool kanon -type=Note -canonical

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"slices"
	"unicode/utf8"

	"go.thesmos.sh/core/crypto/sign"
)

const (
	// lastControl is the last character below U+0020. signed-note forbids
	// every character below U+0020 in a note, apart from newline.
	lastControl = 0x1f

	// signatureSplit separates the text of a note from its signature
	// lines: the newline that ends the text, and a blank line.
	signatureSplit = "\n\n"

	// nameBuffer is the length of the key names that [Parse] collects on
	// the stack before it copies them into one string.
	nameBuffer = 256

	// checkLines is the number of signature lines that [Note.Check]
	// converts on the stack: 16, the number that signed-note requires a
	// verifier to accept.
	checkLines = 16
)

// Note is a signed note: a text and its signature lines.
//
// A Note that [Parse] returns is Valid, and [Note.AppendText] writes it
// back byte for byte.
//
// # Encoding
//
// [Parse] and [Note.AppendText] read and write the signed note, the text
// form that signed-note specifies and that the signatures cover. kanon
// generates the canonical codec of Note, the form of a Note in a kanon
// record, with the field numbers Text 1 and Signatures 2. The codec
// follows the rules of the codec of [Key].
type Note struct {
	// Text is the note text. It ends in a newline.
	Text []byte

	// Signatures are the signature lines, in the order of the note.
	Signatures []Signature
}

// Parse parses the signed note msg without verifying it: the text up to
// the last blank line of msg, and one signature line per line after it.
// The text of a note is untrusted until [Note.Check] returns nil. It
// parses msg into a new Note, as [Note.UnmarshalText] parses it.
//
// Text is a subslice of msg. The key names share one string, and the
// values of the signature lines share one buffer, which does not alias
// msg. Parse keeps every line, including repeated lines and the lines of
// unknown keys.
//
// Returns [ErrNote], classified [errs.Invalid], for a note that is not
// valid UTF-8, that contains a character below U+0020 other than newline,
// that has no blank line before its signature lines, that has no signature
// line, or that has a signature line that does not start with "— ", has
// an invalid name, or is not the padded standard base64 of a key ID and a
// signature. It returns the zero Note with an error.
//
// # Allocation contract
//
// Allocates three times, whatever the number of lines: the slice of
// lines, the buffer of the values, and the string of the key names. Key
// names of more than 256 bytes together allocate once more. A note that
// Parse refuses allocates its error. UnmarshalText into a reused Note is
// the path without these allocations.
func Parse(msg []byte) (Note, error) {
	var n Note
	if err := n.UnmarshalText(msg); err != nil {
		return Note{}, err
	}

	return n, nil
}

// Open parses msg, as [Parse] does, and returns the note when its
// signature lines satisfy p, as [Note.Check] reports.
//
// Returns the errors of Parse and of Check, with the zero Note.
//
// # Allocation contract
//
// The allocations of Parse and of Check. UnmarshalText into a reused
// Note, followed by Check, is the path without them.
func Open(msg []byte, p sign.Policy) (Note, error) {
	n, err := Parse(msg)
	if err != nil {
		return Note{}, err
	}

	if err := n.Check(p); err != nil {
		return Note{}, err
	}

	return n, nil
}

// Sign returns the note of text with one signature line from each of
// signers, in their order, as [Note.Sign] signs it into a new Note.
//
// Returns the errors of Note.Sign, with the zero Note.
//
// # Allocation contract
//
// Allocates the slice of lines and the value of each line. Note.Sign into
// a reused Note is the path without these allocations.
func Sign(ctx context.Context, text []byte, signers ...Signer) (Note, error) {
	var n Note
	if err := n.Sign(ctx, text, signers...); err != nil {
		return Note{}, err
	}

	return n, nil
}

// Sign sets n to the note of text with one signature line from each of
// signers, in their order, and reuses the memory of n: the capacity of
// Signatures, and the Value at the position of each signer, into which
// [Signer.AppendSign] appends the signature. ctx bounds the wait of a
// signer behind a process boundary. The Text of n is text.
//
// A witness that cosigns one log's checkpoints in a loop signs each into
// one Note, whose values then have room for the next signatures.
//
// Returns [ErrNote], classified [errs.Invalid], for a text that does not
// end in a newline, that is not valid UTF-8, or that contains a character
// below U+0020 other than newline, for no signers, and for a signer that
// returns an empty signature. Returns [ErrKey], classified errs.Invalid,
// for a nil signer and a signer with an invalid name. Returns the error of
// a signer that fails. After an error n has no text and no line, and keeps
// its memory for the next Sign.
//
// # Allocation contract
//
// Zero-alloc when n has room for the lines and for each signature, and
// every signer appends without an allocation, as an Ed25519 signer does.
// An ML-DSA signer allocates the signature that the standard library
// returns.
func (n *Note) Sign(ctx context.Context, text []byte, signers ...Signer) error {
	if err := n.sign(ctx, text, signers); err != nil {
		n.Text, n.Signatures = nil, n.Signatures[:0]

		return err
	}

	return nil
}

// sign is Note.Sign without the emptying of n on an error. It reuses the
// lines in the capacity of Signatures, so that a Note that an error
// emptied signs into the memory of its last note.
func (n *Note) sign(ctx context.Context, text []byte, signers []Signer) error {
	if !validText(text) {
		return fmt.Errorf(
			"%w: a text ends in a newline, is valid UTF-8, and has no character below U+0020 other than newline",
			ErrNote)
	}

	if len(signers) == 0 {
		return fmt.Errorf("%w: no signer", ErrNote)
	}

	old := n.Signatures[:cap(n.Signatures)]
	sigs := slices.Grow(old[:0], len(signers))

	for i, s := range signers {
		if s == nil {
			return fmt.Errorf("%w: a nil signer", ErrKey)
		}

		k := s.Key()
		if !k.Name.Valid() {
			return fmt.Errorf("%w: a signer with the invalid name %q", ErrKey, k.Name)
		}

		var room []byte
		if i < len(old) {
			room = old[i].Value[:0]
		}

		value, err := s.AppendSign(ctx, room, text)
		if err != nil {
			return fmt.Errorf("note: sign with %s: %w", k.Name, err)
		}

		if len(value) == 0 {
			return fmt.Errorf("%w: %s returned an empty signature", ErrNote, k.Name)
		}

		sigs = append(sigs, Signature{Name: k.Name, Value: value, ID: k.ID()})
	}

	n.Text, n.Signatures = text, sigs

	return nil
}

// Check reports whether the signature lines of n satisfy p for n.Text. It
// converts each line to the [sign.Signature] {[Algorithm], [KeyID](Name,
// ID), Value} and calls [sign.Policy.Check], which considers the first
// line of each key of p, ignores the other lines without verifying them,
// and verifies a line only when the result depends on it.
//
// Check takes no excluded keys. sign.Policy.Check removes the whole party
// of an excluded key, and the parties of the tree of a checkpoint are its
// log rule and its quorum. A caller that excludes keys of a policy of its
// own converts the lines itself and calls sign.Policy.Check.
//
// Returns nil when p is satisfied, and the error of sign.Policy.Check
// otherwise, which wraps [sign.ErrThreshold], classified [errs.Integrity],
// for lines that do not satisfy p.
//
// # Allocation contract
//
// Zero-alloc for a note of at most 16 lines, apart from the allocations
// of sign.Policy.Check: it converts the lines on the stack. A note of more
// lines allocates its slice of sign.Signature.
func (n *Note) Check(p sign.Policy) error {
	var buf [checkLines]sign.Signature

	sigs := buf[:0]
	for _, s := range n.Signatures {
		sigs = append(sigs, sign.Signature{Algorithm: Algorithm, KeyID: KeyID(s.Name, s.ID), Value: s.Value})
	}

	return p.Check(n.Text, sigs)
}

// Find returns the first signature line of n whose key name and key ID
// are those of k, and reports whether n has one. It does not verify the
// line.
//
// # Allocation contract
//
// Zero-alloc.
func (n *Note) Find(k Key) (Signature, bool) {
	id := k.ID()
	for _, s := range n.Signatures {
		if s.Name == k.Name && s.ID == id {
			return s, true
		}
	}

	return Signature{}, false
}

// Valid reports whether the text of n ends in a newline, is valid UTF-8
// and contains no character below U+0020 other than newline, and whether n
// has at least one signature line and every line is Valid.
//
// # Allocation contract
//
// Zero-alloc.
func (n *Note) Valid() bool {
	if !validText(n.Text) || len(n.Signatures) == 0 {
		return false
	}

	for _, s := range n.Signatures {
		if !s.Valid() {
			return false
		}
	}

	return true
}

// AppendText appends the note to b and returns the extended slice: the
// text, a blank line, and each signature line. It implements
// [encoding.TextAppender].
//
// Returns b unchanged and [ErrNote], classified [errs.Invalid], for a Note
// that is not Valid.
//
// # Allocation contract
//
// Zero-alloc when b has room for the note.
func (n *Note) AppendText(b []byte) ([]byte, error) {
	if !n.Valid() {
		return b, fmt.Errorf("%w: a Note needs a valid text and valid signature lines", ErrNote)
	}

	b = append(b, n.Text...)
	b = append(b, '\n')

	for _, s := range n.Signatures {
		b = s.appendLine(b)
	}

	return b, nil
}

// MarshalText returns the note, as [Note.AppendText] writes it. It
// implements [encoding.TextMarshaler].
//
// Returns nil and the error of AppendText for a Note that is not Valid.
//
// # Allocation contract
//
// Allocates the returned note once, at its exact length.
func (n *Note) MarshalText() ([]byte, error) {
	b, err := n.AppendText(make([]byte, 0, n.textLen()))
	if err != nil {
		return nil, err
	}

	return b, nil
}

// UnmarshalText sets n to the note of msg, as [Parse] parses it, and
// reuses the memory of n: the lines in the capacity of Signatures, each
// Name that equals the key name of the line at its position, and each
// Value, into whose capacity it decodes the signature of the line at its
// position. It implements [encoding.TextUnmarshaler]. Text is a subslice
// of msg.
//
// A caller that verifies one log's checkpoints in a loop parses each into
// one Note, whose lines then repeat the names and the sizes of the lines
// before.
//
// Returns the errors of Parse. After an error n has no text and no line,
// and keeps its memory for the next UnmarshalText.
//
// # Allocation contract
//
// Zero-alloc when each line repeats the key name of the line at its
// position in n and fits its signature into the Value there. Otherwise
// allocates one string for the key names that change, one buffer for the
// values of the lines that have no Value in n, the value of a line whose
// signature outgrows its Value, and the growth of Signatures.
func (n *Note) UnmarshalText(msg []byte) error {
	if err := n.unmarshal(msg); err != nil {
		n.Text, n.Signatures = nil, n.Signatures[:0]

		return err
	}

	return nil
}

// unmarshal is Note.UnmarshalText without the emptying of n on an error.
// It reads the signature lines twice. The first pass checks the form of
// each line, collects the key names that differ from the names of n, and
// sums the bound of the values of the lines that have no memory in n. The
// second pass decodes each line into the memory of the line at its
// position, or into a region of one buffer of that sum.
func (n *Note) unmarshal(msg []byte) error {
	text, lines, err := splitNote(msg)
	if err != nil {
		return err
	}

	old := n.Signatures[:cap(n.Signatures)]

	var buf [nameBuffer]byte

	names, count, size := buf[:0], 0, 0

	for line := range bytes.Lines(lines) {
		name, encoded, err := splitLine(line)
		if err != nil {
			return err
		}

		prev := lineAt(old, count)
		if string(name) != string(prev.Name) {
			names = append(names, name...)
		}

		if cap(prev.Value) == 0 {
			size += base64.StdEncoding.DecodedLen(len(encoded))
		}

		count++
	}

	joined := string(names)
	values := make([]byte, 0, size)
	sigs := slices.Grow(old[:0], count)
	i, off := 0, 0

	for line := range bytes.Lines(lines) {
		name, encoded, _ := splitLine(line)

		prev := lineAt(old, i)
		if string(name) != string(prev.Name) {
			prev.Name = Name(joined[:len(name)])
			joined = joined[len(name):]
		}

		dst := prev.Value[:0]
		if cap(dst) == 0 {
			bound := base64.StdEncoding.DecodedLen(len(encoded))
			dst = values[off : off : off+bound]
			off += bound
		}

		s, _, err := parseSignature(prev.Name, encoded, dst)
		if err != nil {
			return err
		}

		sigs = append(sigs, s)
		i++
	}

	n.Text, n.Signatures = text, sigs

	return nil
}

// lineAt returns the line at position i of lines, and the zero Signature
// for a position past their end.
func lineAt(lines []Signature, i int) Signature {
	if i < len(lines) {
		return lines[i]
	}

	return Signature{}
}

// textLen returns the length of the note of n: the text, a blank line,
// and each signature line.
func (n *Note) textLen() int {
	size := len(n.Text) + 1
	for _, s := range n.Signatures {
		size += s.textLen()
	}

	return size
}

// TextOf returns the text of the signed note msg, as [Parse] returns it in
// Note.Text, without building a Note: the text up to the last blank line
// of msg. The text is a subslice of msg. TextOf checks msg as Parse does,
// the form of each signature line included, in the same order, so it
// accepts exactly the notes that Parse accepts and returns the same
// errors. It decodes each signature in chunks of 512 characters into an
// array on its stack, and keeps nothing of it.
//
// A caller that hashes or compares the texts of the notes of many logs,
// whose key names differ from one note to the next, reads each text
// without the allocations of [Note.UnmarshalText] into a reused Note.
//
// Returns [ErrNote], classified [errs.Invalid], for every note that Parse
// refuses, with the message of Parse, and nil text.
//
// # Allocation contract
//
// Zero-alloc for a note that TextOf accepts, whatever its key names and
// its number of lines. A note that TextOf refuses allocates its error.
func TextOf(msg []byte) ([]byte, error) {
	text, lines, err := splitNote(msg)
	if err != nil {
		return nil, err
	}

	for line := range bytes.Lines(lines) {
		if _, _, err := splitLine(line); err != nil {
			return nil, err
		}
	}

	for line := range bytes.Lines(lines) {
		name, encoded, _ := splitLine(line)
		if err := checkLine(name, encoded); err != nil {
			return nil, err
		}
	}

	return text, nil
}

// splitNote splits msg, a signed note, into its text and its signature
// lines, the bytes after the last blank line.
//
// Returns [ErrNote] for a note that is not valid UTF-8, that contains a
// character below U+0020 other than newline, that has no blank line, or
// whose last line is empty or does not end in a newline.
func splitNote(msg []byte) (text, lines []byte, err error) {
	if !validBytes(msg) {
		return nil, nil, fmt.Errorf("%w: not valid UTF-8, or a character below U+0020 other than newline", ErrNote)
	}

	split := bytes.LastIndex(msg, []byte(signatureSplit))
	if split < 0 {
		return nil, nil, fmt.Errorf("%w: no blank line before the signature lines", ErrNote)
	}

	text, lines = msg[:split+1], msg[split+len(signatureSplit):]
	if len(lines) == 0 || lines[len(lines)-1] != '\n' {
		return nil, nil, fmt.Errorf("%w: no signature line, or a last line without a newline", ErrNote)
	}

	return text, lines, nil
}

// validText reports whether b is a note text: valid UTF-8 that ends in a
// newline, without a character below U+0020 other than newline.
func validText(b []byte) bool {
	return len(b) > 0 && b[len(b)-1] == '\n' && validBytes(b)
}

// validBytes reports whether b is valid UTF-8 without a character below
// U+0020 other than newline. A character below U+0020 is one byte in
// UTF-8, and no other character encodes to such a byte.
func validBytes(b []byte) bool {
	for _, c := range b {
		if c <= lastControl && c != '\n' {
			return false
		}
	}

	return utf8.Valid(b)
}
