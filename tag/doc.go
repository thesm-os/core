// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package tag defines [Tag] and [Tags] — the string key/value
// pair shape used in place of [map][string]string on value-type
// structs that cross async-buffered, cached, or cross-goroutine
// boundaries.
//
// # Why not map[string]string
//
// Go maps are pointer-backed: a value-type struct that embeds a
// map field carries a shared reference, not an independent copy.
// Passing such a struct by value into an async I/O path (an audit
// buffer, a metric pipeline, a memoising cache) and then clearing
// or mutating the map from the caller causes a data race on the
// buffered writer's side — and Go maps are not safe for concurrent
// read alongside write even with a single writer (rehashing
// invalidates concurrent readers).
//
// [Tags] avoids the problem: strings are immutable in Go, and a
// slice constructed per-event is snapshot-immutable by convention.
// Callers that need to mutate must allocate a new slice; there is
// no shared mutable state to leak across the boundary.
//
// # When to use Tags vs map[string]string
//
//   - [Tags]: every value-type struct that is passed by value into
//     an interface whose implementation may buffer, cache, or hand
//     off to another goroutine. Compliance evidence (audit events,
//     usage events, evaluation runs), structured-log fields,
//     metric labels.
//   - [map][string]string: declarative control-plane state where
//     mutability is part of the contract and implementations
//     deep-copy on store. Kubernetes-shaped Labels and Annotations
//     remain maps.
//
// # Allocation contract
//
// [Tag] is a value type; pass by value. [Tags] is an ordinary
// slice — outside the zero-allocation contract — but the helpers
// in this package ([Tags.Find], [Tags.Has], [Tags.Get]) are
// zero-alloc. [Tags.With] and [Tags.Without] allocate a new slice
// for the result by design (snapshot immutability).
package tag
