// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package resilience

import (
	"context"
	"sync/atomic"
	"time"

	"go.thesmos.sh/core/clock"
)

// BulkheadConfig configures a [Bulkhead]. Only Limit is required;
// Queue and Wait have meaningful zero values.
type BulkheadConfig struct {
	// Clock bounds a queued caller's wait. Under a virtual clock the
	// timeout is exact, which is what makes a saturated bulkhead
	// testable without sleeping.
	Clock clock.Clock

	// Limit is the number of calls allowed to be in flight at once.
	// Must be > 0.
	Limit int

	// Queue is how many further callers may wait for a permit. Zero
	// means none: a caller arriving at the limit is rejected at once.
	//
	// A queue trades latency for throughput, and only pays when the
	// saturation is a burst rather than a sustained overload. Queueing
	// in front of a dependency that is simply too slow adds waiting to
	// the failure rather than replacing it, which is why Wait exists.
	Queue int

	// Wait is how long a queued caller waits before giving up. Zero
	// means bounded only by the caller's context.
	Wait time.Duration
}

// Bulkhead bounds how many calls may be in flight at once, so one slow
// dependency cannot consume every goroutine in the process.
//
// The name is the ship's: a hull is divided into compartments so that
// a breach floods one rather than sinking the vessel. One Bulkhead per
// dependency; a shared one is a single compartment again.
//
// # Concurrency
//
// Safe for concurrent use.
type Bulkhead struct {
	clock clock.Clock

	// permits holds one token per free slot rather than per held slot,
	// so acquiring is a receive and the limit is enforced by the
	// channel's capacity rather than by a counter this package would
	// have to keep correct.
	permits chan struct{}

	// queue holds one token per waiting caller. Its capacity is the
	// admission control: a caller that cannot claim a slot is rejected
	// without ever blocking.
	queue chan struct{}

	wait time.Duration
}

// NewBulkhead returns a Bulkhead over cfg.
//
// Returns [ErrConfig] when Clock is nil, Limit is not positive, or
// Queue or Wait is negative.
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

// Acquire claims a permit, returning the function that gives it back.
//
// The release is one-shot: calling it again is a no-op rather than a
// permit the caller never held. Callers should defer it immediately.
//
//	release, err := b.Acquire(ctx)
//	if err != nil {
//	    return err
//	}
//	defer release()
//
// Returns [ErrFull] when the limit and the queue are both full,
// [ErrWaitTimeout] when a queued caller waited its full allowance, and
// ctx.Err() when the caller's context ended first.
//
// Those three are kept distinct because they describe different
// outages. A cancellation storm on the client side would otherwise be
// indistinguishable from a saturated dependency in any metric derived
// from them, and the two call for opposite responses.
//
// # Allocation contract
//
// Two allocations on the admitted path — the release closure and its
// one-shot guard — and none on a rejection. That is the price of
// handing back a release the caller can defer, and it is negligible
// beside the remote call it is admitting.
func (b *Bulkhead) Acquire(ctx context.Context) (func(), error) {
	// The uncontended path, and the only one that touches neither the
	// queue nor the clock.
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

// grant returns the one-shot release for a permit just taken.
func (b *Bulkhead) grant() func() {
	var released atomic.Bool

	return func() {
		if released.CompareAndSwap(false, true) {
			b.permits <- struct{}{}
		}
	}
}

// InFlight reports how many permits are held, for emission as a gauge.
//
// A point-in-time observation, and racy by nature.
func (b *Bulkhead) InFlight() int {
	return cap(b.permits) - len(b.permits)
}

// Queued reports how many callers are waiting for a permit, for
// emission as a gauge.
//
// A point-in-time observation, and racy by nature. Sustained depth
// here is the signal that the limit is too low or the dependency too
// slow; it is the number worth alerting on, because it rises before
// [ErrFull] appears.
func (b *Bulkhead) Queued() int {
	return len(b.queue)
}
