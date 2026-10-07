// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package ulid

import (
	"encoding/binary"

	"go.thesmos.sh/core/clock"
	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/rand"
)

// Generator produces ULIDs from an injected [clock.Clock]
// (timestamp source) and [rand.Rand] (entropy source).
//
// # Concurrency
//
// A Generator is safe for concurrent use when the underlying
// [clock.Clock] and [rand.Rand] are both safe. [clock/hlc.Clock],
// [rand/crypto.Rand] and [rand/seeded.Rand] are safe, and
// [rand/pcg.Rand] is not.
//
// # Allocation contract
//
// [Generator.Generate] reads entropy through [rand.Rand.Uint64], which
// returns a uint64 by value, and not through [rand.Rand.Read], whose
// slice would escape through the interface. Generate allocates what
// the Uint64 of its source allocates: nothing for [rand/seeded],
// [rand/pcg] and [rand/constant], and once per call for [rand/crypto],
// whose Uint64 reads through an [io.Reader].
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

// Generate returns a fresh ULID of this layout:
//
//	bytes 0..5 : 48-bit Unix-millisecond timestamp, big-endian
//	bytes 6..15: 80 random bits
//
// A timestamp before the Unix epoch, a negative number of
// milliseconds, wraps to its truncated unsigned form, and a consumer
// that runs before 1970 is out of scope. A timestamp beyond 10889 AD
// overflows the 48-bit field, and is out of scope too.
//
// # Allocation contract
//
// Generate allocates what the [rand.Rand.Uint64] of its source
// allocates.
func (g *Generator) Generate() id.ID {
	var u [id.Size128]byte
	// Encode the millisecond timestamp into bytes 0..5
	// (big-endian). [clock.Instant.Wall] is unix nanoseconds;
	// dividing by one million gives milliseconds without
	// constructing a [time.Time]. The 48-bit field rolls over in
	// 10889 AD. Pre-1970 timestamps are out of scope per package
	// doc; the int64→uint64 conversion below relies on that.
	//#nosec G115 -- documented out-of-scope: pre-1970 wraps
	ms := uint64(g.clk.Now().Wall / 1_000_000)
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], ms)
	// PutUint64 writes 8 bytes; the first 2 are the high bits we
	// truncate, the last 6 are what we keep.
	copy(u[0:6], ts[2:8])
	// Fill bytes 6..15 with 80 random bits via two Uint64 reads
	// (avoids the slice-escape that [rand.Rand.Read] would force
	// through the interface boundary). Only the high 16 bits of
	// the second uint64 are consumed.
	hi := g.rng.Uint64()
	lo16 := uint16(g.rng.Uint64() >> 48)
	binary.BigEndian.PutUint64(u[6:14], hi)
	binary.BigEndian.PutUint16(u[14:16], lo16)
	return id.New128(u)
}

// TimestampMillis extracts the embedded millisecond timestamp
// from u, treating bytes 0..5 of [id.ID.Bytes] as a 48-bit
// big-endian unsigned integer.
//
// TimestampMillis is defined for any [id.ID] of size [id.Size128] or
// larger, and its result is meaningful only when a ULID generator
// produced u. It returns 0 for [id.Zero].
//
// # Allocation contract
//
// TimestampMillis does not allocate.
func TimestampMillis(u id.ID) uint64 {
	b := u.Bytes()
	//dokimi:mutate-skip sbr-delete,ror-false: the only ID shorter than 128 bits is Zero, whose zero bytes decode to 0
	if len(b) < id.Size128 {
		return 0
	}
	var ms uint64
	for _, x := range b[0:6] {
		ms = (ms << 8) | uint64(x)
	}
	return ms
}
