// Copyright Thesmos 2026
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

// budgetBuckets is how finely the budget window is divided.
//
// A single counter reset at the window boundary would let a caller
// that has just crossed one spend the whole budget twice over. Twelve
// buckets bound that error to a twelfth of the window while keeping
// the whole window in one cache line's worth of counters.
const budgetBuckets = 12

// RetryConfig has no defaults. Every field is required, because a
// retry policy that is wrong is wrong under load, which is the one
// time nobody is reading the code.
type RetryConfig struct {
	// Clock times the backoff. Under a virtual clock the intervals
	// are exact, so a retry test costs no wall-clock time.
	Clock clock.Clock

	// Rand draws the jitter. Any implementation will do: [Retrier]
	// serialises the draw, so a source that is not safe for concurrent
	// use is still safe here.
	Rand rand.Rand

	// Attempts is the total number of calls, not the number of
	// retries. Must be > 0; one means never retry.
	Attempts int

	// Base is the ceiling of the first backoff interval, doubling with
	// each retry. Must be > 0.
	Base time.Duration

	// Max caps that ceiling. Must be >= Base.
	Max time.Duration

	// Budget is the fraction of calls that may become retries, across
	// every caller sharing this Retrier, measured over BudgetWindow.
	// Must be >= 0.
	//
	// This is the protection: a failure affecting one call in a
	// thousand retries freely, one affecting everything retries almost
	// not at all. It is also unusable on its own below one call per
	// window, which is what MinRetries is for.
	Budget float64

	// MinRetries is the floor beneath that fraction: this many retries
	// are affordable within the window whatever the ratio says. Must
	// be >= 0.
	//
	// Without it a Retrier refuses the first retry it is ever asked
	// for at any Budget below 1.0 — one call cannot pay for one retry
	// at a fraction — so a caller behind thin traffic would never
	// retry at all, and the budget would protect a dependency that was
	// never at risk.
	//
	// Below the floor the attempt count is the only bound, which is
	// the right answer while the aggregate is too small to threaten
	// anything. Above it the ratio takes over.
	//
	// Both this and Budget at zero disables retrying entirely.
	MinRetries int

	// BudgetWindow is how far back the budget looks. Must be > 0 when
	// either Budget or MinRetries is.
	//
	// Long enough to span a dependency's recovery, short enough that
	// yesterday's traffic does not pay for today's retries.
	BudgetWindow time.Duration
}

// Retrier retries a call, bounded by both an attempt count and a
// budget.
//
// An attempt limit bounds one call and does nothing about the
// aggregate. A dependency that starts failing for everyone turns every
// in-flight call into Attempts calls, so the moment it is least able
// to serve traffic is the moment it receives several times as much,
// and the retries are what keep it down. The budget caps retries as a
// fraction of the overall call rate: a failure affecting one call in a
// thousand retries freely, one affecting everything retries almost not
// at all.
//
// The allowance over the window is max(MinRetries, Budget×calls): a
// floor while the traffic is too thin for a ratio to mean anything,
// and the ratio once it is not.
//
// That is why this is a type rather than a free function. A budget is
// state shared across calls, and a per-call value would bound nothing.
// One Retrier per dependency, shared by every caller of it.
//
// # Concurrency
//
// Safe for concurrent use.
type Retrier struct {
	clock clock.Clock
	rand  rand.Rand

	// start is the beginning of the bucket at cur.
	start time.Time

	attempts   int
	base       time.Duration
	max        time.Duration
	budget     float64
	minRetries int

	// bucket is BudgetWindow/budgetBuckets: the resolution at which
	// old traffic ages out.
	bucket time.Duration
	cur    int

	calls   [budgetBuckets]int
	retries [budgetBuckets]int

	// mu guards the ring above and serialises the jitter draw, so that
	// a Rand which is not safe for concurrent use still is here.
	mu sync.Mutex
}

// NewRetrier returns a Retrier over cfg.
//
// Returns [ErrConfig] when Clock or Rand is nil, Attempts is not
// positive, Base is not positive, Max is below Base, Budget or
// MinRetries is negative, or BudgetWindow is not positive while either
// of those is.
func NewRetrier(cfg RetryConfig) (*Retrier, error) {
	switch {
	case cfg.Clock == nil,
		cfg.Rand == nil,
		cfg.Attempts <= 0,
		cfg.Base <= 0,
		cfg.Max < cfg.Base,
		cfg.Budget < 0,
		cfg.MinRetries < 0,
		(cfg.Budget > 0 || cfg.MinRetries > 0) && cfg.BudgetWindow <= 0:
		return nil, ErrConfig
	}

	return &Retrier{
		clock:      cfg.Clock,
		rand:       cfg.Rand,
		attempts:   cfg.Attempts,
		base:       cfg.Base,
		max:        cfg.Max,
		budget:     cfg.Budget,
		minRetries: cfg.MinRetries,
		// A window shorter than the bucket count would divide to zero.
		bucket: max(cfg.BudgetWindow/budgetBuckets, 1),
		start:  cfg.Clock.Time(),
	}, nil
}

