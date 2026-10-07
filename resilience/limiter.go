// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package resilience

import (
	"context"
	"math"
	"sync"
	"time"

	"go.thesmos.sh/core/clock"
)

// maxBurst is the largest Burst of a [Limiter]: 2^62 nanounits, where a
// nanounit is a billionth of a unit. At a Rate of 1, a bucket of maxBurst
// units fills in 2^62 nanoseconds, about 146 years. The time that the
// bucket owes to waiting calls grows to the largest [time.Duration] only
// once the calls have reserved at least that long, and the Limiter then
// saturates it instead of letting it wrap.
const maxBurst = 4_611_686_018

// nanounits is the number of nanounits in one unit. The cost of n units at
// Rate r is n*nanounits/r nanoseconds, and n*nanounits fits an int64 for
// every n up to maxBurst.
const nanounits = 1_000_000_000

// LimiterConfig configures a [Limiter]. Clock and Rate are required, and
// Burst is required when Rate is positive. The zero LimiterConfig is
// invalid, because it has no Clock.
type LimiterConfig struct {
	// Clock measures the time that refills the bucket, and times the wait
	// of [Limiter.WaitN]. A test passes a fake clock and advances it.
	Clock clock.Clock

	// Rate is the number of units that the bucket gains per second. Zero
	// means no limit: every call takes its units at once, and Burst is
	// ignored. A negative Rate is invalid.
	Rate int64

	// Burst is the capacity of the bucket: the most units that one call
	// takes, and the most units that the bucket gains while no call takes
	// any. It must be between 1 and 4,611,686,018 when Rate is positive.
	Burst int64
}

// Limiter admits units of work at a rate, as a token bucket. The bucket
// gains Rate units per second up to Burst, and each call takes units from
// it. A new Limiter starts with a full bucket.
//
// The Limiter keeps the bucket as the generic cell rate algorithm does:
// one time, due, at which the bucket would be empty once it covered every
// unit taken so far. A call of n units moves due forward by the time in
// which the bucket gains n units, rounded up to a whole nanosecond, so the
// Limiter admits at most Rate units per second, and over many calls a few
// nanoseconds fewer. All the arithmetic is on int64 nanoseconds.
//
// A call that waits reserves its units when it starts, and the bucket owes
// them to it while it waits. Calls that wait together therefore take their
// units in the order in which they started, and a later call does not take
// units ahead of an earlier one. A call whose context ends gives its
// reservation back.
//
// The Limiter reads time from its Clock. A Clock that moves back adds no
// units until its time passes the last time that the Limiter read.
//
// The zero Limiter has a Rate of zero and admits every call, as
// [NewLimiter] does for a Rate of zero.
//
// # Concurrency
//
// Safe for concurrent use. One mutex guards due, and a call locks it only
// to read the clock and to move due, never while it waits.
//
// # Allocation contract
//
// [Limiter.AllowN] does not allocate. [Limiter.WaitN] allocates only the
// timer of a call that waits, through [clock.Clock.NewTimer], and nothing
// for a call that does not.
type Limiter struct {
	clock clock.Clock

	// start is the time of the Limiter's construction. Every other time of
	// the Limiter is a duration since start.
	start time.Time

	mu sync.Mutex

	// due is the duration since start at which the bucket would be empty
	// once it covered every unit taken so far: the theoretical arrival
	// time of the generic cell rate algorithm. The bucket is full while
	// due is at or before the current time. A write locks mu.
	due time.Duration

	// fill is the time in which an empty bucket gains Burst units.
	fill time.Duration

	// rate and burst are the Rate and Burst of the configuration. A rate
	// of zero means no limit, and burst is then zero.
	rate, burst int64
}

// NewLimiter returns a Limiter over cfg, with a full bucket. A Rate of
// zero returns a Limiter that admits every call.
//
// Error modes, each classified [go.thesmos.sh/core/errs.Invalid]:
//
//   - A nil Clock returns [ErrConfig].
//   - A negative Rate returns ErrConfig.
//   - A positive Rate with a Burst below 1 or above 4,611,686,018 returns
//     ErrConfig.
//
// # Allocation contract
//
// One allocation, the Limiter.
func NewLimiter(cfg LimiterConfig) (*Limiter, error) {
	if cfg.Clock == nil || cfg.Rate < 0 {
		return nil, ErrConfig
	}

	l := &Limiter{clock: cfg.Clock, start: cfg.Clock.Time(), rate: cfg.Rate}
	if cfg.Rate == 0 {
		return l, nil
	}

	if cfg.Burst < 1 || cfg.Burst > maxBurst {
		return nil, ErrConfig
	}

	l.burst = cfg.Burst
	l.fill = l.cost(cfg.Burst)

	return l, nil
}

