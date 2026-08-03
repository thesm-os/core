// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package pool

import (
	"context"
	"sync"
)

// Bounded is a fixed-capacity pool that never evicts.
//
// Unlike [Pool], which is backed by [sync.Pool] and may drop cached
// values under GC pressure, Bounded holds exactly what it is given up
// to its limit. Use it when the pooled thing is a scarce resource
// rather than an allocation optimisation — a connection, a licence, a
// buffer held against a hard memory budget — where the capacity IS
// the contract.
//
// The distinction matters because [sync.Pool]'s eviction is wrong in
// exactly the case that needs a bound: it drops values under memory
// pressure and recreates them on demand, so the limit is exceeded
// precisely when resources are already tight.
//
// # Deadlock
//
// [Bounded.Get] blocks, so a caller holding one value and waiting for
// a second from the same pool deadlocks once the limit is reached and
// every holder is doing the same. Take one value at a time, or size
// the pool above the maximum a single caller holds.
//
// # Concurrency
//
// Safe for concurrent use.
type Bounded[T any] struct {
	// free holds values returned by Put. Its capacity is the pool's
	// limit, which is what makes Put non-blocking: at most limit
	// values exist, so there is always a slot for one coming back.
	free chan T

	newFn func() T

	// mu guards created, which must not exceed limit. A mutex rather
	// than an atomic because the check and the increment have to be
	// one step — two callers reading limit-1 concurrently would each
	// decide to construct.
	mu      sync.Mutex
	created int
	limit   int
}

// NewBounded returns a pool holding at most limit values.
//
// Returns [ErrLimit] unless limit is positive: a pool that can hold
// nothing would block every [Bounded.Get] forever, which is a
// configuration error worth surfacing at wiring time.
//
// newFn is called lazily, at most limit times, so a pool of expensive
// resources constructs them as load demands rather than all at
// startup. A process that never reaches peak concurrency never pays
// for the peak.
func NewBounded[T any](limit int, newFn func() T) (*Bounded[T], error) {
	if limit <= 0 {
		return nil, ErrLimit
	}

	return &Bounded[T]{
		free:  make(chan T, limit),
		newFn: newFn,
		limit: limit,
	}, nil
}

// Get returns a value, blocking until one is available or ctx ends.
//
// A value already in the pool is returned immediately, without
// consulting ctx: a caller whose context is done still gets a value
// that was sitting there, because refusing it would serve nobody.
// Only a caller that has to wait can be cancelled.
//
// When the pool is empty and the limit has not been reached, Get
// constructs a new value rather than waiting.
//
// # Allocation contract
//
// Zero alloc when a value is available. One allocation via newFn when
// the pool is empty below its limit.
func (p *Bounded[T]) Get(ctx context.Context) (T, error) {
	// Fast path: something is already free.
	select {
	case v := <-p.free:
		return v, nil
	default:
	}

	if v, ok := p.construct(); ok {
		return v, nil
	}

	// At the limit: wait for a holder to return one.
	select {
	case v := <-p.free:
		return v, nil
	case <-ctx.Done():
		var zero T

		return zero, ctx.Err()
	}
}

// Put returns v to the pool.
//
// Never blocks and never discards. Every value the pool issued has a
// slot waiting for it, so a cleanup path can always return what it
// holds — a Put that could block would turn releasing a resource into
// a place to get stuck.
//
// Putting a value the pool did not issue is a programmer error. It
// cannot be detected for an arbitrary T without tracking identity, so
// it is documented rather than checked; the consequence is a pool
// reporting more capacity than it was built with.
//
// # Allocation contract
//
// Zero alloc.
func (p *Bounded[T]) Put(v T) {
	p.free <- v
}

// Len reports how many values are currently available to [Bounded.Get].
//
// A point-in-time observation, useful as a gauge and not as the basis
// of a decision: by the time a caller acts on it, another goroutine
// may have taken or returned a value.
func (p *Bounded[T]) Len() int {
	return len(p.free)
}

// Cap reports the pool's capacity — the limit given to [NewBounded].
func (p *Bounded[T]) Cap() int {
	return p.limit
}

// Created reports how many values newFn has produced. It rises to
// [Bounded.Cap] under load and never falls.
//
// Cap minus Created is headroom the pool has never needed; Created
// equal to Cap means the ceiling has been reached at least once and
// callers have waited, which is the signal that the limit is binding.
func (p *Bounded[T]) Created() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.created
}

// construct builds a new value if the pool is below its limit,
// reporting whether it did.
func (p *Bounded[T]) construct() (T, bool) {
	p.mu.Lock()
	if p.created >= p.limit {
		p.mu.Unlock()

		var zero T

		return zero, false
	}
	p.created++
	p.mu.Unlock()

	// newFn runs outside the lock: it may be slow — opening a
	// connection, acquiring a licence — and holding the lock would
	// serialise every other caller behind it.
	return p.newFn(), true
}
