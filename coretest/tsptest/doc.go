// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package tsptest provides an RFC 3161 time-stamp authority for tests of
// [go.thesmos.sh/core/crypto/tsp] and of its consumers.
//
// An [Authority] has its own certificate chain: a root, an intermediate,
// and an authority certificate with one critical extended key usage,
// id-kp-timeStamping. [Authority.Respond] returns the DER of a
// TimeStampResp for the DER of a TimeStampReq, so a test serves it with
// net/http/httptest, or calls it directly, and verifies the token against
// [Authority.Roots] without a network.
//
// [Authority.Token] builds a token from a [Spec], whose fields replace the
// parts of the token that the Authority writes, so a test of a verifier
// builds the malformed and the forged tokens that it must refuse.
// [StatusResponse] builds a response that grants no token.
//
// # Configurations
//
// A [Config] chooses the key and the digest of the tokens from [Key] and
// [Digest], and the deviations from RFC 3161 that a verifier refuses
// from [ESS] and [Usage]. Each of the four types has a Valid method, and
// [New] refuses a value outside its constants.
//
// # Errors
//
// [ErrConfig] and [ErrRequest] report a misuse, and classify as
// [go.thesmos.sh/core/errs.Invalid]. The errors of key generation, of
// crypto/x509 and of a signer are wrapped.
//
// # Concurrency
//
// An Authority is safe for concurrent use. The other types are values.
//
// # Allocation contract
//
// The package is test support. Each call allocates its result, and New
// allocates three keys and three certificates.
//
// # Dependency position
//
// Imports bytes, crypto, crypto/ecdsa, crypto/ed25519, crypto/elliptic,
// crypto/mldsa, crypto/rand, crypto/rsa, crypto/sha1, crypto/sha256,
// crypto/sha512, crypto/x509, crypto/x509/pkix, encoding/asn1, errors,
// fmt, math/big, slices, sync and time from the standard library, and
// go.thesmos.sh/core/clock, go.thesmos.sh/core/errs and
// go.thesmos.sh/core/internal/der from this module.
package tsptest
