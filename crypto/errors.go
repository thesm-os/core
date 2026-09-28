// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto

//go:generate testkit sentinel -o errors.gen_test.go

import (
	"errors"

	"go.thesmos.sh/core/errs"
)

// Sentinel errors returned by this package and by its implementations.
// Each has the class its doc states under
// [go.thesmos.sh/core/errs.Classify], and none of the classes is
// Transient, so a retry loop stops at every one of them.
var (
	// ErrDigestSize is returned when a byte slice decoded as a [Digest]
	// is not exactly [DigestSize256], [DigestSize384], or
	// [DigestSize512] bytes long. A truncated input returns this error
	// instead of a panic or a truncated digest. Classifies as Invalid.
	ErrDigestSize = errs.WithClass(errors.New("crypto: digest length must be 32, 48, or 64 bytes"), errs.Invalid)

	// ErrDigestZero is returned when the zero [Digest] is marshalled,
	// and classifies as Invalid. The zero Digest is the uninitialised
	// value and has no wire form. As zero bytes, it would make every
	// truncated read decode back into a digest the caller never wrote,
	// so the format that contains a digest encodes its absence.
	ErrDigestZero = errs.WithClass(errors.New("crypto: the zero digest has no binary encoding"), errs.Invalid)

	// ErrKeySize is returned when a key's length does not match any
	// size the algorithm accepts, and when a requested key size is not
	// positive. Classifies as Invalid.
	ErrKeySize = errs.WithClass(errors.New("crypto: key length does not match the algorithm"), errs.Invalid)

	// ErrCiphertextShort is returned by [Open], [AppendOpen], and
	// [PeekAlgorithm] when the input is too short to hold the envelope
	// it claims: a truncated header, an algorithm name longer than the
	// bytes that follow it, or no room for a nonce.
	//
	// Classifies as Integrity: the envelope failed a check of its
	// structure, and a retry reads the same bytes.
	ErrCiphertextShort = errs.WithClass(errors.New("crypto: sealed envelope truncated"), errs.Integrity)

	// ErrEnvelopeVersion is returned when a sealed envelope declares a
	// layout this build does not know.
	//
	// An unknown version is refused outright, never parsed as far as
	// the reader recognises. Forward-compatible parsing of a security
	// envelope is a downgrade path. It lets a reader act on the part of
	// a structure it understands while it ignores the part that changed
	// the meaning.
	//
	// Classifies as Unsupported: a later build may open the envelope,
	// and this build cannot.
	ErrEnvelopeVersion = errs.WithClass(errors.New("crypto: unknown sealed-envelope version"), errs.Unsupported)

	// ErrAlgorithmMismatch is returned by [Open] and [AppendOpen] when
	// the envelope names an algorithm other than the [AEAD]'s own.
	//
	// Decided from the header before any key is used, so it is not the
	// distinguishable authentication failure the package otherwise
	// forbids: it reports a fact the caller supplied and can already
	// read, never why a tag failed to verify. Select the implementation
	// with [PeekAlgorithm] to avoid it.
	//
	// Classifies as Invalid: the caller opened the envelope with the
	// wrong AEAD.
	ErrAlgorithmMismatch = errs.WithClass(
		errors.New("crypto: envelope algorithm does not match the AEAD"), errs.Invalid)

	// ErrAlgorithmSize is returned when an algorithm name will not fit
	// the envelope's single length byte. Classifies as Integrity, the
	// class of the open case.
	//
	// On open it means the envelope declares a zero-length name, which
	// no AEAD can match and which would otherwise surface as a mismatch
	// against a name that was never there. On seal it means the [AEAD]
	// reports a name that is empty or over 255 bytes, which is a defect
	// in that implementation.
	ErrAlgorithmSize = errs.WithClass(errors.New("crypto: algorithm name empty or over 255 bytes"), errs.Integrity)

	// ErrKeyID is returned when a key identifier is empty, or names a
	// key the custodian does not have. See [Keeper.KeyID] and
	// [Destroyer.Destroy].
	//
	// Classifies as NotFound: no key has the identifier, and an empty
	// identifier does not name a key.
	ErrKeyID = errs.WithClass(errors.New("crypto: unknown or empty key identifier"), errs.NotFound)

	// ErrKeyDestroyed is returned by a [Destroyer] whose wrapping key
	// has been destroyed or scheduled for destruction. It is distinct
	// from a corruption failure. A destroyed key is unrecoverable by
	// design, while corrupted material points to damaged storage, and
	// a caller handles the two differently.
	//
	// Classifies as Denied: the custodian refuses every operation under
	// the key by policy, and no retry succeeds. Until the destruction is
	// irreversible, an administrator of the custodian can cancel it.
	ErrKeyDestroyed = errs.WithClass(errors.New("crypto: wrapping key destroyed"), errs.Denied)

	// ErrXOFSqueezing is returned by [XOFStream.Write] once Read has
	// been called. The sponge absorbs and then squeezes, and the phases
	// do not interleave. Resuming absorption would produce output
	// unrelated to what a reader expects.
	//
	// The standard library panics on this. The seam returns an error,
	// because a caller gets into this state by passing a stream through
	// code that does not know its phase. That is a runtime condition
	// and not a programmer error. Classifies as Invalid: every later
	// write to the stream fails the same way.
	ErrXOFSqueezing = errs.WithClass(errors.New("crypto: write after read on an XOF stream"), errs.Invalid)

	// ErrChunkSize is returned when a chunk breaks the size rules of its
	// [ChunkHeader], when a chunk size is out of range, and when an [AEAD]
	// seals a chunk to a length other than [SealedSize]. Only the
	// functions that encode a header or seal a chunk return it.
	// Classifies as Invalid.
	ErrChunkSize = errs.WithClass(errors.New("crypto: chunk size does not match its header"), errs.Invalid)

	// ErrChunkHeader is returned by [ChunkHeader.UnmarshalBinary] for an
	// encoding of another length, an unknown version or a chunk size of 0.
	// Classifies as Invalid.
	ErrChunkHeader = errs.WithClass(errors.New("crypto: malformed chunk header"), errs.Invalid)
)
