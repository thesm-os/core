// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package checkpoint

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"time"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sign"
	"go.thesmos.sh/core/note"
)

// TypeMLDSA44Cosignature is type 0x06, the timestamped ML-DSA-44
// cosignature of tlog-cosignature. Its [note.Resolver] entry is
// SubtreeV1(mldsa.Resolver(mldsa.MLDSA44, "")), and its public key is the
// 1,312-byte FIPS 204 encoding of an ML-DSA-44 key.
const TypeMLDSA44Cosignature note.Type = "\x06"

const (
	// subtreeLabel is the label that starts a cosigned_message.
	subtreeLabel = "subtree/v1\n\x00"

	// maxField is the length of the longest key name and the longest
	// origin that a cosigned_message contains: each is an opaque field
	// with a one-byte length.
	maxField = 255
)

// subtreeV1 is the format of [SubtreeV1] signatures.
var subtreeV1 = format{message: appendSubtreeV1, assigned: TypeMLDSA44Cosignature, alg: crypto.AlgMLDSA44}

// emptyRoot is SHA-256 of the empty string, the root of the empty tree
// over SHA-256.
var emptyRoot = crypto.NewDigest256(sha256.Sum256(nil))

// SubtreeV1 returns the [note.Resolver] entry of a type whose signatures
// are timestamped signatures, by the algorithm of resolve, over the
// cosigned_message of tlog-cosignature for the checkpoint in the note
// text:
//
//	struct {
//	    uint8 label[12] = "subtree/v1\n\0";
//	    opaque cosigner_name<1..2^8-1>;
//	    uint64 timestamp;
//	    opaque log_origin<1..2^8-1>;
//	    uint64 start;
//	    uint64 end;
//	    uint8 hash[32];
//	} cosigned_message;
//
// The message contains the key name, the timestamp of the signature, the
// origin, start 0, the tree size as end, and the root. resolve is the
// [sign.Resolver] entry of the algorithm, such as
// mldsa.Resolver(mldsa.MLDSA44, "") for [TypeMLDSA44Cosignature], whose
// signatures use the empty context. A timestamp of 0 states no time, as a
// log writes it when it signs its own checkpoint.
//
// A cosigned_message omits the extension lines and has room for a 32-byte
// root. A Verifier of the entry therefore reports false for a note text
// that is not a [Body], a body with extension lines, a root that is not 32
// bytes, an origin or a key name longer than 255 bytes, and a body of size
// 0 whose root is not SHA-256 of the empty string.
//
// The entry returns the errors that [CosignatureV1] documents.
//
// # Allocation contract
//
// The allocation contract of CosignatureV1. The message is at most 580
// bytes.
func SubtreeV1(resolve func(pub []byte) (sign.Verifier, error)) func(note.Key) (note.Verifier, error) {
	return entry(subtreeV1, resolve)
}

// SubtreeV1Signer is a [note.Signer] whose signatures are [SubtreeV1]
// signatures of a [sign.Signer]. Each signature starts with the timestamp
// of a reading of a [clock.UTCSource], as a [CosignatureV1Signer] writes
// it. Without a UTC source the timestamp is 0, which states no time, as a
// log writes it when it signs its own checkpoint. [NewSubtreeV1Signer]
// returns a new one, and [SubtreeV1Signer.Reset] sets one in memory of the
// caller without an allocation.
//
// A SubtreeV1Signer implements [sign.ContextSigner] and
// [sign.AppendSigner]. Its Sign and AppendSign return the errors of those
// of a CosignatureV1Signer, and an error that wraps [ErrBody], classified
// [errs.Invalid], for a text that a SubtreeV1 signature cannot cover.
//
// The zero SubtreeV1Signer has no key. Its Key is the zero Key, its Verify
// reports false, and its Sign, SignContext and AppendSign return
// [note.ErrKey].
//
// # Concurrency
//
// Safe for concurrent use while no goroutine calls Reset on it.
//
// # Allocation contract
//
// The allocation contract of a CosignatureV1Signer.
type SubtreeV1Signer struct {
	cosigner
}

var (
	_ note.Signer        = (*SubtreeV1Signer)(nil)
	_ sign.ContextSigner = (*SubtreeV1Signer)(nil)
)

