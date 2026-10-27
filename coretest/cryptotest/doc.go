// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package cryptotest is the conformance suite of the interfaces of
// [go.thesmos.sh/core/crypto] and [go.thesmos.sh/core/crypto/sign], with the
// test doubles of each interface and the key fixtures of the signature
// algorithms.
//
// # Suites
//
// Every implementation of an interface passes the same checks. [RunHasherSuite],
// [RunXOFSuite], [RunAEADSuite], [RunKeeperSuite], [RunMACSuite],
// [RunSignerSuite], [RunVerifierSuite], [RunStreamingSignerSuite] and
// [RunStreamingVerifierSuite] apply each check of a suite to a new value of a
// factory. The application of the suite of AEAD, Keeper, Verifier or
// StreamingVerifier passes [go.dokimi.dev/conform.Second]: a second AEAD or
// Keeper of the same key, or the Signer of the key of a verifier. The
// application of the suite of Hasher or MAC passes
// [go.dokimi.dev/conform.Reference]: the hash of the standard library that
// [NewStdlibHasherStub] returns, or the HMAC of the standard library under the
// same key that [NewStdlibMACStub] returns.
//
// [HasherZeroAllocCases] returns the calls of a Hasher that allocate nothing on
// the warm path, for the allocation test of a hash.
//
// # Capabilities
//
// [AssertDestroyerContract], [AssertKeyGeneratorContract],
// [AssertAADKeeperContract] and [AssertKeyCreatorContract] assert the optional
// capabilities of a [crypto.Keeper]. [AssertContextSignerContract] and
// [AssertAppendSignerContract] assert the optional capabilities of a
// [sign.Signer].
//
// # Planted defects
//
// Each check of a suite has a planted defect: the double of an interface with a
// call that breaks the law of the check. A defect keeps its other calls on an
// implementation over the standard library: SHA-256 of crypto/sha256, SHAKE128
// of crypto/sha3, AES-256-GCM of crypto/cipher, HMAC-SHA-256 of crypto/hmac, and
// Ed25519 and ECDSA P-384 over the keys of [NewEd25519Sample] and
// [NewECDSAP384Sample]. The test of a suite runs each check on every defect. A
// check fails on its own defect at the assertion of its statement, and it passes
// on the defects of the other checks.
//
// # Doubles
//
// [HasherStub], [XOFStub], [XOFStreamStub], [AEADStub], [KeeperStub],
// [DestroyerStub], [KeyGeneratorStub], [MACStub], [StreamStub], [SignerStub],
// [VerifierStub], [StreamingSignerStub], [StreamingVerifierStub],
// [SignStreamStub] and [VerifyStreamStub] are recording doubles. Each method
// returns the result of a function that a With option sets, such as
// [WithAEADOpen], a fixed result, or the result of the implementation that a
// DelegateTo option names, such as [AEADStubDelegateTo].
// [NewStdlibHasherStub], [NewStdlibMACStub], [NewStdlibStreamStub],
// [NewStdlibSignerStub] and [NewStdlibVerifierStub] return doubles over the
// standard library.
//
// # Dependency position
//
// Imports bytes, context, crypto/aes, crypto/cipher, crypto/ecdsa,
// crypto/ed25519, crypto/hmac, crypto/sha256, crypto/sha3, crypto/sha512,
// crypto/subtle, crypto/x509, encoding/hex, errors, fmt, hash, io,
// math/rand/v2, slices, testing and time of the standard library,
// go.dokimi.dev/assert, go.dokimi.dev/assert/prop, go.dokimi.dev/conform and
// go.dokimi.dev/stub, and go.thesmos.sh/core/crypto,
// go.thesmos.sh/core/crypto/sign and go.thesmos.sh/core/rand/crypto.
package cryptotest
