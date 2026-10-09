// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package uuidv4

import (
	"encoding/binary"

	"go.thesmos.sh/core/id"
	"go.thesmos.sh/core/rand"
)

// Generator produces version-4 UUIDs of RFC 9562 from an injected
// [rand.Rand].
//
// # Concurrency
//
// Safe for concurrent use when the [rand.Rand] is.
// [go.thesmos.sh/core/rand/crypto.Rand] and
// [go.thesmos.sh/core/rand/seeded.Rand] are.
// [go.thesmos.sh/core/rand/pcg.Rand] is not, so a caller wraps it in a
// mutex or uses one Generator per goroutine.
//
// # Allocation contract
//
// [Generator.Generate] reads entropy with [rand.Rand.Uint64], which
// returns a value, and not with [rand.Rand.Read], whose slice would
// escape through the interface. Generate does not allocate when Uint64
// does not. [go.thesmos.sh/core/rand/seeded],
// [go.thesmos.sh/core/rand/pcg] and [go.thesmos.sh/core/rand/constant]
// never allocate in Uint64, and [go.thesmos.sh/core/rand/crypto]
// allocates only when its pool of scratch buffers is empty.
type Generator struct {
	src rand.Rand
}

// Compile-time interface check.
var _ id.Generator = (*Generator)(nil)

// New returns a [Generator] that reads entropy from src. A caller that
// needs unguessable identifiers passes a cryptographically secure source,
// such as [go.thesmos.sh/core/rand/crypto.Rand].
func New(src rand.Rand) *Generator {
	return &Generator{src: src}
}

// Generate returns a new version-4 UUID, laid out as RFC 9562 specifies:
//
//	bytes 0-5:  random
//	byte  6:    version 4 in the high 4 bits, random in the low 4 bits
//	byte  7:    random
//	byte  8:    variant 10 in the high 2 bits, random in the low 6 bits
//	bytes 9-15: random
//
// 122 of the 128 bits are random.
//
// # Allocation contract
//
// Zero alloc when [rand.Rand.Uint64] does not allocate.
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
