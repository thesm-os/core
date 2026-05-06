// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package fixed provides an [id.Generator] that returns the same
// [id.ID] on every call. Useful for fixtures and deterministic
// tests where the test asserts on a specific identifier value.
//
// The zero value [Generator] returns [id.Zero] on every [Generator.New];
// use [New] to pick a specific value.
//
// Stateless and trivially safe for concurrent use. Zero alloc per
// [Generator.New].
package fixed
