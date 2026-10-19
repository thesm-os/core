// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package resilience

import (
	"context"
	"math"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"go.thesmos.sh/core/clock"
)

// maxBurst is the largest Burst of a [Limiter]: 2^62 nanounits, where a
// nanounit is a billionth of a unit. At a Rate of 1, a bucket of maxBurst
// units fills in 2^62 nanoseconds, about 146 years. The time that the
// bucket owes to waiting calls grows to the largest [time.Duration] only
// once the calls have taken at least that long, and the Limiter then
// saturates it instead of letting it wrap.
const maxBurst = 4_611_686_018

// nanounits is the number of nanounits in one unit. The cost of n units at
// Rate r is n*nanounits/r nanoseconds, and n*nanounits fits an int64 for
// every n up to maxBurst.
const nanounits = 1_000_000_000

// shareRun is the number of grants in a row to calls of [PriorityHigh]
// while a call of [PriorityNormal] waits, after which a [Limiter] grants
// the oldest call of PriorityNormal. One grant in ten then goes to a call
// of PriorityNormal while calls of both priorities wait.
const shareRun = 9

// LimiterConfig configures a [Limiter]. Clock and Rate are required, and
// Burst is required when Rate is positive. The zero LimiterConfig is
// invalid, because it has no Clock.
type LimiterConfig struct {
	// Clock measures the time that refills the bucket, and times the wait
	// of [Limiter.WaitNPriority]. A test passes a fake clock and advances
	// it.
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

// Priority orders the calls of a [Limiter] that wait for units. A waiting
// call of [PriorityHigh] takes its units before the waiting calls of
// [PriorityNormal], apart from one grant in ten, which goes to the oldest
// call of PriorityNormal while calls of both priorities wait.
type Priority uint8

const (
	// PriorityNormal is the priority of [Limiter.WaitN], such as the
	// priority of the chunks of a compaction.
	PriorityNormal Priority = 0

	// PriorityHigh is the priority of a call that other work waits for,
	// such as a flush that the writers of a volume wait for.
	PriorityHigh Priority = 1
)

// Valid reports whether p is PriorityNormal or PriorityHigh.
func (p Priority) Valid() bool {
	return p <= PriorityHigh
}

// Limiter admits units of work at a rate, as a token bucket. The bucket
// gains Rate units per second up to Burst, and each call takes units from
// it. A new Limiter starts with a full bucket. [Limiter.SetRate] changes the
// Rate and the Burst, and the bucket keeps its units up to the new Burst.
//
// The Limiter keeps the bucket as the generic cell rate algorithm does:
// one time, due, at which the bucket would be empty once it covered every
// unit taken so far. A call of n units moves due forward by exactly the
// time in which the bucket gains n units. The Limiter keeps each such time
// as whole nanoseconds and a remainder in Rate-ths of a nanosecond, so the
// bucket never has more than Burst units. Only the timer of a wait rounds,
// up to the next whole nanosecond, so the Limiter admits at most Rate units
// per second. All the arithmetic is on int64.
//
// A call that the bucket covers while no call waits takes its units at
// once. Every other call of [Limiter.WaitNPriority] joins the queue of its
// [Priority], in the order in which the calls started, and the Limiter
// grants one waiting call at a time. The call that it grants next, the
// head, is the oldest call of [PriorityHigh]. It is the oldest call of
// [PriorityNormal] when no call of PriorityHigh waits, and after nine
// grants in a row to calls of PriorityHigh while a call of PriorityNormal
// waited. The head waits on a timer of the Clock until the bucket covers its
// units, and then takes them. Every other waiting call waits until it is the
// head. A call of PriorityHigh that starts while a call of PriorityNormal is
// the head therefore becomes the head, unless that call has the tenth
// grant. A call whose context ends leaves its queue, and took no units.
//
// The Limiter reads time from its Clock. A Clock that moves back adds no
// units until its time passes the last time that the Limiter read.
//
// The zero Limiter has a Rate of zero and admits every call, as
// [NewLimiter] does for a Rate of zero.
//
// # Concurrency
//
// Safe for concurrent use. One mutex guards the Rate, the Burst, due, the
// queues and the count of the grants to calls of PriorityHigh. A call locks
// it to read the Rate and the Burst, to join or leave a queue, to take its
// units and to choose the head, and never while it waits. [Limiter.Burst]
// reads a copy of the Burst from an atomic, and takes no lock.
//
// # Allocation contract
//
// [Limiter.AllowN], [Limiter.SetRate] and [Limiter.Burst] do not allocate,
// and neither does a call of [Limiter.WaitN] or WaitNPriority that does not
// wait. A call that waits allocates its place in its queue and its channel,
// and a timer through [clock.Clock.NewTimer] each time it becomes the head.
type Limiter struct {
	clock clock.Clock

	// start is the time of the Limiter's construction. Every other time of
	// the Limiter is a duration since start.
	start time.Time

	// head is the waiting call that the Limiter grants next, or nil when no
	// call waits. A write locks mu.
	head *waiter

	// queues are the waiting calls of each Priority, in the order in which
	// they started. A write locks mu.
	queues [2][]*waiter

	mu sync.Mutex

	// due and dueRem are the duration since start at which the bucket would
	// be empty once it covered every unit taken so far: the theoretical
	// arrival time of the generic cell rate algorithm. The time is due plus
	// dueRem Rate-ths of a nanosecond, and dueRem is below rate. The bucket
	// is full while the time is at or before the current time. A write
	// locks mu.
	due    time.Duration
	dueRem int64

	// fill and fillRem are the time in which an empty bucket gains Burst
	// units: fill plus fillRem Rate-ths of a nanosecond. A write locks mu.
	fill    time.Duration
	fillRem int64

	// highs is the number of grants in a row to calls of PriorityHigh while
	// a call of PriorityNormal waited. A write locks mu.
	highs int

	// rate and burst are the Rate and the Burst of the configuration or of
	// the last SetRate. A rate of zero means no limit, and burst, due and
	// fill then have no meaning. A read and a write lock mu.
	rate, burst int64

	// limit is the Burst that Burst returns, and zero while no limit
	// applies. NewLimiter and SetRate store it with the burst, and Burst
	// loads it without the lock.
	limit atomic.Int64
}

// waiter is a call of [Limiter.WaitNPriority] that waits for its units.
type waiter struct {
	// wake receives a value when the call becomes the head, stops being the
	// head or ends. It has room for one value, so a send under the mutex
	// never blocks.
	wake chan struct{}

	// err is the result of a call that [Limiter.SetRate] ended. It is nil
	// for a grant at a Rate of zero, and [ErrUnits] for more units than the
	// new Burst. A write locks mu.
	err error

	// n is the number of units of the call.
	n int64

	// priority is the priority of the call.
	priority Priority

	// done reports that SetRate removed the call from its queue, and that
	// err is its result. A write locks mu.
	done bool
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
	l.limit.Store(cfg.Burst)
	l.fill, l.fillRem = l.cost(cfg.Burst)

	return l, nil
}

// AllowN takes n units when the bucket has them and no call waits, and
// reports whether it took them. It never waits. A call that reports false
// takes nothing.
//
// AllowN reports false while a call of [Limiter.WaitNPriority] waits, so a
// caller that polls does not take units ahead of a waiting call. An n of
// zero always fits.
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

	if n == 0 {
		return true
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	return l.rate == 0 || n <= l.burst && l.head == nil && l.claim(n) <= 0
}

// WaitN takes n units at [PriorityNormal], and waits until the Limiter
// grants them. [Limiter.WaitNPriority] documents the wait, the errors and
// the allocations.
func (l *Limiter) WaitN(ctx context.Context, n int64) error {
	return l.WaitNPriority(ctx, n, PriorityNormal)
}

// WaitNPriority takes n units at the priority p, and waits until the
// Limiter grants them. It returns nil once the units are taken.
//
// A call that the bucket covers while no call waits takes its units at
// once and starts no timer. Every other call joins the queue of p, and
// takes its units once it is the head and the bucket covers them, as
// [Limiter] documents. A call whose context ends while it waits leaves its
// queue.
//
// Error modes, each of which takes no units:
//
//   - A ctx that has ended before the call returns the context's error,
//     unwrapped, whatever n and p are.
//   - A negative n returns [ErrUnits], classified
//     [go.thesmos.sh/core/errs.Invalid].
//   - A p other than [PriorityNormal] and [PriorityHigh] returns
//     [ErrPriority], classified errs.Invalid.
//   - An n above Burst returns ErrUnits. The caller splits such work into
//     calls of at most Burst units.
//   - A ctx that ends while the call waits returns the context's error,
//     unwrapped.
//   - A [Limiter.SetRate] to a Burst below n while the call waits returns
//     ErrUnits.
//
// With a Rate of zero, and for an n of zero, WaitNPriority returns nil at
// once. A SetRate to a Rate of zero returns nil to a call that waits.
//
// # Allocation contract
//
// Zero alloc for a call that does not wait. A call that waits allocates its
// place in its queue and its channel, and a timer through
// [clock.Clock.NewTimer] each time it becomes the head.
func (l *Limiter) WaitNPriority(ctx context.Context, n int64, p Priority) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if n < 0 {
		return ErrUnits
	}

	if !p.Valid() {
		return ErrPriority
	}

	if n == 0 {
		return nil
	}

	l.mu.Lock()
	if l.rate > 0 && n > l.burst {
		l.mu.Unlock()

		return ErrUnits
	}
	if l.rate == 0 || l.head == nil && l.claim(n) <= 0 {
		l.mu.Unlock()

		return nil
	}
	w := &waiter{wake: make(chan struct{}, 1), n: n, priority: p}
	l.queues[p] = append(l.queues[p], w)
	l.choose()
	l.mu.Unlock()

	return l.wait(ctx, w)
}

