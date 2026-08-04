// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package constant provides an [id.Generator] that returns the same
// [id.ID] on every call. Useful for fixtures and deterministic
// tests where the test asserts on a specific identifier value.
//
// The zero value [Generator] returns [id.Zero] on every
// [Generator.Generate]; use [New] to pick a specific value.
//
// The value is constant per [Generator], not across the package:
// [New] takes the [id.ID] to return, so two Generators can differ.
//
// Stateless and trivially safe for concurrent use. Zero alloc per
// [Generator.Generate].
package constant
