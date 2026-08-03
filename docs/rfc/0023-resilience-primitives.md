---
rfc: 0023
title: Resilience Primitives
author: Roy Klopper <roy.klopper@stealthscale.io>
status: Draft
created: 2026-08-03
updated: 2026-08-03
discussion: none
supersedes: none
superseded-by: none
produces-adr: none
---

# RFC-0023: Resilience Primitives

## Summary

`resilience` — circuit breaking, concurrency limiting, and jittered
backoff, all reading time through `clock.Clock` and randomness through
`rand.Rand` so their behaviour is exact under a virtual clock and a
seeded source. Every threshold is required at construction; the package
ships no defaults. Which error classes trip a breaker is a required
configuration decision, and a caller's own cancellation never counts as
a dependency failure.

## Motivation

Every caller of a remote dependency needs the same three things: stop
calling something that is failing, bound how much concurrency one slow
dependency can consume, and space out retries so a fleet does not
synchronise.

These are pure algorithms over time and randomness. They hold no domain
vocabulary. And they are re-implemented per caller, incompatibly,
because there is nowhere shared to put them.

The specific reason they belong here rather than one layer up is
testability. A circuit breaker's behaviour is a function of elapsed
time, so testing one requires controlling time. `core` is the only
module where `clock.Clock` and `rand.Rand` are both available without
taking a dependency. Built anywhere else, breaker state transitions are
tested by sleeping — which is slow, flaky, and eventually skipped — and
that is why breaker bugs reach production rather than a test run.

The standard library omits these because Go has no clock seam. `core`
has one, and that changes the calculus rather than repeating it.

## Detailed design

```go
// Package resilience holds the algorithms every caller of a remote
// dependency needs: circuit breaking, concurrency limiting, and
// backoff.
//
// All three read time through [clock.Clock] and randomness through
// [rand.Rand], so behaviour is deterministic under a virtual clock and
// a seeded source. That is the property that makes them testable, and
// the reason they belong here rather than in each caller.
package resilience
```

### Breaker

```go
// Breaker stops calling a dependency that is failing, and probes
// periodically to discover recovery.
//
// State transitions are a function of elapsed time read through the
// configured [clock.Clock]; under a virtual clock they are exact.
type Breaker struct{ ... }

// BreakerConfig has no defaults. Every field is required, because a
// threshold that is wrong is wrong silently.
type BreakerConfig struct {
    Clock clock.Clock

    // FailureThreshold is the count of consecutive failures that opens
    // the breaker. Must be > 0.
    FailureThreshold int

    // OpenFor is how long the breaker stays open before admitting a
    // probe. Must be > 0.
    OpenFor time.Duration

    // HalfOpenProbes is the count of consecutive successes required to
    // close again. Must be > 0.
    HalfOpenProbes int

    // TripOn are the error classes that count as a dependency failure.
    // Must be non-empty.
    //
    // There is no default, and the omission is deliberate: whether an
    // unclassified error indicates a failing dependency is the single
    // decision that determines whether this breaker protects anything
    // or opens at random. A caller whose dependencies all classify
    // their errors passes [errs.Transient] alone. A caller wrapping
    // code that does not classify must decide whether
    // [errs.Unspecified] belongs here, knowing that including it means
    // every unclassified error trips the breaker.
    TripOn []errs.Class
}

func NewBreaker(cfg BreakerConfig) (*Breaker, error)

// State reports the breaker's current state, for emission as a gauge.
// It is a point-in-time observation and racy by nature: a caller that
// branches on it rather than calling Call is reimplementing the
// breaker badly.
func (b *Breaker) State() State

type State uint8

const (
    Closed State = iota
    Open
    HalfOpen
)

// ErrOpen is returned instead of calling through. Classifies as
// [errs.Transient]: the dependency may recover, and the caller should
// retry later rather than treat the failure as permanent.
var ErrOpen = errors.New("resilience: circuit open")

// Call runs fn unless the breaker is open, in which case it returns
// [ErrOpen] without calling fn.
//
// Whether an error counts as a failure is decided by [errs.Classify]
// against cfg.TripOn, with one exception below. A dependency correctly
// rejecting a bad request is not a failing dependency, so
// [errs.Invalid], [errs.NotFound] and [errs.Denied] should not
// normally appear in TripOn.
func Call[T any](ctx context.Context, b *Breaker, fn func(context.Context) (T, error)) (T, error)
```

