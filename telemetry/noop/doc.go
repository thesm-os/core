// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package noop provides a [telemetry.Reporter] that discards every
// signal it receives.
//
// Suitable as the default for library code running outside an
// observability deployment, and as the test-suite reporter when a
// test exercises a code path that would otherwise emit metrics or
// spans.
//
// Every method is implemented as an empty-struct receiver with a
// trivial body, so the entire surface is zero-allocation by
// inspection — a property the package's TestZeroAlloc suite locks
// in via [testing.AllocsPerRun].
package noop
