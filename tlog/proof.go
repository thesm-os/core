// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package tlog

//go:generate go tool kanon -type=Proof -canonical

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strconv"

	"go.thesmos.sh/core/crypto"
)

// The lines of the text of a C2SP tlog-proof.
const (
	// proofHeader is the first line of a proof.
	proofHeader = "c2sp.org/tlog-proof@v1"

	// extraPrefix starts the optional extra line, and indexPrefix the index
	// line.
	extraPrefix = "extra "
	indexPrefix = "index "

	// hashText is the length of a hash line: the padded standard base64 of
	// a SHA-256 hash.
	hashText = 44

	// lineEnd ends every line of a proof.
	lineEnd = '\n'
)

// strictBase64 decodes padded standard base64, and refuses an encoding whose
// spare bits are not zero, as C2SP requires of the base64 of a proof.
var strictBase64 = base64.StdEncoding.Strict()

// Proof is a C2SP tlog-proof. It contains the inclusion proof of the record
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
// that C2SP tlog-proof specifies. kanon generates the canonical codec of
// Proof, the form of a Proof in a kanon record, with the field numbers Extra
// 1, Hashes 2, Checkpoint 3 and Index 4. Its decode accepts only the encoding
// that its encode writes. The methods of the codec have pointer receivers.
//
// A proof without extra data has an empty Extra in both forms. Its text has
// no extra line. UnmarshalText accepts an extra line without data, which
// C2SP permits, as a proof without extra data, so AppendText writes that
// text back without the line. Every other text that UnmarshalText accepts,
// AppendText writes back byte for byte.
//
// # Concurrency
//
// Not safe for concurrent use.
//
// # Allocation contract
//
// [Proof.AppendText] allocates nothing when dst has room for the text.
// [Proof.UnmarshalText] allocates nothing when the slices of the Proof have
// room for the extra data, the hashes and the checkpoint.
type Proof struct {
	// Extra is the extra data, and empty for a proof without extra data.
	Extra []byte

	// Hashes is the inclusion proof, from the sibling of the leaf up to a
	// child of the root. Each hash is a SHA-256 digest.
	Hashes []crypto.Digest

	// Checkpoint is the signed note of the checkpoint, verbatim. It ends with
	// a newline.
	Checkpoint []byte

	// Index is the index of the record in the log.
	Index uint64
}

// AppendText appends the text of p to dst and returns the extended slice. It
// writes the extra line for extra data of one byte or more, each hash in
// padded standard base64, and the checkpoint verbatim. It implements
// [encoding.TextAppender].
//
// Returns dst unchanged and [ErrProofText] for a hash that is not a SHA-256
// digest, and for a checkpoint that does not end with a newline, an empty
// checkpoint included.
//
// # Allocation contract
//
// Zero alloc when dst has room for the text.
func (p *Proof) AppendText(dst []byte) ([]byte, error) {
	for i := range p.Hashes {
		if size := p.Hashes[i].Size(); size != crypto.DigestSize256 {
			return dst, fmt.Errorf("%w: hash %d has %d bytes", ErrProofText, i, size)
		}
	}

	if len(p.Checkpoint) == 0 || p.Checkpoint[len(p.Checkpoint)-1] != lineEnd {
		return dst, fmt.Errorf("%w: the checkpoint does not end with a newline", ErrProofText)
	}

	dst = append(dst, proofHeader...)
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

// UnmarshalText sets p to the proof of text, and reuses the memory of p. It
// decodes the extra data into the capacity of Extra. It copies the hashes and
// the checkpoint into the capacity of Hashes and Checkpoint, so p does not
// alias text. It implements [encoding.TextUnmarshaler].
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
	if err := p.unmarshal(text); err != nil {
		p.Extra, p.Hashes, p.Checkpoint, p.Index = p.Extra[:0], p.Hashes[:0], p.Checkpoint[:0], 0

		return err
	}

	return nil
}

// unmarshal sets p to the proof of text, as [Proof.UnmarshalText]
// describes, and returns the error of UnmarshalText without emptying p.
func (p *Proof) unmarshal(text []byte) error {
	// A text that ends inside a line before the checkpoint leaves no
	// checkpoint, which the check of the checkpoint refuses, so the cuts do
	// not check that they found a newline.
	line, rest, _ := bytes.Cut(text, []byte{lineEnd})
	if string(line) != proofHeader {
		return fmt.Errorf("%w: the first line is not %s", ErrProofText, proofHeader)
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

		h, valid := parseHash(line)
		if !valid {
			return fmt.Errorf("%w: hash %d is not the canonical base64 of 32 bytes", ErrProofText, len(hashes))
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

// parseHash returns the SHA-256 hash whose canonical base64 is line, and
// reports false for a line that is not the encoding of 32 bytes. A line of
// hashText characters decodes to 31, 32 or 33 bytes, and parseHash refuses
// 31 and 33.
func parseHash(line []byte) (crypto.Digest, bool) {
	if len(line) != hashText {
		return crypto.Digest{}, false
	}

	var raw [crypto.DigestSize256 + 1]byte

	n, err := strictBase64.Decode(raw[:], line)
	//dokimi:mutate-skip ror-false,sbr-delete: Decode returns at most 30 bytes of a line of hashText characters that it refuses, and the check of n refuses the line as well
	if err != nil {
		return crypto.Digest{}, false
	}

	if n != crypto.DigestSize256 {
		return crypto.Digest{}, false
	}

	return crypto.NewDigest256([crypto.DigestSize256]byte(raw[:n])), true
}
