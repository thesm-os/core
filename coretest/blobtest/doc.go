// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package blobtest is the conformance suite of [blob.Store],
// [blob.RangeReader], [blob.GetSigner] and [blob.CreateSigner], with the test
// doubles of the four interfaces.
//
// # Suites
//
// Every implementation of an interface passes the same checks.
// [RunStoreSuite] applies each check of [StoreSuite] to a new Store of a
// factory. [RunRangeReaderSuite], [RunGetSignerSuite] and
// [RunCreateSignerSuite] apply the checks of [RangeReaderSuite],
// [GetSignerSuite] and [CreateSignerSuite] in the same way. The subject of a
// check of RangeReader, GetSigner or CreateSigner is a Store too, because the
// check writes the objects that it reads.
//
// A store over durable storage passes [go.dokimi.dev/conform.Restartable] and
// [go.dokimi.dev/conform.Crashable] to its application. The checks
// StoreKeepsEveryWriteAcrossARestart and StoreLeavesEachKeyAsItWasOrWhole open
// the storage again after a restart, and after a crash during the Puts of the
// check. A store in memory keeps its objects across both, so its doors return
// the store itself.
//
// A check of GetSigner or CreateSigner sends each signed request to the backend
// of its subject, as a client without the credentials of the store does, and
// compares the response with the state of the store. The test of a store
// supplies the [Backend] of its subject through
// [go.dokimi.dev/conform.Supplies], and a check opens it with
// [go.dokimi.dev/conform.Open]. A check of an expiry signs the shortest
// lifetime plus the [Backend.SignDelay] of the store, and it waits until the
// Expiry of its request on the clock of the backend.
//
// # Planted defects
//
// Each check has a planted defect: the double of an interface with a call that
// breaks the law of the check, over the store of
// [go.thesmos.sh/core/blob/memory]. A defect changes the calls of the keys of
// its own check alone. The defects of the checks of a restart and of a crash
// are the sound store, with a restart that forgets every object and a crash
// that tears an overwrite. An httptest server serves the store of each defect
// of a check of a signer, and the defect has a Backend of its own. The test of
// a suite runs each check on every defect. A check fails on its own defect at
// the assertion of its statement, and it passes on the defects of the other
// checks.
//
// # Doubles
//
// [StoreStub], [RangeReaderStub], [GetSignerStub] and [CreateSignerStub] are
// recording doubles. Each method returns the result of a function that a With
// option sets, such as [WithStorePut], a fixed result, or the result of the
// implementation of a DelegateTo option, such as [StoreStubDelegateTo].
//
// # Dependency position
//
// Imports bytes, context, errors, fmt, io, maps, math/rand/v2, net/http,
// net/http/httptest, net/url, strconv, strings, sync, testing and time of the
// standard library, go.dokimi.dev/assert, go.dokimi.dev/assert/prop,
// go.dokimi.dev/conform, go.dokimi.dev/conform/probe, go.dokimi.dev/spec and
// go.dokimi.dev/stub, and go.thesmos.sh/core/blob,
// go.thesmos.sh/core/blob/memory, go.thesmos.sh/core/clock/fake,
// go.thesmos.sh/core/errs, go.thesmos.sh/core/page and
// go.thesmos.sh/core/version.
package blobtest
