// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package epoch defines [Epoch], a strictly-monotonic 64-bit counter for
// leader generations, schema versions, optimistic-concurrency tokens and
// any other tag that orders events in time.
//
// # When to use Epoch
//
// Use an [Epoch] to order two events without consulting other state:
//
//   - Leader generations in a consensus protocol. A leader claims [Epoch]
//     N, and followers reject a message tagged with an epoch below N.
//   - Schema generations. A read tagged with [Epoch] M uses the M-th
//     schema, and a migration increments the epoch.
//   - Cache invalidation. A cached value records the [Epoch] at which it
//     was loaded, and a [Counter.Next] call elsewhere marks it stale.
//   - Membership incarnations. A node that restarts increments its
//     [Epoch], so that its peers can tell the new instance from an old
//     one.
//
// An [Epoch] is not a wall-clock timestamp, which
// [go.thesmos.sh/core/clock] provides, and not a compare-and-swap token
// for a key, which [go.thesmos.sh/core/version] provides.
//
// # Issuing and awaiting positions
//
// [Counter] issues sequence positions. [EventCount] publishes progress
// through them, for example the highest durable position of a log.
// [EventCount.Wait] blocks until the count is at least a target, the
// context is done, or the count fails.
//
// # Fencing
//
// [Admissible] and [Watermark] admit a write whose fence epoch is at or
// above the watermark of its scope. [ErrFenced] reports a write whose
// authority was revoked.
//
// # In-process scope
//
// An [Epoch] is a position in a sequence, not the identity of an epoch
// across a distributed system. Two uncoordinated producers can both issue
// epoch 5, and nothing in the type detects it. For an identity across a
// cluster, such as a leadership tenure that must persist across a
// restart, use a time-sortable identifier from [go.thesmos.sh/core/id],
// and keep the [Epoch] for the position within one producer. A leader
// often uses both: an identifier for the tenure across the cluster, and a
// [Counter] for the positions within the tenure.
//
// # Comparison with version
//
// An [Epoch] and a [go.thesmos.sh/core/version.Version] both order
// events in time, for different callers:
//
//   - An [Epoch] belongs to a process or a component, and its producer
//     advances it, such as a leader, a schema migrator or a membership
//     manager. It is always a uint64.
//   - A [go.thesmos.sh/core/version.Version] belongs to one key and is
//     opaque. A storage backend computes it from its own representation,
//     and a caller never advances it.
//
// Use an [Epoch] for an in-process monotonic counter, a
// [go.thesmos.sh/core/version.Version] for a compare-and-swap token of
// storage, and an identifier from [go.thesmos.sh/core/id] for an identity
// across a distributed system.
//
// # Allocation contract
//
// [Epoch] is a comparable value type over uint64. [NewCounter] and
// [NewWatermark] allocate once. The methods of [Counter] and [Watermark]
// are zero-alloc atomic operations. [EventCount.Current] and
// [EventCount.Wait] do not allocate. [EventCount.Advance] allocates one
// channel when a waiter took the current one.
package epoch
