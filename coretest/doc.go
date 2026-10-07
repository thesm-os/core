// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package coretest groups the test infrastructure of core's packages in
// its subpackages, and contains no code of its own.
//
// Each subpackage serves the package of core whose name it extends:
// blobtest for blob, castest for cas, clocktest for clock, cryptotest for
// crypto and crypto/sign, epochtest for epoch, idtest for id, randtest
// for rand, telemetrytest for telemetry, tsptest for crypto/tsp and
// versiontest for version. Together they contain the conformance suites
// that check an implementation against the contract of its interface,
// the test doubles, benchmarks and models that testkit generates in files
// with .gen in their names, and hand-written fixtures and assertion
// bundles.
package coretest
