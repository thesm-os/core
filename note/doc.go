// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package note implements C2SP signed-note v1: a text signed by one or
// more keys, the verifier keys that name those keys, and the signature
// types that select their algorithms.
//
// A note key is a [sign.Verifier], and a signature line converts to a
// [sign.Signature], so a [sign.Policy] of note keys verifies a note: one
// key, every key of a hybrid signer, or a quorum of nested groups.
// [sign.Policy.Check] verifies at most one line per key of the policy, and
// none when the lines cannot satisfy it.
//
// # Keys and types
//
// A [Key] is a key [Name], a signature [Type] and public key material.
// [ParseKey] reads the verifier key form
//
//	<name>+<hex key ID>+<base64(type ‖ public key)>
//
// and [Key.AppendText] writes it. [Key.Set] parses it into a Key of the
// caller, so a Key is a [flag.Value], and [Key.UnmarshalText] parses it
// from a configuration. The key ID is the first four bytes of
// SHA-256(name ‖ 0x0A ‖ type ‖ public key). [TypeEd25519] is the type of an
// Ed25519 signature over the note text. [NewType] encodes a type that
// signed-note assigns no byte as 0xff, a length byte and an identifier.
//
// # Identity
//
// [KeyID] maps a key name and a key ID to a [sign.KeyID]: the key ID in
// its first four bytes, then 12 bytes of SHA-256 of the name. A signature
// line names its key by the same pair, so [Note.Check] converts each line
// to a [sign.Signature] without a table of keys, and the policy finds the
// key of each line in its own index. Every note [Verifier] reports
// [Algorithm], and the type of its key selects the algorithm that it runs.
//
// # Resolution
//
// A [Resolver] maps a signature type to a function that builds the
// [Verifier] of a key of that type. The caller writes it as a map literal
// of the types it trusts. There is no global registry and there are no
// default entries, so a verifier key selects only a type that the caller
// listed. [Text] builds the entry of a type whose signatures cover the
// note text, from the [sign.Resolver] entry of an algorithm:
//
//	r := note.Resolver{note.TypeEd25519: note.Text(ed25519.Resolve)}
//
// A [Keyring] resolves the keys of a configuration through a Resolver, and
// keeps each Verifier for the next load of the configuration. A process
// that reloads its configuration builds the Verifier of a key once.
//
// # Verifying
//
// [Parse] splits a note into its text and its signature lines, and
// verifies nothing. [Note.Check] verifies the lines against a
// [sign.Policy], and [Open] does both. The text of a note is untrusted
// until Check returns nil. [Note.Find] returns the line of one key, for a
// caller that verifies the signature of that key itself. [TextOf] returns
// the text of a note without building a Note, for a caller that hashes or
// compares texts, and checks the note as Parse does.
//
// Check verifies the signature of a known key only when the result of the
// policy depends on it. signed-note asks a verifier to reject a note when
// the signature of a known key fails. Check accepts such a note when the
// policy is met without that key, because a failing signature never makes
// a note valid.
//
// # Signing
//
// A [TextSigner] is a [Signer] over a [sign.Signer]. [NewTextSigner]
// returns a new one, and [TextSigner.Reset] sets one in memory of the
// caller. [Sign] signs a text with one or more signers and returns a new
// [Note]. [Note.Sign] signs
// into an existing Note and reuses its lines and the value of each line, so
// a witness that cosigns the checkpoints of one log signs each into the
// same Note. A log formats the whole note with [Note.AppendText], and a
// witness formats only its own lines with [Signature.AppendText].
//
// Every Signer is a [sign.AppendSigner]: Note.Sign appends each signature
// to the memory of the Note through [Signer.AppendSign]. Every Signer of
// this package also implements [sign.ContextSigner], so the context of
// Sign bounds a signer behind a process boundary.
//
// # Encoding
//
// Each type has two encodings:
//
//   - The C2SP text, which signatures cover. Parse accepts exactly the
//     notes that Note.AppendText writes, and ParseKey accepts exactly the
//     keys that Key.AppendText writes. A note is valid UTF-8 without a
//     character below U+0020 other than newline. It may contain U+007F and
//     U+0080 to U+009F. Signature values and verifier keys use padded
//     standard base64 without spare bits, and key IDs lowercase
//     hexadecimal.
//   - The canonical kanon codec of [Key], [Signature] and [Note], the form
//     of these types in a kanon record. A Name and a Type encode as
//     strings, and the codec of a struct refuses one that is not Valid. A
//     kanon decode keeps the bytes of the text, so Note.AppendText writes
//     the signed text of a decoded Note again.
//
// # Bounds
//
// Parse costs time and memory linear in the length of a note, and sets no
// limit on its signature lines. The caller bounds the bytes that it reads.
// Check verifies at most one line per key of the policy, whatever the
// number of lines.
//
// # Errors
//
// Every error classifies under [errs.Classify]:
//
//   - [ErrNote], [ErrKey] and [ErrType], classified [errs.Invalid], report a
//     malformed note, key or type.
//   - [ErrUnknownType], classified [errs.Unsupported], reports a type that a
//     [Resolver] cannot resolve.
//   - Check returns the error of [sign.Policy.Check] for lines that do not
//     satisfy the policy, which wraps [sign.ErrThreshold], classified
//     [errs.Integrity].
//
// # Concurrency
//
// Every [Verifier] and [Signer] of this package, and a [Resolver] that is
// only read, are safe for concurrent use. A [TextSigner] is safe while no
// goroutine calls its Reset. A [Note] and a [Key] are values, safe to
// share while no goroutine changes them. A [Keyring] is not safe for
// concurrent use.
//
// # Allocation contract
//
// Every operation on a note, a line or a key has a path without an
// allocation, through memory of the caller:
//
//   - The Valid methods, [Key.ID], [KeyID], [Note.Find], [Type.String] of a
//     Valid type and the Verify of every Verifier of this package allocate
//     nothing, apart from what the wrapped [sign.Verifier] allocates.
//   - The AppendText methods allocate nothing into a buffer with room.
//     The MarshalText methods and [Key.String] allocate the text once.
//   - [Note.Check] allocates nothing for a note of at most 16 lines, apart
//     from what sign.Policy.Check allocates.
//   - [Note.UnmarshalText] allocates nothing into a Note that contains a
//     note of the same keys, such as the previous checkpoint of one log.
//     [Key.Set] and [Key.UnmarshalText] allocate nothing into a Key that
//     contains the same key. [Parse] allocates three times, whatever the
//     number of lines, and [ParseKey] once.
//   - [TextOf] allocates nothing for a note that it accepts, whatever its
//     key names and its number of lines.
//   - [Note.Sign] and the AppendSign of a Signer allocate nothing into
//     memory with room when the algorithm appends without an allocation,
//     as Ed25519 does. Sign allocates the lines and each value.
//   - [TextSigner.Reset] allocates nothing, and NewTextSigner allocates the
//     TextSigner.
//   - The [Text] entries and [Resolver.Verifier] allocate the Verifier of a
//     key and what the algorithm allocates: two allocations for Ed25519.
//     [Keyring.Verifier] returns the Verifier that it keeps for a key
//     without an allocation.
//   - The kanon codecs follow the allocation contract of
//     [go.thesmos.sh/kanon.Message].
//
// Each benchmark of the package asserts the allocations of its operation.
//
// # Dependency position
//
// Imports bytes, cmp, context, crypto/sha256, encoding/base64,
// encoding/binary, encoding/hex, errors, fmt, slices, strings, unicode and
// unicode/utf8 from
// the standard library, go.thesmos.sh/core/crypto,
// go.thesmos.sh/core/crypto/sign, go.thesmos.sh/core/errs and
// go.thesmos.sh/core/pool from this module, and, in the code that kanon
// generates, io, go.thesmos.sh/kanon and go.thesmos.sh/kanon/wire.
package note
