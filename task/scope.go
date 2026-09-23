// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package task

import (
	"context"
	"sync"
)

// scope is the failure state that every function in the package
// shares: the cancel function of the derived context and the first
// error recorded against it.
type scope struct {
	cancel context.CancelCauseFunc
	err    error      // the first recorded error; guarded by mu
	mu     sync.Mutex // taken only when an error is recorded
}

// fail records err as the result unless an error is already recorded,
// and cancels the derived context with err as its cause. A nil err
// does not record anything, so a task's result passes straight to
// fail.
func (s *scope) fail(err error) {
	if err == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.err == nil {
		s.err = err
		s.cancel(err)
	}
}

// skip records the cause of ctx for work that did not run because ctx
// is done, so a call that skipped work cannot return nil.
func (s *scope) skip(ctx context.Context) {
	s.fail(context.Cause(ctx))
}

// settle ends a worker goroutine, which defers it as its only deferred
// call. returned reports whether the worker's function returned.
//
// When it did not, the worker either panicked or called
// [runtime.Goexit]. settle is the deferred function itself, so its
// call to recover observes the worker's panic. [crash] raises that
// panic again before release runs, so the function that started the
// worker cannot return before the process exits. After
// runtime.Goexit, recover returns nil, and settle records [ErrExited]
// and releases the worker.
func (s *scope) settle(returned *bool, release func()) {
	if !*returned {
		crash(recover())
		s.fail(ErrExited)
	}

	release()
}
