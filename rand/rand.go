// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package rand

// Rand is the seam of randomness. An implementation implements
// [Rand.Uint64] or [Rand.Read] over its generator, whichever the generator
// produces, and derives the other from it.
//
// # Crypto strength
//
// Rand states no cryptographic property. The doc comment of each
// implementation states whether it is a cryptographic source.
//
// # Concurrency
//
// Each implementation states whether it is safe for concurrent use. The
// implementations of core differ:
//
//   - A [go.thesmos.sh/core/rand/pcg.Rand] is safe for one goroutine at a
//     time.
//   - [go.thesmos.sh/core/rand/crypto.Rand] and
//     [go.thesmos.sh/core/rand/seeded.Rand] are safe for concurrent use.
//   - [go.thesmos.sh/core/rand/constant.Rand] has no mutable state, so it
//     is safe for concurrent use.
//
// # Allocation contract
//
// [Rand.Uint64] must not allocate.
//
//dokimi:stub
//dokimi:suite
//dokimi:out path=../coretest/randtest/
type Rand interface {
	// Uint64 returns a uniformly distributed 64-bit value. Each call
	// returns the next value of the source.
	//
	//dokimi:mixin side-effect
	Uint64() uint64

	// Read fills p with random bytes, and returns the number of bytes that
	// it filled and an error. A PRNG can fill p with successive Uint64
	// values, and a CSPRNG reads p from its entropy source.
	//
	// An implementation that cannot fail returns len(p) and nil. A
	// cryptographic implementation returns an error when its entropy
	// source fails, and a non-cryptographic implementation never returns
	// one.
	Read(p []byte) (n int, err error)
}

// Seed is the seed of a deterministic implementation of [Rand].
//
// An option takes a Seed when its caller needs reproducible results, such
// as the shuffle of a dataset, the replay of a simulation or a
// deterministic test fixture. An implementation that receives
// [SeedUnspecified], the zero value, reads its own entropy.
//
// [Rand] has no seed. Only a deterministic implementation, such as
// [go.thesmos.sh/core/rand/pcg.Rand] or
// [go.thesmos.sh/core/rand/seeded.Rand], takes one. Every implementation
// takes the same type, which this package declares.
type Seed int64

// SeedUnspecified is the zero Seed. An implementation that receives it
// reads its own entropy, such as from a CSPRNG or from the system time.
const SeedUnspecified Seed = 0
