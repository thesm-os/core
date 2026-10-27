// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package castest is the conformance suite of [cas.Store], with the test
// doubles of [cas.Store] and [cas.Streamer].
//
// # Suite
//
// Every implementation of [cas.Store] passes the same checks. [RunStoreSuite]
// applies each check of [StoreSuite] to a new Store of a factory. A check
// computes each address with the Hasher of the store. The stream checks call
// [cas.PutStream] and [cas.GetStream], so a store that implements
// [cas.Streamer] and a store that leaves streaming to the buffering fallback
// pass the same laws.
//
// A store over durable storage passes [go.dokimi.dev/conform.Restartable] and
// [go.dokimi.dev/conform.Crashable] to its application. The checks
// StoreKeepsEveryWriteAcrossARestart and StoreLeavesEachAddressAbsentOrWhole
// open the storage again after a restart, and after a crash during the writes
// of the check. A store in memory keeps its values across both, so its doors
// return the store itself.
//
// # Planted defects
//
// Each check has a planted defect: the double of [cas.Store] with a call that
// breaks the law of the check, over the store of
// [go.thesmos.sh/core/cas/memory] with SHA-256. A defect changes the calls of
// the addresses of its own check alone. The defects of the checks of a restart
// and of a crash are the sound store, with a restart that forgets every value
// and a crash that tears each value. The test of the suite runs each check on
// every defect. A check fails on its own defect at the assertion of its
// statement, and it passes on the defects of the other checks.
//
// # Doubles
//
// [StoreStub] and [StreamerStub] are recording doubles. Each method returns the
// result of a function that a With option sets, such as [WithStorePut], a fixed
// result, or the result of the implementation that [StoreStubDelegateTo] or
// [StreamerStubDelegateTo] names.
//
// # Dependency position
//
// Imports bytes, context, errors, io, sync/atomic and testing of the standard
// library, go.dokimi.dev/assert, go.dokimi.dev/assert/prop,
// go.dokimi.dev/conform and go.dokimi.dev/stub, and go.thesmos.sh/core/cas,
// go.thesmos.sh/core/cas/memory, go.thesmos.sh/core/crypto,
// go.thesmos.sh/core/crypto/sha256, go.thesmos.sh/core/crypto/sha3,
// go.thesmos.sh/core/crypto/sha512, go.thesmos.sh/core/errs and
// go.thesmos.sh/core/task.
package castest
