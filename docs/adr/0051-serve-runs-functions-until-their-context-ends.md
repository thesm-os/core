---
adr: 0051
title: Serve Runs Functions Until Their Context Ends
status: Accepted
date: 2026-10-13
supersedes: none
superseded-by: none
---

<!--
  ~ Copyright ThesmOS B.V. 2026
  ~ SPDX-License-Identifier: Apache-2.0
-->

# ADR-0051: Serve Runs Functions Until Their Context Ends

## Status

Accepted

## Context

Every function of package `task` follows one rule for failures. Work that
a function skips because its context has ended records the cause of the
context, so a nil result means that every task ran and returned nil. `All`
skips a function whose turn comes after the end of its context. For a
context that ended before the call, `All` does not call a function and
returns `context.Canceled`.

A service runs functions that last until their context ends, such as the
`Run` of a storage engine and a loop of `task.Every`. The end of the
context is the normal end of such a function. A call that `All` skips at
that point does not lose work. The service needs a nil result for its
shutdown. `Every` returns nil when its context ends for the same reason.

Without a function of `task` for this case, the caller writes its own
wait, its own cancellation and its own collection of errors.

## Decision

We will add `task.Serve` to run every function until the first one
returns, because a service needs nil at the end of its context and a
failure when one of its functions stops by itself:

- Serve calls every function on its own goroutine, also when its context
  ended before the call.
- The first call that returns ends the context of the other calls.
- A call that returns nil, or the error or the cause of its context, after
  its context has ended, stopped at the end of its context and records
  nothing.
- A call that returns nil before its context has ended records an error
  that wraps `ErrReturned` and names the index of the function.
  `ErrReturned` classifies as Invalid.
- Serve returns the first recorded error, or nil.
- A panic and `runtime.Goexit` follow the rules of `All`.

`All` keeps its rule.

## Alternatives Considered

### An option of All that starts every function

`All` would take an option that calls every function on an ended context.

Rejected. A nil result of `All` means that every task ran. With the option,
the meaning of a nil result would depend on the arguments of the call.

### A nil result for a function that returns nil early

Serve would treat every nil return as the end of the function, as the
request proposed.

Rejected. A function of a service that returns nil while its context is
live has stopped by itself. Serve would end the other functions and return
nil, and the main function of the service would exit with status 0 after
it lost a component.

### The error of the first function that returns

Serve would return the result of the first call that returns, as
`Group.Run` of oklog/run does.

Rejected. A call that returns nil at the end of the context could then hide
the failure of another call.

### errgroup of golang.org/x/sync

`errgroup.WithContext` starts each function whatever the state of its
context.

Rejected. It cancels the context only for an error and at `Wait`, so a
function that returns nil early does not stop the other functions.

## Consequences

**Positive:**

- A service runs its loops with one call. The end of its context returns
  nil.
- A failure of one function ends the other functions, and Serve returns
  the failure.
- A function that stops by itself fails the call with `ErrReturned`.

**Negative:**

- Package `task` has three exceptions to its rule for failures: `Quorum`,
  `Every` and `Serve`. A reader checks which function a caller calls.
- A function that finishes on purpose before its context ends cannot run
  under Serve. Its caller uses `All`.
- Serve starts one goroutine per function, without a limit.

**Neutral:**

- `ErrReturned` names the index of the function in the arguments of
  Serve, not the name of the function.

## References

- `task/doc.go`, the section Failure semantics.
- `task/each.go`: `forEach`, which skips work after the end of its
  context. `task/serve.go`: `Serve`.
- oklog/run, `Group.Run`, <https://github.com/oklog/run>.
- golang.org/x/sync/errgroup, <https://pkg.go.dev/golang.org/x/sync/errgroup>.
