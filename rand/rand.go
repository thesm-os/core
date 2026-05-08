// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package rand

// Rand is the unified randomness seam. Both fixed-width
// [Rand.Uint64] and variable-length [Rand.Read] are first-class:
// implementations natively implement whichever shape matches their
// underlying generator and derive the other.
//
// # Crypto strength
//
// The Rand interface itself is silent on cryptographic properties;
// strength is a property of the implementation, documented in its
// own godoc. See [package rand] for guidance on which
// implementation to pick.
//
// # Concurrency
//
// Implementations document their concurrency safety. The standard
// implementations in this module are split:
//
//   - [pcg.Rand] is NOT safe for concurrent use; one instance per
//     goroutine.
//   - [crypto.Rand] is safe for concurrent use ([crypto/rand]
//     itself is).
//   - [seeded.Rand] is safe for concurrent use.
//   - [fixed.Rand] is trivially safe for concurrent use (stateless).
type Rand interface {
	// Uint64 returns a uniformly distributed 64-bit value.
	//
	// Allocation contract: zero alloc.
	//
	//testkit:nondeterministic
	Uint64() uint64

	// Read fills p with random bytes and returns the number of
	// bytes filled along with any error. PRNG implementations may
	// fill p by writing successive Uint64 values; CSPRNG
	// implementations Read directly from the entropy source.
	//
	// Implementations that always succeed return (len(p), nil).
	// Cryptographic implementations may return an error if the
	// underlying entropy source fails; non-cryptographic
	// implementations should never return an error.
	//
	//testkit:nondeterministic
	Read(p []byte) (n int, err error)
}

// Seed is a typed integer for deterministic-RNG seeding.
//
// Use Seed in option types where the caller wants reproducible
// results: dataset shuffling, simulation replay, deterministic test
// fixtures. SeedUnspecified is the reserved zero value meaning "no
// fixed seed; let the implementation source its own entropy."
//
// Seed is not on the [Rand] interface — only deterministic
// implementations (e.g. [pcg.Rand], [seeded.Rand]) carry one. The
// type lives in this package so implementations share a vocabulary.
type Seed int64

// SeedUnspecified is the reserved zero value indicating no seed.
// Implementations receiving SeedUnspecified should source their own
// entropy (typically from a CSPRNG or the system time).
const SeedUnspecified Seed = 0
