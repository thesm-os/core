// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package note

import (
	"context"

	"go.thesmos.sh/core/crypto/sign"
)

// Verifier is a [sign.Verifier] for the signatures of one note key. It
// verifies the value of a signature line, the bytes after its key ID,
// over the text of a note: Verify(text, value).
//
// An implementation reports [KeyID](k.Name, k.ID()) from KeyID,
// [Algorithm] from Algorithm, and k.PublicKey from PublicKey, where k is
// the Key that Key returns. [Resolver.Verifier] refuses an entry that
// builds a Verifier that breaks this rule. The type of the key decides the
// message that Verify checks a signature over: the note text for [Text],
// and a message built from the text for a cosignature type.
//
// # Concurrency
//
// Implementations must be safe for concurrent use.
//
// # Allocation contract
//
// KeyID, PublicKey, Algorithm and Key allocate nothing on every
// implementation of this package. Verify allocates what the algorithm
// allocates. Implementations document their own allocation behaviour.
type Verifier interface {
	sign.Verifier

	// Key returns the key whose signatures the Verifier checks. Its
	// PublicKey aliases the storage of the Verifier, and callers must
	// treat it as immutable.
	Key() Key
}

// Signer is a [Verifier] that signs note texts. Sign returns the value of
// a signature line for a text, the bytes after the key ID, and AppendSign
// appends it to a buffer of the caller. Every Signer is a
// [sign.AppendSigner], and through it a [sign.Signer].
//
// [Note.Sign] signs through AppendSign into the memory of the Note, so a
// Signer whose algorithm appends without an allocation, such as Ed25519,
// signs a reused Note without one. Every Signer of this package also
// implements [sign.ContextSigner], and none implements Unwrap: through
// Unwrap, [sign.AsStreamingSigner] would find the streaming capability of
// the wrapped algorithm, which signs the text and not the message of a
// cosignature type.
//
// # Concurrency
//
// Implementations must be safe for concurrent use.
type Signer interface {
	Verifier

	// Sign returns the value of a signature line over text.
	//
	//testkit:nondeterministic
	Sign(text []byte) ([]byte, error)

	// AppendSign appends the value of a signature line over text to dst
	// and returns the extended slice, by the rules of
	// [sign.AppendSigner]: it reads text and writes dst only until it
	// returns, and it returns dst unchanged with an error, which wraps
	// context.Cause(ctx) when ctx ends first.
	//
	//testkit:nondeterministic
	AppendSign(ctx context.Context, dst, text []byte) ([]byte, error)
}
