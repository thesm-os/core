// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package tlog

//go:generate go tool kanon -type=Proof -canonical

import (
	"bytes"
	"cmp"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"go.thesmos.sh/core/crypto"
)

// The lines of the text of a C2SP tlog-proof.
const (
	// proofHeader is the header line of C2SP tlog-proof v1, the format of the
	// zero ProofFormat.
	proofHeader = "c2sp.org/tlog-proof@v1"

	// extraPrefix starts the optional extra line, and indexPrefix the index
	// line.
	extraPrefix = "extra "
	indexPrefix = "index "

	// lineEnd ends every line of a proof.
	lineEnd = '\n'
)

// strictBase64 decodes padded standard base64, and refuses an encoding whose
// spare bits are not zero, as C2SP requires of the base64 of a proof.
var strictBase64 = base64.StdEncoding.Strict()

// ProofFormat is a text format of a [Proof]: its header line and the size of
// each hash. An empty Header is the header line of C2SP tlog-proof v1,
// c2sp.org/tlog-proof@v1, and a Size of 0 is 32 bytes, so the zero
// ProofFormat is C2SP tlog-proof v1. Every other rule of the text is the rule
// of C2SP tlog-proof.
//
// A log whose tree hashes with another algorithm than SHA-256 has a format of
// its own, such as
//
//	ProofFormat{Header: "example.org/tlog-proof-sha384@v1", Size: crypto.DigestSize384}
//
// C2SP defines no header line for another hash. The owner of a format names
// its header line under a domain that the owner controls, so that two owners
// do not choose one header line.
//
// A ProofFormat is valid when its Size is [crypto.DigestSize256],
// [crypto.DigestSize384] or [crypto.DigestSize512] and its Header has no
// newline. The header line of C2SP tlog-proof v1 is valid with a Size of 32
// alone. [Proof.AppendFormat] and [Proof.UnmarshalFormat] return
// [ErrProofText] for a format that is not valid.
type ProofFormat struct {
	// Header is the first line of the text, without its newline.
	Header string

	// Size is the size of each hash in bytes.
	Size int
}

// Proof is the proof of one record of a log, in the text of C2SP tlog-proof
// or of another [ProofFormat]. It contains the inclusion proof of the record
// at Index in the tree of a checkpoint, and the signed note of the
// checkpoint. It can also contain extra data. Its text is a header line, an
// optional extra line, an index line, one hash per line, an empty line and
// the checkpoint.
//
// A verifier parses the checkpoint with
// [go.thesmos.sh/core/note.Note.UnmarshalText], verifies it with
// [go.thesmos.sh/core/tlog/checkpoint.Verifier.Verify], and checks the leaf
// hash of the record against the root of the checkpoint with
// [VerifyInclusion] at Index. The extra data is not authenticated.
//
// # Encoding
//
// [Proof.AppendText] and [Proof.UnmarshalText] write and read the text form
// that C2SP tlog-proof v1 specifies. [Proof.AppendFormat] and
// [Proof.UnmarshalFormat] write and read the text of a ProofFormat, such as
// a format of SHA-384 hashes. A reader of more than one format reads the
// first line of a text, and passes the format of that header line to
// UnmarshalFormat. kanon generates the canonical codec of Proof, the form of
// a Proof in a kanon record, with the field numbers Extra 1, Hashes 2,
// Checkpoint 3 and Index 4. Its decode accepts only the encoding that its
// encode writes. The methods of the codec have pointer receivers.
//
// A proof without extra data has an empty Extra in both forms. Its text has
// no extra line. UnmarshalText accepts an extra line without data, which
// C2SP permits, as a proof without extra data, so AppendText writes that
// text back without the line. Every other text that UnmarshalText accepts,
// AppendText writes back byte for byte. AppendFormat and UnmarshalFormat
// treat the text of every format the same way.
//
// # Concurrency
//
// Not safe for concurrent use.
//
// # Allocation contract
//
// [Proof.AppendText] and [Proof.AppendFormat] allocate nothing when dst has
// room for the text. [Proof.UnmarshalText] and [Proof.UnmarshalFormat]
// allocate nothing when the slices of the Proof have room for the extra
// data, the hashes and the checkpoint.
type Proof struct {
	// Extra is the extra data, and empty for a proof without extra data.
	Extra []byte

	// Hashes is the inclusion proof, from the sibling of the leaf up to a
	// child of the root. Each hash is a digest of the hash of the log, which
	// is SHA-256 in C2SP tlog-proof v1.
	Hashes []crypto.Digest

	// Checkpoint is the signed note of the checkpoint, verbatim. It ends with
	// a newline.
	Checkpoint []byte

	// Index is the index of the record in the log.
	Index uint64
}

