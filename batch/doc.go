// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package batch coalesces concurrent single-key loads into batched
// calls and deduplicates concurrent loads of the same key.
//
// Code that fans out per-item lookups against a remote dependency
// issues N calls where one would do. The pattern arises structurally
// rather than by carelessness — a handler resolves a list of
// identifiers, and each resolution is written independently because
// that is what makes the code readable — and the cost is N round
// trips, N times the connection pressure, and a latency floor set by
// the slowest of them.
//
// [Loader] accumulates keys arriving within a short window, issues one
// call, and distributes the results.
//
// # Not a cache
//
// Results are not retained beyond the in-flight window. Caching brings
// invalidation, and invalidation is a policy decision with no
// defensible default; a loader whose results outlived the window would
// be a cache with a hidden TTL, and callers would discover it by
// reading stale data.
//
// # The window is a duration, so the clock is a seam
//
// [LoaderConfig.Clock] reads elapsed time, so the accumulation window
// is exact under a virtual clock. That is why this package lives
// beside the clock seam rather than in each caller: a coalescer tested
// against the wall clock is tested by sleeping, which is tested
// loosely or not at all.
//
// # Allocation contract
//
// Unspecified. Every dispatch brackets a remote call, and the internal
// map and slice churn is not the cost that governs.
package batch
