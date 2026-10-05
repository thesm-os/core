// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/tlog/checkpoint"
)

const (
	// addCheckpointPath follows the submission prefix in the URL of
	// add-checkpoint.
	addCheckpointPath = "/add-checkpoint"

	// checkpointPath ends the URL of the monitor retrieval route, after
	// the monitoring prefix and the origin hash.
	checkpointPath = "/checkpoint"

	// sizeContentType is the content type of the body of a 409, which
	// contains the committed size.
	sizeContentType = "text/x.tlog.size"

	// linesContentType is the content type of the cosignature lines of a
	// 200, and of the checkpoint of the monitor retrieval route.
	linesContentType = "text/plain; charset=utf-8"

	// oldPrefix starts the old size line of a request body.
	oldPrefix = "old "

	// maxProof is the largest number of proof lines of a request, which
	// tlog-witness permits.
	maxProof = 63

	// maxLines is the largest number of signature lines of a note that the
	// server verifies. signed-note requires a verifier to accept 16.
	maxLines = 64

	// maxHashText is the length of the longest proof line: the padded
	// standard base64 of a 64-byte hash.
	maxHashText = 88

	// maxHashBytes is the number of bytes that a proof line of maxHashText
	// characters decodes to at most.
	maxHashBytes = 66

	// maxSizeText is the length of the decimal of the largest uint64.
	maxSizeText = 20

	// hashChunk is the size of the buffer on the stack through which
	// hashOrigin copies an origin to its hash.
	hashChunk = 64

	// sizeErrorHead and sizeErrorTail surround the size in the text of a
	// SizeError.
	sizeErrorHead = "witness: the witness committed the size "
	sizeErrorTail = " last"
)

// strictBase64 decodes padded standard base64, and refuses an encoding
// whose spare bits are not zero, which tlog-witness calls canonical.
var strictBase64 = base64.StdEncoding.Strict()

// originHash is the SHA-256 of an origin. It is the key of the state of
// the origin, and its lowercase hexadecimal is the element of the monitor
// retrieval route before "checkpoint".
type originHash [sha256.Size]byte

// hashOrigin returns the originHash of o. It copies o to the hash through
// a buffer on the stack, as note.KeyID copies a key name, so it allocates
// nothing for an origin of any length.
func hashOrigin(o checkpoint.Origin) originHash {
	h := sha256.New()

	var chunk [hashChunk]byte

	for s := string(o); s != ""; {
		n := copy(chunk[:], s)
		h.Write(chunk[:n])
		s = s[n:]
	}

	var sum originHash

	return originHash(h.Sum(sum[:0]))
}

// SizeError is the error of a 409: the witness committed another size of
// the origin last. The server returns it for a call whose old size is not
// the committed size, and [Client.AddCheckpoint] for a 409 of the
// witness.
//
// # Allocation contract
//
// Error allocates its text. Class allocates nothing.
type SizeError struct {
	// Size is the size of the checkpoint that the witness committed last.
	Size uint64
}

// Error returns the text of e: the prefix of the package and the
// committed size. It builds the text in an array on its stack.
func (e *SizeError) Error() string {
	var buf [len(sizeErrorHead) + maxSizeText + len(sizeErrorTail)]byte

	b := append(buf[:0], sizeErrorHead...)
	b = strconv.AppendUint(b, e.Size, 10)
	b = append(b, sizeErrorTail...)

	return string(b)
}

// Class returns [errs.Conflict]: the caller sends its checkpoint again,
// with the committed size as its old size.
func (*SizeError) Class() errs.Class {
	return errs.Conflict
}

// request is the body of an add-checkpoint request before its note.
type request struct {
	// note is the checkpoint note after the blank line, a subslice of
	// the body.
	note []byte

	// proof is the consistency proof, in the order of the body.
	proof []crypto.Digest

	// oldSize is the size of the old size line.
	oldSize uint64
}

