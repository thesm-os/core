// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package resilience

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"go.thesmos.sh/core/clock"
)

// BulkheadConfig configures a [Bulkhead]. Limit is required. The zero
// values of Queue and Wait mean no queue, and no bound on a wait beyond
// the caller's context.
type BulkheadConfig struct {
	// Clock bounds the wait of a queued caller. A test passes a fake clock,
	// which times the bound exactly without a sleep.
	Clock clock.Clock

	// Limit is the number of calls in flight at once. It must be positive.
	// [Bulkhead.SetLimit] changes it.
	Limit int

	// Queue is the number of callers that wait for a permit beyond the
	// Limit. Zero means no queue, so a caller at the limit receives
	// [ErrFull] at once.
	//
	// A queue trades latency for throughput. It helps against a burst. In
	// front of a dependency that is too slow for its load, it adds a wait to
	// every failure, which Wait bounds.
	Queue int

	// Wait bounds the wait of a queued caller. Zero means that the caller's
	// context alone bounds it.
	Wait time.Duration
}

// Bulkhead bounds the calls in flight to one dependency, so that a slow
// dependency cannot occupy every goroutine of the process. A process keeps
// one Bulkhead per dependency, because a shared one bounds the dependencies
// together.
//
// The Bulkhead counts the permits that callers have taken against its
// Limit. A caller at the limit waits in a queue in the order of the calls. A
// permit that a release frees goes to the first caller of the queue.
// [Bulkhead.SetLimit] changes the Limit while calls are in flight. Each
// queued caller waits on a channel of its own, so a [testing/synctest]
// bubble counts the wait as durable.
//
// # Concurrency
//
// Safe for concurrent use. One mutex guards the Limit, the count of the
// permits that callers have taken and the queue. A call locks it to take or
// give back a permit and to join or leave the queue. A call does not lock it
// while the call waits.
//
// # Allocation contract
//
// [Bulkhead.Acquire] allocates the release of a permit that it grants, and
// nothing for a rejection. A queued caller allocates its place in the queue
// and its channel. It also allocates a timer through [clock.Clock.NewTimer]
// when the Wait of the configuration is set. [Bulkhead.TryAcquire] and
// [Bulkhead.Release] do not allocate.
type Bulkhead struct {
	clock clock.Clock

	// waiters are the channels of the queued callers in the order of their
	// calls. Each channel receives the permit that the Bulkhead passes to its
	// caller. It has room for one value, so a send under the mutex does not
	// block. A write locks mu.
	waiters []chan struct{}

	mu sync.Mutex

	// limit is the Limit of the configuration or of the last SetLimit. A
	// read and a write lock mu.
	limit int

	// inFlight is the number of the permits that callers have taken. It can
	// be above limit after a SetLimit to a lower limit. A read and a write
	// lock mu.
	inFlight int

	// queue is the Queue of the configuration. It is the most callers that
	// wait at once.
	queue int

	wait time.Duration
}

// NewBulkhead returns a Bulkhead of cfg with every permit free.
//
// Error modes: a nil Clock, a Limit that is not positive, and a negative
// Queue or Wait return [ErrConfig], classified Invalid.
func NewBulkhead(cfg BulkheadConfig) (*Bulkhead, error) {
	if cfg.Clock == nil || cfg.Limit <= 0 || cfg.Queue < 0 || cfg.Wait < 0 {
		return nil, ErrConfig
	}

	return &Bulkhead{clock: cfg.Clock, limit: cfg.Limit, queue: cfg.Queue, wait: cfg.Wait}, nil
}

