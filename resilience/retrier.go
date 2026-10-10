// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package resilience

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/rand"
)

// budgetBuckets is the number of buckets of the budget window. A single
// counter that resets at the end of the window lets a caller spend the
// budget twice across the reset. Twelve buckets bound that error to a
// twelfth of the window.
const budgetBuckets = 12

// RetryConfig configures a [Retrier]. No field has a default, because a
// wrong retry policy shows only under load.
type RetryConfig struct {
	// Clock times the backoff. A test passes a fake clock, so a retry costs
	// no wall-clock time.
	Clock clock.Clock

	// Rand supplies the jitter. [Retrier] calls it under its lock, so a
	// source that is not safe for concurrent use works too.
	Rand rand.Rand

	// Attempts is the total number of calls, not the number of retries. It
	// must be positive, and 1 means no retry.
	Attempts int

	// Base is the ceiling of the first backoff, which doubles with each
	// retry. It must be positive.
	Base time.Duration

	// Max caps the ceiling of the backoff. It must be at least Base.
	Max time.Duration

	// MaxRetryAfter is the longest delay that a retry waits for when
	// [errs.RetryAfter] reports one for a failure. [Retrier.Retry] refuses
	// a failure whose delay is longer, and [Do] returns it instead of
	// waiting. It must not be negative, and zero makes Do return every
	// failure with a delay.
	MaxRetryAfter time.Duration

	// Budget is the fraction of calls that may become retries, across every
	// caller of the Retrier, over BudgetWindow. It must not be negative.
	//
	// A failure of one call in a thousand retries freely, and a failure of
	// every call retries almost never. Below one call per window the
	// fraction allows no retry, and MinRetries sets the floor.
	Budget float64

	// MinRetries is the number of retries that the window allows whatever
	// the fraction allows. It must not be negative.
	//
	// Without it, a Retrier at a Budget below 1 refuses the first retry that
	// it is asked for, because one call cannot afford one retry at a
	// fraction, and a caller of thin traffic never retries. Below the floor
	// the attempt count alone bounds the retries, and above it the fraction
	// applies. A Budget and a MinRetries of zero turn retries off.
	MinRetries int

	// BudgetWindow is the time over which the budget counts calls and
	// retries. It must be positive when Budget or MinRetries is. A window
	// spans the recovery of a dependency, and is short enough that old
	// traffic does not fund new retries.
	BudgetWindow time.Duration
}

// Retrier retries a call, bounded by an attempt count and a budget.
//
// An attempt count bounds one call, not the load of every caller. When a
// dependency fails for every caller, each call in flight becomes Attempts
// calls, so the dependency receives several times its load when it is
// least able to serve it. The budget caps the retries at a fraction of the
// calls: over the window, the allowance is max(MinRetries, Budget×calls).
//
// A budget is state that the calls share, so Retrier is a type and not a
// function. A process keeps one Retrier per dependency, and every caller of
// the dependency shares it.
//
// [Do] makes the attempts of a call and waits each backoff on the
// goroutine of its caller. A caller that schedules its own waits, such as
// an event loop on one goroutine, makes the attempts itself. It calls
// [Retrier.Start] once per call and [Retrier.Retry] after each failed
// attempt, and waits the delays that Retry returns. Both kinds of caller
// share the budget.
//
// # Concurrency
//
// Safe for concurrent use.
type Retrier struct {
	clock clock.Clock
	rand  rand.Rand

	// start is the beginning of the bucket at cur.
	start time.Time

	attempts      int
	base          time.Duration
	max           time.Duration
	maxRetryAfter time.Duration
	budget        float64
	minRetries    int

	// bucket is BudgetWindow/budgetBuckets: the resolution at which old
	// traffic leaves the window.
	bucket time.Duration
	cur    int

	calls   [budgetBuckets]int
	retries [budgetBuckets]int

	// mu guards the ring and serialises the calls of rand, so that a Rand
	// that is not safe for concurrent use works.
	mu sync.Mutex
}

