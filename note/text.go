// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package note

import (
	"context"
	"fmt"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sign"
)

// errNoSigner is the error of a zero [TextSigner], which has no signer.
// It is one value, so that a call of a zero TextSigner allocates nothing.
var errNoSigner = fmt.Errorf("%w: a TextSigner without a signer", ErrKey)

// Text returns the [Resolver] entry of a signature type whose signatures
// are the signatures of an algorithm over the note text. resolve is the
// [sign.Resolver] entry of the algorithm, such as ed25519.Resolve for
// [TypeEd25519], or mldsa.Resolver(mldsa.MLDSA87, context) for a type
// without an assigned byte.
//
// The entry returns a Verifier of the key. The PublicKey of the Verifier
// and of its Key is the copy of the public key that the Verifier of the
// algorithm keeps, so the Verifier does not alias the caller's key. It
// returns [ErrKey], classified [errs.Invalid], for a key that is not
// Valid, the error of resolve for a public key that resolve refuses, and
// [ErrUnknownType], classified [errs.Unsupported], when resolve returns
// neither a Verifier nor an error.
//
// # Allocation contract
//
// The entry allocates the Verifier and what resolve allocates: two
// allocations for Ed25519. A [Keyring] builds the Verifier of a key once.
func Text(resolve func(pub []byte) (sign.Verifier, error)) func(Key) (Verifier, error) {
	return func(k Key) (Verifier, error) {
		if !k.Valid() {
			return nil, fmt.Errorf("%w: a key needs a valid name, a valid type and a public key", ErrKey)
		}

		inner, err := resolve(k.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("note: resolve the key of %s: %w", k.Name, err)
		}

		if inner == nil {
			return nil, fmt.Errorf("%w: the algorithm of %s resolved no Verifier", ErrUnknownType, k.Type)
		}

		v := &textVerifier{}
		v.reset(k, inner)

		return v, nil
	}
}

// TextSigner is a [Signer] whose signatures are the signatures of a
// [sign.Signer] over the note text, such as the signatures of type 0x01.
// [NewTextSigner] returns a new one, and [TextSigner.Reset] sets one in
// memory of the caller, such as a field of a struct, without an
// allocation.
//
// The zero TextSigner has no key. Its Key is the zero Key, its Verify
// reports false, and its Sign, SignContext and AppendSign return [ErrKey].
//
// # Concurrency
//
// Safe for concurrent use while no goroutine calls Reset on it.
type TextSigner struct {
	// signer makes the signatures.
	signer sign.Signer

	textVerifier
}

var _ Signer = (*TextSigner)(nil)

// NewTextSigner returns a new [TextSigner] for the key with name, type t
// and the public key of s, as [TextSigner.Reset] sets it.
//
// Returns the errors of Reset.
//
// # Allocation contract
//
// Allocates the TextSigner once.
func NewTextSigner(name Name, t Type, s sign.Signer) (*TextSigner, error) {
	ts := &TextSigner{}
	if err := ts.Reset(name, t, s); err != nil {
		return nil, err
	}

	return ts, nil
}

// Reset sets ts to the signer of the key with name, type t and the public
// key of s, whose signatures are the signatures of s over the note text.
// The PublicKey of its Key is the slice that s returns from PublicKey,
// which the [sign.Verifier] contract makes immutable, so ts does not copy
// it.
//
// Returns [ErrKey], classified [errs.Invalid], for a nil s, an invalid
// name and a signer without a public key, and [ErrType], classified
// errs.Invalid, for an invalid type. For [TypeEd25519], returns ErrKey for
// a signer whose algorithm is not Ed25519. It leaves ts unchanged on an
// error.
//
// # Allocation contract
//
// Zero-alloc.
func (ts *TextSigner) Reset(name Name, t Type, s sign.Signer) error {
	if s == nil {
		return fmt.Errorf("%w: a nil signer", ErrKey)
	}

	if !t.Valid() {
		return fmt.Errorf("%w: %s", ErrType, t)
	}

	if t == TypeEd25519 && s.Algorithm() != crypto.AlgEd25519 {
		return fmt.Errorf("%w: type %s signs with Ed25519, and the signer with %s", ErrKey, t, s.Algorithm())
	}

	k := Key{Name: name, Type: t, PublicKey: s.PublicKey()}
	if !k.Valid() {
		return fmt.Errorf("%w: a signer needs a valid name and a public key", ErrKey)
	}

	ts.signer = s
	ts.reset(k, s)

	return nil
}

// Sign returns the signature of the signer over text.
//
// Returns [ErrKey] for the zero TextSigner, and the error of the signer.
func (ts *TextSigner) Sign(text []byte) ([]byte, error) {
	if ts.signer == nil {
		return nil, errNoSigner
	}

	return ts.signer.Sign(text)
}

// SignContext returns the signature of the signer over text, through
// [sign.SignContext], so ctx bounds a signer behind a process boundary.
//
// Returns [ErrKey] for the zero TextSigner, and the error of
// sign.SignContext.
func (ts *TextSigner) SignContext(ctx context.Context, text []byte) ([]byte, error) {
	if ts.signer == nil {
		return nil, errNoSigner
	}

	return sign.SignContext(ctx, ts.signer, text)
}

// AppendSign appends the signature of the signer over text to dst,
// through [sign.AppendSign]: without an allocation for a signer that
// appends without one, such as Ed25519, and through SignContext and a
// copy otherwise.
//
// Returns dst unchanged with [ErrKey] for the zero TextSigner, and with
// the error of sign.AppendSign.
func (ts *TextSigner) AppendSign(ctx context.Context, dst, text []byte) ([]byte, error) {
	if ts.signer == nil {
		return dst, errNoSigner
	}

	return sign.AppendSign(ctx, ts.signer, dst, text)
}

// textVerifier verifies the signatures of a key whose type signs the note
// text, with the [sign.Verifier] of its algorithm.
type textVerifier struct {
	// inner verifies a signature over the note text.
	inner sign.Verifier

	// key is the key that the Verifier checks. Its public key is the
	// public key that inner keeps.
	key Key

	// keyID is KeyID(key.Name, key.ID()), computed once.
	keyID sign.KeyID
}

// reset sets v to the Verifier of k, a Valid key, that verifies with
// inner, whose public key equals the public key of k. It keeps the
// public key of inner in place of the public key of k, so that v does not
// alias the memory of the caller.
func (v *textVerifier) reset(k Key, inner sign.Verifier) {
	k.PublicKey = inner.PublicKey()
	v.inner, v.key, v.keyID = inner, k, KeyID(k.Name, k.ID())
}

// Key returns the key that v checks. Its PublicKey aliases the storage of
// the Verifier of the algorithm.
func (v *textVerifier) Key() Key { return v.key }

// KeyID returns KeyID(name, key ID) of the key.
func (v *textVerifier) KeyID() sign.KeyID { return v.keyID }

// PublicKey returns the public key. It aliases the storage of the
// Verifier of the algorithm.
func (v *textVerifier) PublicKey() []byte { return v.key.PublicKey }

// Algorithm returns [Algorithm].
func (*textVerifier) Algorithm() crypto.Algorithm { return Algorithm }

// Verify reports whether value is a valid signature over text under the
// key. It reports false for the zero TextSigner, which has no key.
func (v *textVerifier) Verify(text, value []byte) bool {
	return v.inner != nil && v.inner.Verify(text, value)
}
