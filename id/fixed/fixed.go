// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fixed

import "go.thesmos.sh/core/id"

// Generator returns the same [id.ID] on every call.
//
// The zero-value Generator returns [id.Zero]; use [New] to pick a
// specific value.
//
// # Concurrency
//
// Stateless. Trivially safe for concurrent use.
//
// # Allocation contract
//
// Zero alloc per [Generator.Generate].
type Generator struct {
	value id.ID
}

// Compile-time interface check.
var _ id.Generator = Generator{}

// New returns a [Generator] that always returns value from
// [Generator.Generate].
func New(value id.ID) Generator {
	return Generator{value: value}
}

// Generate returns the configured [id.ID].
//
// # Allocation contract
//
// Zero alloc.
func (g Generator) Generate() id.ID {
	return g.value
}
