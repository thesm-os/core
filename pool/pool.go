// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package pool

import "sync"

// Pool is a typed [sync.Pool] wrapper. Generic over T so
// consumers don't lose type safety to the [any] cast required
// by the stdlib pool.
//
// Pool guarantees nothing about which T instance [Pool.Get]
// returns — callers MUST treat the returned T as potentially
// freshly allocated, or as carrying stale state from a prior
// use. For values that need their state cleared between uses,
// use [ResetPool] (which calls [Resettable.Reset] before
// caching) instead of relying on caller discipline.
//
// # Concurrency
//
// Safe for concurrent use by multiple goroutines.
//
// # Safe usage
//
// The canonical pattern is Get + defer Put, with any
// tenant-specific or request-specific state cleared at the top
// of the deferred block (or before the value is used):
//
//	v := pool.Get()
//	defer pool.Put(v)
//	// ... use v ...
//
// Do NOT leak v beyond the defer (no goroutine hand-off, no
// async buffering): once Put returns, the next Get may hand
// the same v to a different caller. Leaking v will produce
// data races and/or use-after-free.
//
// # Allocation contract
//
// Requires a pointer T. [Pool.Get] and [Pool.Put] are
// zero-allocation on the success path (when the pool has a
// cached value). [Pool.Get] allocates once via newFn when the
// pool is empty or after GC eviction.
//
// For a non-pointer T, [Pool.Put] allocates on every call — see
// the package documentation.
type Pool[T any] struct {
	p sync.Pool
}

// NewPool returns a [Pool] that creates fresh T values via
// newFn when the pool is empty.
//
// # Allocation contract
//
// Allocates once at construction (the [sync.Pool] backing).
func NewPool[T any](newFn func() T) *Pool[T] {
	return &Pool[T]{
		p: sync.Pool{New: func() any { return newFn() }},
	}
}

// Get returns a pooled or freshly created T. If the pool has a
// cached value from a previous [Pool.Put], it is returned
// directly. Otherwise the newFn supplied to [NewPool] is called
// to create a fresh instance.
//
// The returned value may contain stale state from a prior use
// (the pool stores values verbatim across [Pool.Put]). Callers
// MUST clear or overwrite any fields before using the value.
//
// # Allocation contract
//
// Zero-alloc when the pool has a cached value. One allocation
// via newFn when the pool is empty.
func (p *Pool[T]) Get() T {
	return p.p.Get().(T)
}

// Put returns v to the pool for future reuse. The pool may
// discard v if it is at capacity — Put is best-effort.
//
// Callers MUST clear any tenant-specific or request-specific
// state from v before calling Put. For values that need state
// cleared between uses, use [ResetPool] instead.
//
// # Allocation contract
//
// Zero-alloc for a pointer T. For a non-pointer T, converting v
// to the `any` that [sync.Pool] stores allocates on every call.
func (p *Pool[T]) Put(v T) {
	p.p.Put(v)
}
