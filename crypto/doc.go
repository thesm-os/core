// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package crypto defines the cryptographic seams used by every
// thesmos library that constructs digests, audit chains, Merkle
// accumulators, or content addresses.
//
// The seam exists so library code can compute hashes through an
// injected [Hasher] without binding to a specific algorithm at
// compile time. Tests substitute fixed-output implementations,
// production deployments choose between SHA-256, SHA-384,
// SHA-512, or any of the SHA-3 variants by configuration.
// Receipts and entry headers persist the [ID] of the [Hasher]
// that produced each digest, so verifiers can pick the matching
// implementation offline and so receipts survive algorithm
// rotation (e.g. SHA-256 → SHA3-512 under CNSA 2.0).
//
// # Provided implementations
//
//   - [sha256] — SHA-256 backed by [crypto/sha256].
//   - [sha512] — SHA-384 and SHA-512 backed by [crypto/sha512].
//   - [sha3] — SHA3-256, SHA3-384, SHA3-512 backed by [crypto/sha3].
//
// # Algorithm vocabulary
//
// [Algorithm] is an open-string vocabulary type that names the
// algorithm a digest, signature, or ciphertext was produced
// with. Each implementation reports its [Algorithm] via
// [Hasher.Algorithm] and persists the same string into receipts
// and audit headers. The string is the long-term identifier —
// across builds, hosts, language ports — and is more durable
// than a build-local [ID].
//
// # Streaming
//
// Every [Hasher] supports streaming via [Hasher.NewStream]. The
// returned [Stream] is an [io.Writer] that absorbs input of any
// length without buffering it. [Stream.Sum] finalises the digest.
// [Stream.Reset] makes the state reusable across many hashes for
// amortised allocation.
//
// # Domain separation
//
// [HashDomain] computes a domain-separated digest by streaming
// the caller-supplied domain bytes followed by the input parts.
// Different domain bytes guarantee non-colliding digests for
// otherwise-identical inputs. The domain string is the caller's
// concern; this package ships no domain constants.
//
// # Key custody and erasure
//
// A [Keeper] wraps data keys under one wrapping key that the custodian
// keeps. Erasure works at two grains, and neither needs more than one
// wrapping key per tenant:
//
//   - A unit of erasure, such as a stream, a subject or a period, has
//     its own data key from [GenerateKey], stored wrapped as one
//     object. Deleting that object erases the unit. Backups that
//     contain the object keep it readable until they expire, so the
//     erasure is complete when the last of them has expired.
//   - A tenant has one wrapping key. [Destroyer.Destroy] erases every
//     data key wrapped under it, backups included, once the
//     destruction is irreversible.
//
// Hosted custodians limit the number of keys, 100,000 per account and
// region by default on AWS KMS, so a wrapping key per unit does not
// scale. Creating a wrapping key is a provisioning task, and the seam
// has no method for it.
//
// A process that serves several tenants builds one [Keeper] per tenant
// and gives each component only its tenant's Keeper, when the component
// is built. The component then has no way to name another tenant's
// key, and the custodian's access policy enforces the same separation
// for the process's credentials.
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
// [AsDestroyer] and [AsKeyGenerator] follow UnwrapKeeper to find a
// capability behind any number of decorators, and [GenerateKey] uses
// AsKeyGenerator. The method is not named Unwrap, because
// [Keeper.Unwrap] unwraps a data key.
//
// # Allocation contract
//
// [Hasher.ID], [Hasher.Algorithm], [Hasher.Hash], and
// [Hasher.CombineTagged] are zero-allocation on every
// implementation in this module, and [Hasher.HashTagged] is on the
// warm path. [Hasher.NewStream] allocates the underlying
// hash state once; [Stream.Write], [Stream.Sum], and
// [Stream.Reset] are zero-allocation thereafter. [Digest], [ID],
// and [Algorithm] are value types passed by value.
//
// # Failure semantics
//
// The package separates two classes of failure:
//
//   - Runtime errors are returned. They include entropy exhaustion,
//     I/O faults, network failures and anything else the environment
//     causes. [HashReader] wraps [io.Reader] failures with package
//     context. [AEAD] and [Keeper] return their runtime errors too.
//   - Precondition violations panic. They are programmer errors with
//     no legitimate runtime cause. The canonical example is
//     [Hasher.CombineTagged] called with a [Digest] whose
//     [Digest.Size] does not match the hasher's output size, or with a
//     [Role] from the wrong arity half. A wrong digest in an audit
//     chain returns no error where the defect is. A panic fails the
//     first test that exercises the defect.
//
// There is no admitted exception. The zero [Digest] is not a valid
// operand. A chain's first link is a unary [Role] over one operand, so
// no chain needs a zero sentinel. See [Digest.IsZero].
//
// The Go standard library uses the same split. I/O packages return
// errors, while [encoding/binary], [crypto/cipher], [sync.Mutex] and
// the slice and string operators panic on programmer-supplied
// invariants.
package crypto