// NewRetrier returns a Retrier of cfg.
//
// Error modes: a nil Clock or Rand, an Attempts or a Base that is not
// positive, a Max below Base, a negative MaxRetryAfter, Budget or
// MinRetries, and a BudgetWindow that is not positive while Budget or
// MinRetries is positive return [ErrConfig], classified Invalid.
func NewRetrier(cfg RetryConfig) (*Retrier, error) {
	if cfg.Clock == nil ||
		cfg.Rand == nil ||
		cfg.Attempts <= 0 ||
		cfg.Base <= 0 ||
		cfg.Max < cfg.Base ||
		cfg.MaxRetryAfter < 0 ||
		cfg.Budget < 0 ||
		cfg.MinRetries < 0 ||
		((cfg.Budget > 0 || cfg.MinRetries > 0) && cfg.BudgetWindow <= 0) {

		return nil, ErrConfig
	}

	return &Retrier{
		clock:         cfg.Clock,
		rand:          cfg.Rand,
		attempts:      cfg.Attempts,
		base:          cfg.Base,
		max:           cfg.Max,
		maxRetryAfter: cfg.MaxRetryAfter,
		budget:        cfg.Budget,
		minRetries:    cfg.MinRetries,
		// A window shorter than the bucket count would divide to zero.
		bucket: max(cfg.BudgetWindow/budgetBuckets, 1),
		start:  cfg.Clock.Time(),
	}, nil
}

// Start counts one call in the budget window of r, as [Do] does before the
// first attempt of a call. A caller that makes the attempts of a call
// itself calls Start once per call, before the first attempt. The calls
// that Start counts fund the retries of every caller of r.
//
// Start cannot fail.
//
// # Allocation contract
//
// Zero alloc.
func (r *Retrier) Start() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.roll()
	r.calls[r.cur]++
}

// Retry decides whether a call may make attempt after err, the failure of
// the attempt before it. The first attempt of a call is attempt 0, so the
// first retry is attempt 1, as for [Backoff]. Retry returns the delay to
// wait before attempt: the larger of its backoff and the delay that
// [errs.RetryAfter] reports for err.
//
// Retry returns err unchanged, and takes nothing from the budget, when
// [errs.Classify] does not report err as [errs.Transient], when attempt is
// below 1 or not below Attempts, and when the delay of err exceeds
// MaxRetryAfter. When the budget refuses the retry, Retry returns an error
// that wraps [ErrBudget] and err. Otherwise it takes one retry from the
// budget and returns a nil error.
//
// Retry returns at once. The caller waits the delay, and does not make the
// attempt once the context of the call has ended. A caller calls Retry only
// after a failed attempt: for a nil err, Retry returns 0 and a nil error,
// and takes nothing from the budget.
//
// # Allocation contract
//
// Zero alloc, apart from the error of a retry that the budget refuses.
func (r *Retrier) Retry(attempt int, err error) (time.Duration, error) {
	if attempt < 1 || attempt >= r.attempts || !errs.Retryable(err) {
		return 0, err
	}

	delay, _ := errs.RetryAfter(err)
	if delay > r.maxRetryAfter {
		return 0, err
	}

	backoff, ok := r.spend(attempt)
	if !ok {
		return 0, fmt.Errorf("%w: %w", ErrBudget, err)
	}

	return max(backoff, delay), nil
}

