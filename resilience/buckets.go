// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package resilience

import (
	"sync/atomic"
	"time"

	"go.thesmos.sh/core/clock"
)

// maxBucketsRate is the largest Rate of a [Buckets]. At that Rate a unit
// costs one nanosecond, and no cost in whole nanoseconds is shorter.
const maxBucketsRate = nanounits

// BucketsConfig configures a [Buckets]. Every field is required, so the zero
// BucketsConfig is invalid.
type BucketsConfig struct {
	// Clock measures the time that refills the buckets. A test passes a fake
	// clock and advances it.
	Clock clock.Clock

	// Rate is the number of units that each bucket gains per second. It must
	// be between 1 and 10^9.
	Rate int64

	// Burst is the capacity of each bucket. One call takes at most Burst
	// units, and a bucket gains at most Burst units while no call takes any.
	// It must be between 1 and [MaxBurst].
	Burst int64

	// Size is the number of buckets. It must be positive.
	Size int
}

// Buckets is a table of Size token buckets that share one Rate and one
// Burst. A service keeps such a table for the hashes of its principals. Each
// bucket gains Rate units per second up to Burst. A new Buckets has every
// bucket full. A call takes its units without a lock and does not wait for
// units.
//
// Each bucket keeps one time of the generic cell rate algorithm, as a
// [Limiter] does. The bucket would be empty at that time once it covered the
// units taken so far. The time is a count of whole nanoseconds in one atomic
// word, and a call moves it with a compare-and-swap. A unit costs 10^9/Rate
// nanoseconds rounded up to a whole nanosecond. The rounding lets a bucket
// admit at most Rate units per second. A Rate that does not divide 10^9
// admits fewer units per second. The shortfall is less than Rate/10^9 of the
// units.
//
// The Buckets measures its time with the Elapsed of its Clock, so a step of
// the wall time does not add or take units.
//
// # Concurrency
//
// Safe for concurrent use. The calls of one bucket retry their
// compare-and-swap while other calls change the bucket. Neighbouring buckets
// share a cache line, so the calls of two buckets of one line contend for
// it.
//
// # Allocation contract
//
// [NewBuckets] allocates the table with eight bytes for each bucket.
// [Buckets.AllowN] and [Buckets.ReturnN] do not allocate.
type Buckets struct {
	clock clock.Clock

	// due contains the time of each bucket as nanoseconds of the Elapsed of
	// the clock. A bucket is full while its time is at or before the current
	// time, so the zero time is a full bucket.
	due []atomic.Int64

	// unit is the cost of one unit in nanoseconds. NewBuckets rounds
	// 10^9/Rate up to compute it.
	unit int64

	// fill is the time in which an empty bucket gains Burst units. It is
	// Burst times unit.
	fill int64

	burst int64
}

// NewBuckets returns a Buckets of cfg. Every bucket of the table is full.
//
// Error modes, each classified [go.thesmos.sh/core/errs.Invalid]:
//
//   - A nil Clock returns [ErrConfig].
//   - A Rate below 1 or above 10^9 returns ErrConfig.
//   - A Burst below 1 or above [MaxBurst] returns ErrConfig.
//   - A Size below 1 returns ErrConfig.
//
// # Allocation contract
//
// Allocates the Buckets and its table of eight bytes for each bucket.
func NewBuckets(cfg BucketsConfig) (*Buckets, error) {
	if cfg.Clock == nil || cfg.Rate < 1 || cfg.Rate > maxBucketsRate {
		return nil, ErrConfig
	}

	if cfg.Burst < 1 || cfg.Burst > MaxBurst || cfg.Size < 1 {
		return nil, ErrConfig
	}

	unit := (nanounits + cfg.Rate - 1) / cfg.Rate

	return &Buckets{
		clock: cfg.Clock,
		due:   make([]atomic.Int64, cfg.Size),
		unit:  unit,
		fill:  cfg.Burst * unit,
		burst: cfg.Burst,
	}, nil
}

// AllowN takes n units from the bucket i when it has them and reports
// whether it took them. It does not wait. A call that reports false does not
// take units. An n of zero always fits.
//
// AllowN reports false for a negative n and for an i outside the table. It
// also reports false for an n above Burst, because a bucket does not contain
// more than Burst units.
//
// # Allocation contract
//
// Zero-alloc.
func (b *Buckets) AllowN(i int, n int64) bool {
	if n < 0 || n > b.burst || i < 0 || i >= len(b.due) {
		return false
	}

	if n == 0 {
		return true
	}

	cost := time.Duration(n * b.unit)
	now := b.clock.Elapsed()

	for {
		due := b.due[i].Load()
		next := addSaturated(max(time.Duration(due), now), cost)
		if next-time.Duration(b.fill) > now {
			return false
		}

		if b.due[i].CompareAndSwap(due, int64(next)) {
			return true
		}
	}
}

// ReturnN gives back n units that [Buckets.AllowN] took from the bucket i
// and that the caller did not use. A caller gives back the units of a call
// that the bucket of another table refused. The bucket keeps at most Burst
// units, so ReturnN gives back fewer units once the bucket gained units since
// the call took them.
//
// ReturnN does not give back units for an n of zero or less or for an i
// outside the table. An n above Burst gives back Burst units at most.
//
// # Allocation contract
//
// Zero-alloc.
func (b *Buckets) ReturnN(i int, n int64) {
	if n <= 0 || i < 0 || i >= len(b.due) {
		return
	}

	back := min(n, b.burst) * b.unit
	now := int64(b.clock.Elapsed())

	for {
		due := b.due[i].Load()
		if due <= now {
			return
		}

		if b.due[i].CompareAndSwap(due, max(due-back, now)) {
			return
		}
	}
}
