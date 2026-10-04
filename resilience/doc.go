// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package resilience implements the mechanisms that bound the calls to a
// dependency: circuit breaking, concurrency limiting, retry with jittered
// backoff, and rate limiting.
//
// Each mechanism reads time through [go.thesmos.sh/core/clock.Clock], and
// the retrier reads randomness through [go.thesmos.sh/core/rand.Rand]. A
// test drives them with a virtual clock and a seeded source, so the open
// interval of a breaker and the wait of a limiter take no wall-clock time.
//
// # The mechanisms
//
//   - [Breaker] stops the calls to a dependency that keeps failing. A
//     timeout bounds one call, and the breaker bounds the calls after it.
//   - [Bulkhead] bounds the calls in flight to one dependency. At the same
//     arrival rate, a dependency ten times slower has ten times as many
//     calls in flight, and each of them occupies a goroutine.
//   - [Retrier] retries a call, bounded by an attempt count and a budget.
//     The budget stops the retries of every caller from adding load to a
//     dependency that fails for all of them.
//   - [Limiter] bounds the rate of work in units per second, with a
//     burst, such as the bytes that background jobs read and write.
//   - [Failover] calls redundant dependencies one after another under
//     their circuits, such as the time-stamp authorities of a deployment,
//     until one succeeds. Its error remains retryable while the error of
//     any dependency is.
//
// # No defaults
//
// Every threshold is required at construction, and a constructor returns
// [ErrConfig] for a missing one. A default in a foundation library applies
// to every caller, and no caller reviews a value that it did not write.
//
// # Failure is the caller's judgement
//
// [Breaker.Allow] and [Breaker.Record] are separate, because the
// transports that most need a breaker do not report failure as an error.
// Over HTTP a 5xx status arrives in a successful round trip, and over an
// RPC protocol the status may arrive in a trailer after the body. [Call]
// covers a dependency whose failure is an error.
package resilience
