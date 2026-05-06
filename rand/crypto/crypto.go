// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto

import (
	cryptorand "crypto/rand"
	"encoding/binary"
	"fmt"
	"io"

	"go.thesmos.sh/core/rand"
)

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
// [Rand.Uint64] allocates a heap-local 8-byte buffer per call:
// supporting [NewWithReader] requires routing the buffer through
// an [io.Reader] interface boundary, and Go's escape analysis
// conservatively heap-allocates any slice that could flow through
// such a boundary even when the runtime path does not. Callers in
// alloc-sensitive code should use [Rand.Read] with a caller-owned
// buffer.
type Rand struct {
	src io.Reader
}

// Compile-time interface check.
var _ rand.Rand = Rand{}

// New returns a [Rand] backed by [crypto/rand.Reader]. Equivalent
// to the zero-value Rand; offered as a constructor for use sites
// that prefer one. Leaves the internal source field nil so that
// [Rand.Uint64] takes the allocation-free fast path through
// [crypto/rand.Read] rather than dispatching through an
// [io.Reader] interface boundary.
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
// Allocates a heap-local 8-byte buffer per call; see the
// package-level "Allocation contract".
func (r Rand) Uint64() uint64 {
	var b [8]byte
	_, err := io.ReadFull(r.reader(), b[:])
	if err != nil {
		panic(fmt.Errorf("rand/crypto: entropy source unavailable: %w", err))
	}
	return binary.LittleEndian.Uint64(b[:])
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
