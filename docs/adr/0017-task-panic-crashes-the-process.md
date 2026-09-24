---
adr: 0017
title: A Task's Panic Crashes the Process
status: Accepted
date: 2026-09-24
supersedes: none
superseded-by: none
---

# ADR-0017: A Task's Panic Crashes the Process

## Status

Accepted

## Context

The `task` package runs caller functions on goroutines it starts:
`All`, `Each`, `Map`, `Stream`, `Run` and `Quorum`. A caller can
recover a panic in a sequential call. A panic on another goroutine can
be recovered only on that goroutine.

Other libraries that run tasks remove that difference:

- conc propagates a task's panic to the goroutine that started it.
- ants recovers a worker's panic and logs it by default.
- `golang.org/x/sync/errgroup` added propagation through `Wait` in
  2025 and reverted it on 20 June 2025.

The comment that replaced errgroup's propagation, in `errgroup.go` at
v0.23.0, gives the reasons for the revert:

- Propagation delays a panic arbitrarily, which makes the bug harder
  to detect.
- It turns the panic's stack into a value, which hides the stack from
  crash-monitoring tools.
- It risks a deadlock that hides the panic entirely, when the panic
  leaves the program unable to call `Wait`.

`sync.WaitGroup.Go` in Go 1.27.1 raises a task's panic again on the
task's goroutine and skips `Done`, so `Wait` cannot return before the
process exits.

## Decision

We will let a panic in a task crash the process from the task's own
goroutine, before the function that started the task returns, because
a propagated or recovered panic hides a programmer error from the crash
trace and from the tools that read it.

## Alternatives Considered

### Propagate the panic to the waiting goroutine

conc does this, and errgroup did until the revert. A caller can then
recover a panic in concurrent code as it can in sequential code.

Rejected. A propagated panic appears in the crash trace late and
without the task's stack, as the errgroup comment records. A recovering handler
upstream, such as the one `net/http` runs around each request, then
keeps the process serving with the bug in it.

### Recover the panic and return it as an error

ants does this by default. The group fails, and the process keeps
running.

Rejected. Retry loops, logging and `errs.Classify` then treat a
programmer error as a runtime failure, and nothing downstream can tell
a bug from a failing dependency.

## Consequences

**Positive:**

- The crash trace contains the task's frames. A probe on Go 1.27.1
  printed `panic: probe [recovered, repanicked]` with the panicking
  function and its line.
- No `recover` above the entry point can swallow a task's panic.
- `task` follows the same rule as `sync.WaitGroup.Go`.

**Negative:**

- A caller cannot recover a panic in concurrent code that it could
  recover in the same code run sequentially.
- No in-process test can observe the crash. `TestCrash` runs every
  entry point in a child process. `.ergon.yaml` exempts `crash` from
  the coverage and mutation gates, and `.golangci.yml` exempts
  `task/crash.go` from the rule that forbids `panic`.
- A panic in `Run`'s body or in `Stream`'s iterator runs on the
  caller's goroutine. It cancels the tasks and waits for them before it
  continues, so a task that ignores its context delays that panic
  without bound.

**Neutral:**

- A task that ends by `runtime.Goexit` does not crash the process. The
  entry point records `task.ErrExited`, which classifies as
  `errs.Invalid`.
- `task.Every` runs its function on the caller's goroutine, so a panic
  in it continues to the caller.

## References

- RFC-0030, structured concurrency.
- RFC-0037, periodic and quorum tasks.
- `golang.org/x/sync/errgroup` v0.23.0, `errgroup.go`.
- `golang.org/x/sync` commit `7fad2c921`, "errgroup: revert
  propagation of panics", 20 June 2025.
- golang/go#53757 and golang/go#74275.
- Go 1.27.1, `src/sync/waitgroup.go`, `WaitGroup.Go`.
- github.com/sourcegraph/conc, README, "Handle panics gracefully".
- github.com/panjf2000/ants, `options.go`, `PanicHandler`.
