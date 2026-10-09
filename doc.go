// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package core is the foundation module of the thesmos ecosystem.
//
// core defines the contract seams, such as Clock, Rand and Reporter,
// that every other thesmos library imports, and the value types that
// they share. Its production code imports the Go standard library,
// golang.org/x modules without module requirements, and the runtime
// packages of kanon, which the generated encodings of core's types
// import.
package core
