// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto

//go:generate testkit sentinel -o errors.gen_test.go

import "errors"

// Sentinel errors returned by [DigestFromBytes], [Digest.AppendBinary],
// [Digest.MarshalBinary], and [Digest.UnmarshalBinary].
var (
	// ErrDigestSize is returned when a digest-shaped byte slice is
	// not exactly [DigestSize256], [DigestSize384], or
	// [DigestSize512] bytes long. A truncated input is a decode
	// error, never a panic and never a silent truncation.
	ErrDigestSize = errors.New("crypto: digest length must be 32, 48, or 64 bytes")

	// ErrDigestZero is returned when the zero [Digest] is marshalled.
	// The zero Digest is an in-memory sentinel with no wire form:
	// encoding it as zero bytes would make every truncated read
	// decode back into a genesis anchor. Encoding the absence of a
	// digest is the containing format's job. See ADR-0007.
	ErrDigestZero = errors.New("crypto: the zero digest has no binary encoding")

	// ErrKeySize is returned when a key's length does not match any
	// size the algorithm accepts.
	ErrKeySize = errors.New("crypto: key length does not match the algorithm")

	// ErrCiphertextShort is returned by [Open], [AppendOpen], and
	// [PeekAlgorithm] when the input is too short to hold the envelope
	// it claims: a truncated header, an algorithm name longer than the
	// bytes that follow it, or no room for a nonce.
	ErrCiphertextShort = errors.New("crypto: sealed envelope truncated")

	// ErrEnvelopeVersion is returned when a sealed envelope declares a
	// layout this build does not know.
	//
	// An unknown version is refused outright, never parsed as far as
	// the reader recognises. Forward-compatible parsing of a security
	// envelope is a downgrade path: it invites a reader to act on the
	// part of a structure it understands while ignoring the part that
	// changed the meaning.
	ErrEnvelopeVersion = errors.New("crypto: unknown sealed-envelope version")

	// ErrAlgorithmMismatch is returned by [Open] and [AppendOpen] when
	// the envelope names an algorithm other than the [AEAD]'s own.
	//
	// Decided from the header before any key is used, so it is not the
	// distinguishable authentication failure the package otherwise
	// forbids: it reports a fact the caller supplied and can already
	// read, never why a tag failed to verify. Select the implementation
	// with [PeekAlgorithm] to avoid it.
	ErrAlgorithmMismatch = errors.New("crypto: envelope algorithm does not match the AEAD")

	// ErrAlgorithmSize is returned when an algorithm name will not fit
	// the envelope's single length byte.
	//
	// On seal that means the [AEAD] reports a name that is empty or
	// over 255 bytes, which is a defect in that implementation. On open
	// it means the envelope declares a zero-length name, which no AEAD
	// can match and which would otherwise surface as a mismatch against
	// a name that was never there.
	ErrAlgorithmSize = errors.New("crypto: algorithm name empty or over 255 bytes")

	// ErrKeyID is returned when a key identifier is empty, or names a
	// key the custodian does not hold. See [Keeper.KeyID] and
	// [Destroyer.Destroy].
	ErrKeyID = errors.New("crypto: unknown or empty key identifier")

	// ErrKeyDestroyed is returned by a [Destroyer] whose wrapping key
	// has been destroyed. Distinct from a corruption failure: this
	// one is unrecoverable by design, where corrupted material
	// suggests damaged storage, and a caller acts on them
	// differently.
	ErrKeyDestroyed = errors.New("crypto: wrapping key destroyed")

	// ErrXOFSqueezing is returned by [XOFStream.Write] once Read has
	// been called. The sponge absorbs and then squeezes; the phases
	// do not interleave, and resuming absorption would silently
	// produce output unrelated to what a reader expects.
	//
	// The standard library panics on this. The seam returns an error
	// because a caller reaches the state by threading a stream
	// through code that does not know its phase, which is a runtime
	// condition rather than a programmer error.
	ErrXOFSqueezing = errors.New("crypto: write after read on an XOF stream")
)
