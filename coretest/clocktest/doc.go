// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package clocktest is the conformance suite of [clock.Clock] and [clock.Timer],
// the test doubles of both interfaces, and the builders of [clock.Instant] and
// [clock.InstantRange].
//
// Every implementation of an interface passes the same checks. [RunClockSuite]
// applies each check of [ClockSuite] to a new Clock of a factory, and
// [RunTimerSuite] applies each check of [TimerSuite] to a new pending Timer of a
// factory. A Timer of the suite is pending for an hour, and a check sends its tick
// through Reset(0).
//
// # Planted defects
//
// Each check has a planted defect: a Clock or a Timer with a method that breaks the
// law of the check. A defect delegates the other calls to a fake Clock at
// 2026-01-01T00:00:00Z, or to a Timer of it. The test of a suite runs each check on
// every defect. A check fails on its own defect at the assertion of its statement,
// and it passes on the defects of the other checks.
//
// # Doubles
//
// [ClockStub] and [TimerStub] are recording doubles. Each method returns the result
// of a function that a With option sets, such as [WithClockUpdate], a fixed result,
// or the result of the implementation that [ClockStubDelegateTo] or
// [TimerStubDelegateTo] names.
//
// # Builders
//
// [NewInstantBuilder] and [NewInstantRangeBuilder] build test values field by
// field. [NewInstantOrigin], [NewInstantSample], [NewInstantPeer] and
// [NewInstantRangeSample] return builders of fixed values.
//
// # Dependency position
//
// Imports context, math/rand/v2, testing and time of the standard library,
// go.dokimi.dev/assert, go.dokimi.dev/assert/prop, go.dokimi.dev/conform and
// go.dokimi.dev/stub, and go.thesmos.sh/core/clock and
// go.thesmos.sh/core/clock/fake.
package clocktest
