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

`resilience` — circuit breaking, concurrency limiting, and retry with
jittered backoff, as state machines over `clock.Clock` and `rand.Rand`
so their behaviour is exact under a virtual clock and a seeded source.
Circuits are per-dependency. What counts as a failure is the caller's
judgement, because the transports that need this most do not report
failure as an error. Retries are bounded by a budget as well as an
attempt count, because the attempt count does not bound the aggregate.

## Motivation

Every caller of a remote dependency needs the same three things: stop
calling something that is failing, bound how much concurrency one slow
dependency can consume, and space out retries so a fleet does not
synchronise.

These are pure algorithms over time and randomness. They hold no
domain vocabulary, and they are re-derived per caller because there is
nowhere shared to put them. The derivations then disagree about the
questions that matter — whether a half-open circuit admits one probe
or a thousand, whether a cancelled caller counts as load shedding,
whether retries have any aggregate bound — and the disagreement is
invisible until an outage, when two callers behave differently against
the same failing dependency.

The specific reason they belong here rather than one layer up is
testability. Every one of these is a function of elapsed time, so
testing one requires controlling time. `core` is the only module where
`clock.Clock` and `rand.Rand` are both available without taking a
dependency.

Built anywhere else, a breaker's state transitions are tested by
sleeping past the open interval. That is slow, then flaky under load,
then skipped — which is why breaker bugs reach production. Under a
virtual clock the same test advances time by an exact amount and runs
in microseconds.

The standard library omits these because Go has no clock seam. `core`
has one, and that changes the calculus rather than repeating it.

## Detailed design

```go
// Package resilience holds the algorithms every caller of a remote
// dependency needs: circuit breaking, concurrency limiting, and
// retry.
//
// All three read time through [clock.Clock] and randomness through
// [rand.Rand], so behaviour is deterministic under a virtual clock
// and a seeded source. That is the property that makes them testable,
// and the reason they belong here rather than in each caller.
package resilience
```

### Circuits are per-dependency

A service calls several dependencies, and one being down says nothing
about the others. A single circuit shared across them either opens for
all when one fails, or never opens at all — so the unit is one circuit
per target, and the type holds a set of them.

```go
// Breaker holds one circuit per target.
type Breaker struct{ ... }

// BreakerConfig has no defaults. Every field is required, because a
// threshold that is wrong is wrong silently.
type BreakerConfig struct {
    Clock clock.Clock

    // FailureThreshold is the count of consecutive failures that
    // opens a circuit. Must be > 0.
    //
    // Consecutive rather than a rate: a dependency that fails N times
    // in a row is down, whereas a rate needs a window to be
    // meaningful and a window needs traffic to fill it.
    FailureThreshold int

    // OpenFor is how long a circuit stays open before admitting a
    // probe. Must be > 0.
    OpenFor time.Duration

    // SuccessThreshold is the count of consecutive probe successes
    // that closes a circuit again. Must be > 0.
    SuccessThreshold int

    // TripOn are the error classes [Call] counts as a dependency
    // failure. Must be non-empty. Ignored by Allow and Record, where
    // the caller judges for itself.
    TripOn []errs.Class
}

func NewBreaker(cfg BreakerConfig) (*Breaker, error)

// Allow reports whether a call to target may proceed, claiming the
// probe slot when the circuit has just become eligible for one.
//
// A caller that receives true MUST report the outcome with Record, or
// the circuit stays half-open and admits nothing further.
func (b *Breaker) Allow(target string) bool

// Record folds one outcome into target's circuit.
func (b *Breaker) Record(target string, failed bool)

// State reports a circuit's current state, for emission as a gauge.
func (b *Breaker) State(target string) State

// Call runs fn under target's circuit, judging failure by
// [errs.Classify] against cfg.TripOn.
func Call[T any](ctx context.Context, b *Breaker, target string,
    fn func(context.Context) (T, error)) (T, error)

// ErrOpen is returned instead of calling through. Classifies as
// [errs.Transient].
var ErrOpen = errors.New("resilience: circuit open")
```

### The caller judges what failed

`Allow` and `Record` are separate, rather than only offering `Call`,
because **the transports that need a breaker most do not report
failure as an error**.

Over HTTP a 5xx is a successful round trip carrying a bad status —
the error is nil. Over an RPC protocol the status may arrive in a
trailer that cannot be read without consuming the body. In both cases
the thing that knows a call failed is the caller, not the error value.

A breaker that only accepts an `error` is therefore unusable for the
cases it exists to protect, and a caller works around it by
synthesising an error purely to feed the breaker. Splitting `Allow`
from `Record` lets the caller state the judgement directly, and keeps
one state machine shared between callers that judge differently.