// NewSubtreeV1Signer returns a new [SubtreeV1Signer] for the key with
// name, type t and the public key of s, as [SubtreeV1Signer.Reset] sets
// it.
//
// Returns the errors of Reset.
//
// # Allocation contract
//
// Allocates the SubtreeV1Signer once.
func NewSubtreeV1Signer(
	name note.Name, t note.Type, s sign.Signer, utc clock.UTCSource, maxError time.Duration,
) (*SubtreeV1Signer, error) {
	c := &SubtreeV1Signer{}
	if err := c.Reset(name, t, s, utc, maxError); err != nil {
		return nil, err
	}

	return c, nil
}

// Reset sets c to the signer of the key with name, type t and the public
// key of s, whose signatures are SubtreeV1 signatures of s with the
// timestamps of readings of utc within maxError, or the timestamp 0 for a
// nil utc. The PublicKey of its Key is the slice that s returns from
// PublicKey, so c does not copy it.
//
// Returns the errors of [CosignatureV1Signer.Reset] apart from the one for
// a nil utc, and [note.ErrKey], classified [errs.Invalid], for a name
// longer than 255 bytes. For [TypeMLDSA44Cosignature], returns note.ErrKey
// for a signer whose algorithm is not ML-DSA-44, and for a signer that
// reports a context other than the empty one through a Context() string
// method, as [go.thesmos.sh/core/crypto/sign/mldsa.Signer] does. The
// method may belong to s or to a signer behind the Unwrap of s. A signer
// that does not report its context signs without the check. It leaves c
// unchanged on an error.
//
// # Allocation contract
//
// Zero-alloc.
func (c *SubtreeV1Signer) Reset(
	name note.Name, t note.Type, s sign.Signer, utc clock.UTCSource, maxError time.Duration,
) error {
	if len(name) > maxField {
		return fmt.Errorf("%w: a name of %d bytes, above 255", note.ErrKey, len(name))
	}

	if got, ok := contextOf(s); ok && t == TypeMLDSA44Cosignature && got != "" {
		return fmt.Errorf("%w: type %s signs with the empty context, and the signer with %q", note.ErrKey, t, got)
	}

	return c.reset(subtreeV1, name, t, s, utc, maxError)
}

// appendSubtreeV1 appends to b the cosigned_message of a SubtreeV1
// signature with timestamp t, by the key with the given name, over the
// body in text, and returns the extended slice.
//
// Returns b and the error of [ParseBody] for a text that is not a body,
// and an error that wraps [ErrBody] for a body that a cosigned_message
// cannot cover.
func appendSubtreeV1(b []byte, name note.Name, t uint64, text []byte) ([]byte, error) {
	l, err := splitBody(text)
	if err != nil {
		return b, err
	}

	if len(l.extensions) > 0 {
		return b, fmt.Errorf("%w: a SubtreeV1 signature covers no extension lines", ErrBody)
	}

	if l.root.Size() != crypto.DigestSize256 {
		return b, fmt.Errorf("%w: a SubtreeV1 signature covers a root of 32 bytes, not %d", ErrBody, l.root.Size())
	}

	if len(l.origin) > maxField || len(name) > maxField {
		return b, fmt.Errorf("%w: a SubtreeV1 signature covers an origin and a key name of at most 255 bytes",
			ErrBody)
	}

	if l.size == 0 && l.root != emptyRoot {
		return b, fmt.Errorf("%w: the root of an empty tree is SHA-256 of the empty string", ErrBody)
	}

	b = append(b, subtreeLabel...)
	b = append(b, byte(len(name))) //nolint:gosec // the length is at most maxField
	b = append(b, name...)
	b = binary.BigEndian.AppendUint64(b, t)
	b = append(b, byte(len(l.origin))) //nolint:gosec // the length is at most maxField
	b = append(b, l.origin...)
	b = binary.BigEndian.AppendUint64(b, 0)
	b = binary.BigEndian.AppendUint64(b, l.size)

	return append(b, l.root.Bytes()...), nil
}

// contextOf returns the FIPS 204 context string that s reports through a
// Context() string method, or that the first signer in the chain of its
// decorators reports, and reports whether one reports it. It follows each
// decorator's Unwrap() Signer or Unwrap() Verifier, as the As functions of
// [sign] do.
func contextOf(s sign.Signer) (string, bool) {
	var v sign.Verifier = s

	for {
		if c, ok := v.(interface{ Context() string }); ok {
			return c.Context(), true
		}

		switch u := v.(type) {
		case interface{ Unwrap() sign.Signer }:
			v = u.Unwrap()
		case interface{ Unwrap() sign.Verifier }:
			v = u.Unwrap()
		default:
			return "", false
		}
	}
}
