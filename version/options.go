// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package version

// WriteOptions are the per-call preconditions a write may carry.
//
// # Field semantics
//
//   - [WriteOptions.IfMatch]: write only if the current [Version]
//     equals this. Empty = unconditional. Use for
//     optimistic-concurrency updates: read, transform,
//     write-with-IfMatch.
//   - [WriteOptions.IfNoneMatch]: write only if no current value
//     exists with this [Version]. The conventional value
//     [Wildcard] ("*") means "create if absent" (write fails with
//     [ErrExists] if any value exists); an explicit [Version]
//     means "create unless that exact version exists" (rarely
//     useful).
//
// Both fields default to "no precondition" ([Unspecified]).
//
// # Allocation contract
//
// Value type; pass by value. [WriteOptions.IsConditional] is
// zero-alloc.
type WriteOptions struct {
	IfMatch     Version
	IfNoneMatch Version
}

// IsConditional reports whether either precondition is set.
func (o WriteOptions) IsConditional() bool {
	return !o.IfMatch.IsZero() || !o.IfNoneMatch.IsZero()
}
