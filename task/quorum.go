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
// Quorum runs on a new [QuorumRunner]. A caller that runs one quorum at a
// time keeps a QuorumRunner and calls [QuorumRunner.Run], which allocates
// less.
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
// for a place while elements remain also allocates the Done channel of the
// derived context once. Quorum allocates a wrapped error for each failure,
// and the list of failures grows by [append].
func Quorum[E any](
	ctx context.Context,
	limit, k int,
	items []E,
	fn func(ctx context.Context, i int, item E) error,
) error {
	return new(QuorumRunner[E]).quorum(ctx, limit, k, items, fn)
}

// QuorumRunner runs the calls of [Quorum] from state that it keeps across
// its calls: the count of the results, the channel of the places, the list
// of failures and the closure that its workers share. A caller that runs
// one quorum at a time, such as a signer that signs each message with k of
// n keys, keeps a QuorumRunner and calls [QuorumRunner.Run] for each
// quorum.
//
// The zero value is ready to use. A QuorumRunner does not keep the
// context, the items, the function or the failures of a call after Run
// returns.
//
// # Concurrency
//
// A QuorumRunner runs one call of Run at a time. Concurrent calls of Run
// run one after the other. A call that waits for another call does not
// end at the end of its context.
//
// # Allocation contract
//
// Run allocates the derived context and its cancel function. The first
// call of Run also allocates the closure that the workers share. A call
// allocates the channel of the places when the places of the call differ
// from the capacity of the channel that the QuorumRunner keeps. A call in
// which a worker waits for a place while elements remain also allocates
// the Done channel of the derived context once. Run allocates a wrapped
// error for each failure, and the list of failures grows by [append] until
// its capacity fits the failures of a call.
type QuorumRunner[E any] struct {
	// ctx is the context of the calls of fn, derived from the context of
	// the caller of Run.
	ctx context.Context //nolint:containedctx // the workers of a call read the context of its calls

	// parent is the context of the caller of Run. After parent has ended, a
	// failure does not decide the quorum.
	parent context.Context //nolint:containedctx // a failure reads the end of the caller's context

	// result is the decision's result. Guarded by mu.
	result error

	cancel context.CancelCauseFunc

	// fn is the function of the call, and items its elements.
	fn func(ctx context.Context, i int, item E) error

	// work is the loop of a worker. One closure serves every worker of
	// every call.
	work func()

	// places contains a value for each call in flight and each call that
	// succeeded. A worker sends a value before it claims an element. After
	// a call fails, its worker receives a value to free the place.
	places chan struct{}

	items []E

	// failures contains every failure recorded before the decision. Each
	// failure is wrapped with the index of its item. Guarded by mu.
	failures []error

	wg   sync.WaitGroup
	next atomic.Int64

	// ok counts the calls that returned nil. Guarded by mu.
	ok int

	k, n int

	// run lets one call of Run at a time use the state.
	run sync.Mutex

	mu sync.Mutex

	// decided reports whether k successes were recorded or became
	// impossible. The first decision is final. Guarded by mu.
	decided bool
}

// Run calls fn for the elements of items with the rules of [Quorum], and
// returns the result that Quorum returns for the same calls.
//
// Error modes: as [Quorum].
//
// # Allocation contract
//
// As the allocation contract of [QuorumRunner] states.
func (q *QuorumRunner[E]) Run(
	ctx context.Context,
	limit, k int,
	items []E,
	fn func(ctx context.Context, i int, item E) error,
) error {
	q.run.Lock()
	defer q.run.Unlock()

	err := q.quorum(ctx, limit, k, items, fn)
	q.reset()

	return err
}

