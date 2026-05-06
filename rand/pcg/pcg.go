// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package pcg

import (
	"encoding/binary"
	mrand "math/rand/v2"

	"go.thesmos.sh/core/rand"
)

// Rand implements [rand.Rand] using the PCG generator from
// [math/rand/v2]. See package docs for crypto and concurrency
// caveats.
//
// # Allocation contract
//
// [Rand.Uint64] is zero-alloc. [Rand.Read] uses a stack-local
// 8-byte buffer per call and is zero-alloc.
type Rand struct {
	rng  *mrand.Rand
	seed rand.Seed
}

// Compile-time interface check.
var _ rand.Rand = (*Rand)(nil)

// New returns a PCG-backed [rand.Rand] seeded with seed. Two
// instances constructed with the same seed produce identical
// Uint64 streams.
//
// Passing [rand.SeedUnspecified] is permitted but produces a
// generator seeded with state (0, 0), which is itself deterministic;
// callers wanting non-deterministic seeding should derive a seed
// from a higher-entropy source (e.g. [crypto.Rand], the system
// time, or a configuration-supplied value) before calling New.
func New(seed rand.Seed) *Rand {
	return &Rand{
		rng:  mrand.New(mrand.NewPCG(uint64(seed), 0)),
		seed: seed,
	}
}

// Uint64 returns a uniformly distributed 64-bit value.
//
// # Allocation contract
//
// Zero alloc.
func (r *Rand) Uint64() uint64 {
	return r.rng.Uint64()
}

// Read fills p with bytes derived from successive Uint64 values.
// Always returns (len(p), nil) — PCG cannot fail.
//
// The aligned 8-byte path writes Uint64s directly into p; only
// the non-aligned tail (if any) routes through a stack-local
// chunk buffer.
//
// # Allocation contract
//
// Zero alloc (any tail buffer is stack-local).
func (r *Rand) Read(p []byte) (int, error) {
	n := len(p)
	full := n / 8
	for i := range full {
		binary.LittleEndian.PutUint64(p[i*8:(i+1)*8], r.rng.Uint64())
	}
	if rem := n % 8; rem > 0 {
		var tail [8]byte
		binary.LittleEndian.PutUint64(tail[:], r.rng.Uint64())
		copy(p[full*8:], tail[:rem])
	}
	return n, nil
}

// Seed returns the seed used to construct this Rand. The returned
// value is always the seed passed to [New]; it does not change as
// the generator advances.
func (r *Rand) Seed() rand.Seed {
	return r.seed
}