// Do calls fn until it succeeds, ctx ends, the attempts run out, or the
// budget refuses a retry. It counts the call with [Retrier.Start], decides
// each retry with [Retrier.Retry], and waits each delay on the clock of r.
//
// Do stops at an error that [errs.Classify] does not report as
// [errs.Transient]. A retry of an error that its producer attributes to the
// caller cannot succeed, and spends budget that a call that can succeed
// then lacks.
//
// Do does not retry a call whose ctx ended, because the caller stopped
// asking and the dependency did not refuse. It returns the error of that
// attempt.
//
// When the budget refuses a retry, the error wraps [ErrBudget] and the
// failure before it, so the caller sees why Do stopped and on what.
//
// # Delays
//
// When [errs.RetryAfter] reports a delay for a failure, Do waits the longer
// of the delay and its backoff before the next attempt. Do returns a failure
// whose delay exceeds MaxRetryAfter at once, and errs.RetryAfter reports the
// delay of the returned error, so the caller can schedule the attempt.
//
// # Idempotency
//
// A failure that [errs.Classify] reports as [errs.Transient] does not mean
// that fn had no effect. A timeout can arrive after the dependency applied a
// write, and Do then calls fn again. fn must be safe to repeat, for example
// with the same idempotency key in every attempt.
//
// # Composing with a breaker
//
// The breaker goes inside the retry:
//
//	resilience.Do(ctx, retrier, func(ctx context.Context) (T, error) {
//	    return resilience.Call(ctx, breaker, "inventory", fetch)
//	})
//
// The breaker then counts each attempt, and the retry backs off against
// [ErrOpen], which classifies as [errs.Transient]. Inverted, one call adds
// Attempts failures to the circuit, which then opens after one bad request.
//
// # Allocation contract
//
// Zero alloc for a call whose first attempt succeeds, apart from what fn
// allocates.
func Do[T any](
	ctx context.Context, r *Retrier,
	fn func(context.Context) (T, error),
) (T, error) {
	r.Start()

	for next := 1; ; next++ {
		v, err := fn(ctx)
		if err == nil {
			return v, nil
		}

		if ctx.Err() != nil {
			return v, err
		}

		delay, rerr := r.Retry(next, err)
		if rerr != nil {
			return v, rerr
		}

		if werr := clock.Wait(ctx, r.clock, delay); werr != nil {
			return v, werr
		}
	}
}

// Backoff returns the delay before retry attempt: zero for an attempt below
// 1, and otherwise a draw of r from [0, min(base<<(attempt-1), limit)).
//
// The jitter covers the whole interval, because the retries of a fleet that
// fail together and retry together are the failure that a backoff prevents,
// and an exponential backoff without jitter keeps them together. A draw of
// zero is a valid delay.
//
// Backoff is a function of its four values. [Retrier] calls it under its
// lock, and another caller shares r as the contract of r allows.
//
// # Allocation contract
//
// Zero alloc.
func Backoff(r rand.Rand, attempt int, base, limit time.Duration) time.Duration {
	if attempt < 1 {
		return 0
	}

	// d starts at limit or below, so no step of the loop exceeds limit.
	d := min(base, limit)
	for range attempt - 1 {
		// A doubling beyond limit/2 passes the cap, and for a large attempt
		// count it would overflow.
		if d > limit/2 {
			d = limit

			break
		}
		d *= 2
	}

	return time.Duration(rand.Float64(r) * float64(d))
}

// spend takes one retry from the budget and returns the backoff of attempt,
// both under the lock of r. When the window does not allow the retry, it
// reports false and does not compute a backoff.
func (r *Retrier) spend(attempt int) (time.Duration, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.roll()

	var calls, retries int
	for i := range budgetBuckets {
		calls += r.calls[i]
		retries += r.retries[i]
	}

	if float64(retries+1) > max(float64(r.minRetries), r.budget*float64(calls)) {
		return 0, false
	}
	r.retries[r.cur]++

	return Backoff(r.rand, attempt, r.base, r.max), true
}

// roll moves the ring forward to the current bucket, and clears the buckets
// that it passes. The caller locks r.mu.
func (r *Retrier) roll() {
	steps := int(r.clock.Time().Sub(r.start) / r.bucket)
	if steps < 1 {
		return
	}

	r.start = r.start.Add(time.Duration(steps) * r.bucket)

	// After a full lap every bucket is stale, so roll goes round at most
	// once.
	for range min(steps, budgetBuckets) {
		r.cur = (r.cur + 1) % budgetBuckets
		r.calls[r.cur] = 0
		r.retries[r.cur] = 0
	}
}
