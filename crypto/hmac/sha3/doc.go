// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package sha3 provides HMAC-SHA3-256, HMAC-SHA3-384, and
// HMAC-SHA3-512 [crypto.MAC] implementations backed by
// [crypto/hmac] and [crypto/sha3].
//
// [NewSHA3_256], [NewSHA3_384], and [NewSHA3_512] each take the
// keying material and return a [MAC] bound to it. The key is
// copied at construction; callers may zero or reuse the source
// buffer immediately afterwards.
//
// SHA-3 is the FIPS 202 sponge construction. Selected where
// length-extension resistance, NIST-approved diversity from the
// SHA-2 family, or PQC parameter-set co-location is required.
//
// [MAC.Sign] and [MAC.Verify] each allocate the underlying HMAC
// state once per call. [MAC.NewStream] allocates the wrapper
// plus the underlying HMAC state on construction; subsequent
// stream calls reuse them and are zero-alloc. Hot-path
// consumers should construct one stream per goroutine and
// reuse it across [crypto.Stream.Reset] cycles.
package sha3
