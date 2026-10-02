// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"go.thesmos.sh/core/clock"
)

// maxShift is the largest exponent of the sampling factor of a
// BoundedHistogram: N is at most 2^63, the largest power of two of a
// uint64.
const maxShift = 63

// Outcome is the outcome of the call whose value a [BoundedHistogram]
// records. The histogram samples the values of the calls that succeeded,
// and records every value of a call that failed, because failures are rare
// and each one matters.
type Outcome uint8

// The outcomes of a call.
const (
	// OutcomeSuccess marks a call that succeeded. It is the zero Outcome.
	OutcomeSuccess Outcome = 0

	// OutcomeFailure marks a call that failed.
	OutcomeFailure Outcome = 1
)

// Valid reports whether o is one of the Outcomes of this package.
func (o Outcome) Valid() bool {
	return o <= OutcomeFailure
}

// BoundedHistogram records the values of a [Histogram] on a hot path at a
// bounded rate. It records every value of a call that failed, and one
// value in N of the calls that succeeded. N is a power of two that
// [BoundedHistogram.Flush] recomputes from the rate of the successful calls
// since the previous Flush: the smallest N that keeps the recorded
// successes at or below the configured rate. N is 1 until the first Flush.
//
// The histogram's distribution of successful values is then a sample, and
// its count is the number of recorded values. A caller that needs the
// number of calls counts them with a [ShardedCounter], and reports N from
// Flush, so a reader can scale the sample.
//
// # Concurrency
//
// Safe for concurrent use. Record takes no lock: it adds to one atomic
// counter that every caller shares, and reads N with an atomic load. Flush
// takes a mutex.
//
// # Allocation contract
//
// Record and Flush do not allocate, apart from what the Histogram's Record
// allocates, which is nothing for the implementations of this module.
type BoundedHistogram struct {
	// last is the time of the previous Flush, or of NewBoundedHistogram.
	last time.Time

	histogram Histogram
	clock     clock.Clock

	// successes counts the calls of OutcomeSuccess, and mask is N-1.
	successes atomic.Uint64
	mask      atomic.Uint64

	// perSecond is the rate of recorded successes that N keeps the
	// histogram at or below.
	perSecond float64

	mu sync.Mutex

	// counted is the value of successes at the previous Flush.
	counted uint64
}

// NewBoundedHistogram returns a BoundedHistogram of h that records at most
// about perSecond values of successful calls per second, and reads the
// time of each Flush from c.
//
// Error modes: a nil h or c, and a perSecond below 1, return [ErrConfig],
// classified Invalid.
func NewBoundedHistogram(h Histogram, perSecond int, c clock.Clock) (*BoundedHistogram, error) {
	if h == nil || c == nil || perSecond < 1 {
		return nil, ErrConfig
	}

	return &BoundedHistogram{histogram: h, clock: c, perSecond: float64(perSecond), last: c.Time()}, nil
}

// Record records v with ctx when o is not OutcomeSuccess, and records the
// value of every Nth call of OutcomeSuccess. An Outcome that is not Valid
// counts as a failure, so Record drops no value of a call that did not
// succeed.
//
// # Allocation contract
//
// Zero alloc, apart from the Histogram's Record.
func (b *BoundedHistogram) Record(ctx context.Context, v float64, o Outcome) {
	if o == OutcomeSuccess && b.successes.Add(1)&b.mask.Load() != 0 {
		return
	}

	b.histogram.Record(ctx, v)
}

// Flush recomputes N from the rate of the successful calls since the
// previous Flush, and returns it: the smallest power of two that brings the
// rate divided by N to the configured rate or below, at most 2^63. When the
// clock has not advanced since the previous Flush, as a coarse or stepped
// clock allows, Flush keeps N and returns it.
//
// A caller flushes once per export interval, from one goroutine, and
// reports N beside the histogram.
//
// # Allocation contract
//
// Zero alloc.
func (b *BoundedHistogram) Flush() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.clock.Time()
	elapsed := now.Sub(b.last).Seconds()
	successes := b.successes.Load()

	if elapsed <= 0 {
		return b.mask.Load() + 1
	}

	ratio := float64(successes-b.counted) / elapsed / b.perSecond
	b.last, b.counted = now, successes

	n := oneIn(ratio)
	b.mask.Store(n - 1)

	return n
}

// oneIn returns the smallest power of two at or above ratio: 1 for a
// ratio of at most 1, and 2^63 for a ratio above 2^63 or NaN.
func oneIn(ratio float64) uint64 {
	n := uint64(1)
	for range maxShift {
		if float64(n) >= ratio {
			break
		}

		n <<= 1
	}

	return n
}
