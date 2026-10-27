// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package randtest is the conformance suite of [go.thesmos.sh/core/rand.Rand] and
// the test double of the interface.
//
// Every implementation of Rand passes the same checks. [RunRandSuite] applies each
// check of [RandSuite] to a new Rand of a factory, one Rand for each check. An
// implementation that does not keep a law excuses its check, as a constant Rand
// excuses the check of distinct values.
//
// # Planted defects
//
// Each check has a planted defect: the double of Rand with a Uint64 or a Read that
// breaks the law of the check. The test of the suite runs each check on every
// defect. A check fails on its own defect at the assertion of its statement, and it
// passes on the defects of the other checks.
//
// # Doubles
//
// [RandStub] is a recording double of Rand. Its Uint64 and its Read return the
// result of a function that [WithRandUint64] and [WithRandRead] set, a fixed result,
// or the result of the Rand that [RandStubDelegateTo] names.
//
// # Dependency position
//
// Imports context, fmt, io, math/rand/v2, testing and time of the standard library,
// go.dokimi.dev/assert, go.dokimi.dev/assert/prop, go.dokimi.dev/conform and
// go.dokimi.dev/stub, and go.thesmos.sh/core/rand.
package randtest
