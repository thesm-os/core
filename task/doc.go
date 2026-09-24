// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package task runs concurrent work inside a scope. Every function in
// the package returns only after every goroutine it started has
// returned, so no task runs after the call that started it has
// returned.
//
// A goroutine started with a go statement outlives the function that
// started it unless that function waits for it. A caller that runs
// work concurrently by hand also writes its own limit, its own error
// collection and its own cancellation. [sync.WaitGroup.Go] supplies the
// wait and none of the rest. This package supplies the rest, with one
// rule for failures and one rule for panics across every function.
//
// # Choosing a function
//
//   - [All] runs a fixed set of functions written at the call site.
//   - [Each] calls one function for every element of a slice.
//   - [Map] calls one function for every element of a slice and
//     returns the results in order.
//   - [Stream] calls one function for every element of an [iter.Seq2]
//     that yields errors, such as the iterator of a
//     [go.thesmos.sh/core/page.Cursor].
//   - [Run] starts tasks one at a time through [Group.Go], for stages
//     that run together and for tasks that start tasks.
//   - [Quorum] calls one function for every element of a slice and
//     returns once k calls have succeeded, for k of n replicas or
//     witnesses.
//   - [Every] calls one function repeatedly, with a delay between
//     calls, until its context ends.
//
// A task is a function passed to [All] or [Group.Go], or one call of
// the function passed to [Each], [Map], [Stream] or [Quorum].
//
// # Failure semantics
//
// The first non-nil error is the result. Recording it cancels the
// context that every task receives, with the error as its cause, and
// later errors are discarded. Work skipped because the context was
// done records the context's cause, so a nil result means that every
// task ran and returned nil.
//
// [Quorum] and [Every] differ. Quorum succeeds when k calls succeed,
// and fails only when k successes have become impossible. Every stops
// at its function's first error, and returns nil when its context
// ends.
//
// A task that ends by [runtime.Goexit] records [ErrExited]. The
// testing package's FailNow and SkipNow call runtime.Goexit, and they
// must run on the test goroutine, so a task returns its error and the
// test checks the result.
//
// # Panics
//
// A task that panics crashes the process from its own goroutine, and
// the function that started it does not return. The crash trace
// includes the task's frames, and no recover elsewhere in the program
// can stop the crash.
//
// The body passed to [Run] and the sequence passed to [Stream] run on
// the caller's goroutine. A panic there cancels the context, waits for
// every task, and then continues to the caller. The function passed to
// [Every] also runs on the caller's goroutine, and a panic in it
// continues to the caller.
//
// # Limits
//
// [Each], [Map], [Stream], [Quorum] and [Run] require a limit of at
// least one and return [ErrLimit] for a smaller one. For CPU-bound work, pass
// runtime.GOMAXPROCS(0), which follows the cgroup CPU limit on Linux.
// For calls to a dependency, pass the concurrency that the dependency
// accepts. [All] takes no limit, because its functions are written at
// the call site and their number does not depend on input.
//
// # Contexts
//
// Every call derives a cancellable context from its ctx argument, and
// the derived context carries the values and the deadline of ctx. When
// ctx is itself cancellable, deriving and releasing the context lock a
// mutex inside ctx, so concurrent calls that share one ctx contend on
// that mutex. A long-lived goroutine that makes many calls derives its
// own context from the shared one and passes that.
//
// # Allocation contract
//
// [Each], [Map], [Stream] and [Quorum] run on a fixed set of worker
// goroutines that claim elements, so their allocations do not grow
// with the number of elements. [Group.Go] starts one goroutine per task
// and allocates the closure that starts it. [Every] allocates one timer
// per wait.
package task