`TripOn` being required is the load-bearing decision in this section.
An unclassified error is ambiguous by construction: it may be a failing
dependency or a caller-side bug, and `errs` cannot tell them apart. A
default in either direction is wrong for half of all callers, and wrong
silently — a breaker that never opens looks identical to a healthy
dependency, and a breaker that opens on every unclassified error looks
identical to an outage.

`Call` rather than `Do`: `resilience.Do` says nothing at a call site,
and the package holds three primitives of which only one would own such
a generic verb.

### Caller cancellation never trips the breaker

If `ctx.Err() != nil` when `fn` returns, `Call` returns the error
without counting it, whatever its class.

The dependency did not fail; the caller stopped waiting. Counting it
inverts the breaker's purpose — a burst of client timeouts or a
cancelled request batch would open the circuit against a dependency
that is healthy, and the resulting `ErrOpen` responses would then look
like the outage that was not happening.

This is also the answer to whether `errs` needs a ninth class for
caller-side cancellation. It does not: the condition is observable
directly from the context at the one place that cares.

### Bulkhead

```go
// Bulkhead bounds concurrent execution, so one slow dependency cannot
// consume more than its share of the caller's concurrency.
type Bulkhead struct{ ... }

// NewBulkhead admits at most limit concurrent calls. limit must be > 0.
func NewBulkhead(limit int) (*Bulkhead, error)

// Acquire blocks until a slot is free or ctx ends. The returned
// release function must be called exactly once.
func (b *Bulkhead) Acquire(ctx context.Context) (release func(), err error)
```

The implementation is a buffered channel and the value is not the
algorithm. It is that `Acquire` is context-aware, that `release` is
returned rather than being a second method a caller can pair with the
wrong acquisition, and that the limit is required.

### Backoff and Retry

```go
// Backoff returns the delay before attempt n, exponential from base
// and capped at max, with full jitter drawn from r.
//
// Jitter is not optional. Synchronised retry across a fleet is the
// failure mode backoff exists to prevent, and unjittered exponential
// backoff preserves it exactly.
func Backoff(r rand.Rand, attempt int, base, max time.Duration) time.Duration

// RetryConfig has no defaults.
type RetryConfig struct {
    Clock clock.Clock
    Rand  rand.Rand

    // Attempts is the total number of calls, not the number of
    // retries. Must be > 0.
    Attempts int

    // Base is the first backoff interval. Must be > 0.
    Base time.Duration

    // Max caps the backoff interval. Must be >= Base.
    Max time.Duration
}

// Retry calls fn until it succeeds, ctx ends, or Attempts is
// exhausted, waiting [Backoff] between attempts and stopping early on
// any error that [errs.Classify] reports as non-retryable.
func Retry[T any](
    ctx context.Context,
    cfg RetryConfig,
    fn func(context.Context) (T, error),
) (T, error)
```

`Retry` takes a config struct rather than positional parameters for one
concrete reason: `Base` and `Max` are adjacent values of the same type,
and a transposition compiles, passes review, and produces a retry loop
whose first wait is its longest. Named fields make that unrepresentable
at the call site, and the shape then matches `BreakerConfig` and
`LoaderConfig`.

`Backoff` keeps its positional form. It is a pure function of four
values with no configuration lifetime, and a config struct for a
one-line computation is ceremony.

`Retry` waits through `clock.Wait`, so a retry loop under a virtual
clock consumes no real time and a cancelled retry leaks no timer.

`Call` and `Retry` are generic functions rather than methods because Go
methods cannot take type parameters. Returning `T` rather than forcing
the caller to capture a result in a closure is what makes them usable.

### Composing a breaker with a retry

