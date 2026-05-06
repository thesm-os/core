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
// Future seams (signing, AEAD, KEM) will land alongside these in
// the same package family as their consumer use cases emerge.
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
// returned [Stream] is an [io.Writer] that absorbs arbitrarily
// large inputs without holding them in memory; [Stream.Sum]
// finalises the digest. [Stream.Reset] makes the state reusable
// across many hashes for amortised allocation.
//
// # Domain separation
//
// [HashDomain] computes a domain-separated digest by streaming
// the caller-supplied domain bytes followed by the input parts.
// Different domain bytes guarantee non-colliding digests for
// otherwise-identical inputs. The domain string is the caller's
// concern; this package ships no domain constants.
//
// # Allocation contract
//
// [Hasher.ID], [Hasher.Algorithm], [Hasher.Hash], and
// [Hasher.Combine] are zero-allocation on every implementation
// in this module. [Hasher.NewStream] allocates the underlying
// hash state once; [Stream.Write], [Stream.Sum], and
// [Stream.Reset] are zero-allocation thereafter. [Digest], [ID],
// and [Algorithm] are value types passed by value.
//
// # Failure semantics
//
// Two failure classes, two disciplines:
//
//   - Runtime errors — entropy exhaustion, IO faults, network
//     failures, anything caused by the environment — are
//     returned through error channels. [HashReader] wraps
//     [io.Reader] failures with package context; future seams
//     (signing, AEAD, KEM) follow the same shape.
//   - Precondition violations — programmer errors that have no
//     legitimate runtime cause — panic. The canonical example is
//     [Hasher.Combine] called with a [Digest] whose [Digest.Size]
//     does not match the hasher's output size. Returning a
//     silently-wrong digest is the worst possible failure mode
//     for an audit-chain primitive; panic converts it to an
//     immediate, unmissable test failure that the offending
//     change cannot ship.
//
// This split matches the Go standard library: I/O packages
// return errors; [encoding/binary], [crypto/cipher],
// [sync.Mutex], and the slice/string operators panic on
// programmer-supplied invariants.
package crypto
