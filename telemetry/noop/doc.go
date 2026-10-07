// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package noop provides a [telemetry.Reporter] that discards every signal
// that it receives.
//
// A program without metrics and spans passes [Reporter] where a component
// requires a reporter, and a test passes it to code that records
// telemetry.
//
// # Concurrency
//
// Every type of the package is an empty struct, so every value is safe for
// concurrent use.
//
// # Allocation contract
//
// No method allocates. Every method discards its arguments, and every
// value is an empty struct.
package noop
