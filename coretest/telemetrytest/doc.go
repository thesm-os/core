// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package telemetrytest holds testkit-generated test
// infrastructure for the [go.thesmos.sh/core/telemetry] interface
// seams (Counter, Gauge, Histogram, Reporter, Span, Tracer) plus
// hand-rolled assertion bundles. Generated artefacts have a
// `.gen.go` suffix; hand-rolled spec files do not.
//
// Model-layer generation is intentionally omitted — the
// instrument methods (Add / Set / Record / End / SetAttributes /
// AddEvent) are side-effect-only with no return values to compare
// against a reference, so the model framework's
// auto-determinism / cross-stdlib equivalence properties don't
// apply. -race + the contract suite cover the same ground without
// the rapid harness overhead.
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
