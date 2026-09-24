---
rfc: 0037
title: Periodic and Quorum Tasks
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-09-24
updated: 2026-09-24
discussion: none
supersedes: none
superseded-by: none
produces-adr: none
---

# RFC-0037: Periodic and Quorum Tasks

## Summary

We propose two functions for package `task`:

- `Every` runs a function repeatedly, with a fixed delay between runs,
  until its context ends or the function fails. It reads time through
  `clock.Clock`, so a test drives it with the fake clock.
- `Quorum` runs a function for every item and returns as soon as k
  calls have succeeded, cancelling the rest. It fails as soon as k
  successes have become impossible.

Both keep `task`'s guarantee that no goroutine outlives the call. A
panic in a call that `Quorum` makes crashes the process. `Every` runs
its function on the caller's goroutine, so a panic there continues to
the caller, as a panic in the body passed to `task.Run` does.

## Motivation

### Periodic loops

A service runs work on an interval: relaying an outbox, probing a
dependency, renewing a lease, sweeping due timers. Each loop written by
hand repeats the same decisions:

- It uses `time.NewTicker`, so a test waits in real time instead of
  advancing a fake clock.
- With a ticker the next run is due on the ticker's schedule, whatever
  the previous run cost. A run longer than the period is followed at
  once by the next, with no gap.
- Many instances that start together run together, and the load
  arrives in bursts unless the loop adds jitter.
- A panic in the work kills the loop's goroutine alone, or the loop
  recovers it and hides the fault.

`task.Run` gives a loop its lifetime, and `clock.Wait` gives it a
cancellable delay. Nothing in core composes them into a loop.

### Quorums

A caller that needs k of n remote parties, such as k witnesses of n or
k replicas of n, waits for the first k successes. With `task.Each` it
waits for all n: `Each` cancels only on an error or when its context
ends, so the slowest party sets the latency even after the quorum is
met.

That is the latency a quorum exists to avoid. Dean and Barroso's "The
Tail at Scale" shows that a request fanned out to many servers takes
the latency of the slowest one. A 1-in-100 slow server, at 100
servers, delays 63% of requests. Waiting for the first k of n instead
of all n is the standard way to cut that tail, and Dynamo-style stores
use it for reads and writes.

## Detailed design

### Every

```go
// Every calls fn, then waits period plus a random delay in
// [0, jitter), then calls fn again, until ctx ends or fn returns an
// error. The first call runs at once.
//
// The delay starts when fn returns, so calls never overlap and a slow
// call does not cause the next to run early. The jitter spreads the
// calls of many instances that started together. It is read from r,
// which may be nil when jitter is 0.
//
// Every returns nil when ctx ends, which is how a loop stops. It
// returns fn's error, unchanged, when fn fails, except an error that
// is ctx's error or cause after ctx has ended. A call cut short by the
// end of ctx also stops the loop with nil, so the result of a shutdown
// does not depend on when it arrives. A caller that wants the loop to
// continue after a failure handles the failure inside fn.
//
// fn runs on the caller's goroutine, so a panic in fn continues to the
// caller. Inside a task started by Group.Go, it crashes the process as
// a panic in any task does.
//
// Every returns ErrPeriod for a period that is not positive, a
// negative jitter, or a positive jitter with a nil r.
//
// # Allocation contract
//
// Every allocates one timer from c for each wait.
func Every(ctx context.Context, c clock.Clock, r rand.Rand, period, jitter time.Duration, fn func(ctx context.Context) error) error
```

A loop runs inside a group, so it stops with the group:

```go
err := task.Run(ctx, 1, func(ctx context.Context, g *task.Group) error {
    return g.Go(func(ctx context.Context) error {
        return task.Every(ctx, clk, rnd, 30*time.Second, 3*time.Second, relay.Once)
    })
})
```

### Quorum

```go
// Quorum calls fn for every item, at most limit at a time, and
// returns nil as soon as k calls have returned nil. It then cancels
// the context of the calls still running.
//
// As soon as fewer than k calls can still succeed, Quorum cancels the
// rest and returns ErrNoQuorum joined with the failures that decided
// it, each wrapped with its item's index. When ctx ends first, it
// returns context.Cause(ctx). The first decision is final, and results
// that arrive after it are discarded.
//
// Quorum returns only after every call it started has returned. A
// call that ignores its context delays Quorum's return
// until it does. A call made through net/http with the context stops
// promptly, because the context "controls the entire lifetime of a
// request and its response".
//
// fn records its own results, for example into a slice indexed by i,
// as it does for Each. A panic in fn crashes the process. A call that
// ends by runtime.Goexit counts as a failure with ErrExited.
//
// Returns ErrLimit when limit is below one, and ErrQuorumSize unless
// 0 < k <= len(items).
//
// # Allocation contract
//
// As Each: a fixed number of allocations per call, and none per item.
// Each failure allocates its wrapped error.
func Quorum[E any](ctx context.Context, limit, k int, items []E, fn func(ctx context.Context, i int, item E) error) error
```