`Call` remains for the common case where failure genuinely is an
error, judging via `errs.Classify` against `TripOn`. An unclassified
error is ambiguous by construction — it may be a failing dependency or
a caller-side bug — so whether `errs.Unspecified` belongs in `TripOn`
is a required decision rather than a default in either direction.

`Call` never counts a failure when `ctx.Err() != nil`: the dependency
did not fail, the caller stopped waiting. Counting it would open a
circuit against a healthy dependency during a cancellation storm, and
the resulting `ErrOpen` responses would then look like the outage that
was not happening.

### Half-open admits exactly one probe

When `OpenFor` elapses, the circuit becomes eligible for a probe —
**one**. `Allow` claims that slot and returns false to every other
caller until `Record` reports the outcome.

Without the claim, "the open interval elapsed" would release the full
load at once, which is indistinguishable from never having opened for
a dependency that is still down. A failed probe re-opens for the full
interval rather than admitting the next caller immediately, for the
same reason.

`SuccessThreshold` then governs closing: a dependency that answers one
request and falls over should not get the whole traffic back.

### Bulkhead: limit, queue and wait are three things

```go
// Bulkhead bounds concurrent execution.
type Bulkhead struct{ ... }

// BulkheadConfig requires Limit. Queue and Wait have meaningful zero
// values.
type BulkheadConfig struct {
    Clock clock.Clock

    // Limit is how many calls may be in flight at once. Must be > 0.
    //
    // Size it from the dependency's capacity, not from this caller's
    // traffic: letting more through only converts the dependency's
    // queue into this process's queue.
    Limit int

    // Queue is how many callers may wait for a permit. Zero means
    // none — a call arriving at the limit is rejected immediately.
    //
    // A queue trades latency for fewer rejections and only pays when
    // overload is brief. A permanently saturated dependency with a
    // deep queue serves everyone slowly and fails them anyway, having
    // wasted the wait.
    Queue int

    // Wait bounds how long a queued caller waits. Zero leaves it
    // bounded only by the caller's context, which is right only where
    // every caller sets a deadline.
    Wait time.Duration
}

func NewBulkhead(cfg BulkheadConfig) (*Bulkhead, error)

// Acquire takes a permit, returning the release for it.
func (b *Bulkhead) Acquire(ctx context.Context) (release func(), err error)

// InFlight and Queued are point-in-time samples, for a gauge.
func (b *Bulkhead) InFlight() int
func (b *Bulkhead) Queued() int

var (
    // ErrFull means the limit and the queue were both full.
    ErrFull = errors.New("resilience: bulkhead limit and queue are full")

    // ErrWaitTimeout means the caller waited its full allowance.
    ErrWaitTimeout = errors.New("resilience: timed out waiting for a permit")
)
```

Concurrency is the quantity to bound, not rate: the same arrival rate
against a dependency ten times slower is ten times the occupancy. A
timeout bounds one call and says nothing about how many are in flight;
a breaker acts only once a dependency has clearly failed, and one that
is merely slow never trips it.

**A cancelled caller is not a rejection.** `Acquire` returns
`ctx.Err()` distinct from `ErrFull` and `ErrWaitTimeout`, because a
client-side cancellation storm would otherwise be indistinguishable
from a saturated dependency in every metric derived from these errors.

**Release is one-shot.** Releasing twice would hand back a permit that
was never held, silently raising the effective limit for every other
caller. The second call is a no-op rather than a panic — this module
does not panic — but the doc states plainly that it indicates a bug.

### Retry needs a budget, not only an attempt count

```go
// Retrier retries a call, bounded by both an attempt count and a
// budget.
type Retrier struct{ ... }

// RetryConfig has no defaults.
type RetryConfig struct {
    Clock clock.Clock
    Rand  rand.Rand

    // Attempts is the total number of calls, not the number of
    // retries. Must be > 0.
    Attempts int

    // Base is the first backoff interval; Max caps it. Both must
    // be > 0, and Max must be >= Base.
    Base, Max time.Duration

    // Budget is the fraction of calls that may become retries, across
    // every caller sharing this Retrier. Must be >= 0; zero disables
    // retrying entirely.
    Budget float64
}

func NewRetrier(cfg RetryConfig) (*Retrier, error)

// Do calls fn until it succeeds, ctx ends, attempts are exhausted, or
// the budget is spent, stopping early on any error errs.Classify
// reports as non-retryable.
func Do[T any](ctx context.Context, r *Retrier,
    fn func(context.Context) (T, error)) (T, error)

// Backoff returns the delay before attempt n: exponential from base,
// capped at max, with full jitter drawn from r.
func Backoff(r rand.Rand, attempt int, base, max time.Duration) time.Duration
```