// AllowN takes n units when the bucket has them, and reports whether it
// took them. It never waits. A call that reports false takes nothing.
//
// Units that a waiting [Limiter.WaitN] reserved are not in the bucket, so
// AllowN reports false while the reservations exceed what the bucket
// gained. An n of zero always fits.
//
// AllowN reports false for a negative n and for an n above Burst, which
// the bucket never contains. With a Rate of zero it reports true for every
// other n.
//
// # Allocation contract
//
// Zero alloc.
func (l *Limiter) AllowN(n int64) bool {
	if n < 0 {
		return false
	}

	if l.rate == 0 {
		return true
	}

	if n > l.burst {
		return false
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	due := addSaturated(max(l.due, now), l.cost(n))
	if due-now > l.fill {
		return false
	}

	l.due = due

	return true
}

// WaitN takes n units, and waits until the bucket has gained them. It
// returns nil once the units are taken.
//
// WaitN reserves the n units when it starts, so the bucket owes them to the
// call while it waits, and every later call waits behind the reservation.
// It then waits on a timer of its Clock for the time in which the bucket
// gains what it lacks. A call that the bucket covers at once starts no
// timer. A call whose context ends while it waits gives its reservation
// back, and a later call keeps the wait that it computed.
//
// Error modes, each of which takes no units:
//
//   - A ctx that has ended before the call returns the context's error,
//     unwrapped, whatever n is.
//   - A negative n returns [ErrUnits], classified
//     [go.thesmos.sh/core/errs.Invalid].
//   - An n above Burst returns ErrUnits. The caller splits such work into
//     calls of at most Burst units.
//   - A ctx that ends while the call waits returns the context's error,
//     unwrapped.
//
// With a Rate of zero, and for an n of zero, WaitN returns nil at once.
//
// # Allocation contract
//
// One timer, through [clock.Clock.NewTimer], for a call that waits, and
// nothing for a call that does not.
func (l *Limiter) WaitN(ctx context.Context, n int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if n < 0 || l.rate > 0 && n > l.burst {
		return ErrUnits
	}

	if l.rate == 0 || n == 0 {
		return nil
	}

	wait := l.reserve(n)
	if wait <= 0 {
		return nil
	}

	t := l.clock.NewTimer(wait)
	//dokimi:mutate-skip sbr-delete: a timer that runs no goroutine has no effect after WaitN returns that a test can observe
	defer t.Stop()

	select {
	case <-t.C():
		return nil
	case <-ctx.Done():
		l.release(n)

		return ctx.Err()
	}
}

// reserve takes n units, at most Burst, from the bucket, and returns how
// long the caller waits until the bucket has gained them: zero or less
// when the bucket has them. The bucket may owe units to the calls that
// reserved them, and due is then past the time at which the bucket would
// be full. reserve locks the mutex while it reads the clock and moves due.
func (l *Limiter) reserve(n int64) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.due = addSaturated(max(l.due, now), l.cost(n))

	return l.due - l.fill - now
}

// release gives back the n units of a reservation whose call stopped
// waiting, by moving due back by their cost. A call that reserved after it
// keeps the wait that it computed, which can only be longer than it needs
// to be. release locks the mutex while it moves due.
func (l *Limiter) release(n int64) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.due -= l.cost(n)
}

// now returns the duration since the Limiter's construction, read from its
// Clock. It is negative while the Clock is behind the start, and
// [time.Time.Sub] saturates it at the extremes of a Duration.
func (l *Limiter) now() time.Duration {
	return l.clock.Time().Sub(l.start)
}

// cost returns the time in which the bucket gains n units at the Limiter's
// rate, rounded up to a whole nanosecond, so the Limiter never charges a
// call less than its units take to accrue. n is between 0 and maxBurst and
// the rate is positive, so n*nanounits fits an int64 and the division is
// exact apart from the rounding.
func (l *Limiter) cost(n int64) time.Duration {
	c := n * nanounits
	d := c / l.rate
	if d*l.rate != c {
		d++
	}

	return time.Duration(d)
}

// addSaturated returns a+b, or the largest Duration when the sum does not
// fit an int64. b is not negative, so the sum wrapped exactly when it is
// below a.
func addSaturated(a, b time.Duration) time.Duration {
	if s := a + b; s >= a {
		return s
	}

	return math.MaxInt64
}
