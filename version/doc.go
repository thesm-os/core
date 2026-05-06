// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package version defines [Version], the opaque, monotonic
// per-(scope, key) version token used by reads and writes for
// read-your-writes and compare-and-swap semantics.
//
// [Version] is the foundation's ETag analogue. Stores compute it
// from whatever they have natively — a row version, a content
// hash, an HLC timestamp, an MVCC snapshot ID. Callers treat it as
// opaque: pass-through and bytewise-compare only.
//
// # Comparison with epoch
//
// [Version] and [go.thesmos.sh/core/epoch.Epoch] both express
// "ordering across time", but for different audiences:
//
//   - [Version] is per-(key, scope), opaque, computed by the
//     storage backend from its native representation.
//   - [Epoch] is process-scoped, transparent (uint64), advanced
//     by the producer.
//
// Use [Version] for storage CAS tokens; use [Epoch] for in-process
// monotonic counters.
//
// # Allocation contract
//
// [Version] is a string-typed value; pass by value. Construction,
// comparison, and the [IsZero] / [IsWildcard] predicates are
// zero-alloc. [WriteOptions] is a value type; pass by value.
// [Versioned] is a generic value type; the underlying value
// follows the consumer's allocation behaviour.
package version
