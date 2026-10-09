// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package rand

import "math/bits"

// Float64 returns a uniformly distributed value in [0.0, 1.0)
// derived from r.Uint64: the top 53 bits of one draw, the precision
// of the float64 mantissa, divided by 2^53. [math/rand/v2] divides the
// low 53 bits instead, so the two return different values for one
// draw.
//
// # Allocation contract
//
// Zero alloc.
func Float64(r Rand) float64 {
	return float64(r.Uint64()>>11) / (1 << 53)
}

// Shuffle pseudo-randomly permutes the range [0, n) with the
// Fisher-Yates algorithm. It calls swap(i, j) once for each i from n-1
// down to 1, with j drawn from [0, i] by [Uint64N]. For an n of 1 or
// less, math.MinInt included, Shuffle neither draws nor calls swap.
//
// # Allocation contract
//
// Zero alloc.
func Shuffle(r Rand, n int, swap func(i, j int)) {
	// n-1 overflows for math.MinInt, so the loop alone does not stop
	// every n below 2.
	if n < 2 {
		return
	}
	// The loop runs a count fixed before it starts, with i from n-1 down
	// to 1.
	for k := range n - 1 {
		i := n - 1 - k
		swap(i, int(Uint64N(r, uint64(i+1))))
	}
}

// Uint64N returns a uniformly distributed value in [0, n) using
// Lemire's nearly-divisionless rejection algorithm.
//
// Reference: Daniel Lemire, "Fast Random Integer Generation in an
// Interval", 2018, https://arxiv.org/abs/1805.10941
//
// Uint64N returns 0 when n is 0 or 1 — a non-empty interval is the
// caller's precondition. Lemire's algorithm naturally returns 0 in
// both cases without a guard, so no special-case is needed.
//
// # Allocation contract
//
// Zero alloc.
func Uint64N(r Rand, n uint64) uint64 {
	hi, lo := bits.Mul64(r.Uint64(), n)
	if lo < n {
		// Bias-rejection band. thresh = -n mod n; redraw while
		// lo falls below thresh.
		thresh := -n % n
		for lo < thresh {
			hi, lo = bits.Mul64(r.Uint64(), n)
		}
	}
	return hi
}
