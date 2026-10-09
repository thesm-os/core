// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package checkpoint

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"strconv"
	"time"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sign"
	"go.thesmos.sh/core/note"
	"go.thesmos.sh/core/pool"
)

// TypeEd25519Cosignature is type 0x04, the timestamped Ed25519
// cosignature of tlog-cosignature. Its [note.Resolver] entry is
// CosignatureV1(ed25519.Resolve), and its public key is the 32-byte
// Ed25519 key.
const TypeEd25519Cosignature note.Type = "\x04"

const (
	// cosignatureHeader is the first line of the message that a
	// CosignatureV1 signature signs.
	cosignatureHeader = "cosignature/v1\n"

	// timePrefix starts the second line of that message, before the
	// timestamp in decimal.
	timePrefix = "time "

	// checkTimestamp is the timestamp of the message that CheckText
	// builds. No format refuses a text for its timestamp, so any
	// timestamp gives the answer of every other.
	checkTimestamp = 1
)

// cosignatureV1 is the format of [CosignatureV1] signatures.
var cosignatureV1 = format{message: appendCosignatureV1, assigned: TypeEd25519Cosignature, alg: crypto.AlgEd25519}

// buffers contains the scratch buffers of the package: the messages that
// a verifier and a cosigner build, the values that the Sign of a cosigner
// builds before it copies them out, and the verifier keys that
// [Policy.UnmarshalText] compares.
var buffers = pool.NewPool(func() *[]byte { return new([]byte) })

// errNoSigner is the error of a zero [CosignatureV1Signer] or
// [SubtreeV1Signer], which has no signer. It is one value, so that a call
// of a zero cosigner allocates nothing.
var errNoSigner = fmt.Errorf("%w: a cosigner without a signer", note.ErrKey)

// format is a signed message of tlog-cosignature, with the signature type
// that C2SP assigns it and the algorithm of that type.
type format struct {
	// message appends to b the message that a timestamped signature with
	// timestamp t, by the key with the given name, signs for the note text
	// text, and returns the extended slice. It returns b and an error that
	// wraps ErrBody for a text that the format cannot sign.
	message func(b []byte, name note.Name, t uint64, text []byte) ([]byte, error)

	// assigned is the signature type that C2SP assigns the format.
	assigned note.Type

	// alg is the algorithm of the assigned type.
	alg crypto.Algorithm
}

// verifier is the [note.Verifier] of a key whose signatures are
// timestamped signatures over the messages of one format. The zero
// verifier has no key, and its Verify reports false.
type verifier struct {
	// inner verifies a signature over a message.
	inner sign.Verifier

	// message appends the message that a signature signs, as
	// format.message does.
	message func(b []byte, name note.Name, t uint64, text []byte) ([]byte, error)

	// key is the key that the verifier checks. Its public key is the
	// public key that inner keeps.
	key note.Key

	// keyID is note.KeyID(key.Name, key.ID()), computed once.
	keyID sign.KeyID
}

var _ note.Verifier = (*verifier)(nil)

// Key returns the key that v checks. Its PublicKey aliases the storage of
// the Verifier of the algorithm.
func (v *verifier) Key() note.Key { return v.key }

// KeyID returns note.KeyID(name, key ID) of the key.
func (v *verifier) KeyID() sign.KeyID { return v.keyID }

// PublicKey returns the public key. It aliases the storage of the
// Verifier of the algorithm.
func (v *verifier) PublicKey() []byte { return v.key.PublicKey }

// Algorithm returns [note.Algorithm].
func (*verifier) Algorithm() crypto.Algorithm { return note.Algorithm }

// Verify reports whether value is a valid timestamped signature over the
// message of text: the timestamp in its first 8 bytes, and a signature of
// the key over the message of that timestamp and text. It reports false
// for a value shorter than 8 bytes, for a timestamp above 2^63 − 1, for a
// text that the format cannot sign, and for the zero verifier.
//
// Verify builds the message in a pooled buffer.
func (v *verifier) Verify(text, value []byte) bool {
	t, sig, ok := splitValue(value)
	if !ok || v.inner == nil {
		return false
	}

	buf := buffers.Get()
	msg, err := v.message((*buf)[:0], v.key.Name, t, text)
	ok = err == nil && v.inner.Verify(msg, sig)
	*buf = msg[:0]
	buffers.Put(buf)

	return ok
}

