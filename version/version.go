// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package version

// Version is an opaque, monotonic per-(scope, key) version token
// returned by reads and accepted by writes for read-your-writes
// and compare-and-swap semantics.
//
// Version is the foundation's ETag analogue. Stores compute it
// from whatever they have natively — a row version, a content
// hash, an HLC timestamp, an MVCC snapshot ID. Callers treat it as
// opaque: pass-through and bytewise-compare only.
//
// The empty string is reserved ([Unspecified]). When passed in
// [WriteOptions.IfMatch], an empty Version means "unconditional";
// in [WriteOptions.IfNoneMatch], an empty Version means "no
// precondition".
//
// # Encoding contract
//
// Implementations MUST encode their native version representation
// to the [Version] string deterministically and losslessly:
//
//   - Deterministic: the same internal version always produces
//     byte-identical [Version] output. CAS comparisons are
//     bytewise; any non-determinism (timestamp formatting, map
//     iteration order, locale-dependent number formatting)
//     silently breaks IfMatch correctness.
//   - Lossless: the encoded [Version] round-trips back to the
//     same internal version on parse. Versions returned to
//     callers and later supplied as IfMatch must select the exact
//     same historical state — losing precision (for example
//     truncating a vector clock to a single counter) silently
//     breaks linearizability under concurrent writes.
//
// Implementations that wrap structured native versions (vector
// clocks, MVCC snapshot IDs with multiple components) typically
// encode as canonical JSON or fixed-width hex; bare counters
// encode as decimal; content hashes encode as their canonical
// hex / base64 form.
//
// # High-water-mark / ABA prevention
//
// Versions MUST be globally unique within a (scope, key) pair
// across ALL TIME, including deletion cycles. Concretely:
//
//   - If key K is created (Version v1), deleted, then recreated,
//     the recreated key's first [Version] v2 MUST NOT equal v1
//     or any [Version] K ever held before deletion.
//   - Implementations using monotonic counters (Postgres xmin,
//     etcd revision, Spanner commit-timestamp) satisfy this
//     naturally because the counter never resets.
//   - Implementations using content hashes satisfy this as long
//     as the recreated value differs from the deleted value
//     (typical).
//   - Implementations using random UUIDs per write satisfy this
//     probabilistically (collision probability negligible).
//
// The requirement prevents the ABA problem: a stalled worker
// holding a [Version] from before deletion attempts a conditional
// write (IfMatch: v1) against the recreated key. Because v2 ≠ v1,
// the write correctly fails with [ErrMismatch]. Without this
// requirement, the stalled worker could silently clobber the
// resurrected state — a correctness-critical bug in distributed
// systems where workers may outlive the keys they cached.
type Version string

// Unspecified is the reserved zero value, meaning "no version" /
// "no precondition" depending on the field that carries it.
const Unspecified Version = ""

// Wildcard is the conventional [WriteOptions.IfNoneMatch] value
// meaning "any version" — a write conditioned on Wildcard is a
// "create if absent" write.
const Wildcard Version = "*"

// IsZero reports whether v is the [Unspecified] zero value.
func (v Version) IsZero() bool {
	return v == Unspecified
}

// IsWildcard reports whether v is the [Wildcard] sentinel.
func (v Version) IsWildcard() bool {
	return v == Wildcard
}
