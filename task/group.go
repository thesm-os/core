// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package task

import (
	"context"
	"sync"
	"sync/atomic"
)

// Group starts the tasks of one call to [Run].
//
// A Group exists only inside the call to Run that created it. After
// Run returns, [Group.Go] returns [ErrClosed] and starts nothing.
//
// # Concurrency
//
// Safe for concurrent use. The body passed to Run and every running
// task may call Go.
type Group struct {
	// ctx is the context every task receives. The group holds it
	// because a task that Go starts after body has returned still
	// needs it.
	ctx context.Context //nolint:containedctx // tasks started after body returns receive it from the group.

	// sem holds one token per running task; its capacity is the limit.
	sem chan struct{}

	scope

	// idle is a one-shot event that leave releases when n reaches zero.
	// A WaitGroup is stored in the Group itself, and a channel would
	// cost an allocation of its own.
	idle sync.WaitGroup

	// n counts body and every admitted task, and it reaches zero once,
	// after the last of them returns. Admission fails at zero, so a
	// closed group cannot reopen.
	n atomic.Int64
}

// Run calls body with a new [Group], then waits until body and every
// task started in the group have returned.
//
// At most limit tasks run at once. body runs on the caller's goroutine
// and does not count against limit, so a limit of one runs one task
// beside body.
//
// Run returns the first non-nil error from body, from a task or from a
// refused call to [Group.Go], or nil. Recording that error cancels the
// group's context, with the error as its cause. Run returns [ErrLimit]
// without calling body when limit is below one.
//
// # Context
//
// The group's context is derived from ctx, so it carries the values
// and the deadline of ctx. body and every task receive it. It is
// cancelled when ctx is done, when the first error is recorded, and
// when Run returns.
//
// # Panics
//
// A task that panics crashes the process from its own goroutine, and
// Run does not return. A body that panics, or that calls
// [runtime.Goexit], cancels the group's context and waits for every
// task, and then the panic or the exit continues.
func Run(ctx context.Context, limit int, body func(ctx context.Context, g *Group) error) error {
	if limit < 1 {
		return ErrLimit
	}

	gctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	g := &Group{
		ctx:   gctx,
		sem:   make(chan struct{}, limit),
		scope: scope{cancel: cancel},
	}
	g.n.Store(1) // body
	g.idle.Add(1)

	returned := false
	defer func() {
		if !returned {
			// body panicked or called runtime.Goexit on the caller's
			// goroutine. The tasks stop and finish before the unwinding
			// continues.
			g.fail(ErrExited)
			g.leave()
			g.idle.Wait()
		}
	}()

	g.fail(body(gctx, g))
	returned = true

	g.leave()
	g.idle.Wait()

	return g.err
}

// Go starts fn in a new goroutine and returns nil, or returns an error
// and does not start fn. A nil result means fn runs exactly once.
//
// When limit tasks are running, Go waits until one of them returns or
// the group's context is done.
//
// Error modes:
//
//   - The group's context is done. Go returns [context.Cause] of that
//     context and records it, so [Run] does not return nil after a
//     refused task.
//   - Run has returned. Go returns [ErrClosed].
//
// # Deadlock
//
// A task that calls Go waits while limit tasks run. When every running
// task waits in Go, none of them returns, and the group waits until its
// context is done. Set limit above the nesting depth, or run nested
// work under a separate call to Run.
func (g *Group) Go(fn func(ctx context.Context) error) error {
	if !g.enter() {
		return ErrClosed
	}

	if g.ctx.Err() != nil {
		return g.refuse()
	}

	// Try the send alone first. A select over the send and the context
	// costs more, and it matters only when limit tasks are running.
	select {
	case g.sem <- struct{}{}:
	default:
		select {
		case g.sem <- struct{}{}:
		case <-g.ctx.Done():
			return g.refuse()
		}
	}

	go g.run(fn)

	return nil
}

// run is the goroutine of one task.
func (g *Group) run(fn func(ctx context.Context) error) {
	returned := false
	defer g.settle(&returned, g.release)

	g.fail(fn(g.ctx))
	returned = true
}

// release frees the slot of a task that returned.
func (g *Group) release() {
	<-g.sem
	g.leave()
}

// refuse records the cause of a task that Go did not start and undoes
// its admission.
func (g *Group) refuse() error {
	cause := context.Cause(g.ctx)
	g.fail(cause)
	g.leave()

	return cause
}

// enter admits one task. It fails once the count has reached zero.
// Every legitimate caller is body or a running task, which holds a
// count of its own, so only a group used after Run returned sees zero.
func (g *Group) enter() bool {
	for {
		n := g.n.Load()
		if n == 0 {
			return false
		}

		if g.n.CompareAndSwap(n, n+1) {
			return true
		}
	}
}

// leave releases one count and releases idle when it was the last.
func (g *Group) leave() {
	if g.n.Add(-1) == 0 {
		g.idle.Done()
	}
}
