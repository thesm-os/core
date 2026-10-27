// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package blobtest is the conformance suite of [blob.Store] and
// [blob.RangeReader], with the test doubles of both interfaces.
//
// # Suites
//
// Every implementation of an interface passes the same checks.
// [RunStoreSuite] applies each check of [StoreSuite] to a new Store of a
// factory, and [RunRangeReaderSuite] applies each check of [RangeReaderSuite]
// to a new RangeReader of a factory. The subject of a check of RangeReader is
// a Store too, because the check writes the object that it reads.
//
// A store over durable storage passes [go.dokimi.dev/conform.Restartable] and
// [go.dokimi.dev/conform.Crashable] to its application. The checks
// StoreKeepsEveryWriteAcrossARestart and StoreLeavesEachKeyAsItWasOrWhole open
// the storage again after a restart, and after a crash during the Puts of the
// check. A store in memory keeps its objects across both, so its doors return
// the store itself.
//
// # Planted defects
//
// Each check has a planted defect: the double of an interface with a call that
// breaks the law of the check, over the store of
// [go.thesmos.sh/core/blob/memory]. A defect changes the calls of the keys of
// its own check alone. The defects of the checks of a restart and of a crash
// are the sound store, with a restart that forgets every object and a crash
// that tears an overwrite. The test of a suite runs each check on every
// defect. A check fails on its own defect at the assertion of its statement,
// and it passes on the defects of the other checks.
//
// # Doubles
//
// [StoreStub] and [RangeReaderStub] are recording doubles. Each method returns
// the result of a function that a With option sets, such as [WithStorePut], a
// fixed result, or the result of the implementation that [StoreStubDelegateTo]
// or [RangeReaderStubDelegateTo] names.
//
// # Dependency position
//
// Imports bytes, context, errors, fmt, io, strconv, strings, sync, testing and
// time of the standard library, go.dokimi.dev/assert, go.dokimi.dev/assert/prop,
// go.dokimi.dev/conform and go.dokimi.dev/stub, and go.thesmos.sh/core/blob,
// go.thesmos.sh/core/blob/memory, go.thesmos.sh/core/clock/fake,
// go.thesmos.sh/core/errs, go.thesmos.sh/core/page and
// go.thesmos.sh/core/version.
package blobtest