// reset sets v to the verifier of k, a Valid key, that verifies the
// messages of message with inner, whose public key equals the public key
// of k. It keeps the public key of inner in place of the public key of k,
// so that v does not alias the memory of the caller.
func (v *verifier) reset(
	k note.Key, inner sign.Verifier,
	message func(b []byte, name note.Name, t uint64, text []byte) ([]byte, error),
) {
	k.PublicKey = inner.PublicKey()
	v.inner, v.message, v.key, v.keyID = inner, message, k, note.KeyID(k.Name, k.ID())
}

// Cosigner is a [note.Signer] of timestamped signatures that also signs at
// a timestamp of its caller, and reports whether it signs a text. A
// witness that commits a checkpoint before it cosigns it checks each note
// with CheckText before the commit, reads one time for the commit, and
// signs after the commit with AppendSignAt at that time.
// *[CosignatureV1Signer] and *[SubtreeV1Signer] implement it.
//
// # Concurrency
//
// Implementations must be safe for concurrent use.
type Cosigner interface {
	note.Signer

	// AppendSignAt appends to dst the value of a signature line for text
	// with the timestamp of t, in whole seconds since the Unix epoch, and
	// returns the extended slice: the timestamp, then the signature of the
	// wrapped signer over the message of that timestamp and text. It does
	// not read a clock. It returns dst unchanged with an error, which
	// wraps context.Cause(ctx) when ctx ends first.
	AppendSignAt(ctx context.Context, dst, text []byte, t time.Time) ([]byte, error)

	// CheckText returns nil when AppendSignAt signs text, and the error
	// that AppendSignAt returns for a text that the format does not sign.
	// It neither signs nor reads a clock.
	CheckText(text []byte) error
}

// cosigner is the [note.Signer] of a key whose signatures are timestamped
// signatures over the messages of one format. The zero cosigner has no
// signer, and its Sign, SignContext, AppendSign, AppendSignAt and
// CheckText return [note.ErrKey].
type cosigner struct {
	// signer makes the signatures.
	signer sign.Signer

	// utc is the source of the timestamps, and nil for a cosigner that
	// writes the timestamp 0.
	utc clock.UTCSource

	verifier

	// maxError is the largest error bound of a reading that the
	// cosigner signs with.
	maxError time.Duration
}

// Sign returns the value of a signature line for text, as SignContext
// returns it with a context that never ends.
func (c *cosigner) Sign(text []byte) ([]byte, error) {
	return c.SignContext(context.Background(), text)
}

// SignContext returns the value of a signature line for text in a new
// slice of its length. It appends the value to a pooled buffer, as
// AppendSign appends it, and copies it out, so ctx bounds the wait of a
// signer behind a process boundary. It returns nil with the error of
// AppendSign.
func (c *cosigner) SignContext(ctx context.Context, text []byte) ([]byte, error) {
	buf := buffers.Get()
	defer buffers.Put(buf)

	value, err := c.AppendSign(ctx, (*buf)[:0], text)
	*buf = value[:0]

	if err != nil {
		return nil, err
	}

	return bytes.Clone(value), nil
}

// AppendSign appends the value of a signature line for text to dst: the
// timestamp of a reading of the UTC source, then the signature of the
// wrapped signer over the message of that timestamp and text, as
// appendAt appends it.
//
// Returns dst unchanged with [note.ErrKey] for the zero cosigner, with the
// error of the clock as [NewCosignatureV1Signer] documents it, with an
// error that wraps [ErrBody] for a text that the format cannot sign, and
// with the error of the signer.
func (c *cosigner) AppendSign(ctx context.Context, dst, text []byte) ([]byte, error) {
	if c.signer == nil {
		return dst, errNoSigner
	}

	t, err := readTimestamp(c.utc, c.maxError)
	if err != nil {
		return dst, err
	}

	return c.appendAt(ctx, dst, t, text)
}

