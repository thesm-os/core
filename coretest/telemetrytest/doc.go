// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package telemetrytest tests the packages that emit telemetry through
// [go.thesmos.sh/core/telemetry], and the implementations of its
// interfaces.
//
// # Recorders
//
//   - [Recorder] is a [telemetry.Reporter] that keeps the spec of each
//     instrument, the values of each series and every span that ends. It
//     counts the bound instruments of each series that Release has not
//     ended.
//   - [Records] is a [log/slog.Handler] that keeps every record at or
//     above its level.
//
// A test passes a Recorder as the Reporter of the package under test, and
// a Records as the handler of its logger. It then reads what the package
// emitted.
//
// # Suites
//
// Every implementation of an interface of telemetry passes the same checks.
// [RunReporterSuite], [RunTracerSuite], [RunSpanSuite], [RunCounterSuite],
// [RunGaugeSuite] and [RunHistogramSuite] apply the checks of a suite to a new
// value of a factory, one value for each check. The checks state that a call
// returns without a panic, that a constructor returns a value that is not nil,
// and that Release of a bound instrument returns while other goroutines emit
// through it. Under the race detector the last check also fails on a data race.
//
// # Planted defects
//
// Each check has a planted defect: the double of an interface with a call that
// breaks the law of the check. A defect keeps its other calls on a no-op
// implementation of [go.thesmos.sh/core/telemetry/noop]. The test of a suite
// runs each check on every defect. A check fails on its own defect at the
// assertion of its statement, and it passes on the defects of the other
// checks.
//
// # Doubles
//
// [ReporterStub], [TracerStub], [SpanStub], [CounterStub], [GaugeStub] and
// [HistogramStub] are recording doubles. Each method returns the result of a
// function that a With option sets, such as [WithCounterAdd], a fixed result,
// or the result of the implementation that a DelegateTo option names, such as
// [CounterStubDelegateTo].
//
// # Dependency position
//
// Imports bytes, cmp, context, encoding/binary, encoding/hex, log/slog, math,
// math/rand/v2, slices, sync, sync/atomic, testing and time of the standard
// library, go.dokimi.dev/assert, go.dokimi.dev/assert/history,
// go.dokimi.dev/assert/prop, go.dokimi.dev/conform and go.dokimi.dev/stub, and
// go.thesmos.sh/core/telemetry and go.thesmos.sh/core/telemetry/noop.
package telemetrytest
