// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package crypto defines the cryptographic seams of core: hashing,
// message authentication, authenticated encryption, extendable-output
// functions and key custody.
//
// Library code computes digests through an injected [Hasher] and does not
// bind to an algorithm at compile time. Tests substitute fixed-output
// implementations, and a deployment selects SHA-256, SHA-384, SHA-512 or a
// SHA-3 variant by configuration. Receipts and entry headers persist the
// [ID] of the [Hasher] that produced each digest. A verifier then selects
// the matching implementation offline, and receipts remain verifiable
// across an algorithm rotation, such as SHA-256 to SHA3-512 under CNSA
// 2.0.
//
// # Provided implementations
//
//   - [go.thesmos.sh/core/crypto/sha256]: SHA-256 over [crypto/sha256].
//   - [go.thesmos.sh/core/crypto/sha512]: SHA-384 and SHA-512 over
//     [crypto/sha512].
//   - [go.thesmos.sh/core/crypto/sha3]: SHA3-256, SHA3-384 and SHA3-512
//     over [crypto/sha3].
//
// # Algorithm vocabulary
//
// [Algorithm] names the algorithm that produced a digest, a signature or a
// ciphertext. Each implementation reports it through [Hasher.Algorithm],
// and callers persist the same string in receipts and audit headers. The
// string is stable across builds, hosts and language ports, which a
// build-local [ID] is not.
//
// # Streaming
//
// Every [Hasher] streams through [Hasher.NewStream]. The returned [Stream]
// is an [io.Writer] that absorbs input of any length without buffering it.
// [Stream.Sum] finalises the digest, and [Stream.Reset] reuses the state
// for the next digest.
//
// # Domain separation
//
// [HashDomain] computes a domain-separated digest by streaming the
// caller's domain bytes and then the input parts. Different domain bytes
// give different digests for the same input. The caller chooses the
// domain, and this package defines no domain constants.
//
// # Key custody and erasure
//
// A [Keeper] wraps data keys under one wrapping key that the custodian
// keeps. Erasure works at two levels:
//
//   - A unit of erasure, such as a stream, a subject or a period, has its
//     own data key from [GenerateKey], stored wrapped as one object.
//     Deleting that object erases the unit. A copy of the object in a
//     backup can be read until the backup expires.
//   - [Destroyer.Destroy] destroys a wrapping key. That erases every data
//     key wrapped under it, backups included, once the destruction is
//     irreversible.
//
// Deleting an object does not remove its copies from free filesystem
// blocks or from the spare area of a flash drive. A caller that bounds
// how long deleted data can be decrypted rotates its wrapping key with a
// [KeyCreator] and destroys each previous key.
//
// Hosted custodians limit and bill keys, with a default of 100,000 per
// account and Region on AWS KMS. A wrapping key per unit of erasure does
// not scale. A wrapping key per tenant, or per rotation period, does.
//
// A process that serves more than one tenant builds one [Keeper] per tenant and
// passes each component only its tenant's Keeper. The component then has
// no way to name another tenant's key, and the custodian's access policy
// enforces the same separation for the process's credentials. A
// [KeyCreator] opens every key in its scope, so each tenant needs a
// KeyCreator with a scope of its own.
//
// # Chunked messages
//
// [AppendSealChunk] and [AppendOpenChunk] seal a message as a sequence of
// chunks, and a reader opens any chunk on its own. A [ChunkHeader] from
// [NewChunkHeader] identifies the message with a random ID and fixes its
// chunk size. Every chunk binds the header, its index, whether it is the
// last chunk and the caller's associated data, so each of these changes
// fails to open:
//
//   - A chunk moved to another index, or a dropped chunk.
//   - A message truncated or extended at a chunk boundary.
//   - A chunk of another message under the same key.
//
// [SealedSize] returns the length of a sealed chunk, so a reader computes
// the offset of any chunk without opening another.
//
// # Decorators
//
// A decorator that wraps a [Keeper], for example to add tracing,
// implements UnwrapKeeper() Keeper and returns the Keeper it wraps.
// [AsDestroyer], [AsKeyGenerator], [AsAADKeeper] and [AsKeyCreator] follow
// UnwrapKeeper to find a capability behind any number of decorators, and
// [GenerateKey] uses AsKeyGenerator. The method is not named Unwrap,
// because [Keeper.Unwrap] unwraps a data key.
//
// # Allocation contract
//
// [Hasher.ID], [Hasher.Algorithm], [Hasher.Hash] and [Hasher.CombineTagged]
// do not allocate on any implementation in this module, and
// [Hasher.HashTagged] does not allocate on the warm path.
// [Hasher.NewStream] allocates the hash state once. [Stream.Write],
// [Stream.Sum] and [Stream.Reset] do not allocate afterwards. [Digest],
// [ID] and [Algorithm] are value types, passed by value.
//
// # Failure semantics
//
// The package separates two classes of failure:
//
//   - Runtime errors are returned. They include entropy exhaustion, I/O
//     faults, network failures and any other failure that the environment
//     causes. [HashReader] wraps [io.Reader] failures with package
//     context. [AEAD] and [Keeper] return their runtime errors too.
//   - Precondition violations panic. They are programmer errors with no
//     legitimate runtime cause, such as [Hasher.CombineTagged] with a
//     [Digest] whose [Digest.Size] does not match the hasher's output
//     size, or with a [Role] from the wrong arity half. A wrong digest in
//     an audit chain returns no error where the defect is. A panic fails
//     the first test that exercises the defect.
//
// The zero [Digest] is not a valid operand. A chain's first link is a
// unary [Role] over one operand, so no chain needs a zero sentinel. See
// [Digest.IsZero].
//
// The Go standard library uses the same split. I/O packages return
// errors, while [encoding/binary], [crypto/cipher], [sync.Mutex] and the
// slice and string operators panic on invariants that the programmer
// supplies.
package crypto
