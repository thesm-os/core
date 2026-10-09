// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package checkpoint implements the C2SP formats of the signed tree heads
// of a transparency log: the bodies of tlog-checkpoint, the two signed
// messages of tlog-cosignature, and the policy files of tlog-policy.
//
// A checkpoint is a [note.Note] whose text is a [Body]. The log signs the
// note, and witnesses cosign it after they verify that the tree only grew.
// A [Policy] names the logs, the witnesses and the quorum of cosignatures
// that a checkpoint needs, and a [Verifier] checks a checkpoint against
// it.
//
// # Bodies
//
// [ParseBody] reads the text of a checkpoint, and [Body.AppendText] writes
// it:
//
//	<origin>
//	<size in decimal>
//	<root in padded standard base64>
//	[<extension line>...]
//
// ParseBody accepts exactly the texts that AppendText writes. The root is
// the [crypto.Digest] that [go.thesmos.sh/core/tlog.Builder.Root] returns:
// 32 bytes for a tree over SHA-256, and 48 or 64 bytes over another
// hasher. [Body.UnmarshalText] parses into an existing Body, and reuses its
// origin and its extension lines.
//
// # Cosignatures
//
// A cosignature is a timestamped signature: an 8-byte big-endian
// timestamp in seconds since the Unix epoch, followed by a signature over
// a message that contains the timestamp. [Timestamp] reads the timestamp
// of a signature that verified. [CosignatureV1] and [SubtreeV1] build the
// [note.Resolver] entries of the two messages from the [sign.Resolver]
// entry of an algorithm:
//
//	r := note.Resolver{
//		note.TypeEd25519:                  note.Text(ed25519.Resolve),
//		checkpoint.TypeEd25519Cosignature: checkpoint.CosignatureV1(ed25519.Resolve),
//		checkpoint.TypeMLDSA44Cosignature: checkpoint.SubtreeV1(mldsa.Resolver(mldsa.MLDSA44, "")),
//	}
//
// A CosignatureV1 signature covers the whole note text, after a line with
// the format and a line with the timestamp. A SubtreeV1 signature covers a
// binary message of the key name, the timestamp, the origin, the size and
// a 32-byte root, so it covers no extension line and no larger root.
// [CosignatureV1Signer] and [SubtreeV1Signer] make the two signatures:
// [NewCosignatureV1Signer] and [NewSubtreeV1Signer] return new ones, and
// their Reset sets one in memory of the caller. A cosigner reads a
// [clock.UTCSource], and signs only with a reading within its error bound.
// A cosigner is a [sign.AppendSigner]: it builds the message in a pooled
// buffer and appends the value to the buffer of the caller, so
// [note.Note.Sign] cosigns into a reused Note.
//
// A witness that commits a checkpoint before it cosigns it signs at the
// time of its commit through the [Cosigner] interface of both cosigners.
// [Cosigner.CheckText] reports before the commit whether a cosigner signs
// a text, and [Cosigner.AppendSignAt] signs after the commit at a time of
// the caller, without a reading of the UTC source.
//
// # Policies
//
// [ParsePolicy] reads a tlog-policy file, and [Policy.UnmarshalText] reads
// one into an existing Policy, which keeps its memory when the file has
// not changed. A [Verifier] keeps one [sign.Policy] per origin, which
// requires the signature of one of the origin's log keys and the
// cosignatures of the quorum. [NewVerifier] builds one, and
// [Verifier.Reset] builds one again in its own memory for a reloaded
// policy. [Verifier.Verify] parses the body, selects the tree of its origin
// by a binary search, calls [note.Note.Check], which verifies at most one
// signature per key of the tree, and sets a Body of the caller.
// [Policy.QuorumRule] returns the quorum as a [sign.Rule] in a
// [sign.Rules] of the caller, for a caller that combines it with a log rule
// of its own.
//
// # Encoding
//
// Each type has two encodings:
//
//   - The C2SP text, which signatures cover: ParseBody and Body.AppendText
//     for a body, and ParsePolicy for a policy file.
//   - The canonical kanon codec of [Body], [Policy], [Log], [Witness] and
//     [Group], the form of these types in a kanon record. An [Origin], an
//     [Extension] and a [PolicyName] encode as strings, and the codec of a
//     struct refuses one that is not Valid. The codec does not check the
//     rules of [Policy.Valid], which Verifier.Reset and Policy.QuorumRule
//     check.
//
// # Errors
//
// Every error classifies under [errs.Classify]:
//
//   - [ErrBody], [ErrPolicy] and [ErrTimestamp], classified [errs.Invalid],
//     report a malformed body, policy or timestamp.
//   - [ErrOrigin], classified [errs.Integrity], reports a checkpoint of a
//     log that the policy does not list.
//   - [ErrClock], classified [errs.Transient], reports a cosigner whose
//     clock has no reading within its error bound.
//   - Verify returns the error of Note.Check for signatures that do not
//     satisfy the tree, which wraps [sign.ErrThreshold], classified
//     errs.Integrity.
//
// A note Verifier of this package reports false for a malformed signature
// value, as every [sign.Verifier] does.
//
// # Concurrency
//
// Every note Verifier of this package and a [Policy] that is only read
// are safe for concurrent use. A [Verifier], a [CosignatureV1Signer] and a
// [SubtreeV1Signer] are safe for concurrent use while no goroutine calls
// their Reset. A caller that reloads the policy of goroutines that verify
// resets a second Verifier and swaps the two.
//
// # Allocation contract
//
// Every operation on a checkpoint, a cosignature or a policy has a path
// without an allocation, through memory of the caller:
//
//   - The Valid methods, [Policy.Valid] included, and [Timestamp] allocate
//     nothing.
//   - Verifier.Verify allocates nothing into a Body whose extension lines
//     equal those of the checkpoint, apart from what the Verifiers of the
//     keys allocate.
//   - The Verify of a CosignatureV1 or SubtreeV1 Verifier allocates
//     nothing apart from the wrapped [sign.Verifier], because it builds its
//     message in a pooled buffer.
//   - The AppendSign and AppendSignAt of a cosigner allocate nothing into
//     a buffer with room when the wrapped signer appends without an
//     allocation, as Ed25519 does. Its Sign allocates the value once, and
//     its CheckText allocates nothing for a text that it accepts.
//   - Body.AppendText allocates nothing into a buffer with room, and
//     [Body.MarshalText] allocates the text once, at its length.
//   - Body.UnmarshalText allocates nothing into a Body of the same origin
//     and extension lines. ParseBody allocates the string of the text and
//     the slice of extension lines.
//   - Policy.UnmarshalText allocates nothing into a Policy that already
//     contains the policy of the file. ParsePolicy allocates the string of
//     the file and one slice per kind of value: six allocations for a
//     policy with groups.
//   - Verifier.Reset allocates nothing for the policy that the Verifier
//     was built from, and Policy.QuorumRule nothing into the memory of the
//     quorum before. NewVerifier allocates the memory of the Verifier at
//     the size of the policy, two allocations for the Verifier of each
//     Ed25519 key, and four for the sign.Policy of each origin: 18 for one
//     log and three witnesses.
//   - The Reset of a cosigner allocates nothing. The cosigner constructors
//     allocate the cosigner, and the Resolver entries the Verifier and what
//     the algorithm allocates.
//   - The kanon codecs follow the allocation contract of
//     [go.thesmos.sh/kanon.Message].
//
// Each benchmark of the package asserts the allocations of its operation.
//
// # Dependency position
//
// Imports bytes, context, crypto/sha256, encoding/base64, encoding/binary,
// errors, fmt, math, slices, strconv, strings, time and unicode/utf8 from
// the standard library, go.thesmos.sh/core/clock,
// go.thesmos.sh/core/crypto, go.thesmos.sh/core/crypto/sign,
// go.thesmos.sh/core/errs, go.thesmos.sh/core/note and
// go.thesmos.sh/core/pool from this module, and, in the code that kanon
// generates, io, go.thesmos.sh/kanon and go.thesmos.sh/kanon/wire.
package checkpoint