// quorum runs one call of Quorum on the state of q, and returns its result.
// It leaves the context, the items, the function, the result and the
// failures of the call in q, and the places that the call took in the
// channel. Run resets q for its next call. Quorum discards q, so it does
// not reset q.
func (q *QuorumRunner[E]) quorum(
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

	if places := min(max(k, limit), len(items)); cap(q.places) != places {
		q.places = make(chan struct{}, places)
	}

	if q.work == nil {
		// One closure serves every goroutine of every call, so starting a
		// worker does not allocate.
		q.work = q.loop
	}

	q.k, q.n, q.items, q.fn, q.parent = k, len(items), items, fn, ctx
	q.ctx, q.cancel = context.WithCancelCause(ctx)

	workers := min(limit, len(items))
	q.wg.Add(workers)

	for range workers {
		go q.work()
	}

	q.wg.Wait()

	// A call ends without a decision only after ctx has ended. The derived
	// context then has the cause of ctx.
	err := q.result
	if !q.decided {
		err = context.Cause(ctx)
	}

	//dokimi:mutate-skip sbr-delete: every return follows a decision, which cancels, or the end of ctx
	q.cancel(nil)

	return err
}

// loop is the loop of a worker. It takes a place, claims the next element
// and calls fn for it, until no element is left, the derived context ends
// or the quorum is decided. A worker that runtime.Goexit ends records
// ErrExited for its element, frees its place and starts a worker in its
// place.
func (q *QuorumRunner[E]) loop() {
	i := 0
	returned := false

	defer func() {
		if !returned {
			// Without the recover, the panic of a task continues after this
			// call and still ends the process. Only the order of the exit and
			// the worker that this call starts differs. No test can force that
			// order.
			//dokimi:mutate-skip sbr-delete: the panic of a task ends the process with or without the recover
			crash(recover())
			q.record(i, ErrExited)
			<-q.places

			// runtime.Goexit ended this worker, so a new worker replaces
			// it and claims the elements left.
			q.wg.Add(1)
			go q.work()
		}

		q.wg.Done()
	}()

	n := int64(q.n)

	// A worker without an element left returns before it takes a place,
	// so it does not wait for a place that it cannot use.
	for q.next.Load() < n && q.place() {
		next := q.next.Add(1) - 1
		if next >= n || q.ctx.Err() != nil {
			break
		}

		i = int(next)
		err := q.fn(q.ctx, i, q.items[i])
		q.record(i, err)

		if err != nil {
			<-q.places
		}
	}

	returned = true
}

// place takes a place for one more call and reports true. It waits while
// every place is in use. It reports false when the derived context ends
// first.
func (q *QuorumRunner[E]) place() bool {
	// The first call of Done before the context ends allocates a channel.
	// place calls Done only when it has to wait.
	select {
	case q.places <- struct{}{}:
		return true
	default:
	}

	select {
	case q.places <- struct{}{}:
		return true
	case <-q.ctx.Done():
		return false
	}
}

// record counts the result of the call for item i and decides the
// quorum when k successes are recorded or become impossible. After the
// context of the caller of Run has ended, a failure does not decide the
// quorum. When that failure makes k successes impossible, a later result
// cannot decide the quorum either. Run then returns the cause of that
// context.
func (q *QuorumRunner[E]) record(i int, err error) {
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

	// After the caller's context has ended, Run returns its cause in place
	// of ErrNoQuorum.
	if context.Cause(q.parent) != nil {
		return
	}

	failed := errors.Join(append([]error{ErrNoQuorum}, q.failures...)...)
	q.decide(failed, failed)
}

// decide records result as the decision and cancels the calls still
// running with cause. The caller locks mu before the call.
func (q *QuorumRunner[E]) decide(result, cause error) {
	q.decided = true
	q.result = result
	q.cancel(cause)
}

// reset returns q to the state of a QuorumRunner without a call. It
// empties the channel of the places, and keeps the channel, the closure of
// the workers and the capacity of the list of failures for the next call.
// It drops the context, the items, the function, the result and the
// failures of the call. Every worker of the call has returned.
func (q *QuorumRunner[E]) reset() {
	for len(q.places) > 0 {
		<-q.places
	}

	clear(q.failures)
	q.failures = q.failures[:0]
	q.next.Store(0)
	q.ok, q.decided, q.result = 0, false, nil
	q.ctx, q.parent, q.cancel, q.items, q.fn = nil, nil, nil, nil, nil
}