// AppendText appends the text of p in C2SP tlog-proof v1 to dst and returns
// the extended slice, as [Proof.AppendFormat] of the zero [ProofFormat]
// does. It implements [encoding.TextAppender].
//
// Returns dst unchanged and [ErrProofText] for a hash that is not a SHA-256
// digest, and for a checkpoint that does not end with a newline, an empty
// checkpoint included.
//
// # Allocation contract
//
// Zero alloc when dst has room for the text.
func (p *Proof) AppendText(dst []byte) ([]byte, error) {
	return p.AppendFormat(dst, ProofFormat{})
}

// AppendFormat appends the text of p in the format f to dst and returns the
// extended slice. It writes the header line of f, the extra line for extra
// data of one byte or more, each hash in padded standard base64, and the
// checkpoint verbatim.
//
// Returns dst unchanged and [ErrProofText] for a format that is not valid,
// for a hash whose size is not the size of f, and for a checkpoint that does
// not end with a newline, an empty checkpoint included.
//
// # Allocation contract
//
// Zero alloc when dst has room for the text.
func (p *Proof) AppendFormat(dst []byte, f ProofFormat) ([]byte, error) {
	header, size, err := f.lines()
	if err != nil {
		return dst, err
	}

	for i := range p.Hashes {
		if n := p.Hashes[i].Size(); n != size {
			return dst, fmt.Errorf("%w: hash %d has %d bytes", ErrProofText, i, n)
		}
	}

	if len(p.Checkpoint) == 0 || p.Checkpoint[len(p.Checkpoint)-1] != lineEnd {
		return dst, fmt.Errorf("%w: the checkpoint does not end with a newline", ErrProofText)
	}

	dst = append(dst, header...)
	dst = append(dst, lineEnd)

	if len(p.Extra) > 0 {
		dst = append(dst, extraPrefix...)
		dst = base64.StdEncoding.AppendEncode(dst, p.Extra)
		dst = append(dst, lineEnd)
	}

	dst = append(dst, indexPrefix...)
	dst = strconv.AppendUint(dst, p.Index, 10)
	dst = append(dst, lineEnd)

	for i := range p.Hashes {
		dst = base64.StdEncoding.AppendEncode(dst, p.Hashes[i].Bytes())
		dst = append(dst, lineEnd)
	}

	dst = append(dst, lineEnd)

	return append(dst, p.Checkpoint...), nil
}

// UnmarshalText sets p to the proof of text in C2SP tlog-proof v1, as
// [Proof.UnmarshalFormat] of the zero [ProofFormat] does. It implements
// [encoding.TextUnmarshaler].
//
// Returns [ErrProofText] for a text without the header line, an extra line
// that is not canonical base64, a text without an index line, an index that
// is not the decimal of a uint64 without leading zeros, a hash line that is
// not the canonical base64 of 32 bytes, a text without the empty line before
// the checkpoint, and a checkpoint that does not end with a newline, an empty
// checkpoint included. After an error the slices of p have the length 0 and
// keep their memory for the next UnmarshalText, and Index is 0.
//
// # Allocation contract
//
// Zero alloc when the slices of p have room for the extra data, the hashes
// and the checkpoint.
func (p *Proof) UnmarshalText(text []byte) error {
	return p.UnmarshalFormat(text, ProofFormat{})
}

// UnmarshalFormat sets p to the proof of text in the format f, and reuses the
// memory of p. It decodes the extra data into the capacity of Extra. It
// copies the hashes and the checkpoint into the capacity of Hashes and
// Checkpoint, so p does not alias text.
//
// Returns [ErrProofText] for a format that is not valid, and for a text that
// is not a text of f: a text without the header line of f, an extra line
// that is not canonical base64, a text without an index line, an index that
// is not the decimal of a uint64 without leading zeros, a hash line that is
// not the canonical base64 of the size of f, a text without the empty line
// before the checkpoint, and a checkpoint that does not end with a newline,
// an empty checkpoint included. After an error the slices of p have the
// length 0 and keep their memory for the next call, and Index is 0.
//
// # Allocation contract
//
// Zero alloc when the slices of p have room for the extra data, the hashes
// and the checkpoint.
func (p *Proof) UnmarshalFormat(text []byte, f ProofFormat) error {
	if err := p.unmarshal(text, f); err != nil {
		p.Extra, p.Hashes, p.Checkpoint, p.Index = p.Extra[:0], p.Hashes[:0], p.Checkpoint[:0], 0

		return err
	}

	return nil
}

