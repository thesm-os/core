// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package resilience

import (
	"context"
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
// # Concurrency
//
// Safe for concurrent use.
//
// # Allocation contract
//
// [Bulkhead.Acquire] allocates the release of a permit that it grants, and
// nothing for a rejection.
type Bulkhead struct {
	clock clock.Clock

	// permits contains one token per free slot, so Acquire takes a slot with
	// a receive, and the capacity of the channel is the Limit.
	permits chan struct{}

	// queue contains one token per waiting caller. Its capacity is the
	// Queue, so a caller that finds it full receives ErrFull without a wait.
	queue chan struct{}

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

	permits := make(chan struct{}, cfg.Limit)
	for range cfg.Limit {
		permits <- struct{}{}
	}

	return &Bulkhead{
		clock:   cfg.Clock,
		permits: permits,
		queue:   make(chan struct{}, cfg.Queue),
		wait:    cfg.Wait,
	}, nil
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
// A caller at the limit waits in the queue for a free permit.
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
// and none for a rejection.
func (b *Bulkhead) Acquire(ctx context.Context) (func(), error) {
	// A free permit touches neither the queue nor the clock.
	select {
	case <-b.permits:
		return b.grant(), nil
	default:
	}

	select {
	case b.queue <- struct{}{}:
		defer func() { <-b.queue }()
	default:
		return nil, ErrFull
	}

	if b.wait == 0 {
		select {
		case <-b.permits:
			return b.grant(), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	t := b.clock.NewTimer(b.wait)
	//dokimi:mutate-skip sbr-delete: a timer that runs no goroutine has no effect after Acquire returns that a test can observe
	defer t.Stop()

	select {
	case <-b.permits:
		return b.grant(), nil
	case <-t.C():
		return nil, ErrWaitTimeout
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// InFlight returns the number of permits that callers have taken, for a
// gauge. The value is a snapshot, which concurrent calls change.
func (b *Bulkhead) InFlight() int {
	return cap(b.permits) - len(b.permits)
}

// Queued returns the number of callers that wait for a permit, for a gauge.
// The value is a snapshot, which concurrent calls change. A queue that is
// deep over time shows a limit that is too low or a dependency that is too
// slow. It grows before [ErrFull] appears, so it is the value to alert on.
func (b *Bulkhead) Queued() int {
	return len(b.queue)
}

// grant returns the release of a permit that the caller took. The release
// gives the permit back on its first call alone.
func (b *Bulkhead) grant() func() {
	var released atomic.Bool

	return func() {
		if released.CompareAndSwap(false, true) {
			b.permits <- struct{}{}
		}
	}
}
