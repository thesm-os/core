// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package task

import (
	"context"
	"iter"
	"sync"
)

// flow is the state of one call to [Stream]: the buffer between the
// caller's goroutine and the workers, and the workers still running.
type flow[E any] struct {
	ch chan E

	scope

	wg sync.WaitGroup

	// workers counts the workers started. Only the caller's goroutine
	// reads and writes it.
	workers int
}

// Stream calls fn for every element that seq yields, with at most limit
// calls running at once, and returns after every call has returned.
//
// Stream calls seq on the caller's goroutine, so seq does not need to
// be safe for concurrent use. It hands elements to its worker
// goroutines through a buffer of limit elements, and it starts one
// worker for each of the first limit elements. A sequence shorter than
// limit starts one goroutine per element.
//
// Stream returns the first non-nil error from seq or from fn, or nil.
// Recording that error cancels the context passed to fn, and the next
// yield returns false, which stops seq. Stream records [context.Cause]
// for every element that seq yielded and fn did not receive, so a nil
// result means that seq did not yield an error and fn returned nil for
// every element. Stream returns [ErrLimit] when limit is below one.
//
// # Context
//
// fn receives a context derived from ctx. It is cancelled when ctx is
// done, when the first error is recorded, and when Stream returns.
//
// # Panics
//
// A call to fn that panics crashes the process from its own goroutine,
// and Stream does not return. A seq that panics, or that calls
// [runtime.Goexit], cancels the context and waits for every worker,
// and then the panic or the exit continues.
//
// # Allocation contract
//
// Stream allocates a fixed amount per call, whatever the number of
// elements: the derived context, its state, the buffer, the closure
// shared by the workers and the yield function.
func Stream[E any](
	ctx context.Context,
	limit int,
	seq iter.Seq2[E, error],
	fn func(ctx context.Context, item E) error,
) error {
	if limit < 1 {
		return ErrLimit
	}

	f := &flow[E]{ch: make(chan E, limit)}
	ctx, f.cancel = context.WithCancelCause(ctx)
	defer f.cancel(nil)

	// One closure serves every worker, so starting one does not
	// allocate.
	work := func() {
		returned := false
		defer f.settle(&returned, f.wg.Done)

		for item := range f.ch {
			if ctx.Err() != nil {
				f.skip(ctx)

				continue
			}

			f.fail(fn(ctx, item))
		}

		returned = true
	}

	returned := false
	defer func() {
		if !returned {
			// seq panicked or called runtime.Goexit on the caller's
			// goroutine. The workers stop and finish before the
			// unwinding continues.
			f.fail(ErrExited)
			close(f.ch)
			f.wg.Wait()
		}
	}()

	// seq is called directly with one yield function, where a
	// range-over-func loop costs a closure and a state variable. A seq
	// that yields again after a false result finds the context done
	// and receives false again.
	seq(func(item E, err error) bool {
		if err != nil {
			f.fail(err)

			return false
		}

		if ctx.Err() != nil {
			f.skip(ctx)

			return false
		}

		if f.workers < limit {
			f.workers++
			f.wg.Add(1)

			go work()
		}

		// Try the send alone first. A select over the send and the
		// context costs more, and it matters only when the buffer is
		// full.
		select {
		case f.ch <- item:
		default:
			select {
			case f.ch <- item:
			case <-ctx.Done():
				f.skip(ctx)
			}
		}

		return true
	})

	returned = true

	close(f.ch)
	f.wg.Wait()

	return f.err
}
