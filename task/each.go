// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package task

import (
	"context"
	"sync"
	"sync/atomic"
)

// sweep is the state of one call to [Each], [Map] or [All]: the next
// index to claim and the workers still running.
type sweep struct {
	scope

	wg   sync.WaitGroup
	next atomic.Int64
}

// Each calls fn once for every element of items, with at most limit
// calls running at once, and returns after every call has returned.
//
// Each starts min(limit, len(items)) goroutines. Every goroutine claims
// the next unclaimed index and calls fn with that index and element.
// When items is empty, Each returns nil without starting a goroutine.
//
// Each returns the first non-nil error from fn, or nil. Recording that
// error cancels the context passed to fn, and the goroutines stop
// claiming indices. A goroutine that claims an index after the context
// is done records [context.Cause] and does not call fn. A nil result
// means that fn returned nil for every element. Each returns
// [ErrLimit] when limit is below one.
//
// # Context
//
// fn receives a context derived from ctx. It is cancelled when ctx is
// done, when the first error is recorded, and when Each returns.
//
// # Concurrency
//
// fn runs on up to min(limit, len(items)) goroutines at once. The
// caller must not modify items until Each returns. fn may write
// element i of a slice that the caller owns without a lock, because
// one call receives index i.
//
// # Panics
//
// A call to fn that panics crashes the process from its own goroutine,
// and Each does not return.
//
// # Allocation contract
//
// Each allocates a fixed amount per call, whatever the length of
// items: the derived context, its state and one closure shared by the
// goroutines.
func Each[E any](ctx context.Context, limit int, items []E, fn func(ctx context.Context, i int, item E) error) error {
	return forEach(ctx, limit, items, eachCall[E]{fn: fn})
}

// Map calls fn for every element of items, with at most limit calls
// running at once, and returns the results in the order of items.
//
// Map follows the rules of [Each], including its context, concurrency
// and panic rules. When a call fails, Map returns nil and the first
// error, because the results of the other calls are incomplete.
//
// # Allocation contract
//
// Map allocates what Each allocates, and the result slice.
func Map[E, R any](
	ctx context.Context,
	limit int,
	items []E,
	fn func(ctx context.Context, item E) (R, error),
) ([]R, error) {
	out := make([]R, len(items))

	if err := forEach(ctx, limit, items, mapCall[E, R]{fn: fn, out: out}); err != nil {
		return nil, err
	}

	return out, nil
}

// All calls every function in fns on its own goroutine, and returns
// after every call has returned.
//
// All returns the first non-nil error, or nil. Recording that error
// cancels the context passed to the other functions. All does not take
// a limit, because it is meant for a fixed set of functions written at
// the call site. Work whose size depends on input uses [Each], [Map] or
// [Stream].
//
// All follows the context, panic and allocation rules of [Each]: every
// function receives a context derived from ctx, and a function that
// panics crashes the process from its own goroutine.
func All(ctx context.Context, fns ...func(ctx context.Context) error) error {
	return forEach(ctx, max(1, len(fns)), fns, allCall{})
}

// caller makes the call for one element of a sweep. [Each], [Map] and
// [All] each pass a small struct type, and the worker closure copies
// the value. The call does not allocate, where a closure wrapping fn
// would need an allocation of its own.
type caller[E any] interface {
	call(ctx context.Context, i int, item E) error
}

// eachCall calls the function passed to [Each].
type eachCall[E any] struct {
	fn func(ctx context.Context, i int, item E) error
}

func (c eachCall[E]) call(ctx context.Context, i int, item E) error {
	return c.fn(ctx, i, item)
}

// mapCall calls the function passed to [Map] and stores its result at
// the element's index.
type mapCall[E, R any] struct {
	fn  func(ctx context.Context, item E) (R, error)
	out []R
}

func (c mapCall[E, R]) call(ctx context.Context, i int, item E) error {
	r, err := c.fn(ctx, item)
	c.out[i] = r

	return err
}

// allCall calls the element itself, which is one of the functions
// passed to [All].
type allCall struct{}

func (allCall) call(ctx context.Context, _ int, fn func(ctx context.Context) error) error {
	return fn(ctx)
}

// forEach runs c for every element of items on min(limit, len(items))
// goroutines that claim indices from a shared counter. It implements
// [Each], [Map] and [All].
func forEach[E any, C caller[E]](ctx context.Context, limit int, items []E, c C) error {
	if limit < 1 {
		return ErrLimit
	}

	if len(items) == 0 {
		return nil
	}

	s := &sweep{}
	ctx, s.cancel = context.WithCancelCause(ctx)
	defer s.cancel(nil)

	n := int64(len(items))

	// One closure serves every goroutine. A go statement without
	// arguments passes the existing function value, so starting a
	// goroutine does not allocate.
	work := func() {
		returned := false
		defer s.settle(&returned, s.wg.Done)

		for {
			i := s.next.Add(1) - 1
			if i >= n {
				break
			}

			if ctx.Err() != nil {
				s.skip(ctx)

				break
			}

			if err := c.call(ctx, int(i), items[i]); err != nil {
				s.fail(err)

				break
			}
		}

		returned = true
	}

	workers := min(limit, len(items))
	s.wg.Add(workers)

	for range workers {
		go work()
	}

	s.wg.Wait()

	return s.err
}
