// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package uuidv4

import (
	"encoding/binary"

	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/rand"
)

// Generator produces RFC 4122 version-4 UUIDs from an injected
// [rand.Rand] entropy source.
//
// # Concurrency
//
// Safe for concurrent use when the underlying [rand.Rand] is.
// [rand/crypto.Rand] and [rand/seeded.Rand] are both safe;
// [rand/pcg.Rand] is not — wrap with a mutex or use one
// generator per goroutine when the source is non-concurrent-safe.
//
// # Allocation contract
//
// [Generator.Generate] reads entropy via [rand.Rand.Uint64]
// (returning uint64 by value) rather than [rand.Rand.Read]
// (which would escape a slice through the interface boundary).
// [Generator.Generate] inherits the underlying source's
// [rand.Rand.Uint64] allocation contract: zero-alloc for
// [rand/seeded], [rand/pcg], and [rand/fixed]; one alloc per
// call for [rand/crypto] (an unavoidable cost of the
// [io.Reader] indirection in [rand/crypto.Rand.Uint64]).
type Generator struct {
	src rand.Rand
}

// Compile-time interface check.
var _ id.Generator = (*Generator)(nil)

// New returns a [Generator] backed by src. src must produce
// CSPRNG-grade output for security-sensitive call sites.
func New(src rand.Rand) *Generator {
	return &Generator{src: src}
}

// Generate returns a fresh UUIDv4. Layout (RFC 4122):
//
//	bytes  0..3: random
//	bytes  4..5: random
//	byte   6   : (random & 0x0F) | 0x40   — version 4
//	byte   7   : random
//	byte   8   : (random & 0x3F) | 0x80   — variant 10
//	bytes  9..15: random
//
// The version (4) and variant (10) bits are stamped in-place
// over the random bytes; the remaining 122 bits are random.
//
// # Allocation contract
//
// Inherits the underlying [rand.Rand.Uint64] allocation contract.
func (g *Generator) Generate() id.ID {
	hi := g.src.Uint64()
	lo := g.src.Uint64()
	var u [id.Size128]byte
	binary.BigEndian.PutUint64(u[0:8], hi)
	binary.BigEndian.PutUint64(u[8:16], lo)
	// Stamp version 4 in the high nibble of byte 6.
	u[6] = (u[6] & 0x0F) | 0x40
	// Stamp variant 10 in the high two bits of byte 8.
	u[8] = (u[8] & 0x3F) | 0x80
	return id.New128(u)
}