// unmarshal sets p to the proof of text in the format f, as
// [Proof.UnmarshalFormat] describes, and returns the error of
// UnmarshalFormat without emptying p.
func (p *Proof) unmarshal(text []byte, f ProofFormat) error {
	header, size, err := f.lines()
	if err != nil {
		return err
	}

	// A text that ends inside a line before the checkpoint leaves no
	// checkpoint, which the check of the checkpoint refuses, so the cuts do
	// not check that they found a newline.
	line, rest, _ := bytes.Cut(text, []byte{lineEnd})
	if string(line) != header {
		return fmt.Errorf("%w: the first line is not %s", ErrProofText, header)
	}

	p.Extra = p.Extra[:0]

	line, rest, _ = bytes.Cut(rest, []byte{lineEnd})
	if encoded, found := bytes.CutPrefix(line, []byte(extraPrefix)); found {
		extra, err := strictBase64.AppendDecode(p.Extra, encoded)
		if err != nil {
			return fmt.Errorf("%w: the extra line is not canonical base64", ErrProofText)
		}

		p.Extra = extra
		line, rest, _ = bytes.Cut(rest, []byte{lineEnd})
	}

	digits, found := bytes.CutPrefix(line, []byte(indexPrefix))
	if !found {
		return fmt.Errorf("%w: no index line", ErrProofText)
	}

	if len(digits) > 1 && digits[0] == '0' {
		return fmt.Errorf("%w: the index has a leading zero", ErrProofText)
	}

	// strconv.ParseUint accepts only the digits of base 10.
	index, err := strconv.ParseUint(string(digits), 10, 64)
	if err != nil {
		return fmt.Errorf("%w: the index is not the decimal of a uint64", ErrProofText)
	}

	hashes := p.Hashes[:0]

	for {
		line, rest, _ = bytes.Cut(rest, []byte{lineEnd})
		if len(line) == 0 {
			break
		}

		h, valid := parseHash(line, size)
		if !valid {
			return fmt.Errorf("%w: hash %d is not the canonical base64 of %d bytes", ErrProofText, len(hashes), size)
		}

		hashes = append(hashes, h)
	}

	if len(rest) == 0 || rest[len(rest)-1] != lineEnd {
		return fmt.Errorf("%w: the checkpoint does not end with a newline", ErrProofText)
	}

	p.Index, p.Hashes = index, hashes
	p.Checkpoint = append(p.Checkpoint[:0], rest...)

	return nil
}

// lines returns the header line of f and the size of its hashes, the header
// line of C2SP tlog-proof v1 for an empty Header and 32 bytes for a Size of
// 0. It returns [ErrProofText] for a format that is not valid.
func (f ProofFormat) lines() (string, int, error) {
	header, size := cmp.Or(f.Header, proofHeader), cmp.Or(f.Size, crypto.DigestSize256)
	switch {
	case size != crypto.DigestSize256 && size != crypto.DigestSize384 && size != crypto.DigestSize512:
		return "", 0, fmt.Errorf("%w: a hash of %d bytes is not a digest", ErrProofText, size)
	case strings.ContainsRune(header, lineEnd):
		return "", 0, fmt.Errorf("%w: the header line has a newline", ErrProofText)
	case header == proofHeader && size != crypto.DigestSize256:
		return "", 0, fmt.Errorf("%w: the hashes of %s have 32 bytes", ErrProofText, proofHeader)
	}

	return header, size, nil
}

// parseHash returns the hash of size bytes whose canonical base64 is line,
// and reports false for a line that is not the encoding of size bytes. A
// line as long as the encoding of size bytes decodes to two bytes fewer up to
// two bytes more than size. [crypto.DigestFromBytes] accepts no such length
// but size, because the sizes of a digest are 16 bytes apart.
func parseHash(line []byte, size int) (crypto.Digest, bool) {
	if len(line) != base64.StdEncoding.EncodedLen(size) {
		return crypto.Digest{}, false
	}

	var raw [crypto.MaxDigestSize + 2]byte

	n, err := strictBase64.Decode(raw[:], line)
	if err != nil {
		return crypto.Digest{}, false
	}

	d, err := crypto.DigestFromBytes(raw[:n])
	if err != nil {
		return crypto.Digest{}, false
	}

	return d, true
}