// Do calls fn until it succeeds, ctx ends, the attempts are exhausted,
// or the budget is spent.
//
// It stops early on any error [errs.Classify] does not report as
// [errs.Transient]: retrying an error the producer has called the
// caller's own fault cannot succeed, and spends budget that a call
// which might succeed then cannot have.
//
// A call whose context ended is never retried — the dependency did not
// refuse, the caller stopped asking — and the attempt's own error is
// what reaches the caller.
//
// When the budget refuses a retry, the returned error wraps both
// [ErrBudget] and the failure that prompted it, so a caller sees why
// it stopped and what it stopped on.
//
// # Composing with a breaker
//
// The breaker goes inside the retry:
//
//	resilience.Do(ctx, retrier, func(ctx context.Context) (T, error) {
//	    return resilience.Call(ctx, breaker, "inventory", fetch)
//	})
//
// so the breaker observes each attempt and [ErrOpen] — classified
// [errs.Transient] — is what the retry backs off against. Inverted,
// one logical call contributes Attempts failures to the circuit and
// opens it after a single bad request.
func Do[T any](
	ctx context.Context, r *Retrier,
	fn func(context.Context) (T, error),
) (T, error) {
	r.observe()

	var (
		v   T
		err error
	)

	for attempt := range r.attempts {
		if attempt > 0 {
			if !r.spend() {
				return v, fmt.Errorf("%w: %w", ErrBudget, err)
			}

			if werr := clock.Wait(ctx, r.clock, r.backoff(attempt)); werr != nil {
				return v, werr
			}
		}

		v, err = fn(ctx)
		if err == nil {
			return v, nil
		}

		if ctx.Err() != nil || !errs.Retryable(err) {
			return v, err
		}
	}

	return v, err
}

// Backoff returns the delay before retry n: zero for n below one, then
// a full-jitter draw from the interval [0, base<<(n-1)), capped at
// limit.
//
// Full jitter rather than a fixed fraction of the interval, because
// synchronised retry across a fleet is the failure backoff exists to
// prevent, and unjittered exponential backoff preserves it exactly. A
// draw of zero is a legitimate outcome, not a bug.
//
// A free function because it is a pure computation over four values,
// with no lifetime to manage. [Retrier] draws through its own lock;
// callers of this share r at their own discretion.
//
// # Allocation contract
//
// Zero alloc.
func Backoff(r rand.Rand, attempt int, base, limit time.Duration) time.Duration {
	if attempt < 1 {
		return 0
	}

	d := base
	for range attempt - 1 {
		// Doubling from here would pass the cap, and for a large
		// attempt count would overflow before it got there.
		if d > limit/2 {
			d = limit

			break
		}
		d *= 2
	}

	return time.Duration(rand.Float64(r) * float64(min(d, limit)))
}

// backoff draws the delay before the given attempt under r's lock.
func (r *Retrier) backoff(attempt int) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()

	return Backoff(r.rand, attempt, r.base, r.max)
}

// observe counts one logical call against the budget window.
func (r *Retrier) observe() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.roll()
	r.calls[r.cur]++
}

// spend claims one retry from the budget, reporting whether the window
// could pay for it.
func (r *Retrier) spend() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.roll()

	var calls, retries int
	for i := range budgetBuckets {
		calls += r.calls[i]
		retries += r.retries[i]
	}

	if float64(retries+1) > max(float64(r.minRetries), r.budget*float64(calls)) {
		return false
	}
	r.retries[r.cur]++

	return true
}

// roll ages the ring forward to the current bucket, clearing the ones
// it passes. Must be called with r.mu held.
func (r *Retrier) roll() {
	steps := int(r.clock.Time().Sub(r.start) / r.bucket)
	if steps <= 0 {
		return
	}

	r.start = r.start.Add(time.Duration(steps) * r.bucket)

	// Beyond a full lap every bucket is stale, so there is nothing to
	// gain from going round more than once.
	for range min(steps, budgetBuckets) {
		r.cur = (r.cur + 1) % budgetBuckets
		r.calls[r.cur] = 0
		r.retries[r.cur] = 0
	}
}
