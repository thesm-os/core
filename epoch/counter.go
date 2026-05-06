// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package epoch

import "sync/atomic"

// Counter is a thread-safe monotonic [Epoch] generator. Each call
// to [Counter.Next] returns a new [Epoch] strictly greater than
// every previous return; concurrent callers each receive a
// distinct value with no missed counts.
//
// The zero-value [Counter] starts at [Zero]; the first
// [Counter.Next] returns 1. Use [NewCounter] to start the sequence
// at a specific value (for example, after restoring from
// persisted state).
//
// # Overflow
//
// At [math.MaxUint64] [Counter.Next] wraps to zero, which breaks
// monotonicity. The wrap is unreachable in practice — see
// [Epoch.Successor] — and is therefore not guarded.
//
// # Allocation contract
//
// Pointer-allocated once at construction. [Counter.Next] and
// [Counter.Current] are zero-allocation atomic operations.
//
// # Concurrency
//
// Safe for concurrent use by any number of goroutines.
type Counter struct {
	// v holds the most-recently-issued epoch. The next call to
	// [Counter.Next] returns v+1.
	v atomic.Uint64
}

// NewCounter returns a [Counter] whose first [Counter.Next] call
// returns start.Successor(). Pass [Zero] to start the sequence
// at 1.
func NewCounter(start Epoch) *Counter {
	c := &Counter{}
	c.v.Store(uint64(start))
	return c
}

// Next returns the next [Epoch] in the sequence. The returned
// value is strictly greater than every previous [Counter.Next]
// return on this counter, and strictly greater than the
// concurrently-observed [Counter.Current].
//
// # Allocation contract
//
// Zero alloc.
func (c *Counter) Next() Epoch {
	return Epoch(c.v.Add(1))
}

// Current returns the most-recently-issued [Epoch] without
// advancing the counter. The zero-value [Counter] returns [Zero];
// after the first [Counter.Next] call, returns the value that
// call returned.
//
// # Allocation contract
//
// Zero alloc.
func (c *Counter) Current() Epoch {
	return Epoch(c.v.Load())
}