// SetRate changes the Rate and the Burst of l to rate and burst, which
// follow the rules of [LimiterConfig]. The bucket keeps the units that it
// has at the time of the call, up to the new Burst, and gains rate units per
// second from then on. A larger Burst fills at the new rate as well. A
// policy that sets the same rate at every interval admits the units of one
// Limiter, without a burst at each change.
//
// The waiting calls keep their places in their queues, and the head waits
// for the units that it lacks at the new rate. A waiting call of more units
// than burst returns [ErrUnits], because the bucket never contains them. A
// rate of zero means no limit, so every waiting call returns nil. A change
// from a rate of zero starts with a full bucket, as [NewLimiter] does.
//
// A Clock that moves back can leave the bucket below empty. SetRate keeps
// the time at which such a bucket is empty, and the bucket gains units at
// the new rate after that time.
//
// Error modes, each classified [go.thesmos.sh/core/errs.Invalid], and each
// of which leaves l unchanged:
//
//   - A negative rate returns [ErrConfig].
//   - A positive rate with a burst below 1 or above 4,611,686,018 returns
//     ErrConfig.
//   - A positive rate returns ErrConfig for the zero Limiter, which has no
//     Clock.
//
// # Allocation contract
//
// Zero alloc.
func (l *Limiter) SetRate(rate, burst int64) error {
	if rate < 0 || rate > 0 && (burst < 1 || burst > maxBurst) {
		return ErrConfig
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if rate > 0 && l.clock == nil {
		return ErrConfig
	}

	for p := range l.queues {
		l.queues[p] = slices.DeleteFunc(l.queues[p], func(w *waiter) bool {
			if rate > 0 && w.n <= burst {
				return false
			}
			w.done = true
			if rate > 0 {
				w.err = ErrUnits
			}
			select {
			case w.wake <- struct{}{}:
			default:
			}

			return true
		})
	}
	if len(l.queues[PriorityNormal]) == 0 {
		l.highs = 0
	}
	// A head computes its wait again under the new configuration.
	l.head = nil
	l.choose()

	if rate == 0 {
		l.rate = 0
		l.limit.Store(0)

		return nil
	}

	// owed is the number of nanounits that the bucket lacks at now. below is
	// the time until a bucket below empty is empty, rounded up to a whole
	// nanosecond. A bucket without a limit is full.
	now := l.now()
	var owed int64
	var below time.Duration
	if l.rate > 0 {
		below = l.due - l.fill - now
		if l.dueRem > l.fillRem {
			below++
		}
		if below > 0 {
			owed = l.burst * nanounits
		} else if l.due >= now {
			owed = int64(l.due-now)*l.rate + l.dueRem
		}
		owed = max(owed+(burst-l.burst)*nanounits, 0)
	}

	l.rate, l.burst = rate, burst
	l.limit.Store(burst)
	l.fill, l.fillRem = l.cost(burst)
	l.due = addSaturated(addSaturated(now, max(below, 0)), time.Duration(owed/rate))
	l.dueRem = owed % rate

	return nil
}

// Burst returns the Burst of the configuration or of the last
// [Limiter.SetRate]. One call of [Limiter.AllowN] or [Limiter.WaitNPriority]
// takes at most that many units, so a caller splits work into calls of
// min(left, l.Burst()) units. A SetRate between Burst and the call can lower
// the Burst. WaitNPriority then returns [ErrUnits] and AllowN reports false,
// and the caller reads Burst again.
//
// A Limiter with a Rate of zero takes any number of units in one call, and
// so does the zero Limiter. Burst returns [math.MaxInt64] for both, so the
// loop needs no case for a Limiter without a limit.
//
// Burst takes no lock. It loads the copy of the Burst that NewLimiter and
// SetRate store in an atomic.
//
// # Allocation contract
//
// Zero alloc.
func (l *Limiter) Burst() int64 {
	if b := l.limit.Load(); b > 0 {
		return b
	}

	return math.MaxInt64
}

// wait waits until the waiting call w takes its units, SetRate ends it or
// ctx ends. While w is the head, it waits on a timer for the time in which
// the bucket gains the units that it lacks, and then takes them. A timer
// that ends before the bucket covers the units, because the Clock moved
// back, leads to another timer. While w is not the head, it waits on its
// channel. The end of ctx ends either wait.
//
// A wake reports that w became the head, stopped being the head or ended,
// and a grant to another call can have moved due meanwhile. Each pass of the
// loop ends with a wake, the end of its timer or the end of ctx, and stops
// its timer. The next pass first returns the result of a call that SetRate
// ended. It then returns the error of an ended ctx, and otherwise computes
// the wait of a head again. A call that SetRate ended returns the result of
// SetRate also when its ctx has ended.
func (l *Limiter) wait(ctx context.Context, w *waiter) error {
	for {
		l.mu.Lock()
		// The state below is the state after every wake that arrived before
		// the lock, so such a wake would only repeat the pass.
		select {
		case <-w.wake:
		default:
		}
		if w.done {
			l.mu.Unlock()

			return w.err
		}
		if err := ctx.Err(); err != nil {
			l.leave(w)
			l.mu.Unlock()

			return err
		}
		var (
			t    clock.Timer
			tick <-chan time.Time
		)
		if l.head == w {
			wait := l.claim(w.n)
			if wait <= 0 {
				l.grant(w)
				l.mu.Unlock()

				return nil
			}
			t = l.clock.NewTimer(wait)
			tick = t.C()
		}
		l.mu.Unlock()

		select {
		case <-tick:
		case <-w.wake:
		case <-ctx.Done():
		}
		if t != nil {
			t.Stop()
		}
	}
}

// grant records that the head w took its units. A grant to a call of
// PriorityNormal starts the count of the grants to calls of PriorityHigh
// again, and a grant to a call of PriorityHigh adds one to it. leave starts
// the count again when no call of PriorityNormal waits, so the count covers
// the grants while a call of PriorityNormal waited. grant then removes w
// from its queue and chooses the next head. The caller has locked the mutex.
func (l *Limiter) grant(w *waiter) {
	if w.priority == PriorityNormal {
		l.highs = 0
	} else {
		l.highs++
	}
	l.leave(w)
}

// leave removes the waiting call w from its queue and chooses the next
// head. With no call of PriorityNormal left, the count of the grants to
// calls of PriorityHigh starts again. The caller has locked the mutex.
func (l *Limiter) leave(w *waiter) {
	q := l.queues[w.priority]
	i := slices.Index(q, w)
	l.queues[w.priority] = slices.Delete(q, i, i+1)
	if len(l.queues[PriorityNormal]) == 0 {
		l.highs = 0
	}
	l.choose()
}

// choose sets the head to the call that the Limiter grants next: the oldest
// call of PriorityHigh, or the oldest call of PriorityNormal when no call
// of PriorityHigh waits or after shareRun grants in a row to calls of
// PriorityHigh while it waited. The count of the grants is above zero only
// while a call of PriorityNormal waits. A change of the head wakes the
// former head and the new head. The caller has locked the mutex.
func (l *Limiter) choose() {
	high, normal := l.queues[PriorityHigh], l.queues[PriorityNormal]
	var next *waiter
	if len(high) != 0 && l.highs < shareRun {
		next = high[0]
	} else if len(normal) != 0 {
		next = normal[0]
	}
	if next == l.head {
		return
	}

	for _, w := range [...]*waiter{l.head, next} {
		if w != nil {
			select {
			case w.wake <- struct{}{}:
			default:
			}
		}
	}
	l.head = next
}

// claim takes n units when the bucket has them at the current time, and
// returns zero or less. Otherwise it takes nothing, and returns the time in
// which the bucket gains the units that it lacks, rounded up to a whole
// nanosecond, so a timer of that time never ends before the bucket has
// them. n is between 1 and Burst. The caller has locked the mutex.
func (l *Limiter) claim(n int64) time.Duration {
	now := l.now()
	due, rem := l.after(now, n)
	wait := due - l.fill - now
	if rem > l.fillRem {
		wait++
	}
	if wait <= 0 {
		l.due, l.dueRem = due, rem
	}

	return wait
}

// after returns due and dueRem as they would be once the bucket covered n
// more units at now: the later of due and now, plus the exact cost of n
// units. A time past the largest Duration saturates at it. after adds the
// remainders without an overflow for any rate. The caller has locked the
// mutex.
func (l *Limiter) after(now time.Duration, n int64) (time.Duration, int64) {
	due, rem := l.due, l.dueRem
	if due < now {
		due, rem = now, 0
	}

	q, r := l.cost(n)
	due = addSaturated(due, q)
	//dokimi:mutate-skip ror-boundary: no carry at rem+r == rate leaves the remainder at rate, which every comparison and every carry count as one more nanosecond
	if rem >= l.rate-r {
		return addSaturated(due, 1), rem - (l.rate - r)
	}

	return due, rem + r
}

// now returns the duration since the Limiter's construction, read from its
// Clock. It is negative while the Clock is behind the start, and
// [time.Time.Sub] saturates it at the extremes of a Duration.
func (l *Limiter) now() time.Duration {
	return l.clock.Time().Sub(l.start)
}

// cost returns the time in which the bucket gains n units at the Limiter's
// rate exactly: whole nanoseconds, and a remainder in Rate-ths of a
// nanosecond that is below rate. n is between 0 and maxBurst and the rate
// is positive, so n*nanounits fits an int64.
func (l *Limiter) cost(n int64) (time.Duration, int64) {
	c := n * nanounits

	return time.Duration(c / l.rate), c % l.rate
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