// AppendSignAt appends the value of a signature line for text to dst, as
// AppendSign appends it, with the timestamp of t in whole seconds since
// the Unix epoch in place of a reading of the UTC source. It does not read
// the UTC source, so a cosigner without one signs at t too.
//
// Returns dst unchanged with [note.ErrKey] for the zero cosigner, with
// [ErrTimestamp] for a t whose whole seconds since the Unix epoch are not
// positive, because the timestamp 0 states no time, with an error that
// wraps [ErrBody] for a text that the format cannot sign, and with the
// error of the signer.
func (c *cosigner) AppendSignAt(ctx context.Context, dst, text []byte, t time.Time) ([]byte, error) {
	if c.signer == nil {
		return dst, errNoSigner
	}

	sec := t.Unix()
	if sec <= 0 {
		return dst, fmt.Errorf("%w: the time %v is not a positive number of seconds after the Unix epoch",
			ErrTimestamp, t)
	}

	return c.appendAt(ctx, dst, uint64(sec), text)
}

// CheckText returns nil when AppendSignAt signs text, and the error that
// AppendSignAt returns for a text that the format cannot sign, which wraps
// [ErrBody]. It builds the message of checkTimestamp in a pooled buffer,
// and neither signs nor reads the UTC source.
//
// Returns [note.ErrKey] for the zero cosigner.
func (c *cosigner) CheckText(text []byte) error {
	if c.signer == nil {
		return errNoSigner
	}

	buf := buffers.Get()
	msg, err := c.message((*buf)[:0], c.key.Name, checkTimestamp, text)
	*buf = msg[:0]
	buffers.Put(buf)

	return err
}

// reset sets c to the cosigner of f for the key with name, type t and the
// public key of s, with the timestamps of readings of utc within
// maxError. The public key of the key is the slice that s returns from
// PublicKey, which the [sign.Verifier] contract makes immutable, so c does
// not copy it. It leaves c unchanged on an error.
//
// Returns [note.ErrKey] for a nil s, a signer of another algorithm than
// f.alg for the type f.assigned, an invalid name, and a signer without a
// public key. Returns [note.ErrType] for an invalid type, and
// [ErrTimestamp] for a negative maxError.
func (c *cosigner) reset(
	f format, name note.Name, t note.Type, s sign.Signer, utc clock.UTCSource, maxError time.Duration,
) error {
	if s == nil {
		return fmt.Errorf("%w: a nil signer", note.ErrKey)
	}

	if !t.Valid() {
		return fmt.Errorf("%w: %s", note.ErrType, t)
	}

	if t == f.assigned && s.Algorithm() != f.alg {
		return fmt.Errorf("%w: type %s signs with %s, and the signer with %s",
			note.ErrKey, t, f.alg, s.Algorithm())
	}

	if maxError < 0 {
		return fmt.Errorf("%w: the negative error bound %v", ErrTimestamp, maxError)
	}

	k := note.Key{Name: name, Type: t, PublicKey: s.PublicKey()}
	if !k.Valid() {
		return fmt.Errorf("%w: a signer needs a valid name and a public key", note.ErrKey)
	}

	c.signer, c.utc, c.maxError = s, utc, maxError
	c.verifier.reset(k, s, f.message)

	return nil
}

// appendAt appends the value of a signature line for text with the
// timestamp t to dst: t, then the signature of the wrapped signer over the
// message of t and text, through [sign.AppendSign].
//
// It builds the message in a pooled buffer when the wrapped signer is a
// [sign.AppendSigner], which reads the message only until it returns.
// Otherwise it builds the message in a new buffer, because a signer
// behind a process boundary may still read it after it returns.
//
// It returns dst unchanged with an error that wraps [ErrBody] for a text
// that the format cannot sign, and with the error of the signer.
func (c *cosigner) appendAt(ctx context.Context, dst []byte, t uint64, text []byte) ([]byte, error) {
	if _, ok := sign.AsAppendSigner(c.signer); !ok {
		msg, err := c.message(nil, c.key.Name, t, text)
		if err != nil {
			return dst, err
		}

		return c.appendValue(ctx, dst, t, msg)
	}

	buf := buffers.Get()
	defer buffers.Put(buf)

	msg, err := c.message((*buf)[:0], c.key.Name, t, text)
	*buf = msg[:0]

	if err != nil {
		return dst, err
	}

	return c.appendValue(ctx, dst, t, msg)
}

