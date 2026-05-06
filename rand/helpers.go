// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package rand

import "math/bits"

// Float64 returns a uniformly distributed value in [0.0, 1.0)
// derived from r.Uint64. Uses 53 bits of precision to match the
// float64 mantissa, the same construction as [math/rand/v2].
//
// # Allocation contract
//
// Zero alloc.
func Float64(r Rand) float64 {
	return float64(r.Uint64()>>11) / (1 << 53)
}

// Shuffle pseudo-randomly permutes the range [0, n) by calling swap
// for each pair, using the Fisher-Yates algorithm with Lemire's
// nearly-divisionless integer draw. If n <= 1, Shuffle is a no-op.
//
// # Allocation contract
//
// Zero alloc.
func Shuffle(r Rand, n int, swap func(i, j int)) {
	for i := n - 1; i > 0; i-- {
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
