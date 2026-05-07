// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package clocktest

import "time"

// Canonical fixtures for [clock.Instant] and [clock.InstantRange].
//
// Each fixture returns the underlying builder so callers can
// chain further `With*` setters before calling `Build`. The
// fixtures collapse the most common test setups into a single
// named entry point — replacing scattered `clock.Instant{Wall:
// ..., Logical: ..., Node: ...}` literals with a baseline that
// tests can tweak per-case.
//
// # When to use a fixture vs a struct literal
//
// Fixtures are only useful when the test's intent is "an
// instant for an Update merge" or similar generic shape — the
// test doesn't care what the specific Wall/Logical/Node values
// are. Tests that exercise specific field values (Instant
// algebra in clock/instant_test.go, exact HLC merge math in
// clock/fake/fake_test.go's TestUpdate cases) are clearer with
// inline struct literals; the literal IS the contract under
// test.
//
// # Naming
//
// `New<Type><Variant>` — the type prefix prevents clashes
// between fixtures that share a variant name across types
// (e.g. `NewInstantOrigin` vs `NewInstantRangeOrigin` if both
// existed).

// referenceWall is the wall-time anchor for non-zero Instant
// fixtures: 2026-01-01 00:00:00 UTC. Stable across runs;
// chosen to match the project's reference year so visual
// inspection of test failures lines up with the codebase's
// timeline.
var referenceWall = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano()

// NewInstantOrigin returns a builder seeded with the zero-state
// Instant (Wall=0, Logical=0, Node=0). Use as the "no time has
// passed" baseline for tests that need a defined zero point.
func NewInstantOrigin() *InstantBuilder {
	return NewInstant()
}

// NewInstantSample returns a builder seeded with a canonical
// non-zero Instant (Wall=2026-01-01, Logical=0, Node=1). Use as
// the "plausible non-zero instant" baseline for tests that want
// realistic values without depending on time.Now.
func NewInstantSample() *InstantBuilder {
	return NewInstant().
		WithWall(referenceWall).
		WithLogical(0).
		WithNode(1)
}

// NewInstantPeer returns a builder seeded with an Instant that
// looks like it came from a different node (Node=2, otherwise
// matches NewInstantSample). Use as the "observed from a peer"
// fixture for HLC merge / Update tests.
func NewInstantPeer() *InstantBuilder {
	return NewInstantSample().WithNode(2)
}

// NewInstantRangeSample returns a builder seeded with the
// canonical InstantRange spanning Origin → Sample (i.e. "every
// Instant from zero up to the project reference instant").
// Use as the baseline range for tests that want a non-trivial
// range without bespoke setup.
func NewInstantRangeSample() *InstantRangeBuilder {
	return NewInstantRange().
		WithSince(NewInstantOrigin().Build()).
		WithUntil(NewInstantSample().Build())
}
