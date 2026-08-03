---
rfc: 0024
title: Request Coalescing
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Accepted
created: 2026-08-03
updated: 2026-08-03
discussion: none
supersedes: none
superseded-by: none
produces-adr: none
---

# RFC-0024: Request Coalescing

## Summary

`batch` — a loader that coalesces concurrent single-key loads into one
batched call and deduplicates concurrent loads of the same key. It
reads time through `clock.Clock`, so its accumulation window is exact
under a virtual clock. It is not a cache: results are not retained past
the in-flight window.

## Motivation

Code that fans out per-item lookups against a remote dependency issues
N calls where one would do. The pattern arises structurally rather than
by carelessness — a handler resolves a list of identifiers, and each
resolution is written independently because that is what makes the code
readable — and the cost is N round trips, N times the connection
pressure, and a latency floor set by the slowest of them.

The fix is mechanical: accumulate keys arriving within a short window,
issue one call, distribute the results. It holds no domain vocabulary,
it is generic over the key and value types, and it is re-implemented
per caller because there is nowhere shared to put it.

It belongs beside the resilience primitives for the same reason those
do. The accumulation window is a duration, so testing one against the
wall clock means sleeping, and a coalescer tested by sleeping is tested
loosely or not at all. `core` is where the clock seam is.

## Detailed design

```go
// Package batch coalesces concurrent single-key loads into batched
// calls and deduplicates concurrent loads of the same key.
package batch

// Loader coalesces loads. Concurrent calls to Load for distinct keys
// within one window are delivered to fn as a single batch; concurrent
// calls for the same key share one result.
//
// Loader is not a cache: results are not retained beyond the in-flight
// window. Caching is a separate decision with separate invalidation
// semantics.
type Loader[K comparable, V any] struct{ ... }

// LoaderConfig has no defaults.
type LoaderConfig struct {
    Clock clock.Clock

    // Wait is how long to accumulate keys before dispatching. Must
    // be > 0.
    Wait time.Duration

    // MaxBatch dispatches early once this many keys accumulate. Must
    // be > 0.
    MaxBatch int
}

func NewLoader[K comparable, V any](
    cfg LoaderConfig,
    fn func(context.Context, []K) (map[K]V, error),
) (*Loader[K, V], error)

func (l *Loader[K, V]) Load(ctx context.Context, key K) (V, error)

// LoadAll dispatches keys as one batch immediately, without waiting
// for the accumulation window. A caller that already holds the full
// key set has nothing to wait for, and making it wait would add the
// window to a request that cannot benefit from it.
//
// Duplicate keys are collapsed, and keys beyond MaxBatch are split
// across several concurrent batches. Unlike Load, LoadAll serves
// exactly one caller, so ctx reaches fn unchanged.
func (l *Loader[K, V]) LoadAll(ctx context.Context, keys []K) (map[K]V, error)

// Pending reports how many callers are waiting for a result, for
// emission as a gauge.
func (l *Loader[K, V]) Pending() int

// Close refuses further work. In-flight and accumulating batches run
// to completion, so a caller already waiting is still served; calls
// to Load after Close return ErrClosed.
//
// Close is idempotent and always returns nil. The error is in the
// signature so a Loader satisfies io.Closer and belongs in the same
// shutdown path as everything else.
func (l *Loader[K, V]) Close() error
```

### Goroutines are per batch, not per Loader

A `Loader` between batches holds nothing running. The window timer and
the goroutine that waits on it are created when a batch's first key
arrives and end when that batch dispatches.

The alternative — one dispatch goroutine for the `Loader`'s lifetime —
would make `Close` load-bearing against a leak, and a `Loader`
constructed and dropped would leak a goroutine with nothing in the type
system to say so. Per-batch, an abandoned `Loader` costs at most one
goroutine that is already on its way out.

`Close` remains, because refusing work after shutdown is a separate
need from releasing resources, and a caller that keeps loading through
a shutdown wants to be told.

### Observing the coalescing ratio

`Pending` is the numerator the RFC previously left to future work. Read
against the batch sizes `fn` receives, it says whether `Wait` is
earning its latency: many callers against few keys means the window is
working, and one caller per batch means it is not.

It is a point-in-time observation and racy by nature, like
`Bulkhead.Queued` and `Breaker.State`. A caller that branches on it
rather than calling `Load` is reimplementing the loader badly.

### Missing keys

`fn` returns a map, so a key it cannot resolve is simply absent from
the result. `Load` for that key returns the zero `V` and an error
classifying as `errs.NotFound`.

This is the decision that makes the map return type workable. The
alternative — returning `(V, bool)` per key through some richer result
type — would let a caller ignore absence silently, and absence is the
common case a batch loader has to report correctly. Making it an error
means the ordinary `if err != nil` at the call site handles it.

### Error propagation

