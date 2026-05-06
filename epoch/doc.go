// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package epoch defines [Epoch], a strictly-monotonic 64-bit counter
// type used for leader generations, schema versions, optimistic
// concurrency tokens, and any other "before/after" tag a system
// needs to reason about ordering across time.
//
// # When to use Epoch
//
// Use [Epoch] when consumers need to compare two values and answer
// "which came first?" without consulting external state. Common
// shapes:
//
//   - Leader generations in consensus protocols. A leader claims
//     [Epoch] N; followers reject any message tagged with epoch < N.
//   - Schema migration generations. A read tagged with [Epoch] M
//     uses the M-th schema; later writes increment the epoch.
//   - Cache invalidation. A consumer's cached value carries the
//     [Epoch] at which it was loaded; a [Counter.Next] call elsewhere
//     marks the cache stale.
//   - Membership incarnations. A node restarting bumps its
//     [Epoch] so peers can distinguish the new instance from a
//     resurrected zombie.
//
// [Epoch] is NOT a wall-clock timestamp — see [go.thesmos.sh/core/clock]
// for those — and NOT a CAS token bound to a (key, scope) pair —
// see [go.thesmos.sh/core/version] for that. [Epoch] is the
// "monotonic counter" base type that other primitives compose with.
//
// # In-process scope
//
// [Epoch] expresses position in a sequence, NOT identity of a
// specific epoch instance across a distributed system. Two
// uncoordinated producers can both reach epoch 5 with no
// detectable conflict; nothing in the type guards against it.
// For cluster-wide epoch identity (where each leadership tenure
// or schema migration needs a globally-unique handle that survives
// process restarts), use a time-sortable identifier from
// [go.thesmos.sh/core/id] (ULID-shaped) and keep [Epoch] for the
// within-producer position.
//
// A typical leader composition uses both: a ULID-shaped identifier
// names the leadership tenure across the cluster; an [Epoch.Counter]
// issues sequence positions within the tenure.
//
// # Comparison with version
//
// [Epoch] and [version.Version] both express "ordering across
// time", but for different audiences:
//
//   - [Epoch] is process- or component-scoped, advanced by the
//     producer (a leader, a schema migrator, a membership
//     manager). Always uint64.
//   - [version.Version] is per-(key, scope) and opaque, computed by
//     a storage backend from whatever native representation it has.
//     A consumer never advances a [version.Version]; the storage
//     backend does.
//
// Use [Epoch] for in-process monotonic counters; use
// [version.Version] for storage CAS tokens; use a ULID-shaped
// identifier from [go.thesmos.sh/core/id] for distributed epoch
// identity.
//
// # Allocation contract
//
// [Epoch] is a value type ([uint64]); pass by value, comparable.
// [Counter] holds an [atomic.Uint64] and is pointer-allocated once
// at construction. [Counter.Next] and [Counter.Current] are
// zero-allocation atomic operations.
package epoch
