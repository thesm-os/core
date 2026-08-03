// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package resilience holds the algorithms every caller of a remote
// dependency needs: circuit breaking, concurrency limiting, and retry
// with jittered backoff.
//
// All three read time through [go.thesmos.sh/core/clock.Clock] and
// randomness through [go.thesmos.sh/core/rand.Rand], so their
// behaviour is deterministic under a virtual clock and a seeded
// source. That is the property that makes them testable, and the
// reason they belong here rather than in each caller: built without a
// clock seam, a breaker's state transitions can only be tested by
// sleeping past the open interval, which is slow, then flaky, then
// skipped.
//
// # The three, and what each bounds
//
//   - [Breaker] stops calling a dependency that keeps failing.
//     A timeout bounds one call; it does nothing about the next
//     thousand making the same doomed call.
//   - [Bulkhead] bounds how much concurrency one dependency may
//     occupy. Concurrency is the quantity that maps to the resources
//     at risk: the same arrival rate against a dependency ten times
//     slower is ten times the occupancy.
//   - [Retrier] retries, bounded by an attempt count AND a budget.
//     The attempt count is a safety rail; the budget is what stops
//     retries keeping a failing dependency down.
//
// # No defaults
//
// Every threshold is required at construction. A wrong default in a
// foundation is wrong in every caller and never revisited, because
// nobody reviews a value they did not write. Constructors return
// [ErrConfig] rather than substituting one.
//
// # Failure is the caller's judgement
//
// [Breaker.Allow] and [Breaker.Record] are separate because the
// transports that most need a breaker do not report failure as an
// error: over HTTP a 5xx is a successful round trip carrying a bad
// status, and over an RPC protocol the status may arrive in a trailer
// that cannot be read without consuming the body. [Call] covers the
// case where failure genuinely is an error.
package resilience
