// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package coretest is the umbrella for testkit-generated test
// infrastructure across the core module. Each interface in core
// gets a sibling sub-package — clocktest for clock.Clock, cryptotest
// for crypto.Hasher / crypto.MAC, etc. — that holds the generated
// stub, conformance suite, builder, bench, and model artefacts plus
// hand-rolled fixtures and assertion bundles.
//
// See docs/guides/testkit.md for the layout convention and per-
// generator usage.
package coretest
