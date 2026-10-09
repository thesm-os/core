// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

// Package tsp implements the Time-Stamp Protocol of RFC 3161 offline: the
// request that a client sends to a time-stamp authority, the response
// that it receives, and the verification of a time-stamp token against
// the roots and the policies that the verifier trusts.
//
// A token proves that the data of its message imprint existed before the
// token's time. [AppendRequest] encodes a TimeStampReq for a digest of the
// data, [ParseResponse] checks the TimeStampResp of the authority and
// returns its token, and a [Verifier] verifies the token whenever a caller
// relies on it, with no network access. [Info] is the verified TSTInfo,
// and its Time is a [clock.UTCReading] whose Latest is genTime plus the
// accuracy of the token.
//
// The package has no transport. A caller sends the request to the
// authority over HTTP as RFC 3161 section 3.4 describes, or over any other
// channel, and passes the response to ParseResponse.
//
// # Hashes
//
// A [Hash] names the algorithm of a message imprint by its OID. [SHA256],
// [SHA384] and [SHA512] are the hashes of RFC 5754. A caller with another
// hash, such as that of a national suite, writes a Hash with its OID.
//
// # Signatures
//
// A token is a CMS SignedData, RFC 5652, signed with RSASSA-PKCS1-v1_5,
// RSASSA-PSS, ECDSA, Ed25519, RFC 8419, or ML-DSA-44, ML-DSA-65 and
// ML-DSA-87, RFC 9882, over its signed attributes with SHA-256, SHA-384 or
// SHA-512. The signing-certificate attribute of RFC 5035 names the
// authority's certificate, in its version 2 for every hash, RFC 5816, or
// in its version 1 with SHA-1, which this package does not check in FIPS
// 140-only mode.
//
// # Strictness
//
// The package reads DER and no other encoding: a definite length in its
// shortest form, and the one encoding of each value. A token in BER
// returns [ErrMalformed]. Two fields of DEFAULT FALSE, ordering and the
// critical flag of an extension, may state FALSE.
//
// # Errors
//
// Every error classifies under [go.thesmos.sh/core/errs.Classify]:
//
//   - [ErrMalformed], [ErrImprint], [ErrNonce], [ErrPolicy],
//     [ErrSignature] and [ErrCertificate], classified Integrity, report a
//     response or a token that fails a check.
//   - [ErrUnsupported], classified Unsupported, reports an algorithm that
//     this package does not verify.
//   - [ErrHash] and [ErrConfig], classified Invalid, report an argument of
//     the caller.
//   - A [*StatusError] reports a response that grants no token, and
//     classifies by its status.
//
// # Concurrency
//
// A [Verifier] is safe for concurrent use. [Hash], [Policy] and [Info] are
// values, safe to share while no goroutine changes them.
//
// # Allocation contract
//
//   - AppendRequest allocates nothing into a slice with room.
//   - ParseResponse allocates nothing for a response that grants a token.
//   - Verify allocates nothing for a token of a known certificate signed
//     with Ed25519 or ML-DSA. RSA and ECDSA allocate what crypto/rsa and
//     crypto/ecdsa allocate to verify. A token of an unknown certificate
//     allocates its parse and the verification of its chain.
//
// # Dependency position
//
// Imports bytes, crypto, crypto/ecdsa, crypto/ed25519, crypto/fips140,
// crypto/mldsa, crypto/rsa, crypto/sha1, crypto/sha256, crypto/sha512,
// crypto/x509, encoding/asn1, errors, fmt, math, strconv, strings, time
// and unicode/utf8 from the standard library, and
// go.thesmos.sh/core/cache, go.thesmos.sh/core/clock,
// go.thesmos.sh/core/clock/hlc, go.thesmos.sh/core/crypto,
// go.thesmos.sh/core/errs, go.thesmos.sh/core/internal/der and
// go.thesmos.sh/core/pool from this module.
package tsp
