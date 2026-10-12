// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package telemetrytest tests the packages that emit telemetry through
// [go.thesmos.sh/core/telemetry], and the implementations of its
// interfaces.
//
// # Recorders
//
//   - [Recorder] is a [telemetry.Reporter] that keeps the spec of each
//     instrument, the values of each series and every span that ends.
//   - [Records] is a [log/slog.Handler] that keeps every record at or
//     above its level.
//
// A test passes a Recorder as the Reporter of the package under test, and
// a Records as the handler of its logger. It then reads what the package
// emitted.
//
// # Contract suites and stubs
//
// testkit generates a contract suite, a stub and a benchmark suite for
// each interface: Counter, Gauge, Histogram, Reporter, Span and Tracer. A
// generated file has the suffix .gen.go. Each file *_spec.go lists the
// assertions of one contract.
//
// The package has no model suites. The methods of the instruments return
// no value that a model could compare with a reference, so the contract
// suites under the race detector cover them.
package telemetrytest

// Counter
//go:generate testkit stub -p go.thesmos.sh/core/telemetry -o counter_stub.gen.go Counter
//go:generate testkit suite -p go.thesmos.sh/core/telemetry -o counter_spec.gen.go Counter
//go:generate testkit bench -p go.thesmos.sh/core/telemetry -o counter_bench.gen.go Counter

// Gauge
//go:generate testkit stub -p go.thesmos.sh/core/telemetry -o gauge_stub.gen.go Gauge
//go:generate testkit suite -p go.thesmos.sh/core/telemetry -o gauge_spec.gen.go Gauge
//go:generate testkit bench -p go.thesmos.sh/core/telemetry -o gauge_bench.gen.go Gauge

// Histogram
//go:generate testkit stub -p go.thesmos.sh/core/telemetry -o histogram_stub.gen.go Histogram
//go:generate testkit suite -p go.thesmos.sh/core/telemetry -o histogram_spec.gen.go Histogram
//go:generate testkit bench -p go.thesmos.sh/core/telemetry -o histogram_bench.gen.go Histogram

// Reporter
//go:generate testkit stub -p go.thesmos.sh/core/telemetry -o reporter_stub.gen.go Reporter
//go:generate testkit suite -p go.thesmos.sh/core/telemetry -o reporter_spec.gen.go Reporter
//go:generate testkit bench -p go.thesmos.sh/core/telemetry -o reporter_bench.gen.go Reporter

// Span
//go:generate testkit stub -p go.thesmos.sh/core/telemetry -o span_stub.gen.go Span
//go:generate testkit suite -p go.thesmos.sh/core/telemetry -o span_spec.gen.go Span
//go:generate testkit bench -p go.thesmos.sh/core/telemetry -o span_bench.gen.go Span

// Tracer
//go:generate testkit stub -p go.thesmos.sh/core/telemetry -o tracer_stub.gen.go Tracer
//go:generate testkit suite -p go.thesmos.sh/core/telemetry -o tracer_spec.gen.go Tracer
//go:generate testkit bench -p go.thesmos.sh/core/telemetry -o tracer_bench.gen.go Tracer