// appendValue appends the value of a timestamped signature to dst: t,
// then the signature of the wrapped signer over msg. It returns dst
// unchanged with the error of the signer.
func (c *cosigner) appendValue(ctx context.Context, dst []byte, t uint64, msg []byte) ([]byte, error) {
	value, err := sign.AppendSign(ctx, c.signer, binary.BigEndian.AppendUint64(dst, t), msg)
	if err != nil {
		return dst, err
	}

	return value, nil
}

// CosignatureV1 returns the [note.Resolver] entry of a type whose
// signatures are timestamped signatures, by the algorithm of resolve, over
// the message of the Ed25519 cosignatures of tlog-cosignature:
//
//	cosignature/v1
//	time <timestamp>
//	<the note text>
//
// resolve is the [sign.Resolver] entry of the algorithm, such as
// ed25519.Resolve for [TypeEd25519Cosignature]. The message contains the
// whole note text, so a Verifier of the entry accepts any text: a
// checkpoint with extension lines, or a note that is not a checkpoint.
//
// The entry returns a Verifier of the key. The PublicKey of the Verifier
// and of its Key is the copy of the public key that the Verifier of the
// algorithm keeps, so the Verifier does not alias the caller's key. Its
// Verify reports false for a value shorter than 8 bytes, and for a
// timestamp above 2^63 − 1. The entry returns [note.ErrKey], classified
// [errs.Invalid], for a key that is not Valid, the error of resolve for a
// public key that resolve refuses, and [note.ErrUnknownType], classified
// [errs.Unsupported], when resolve returns neither a Verifier nor an
// error.
//
// # Allocation contract
//
// The entry allocates the Verifier and what resolve allocates: two
// allocations for Ed25519. A [note.Keyring] builds the Verifier of a key
// once. Verify allocates nothing apart from what the wrapped Verifier
// allocates, because it builds the message in a pooled buffer.
func CosignatureV1(resolve func(pub []byte) (sign.Verifier, error)) func(note.Key) (note.Verifier, error) {
	return entry(cosignatureV1, resolve)
}

// CosignatureV1Signer is a [note.Signer] whose signatures are
// [CosignatureV1] signatures of a [sign.Signer]. Each signature starts
// with the timestamp of a reading of a [clock.UTCSource], in whole seconds
// since the Unix epoch, and the signer signs only with a reading for which
// [clock.UTCReading.Within] reports true for its error bound, so the
// timestamp of each signature is within that bound of UTC.
// [NewCosignatureV1Signer] returns a new one, and
// [CosignatureV1Signer.Reset] sets one in memory of the caller, such as a
// field of a struct, without an allocation.
//
// A CosignatureV1Signer implements [Cosigner], [sign.ContextSigner] and
// [sign.AppendSigner]. Its Sign and AppendSign return an error that wraps
// [ErrClock], classified [errs.Transient], when the UTC source returns an
// error, when the reading is not within the error bound, and when the
// reading is before the Unix epoch. They return the error of the wrapped
// signer.
//
// Its AppendSignAt signs at a time of the caller without a reading, and
// returns [ErrTimestamp], classified [errs.Invalid], for a time whose
// whole seconds since the Unix epoch are not positive. A CosignatureV1
// signature covers every text, so its CheckText returns nil for every
// text.
//
// The zero CosignatureV1Signer has no key. Its Key is the zero Key, its
// Verify reports false, and its Sign, SignContext, AppendSign,
// AppendSignAt and CheckText return [note.ErrKey].
//
// # Concurrency
//
// Safe for concurrent use while no goroutine calls Reset on it.
//
// # Allocation contract
//
// AppendSign and AppendSignAt into a buffer with room for the value
// allocate nothing when the wrapped signer appends without an allocation,
// as an Ed25519 signer does, because they build the message in a pooled
// buffer. CheckText allocates nothing. Sign and SignContext allocate the
// value once, at its length, apart from what the wrapped signer
// allocates.
type CosignatureV1Signer struct {
	cosigner
}

