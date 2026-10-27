// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package idtest is the conformance suite of [id.Generator] and the test double of
// the interface.
//
// Every implementation of [id.Generator] passes the same checks. [RunGeneratorSuite]
// applies each check of [GeneratorSuite] to a new Generator of a factory, one
// Generator for each check. An implementation that does not keep a law excuses its
// check, as a constant Generator excuses the check of distinct IDs.
//
// # Planted defects
//
// Each check has a planted defect: the double of [id.Generator] with a Generate that
// breaks the law of the check. The test of the suite runs each check on every
// defect. A check fails on its own defect at the assertion of its statement, and it
// passes on the defects of the other checks.
//
// # Doubles
//
// [GeneratorStub] is a recording double of [id.Generator]. Its Generate returns the
// result of a function that [WithGeneratorGenerate] sets, a fixed result, or the
// result of the Generator that [GeneratorStubDelegateTo] names.
//
// # Dependency position
//
// Imports context, math/rand/v2, testing and time of the standard library,
// go.dokimi.dev/assert, go.dokimi.dev/assert/prop, go.dokimi.dev/conform and
// go.dokimi.dev/stub, and go.thesmos.sh/core/id.
package idtest