// Acquire takes a permit, and returns the function that gives it back. The
// function gives the permit back once, so a second call changes nothing. A
// caller defers it at once:
//
//	release, err := b.Acquire(ctx)
//	if err != nil {
//	    return err
//	}
//	defer release()
//
// A caller at the limit waits in the queue for a free permit. A free permit
// goes to the first queued caller, so a caller does not take a permit ahead
// of a caller that waits.
//
// Error modes, none of which takes a permit:
//
//   - [ErrFull] when the limit and the queue are full.
//   - [ErrWaitTimeout] when a queued caller waited for the Wait of the
//     configuration.
//   - The error of ctx when ctx ends first.
//
// The three errors differ, because they describe different outages. A storm
// of cancellations of the callers is not a saturated dependency, and the two
// call for opposite responses.
//
// # Allocation contract
//
// Two allocations for a granted permit, the release and its one-shot guard,
// and none for a rejection. A queued caller allocates its place and its
// channel. It also allocates its timer when the Wait is set.
func (b *Bulkhead) Acquire(ctx context.Context) (func(), error) {
	// The queue has callers only while the calls in flight are at the limit,
	// because every free permit goes to a queued caller first.
	b.mu.Lock()
	if b.inFlight < b.limit {
		b.inFlight++
		b.mu.Unlock()

		return b.grant(), nil
	}

	if len(b.waiters) >= b.queue {
		b.mu.Unlock()

		return nil, ErrFull
	}

	ready := make(chan struct{}, 1)
	b.waiters = append(b.waiters, ready)
	b.mu.Unlock()

	var tick <-chan time.Time
	if b.wait > 0 {
		t := b.clock.NewTimer(b.wait)
		//dokimi:mutate-skip sbr-delete: a timer that runs no goroutine has no effect after Acquire returns that a test can observe
		defer t.Stop()

		tick = t.C()
	}

	select {
	case <-ready:
		return b.grant(), nil
	case <-tick:
		b.leave(ready)

		return nil, ErrWaitTimeout
	case <-ctx.Done():
		b.leave(ready)

		return nil, ctx.Err()
	}
}

// TryAcquire takes a free permit without a wait and reports whether it took
// one. A caller that it refuses does not take a permit and does not join the
// queue. A free permit goes to a queued caller first, so TryAcquire does not
// take a permit ahead of a caller that waits. A caller gives the permit back
// with [Bulkhead.Release]:
//
//	if !b.TryAcquire() {
//	    return resilience.ErrFull
//	}
//	defer b.Release()
//
// # Allocation contract
//
// Zero-alloc.
func (b *Bulkhead) TryAcquire() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.inFlight >= b.limit {
		return false
	}

	b.inFlight++

	return true
}

// Release gives back one permit that TryAcquire took and passes the free
// permits to the queued callers in the order of their calls. A Release does
// not change b while no permit is taken. A permit of [Bulkhead.Acquire] goes
// back through the function that Acquire returns.
//
// # Allocation contract
//
// Zero-alloc.
func (b *Bulkhead) Release() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.inFlight == 0 {
		return
	}

	b.inFlight--
	b.admit()
}

// SetLimit changes the Limit of b to limit. It does not free the permits
// that callers have taken. A lower limit refuses new calls until the calls in
// flight drop below it. A higher limit passes the permits of the difference
// to the queued callers at once. The first caller of the queue takes the
// first permit, and each queued caller keeps its place in the queue.
//
// [Bulkhead.InFlight] then returns the calls in flight. The count can be
// above the new limit until the calls of the old limit end.
//
// Error modes: a limit that is not positive returns [ErrConfig] and leaves b
// unchanged. ErrConfig is classified Invalid.
func (b *Bulkhead) SetLimit(limit int) error {
	if limit <= 0 {
		return ErrConfig
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.limit = limit
	b.admit()

	return nil
}

// InFlight returns the number of permits that callers have taken, for a
// gauge. The value is a snapshot, which concurrent calls change.
func (b *Bulkhead) InFlight() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.inFlight
}

// Queued returns the number of callers that wait for a permit, for a gauge.
// The value is a snapshot, which concurrent calls change. A queue that is
// deep over time shows a limit that is too low or a dependency that is too
// slow. It grows before [ErrFull] appears, so it is the value to alert on.
func (b *Bulkhead) Queued() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	return len(b.waiters)
}

// grant returns the release of a permit that the caller took. The release
// gives the permit back on its first call alone.
func (b *Bulkhead) grant() func() {
	var released atomic.Bool

	return func() {
		if released.CompareAndSwap(false, true) {
			b.Release()
		}
	}
}

// leave removes the queued caller of the channel ready from the queue after
// its wait ended. A permit that the Bulkhead passed to the caller before the
// removal goes back, so the caller returns without a permit.
func (b *Bulkhead) leave(ready chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if i := slices.Index(b.waiters, ready); i >= 0 {
		b.waiters = slices.Delete(b.waiters, i, i+1)

		return
	}

	b.inFlight--
	b.admit()
}

// admit passes the free permits to the queued callers in the order of their
// calls. It passes a permit only while the calls in flight are below the
// limit. The caller has locked the mutex.
func (b *Bulkhead) admit() {
	for b.inFlight < b.limit && len(b.waiters) != 0 {
		ready := b.waiters[0]
		b.waiters = slices.Delete(b.waiters, 0, 1)
		b.inFlight++
		ready <- struct{}{}
	}
}