var (
	_ Cosigner           = (*CosignatureV1Signer)(nil)
	_ note.Signer        = (*CosignatureV1Signer)(nil)
	_ sign.ContextSigner = (*CosignatureV1Signer)(nil)
)

// NewCosignatureV1Signer returns a new [CosignatureV1Signer] for the key
// with name, type t and the public key of s, as
// [CosignatureV1Signer.Reset] sets it.
//
// Returns the errors of Reset.
//
// # Allocation contract
//
// Allocates the CosignatureV1Signer once.
func NewCosignatureV1Signer(
	name note.Name, t note.Type, s sign.Signer, utc clock.UTCSource, maxError time.Duration,
) (*CosignatureV1Signer, error) {
	c := &CosignatureV1Signer{}
	if err := c.Reset(name, t, s, utc, maxError); err != nil {
		return nil, err
	}

	return c, nil
}

// Reset sets c to the signer of the key with name, type t and the public
// key of s, whose signatures are CosignatureV1 signatures of s with the
// timestamps of readings of utc within maxError. The PublicKey of its Key
// is the slice that s returns from PublicKey, which the [sign.Verifier]
// contract makes immutable, so c does not copy it.
//
// Returns [note.ErrKey], classified [errs.Invalid], for a nil s, an
// invalid name and a signer without a public key. Returns [note.ErrType],
// classified errs.Invalid, for an invalid type, and [ErrTimestamp],
// classified errs.Invalid, for a nil utc and a negative maxError. For
// [TypeEd25519Cosignature], returns note.ErrKey for a signer whose
// algorithm is not Ed25519. It leaves c unchanged on an error.
//
// # Allocation contract
//
// Zero-alloc.
func (c *CosignatureV1Signer) Reset(
	name note.Name, t note.Type, s sign.Signer, utc clock.UTCSource, maxError time.Duration,
) error {
	if utc == nil {
		return fmt.Errorf("%w: a nil UTC source", ErrTimestamp)
	}

	return c.reset(cosignatureV1, name, t, s, utc, maxError)
}

// entry returns the [note.Resolver] entry of a type whose signatures are
// timestamped signatures over the messages of f, by the algorithm of
// resolve. [CosignatureV1] documents the errors of the entry.
func entry(f format, resolve func(pub []byte) (sign.Verifier, error)) func(note.Key) (note.Verifier, error) {
	return func(k note.Key) (note.Verifier, error) {
		if !k.Valid() {
			return nil, fmt.Errorf("%w: a key needs a valid name, a valid type and a public key", note.ErrKey)
		}

		inner, err := resolve(k.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("checkpoint: resolve the key of %s: %w", k.Name, err)
		}

		if inner == nil {
			return nil, fmt.Errorf("%w: the algorithm of %s resolved no Verifier", note.ErrUnknownType, k.Type)
		}

		v := &verifier{}
		v.reset(k, inner, f.message)

		return v, nil
	}
}

// appendCosignatureV1 appends to b the message of a CosignatureV1
// signature with timestamp t over text: "cosignature/v1", "time" and the
// timestamp in decimal, each line with its newline, and then text. The
// message does not contain the key name, and the format signs every
// text.
func appendCosignatureV1(b []byte, _ note.Name, t uint64, text []byte) ([]byte, error) {
	b = append(b, cosignatureHeader...)
	b = append(b, timePrefix...)
	b = strconv.AppendUint(b, t, 10)
	b = append(b, newline)

	return append(b, text...), nil
}
