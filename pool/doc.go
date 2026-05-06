// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package pool provides typed [sync.Pool] wrappers for hot-path
// allocation pressure relief.
//
// # When to use
//
// Use a pool wherever the same shape of allocation happens at
// high frequency: scratch byte buffers, decoded payload structs,
// retry contexts, codec arenas. Profile first — pooling adds
// complexity, and the Go runtime's allocator is fast.
//
// # Provided types
//
//   - [Pool] — typed [sync.Pool] wrapper. No auto-Reset; the
//     caller clears state before [Pool.Put].
//   - [Resettable] — constraint for values whose state must be
//     cleared between uses.
//   - [ResetPool] — typed [sync.Pool] wrapper that auto-Resets
//     on [ResetPool.Put]. Prevents cross-tenant data leaks at
//     the type level.
//   - [NewBufferPool] — convenience constructor for the
//     [bytes.Buffer] specialization (the canonical example of a
//     [Resettable] pooled value).
//
// # Concurrency
//
// All types in this package are safe for concurrent use by
// multiple goroutines. The underlying [sync.Pool] is
// concurrency-safe; the wrappers add no shared state.
//
// # Allocation contract
//
// [Pool.Get] / [Pool.Put] / [ResetPool.Get] / [ResetPool.Put]
// are zero-allocation on the success path (when the pool has a
// cached value). [Pool.Get] / [ResetPool.Get] allocate via the
// caller-supplied newFn when the pool is empty or after
// garbage collection has evicted cached values.
package pool
