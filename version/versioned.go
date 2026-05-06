// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package version

// Versioned wraps a value with the [Version] that produced it.
//
// Use [Versioned][T] as the return type of state-bearing reads
// when callers need to make conditional decisions on top of the
// read (the typical optimistic-concurrency pattern):
//
//  1. cur, err := store.Get(ctx, key)        // returns Versioned[T]
//  2. next := transform(cur.Value)
//  3. err = store.Put(ctx, key, next, WriteOptions{IfMatch: cur.Version})
//
// Step 3 fails with [ErrMismatch] if a concurrent writer changed
// the value between (1) and (3), giving the caller a clean signal
// to retry with the fresh state.
//
// # Allocation contract
//
// Generic value type; pass by value. The underlying value follows
// the consumer's allocation behaviour.
type Versioned[T any] struct {
	Value   T
	Version Version
}