A caller collects two cosignatures from five witnesses:

```go
sigs := make([][]byte, len(witnesses))
err := task.Quorum(ctx, len(witnesses), 2, witnesses, func(ctx context.Context, i int, w Witness) error {
    sig, err := w.Cosign(ctx, checkpoint)
    sigs[i] = sig
    return err
})
```

### Errors

```go
var (
    // ErrPeriod reports a non-positive period, a negative jitter, or a
    // positive jitter without a random source. It classifies as
    // errs.Invalid.
    ErrPeriod = errs.WithClass(errors.New("task: period must be positive and jitter non-negative"), errs.Invalid)

    // ErrQuorumSize reports a quorum of zero or of more than the items.
    // It classifies as errs.Invalid.
    ErrQuorumSize = errs.WithClass(errors.New("task: quorum must be between 1 and the number of items"), errs.Invalid)

    // ErrNoQuorum reports that fewer than k calls can succeed. It has
    // no class of its own, so errs.Classify finds the class of the
    // failures joined with it.
    ErrNoQuorum = errors.New("task: quorum not met")
)
```

### Tests

- `Every` under the fake clock: the first call runs at once, the next
  runs after the period and not before, a slow call delays the next by
  a full period, the jitter remains within its bound, cancellation ends
  the loop with nil, and an error from fn ends it with that error.
- `Quorum`: k successes return nil and cancel the rest. n − k + 1
  failures return `ErrNoQuorum` with those failures joined. The
  context's cause is returned when it ends first.
- `Quorum` runs the rules shared by every function of the package,
  with k equal to the number of tasks: the limit bounds concurrency,
  `runtime.Goexit` records `ErrExited`, and no task's context outlives
  the call. `Every` starts no goroutine.

## Alternatives considered

### A. A ticker that runs at a fixed rate

`Every` would schedule each call at a multiple of the period from the
start, as `time.Ticker` does.

**Why not:** a call longer than the period is followed at once by the
next, and a long stall ends in a run of calls with no gap. A fixed
delay never overlaps calls and never bunches them. Kubernetes'
`wait.Until` makes the same choice: its period "starts after the f
completes".

### B. Continue after a failure

`Every` would record fn's error and keep looping.

**Why not:** every other function in `task` stops at the first error,
and a caller composing `Every` into a group expects the same. A loop
that tolerates failures decides inside fn what to do with each one.

### C. A quorum mode for `Each`

`Each` would take an option that stops at k successes.

**Why not:** the two functions return different things. `Each`
succeeds when every call succeeds, and `Quorum` succeeds when k do. An
option would give one function two contracts.

### D. Return at the quorum and leave the stragglers running

`Quorum` would return as soon as k calls succeed, and the cancelled
calls would finish in the background.

**Why not:** a call that outlives `Quorum` can write into the caller's
results after the caller has read them, and a goroutine that outlives
its scope is the leak structured concurrency exists to prevent. A
cancelled call that honours its context returns within one network
round trip, so waiting for it costs little. A call that ignores its
context is a defect in the call, and `Quorum` makes it visible as
latency instead of hiding it as a leak.

## Drawbacks

- `Every` allocates a timer for each wait, because `clock.Clock` hands
  out timers one at a time.
- `Quorum` waits for every call it started. A remote call that ignores
  cancellation delays the return, as it delays `Each`.
- `ErrNoQuorum` has no class, so a caller that switches on the
  class sees the class of the first classified failure joined with it.

## Open questions

None.

## Unresolved / future work

- A `Timer` that can be reset in place, if `clock.Clock` gains one, so
  `Every` allocates once per loop instead of once per wait.

## References

- Kubernetes, `k8s.io/apimachinery/pkg/util/wait`: `Until` and
  `JitterUntil`.
- J. Dean and L. A. Barroso, "The Tail at Scale", Communications of the
  ACM 56(2), 2013.
- G. DeCandia et al., "Dynamo: Amazon's Highly Available Key-value
  Store", SOSP 2007.
- Go: `net/http.NewRequestWithContext`.
- Go: `time.Ticker`.
- RFC-0030, structured concurrency.
- RFC-0001, the clock seam.