If `fn` returns an error, every caller waiting on that batch receives
it. The error is not wrapped per key, because it describes the batch
call rather than any individual key, and wrapping would suggest
otherwise.

A caller whose key was in a failed batch may retry, and the retry
enters a fresh window. `Loader` does not retry internally: retry policy
is `resilience`'s and composing them is the caller's choice, since the
correct nesting depends on whether a failed batch should count against
a breaker.

### Context

The context passed to `fn` is not any single caller's. A batch serves
several callers with several contexts, and honouring one would cancel
work the others still need.

`Loader` passes a context that is cancelled when every waiting caller's
context is done, and carries no values. A caller whose own context ends
while a batch is in flight returns immediately with `ctx.Err()`; the
batch continues for whoever remains.

This is the subtlest behaviour in the package and the one most likely
to surprise, so it is stated on `Load` rather than only here. Values
attached to a caller's context — a trace span, a deadline, a request
identifier — do not reach `fn`, and code inside `fn` that expects them
will not find them.

### Dispatch

A batch dispatches when `Wait` elapses from the first key added, or
when `MaxBatch` keys accumulate, whichever comes first. `MaxBatch`
bounds the batch size so one burst does not produce a call larger than
the dependency accepts.

Both are required. A wrong `Wait` is wrong silently — too short and
nothing coalesces, too long and every request pays the window as
latency — and it is exactly the value nobody revisits once a default
has made it invisible.

### Errors

```go
// batch/errors.go

// ErrConfig is returned by NewLoader when a required field is missing
// or out of range. A window left at zero would coalesce nothing,
// which looks exactly like a loader that is working.
var ErrConfig = errors.New("batch: invalid configuration")

// ErrClosed reports a Load or LoadAll on a Loader whose Close has
// returned. Classifies as errs.Invalid: the caller's wiring is
// wrong, and retrying cannot help.
var ErrClosed = errs.WithClass(errors.New("batch: loader closed"), errs.Invalid)

// ErrNotFound reports a key the batch function did not resolve.
// Classifies as errs.NotFound.
var ErrNotFound = errs.WithClass(
    errors.New("batch: key not in result"), errs.NotFound)
```

### Allocation contract

Unspecified. Every dispatch brackets a remote call, and the internal
map and slice churn is not the cost that governs.

## Alternatives considered

### A. Deduplication only, without windowed batching

Share in-flight results for identical keys and never accumulate.

**Why not:** it solves the smaller half. The N-calls-for-N-distinct-keys
case is the one that actually costs round trips, and deduplication does
nothing for it.

### B. Make `Loader` a cache

Retain results for a configurable duration.

**Why not:** caching brings invalidation, and invalidation is a policy
decision with no defensible default. A loader whose results are
retained past the in-flight window is a cache with a hidden TTL, and
callers will discover it by reading stale data.

### C. Take a slice-returning `fn` instead of a map

`func(context.Context, []K) ([]V, error)` with positional
correspondence.

**Why not:** positional correspondence is unenforceable and silently
wrong when a dependency returns results out of order or omits missing
keys. A map is keyed by construction.

### D. Leave it to callers

**Why not:** the concurrency is genuinely tricky — the window timer, the
in-flight deduplication map, the fan-out of one result to many waiters,
and the context problem above — and getting it wrong produces a
deadlock or a leaked goroutine rather than a wrong answer, which is
harder to find.

## Drawbacks

- Every `Load` pays up to `Wait` in added latency, including the case
  where it is the only caller. That is the trade the package exists to
  make and it will surprise someone measuring p99.
- The context semantics are unusual and cannot be made ordinary. Code
  inside `fn` that reads request-scoped values will silently see none.
- `Loader` has a lifecycle a caller must manage, even though an
  abandoned one costs only the batch already on its way out. Nothing
  in the type system says `Close` is expected.
- Being generic over both `K` and `V`, one loader serves one key/value
  pairing, so a caller resolving three entity kinds constructs three
  loaders and wires three `fn`s.
- The package sits close to the line ADR-0005 draws: it is a
  concurrency algorithm, not a contract, and `core` holds few of those.

## Open questions

None. The three questions this RFC previously carried are resolved:
`Close` is part of the API, `LoadAll` dispatches immediately, and the
context handed to `fn` carries no caller's values.

The last of those was the closest call. Taking values from the first
waiting caller would make tracing work most of the time, at the cost of
making `fn`'s environment depend on goroutine arrival order — so a
span, a deadline or a request identifier would attach to whichever
caller happened to arrive first, and the resulting trace would
attribute one caller's batch to another. A behaviour that is right by
accident and wrong non-deterministically is worse than one that is
consistently absent, and the metrics hook under future work is the
honest way to observe a batch.

## Unresolved / future work

- Per-key error reporting, if a batch protocol emerges that
  distinguishes "this key failed" from "the call failed".
