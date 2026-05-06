// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package pool

import "sync"

// Resettable is the constraint satisfied by pooled values that
// carry state worth clearing between uses. [Resettable.Reset]
// returns the value to its zero state, erasing any
// tenant-specific or request-specific data.
//
// Types whose only state is already-immutable bytes (a
// []byte-backed arena that is overwritten from the start on
// every Get) do not need to satisfy [Resettable] and should
// use the plain [Pool] instead.
type Resettable interface {
	// Reset clears v's tenant-specific or request-specific
	// state, returning it to a usable post-zero state. Called
	// by [ResetPool.Put] before caching, and by
	// [NewResetPool] on freshly-allocated values. Must not
	// allocate on a pool that promises zero-allocation Put.
	Reset()
}

// ResetPool is a typed [sync.Pool] wrapper whose [ResetPool.Put]
// clears its argument before returning it to the backing
// pool. The "cleared between uses" discipline is enforced by
// the implementation, not by caller convention.
//
// # Why a separate type
//
// A plain [Pool] relies on the caller to Reset before Put. On
// a system serving many tenants, one forgotten Reset is a
// silent cross-tenant data leak: the next Get hands the
// pooled value to a different tenant with the prior tenant's
// bytes still in it.
//
// ResetPool moves that discipline into the implementation:
// [ResetPool.Put] calls [Resettable.Reset] unconditionally
// before returning the value to the backing pool, so the next
// [ResetPool.Get] is guaranteed to yield a cleared instance
// regardless of caller discipline.
//
// # Freshly-allocated values are also Reset
//
// The [sync.Pool] New callback may run when the backing pool
// has no cached value (either empty pool or after GC
// eviction). [ResetPool] calls Reset on the freshly-allocated
// value too, so consumers see a uniformly-cleared value
// regardless of whether Get hit the cache or fell through to
// newFn.
//
// # Concurrency
//
// Safe for concurrent use by multiple goroutines.
//
// # Allocation contract
//
// Same as [Pool]: [ResetPool.Get] and [ResetPool.Put] are
// zero-allocation on the success path. [ResetPool.Put]'s
// internal [Resettable.Reset] call must also be zero-alloc —
// that is the consumer's contract on T.Reset.
type ResetPool[T Resettable] struct {
	p sync.Pool
}

// NewResetPool returns a [ResetPool] that creates fresh T
// values via newFn when the pool is empty. The freshly-created
// value is Reset before being returned to the caller, so
// every [ResetPool.Get] yields a cleared instance regardless
// of cache hit or miss.
func NewResetPool[T Resettable](newFn func() T) *ResetPool[T] {
	return &ResetPool[T]{
		p: sync.Pool{New: func() any {
			v := newFn()
			// Normalise the freshly-allocated value to the
			// "has been Reset" state so Get's contract holds
			// uniformly across cache hit and miss.
			v.Reset()
			return v
		}},
	}
}

// Get returns a pooled or freshly created T. The returned
// value is guaranteed to be Reset (because [ResetPool.Put]
// called Reset before caching, and [NewResetPool] Resets
// freshly-allocated values).
//
// # Allocation contract
//
// Zero-alloc when the pool has a cached value. One allocation
// via newFn when the pool is empty.
func (p *ResetPool[T]) Get() T {
	return p.p.Get().(T)
}

// Put calls [Resettable.Reset] on v then returns v to the
// pool. The pool may discard v if it is at capacity — Put is
// best-effort.
//
// # Allocation contract
//
// Zero-alloc, assuming T.Reset is itself zero-alloc.
func (p *ResetPool[T]) Put(v T) {
	v.Reset()
	p.p.Put(v)
}
