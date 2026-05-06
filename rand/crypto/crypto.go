// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto

import (
	cryptorand "crypto/rand"
	"encoding/binary"
	"fmt"
	"io"

	"go.thesmos.sh/core/pool"
	"go.thesmos.sh/core/rand"
)

// uint64BufPool holds 8-byte scratch buffers for [Rand.Uint64].
// The interface boundary into [io.Reader.Read] forces the buffer
// to escape; routing through a package-level pool keeps the
// allocation amortised across calls (zero-alloc on the warm
// path) while preserving concurrency safety — every Uint64 call
// gets its own buffer for the duration of the read.
var uint64BufPool = pool.NewPool(func() *[8]byte { return new([8]byte) })

// Rand implements [rand.Rand] over a CSPRNG-grade [io.Reader]
// source. The default source is [crypto/rand.Reader] — the
// operating system's CSPRNG; tests and specialised callers (HSM
// backends, audit wrappers) can substitute their own source via
// [NewWithReader].
//
// The zero value is usable and behaves identically to [New].
//
// # Failure semantics
//
// On a healthy host with the default reader the CSPRNG never
// returns an error. If the underlying reader does return one,
// continuing without remediation would either silently produce
// predictable output or surface deep in caller code:
//
//   - [Rand.Read] returns the error wrapped with package context.
//   - [Rand.Uint64] panics with the wrapped error, because
//     returning a sentinel from a CSPRNG is the worst possible
//     failure mode for a security primitive — it looks like a
//     valid random value.
//
// # Allocation contract
//
// [Rand.Read] is zero-alloc per call: the buffer is caller-
// supplied and the underlying [crypto/rand.Read] is zero-alloc
// on supported platforms.
//
// [Rand.Uint64] is zero-alloc on the warm path: an 8-byte
// buffer is borrowed from a package-level [pool.Pool] for the
// duration of the read and returned afterwards. The interface
// boundary into [io.Reader.Read] would otherwise heap-allocate
// the buffer per call (stdlib's [crypto/rand.Read] /
// [io.ReadFull] take an [io.Reader], not a pointer to a
// fixed-size array). Cold-path callers may still observe an
// allocation when the pool is empty.
type Rand struct {
	src io.Reader
}

// Compile-time interface check.
var _ rand.Rand = Rand{}

// New returns a [Rand] backed by [crypto/rand.Reader]. Equivalent
// to the zero-value Rand; offered as a constructor for use sites
// that prefer one.
func New() Rand { return Rand{} }

// NewWithReader returns a [Rand] backed by src. Used to inject a
// custom CSPRNG implementation (for example a hardware key
// device, an audit-logging wrapper, or a fault-injecting reader
// in tests).
//
// src must be CSPRNG-grade for security-sensitive call sites; the
// type system cannot enforce this property.
func NewWithReader(src io.Reader) Rand { return Rand{src: src} }

// reader returns the configured source, defaulting to
// [crypto/rand.Reader] for the zero-value [Rand].
func (r Rand) reader() io.Reader {
	if r.src == nil {
		return cryptorand.Reader
	}
	return r.src
}

// Uint64 returns a uniformly distributed 64-bit value drawn from
// the configured reader. Panics with a wrapped error if the
// reader fails — see package-level "Failure semantics".
//
// Zero-alloc on the warm path; see package-level "Allocation
// contract".
func (r Rand) Uint64() uint64 {
	b := uint64BufPool.Get()
	_, err := io.ReadFull(r.reader(), b[:])
	if err != nil {
		// Don't return b to the pool on failure — the buffer
		// state may be partially-filled, and Uint64 is panicking
		// anyway, so pool hygiene is irrelevant.
		panic(fmt.Errorf("rand/crypto: entropy source unavailable: %w", err))
	}
	v := binary.LittleEndian.Uint64(b[:])
	uint64BufPool.Put(b)
	return v
}

// Read fills p with random bytes from the configured reader.
// Returns the number of bytes filled and any error from the
// source, wrapped with package context for caller diagnosis.
//
// On the default [crypto/rand.Reader], Read returns
// (len(p), nil) on supported operating systems.
func (r Rand) Read(p []byte) (int, error) {
	n, err := io.ReadFull(r.reader(), p)
	if err != nil {
		return n, fmt.Errorf("rand/crypto: read from source: %w", err)
	}
	return n, nil
}