The breaker goes inside the retry:

```go
resilience.Retry(ctx, cfg, func(ctx context.Context) (T, error) {
    return resilience.Call(ctx, breaker, fetch)
})
```

so the breaker observes each attempt and `ErrOpen` — classified
`errs.Transient` — is what the retry backs off against. Inverted, one
logical call contributes `Attempts` failures to the breaker and opens
it after a single bad request.

`Retry` does not take a `*Breaker` to enforce this. Doing so would make
the composition mandatory for callers who want only one of the two, and
the correct nesting is one line to write once it is stated.

### No defaults on policy

Every threshold in this package is required at construction. A wrong
default in a foundation is wrong in every caller and is never revisited,
because a default is invisible: nobody reviews a value they did not
write. An absent default is answered once, deliberately, per call site.

This is why the constructors return errors rather than panicking or
silently correcting: a zero `FailureThreshold` is a configuration error
the caller can handle at wiring time.

### Allocation contract

`Backoff` allocates nothing. `Bulkhead.Acquire` allocates the release
closure. `Call` and `Retry` allocate according to `fn`. None of these
is a hot path in the sense the memory packages use the term — every one
of them brackets a remote call.

## Alternatives considered

### A. Leave these to each caller

**Why not:** they are re-derived per caller today, and the derivations
disagree on the questions that matter — whether the breaker counts
consecutive or windowed failures, whether backoff is jittered, whether
a half-open probe is one request or several. The disagreement is
invisible until an outage, when two callers behave differently against
the same failing dependency.

### B. A configurable retry-policy framework

Strategy objects, predicates, composable policies.

**Why not:** that is where a resilience library becomes a framework, and
a foundation should not be one. `Retry` and `Backoff` have required
parameters and no extension points; a caller needing something else
writes the loop, which is six lines.

### C. Take `time.Now` directly instead of `clock.Clock`

**Why not:** it forfeits the entire reason these belong in `core`.
Testing a breaker against the wall clock means sleeping through
`OpenFor` in a unit test, which means the test is slow, then flaky,
then skipped.

### D. A predicate `func(error) bool` instead of `TripOn []errs.Class`

**Why not:** it lets every caller write a different predicate over the
same classification, which is the divergence `errs` exists to remove. A
caller that genuinely needs a predicate can wrap `Call`.

### E. Positional parameters for `Retry`

**Why not:** two adjacent `time.Duration` parameters are a transposition
waiting to happen, and the failure is silent — the loop still retries,
just with its intervals inverted.

## Drawbacks

- `BreakerConfig` has five required fields and `RetryConfig` five more,
  so the simplest possible use is verbose. That is the intended trade
  and it will be experienced as friction.
- The breaker counts *consecutive* failures, which is the simplest
  correct rule and not the best one. A dependency failing 40% of the
  time never opens it.
- `resilience` depends on `errs`, `clock` and `rand`, making it the
  most connected package in the module. A change to `errs.Class` is a
  change here.
- `Bulkhead` is thin enough that a reader may reasonably ask why it is
  not a channel at the call site. The answer is uniformity of the
  context and release semantics, which is a weaker argument than the
  one for `Breaker`.
- `State` invites exactly the misuse its doc warns against: branching on
  the breaker instead of calling through it.
- The correct breaker/retry nesting is documented, not enforced, and
  getting it backwards is silent.

## Open questions

None. The three questions this RFC previously carried are resolved
above: `State` is added for gauge emission with a warning against
branching on it; the breaker/retry composition is documented rather
than enforced, with the reason; and windowed failure counting moves to
future work rather than complicating `FailureThreshold`.

## Unresolved / future work

- A windowed or rate-based breaker, which handles the partial-failure
  case consecutive counting misses. It needs its own config shape
  rather than an overloaded `FailureThreshold`.
- A rate limiter. It is the fourth algorithm in this family and it was
  left out because token-bucket parameters are policy in a way the
  three here are not.
- Adaptive concurrency limiting, which needs a latency signal the seam
  does not currently carry.
