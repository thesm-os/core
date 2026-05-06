// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tag

import "slices"

// Tags is a typed slice of [Tag]. Constructed once at the
// emission site and reused across hot-path calls (metric label
// binding, structured log attributes).
//
// Tags is intentionally a slice rather than a map: maps are
// pointer-backed and unsafe for concurrent reads alongside a
// single writer, while a snapshot-immutable []Tag round-trips
// safely through async I/O paths. See package doc for the
// rationale.
//
// # Lookup semantics
//
// Lookups are linear ([Tags.Find], [Tags.Has], [Tags.Get]); for
// the small N typical of metric labels and structured-log
// attributes (typically ≤ 16), linear scan is faster than a hash
// lookup and zero-alloc.
//
// When duplicate keys exist, [Tags.Find] / [Tags.Has] /
// [Tags.Get] return the first match — first-write-wins
// semantics. Tags ships no de-duplication helper; callers that
// need uniqueness construct their slice carefully or use
// [Tags.With] which replaces an existing key in place.
type Tags []Tag

// Find returns the [Tag] for key and a boolean reporting whether
// it was found. The returned [Tag] is the zero value when the key
// is absent.
//
// # Allocation contract
//
// Zero alloc.
func (ts Tags) Find(key string) (Tag, bool) {
	for _, t := range ts {
		if t.Key == key {
			return t, true
		}
	}
	return Tag{}, false
}

// Has reports whether key is present.
//
// # Allocation contract
//
// Zero alloc.
func (ts Tags) Has(key string) bool {
	for _, t := range ts {
		if t.Key == key {
			return true
		}
	}
	return false
}

// Get returns the value for key, or the empty string if key is
// absent. Equivalent to discarding [Tags.Find]'s second return.
//
// # Allocation contract
//
// Zero alloc.
func (ts Tags) Get(key string) string {
	t, _ := ts.Find(key)
	return t.Value
}

// With returns a copy of ts with t added. If a tag with the same
// key already exists, its value is replaced in the returned slice;
// the original ts is left unmodified, preserving the
// snapshot-immutability contract that lets Tags cross async
// boundaries safely.
//
// # Allocation contract
//
// Allocates a new slice with len(ts) or len(ts)+1 elements.
func (ts Tags) With(t Tag) Tags {
	for i, existing := range ts {
		if existing.Key == t.Key {
			out := slices.Clone(ts)
			out[i] = t
			return out
		}
	}
	out := make(Tags, len(ts), len(ts)+1)
	copy(out, ts)
	return append(out, t)
}

// Without returns a copy of ts with every tag whose key matches
// removed. The original ts is left unmodified. When no key
// matches, returns a copy of ts unchanged (callers cannot rely on
// pointer equality between input and output).
//
// # Allocation contract
//
// Allocates a new slice.
func (ts Tags) Without(key string) Tags {
	out := make(Tags, 0, len(ts))
	for _, t := range ts {
		if t.Key != key {
			out = append(out, t)
		}
	}
	return out
}
