// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package task

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// quorum is the state of one call to [Quorum]: the successes and
// failures recorded, the decision, and the workers still running.
type quorum struct {
	cancel context.CancelCauseFunc

	// result is the decision's result. Guarded by mu.
	result error

	// failures contains every failure recorded before the decision,
	// each wrapped with its item's index. Guarded by mu.
	failures []error

	wg   sync.WaitGroup
	next atomic.Int64
	mu   sync.Mutex

	// ok counts the calls that returned nil. Guarded by mu.
	ok int

	k, n int

	// decided reports whether k successes were recorded or became
	// impossible. The first decision is final. Guarded by mu.
	decided bool
}

// Quorum calls fn for every element of items, with at most limit calls
// running at once, and returns nil as soon as k calls have returned
// nil. It then cancels the context of the calls still running and does
// not call fn for the elements left.
//
// As soon as fewer than k calls can still succeed, Quorum cancels the
// rest and returns [ErrNoQuorum] joined with the failures that decided
// it, each wrapped with its item's index. When ctx ends before a
// decision, Quorum returns [context.Cause] of ctx. The first decision
// is final, and results that arrive after it are discarded.
//
// Returns [ErrLimit] when limit is below one, and [ErrQuorumSize]
// unless 0 < k <= len(items).
//
// fn records its own results, for example into a slice indexed by i,
// as it does for [Each]. A call that ends by [runtime.Goexit] counts as
// a failure with [ErrExited].
//
// # Waiting for calls
//
// Quorum returns only after every call it started has returned. A call
// that ignores its context delays Quorum's return until it does. A
// call made through net/http with the context stops promptly, because
// the request's context controls the lifetime of the request and its
// response.
//
// # Context
//
// fn receives a context derived from ctx. It is cancelled when ctx is
// done, when the quorum is decided, and when Quorum returns.
//
// # Panics
//
// A call to fn that panics crashes the process from its own goroutine,
// and Quorum does not return.
//
// # Allocation contract
//
// Quorum allocates a fixed amount per call, whatever the length of
// items: the derived context, its state and one closure shared by the
// goroutines. Each failure allocates its wrapped error.
func Quorum[E any](
	ctx context.Context,
	limit, k int,
	items []E,
	fn func(ctx context.Context, i int, item E) error,
) error {
	if limit < 1 {
		return ErrLimit
	}

	if k < 1 || k > len(items) {
		return ErrQuorumSize
	}

	parent := ctx
	q := &quorum{k: k, n: len(items)}
	ctx, q.cancel = context.WithCancelCause(ctx)
	defer q.cancel(nil)

	n := int64(len(items))

	// One closure serves every goroutine, so starting one does not
	// allocate.
	work := func() {
		i := 0
		returned := false

		defer func() {
			if !returned {
				crash(recover())
				q.record(parent, i, ErrExited)
			}

			q.wg.Done()
		}()

		for {
			next := q.next.Add(1) - 1
			if next >= n || ctx.Err() != nil {
				break
			}

			i = int(next)
			q.record(parent, i, fn(ctx, i, items[i]))
		}

		returned = true
	}

	workers := min(limit, len(items))
	q.wg.Add(workers)

	for range workers {
		go work()
	}

	q.wg.Wait()

	if q.decided {
		return q.result
	}

	// Without a decision some elements were never claimed, which
	// happens only when ctx ended.
	return context.Cause(ctx)
}

// record counts the result of the call for item i and decides the
// quorum when k successes are recorded or become impossible. parent is
// the caller's context: a failure that decides the quorum after parent
// has ended returns parent's cause.
func (q *quorum) record(parent context.Context, i int, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.decided {
		return
	}

	if err == nil {
		q.ok++
		if q.ok == q.k {
			q.decide(nil, nil)
		}

		return
	}

	// The decision comes at the latest with failure n-k+1, so n is
	// room enough. A capacity computed from k would give a mutation tool
	// a mutant that only changes the capacity, which no test can see.
	if q.failures == nil {
		q.failures = make([]error, 0, q.n)
	}

	q.failures = append(q.failures, fmt.Errorf("item %d: %w", i, err))
	if q.n-len(q.failures) >= q.k {
		return
	}

	if cause := context.Cause(parent); cause != nil {
		q.decide(cause, cause)

		return
	}

	failed := errors.Join(append([]error{ErrNoQuorum}, q.failures...)...)
	q.decide(failed, failed)
}

// decide records result as the decision and cancels the calls still
// running with cause. Must be called with mu held.
func (q *quorum) decide(result, cause error) {
	q.decided = true
	q.result = result
	q.cancel(cause)
}
