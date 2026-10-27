// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package constant

import (
	"encoding/binary"
	"sync/atomic"

	"go.thesmos.sh/core/rand"
)

// Counter is a [rand.Rand] whose draws differ while [rand.Float64] of each
// draw returns one value. A test of a component that draws both the jitter
// of its backoffs and its identifiers from one Rand passes a Counter. Each
// backoff is then the same fraction of its ceiling, and a generator that
// reads its identifiers from the Counter returns a new identifier on each
// call.
//
// Draw n of a Counter, counted from 0, returns the value of [FromFloat64]
// of v plus n, modulo 2^64. [rand.Float64] reads the top 53 bits of a draw,
// and the value of FromFloat64 has its low 11 bits clear. A count below
// 2,048 changes only those 11 bits. Float64 of each of the first 2,048
// draws returns the Float64 of FromFloat64 of v, which is v itself for a
// multiple of 2^-53 such as 0.5. Float64 of a later draw n is above that
// value by at most n/2^64, until the sum wraps past 2^64. For the largest v
// below 1, draw 2,048 wraps to 0.
//
// # Concurrency
//
// A Counter is safe for concurrent use. Each draw takes the next count with
// one atomic addition.
//
// # Allocation contract
//
// [NewCounter] allocates the Counter. Uint64 and Read do not allocate.
//
//dokimi:subject rand.Rand new=newCounter
//dokimi:budget Rand.Uint64 allocs=0
type Counter struct {
	// n is the number of draws so far.
	n atomic.Uint64

	// value is the value of FromFloat64 of v.
	value uint64
}

// Compile-time interface check.
var _ rand.Rand = (*Counter)(nil)

// NewCounter returns a Counter whose draws return v from [rand.Float64].
// NewCounter clamps v to [0.0, 1.0) as [FromFloat64] clamps it.
//
// # Allocation contract
//
// NewCounter allocates the Counter.
func NewCounter(v float64) *Counter {
	return &Counter{value: FromFloat64(v).value}
}

// Uint64 returns the value of FromFloat64 of v plus the number of earlier
// draws, modulo 2^64.
//
// # Allocation contract
//
// Zero alloc.
func (c *Counter) Uint64() uint64 {
	return c.value + c.n.Add(1) - 1
}

// Read fills p with the little-endian encoding of successive draws of
// Uint64, one draw for each started 8 bytes of p. Always returns
// (len(p), nil).
//
// # Allocation contract
//
// Zero alloc (the 8-byte chunk buffer is stack-local).
func (c *Counter) Read(p []byte) (int, error) {
	var chunk [8]byte
	for i := range (len(p) + len(chunk) - 1) / len(chunk) {
		binary.LittleEndian.PutUint64(chunk[:], c.Uint64())
		copy(p[i*len(chunk):], chunk[:])
	}

	return len(p), nil
}
