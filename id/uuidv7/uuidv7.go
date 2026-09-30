// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package uuidv7

import (
	"sync/atomic"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/rand"
)

// The stamp of an ID is its Unix milliseconds shifted left by
// fractionBits, plus its fraction of the millisecond in fractionSteps
// steps of about 244 ns.
const (
	// fractionBits is the width of rand_a, which contains the fraction.
	fractionBits = 12

	// fractionMask selects the fraction from a stamp.
	fractionMask = 0x0FFF

	// fractionSteps is the number of fractions of one millisecond, 2^12.
	fractionSteps = 4096

	// nanosPerMilli is the number of nanoseconds in one millisecond.
	nanosPerMilli = 1_000_000
)

// Generator produces UUIDv7s from a [clock.Clock] and a [rand.Rand]. The
// IDs of one Generator are distinct and increase in byte order: a call
// that starts after another call has returned receives a greater ID, also
// when the clock repeats a time or moves backwards. Create a Generator
// with [New].
//
// # Ordering
//
// [Generator.Generate] computes the stamp of the clock's wall time: its
// Unix milliseconds, followed by its fraction of the millisecond in 4,096
// steps of about 244 ns. When that stamp does not exceed the stamp of the
// last ID, Generate uses the last stamp plus one. The fraction 4,095 plus
// one is the fraction 0 of the next millisecond. Above 4,096 IDs per
// millisecond, the milliseconds of the IDs move ahead of the clock by one
// millisecond per 4,096 IDs. Generate uses the clock's stamp again as
// soon as it exceeds the last stamp.
//
// The stamps of different Generators are independent. Their IDs sort by
// time to the fraction of a millisecond, and two IDs with the same stamp
// sort by their random bits.
//
// # Concurrency
//
// Safe for concurrent use when the [clock.Clock] and the [rand.Rand] are.
// Generate records each stamp with a compare-and-swap, so no two calls
// return the same stamp.
//
// # Allocation contract
//
// Generate does not allocate when [clock.Clock.Now] and
// [rand.Rand.Uint64] do not. [go.thesmos.sh/core/clock/hlc.Clock] never
// allocates in Now, and [go.thesmos.sh/core/rand/crypto.Rand] allocates in
// Uint64 only when its pool of scratch buffers is empty.
type Generator struct {
	clk clock.Clock
	rng rand.Rand

	// last is the stamp of the last ID.
	last atomic.Uint64
}

// Compile-time interface check.
var _ id.Generator = (*Generator)(nil)

// New returns a [Generator] that reads the time from clk and entropy from
// rng. A caller that needs unguessable identifiers passes a
// cryptographically secure source, such as
// [go.thesmos.sh/core/rand/crypto.Rand].
func New(clk clock.Clock, rng rand.Rand) *Generator {
	return &Generator{clk: clk, rng: rng}
}

// Generate returns a new UUIDv7. Its unix_ts_ms field contains the
// milliseconds of its stamp, and rand_a contains the fraction. For a clock
// time n nanoseconds into its millisecond, the fraction is
// floor(n × 4096 / 1,000,000). rand_b contains the high 62 bits of one
// [rand.Rand.Uint64] call. A wall time before the Unix epoch counts as the
// epoch.
//
// # Allocation contract
//
// Zero alloc when [clock.Clock.Now] and [rand.Rand.Uint64] do not
// allocate.
func (g *Generator) Generate() id.ID {
	s := g.next(stampOf(g.clk.Now().Wall))

	return build(s>>fractionBits, uint16(s&fractionMask), g.rng.Uint64())
}

// next records and returns the stamp of the next ID: s, or the last stamp
// plus one when s does not exceed it.
func (g *Generator) next(s uint64) uint64 {
	for {
		last := g.last.Load()

		n := max(s, last+1)
		if g.last.CompareAndSwap(last, n) {
			return n
		}
	}
}

// stampOf returns the stamp of a wall time in Unix nanoseconds. A negative
// wall time counts as 0.
func stampOf(wall int64) uint64 {
	ns := uint64(max(wall, 0))

	return (ns/nanosPerMilli)<<fractionBits | (ns%nanosPerMilli)*fractionSteps/nanosPerMilli
}
