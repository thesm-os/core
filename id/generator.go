// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package id

// Generator produces fresh [ID] values.
//
// Implementations document their identifier shape (time-sortable
// ULID, random UUIDv4, fixed fixture) and their dependency on
// [go.thesmos.sh/core/clock] / [go.thesmos.sh/core/rand]. The
// dependencies are injected at construction; consumers that want
// deterministic test runs supply a [clock/fake.Clock] and a
// [rand/fixed.Rand] (or [rand/seeded.Rand]) and get reproducible
// identifier streams.
//
// # Concurrency
//
// Implementations document their concurrency safety. The
// canonical implementations in this module ([id/ulid],
// [id/uuidv4], [id/ksuid], [id/fixed]) are safe for concurrent
// use under the concurrency guarantees of the underlying
// [rand.Rand] and [clock.Clock].
type Generator interface {
	// Generate returns a fresh [ID]. Implementations must NOT
	// return the [Zero] sentinel except via [id/fixed] seeded
	// explicitly with the zero value.
	Generate() ID
}
