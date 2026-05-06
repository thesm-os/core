// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

// Package sha512 provides HMAC-SHA-384 and HMAC-SHA-512
// [crypto.MAC] implementations backed by [crypto/hmac] and
// [crypto/sha512].
//
// [NewSHA384] and [NewSHA512] each take the keying material and
// return a [MAC] bound to it. The key is copied at construction;
// callers may zero or reuse the source buffer immediately
// afterwards.
//
// HMAC-SHA-384 is the CNSA 2.0 minimum-strength keyed-MAC for
// U.S. federal traffic. HMAC-SHA-512 is preferred on 64-bit
// hosts for long inputs where SHA-512's throughput advantage
// dominates the per-call HMAC overhead.
//
// [MAC.Sign] and [MAC.Verify] each allocate the underlying HMAC
// state once per call (the stdlib has no zero-allocation HMAC
// primitive). [MAC.NewStream] allocates the wrapper plus the
// underlying HMAC state on construction; subsequent stream
// calls reuse them and are zero-alloc. Hot-path consumers
// should construct one stream per goroutine and reuse it
// across [crypto.Stream.Reset] cycles.
package sha512
