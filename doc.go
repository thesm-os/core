// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package core is the stdlib-only foundation of the thesmos ecosystem.
//
// core defines the contract seams (Clock, Rand, Reporter, …) that
// every other thesmos library imports. It depends on nothing outside
// the Go standard library; consumer-specific implementations
// (OpenTelemetry, Prometheus, crypto/rand, HLC, …) live in consumer
// repositories such as testkit, thesmos, and space.
//
// The constraints governing this module are documented as Architecture
// Decision Records under docs/adr/.
package core
