// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package task

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// serving is the state of one call to [Serve]: the failure state and the
// calls still running.
type serving struct {
	scope

	wg sync.WaitGroup
}

// Serve calls every function in fns on its own goroutine, and returns
// after every call has returned. Each function lasts until its context
// ends, such as the loop of a service or a call of [Every].
//
// The first call that returns ends the context of the other calls,
// whether it returned an error or nil. Serve returns the first error, or
// nil when every call stopped at the end of its context. A call stops at
// the end of its context when it returns nil, or the error or the cause of
// its context, after its context has ended. A nil return before the end of
// the context breaks the contract of Serve, and the call records an error
// that wraps [ErrReturned] and names the index of the function in fns.
//
// Serve calls every function, also when ctx has ended before the call. The
// function starts, finds its context ended and returns, so the end of ctx
// stops Serve with nil. Serve returns nil for no functions.
//
// # Context
//
// Each function receives a context derived from ctx. It is cancelled when
// ctx is done, when the first call returns, and when Serve returns.
//
// # Panics
//
// A function that panics crashes the process from its own goroutine, and
// Serve does not return. A function that ends by [runtime.Goexit] records
// [ErrExited].
func Serve(ctx context.Context, fns ...func(ctx context.Context) error) error {
	// Without functions, Serve returns nil either way. The return spares a
	// derived context that no call would end.
	//dokimi:mutate-skip ror-false,sbr-delete: a call without functions returns nil either way
	if len(fns) == 0 {
		return nil
	}

	// Serve does not defer the cancel of ctx. Every call that returns
	// either finds ctx ended or records an error, which cancels ctx, so ctx
	// has ended once every call has returned.
	s := &serving{}
	ctx, s.cancel = context.WithCancelCause(ctx)

	s.wg.Add(len(fns))

	for i, fn := range fns {
		go s.serve(ctx, i, fn)
	}

	s.wg.Wait()

	return s.err
}

// serve calls fn, function i of a call to [Serve], on its own goroutine,
// and records the result of the call.
func (s *serving) serve(ctx context.Context, i int, fn func(ctx context.Context) error) {
	returned := false
	defer s.settle(&returned, s.wg.Done)

	err := fn(ctx)
	returned = true

	// A call that stopped at the end of ctx returns nil, or ctx's error or
	// cause. Both are nil until ctx ends, and errors.Is with a nil target
	// reports false for a non-nil err.
	if err == nil && ctx.Err() == nil {
		err = fmt.Errorf("%w: function %d", ErrReturned, i)
	} else if errors.Is(err, ctx.Err()) || errors.Is(err, context.Cause(ctx)) {
		err = nil
	}

	s.fail(err)
}
