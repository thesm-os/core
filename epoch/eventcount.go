// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package epoch

import (
	"context"
	"sync"
	"sync/atomic"
)

// EventCount is a monotonically increasing [Epoch] that goroutines wait
// on. A producer publishes progress with [EventCount.Advance]. A consumer
// blocks in [EventCount.Wait] until the count is at least its target, its
// context is done, or the producer calls [EventCount.Fail].
//
// [Counter] issues sequence positions. An EventCount publishes progress
// through them, for example the highest durable position of a log.
//
// The zero value is ready to use and starts at [Zero].
//
// # Wakeups
//
// Blocked waiters share one channel. Advance closes it and installs a new
// one, which wakes every blocked waiter. A waiter whose target is still
// ahead of the count blocks again, so the cost of an Advance is linear in
// the number of blocked waiters.
//
// # Failure
//
// Fail is terminal. Every current and future Wait whose target is not met
// returns the error of the first Fail. An EventCount has no reset, so a
// caller that recovers constructs a new one.
//
// # Concurrency
//
// Safe for concurrent use.
//
// # Allocation contract
//
// Current and Wait do not allocate, except that the first Wait to block
// on a zero value allocates the initial channel. Advance allocates one
// channel when a waiter has taken the current one. A context allocates
// its own done channel on its first Done call, so the first Wait with a
// new context allocates once inside the context package.
type EventCount struct {
	// wake is closed by the next Advance when taken is set. It is nil
	// until the first Wait that finds its target unmet. No method reads
	// it after Fail.
	wake chan struct{}

	// err is the error of the first Fail.
	err error

	// value is the count. Writes happen under mu, and Current reads it
	// without mu.
	value atomic.Uint64

	// mu guards wake, taken and err.
	mu sync.Mutex

	// taken reports whether a waiter has selected on wake since it was
	// installed.
	taken bool
}

// Current returns the count. It does not allocate.
func (c *EventCount) Current() Epoch {
	return Epoch(c.value.Load())
}

// Advance raises the count to v and wakes every blocked waiter. It is a
// no-op when v is not above the count. After [EventCount.Fail] it still
// raises the count.
//
// # Allocation contract
//
// One channel when a waiter has taken the current one, otherwise none.
func (c *EventCount) Advance(v Epoch) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.met(v) {
		return
	}

	c.value.Store(uint64(v))

	if c.taken {
		close(c.wake)
		c.wake = make(chan struct{})
		c.taken = false
	}
}

// Fail makes every current and future [EventCount.Wait] whose target is
// not met return err. Only the first Fail takes effect. Fail(nil) is a
// no-op, because a nil result from Wait means that the target was met.
func (c *EventCount) Fail(err error) {
	if err == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.err != nil {
		return
	}

	c.err = err

	if c.taken {
		close(c.wake)
		c.taken = false
	}
}

// Wait blocks until the count is at least v and returns nil. It returns
// the error of [EventCount.Fail] if the count fails first, and ctx.Err()
// if ctx is done first. A target that is already met returns nil, even
// after Fail or after ctx is done. When v is met as ctx ends, Wait
// returns either result.
//
// # Allocation contract
//
// None, except for the first blocking Wait on a zero value. See
// [EventCount].
func (c *EventCount) Wait(ctx context.Context, v Epoch) error {
	for {
		wake, done, err := c.arm(v)
		if done {
			return err
		}

		select {
		case <-wake:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// arm reports done with the result of Wait when the count has met v, with
// a nil error, or has failed, with the error of Fail. Otherwise it marks
// the wake-up channel as taken and returns it.
func (c *EventCount) arm(v Epoch) (wake <-chan struct{}, done bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.met(v) {
		return nil, true, nil
	}

	if c.err != nil {
		return nil, true, c.err
	}

	if c.wake == nil {
		c.wake = make(chan struct{})
	}

	c.taken = true

	return c.wake, false, nil
}

// met reports whether the count is at least v. arm evaluates it under mu,
// the lock that Advance takes to raise the count, so no Advance can fall
// between a waiter's check and its take of the channel.
func (c *EventCount) met(v Epoch) bool {
	return Epoch(c.value.Load()) >= v
}
