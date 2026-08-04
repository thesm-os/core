// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package constant

import (
	"encoding/binary"

	"go.thesmos.sh/core/rand"
)

// Rand returns the same Uint64 on every call. Useful in tests that
// assert on a specific branch of probabilistic logic ("if the draw
// is below 0.3, fire the fault") — pick the constant that drives
// the branch under test.
//
// The zero-value Rand returns 0 on every Uint64 call; use [New] to
// pick a specific value.
//
// Rand is trivially safe for concurrent use (no mutable state).
//
// # Allocation contract
//
// Zero alloc.
type Rand struct {
	value uint64
}

// Compile-time interface check.
var _ rand.Rand = Rand{}

// New returns a Rand whose Uint64 always returns value.
func New(value uint64) Rand {
	return Rand{value: value}
}

// FromFloat64 returns a Rand whose [Float64] derives to v. v must
// be in [0.0, 1.0); values outside this range are clamped to fit.
//
// Useful for fault-injection tests that want to exercise a
// specific probability threshold:
//
//	r := constant.FromFloat64(0.5)
//	rand.Float64(r) // == 0.5 (within 53-bit precision)
func FromFloat64(v float64) Rand {
	// Largest representable float64 strictly < 1.0.
	const justBelowOne = 1.0 - 1.0/(1<<53)
	// Clamp to [0, justBelowOne]. NaN propagates through min/max
	// per IEEE 754; documented contract is callers do not pass
	// NaN.
	v = max(0, min(v, justBelowOne))
	// Invert Float64's `(uint64 >> 11) / 2^53` construction.
	bits := uint64(v * (1 << 53))
	return Rand{value: bits << 11}
}

// Uint64 returns the configured value.
func (r Rand) Uint64() uint64 {
	return r.value
}

// Read fills p with the little-endian encoding of the configured
// value, repeated as needed. Always returns (len(p), nil).
//
// # Allocation contract
//
// Zero alloc (the 8-byte chunk buffer is stack-local).
func (r Rand) Read(p []byte) (int, error) {
	var chunk [8]byte
	binary.LittleEndian.PutUint64(chunk[:], r.value)
	written := 0
	for written < len(p) {
		written += copy(p[written:], chunk[:])
	}
	return len(p), nil
}
