// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package task

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// quorum is the state of one call to [Quorum]. It contains the successes
// and failures recorded, the decision, the places of the calls and the
// workers still running.
type quorum struct {
	cancel context.CancelCauseFunc

	// result is the decision's result. Guarded by mu.
	result error

	// places contains a value for each call in flight and each call that
	// succeeded. A worker sends a value before it claims an element. After
	// a call fails, its worker receives a value to free the place.
	places chan struct{}

	// work is the loop of a worker. One closure serves every worker.
	work func()

	// failures contains every failure recorded before the decision. Each
	// failure is wrapped with the index of its item. Guarded by mu.
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

// Quorum calls fn for the elements of items and returns nil as soon as k
// calls have returned nil. At most limit calls run at once. Once k calls
// have succeeded, Quorum cancels the context of the calls still running
// and does not call fn for the elements left.
//
// As soon as fewer than k calls can still succeed, Quorum cancels the
// rest and returns [ErrNoQuorum] joined with the failures that decided
// it. Quorum wraps each of those failures with the index of its item.
// When ctx ends before a decision, Quorum returns [context.Cause] of ctx.
// The first decision is final. Quorum discards the results that arrive
// after it.
//
// Quorum returns [ErrLimit] when limit is below one, and [ErrQuorumSize]
// unless 0 < k <= len(items).
//
// fn records its own results as it does for [Each]. For example, a call
// can write its result into a slice at index i. A call that ends by
// [runtime.Goexit] counts as a failure with [ErrExited].
//
// # Calls beyond the quorum
//
// Each call in flight and each call that succeeded takes a place. Quorum
// has as many places as the larger of k and limit. It starts a call only
// in a free place. A call that succeeds keeps its place. A call that
// fails frees its place for the next element.
//
// With limit equal to k, Quorum makes at most k calls and one more for
// each failure. With limit above k, Quorum runs at most limit − k calls
// beyond the quorum at once. With limit at or above the number of items,
// Quorum starts every call at once.
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
// For any length of items, Quorum allocates the derived context, its
// cancel function, the state of the call, the channel of the places and
// one closure that the goroutines share. A call in which a worker waits
// for a place also allocates the Done channel of the derived context
// once. Quorum allocates a wrapped error for each failure, and the list
// of failures grows by [append].
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
	q := &quorum{k: k, n: len(items), places: make(chan struct{}, min(max(k, limit), len(items)))}
	ctx, q.cancel = context.WithCancelCause(ctx)
	//dokimi:mutate-skip sbr-delete: every return follows a decision, which cancels, or the end of ctx
	defer q.cancel(nil)

	n := int64(len(items))

	// One closure serves every goroutine, so starting one does not
	// allocate.
	q.work = func() {
		i := 0
		returned := false

		defer func() {
			if !returned {
				crash(recover())
				q.record(parent, i, ErrExited)
				<-q.places

				// runtime.Goexit ended this worker, so a new worker replaces
				// it and claims the elements left.
				q.wg.Add(1)
				go q.work()
			}

			q.wg.Done()
		}()

		for q.place(ctx) {
			next := q.next.Add(1) - 1
			if next >= n || ctx.Err() != nil {
				break
			}

			i = int(next)
			err := fn(ctx, i, items[i])
			q.record(parent, i, err)

			if err != nil {
				<-q.places
			}
		}

		returned = true
	}

	workers := min(limit, len(items))
	q.wg.Add(workers)

	for range workers {
		go q.work()
	}

	q.wg.Wait()

	if q.decided {
		return q.result
	}

	// Quorum ends without a decision only after ctx has ended.
	return context.Cause(ctx)
}

// place takes a place for one more call and reports true. It waits while
// every place is in use. It reports false when ctx ends first.
func (q *quorum) place(ctx context.Context) bool {
	// The first call of ctx.Done before ctx ends allocates a channel.
	// place calls Done only when it has to wait.
	select {
	case q.places <- struct{}{}:
		return true
	default:
	}

	select {
	case q.places <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

// record counts the result of the call for item i and decides the
// quorum when k successes are recorded or become impossible. parent is
// the caller's context. After parent has ended, a failure does not decide
// the quorum. When that failure makes k successes impossible, a later
// result cannot decide the quorum either. Quorum then returns the cause
// of parent.
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

	q.failures = append(q.failures, fmt.Errorf("item %d: %w", i, err))
	if q.n-len(q.failures) >= q.k {
		return
	}

	// After parent has ended, Quorum returns the cause of parent in place
	// of ErrNoQuorum.
	if context.Cause(parent) != nil {
		return
	}

	failed := errors.Join(append([]error{ErrNoQuorum}, q.failures...)...)
	q.decide(failed, failed)
}

// decide records result as the decision and cancels the calls still
// running with cause. The caller locks mu before the call.
func (q *quorum) decide(result, cause error) {
	q.decided = true
	q.result = result
	q.cancel(cause)
}
