// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package ksuid

import (
	"encoding/binary"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/rand"
)

// Epoch is the KSUID-specific epoch: Unix seconds 1400000000,
// 2014-05-13T16:53:20Z UTC. The 32-bit timestamp field stores
// the offset from this epoch, giving a valid range of ~136 years
// (until 2150-06-19).
const Epoch int64 = 1_400_000_000

// Generator produces KSUIDs from an injected [clock.Clock]
// (timestamp source) and [rand.Rand] (entropy source).
//
// # Concurrency
//
// Safe for concurrent use when the underlying [clock.Clock] and
// [rand.Rand] are both safe. The reference implementations in
// this module ([clock/hlc.Clock], [rand/crypto.Rand],
// [rand/seeded.Rand]) are; [rand/pcg.Rand] is not.
//
// # Allocation contract
//
// [Generator.Generate] reads entropy via [rand.Rand.Uint64]
// (returning uint64 by value) rather than [rand.Rand.Read]
// (which would escape a slice through the interface boundary).
// [Generator.Generate] inherits the underlying source's
// [rand.Rand.Uint64] allocation contract: zero-alloc for
// [rand/seeded], [rand/pcg], and [rand/fixed]; one alloc per
// call for [rand/crypto].
type Generator struct {
	clk clock.Clock
	rng rand.Rand
}

// Compile-time interface check.
var _ id.Generator = (*Generator)(nil)

// New returns a [Generator] that reads the current time from clk
// and entropy from rng.
func New(clk clock.Clock, rng rand.Rand) *Generator {
	return &Generator{clk: clk, rng: rng}
}

// Generate returns a fresh KSUID. Layout:
//
//	bytes  0..3 : 32-bit Unix-second timestamp since [Epoch],
//	              big-endian.
//	bytes  4..19: 128 random bits.
//
// Timestamps before [Epoch] (2014-05-13) wrap to the truncated
// unsigned representation; consumers running before that date
// are out of scope. Timestamps beyond [Epoch] + 2^32 seconds
// (~2150-06-19) overflow the 32-bit field; consumers running
// after then are also out of scope.
//
// # Allocation contract
//
// Inherits the underlying [rand.Rand.Uint64] allocation contract.
func (g *Generator) Generate() id.ID {
	var u [id.Size160]byte

	// 32-bit Unix-second timestamp offset from KSUID epoch.
	// [clock.Instant.Wall] is unix nanoseconds; dividing by 1e9
	// gives seconds without constructing a [time.Time].
	//#nosec G115 -- documented out-of-scope: pre-KSUID-epoch wraps
	secs := uint32(g.clk.Now().Wall/1_000_000_000 - Epoch)
	binary.BigEndian.PutUint32(u[0:4], secs)

	// 128 random bits via two Uint64 reads.
	hi := g.rng.Uint64()
	lo := g.rng.Uint64()
	binary.BigEndian.PutUint64(u[4:12], hi)
	binary.BigEndian.PutUint64(u[12:20], lo)

	return id.New160(u)
}

// TimestampSeconds extracts the embedded Unix-second timestamp
// from u, treating bytes 0..3 of [id.ID.Bytes] as a 32-bit
// big-endian unsigned integer offset from [Epoch]. Returns the
// absolute Unix seconds (NOT the offset).
//
// Defined for any [id.ID] of size [id.Size160] or larger; the
// result is meaningful only when u was produced by a KSUID
// generator. Returns 0 for [id.Zero] or any shorter [id.ID].
//
// # Allocation contract
//
// Zero alloc.
func TimestampSeconds(u id.ID) int64 {
	b := u.Bytes()
	if len(b) < id.Size160 {
		return 0
	}
	return int64(binary.BigEndian.Uint32(b[0:4])) + Epoch
}