An attempt limit bounds one call and does nothing about the aggregate.
A dependency that starts failing for everyone turns every in-flight
call into `Attempts` calls — so the moment it is least able to serve
traffic is the moment it receives several times as much, and the
retries are what keep it down.

The budget caps retries as a fraction of the overall call rate. A
failure affecting one call in a thousand retries freely; one affecting
everything retries almost not at all. **The attempt limit is a safety
rail; the budget is the protection.**

That is why `Retrier` is a type rather than a free function: a budget
is state shared across calls, and a per-call value would bound
nothing.

**Jitter is not optional.** Synchronised retry across a fleet is the
failure mode backoff exists to prevent, and unjittered exponential
backoff preserves it exactly. `Backoff` stays a free function — a pure
computation over four values, with no lifetime to manage.

### No defaults on policy

Every threshold is required at construction. A wrong default in a
foundation is wrong in every caller and never revisited, because
nobody reviews a value they did not write. An absent one is answered
once, deliberately, per call site.

The exceptions are `Queue` and `Wait`, whose zero values are
themselves meaningful answers — no queue, and no bound beyond the
caller's context.

### Composing a breaker with a retry

The breaker goes inside the retry:

```go
resilience.Do(ctx, retrier, func(ctx context.Context) (T, error) {
    return resilience.Call(ctx, breaker, "inventory", fetch)
})
```

so the breaker observes each attempt and `ErrOpen` — classified
`errs.Transient` — is what the retry backs off against. Inverted, one
logical call contributes `Attempts` failures to the circuit and opens
it after a single bad request.

This is documented rather than enforced. Taking a `*Breaker` in
`RetryConfig` would make the composition mandatory for callers who
want only one of the two.

## Alternatives considered

### A. Leave these to each caller

**Why not:** they are re-derived per caller today and the derivations
disagree on exactly the questions that matter. The disagreement
surfaces during an outage.

### B. One circuit rather than one per target

**Why not:** it either opens for every dependency when one fails, or
never opens. Callers would each build the keyed map themselves, and
each would get the locking and the unbounded growth wrong separately.

### C. Judge failure only from the error, via `errs.Class`

**Why not:** over HTTP and over RPC the failure is not an error. A
breaker that can only see errors is unusable for the transports that
need it most, and callers would synthesise errors to feed it.

### D. A configurable retry-policy framework

**Why not:** strategies, predicates and composable policies are where
a resilience library becomes a framework. `Do` has required parameters
and no extension points; a caller needing something else writes the
loop.

### E. Take `time.Now` directly instead of `clock.Clock`

**Why not:** it forfeits the entire reason these belong in `core`.
Testing a breaker against the wall clock means sleeping past
`OpenFor`, which makes the test slow, then flaky, then skipped.

## Drawbacks

- Three types with eleven required configuration fields between them.
  The simplest possible use is verbose, which is the intended trade
  and will be experienced as friction.
- `Allow`/`Record` can be misused: a caller that takes a probe slot
  and never records leaves the circuit half-open forever. `Call` is
  safe but does not cover the transports that most need the split.
- Circuits and their per-target state are never evicted, so a
  `Breaker` keyed on anything a client supplies is a memory leak. The
  doc says so; nothing enforces it.
- The retry budget is a property of a `Retrier`, so callers sharing
  one share a budget. That is the point, and it means the sharing
  boundary is a decision nobody is prompted to make.
- `resilience` depends on `errs`, `clock` and `rand`, making it the
  most connected package in the module.
- Consecutive-failure counting never opens for a dependency failing
  40% of the time. That is the simplest correct rule, not the best
  one.

## Open questions

- Should the budget be a token bucket over a sliding window, or a
  ratio over a fixed window? The first is smoother and holds more
  state; the second is easier to reason about and has edge effects at
  the window boundary.
- Should `Breaker` bound its circuit count — a maximum, or an idle
  timeout — or is documenting the unbounded growth enough?

## Unresolved / future work

- A windowed or rate-based breaker for the partial-failure case
  consecutive counting misses.
- A rate limiter. It is the fourth algorithm in this family, left out
  because token-bucket parameters are policy in a way these are not.
- Per-key bulkhead groups, for fairness between callers sharing a
  dependency. The keying is the same shape as `Breaker`'s targets and
  should be decided once for both.