// parse sets r to the request of body, and reuses the capacity of
// r.proof. It checks the lines before the note as check 1 of the protocol
// requires them: an old size line in decimal without leading zeros, at
// most 63 proof lines, each the canonical padded standard base64 of a
// 32, 48 or 64-byte hash, and a blank line. It leaves the note to its
// caller. On an error r is unchanged.
//
// Returns an error that wraps [ErrRequest] for any other body.
func (r *request) parse(body []byte) error {
	line, rest, ok := bytes.Cut(body, []byte{'\n'})
	if !ok {
		return fmt.Errorf("%w: a body without an old size line", ErrRequest)
	}

	digits, ok := bytes.CutPrefix(line, []byte(oldPrefix))
	if !ok {
		return fmt.Errorf("%w: a first line that does not start with %q", ErrRequest, oldPrefix)
	}

	oldSize, ok := parseSize(digits)
	if !ok {
		return fmt.Errorf("%w: an old size that is not the decimal of a uint64 without leading zeros", ErrRequest)
	}

	proof := r.proof[:0]

	for {
		line, rest, ok = bytes.Cut(rest, []byte{'\n'})
		if !ok {
			return fmt.Errorf("%w: a body without a blank line before the note", ErrRequest)
		}

		if len(line) == 0 {
			break
		}

		if len(proof) == maxProof {
			return fmt.Errorf("%w: more than %d proof lines", ErrRequest, maxProof)
		}

		h, ok := parseHash(line)
		if !ok {
			return fmt.Errorf("%w: a proof line that is not the canonical base64 of a hash", ErrRequest)
		}

		proof = append(proof, h)
	}

	r.note, r.proof, r.oldSize = rest, proof, oldSize

	return nil
}

// appendRequest appends to dst the body of an add-checkpoint request: the
// old size line, one line of padded standard base64 per hash of proof, a
// blank line, and msg.
func appendRequest(dst []byte, oldSize uint64, proof []crypto.Digest, msg []byte) []byte {
	dst = append(dst, oldPrefix...)
	dst = strconv.AppendUint(dst, oldSize, 10)
	dst = append(dst, '\n')

	for _, h := range proof {
		dst = base64.StdEncoding.AppendEncode(dst, h.Bytes())
		dst = append(dst, '\n')
	}

	dst = append(dst, '\n')

	return append(dst, msg...)
}

// appendSize appends the body of a 409 to dst: size in decimal and a
// newline.
func appendSize(dst []byte, size uint64) []byte {
	dst = strconv.AppendUint(dst, size, 10)

	return append(dst, '\n')
}

// parseSizeBody returns the size of the body of a 409, and reports false
// for a body that is not a decimal without leading zeros and a newline.
func parseSizeBody(body []byte) (uint64, bool) {
	digits, ok := bytes.CutSuffix(body, []byte{'\n'})
	if !ok {
		return 0, false
	}

	return parseSize(digits)
}

// parseSize returns the size whose decimal is digits, and reports false
// for digits that are not the decimal of a uint64 without leading zeros.
// strconv.ParseUint accepts only digits in base 10.
func parseSize(digits []byte) (uint64, bool) {
	if len(digits) > 1 && digits[0] == '0' {
		return 0, false
	}

	size, err := strconv.ParseUint(string(digits), 10, 64)

	return size, err == nil
}

// parseHash returns the hash whose canonical padded standard base64 is
// line, and reports false for a line that is not the encoding of 32, 48
// or 64 bytes. The decoder of encoding/base64 skips carriage returns, so
// parseHash refuses a line with one first.
func parseHash(line []byte) (crypto.Digest, bool) {
	if len(line) > maxHashText || bytes.IndexByte(line, '\r') >= 0 {
		return crypto.Digest{}, false
	}

	var raw [maxHashBytes]byte

	n, err := strictBase64.Decode(raw[:], line)
	if err != nil {
		return crypto.Digest{}, false
	}

	h, err := crypto.DigestFromBytes(raw[:n])

	return h, err == nil
}
