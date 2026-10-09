// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry

import (
	"context"
	"math/bits"
	"sync"
	"sync/atomic"
)

// cellSize is the size of a cell of a ShardedCounter in bytes: two cache
// lines of 64 bytes, which the spatial prefetcher of x86-64 fetches as a
// pair. crossbeam-utils pads its CachePadded to 128 bytes on x86-64,
// aarch64 and powerpc64 for the same reason.
const cellSize = 128

// maxCells is the largest number of cells of a ShardedCounter: 65536
// cells of 128 bytes, 8 MiB.
const maxCells = 65536

// cell is one cell of a ShardedCounter: the sum of the adds to it, padded
// to cellSize bytes, as TestCell checks, so that no other cell shares its
// pair of cache lines.
type cell struct {
	n atomic.Int64
	_ [120]byte
}

// ShardedCounter spreads the adds to one [Counter] over cells, so the
// goroutines of a hot path add without contention. The caller chooses the
// cell of each add, such as the index of a worker, and
// [ShardedCounter.Flush] adds the sum of the cells since the previous
// Flush to the Counter, once per export interval and at shutdown.
//
// A Counter that many goroutines add to costs one contended atomic add per
// call, because every add moves the counter's cache line to the adding
// core. An add to a cell of its own moves no line.
//
// # Concurrency
//
// Safe for concurrent use. Add takes no lock. Flush takes a mutex, so two
// Flush calls add each change once. The Counter receives an Add that runs
// concurrently with a Flush at that Flush or at the next.
//
// # Allocation contract
//
// NewShardedCounter allocates the cells, 128 bytes each. Add and Flush do
// not allocate, apart from what the Counter's Add allocates, which is
// nothing for the implementations of this module.
type ShardedCounter struct {
	counter Counter

	// cells has a power-of-two length, and mask is that length minus 1.
	cells []cell
	mask  uint

	mu sync.Mutex

	// flushed is the sum of the cells at the previous Flush.
	flushed int64
}

// NewShardedCounter returns a ShardedCounter of c with cells cells, rounded
// up to a power of two, so that an Add selects its cell with a mask.
//
// Error modes: a nil c, and a number of cells below 1 or above 65536,
// return [ErrConfig], classified Invalid.
func NewShardedCounter(c Counter, cells int) (*ShardedCounter, error) {
	if c == nil || cells < 1 || cells > maxCells {
		return nil, ErrConfig
	}

	n := 1 << bits.Len(uint(cells-1))

	return &ShardedCounter{counter: c, cells: make([]cell, n), mask: uint(n - 1)}, nil
}

// Cells returns the number of cells of s: the cells that NewShardedCounter
// took, rounded up to a power of two.
func (s *ShardedCounter) Cells() int {
	return len(s.cells)
}

// Add adds n to cell i modulo the number of cells, so it does not panic
// for any i, a negative i included. A counter only increases, so Add adds
// nothing for a negative n, as [Counter.Add] does.
//
// # Allocation contract
//
// Zero alloc.
func (s *ShardedCounter) Add(i int, n int64) {
	s.cells[uint(i)&s.mask].n.Add(max(n, 0))
}

// Flush adds the sum of the cells since the previous Flush to the Counter
// with ctx, and calls the Counter not at all when the sum has not changed.
// A cell's sum wraps past 2^63-1, and Flush computes the change modulo
// 2^64, so the change is right while one interval adds less than 2^63.
//
// # Allocation contract
//
// Zero alloc, apart from the Counter's Add.
func (s *ShardedCounter) Flush(ctx context.Context) {
	s.mu.Lock()

	var total int64
	for i := range s.cells {
		total += s.cells[i].n.Load()
	}

	change := total - s.flushed
	s.flushed = total
	s.mu.Unlock()

	if change > 0 {
		s.counter.Add(ctx, change)
	}
}
